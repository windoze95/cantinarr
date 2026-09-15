package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerPushPolicyAuthorizationAndPersonalIsolation(t *testing.T) {
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
	for _, method := range []string{"GET", "PUT"} {
		for _, token := range []string{"", h.requesterToken} {
			w := call(method, "/api/admin/push-notifications", token, `{"enabled":false}`)
			if w.Code != 401 && w.Code != 403 {
				t.Fatalf("unauthorized policy %d %s", w.Code, w.Body.String())
			}
		}
	}
	w := call("PUT", "/api/admin/push-notifications", h.adminToken, `{"enabled":false,"categories":{"new_movie":false}}`)
	if w.Code != 200 {
		t.Fatalf("admin policy: %d %s", w.Code, w.Body.String())
	}
	w = call("PUT", "/api/notifications/preferences", h.requesterToken, `{"push_enabled":false,"server_policy":{"enabled":true}}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"server_policy":{"enabled":false`) {
		t.Fatalf("personal scope: %d %s", w.Code, w.Body.String())
	}
	w = call("PUT", "/api/admin/push-notifications", h.adminToken, `{"categories":{"unknown":true}}`)
	if w.Code != 400 {
		t.Fatalf("unknown category: %d", w.Code)
	}
}
