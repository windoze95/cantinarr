package instance

import (
	"context"
	"encoding/json"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
)

func newHardcoverRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth.GetClaims(r.Context()) == nil {
				r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: 1, Role: auth.RoleAdmin}))
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/instances/{instanceID}/hardcover", h.HardcoverStatus)
	r.Put("/instances/{instanceID}/hardcover", h.SaveHardcoverToken)
	r.Delete("/instances/{instanceID}/hardcover", h.ClearHardcoverToken)
	r.Post("/instances/{instanceID}/hardcover/device/begin", h.BeginHardcoverDevice)
	r.Get("/instances/{instanceID}/hardcover/device/{flowID}", h.CheckHardcoverDevice)
	r.Delete("/instances/{instanceID}/hardcover/device/{flowID}", h.CancelHardcoverDevice)
	r.Post("/instances/{instanceID}/hardcover/apply", h.ApplyHardcoverConnection)
	return r
}

// fakeHardcover stands in for api.hardcover.app: it accepts exactly one
// bearer token and records the Authorization header it was shown.
func fakeHardcover(t *testing.T, accept string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("hardcover method = %s, want POST", r.Method)
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (!strings.Contains(body.Query, "books_trending") || strings.Contains(body.Query, " me ")) {
			t.Errorf("hardcover query = %q, want catalog queries only", body.Query)
		}
		if r.Header.Get("Authorization") != "Bearer "+accept {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":42}],"books_trending":{"ids":[42]}}}`))
	}))
}

