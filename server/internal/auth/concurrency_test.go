package auth

import (
	"net/url"
	"sync"
	"testing"
)

// raceN runs fn from n goroutines released together and returns how many
// returned nil. A single-use claim must let exactly one caller through no
// matter how their statements interleave.
func raceN(n int, fn func() error) (successes int) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = fn()
		}(i)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	return successes
}

// SEC/AUTH: one authorization code yields at most one token set even when the
// same client replays it concurrently (regression: select-then-delete let two
// racers both consume a single code).
func TestOAuthAuthorizationCodeRedemptionIsSingleUseUnderReplay(t *testing.T) {
	svc := setupTestService(t)
	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	redirectURI := "http://127.0.0.1:12345/callback"
	resource := "http://example.com/mcp"
	verifier := "replay-race-verifier-value"
	client, err := svc.RegisterOAuthClient("Replay Agent", []string{redirectURI})
	if err != nil {
		t.Fatalf("register oauth client: %v", err)
	}
	code, err := svc.CreateOAuthAuthorizationCode(client, loginResp.User.ID, redirectURI, pkceChallenge(verifier), resource, defaultOAuthScope)
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}

	successes := raceN(8, func() error {
		_, err := svc.ExchangeOAuthAuthorizationCode(client.ClientID, code, redirectURI, verifier, resource)
		return err
	})
	if successes != 1 {
		t.Fatalf("concurrent exchanges succeeded %d times, want exactly 1", successes)
	}
}

// SEC/AUTH: a wrong-client exchange attempt must not delete a victim's still
// valid authorization code (regression: the code was consumed before the
// client_id was checked, so an attacker-registered client could burn it).
func TestOAuthAuthorizationCodeSurvivesWrongClientAttempt(t *testing.T) {
	svc := setupTestService(t)
	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	redirectURI := "http://127.0.0.1:12345/callback"
	resource := "http://example.com/mcp"
	verifier := "wrong-client-verifier-value"
	victim, err := svc.RegisterOAuthClient("Victim Agent", []string{redirectURI})
	if err != nil {
		t.Fatalf("register victim client: %v", err)
	}
	attacker, err := svc.RegisterOAuthClient("Attacker Agent", []string{redirectURI})
	if err != nil {
		t.Fatalf("register attacker client: %v", err)
	}
	code, err := svc.CreateOAuthAuthorizationCode(victim, loginResp.User.ID, redirectURI, pkceChallenge(verifier), resource, defaultOAuthScope)
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}

	if _, err := svc.ExchangeOAuthAuthorizationCode(attacker.ClientID, code, redirectURI, verifier, resource); err == nil {
		t.Fatal("attacker client exchanged the victim's code")
	}
	// The legitimate client can still redeem its untouched code.
	if _, err := svc.ExchangeOAuthAuthorizationCode(victim.ClientID, code, redirectURI, verifier, resource); err != nil {
		t.Fatalf("victim exchange after wrong-client attempt: %v", err)
	}
}

// SEC/AUTH: rotating a refresh token concurrently mints at most one successor
// (regression: blind delete-then-create let two racers both mint successors and
// defeated refresh-token reuse detection).
func TestOAuthRefreshRotationIsSingleUseUnderReplay(t *testing.T) {
	svc := setupTestService(t)
	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	redirectURI := "http://127.0.0.1:12345/callback"
	resource := "http://example.com/mcp"
	verifier := "refresh-race-verifier-value"
	client, err := svc.RegisterOAuthClient("Refresh Agent", []string{redirectURI})
	if err != nil {
		t.Fatalf("register oauth client: %v", err)
	}
	code, err := svc.CreateOAuthAuthorizationCode(client, loginResp.User.ID, redirectURI, pkceChallenge(verifier), resource, defaultOAuthScope)
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	tokenResp, err := svc.ExchangeOAuthAuthorizationCode(client.ClientID, code, redirectURI, verifier, resource)
	if err != nil {
		t.Fatalf("exchange code: %v", err)
	}

	successes := raceN(8, func() error {
		_, err := svc.RefreshOAuthToken(client.ClientID, tokenResp.RefreshToken, resource)
		return err
	})
	if successes != 1 {
		t.Fatalf("concurrent refreshes succeeded %d times, want exactly 1", successes)
	}
}

// SEC/AUTH: a leaked single-use connect link redeemed concurrently mints at most
// one device session (regression: select-then-update without an atomic guard let
// two racers both redeem one invite).
func TestRedeemConnectTokenIsSingleUseUnderReplay(t *testing.T) {
	svc := setupTestService(t)
	admin, err := svc.getUserByUsername("admin")
	if err != nil {
		t.Fatalf("load admin: %v", err)
	}
	created, err := svc.CreateConnectToken(admin.ID, "race-invitee", "https://cantinarr.example.com")
	if err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	token := connectTokenFromLink(t, created.Link)

	successes := raceN(8, func() error {
		_, err := svc.RedeemConnectToken(token, "Race Device", "")
		return err
	})
	if successes != 1 {
		t.Fatalf("concurrent redemptions succeeded %d times, want exactly 1", successes)
	}
}

// SEC/AUTH: concurrent first-time invites for the same new username converge on
// one user row instead of racing the username UNIQUE constraint and failing one
// caller (regression: lookup-then-insert without ON CONFLICT).
func TestCreateConnectTokenConvergesConcurrentFirstInvites(t *testing.T) {
	svc := setupTestService(t)
	admin, err := svc.getUserByUsername("admin")
	if err != nil {
		t.Fatalf("load admin: %v", err)
	}
	const username = "brand-new-invitee"

	successes := raceN(8, func() error {
		_, err := svc.CreateConnectToken(admin.ID, username, "https://cantinarr.example.com")
		return err
	})
	if successes != 8 {
		t.Fatalf("concurrent first invites succeeded %d times, want all 8 to converge", successes)
	}

	var count int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", username).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("username %q has %d user rows, want exactly 1", username, count)
	}
}

func connectTokenFromLink(t *testing.T, link string) string {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse connect link %q: %v", link, err)
	}
	token := parsed.Query().Get("token")
	if token == "" {
		t.Fatalf("connect link %q has no token", link)
	}
	return token
}
