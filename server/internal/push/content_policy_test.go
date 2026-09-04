package push

import (
	"context"
	"errors"
	"testing"
)

// fakeRecipientPolicy drops the ids it is told to and can fail the check.
type fakeRecipientPolicy struct {
	drop map[int64]bool
	err  error
	seen struct {
		mediaType string
		tmdbID    int
	}
}

func (f *fakeRecipientPolicy) AllowedRecipients(_ context.Context, userIDs []int64, mediaType string, tmdbID int) ([]int64, error) {
	f.seen.mediaType, f.seen.tmdbID = mediaType, tmdbID
	kept := make([]int64, 0, len(userIDs))
	for _, id := range userIDs {
		if !f.drop[id] {
			kept = append(kept, id)
		}
	}
	return kept, f.err
}

func TestNotifyNewMovieDropsBlockedKidsAccounts(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'kid', '', 'user')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)
	policy := &fakeRecipientPolicy{drop: map[int64]bool{2: true}}
	n.SetContentPolicy(policy)

	n.NotifyNewMovie("Deadpool", 293660, "")

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "1" {
		t.Fatalf("recipients = %v, want alice only", ids)
	}
	if policy.seen.mediaType != "movie" || policy.seen.tmdbID != 293660 {
		t.Fatalf("policy asked about %s %d", policy.seen.mediaType, policy.seen.tmdbID)
	}
}

func TestNotifyNewEpisodeKeepsAdultsWhenTheCheckFails(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (2, 'kid', '', 'user')")

	mgr, cap := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)
	// The service drops every child and reports why; the adults still hear.
	n.SetContentPolicy(&fakeRecipientPolicy{drop: map[int64]bool{2: true}, err: errors.New("ratings unreachable")})

	n.NotifyNewEpisode("Severance", 95396, "")

	body := cap.waitForNotification(t)
	ids := userIDsOf(t, body)
	if len(ids) != 1 || ids[0] != "1" {
		t.Fatalf("recipients = %v, want alice only", ids)
	}
}

func TestSetContentPolicyIsNilReceiverSafe(t *testing.T) {
	var n *Notifier
	n.SetContentPolicy(&fakeRecipientPolicy{})
}
