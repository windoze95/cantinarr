package contentpolicy

import (
	"context"
	"errors"
	"testing"
)

func TestStoreSetRefusesAdminsRequiresUserAndRoundTripsGenres(t *testing.T) {
	env := newTestEnv(t)
	admin := env.user(t, "admin", "admin")
	kid := env.user(t, "kid", "user")

	if err := env.svc.Store.Set(admin, usPolicy()); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("admin: %v, want ErrAdminAccount", err)
	}
	if err := env.svc.Store.Set(9999, usPolicy()); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown user: %v, want ErrUserNotFound", err)
	}

	p := usPolicy()
	p.BlockedMovieGenres = []int{53, 27, 27}
	if err := env.svc.Store.Set(kid, p); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := env.svc.Store.Get(kid)
	if err != nil || got == nil {
		t.Fatalf("Get: %v, %v", got, err)
	}
	if got.MaxMovieRating != "PG" || got.MaxTVRating != "TV-PG" || got.RatingRegion != "US" || !got.BlockUnrated {
		t.Fatalf("stored = %+v", got)
	}
	if len(got.BlockedMovieGenres) != 2 || got.BlockedMovieGenres[0] != 27 || got.BlockedMovieGenres[1] != 53 {
		t.Fatalf("movie genres = %v, want sorted and deduplicated", got.BlockedMovieGenres)
	}
	if len(got.BlockedTVGenres) != 1 || got.BlockedTVGenres[0] != 10768 {
		t.Fatalf("tv genres = %v", got.BlockedTVGenres)
	}

	// A second Set replaces, never duplicates.
	p.MaxMovieRating = "G"
	p.BlockedMovieGenres = nil
	if err := env.svc.Store.Set(kid, p); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	got, _ = env.svc.Store.Get(kid)
	if got.MaxMovieRating != "G" || len(got.BlockedMovieGenres) != 0 || got.BlockedMovieGenres == nil {
		t.Fatalf("replaced = %+v", got)
	}
	child, err := env.svc.Store.IsChild(kid)
	if err != nil || !child {
		t.Fatalf("IsChild = %v, %v", child, err)
	}
	if child, _ := env.svc.Store.IsChild(admin); child {
		t.Fatal("admin is not a child")
	}
	if p, err := env.svc.Store.Get(admin); err != nil || p != nil {
		t.Fatalf("admin policy = %v, %v, want nil", p, err)
	}
}

func TestStoreClearIsIdempotentAndUserDeleteCascades(t *testing.T) {
	env := newTestEnv(t)
	kid := env.user(t, "kid", "user")
	if err := env.svc.Store.Clear(kid); err != nil {
		t.Fatalf("clear without a row: %v", err)
	}
	if err := env.svc.Store.Set(kid, usPolicy()); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.Store.Clear(kid); err != nil {
		t.Fatal(err)
	}
	if child, _ := env.svc.Store.IsChild(kid); child {
		t.Fatal("cleared account still reads as a child")
	}

	other := env.user(t, "other", "user")
	if err := env.svc.Store.Set(other, usPolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Exec("DELETE FROM users WHERE id = ?", other); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM user_content_policies WHERE user_id = ?", other).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("deleting the user must cascade to the policy row")
	}
}

func TestPoliciesForAndAllowedRecipients(t *testing.T) {
	env := newTestEnv(t)
	adult := env.user(t, "adult", "user")
	kid := env.user(t, "kid", "user")
	teen := env.user(t, "teen", "user")
	if err := env.svc.Store.Set(kid, usPolicy()); err != nil {
		t.Fatal(err)
	}
	teenPolicy := usPolicy()
	teenPolicy.MaxMovieRating = "R"
	if err := env.svc.Store.Set(teen, teenPolicy); err != nil {
		t.Fatal(err)
	}

	policies, err := env.svc.Store.PoliciesFor([]int64{adult, kid, teen})
	if err != nil || len(policies) != 2 || policies[kid] == nil || policies[teen] == nil {
		t.Fatalf("PoliciesFor = %v, %v", policies, err)
	}
	if empty, err := env.svc.Store.PoliciesFor(nil); err != nil || len(empty) != 0 {
		t.Fatalf("PoliciesFor(nil) = %v, %v", empty, err)
	}

	env.tmdb.set("/movie/1/release_dates", movieReleaseDates(map[string][][2]string{"US": {{"R", "3"}}}))
	kept, err := env.svc.AllowedRecipients(context.Background(), []int64{adult, kid, teen}, MediaMovie, 1)
	if err != nil {
		t.Fatalf("AllowedRecipients: %v", err)
	}
	if len(kept) != 2 || kept[0] != adult || kept[1] != teen {
		t.Fatalf("kept = %v, want adult and teen", kept)
	}

	// No identity: every child is dropped, the adult stays.
	kept, err = env.svc.AllowedRecipients(context.Background(), []int64{adult, kid, teen}, MediaMovie, 0)
	if err != nil || len(kept) != 1 || kept[0] != adult {
		t.Fatalf("no id: kept = %v, %v", kept, err)
	}

	// A failed lookup drops the children and reports the error.
	env.tmdb.fail("/movie/2/release_dates", errors.New("down"))
	env.tmdb.fail("/movie/2/release_dates", errors.New("down"))
	kept, err = env.svc.AllowedRecipients(context.Background(), []int64{adult, kid}, MediaMovie, 2)
	if err == nil || len(kept) != 1 || kept[0] != adult {
		t.Fatalf("lookup failure: kept = %v, err = %v", kept, err)
	}

	// No children among the recipients: nothing is looked up.
	before := env.tmdb.totalHits()
	kept, err = env.svc.AllowedRecipients(context.Background(), []int64{adult}, MediaMovie, 3)
	if err != nil || len(kept) != 1 || env.tmdb.totalHits() != before {
		t.Fatalf("adults only: kept = %v, err = %v, hits %d -> %d", kept, err, before, env.tmdb.totalHits())
	}
}

func TestPolicyForSkipsAdminsAndLimits(t *testing.T) {
	env := newTestEnv(t)
	kid := env.user(t, "kid", "user")
	if err := env.svc.Store.Set(kid, usPolicy()); err != nil {
		t.Fatal(err)
	}
	if p, err := env.svc.PolicyFor(kid, "admin"); err != nil || p != nil {
		t.Fatalf("admin role never reads a policy: %v, %v", p, err)
	}
	p, err := env.svc.PolicyFor(kid, "user")
	if err != nil || p == nil {
		t.Fatalf("PolicyFor = %v, %v", p, err)
	}
	limits := p.Limits()
	if limits.MaxMovieRating != "PG" || limits.MaxTVRating != "TV-PG" || limits.RatingRegion != "US" {
		t.Fatalf("Limits = %+v", limits)
	}
	if (*Policy)(nil).Limits() != nil {
		t.Fatal("nil policy has nil limits")
	}
}
