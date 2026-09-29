package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscordPreferencesAreSelfScopedAndDoNotExposeDestination(t *testing.T) {
	h := newRBACRouterHarness(t, false)
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.router.ServeHTTP(w, r)
		return w
	}
	path := "/api/auth/discord-notifications"
	w := call("PUT", path, h.requesterToken, `{"enabled":true,"discord_ids":["123456789012345678","234567890123456789"],"events":{"request_available":true}}`)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var prefs map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &prefs); err != nil {
		t.Fatal(err)
	}
	if len(prefs["discord_ids"].([]any)) != 2 || prefs["blocked_reason"] == nil {
		t.Fatal(prefs)
	}
	for _, forbidden := range []string{"webhook_url", "role_id", "recent"} {
		if _, ok := prefs[forbidden]; ok {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	w = call("GET", path, h.adminToken, "")
	if strings.Contains(w.Body.String(), "123456789012345678") {
		t.Fatal("other account read personal IDs")
	}
	w = call("PUT", path, h.requesterToken, `{"enabled":false,"discord_ids":[],"events":{"request_pending":true}}`)
	if w.Code != 400 {
		t.Fatalf("admin category: %d", w.Code)
	}
	w = call("PUT", path, h.requesterToken, `{"user_id":1,"enabled":false}`)
	if w.Code != 400 {
		t.Fatalf("foreign account: %d", w.Code)
	}
	w = call("POST", path+"/test", h.requesterToken, "")
	if w.Code != 400 {
		t.Fatalf("muted test: %d", w.Code)
	}
	w = call("GET", path, "", "")
	if w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
}

func TestDiscordAdminConfigurationAndRequesterCreation(t *testing.T) {
	h := newRBACRouterHarness(t, false)
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.router.ServeHTTP(w, r)
		return w
	}
	for _, method := range []string{"GET", "PUT", "DELETE", "POST"} {
		path := "/api/admin/discord-notifications"
		if method == "POST" {
			path += "/test"
		}
		for _, token := range []string{"", h.requesterToken} {
			w := call(method, path, token, `{}`)
			if w.Code != 401 && w.Code != 403 {
				t.Fatalf("unauthorized %s: %d %s", method, w.Code, w.Body.String())
			}
		}
	}
	w := call("PUT", "/api/admin/discord-notifications", h.adminToken, `{"enabled":true,"include_auto_approved":true,"webhook_url":"https://discord.com/api/webhooks/123/secret_token"}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret_token") {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	// Pending requests require no live arr or push gateway. Use the API to
	// verify the same production service hook creates exactly one receipt.
	h.database.Exec(`INSERT INTO settings(key,value) VALUES('request_settings','{"require_approval":true}') ON CONFLICT(key) DO UPDATE SET value=excluded.value`)
	for i := 0; i < 2; i++ {
		w = call("POST", "/api/requests", h.requesterToken, `{"media_type":"movie","tmdb_id":550,"title":"A movie"}`)
		if w.Code != 200 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
	}
	var count int
	h.database.QueryRow(`SELECT COUNT(*) FROM discord_notifications`).Scan(&count)
	if count != 1 {
		t.Fatalf("queued %d alerts", count)
	}
	w = call("GET", "/api/admin/discord-notifications", h.adminToken, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"pending"`) {
		t.Fatalf("delivery status: %d %s", w.Code, w.Body.String())
	}
	// Exercise the actual request decision path, not a synthesized notifier
	// payload. Legacy movie and book decisions must carry their durable ID.
	w = call("PUT", "/api/admin/discord-notifications", h.adminToken, `{"enabled":true,"events":{"request_pending":true,"request_denied":true}}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var id int64
	if err := h.database.QueryRow(`SELECT id FROM request_log ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	w = call("POST", fmt.Sprintf("/api/admin/requests/%d/deny", id), h.adminToken, `{"reason":"Not this time"}`)
	if w.Code != 200 {
		t.Fatalf("deny: %d %s", w.Code, w.Body.String())
	}
	if err := h.database.QueryRow(`SELECT COUNT(*) FROM discord_notifications WHERE json_extract(payload,'$.kind')='request_denied'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("denial notification: %d %v", count, err)
	}
}
