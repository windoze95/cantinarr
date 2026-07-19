package instance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestInstanceURLRejectsEmbeddedSecrets(t *testing.T) {
	for _, rawURL := range []string{
		"http://user:password@example.test/sonarr",
		"https://example.test/sonarr?apiKey=secret",
		"https://example.test/sonarr#secret",
	} {
		inst := &Instance{ServiceType: "sonarr", Name: "TV", URL: rawURL, APIKey: "write-only"}
		if err := validateRequiredFields(inst); err == nil {
			t.Fatalf("validateRequiredFields(%q) accepted a secret-bearing URL", rawURL)
		}
	}
}

// Instance URLs are dialed only by the server, so cluster-internal names
// (Docker service names, k8s cluster DNS, Tailscale MagicDNS) are a supported
// production configuration — lock in that the URL contract accepts them.
func TestInstanceURLAcceptsClusterInternalHostnames(t *testing.T) {
	for _, rawURL := range []string{
		"http://radarr:7878",
		"http://sonarr",
		"https://radarr.media.svc.cluster.local:7878",
		"http://chaptarr:8787/books",
	} {
		inst := &Instance{ServiceType: "sonarr", Name: "TV", URL: rawURL, APIKey: "write-only"}
		if err := validateRequiredFields(inst); err != nil {
			t.Fatalf("validateRequiredFields(%q) = %v, want accepted", rawURL, err)
		}
	}
	// A schemeless host:port parses as an opaque URL, not an absolute one.
	inst := &Instance{ServiceType: "sonarr", Name: "TV", URL: "radarr:7878", APIKey: "write-only"}
	if err := validateRequiredFields(inst); err == nil {
		t.Fatal("validateRequiredFields accepted a schemeless URL")
	}
}

func TestTestConnectionEndpoint(t *testing.T) {
	const storedKey = "stored-api-secret"
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/system/status" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Api-Key") != storedKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(arr.Close)

	s := newTestStore(t)
	stored := &Instance{ServiceType: "radarr", Name: "Movies", URL: arr.URL, APIKey: storedKey}
	if err := s.Create(stored); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	h := NewHandler(s, nil)
	router := chi.NewRouter()
	router.Post("/instances/test", h.TestConnection)
	do := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "/instances/test", strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// A candidate config tests without persisting anything; name is optional
	// because the Test button is usable before the form is complete.
	rec := do(`{"service_type":"radarr","url":"` + arr.URL + `","api_key":"` + storedKey + `"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("test candidate = %d %s, want 204", rec.Code, rec.Body.String())
	}

	// Editing an existing instance: blank credentials fall back to the stored
	// write-only ones, so re-testing an unmodified form passes.
	rec = do(`{"id":"` + stored.ID + `","url":"` + arr.URL + `","api_key":""}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("test with stored credentials = %d %s, want 204", rec.Code, rec.Body.String())
	}

	// A wrong key still fails even when an id is supplied.
	rec = do(`{"id":"` + stored.ID + `","url":"` + arr.URL + `","api_key":"wrong"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "connection test failed") {
		t.Fatalf("test with wrong key = %d %s, want 400 connection test failed", rec.Code, rec.Body.String())
	}

	if rec := do(`{"id":"radarr-missing","url":"` + arr.URL + `"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("test unknown id = %d, want 404", rec.Code)
	}
	if rec := do(`{"service_type":"floppy","url":"` + arr.URL + `"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("test unknown service type = %d, want 400", rec.Code)
	}
	if rec := do(`not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("test invalid body = %d, want 400", rec.Code)
	}
}

func TestValidateArrURLDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if got := r.Header.Get("X-Api-Key"); got != "" {
			t.Errorf("redirect destination received X-Api-Key %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "validation-secret" {
			t.Errorf("validation source X-Api-Key = %q", got)
		}
		http.Redirect(w, r, destination.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	err := validateArrURL(source.URL, "validation-secret", "v3")
	if err == nil || !strings.Contains(err.Error(), "status 307") {
		t.Fatalf("validateArrURL redirect error = %v, want status 307", err)
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

func TestValidateConnectionDoesNotFollowServiceRedirects(t *testing.T) {
	serviceTypes := []string{
		"radarr",
		"sonarr",
		"chaptarr",
		"sabnzbd",
		"qbittorrent",
		"nzbget",
		"transmission",
		"tautulli",
	}

	for _, serviceType := range serviceTypes {
		t.Run(serviceType, func(t *testing.T) {
			var redirectedRequests atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirectedRequests.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(destination.Close)

			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
			}))
			t.Cleanup(source.Close)

			inst := &Instance{
				ServiceType: serviceType,
				Name:        serviceType,
				URL:         source.URL,
				APIKey:      "service-api-secret",
				Username:    "service-user",
				Password:    "service-password",
			}
			if err := validateConnection(inst); err == nil {
				t.Fatal("validateConnection accepted an upstream redirect")
			}
			if got := redirectedRequests.Load(); got != 0 {
				t.Fatalf("redirect destination received %d requests, want 0", got)
			}
		})
	}
}
