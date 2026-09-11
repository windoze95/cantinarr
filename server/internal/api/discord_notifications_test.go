package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

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
	w := call("PUT", "/api/admin/discord-notifications", h.adminToken, `{"enabled":true,"webhook_url":"https://discord.com/api/webhooks/123/secret_token"}`)
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
}
