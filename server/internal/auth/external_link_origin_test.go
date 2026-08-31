package auth

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// postConnectToken drives HandleCreateConnectToken as the admin and returns
// the decoded response plus the raw status code.
func postConnectToken(t *testing.T, h *Handler, body string) (CreateConnectTokenResponse, int) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/admin/connect-token", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{UserID: 1, Username: "admin", Role: "admin"}))
	rec := httptest.NewRecorder()
	h.HandleCreateConnectToken(rec, req)
	var resp CreateConnectTokenResponse
	if rec.Code == 201 {
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return resp, rec.Code
}

// serverParam extracts the server= query parameter from a connect link.
func serverParam(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link %q: %v", link, err)
	}
	return u.Query().Get("server")
}

// TestConnectLinkFallsBackToTheAppAddress pins the unconfigured behavior: with
// no external address set, the link embeds whatever address the generating
// admin's app sent — and says so, so the app can hint that a LAN address won't
// reach an invitee on another network.
func TestConnectLinkFallsBackToTheAppAddress(t *testing.T) {
	h := NewHandler(setupTestService(t))

	resp, code := postConnectToken(t, h, `{"name":"kid","server_url":"http://192.168.1.10:8585"}`)
	if code != 201 {
		t.Fatalf("status = %d, want 201", code)
	}
	if got := serverParam(t, resp.Link); got != "http://192.168.1.10:8585" {
		t.Errorf("server param = %q, want the app-sent address", got)
	}
	if resp.OriginSource != "app" {
		t.Errorf("origin_source = %q, want %q", resp.OriginSource, "app")
	}
}

// TestConnectLinkPrefersTheExternalAddress is the tester-reported gap: an
// admin who configured where the server is reachable from outside must get
// links built on that address, no matter what their own app is connected
// with. The client-sent URL loses even when present.
func TestConnectLinkPrefersTheExternalAddress(t *testing.T) {
	h := NewHandler(setupTestService(t))
	h.SetExternalURLSource(func() string { return "https://cantina.example.com" })

	resp, code := postConnectToken(t, h, `{"name":"kid","server_url":"http://192.168.1.10:8585"}`)
	if code != 201 {
		t.Fatalf("status = %d, want 201", code)
	}
	if got := serverParam(t, resp.Link); got != "https://cantina.example.com" {
		t.Errorf("server param = %q, want the external address", got)
	}
	if resp.OriginSource != "external_address" {
		t.Errorf("origin_source = %q, want %q", resp.OriginSource, "external_address")
	}

	// A configured external address also carries a client that sent no URL at
	// all (future clients need not send one).
	resp, code = postConnectToken(t, h, `{"name":"kid2"}`)
	if code != 201 {
		t.Fatalf("status without server_url = %d, want 201", code)
	}
	if got := serverParam(t, resp.Link); got != "https://cantina.example.com" {
		t.Errorf("server param = %q, want the external address", got)
	}
}

// TestConnectLinkStillRequiresSomeAddress: with nothing configured and nothing
// sent there is no origin to build a usable link from, so the request fails
// loudly instead of minting a link nobody can redeem.
func TestConnectLinkStillRequiresSomeAddress(t *testing.T) {
	h := NewHandler(setupTestService(t))
	if _, code := postConnectToken(t, h, `{"name":"kid"}`); code != 400 {
		t.Errorf("status = %d, want 400", code)
	}
}

// TestIssuingALinkSupersedesThePreviousOne is the promise the invite dialog
// makes in so many words. It matters most in the case reissuing exists for: a
// link that went to the wrong person must die when the admin issues a new one,
// not stay redeemable for the rest of its seven days.
func TestIssuingALinkSupersedesThePreviousOne(t *testing.T) {
	svc := setupTestService(t)

	tokenOf := func(resp *CreateConnectTokenResponse) string {
		t.Helper()
		u, err := url.Parse(resp.Link)
		if err != nil {
			t.Fatalf("parse link: %v", err)
		}
		return u.Query().Get("token")
	}

	first, err := svc.CreateConnectToken(1, "kid", "http://example.com")
	if err != nil {
		t.Fatalf("create first token: %v", err)
	}
	second, err := svc.CreateConnectToken(1, "kid", "http://example.com")
	if err != nil {
		t.Fatalf("create second token: %v", err)
	}

	if _, err := svc.RedeemConnectToken(tokenOf(first), "Leaked Device", "leaked"); err == nil {
		t.Fatal("the superseded link still redeemed, want it dead")
	}
	if _, err := svc.RedeemConnectToken(tokenOf(second), "Kid Device", "kid-device"); err != nil {
		t.Fatalf("redeem current token: %v", err)
	}

	// A redeemed row is history, not an outstanding invite: issuing a link for
	// a second device must not disturb the session the first one created.
	third, err := svc.CreateConnectToken(1, "kid", "http://example.com")
	if err != nil {
		t.Fatalf("create third token: %v", err)
	}
	if _, err := svc.RedeemConnectToken(tokenOf(third), "Kid Tablet", "kid-tablet"); err != nil {
		t.Fatalf("redeem third token: %v", err)
	}
}

// TestPasskeySetupLinkPrefersTheExternalAddress: the passkey setup link is
// opened on the user's own device, so it has the same reachability problem as
// invite links and follows the same preference order — external address first,
// request-derived origin as the fallback.
func TestPasskeySetupLinkPrefersTheExternalAddress(t *testing.T) {
	svc := setupTestService(t)
	h := NewHandler(svc)

	// A passkey setup token requires an active device; mint one the way a real
	// account gets its first (connect link redemption).
	connect, err := svc.CreateConnectToken(1, "kid", "http://example.com")
	if err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	connectURL, err := url.Parse(connect.Link)
	if err != nil {
		t.Fatalf("parse connect link: %v", err)
	}
	login, err := svc.RedeemConnectToken(connectURL.Query().Get("token"), "Kid Device", "kid-device")
	if err != nil {
		t.Fatalf("redeem connect token: %v", err)
	}

	createLink := func() string {
		req := httptest.NewRequest("POST", "http://192.168.1.10:8585/api/auth/passkeys/setup-link", nil)
		req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{
			UserID: login.User.ID, Username: login.User.Username, Role: login.User.Role, DeviceID: login.DeviceID,
		}))
		rec := httptest.NewRecorder()
		h.CreatePasskeySetupLink(rec, req)
		if rec.Code != 201 {
			t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			Link string `json:"link"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return resp.Link
	}

	if link := createLink(); !strings.HasPrefix(link, "http://192.168.1.10:8585/passkeys/create?") {
		t.Errorf("link = %q, want the request-derived origin when no external address is set", link)
	}

	h.SetExternalURLSource(func() string { return "https://cantina.example.com" })
	if link := createLink(); !strings.HasPrefix(link, "https://cantina.example.com/passkeys/create?") {
		t.Errorf("link = %q, want the external address once configured", link)
	}
}
