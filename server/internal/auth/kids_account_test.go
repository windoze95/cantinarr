package auth

import (
	"errors"
	"testing"
)

// insertKidsPolicy writes a user_content_policies row the way the
// contentpolicy store does, so the auth reads can be pinned without
// importing that package.
func insertKidsPolicy(t *testing.T, svc *Service, userID int64) {
	t.Helper()
	if _, err := svc.db.Exec(`INSERT INTO user_content_policies (user_id, max_movie_rating, max_tv_rating, rating_region) VALUES (?, 'PG', 'TV-PG', 'US')`, userID); err != nil {
		t.Fatalf("insert policy: %v", err)
	}
}

func inviteUser(t *testing.T, svc *Service, name string) int64 {
	t.Helper()
	if _, err := svc.CreateConnectToken(1, name, "http://example.com"); err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	var id int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", name).Scan(&id); err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return id
}

func TestUsersCarryChildFlagAndLimits(t *testing.T) {
	svc := setupTestService(t)
	kid := inviteUser(t, svc, "kid")
	plain := inviteUser(t, svc, "plain")
	insertKidsPolicy(t, svc, kid)

	user, err := svc.GetUser(kid)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !user.Child || user.ContentLimits == nil {
		t.Fatalf("kid = child %v, limits %v", user.Child, user.ContentLimits)
	}
	if user.ContentLimits.MaxMovieRating != "PG" || user.ContentLimits.MaxTVRating != "TV-PG" || user.ContentLimits.RatingRegion != "US" {
		t.Fatalf("limits = %+v", user.ContentLimits)
	}
	other, err := svc.GetUser(plain)
	if err != nil {
		t.Fatal(err)
	}
	if other.Child || other.ContentLimits != nil {
		t.Fatalf("plain user reads as a child: %+v", other)
	}

	// The per-request rehydration path reads the same columns.
	byName, err := svc.getUserByUsername("kid")
	if err != nil || !byName.Child {
		t.Fatalf("getUserByUsername: child %v, err %v", byName != nil && byName.Child, err)
	}

	users, err := svc.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, u := range users {
		seen[u.ID] = u.Child
	}
	if !seen[kid] || seen[plain] {
		t.Fatalf("ListUsers child flags = %v", seen)
	}
	summary, err := svc.userSummaryByID(kid)
	if err != nil || !summary.Child {
		t.Fatalf("userSummaryByID: %+v, %v", summary, err)
	}
}

func TestUpdateUserRoleRefusesPromotingChild(t *testing.T) {
	svc := setupTestService(t)
	kid := inviteUser(t, svc, "kid")
	insertKidsPolicy(t, svc, kid)

	if _, err := svc.UpdateUserRole(kid, RoleAdmin); !errors.Is(err, ErrChildCannotBeAdmin) {
		t.Fatalf("promote child: %v, want ErrChildCannotBeAdmin", err)
	}
	var role string
	if err := svc.db.QueryRow("SELECT role FROM users WHERE id = ?", kid).Scan(&role); err != nil || role != RoleUser {
		t.Fatalf("role after refused promotion = %q, %v", role, err)
	}
	// Setting the same role is not a promotion and still works.
	if _, err := svc.UpdateUserRole(kid, RoleUser); err != nil {
		t.Fatalf("keep role: %v", err)
	}
	// With the policy gone the promotion goes through.
	if _, err := svc.db.Exec("DELETE FROM user_content_policies WHERE user_id = ?", kid); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateUserRole(kid, RoleAdmin); err != nil {
		t.Fatalf("promote after clearing: %v", err)
	}
}
