package mediaaccess

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// Account operations share a user read lock; deleting a user holds the write
// lock from the snapshot through commit/abort. This prevents a stale deletion
// snapshot from disabling an account after its management was switched off.
func (s *Service) userLock(userID int64) *sync.RWMutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.userLocks[userID]
	if l == nil {
		l = &sync.RWMutex{}
		s.userLocks[userID] = l
	}
	return l
}

func (s *Service) autoLinkSuppressed(userID int64, instanceID string) (bool, error) {
	var suppressed bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM user_media_server_unlinks WHERE user_id=? AND instance_id=?)", userID, instanceID).Scan(&suppressed)
	return suppressed, err
}

func (s *Service) markAccessPending(userID int64, instanceID string) error {
	_, err := s.db.Exec("UPDATE user_media_server_accounts SET access_sync_pending=1 WHERE user_id=? AND instance_id=? AND manage_access=1", userID, instanceID)
	return err
}

func (s *Service) accountSnapshot(userID int64, instanceID string) (Account, error) {
	accounts, err := s.listAccounts()
	if err != nil {
		return Account{}, err
	}
	for _, account := range accounts {
		if account.UserID == userID && account.InstanceID == instanceID {
			return account, nil
		}
	}
	return Account{}, ErrNoAccount
}

// SetManagement adopts access control explicitly, or stops it without any
// provider traffic. A verified adoption persists its intent before attempting
// the write, so an outage between the read and write is retried after restart.
func (s *Service) SetManagement(ctx context.Context, userID int64, instanceID string, manage bool) (Account, error) {
	unlock := s.lock(userID, instanceID)
	defer unlock()
	row, err := s.getAccount(userID, instanceID)
	if err != nil {
		return Account{}, err
	}
	if row == nil {
		return Account{}, ErrNoAccount
	}
	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()
	if manage {
		inst, err := s.mediaServerInstance(instanceID)
		if err != nil {
			return Account{}, err
		}
		provider, err := s.providers(inst)
		if err != nil {
			return Account{}, ErrNotMediaServer
		}
		remote, err := provider.GetUser(ctx, row.RemoteUserID)
		if errors.Is(err, mediaserver.ErrUserNotFound) {
			return Account{}, ErrRemoteUserNotFound
		}
		if err != nil {
			return Account{}, fmt.Errorf("%w: %v", ErrUpstream, err)
		}
		if remote.IsAdministrator {
			return Account{}, ErrProtectedAccount
		}
	}
	if _, err := s.db.Exec("UPDATE user_media_server_accounts SET manage_access=?, access_sync_pending=? WHERE user_id=? AND instance_id=?", manage, manage, userID, instanceID); err != nil {
		return Account{}, err
	}
	if !manage {
		if err := s.cancelLibrarySync(userID, instanceID); err != nil {
			return Account{}, err
		}
	}
	if manage {
		s.reconcileAccountLocked(ctx, userID, instanceID)
	}
	return s.accountSnapshot(userID, instanceID)
}

// Enrich admin rows with one bounded live read per server. Local grants and
// management intent remain separate from observed remote state; failed reads
// never turn a recorded disable stamp into a claim about current remote access.
func (s *Service) verifyAdminAccounts(ctx context.Context, accounts []Account) {
	groups := map[string][]int{}
	for i, account := range accounts {
		groups[account.InstanceID] = append(groups[account.InstanceID], i)
	}
	var wg sync.WaitGroup
	for instanceID, indices := range groups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
			defer cancel()
			users, err := s.RemoteUsers(ctx, instanceID)
			if err != nil {
				return
			}
			byID := make(map[string]mediaserver.RemoteUser, len(users))
			for _, user := range users {
				byID[user.ID] = user
			}
			for _, i := range indices {
				if user, ok := byID[accounts[i].RemoteUserID]; ok {
					accounts[i].Verified = true
					accounts[i].Disabled = user.IsDisabled
					accounts[i].Administrator = user.IsAdministrator
					if user.IsAdministrator {
						accounts[i].ManageAccess = false
						accounts[i].AccessSyncPending = false
					}
				}
			}
		}()
	}
	wg.Wait()
}
