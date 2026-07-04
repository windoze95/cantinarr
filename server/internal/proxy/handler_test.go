package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// TestInstanceProxyForwardsChaptarrLookup is a smoke test for the Books-tab
// search path: GET /api/instances/{id}/api/v1/book/lookup?term=... must reach
// the Chaptarr upstream verbatim — prefix stripped, query string preserved, and
// the instance's X-Api-Key injected — with the response passed back unchanged.
func TestInstanceProxyForwardsChaptarrLookup(t *testing.T) {
	var gotPath, gotTerm, gotKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTerm = r.URL.Query().Get("term")
		gotKey = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"title":"Dune","foreignBookId":"gr:234","author":{"id":0,"authorName":"Frank Herbert"}}]`))
	}))
	defer upstream.Close()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{
		ServiceType: "chaptarr",
		Name:        "Books",
		URL:         upstream.URL,
		APIKey:      "secret-key",
	}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	r := chi.NewRouter()
	r.HandleFunc("/api/instances/{instanceID}/*", NewHandler(store).InstanceProxy())
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/instances/" + inst.ID + "/api/v1/book/lookup?term=dune")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if gotPath != "/api/v1/book/lookup" {
		t.Errorf("upstream path = %q, want /api/v1/book/lookup", gotPath)
	}
	if gotTerm != "dune" {
		t.Errorf("upstream term = %q, want \"dune\" (query string dropped?)", gotTerm)
	}
	if gotKey != "secret-key" {
		t.Errorf("upstream X-Api-Key = %q, want \"secret-key\"", gotKey)
	}
	if !bytes.Contains(body, []byte("Dune")) {
		t.Errorf("response not passed through verbatim: %s", body)
	}
}

// TestInstanceProxySingleContentType guards against duplicated Content-Type
// headers. The /api router pre-sets "application/json" on every response via
// middleware.SetHeader, and ReverseProxy *appends* the upstream's own header
// rather than replacing it. Browsers merge duplicates into
// "application/json, application/json; charset=utf-8", which Dio on Flutter
// web can't recognize as JSON — every arr screen in the web app then fails
// with a TypeError while native clients (which pick one value) work fine.
func TestInstanceProxySingleContentType(t *testing.T) {
	const upstreamContentType = "application/json; charset=utf-8"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", upstreamContentType)
		_, _ = w.Write([]byte(`[{"title":"Attack of the Clones"}]`))
	}))
	defer upstream.Close()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{
		ServiceType: "radarr",
		Name:        "Movies",
		URL:         upstream.URL,
		APIKey:      "secret-key",
	}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	r := chi.NewRouter()
	// Same default the real /api router applies to every response.
	r.Use(middleware.SetHeader("Content-Type", "application/json"))
	r.HandleFunc("/api/instances/{instanceID}/*", NewHandler(store).InstanceProxy())
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/instances/" + inst.ID + "/api/v3/movie")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	got := resp.Header.Values("Content-Type")
	if len(got) != 1 || got[0] != upstreamContentType {
		t.Errorf("Content-Type = %q, want exactly [%q]", got, upstreamContentType)
	}
}
