package instance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// newUsersRouter mounts the instance-users endpoints the way router.go does,
// so the tests exercise the real URL params and JSON shapes the app relies on.
func newUsersRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/instances/{instanceID}/users", h.GetInstanceUsers)
	r.Put("/instances/{instanceID}/users", h.UpdateInstanceUsers)
	return r
}

func TestInstanceUsersEndpoints(t *testing.T) {
	s := newTestStore(t)
	h := NewHandler(s, nil)
	router := newUsersRouter(h)
	alice := createUser(t, s, "alice")
	bob := createUser(t, s, "bob")
	r1 := mkInstance(t, s, "radarr", "R1")
	r2 := mkInstance(t, s, "radarr", "R2")

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	decodePins := func(rec *httptest.ResponseRecorder) map[int64]string {
		t.Helper()
		var rows []struct {
			UserID     int64  `json:"user_id"`
			InstanceID string `json:"instance_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		pins := make(map[int64]string, len(rows))
		for _, row := range rows {
			pins[row.UserID] = row.InstanceID
		}
		return pins
	}

	// No pins yet: an empty JSON array, not null.
	rec := do("GET", "/instances/"+r1+"/users", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("empty pins body = %q, want []", got)
	}

	// Assign alice; the response reports the whole service type, so bob's
	// separate pin to a sibling instance shows up too.
	if err := s.SetUserDefault(bob, "radarr", r2); err != nil {
		t.Fatalf("SetUserDefault: %v", err)
	}
	rec = do("PUT", "/instances/"+r1+"/users", `{"user_ids":[`+jsonInt(alice)+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body.String())
	}
	pins := decodePins(rec)
	if pins[alice] != r1 || pins[bob] != r2 {
		t.Fatalf("pins = %v, want alice=%s bob=%s", pins, r1, r2)
	}

	// Unknown instance → 404; unknown user → 400 (FK).
	if rec := do("GET", "/instances/radarr-missing/users", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown instance = %d, want 404", rec.Code)
	}
	if rec := do("PUT", "/instances/radarr-missing/users", `{"user_ids":[]}`); rec.Code != http.StatusNotFound {
		t.Fatalf("PUT unknown instance = %d, want 404", rec.Code)
	}
	if rec := do("PUT", "/instances/"+r1+"/users", `{"user_ids":[999999]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown user = %d, want 400", rec.Code)
	}
	if rec := do("PUT", "/instances/"+r1+"/users", `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid body = %d, want 400", rec.Code)
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
