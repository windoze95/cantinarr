package hardcover

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOAuthProtocolUsesPublicClientAndCatalogScope(t *testing.T) {
	now := time.Now()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Method != "POST" || r.Form.Get("client_id") != PublicClientID || r.Form.Get("client_secret") != "" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected client authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/device" {
			if r.Form.Get("scope") != CatalogScope {
				t.Error("excess OAuth scope")
			}
			_, _ = w.Write([]byte(`{"device_code":"server-only","user_code":"ABCD-EFGH","verification_uri":"https://hardcover.app/link","expires_in":600,"interval":12}`))
			return
		}
		switch calls {
		case 2:
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.Form.Get("device_code") != "server-only" {
				t.Error("invalid device exchange")
			}
		case 3:
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh" {
				t.Error("invalid refresh exchange")
			}
		}
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","expires_in":3600,"scope":"read:catalog:data"}`))
	}))
	defer srv.Close()
	c := NewOAuthClient()
	c.DeviceURL = srv.URL + "/device"
	c.TokenURL = srv.URL + "/token"
	c.Now = func() time.Time { return now }
	code, err := c.Begin(context.Background())
	if err != nil || code.Interval != 12 {
		t.Fatalf("begin %v %v", code, err)
	}
	pair, err := c.Poll(context.Background(), code.DeviceCode)
	if err != nil || !pair.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("poll %v", err)
	}
	if _, err = c.Refresh(context.Background(), pair.RefreshToken); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthErrorsArePreciseAndRedacted(t *testing.T) {
	for _, tc := range []struct {
		code int
		body string
		want error
	}{
		{400, `{"error":"authorization_pending"}`, ErrPending}, {400, `{"error":"slow_down"}`, ErrSlowDown},
		{400, `{"error":"access_denied"}`, ErrDenied}, {400, `{"error":"expired_token"}`, ErrExpired},
		{400, `{"error":"invalid_grant","error_description":"secret"}`, ErrInvalidGrant},
		{500, `{"error":"invalid_grant"}`, ErrProvider}, {429, `secret`, ErrSlowDown}, {401, `secret`, ErrProvider},
		{200, `{"access_token":"secret"}`, ErrProvider}, {200, `not json secret`, ErrProvider},
	} {
		t.Run(tc.body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); _, _ = w.Write([]byte(tc.body)) }))
			defer srv.Close()
			c := NewOAuthClient()
			c.TokenURL = srv.URL
			_, err := c.Refresh(context.Background(), "refresh-secret")
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe/wrong error: %v", err)
			}
		})
	}
}

func TestDeviceVerificationURLValidationAndNoRedirects(t *testing.T) {
	for _, url := range []string{"http://hardcover.app/link", "https://hardcover.app.evil.test/link", "https://hardcover.app:444/link", "https://user@hardcover.app/link", "https://hardcover.app/link#secret", "javascript:alert(1)"} {
		if TrustedVerificationURI(url) {
			t.Fatalf("trusted %s", url)
		}
	}
	if !TrustedVerificationURI("https://hardcover.app/link") {
		t.Fatal("official verification URL refused")
	}
	touched := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { touched = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	c := NewOAuthClient()
	c.TokenURL = redirect.URL
	if _, err := c.Refresh(context.Background(), "secret"); err == nil || touched {
		t.Fatal("token followed a redirect")
	}
}

func TestCatalogVerificationDistinguishesCredentialsScopesAndProviderFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"valid empty catalog", 200, `{"data":{"books":[],"books_trending":{"ids":[]}}}`, nil},
		{"invalid", 401, `{}`, ErrUnauthorized}, {"forbidden", 403, `{}`, ErrInsufficientScope},
		{"jwt", 200, `{"errors":[{"message":"JWTExpired"}]}`, ErrUnauthorized},
		{"scope", 200, `{"errors":[{"message":"secret","extensions":{"code":"insufficient_scope"}}]}`, ErrInsufficientScope},
		{"provider", 200, `{"errors":[{"message":"secret"}]}`, ErrProvider},
		{"missing data", 200, `{"data":{}}`, ErrProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				_ = json.NewDecoder(r.Body).Decode(&payload)
				if strings.Contains(payload["query"].(string), " me ") {
					t.Error("verification requires profile permission")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			err := NewClientForURL(srv.URL).VerifyCatalog(context.Background(), "secret")
			if tc.want == nil {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe/missing error %v", err)
			}
			if tc.want != ErrProvider && !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}
