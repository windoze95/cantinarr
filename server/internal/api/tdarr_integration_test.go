package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

func TestTdarrRoutesAreAdminOnlyReadOnlyAndHiddenFromRequesters(t *testing.T) {
	h := newRBACRouterHarness(t, false)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/get-nodes" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	store := instance.NewStore(h.database, h.cipher)
	inst := &instance.Instance{ID: "tdarr-private", Name: "Private progress", ServiceType: "tdarr", URL: upstream.URL, APIKey: "TEST_KEY"}
	if err := store.Create(inst); err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{"activity", "libraries", "stats?library_id=films"} {
		for _, tc := range []struct {
			token string
			want  int
		}{{"", 401}, {h.requesterToken, 403}} {
			response := serveRBACRequest(h.router, "GET", "/api/tdarr/tdarr-private/"+view, tc.token)
			if response.Code != tc.want {
				t.Fatalf("view %s returned %d, want %d", view, response.Code, tc.want)
			}
		}
	}
	// Even admins cannot tunnel reads of raw configuration or arbitrary writes.
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		response := serveRBACRequestWithBody(h.router, method, "/api/instances/tdarr-private/api/v2/cruddb", h.adminToken, `{}`)
		if response.Code != http.StatusForbidden {
			t.Fatalf("proxy %s returned %d", method, response.Code)
		}
	}
	response := serveRBACRequestWithBody(h.router, "POST", "/api/tdarr/tdarr-private/activity", h.adminToken, `{}`)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("write route returned %d", response.Code)
	}
	config := serveRBACRequest(h.router, "GET", "/api/config", h.requesterToken)
	if config.Code != http.StatusOK || strings.Contains(config.Body.String(), "tdarr-private") || strings.Contains(config.Body.String(), "TEST_KEY") {
		t.Fatalf("requester config exposed an admin instance or failed: status=%d", config.Code)
	}
	if calls.Load() != 0 {
		t.Fatal("rejected requests reached Tdarr")
	}
	response = serveRBACRequest(h.router, "GET", "/api/tdarr/tdarr-private/activity", h.adminToken)
	if response.Code != http.StatusOK || calls.Load() != 1 || !strings.Contains(response.Body.String(), `"nodes":[]`) {
		t.Fatalf("admin activity failed: status=%d calls=%d", response.Code, calls.Load())
	}
}