func do(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("status body %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestHardcoverTokenStoredEncryptedAndWriteOnly(t *testing.T) {
	s := newTestStore(t)
	id := mkInstance(t, s, "chaptarr", "Books")

	has, err := s.HasHardcoverToken(id)
	if err != nil || has {
		t.Fatalf("fresh instance HasHardcoverToken = %v, %v; want false", has, err)
	}
	if err := s.SetHardcoverToken(id, "hc-secret-token"); err != nil {
		t.Fatalf("SetHardcoverToken: %v", err)
	}
	var stored string
	if err := s.db.QueryRow("SELECT hardcover_token FROM service_instances WHERE id = ?", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "" || strings.Contains(stored, "hc-secret-token") {
		t.Fatalf("stored token %q is not encrypted", stored)
	}
	got, err := s.HardcoverToken(id)
	if err != nil || got != "hc-secret-token" {
		t.Fatalf("HardcoverToken = %q, %v", got, err)
	}
	has, err = s.HasHardcoverToken(id)
	if err != nil || !has {
		t.Fatalf("HasHardcoverToken after set = %v, %v; want true", has, err)
	}

	// The general instance save must not disturb the token: it is not part
	// of the Instance document at all.
	inst, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	inst.Name = "Books renamed"
	if err := s.Update(inst); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.HardcoverToken(id); got != "hc-secret-token" {
		t.Fatalf("token after unrelated Update = %q, want kept", got)
	}

	if err := s.ClearHardcoverToken(id); err != nil {
		t.Fatalf("ClearHardcoverToken: %v", err)
	}
	if got, _ := s.HardcoverToken(id); got != "" {
		t.Fatalf("token after clear = %q, want empty", got)
	}
	if err := s.SetHardcoverToken("missing", "x"); err == nil {
		t.Fatal("SetHardcoverToken on a missing instance should fail")
	}
}

func TestSaveHardcoverTokenVerifiesThenStores(t *testing.T) {
	s := newTestStore(t)
	id := mkInstance(t, s, "chaptarr", "Books")
	var calls atomic.Int32
	hc := fakeHardcover(t, "good-token", &calls)
	defer hc.Close()
	h := NewHandler(s, nil)
	h.hardcoverAPIURL = hc.URL
	router := newHardcoverRouter(h)

	// Not connected yet, and the type supports it.
	rec := do(t, router, http.MethodGet, "/instances/"+id+"/hardcover", "")
	if st := decodeStatus(t, rec); rec.Code != 200 || st["supported"] != true || st["configured"] != false {
		t.Fatalf("initial status = %d %v", rec.Code, st)
	}

	// The pasted form, prefix and whitespace included, is accepted.
	rec = do(t, router, http.MethodPut, "/instances/"+id+"/hardcover", `{"token":"  Bearer good-token\n"}`)
	if st := decodeStatus(t, rec); rec.Code != 200 || st["configured"] != true {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("hardcover verify calls = %d, want 1", calls.Load())
	}
	if got, _ := s.HardcoverToken(id); got != "good-token" {
		t.Fatalf("stored token = %q, want the normalized token", got)
	}
	if strings.Contains(rec.Body.String(), "good-token") {
		t.Fatalf("save response leaks the token: %s", rec.Body.String())
	}

	rec = do(t, router, http.MethodGet, "/instances/"+id+"/hardcover", "")
	if st := decodeStatus(t, rec); st["configured"] != true || strings.Contains(rec.Body.String(), "good-token") {
		t.Fatalf("status after save = %s", rec.Body.String())
	}

	rec = do(t, router, http.MethodDelete, "/instances/"+id+"/hardcover", "")
	if st := decodeStatus(t, rec); rec.Code != 200 || st["configured"] != false {
		t.Fatalf("clear = %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := s.HardcoverToken(id); got != "" {
		t.Fatalf("token after clear = %q", got)
	}
}

func TestSaveHardcoverTokenRejectedKeepsPreviousConnection(t *testing.T) {
	s := newTestStore(t)
	id := mkInstance(t, s, "chaptarr", "Books")
	if err := s.SetHardcoverToken(id, "old-token"); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	hc := fakeHardcover(t, "good-token", &calls)
	defer hc.Close()
	h := NewHandler(s, nil)
	h.hardcoverAPIURL = hc.URL
	router := newHardcoverRouter(h)

	rec := do(t, router, http.MethodPut, "/instances/"+id+"/hardcover", `{"token":"wrong-token"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "rejected") {
		t.Fatalf("rejected token = %d %s, want 400 naming the rejection", rec.Code, rec.Body.String())
	}
	if got, _ := s.HardcoverToken(id); got != "old-token" {
		t.Fatalf("token after rejected save = %q, want the old one kept", got)
	}
}

func TestSaveHardcoverTokenTreatsGraphQLErrorsAsRejection(t *testing.T) {
	s := newTestStore(t)
	id := mkInstance(t, s, "chaptarr", "Books")
	hc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Hardcover reports an expired token as a 200 with a GraphQL error.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"Could not verify JWT: JWTExpired"}]}`))
	}))
	defer hc.Close()
	h := NewHandler(s, nil)
	h.hardcoverAPIURL = hc.URL
	rec := do(t, newHardcoverRouter(h), http.MethodPut, "/instances/"+id+"/hardcover", `{"token":"expired"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expired token = %d %s, want 400", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "JWTExpired") {
		t.Fatalf("Hardcover's own error text should not be echoed: %s", rec.Body.String())
	}
	if has, _ := s.HasHardcoverToken(id); has {
		t.Fatal("an expired token must not be stored")
	}
}

func TestSaveHardcoverTokenUnreachableIsNotARejection(t *testing.T) {
	s := newTestStore(t)
	id := mkInstance(t, s, "chaptarr", "Books")
	hc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	hc.Close() // nothing listens any more
	h := NewHandler(s, nil)
	h.hardcoverAPIURL = hc.URL
	rec := do(t, newHardcoverRouter(h), http.MethodPut, "/instances/"+id+"/hardcover", `{"token":"maybe-fine"}`)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "reach Hardcover") {
		t.Fatalf("unreachable = %d %s, want 502 blaming reachability", rec.Code, rec.Body.String())
	}
	if has, _ := s.HasHardcoverToken(id); has {
		t.Fatal("an unverified token must not be stored")
	}
}

func TestHardcoverTokenRefusedForNonChaptarr(t *testing.T) {
	s := newTestStore(t)
	id := mkInstance(t, s, "radarr", "Movies")
	var calls atomic.Int32
	hc := fakeHardcover(t, "good-token", &calls)
	defer hc.Close()
	h := NewHandler(s, nil)
	h.hardcoverAPIURL = hc.URL
	router := newHardcoverRouter(h)

	rec := do(t, router, http.MethodGet, "/instances/"+id+"/hardcover", "")
	if st := decodeStatus(t, rec); rec.Code != 200 || st["supported"] != false {
		t.Fatalf("radarr status = %d %v, want unsupported", rec.Code, st)
	}
	rec = do(t, router, http.MethodPut, "/instances/"+id+"/hardcover", `{"token":"good-token"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("radarr save = %d, want 400", rec.Code)
	}
	if calls.Load() != 0 {
		t.Fatal("Hardcover must not be dialed for an unsupported instance type")
	}
	rec = do(t, router, http.MethodPut, "/instances/missing/hardcover", `{"token":"good-token"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing instance = %d, want 404", rec.Code)
	}
}

func TestNormalizeHardcoverToken(t *testing.T) {
	cases := map[string]string{
		"abc":            "abc",
		"  Bearer abc  ": "abc",
		"bearer abc":     "abc",
		"Bearer":         "Bearer",
		"":               "",
		"   ":            "",
		"has space":      "",
		"line\nbreak":    "",
		strings.Repeat("x", hardcoverTokenMaxLen+1): "",
	}
	for in, want := range cases {
		got, err := normalizeHardcoverToken(in)
		if want == "" {
			if err == nil {
				t.Errorf("normalize(%q) = %q, want error", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}
