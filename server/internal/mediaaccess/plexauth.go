package mediaaccess

import (
	"context"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/plex"
)

func (s *Service) SetPlexAuth(a *auth.Service) { s.plexAuth = a; a.SetPlexDirectory(s) }

// PlexAccounts is a fresh, read-only directory of configured servers. The
// owner's ID comes from user verification and ownership from resources; a
// cached owner email, pending invitation, or friendship proves no access.
func (s *Service) PlexAccounts(ctx context.Context) ([]auth.PlexServerAccounts, error) {
	instances, err := s.store.ListAll()
	if err != nil {
		return nil, auth.ErrPlexUnavailable
	}
	out := []auth.PlexServerAccounts{}
	for _, listed := range instances {
		if listed.ServiceType != "plex" {
			continue
		}
		server := auth.PlexServerAccounts{InstanceID: listed.ID, Name: listed.Name}
		read := func() error {
			revision, err := auth.PlexInstanceRevision(s.db, listed.ID)
			if err != nil {
				return err
			}
			inst, err := s.store.Get(listed.ID)
			if err != nil || inst == nil || inst.ServiceType != "plex" || inst.MediaServerConfigInvalid || inst.MediaServerConfig.MachineIdentifier == "" {
				return auth.ErrPlexUnavailable
			}
			check, err := auth.PlexInstanceRevision(s.db, listed.ID)
			if err != nil || check != revision {
				return auth.ErrPlexUnavailable
			}
			c := plex.NewClientAt(inst.URL)
			cfg := inst.MediaServerConfig
			owner, err := c.GetUser(ctx, cfg.ClientID, inst.APIKey)
			if err != nil || owner.ID <= 0 {
				return auth.ErrPlexUnavailable
			}
			owned, err := c.ListServers(ctx, cfg.ClientID, inst.APIKey)
			if err != nil {
				return auth.ErrPlexUnavailable
			}
			owns := false
			for _, r := range owned {
				if r.ClientIdentifier == cfg.MachineIdentifier {
					owns = true
					break
				}
			}
			if !owns {
				return auth.ErrPlexUnavailable
			}
			shares, err := c.ListShares(ctx, cfg.ClientID, inst.APIKey, cfg.MachineIdentifier)
			if err != nil {
				return auth.ErrPlexUnavailable
			}
			server.Revision = revision
			server.Accounts = append(server.Accounts, auth.PlexSharedAccount{Account: *owner, Accepted: true})
			for _, share := range shares {
				if share.UserID > 0 {
					server.Accounts = append(server.Accounts, auth.PlexSharedAccount{Account: plex.Account{ID: share.UserID, Email: share.Email, Username: share.Username}, Accepted: share.Accepted})
				}
			}
			return nil
		}
		if err := read(); err != nil {
			server.Error = "Plex account and share data could not be verified."
			server.Accounts = nil
		}
		out = append(out, server)
	}
	return out, nil
}
