// Handler tests live in tautulli_test because they drive the handler through
// the real instance store/registry, and the instance package imports this one.
package tautulli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/tautulli"
)

const handlerAPIKey = "TAUTULLI_HANDLER_KEY_SENTINEL"

// --- test environment ---

type env struct {
	store    *instance.Store
	registry *instance.Registry
	router   chi.Router
}

// newEnv builds the handler over a real in-memory instance store and mounts it
// on the same route patterns the API router uses. Auth middleware is
// deliberately absent: authorization for these routes is covered by the api
// package's RBAC matrix tests.
func newEnv(t *testing.T) *env {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	registry := instance.NewRegistry(store)
	handler := tautulli.NewHandler(store, registry)

	router := chi.NewRouter()
	router.Get("/tautulli/{instanceID}/activity", handler.GetActivity)
	router.Get("/tautulli/{instanceID}/history", handler.GetHistory)
	router.Get("/tautulli/{instanceID}/stats", handler.GetStats)

	return &env{store: store, registry: registry, router: router}
}

func (e *env) mkInstance(t *testing.T, serviceType, baseURL string) string {
	t.Helper()
	inst := &instance.Instance{
		ServiceType: serviceType,
		Name:        serviceType + " test",
		URL:         baseURL,
		APIKey:      handlerAPIKey,
	}
	if err := e.store.Create(inst); err != nil {
		t.Fatalf("create %s instance: %v", serviceType, err)
	}
	return inst.ID
}

func (e *env) do(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// --- Tautulli fake ---

// tautulliFake serves Tautulli's command-based API: every request hits
// /api/v2 and is dispatched on the cmd query parameter.
type tautulliFake struct {
	t    *testing.T
	data map[string]string // cmd -> data payload wrapped in a success envelope

	// status, when non-zero, forces a bare HTTP error whose body echoes the
	// apikey — proving the handler never copies upstream bodies into
	// client-facing errors.
	status int

	mu    sync.Mutex
	calls []url.Values
}

func (f *tautulliFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2" {
			f.t.Errorf("tautulli path = %s, want /api/v2", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("apikey") != handlerAPIKey {
			f.t.Errorf("tautulli apikey = %q, want %q", q.Get("apikey"), handlerAPIKey)
		}
		f.mu.Lock()
		f.calls = append(f.calls, q)
		f.mu.Unlock()
		if f.status != 0 {
			http.Error(w, "upstream error while handling "+q.Get("apikey"), f.status)
			return
		}
		data, ok := f.data[q.Get("cmd")]
		if !ok {
			f.t.Errorf("unexpected tautulli cmd %q", q.Get("cmd"))
			data = "null"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"response":{"result":"success","message":null,"data":`+data+`}}`)
	}
}

func (f *tautulliFake) lastCall(t *testing.T) url.Values {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("no calls reached the tautulli fake")
	}
	return f.calls[len(f.calls)-1]
}

func (f *tautulliFake) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return srv.URL
}

// --- tests ---

func TestGetActivityShapesResponse(t *testing.T) {
	fake := &tautulliFake{t: t, data: map[string]string{
		"get_activity": `{
			"stream_count": "1",
			"total_bandwidth": "9500",
			"sessions": [
				{"user":"julian","title":"Heat","full_title":"Heat (1995)","player":"Living Room TV","product":"Plex for Apple TV","state":"playing","progress_percent":"42","quality_profile":"1080p","transcode_decision":"transcode","bandwidth":"9500"}
			]
		}`,
	}}
	e := newEnv(t)
	id := e.mkInstance(t, "tautulli", fake.serve(t))

	rec := e.do(t, "/tautulli/"+id+"/activity")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		StreamCount        int `json:"stream_count"`
		TotalBandwidthKbps int `json:"total_bandwidth_kbps"`
		Streams            []struct {
			User            string `json:"user"`
			Title           string `json:"title"`
			FullTitle       string `json:"full_title"`
			Player          string `json:"player"`
			Product         string `json:"product"`
			State           string `json:"state"`
			ProgressPercent int    `json:"progress_percent"`
			Quality         string `json:"quality"`
			StreamType      string `json:"stream_type"`
			BandwidthKbps   int    `json:"bandwidth_kbps"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode activity: %v", err)
	}
	if resp.StreamCount != 1 || resp.TotalBandwidthKbps != 9500 {
		t.Errorf("counts = (%d, %d), want (1, 9500)", resp.StreamCount, resp.TotalBandwidthKbps)
	}
	if len(resp.Streams) != 1 {
		t.Fatalf("streams = %d, want 1", len(resp.Streams))
	}
	s := resp.Streams[0]
	if s.User != "julian" || s.FullTitle != "Heat (1995)" || s.State != "playing" || s.ProgressPercent != 42 {
		t.Errorf("stream = %+v, want session fields mapped", s)
	}
	// The response renames arr-side vocabulary: quality_profile -> quality,
	// transcode_decision -> stream_type, bandwidth -> bandwidth_kbps.
	if s.Quality != "1080p" || s.StreamType != "transcode" || s.BandwidthKbps != 9500 {
		t.Errorf("stream = %+v, want quality/stream_type/bandwidth_kbps renamed", s)
	}
}

func TestGetHistoryShapesRowsAndLimit(t *testing.T) {
	fake := &tautulliFake{t: t, data: map[string]string{
		"get_history": `{"data":[
			{"user":"julian","full_title":"Heat (1995)","date":"1720000000","duration":"3600","percent_complete":87,"player":"TV","platform":"tvOS"},
			{"user":"dex","full_title":"Andor - S02E03","date":0,"duration":600,"percent_complete":10,"player":"phone","platform":"iOS"}
		]}`,
	}}
	e := newEnv(t)
	id := e.mkInstance(t, "tautulli", fake.serve(t))

	rec := e.do(t, "/tautulli/"+id+"/history")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := fake.lastCall(t).Get("length"); got != "50" {
		t.Errorf("default length = %q, want 50", got)
	}
	var resp struct {
		Items []struct {
			User            string `json:"user"`
			FullTitle       string `json:"full_title"`
			Date            string `json:"date"`
			DurationSeconds int    `json:"duration_seconds"`
			PercentComplete int    `json:"percent_complete"`
			Player          string `json:"player"`
			Platform        string `json:"platform"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(resp.Items))
	}
	if resp.Items[0].Date != "2024-07-03T09:46:40Z" {
		t.Errorf("date = %q, want unix seconds rendered as RFC3339 UTC", resp.Items[0].Date)
	}
	if resp.Items[0].DurationSeconds != 3600 || resp.Items[0].PercentComplete != 87 {
		t.Errorf("item[0] = %+v, want numbers mapped", resp.Items[0])
	}
	if resp.Items[1].Date != "" {
		t.Errorf("unknown date = %q, want empty string", resp.Items[1].Date)
	}

	// ?limit=N is forwarded; junk and non-positive limits fall back to 50.
	e.do(t, "/tautulli/"+id+"/history?limit=10")
	if got := fake.lastCall(t).Get("length"); got != "10" {
		t.Errorf("length = %q, want 10", got)
	}
	e.do(t, "/tautulli/"+id+"/history?limit=abc")
	if got := fake.lastCall(t).Get("length"); got != "50" {
		t.Errorf("length for junk limit = %q, want 50", got)
	}
	e.do(t, "/tautulli/"+id+"/history?limit=-3")
	if got := fake.lastCall(t).Get("length"); got != "50" {
		t.Errorf("length for negative limit = %q, want 50", got)
	}
}

func TestGetStatsBucketsRowsByStatID(t *testing.T) {
	fake := &tautulliFake{t: t, data: map[string]string{
		"get_home_stats": `[
			{"stat_id":"top_movies","rows":[{"title":"Heat","total_plays":"9"}]},
			{"stat_id":"top_tv","rows":[{"title":"Andor","total_plays":5}]},
			{"stat_id":"top_users","rows":[
				{"user":"julian","friendly_name":"Julian","total_plays":4},
				{"user":"dex","friendly_name":"","total_plays":2}
			]},
			{"stat_id":"top_platforms","rows":[{"title":"tvOS","total_plays":99}]}
		]`,
	}}
	e := newEnv(t)
	id := e.mkInstance(t, "tautulli", fake.serve(t))

	rec := e.do(t, "/tautulli/"+id+"/stats?days=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := fake.lastCall(t).Get("time_range"); got != "7" {
		t.Errorf("time_range = %q, want 7", got)
	}
	var resp struct {
		TopMovies []struct {
			Title string `json:"title"`
			Plays int    `json:"plays"`
		} `json:"top_movies"`
		TopShows []struct {
			Title string `json:"title"`
			Plays int    `json:"plays"`
		} `json:"top_shows"`
		TopUsers []struct {
			User  string `json:"user"`
			Plays int    `json:"plays"`
		} `json:"top_users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if len(resp.TopMovies) != 1 || resp.TopMovies[0].Title != "Heat" || resp.TopMovies[0].Plays != 9 {
		t.Errorf("top_movies = %+v, want Heat with 9 plays", resp.TopMovies)
	}
	if len(resp.TopShows) != 1 || resp.TopShows[0].Title != "Andor" || resp.TopShows[0].Plays != 5 {
		t.Errorf("top_shows = %+v, want top_tv bucketed as top_shows", resp.TopShows)
	}
	// friendly_name wins when present; user is the fallback. Unknown stat
	// blocks (top_platforms) are dropped.
	if len(resp.TopUsers) != 2 || resp.TopUsers[0].User != "Julian" || resp.TopUsers[1].User != "dex" {
		t.Errorf("top_users = %+v, want friendly-name fallback applied", resp.TopUsers)
	}

	// Default window is 30 days.
	e.do(t, "/tautulli/"+id+"/stats")
	if got := fake.lastCall(t).Get("time_range"); got != "30" {
		t.Errorf("default time_range = %q, want 30", got)
	}
}

func TestGetStatsEmptyBucketsAreArraysNotNull(t *testing.T) {
	fake := &tautulliFake{t: t, data: map[string]string{"get_home_stats": `[]`}}
	e := newEnv(t)
	id := e.mkInstance(t, "tautulli", fake.serve(t))

	rec := e.do(t, "/tautulli/"+id+"/stats")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, key := range []string{`"top_movies":[]`, `"top_shows":[]`, `"top_users":[]`} {
		if !strings.Contains(body, key) {
			t.Errorf("body = %s, want %s (empty arrays, not null)", body, key)
		}
	}
}

// TestUpstreamFailureMapsTo502 pins the failure boundary: a reachable-but-
// broken Tautulli is the upstream's fault (502), and the error body never
// contains the instance API key even when the upstream echoes it.
func TestUpstreamFailureMapsTo502(t *testing.T) {
	fake := &tautulliFake{t: t, status: http.StatusInternalServerError}
	e := newEnv(t)
	id := e.mkInstance(t, "tautulli", fake.serve(t))

	for _, route := range []string{"activity", "history", "stats"} {
		rec := e.do(t, "/tautulli/"+id+"/"+route)
		if rec.Code != http.StatusBadGateway {
			t.Errorf("%s status = %d, want 502", route, rec.Code)
		}
		var resp map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: decode error body: %v", route, err)
		}
		if !strings.Contains(resp["error"], "status 500") {
			t.Errorf("%s error = %q, want the upstream status surfaced", route, resp["error"])
		}
		if strings.Contains(rec.Body.String(), handlerAPIKey) {
			t.Fatalf("%s error body leaked the API key: %s", route, rec.Body.String())
		}
	}
}

// TestUnreachableUpstreamRedactsSecretIn502 pins that transport-level errors
// (whose URLs embed the apikey) reach the client redacted.
func TestUnreachableUpstreamRedactsSecretIn502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()

	e := newEnv(t)
	id := e.mkInstance(t, "tautulli", addr)

	rec := e.do(t, "/tautulli/"+id+"/activity")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), handlerAPIKey) {
		t.Fatalf("error body leaked the API key: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "[redacted]") {
		t.Errorf("body = %s, want the apikey redacted in the transport error", rec.Body.String())
	}
}

func TestUnknownInstanceReturns404(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "/tautulli/tautulli-nope/activity")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "instance not found") {
		t.Errorf("body = %s, want instance-not-found error", rec.Body.String())
	}
}

func TestWrongServiceTypeReturns400(t *testing.T) {
	e := newEnv(t)
	id := e.mkInstance(t, "radarr", "http://radarr.internal")
	rec := e.do(t, "/tautulli/"+id+"/activity")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not a tautulli instance") {
		t.Errorf("body = %s, want a wrong-service-type error", rec.Body.String())
	}
}

// erroring fakes for the infrastructure (500) paths; the handler's interfaces
// exist precisely so these can be simulated.
type staticStore struct {
	typ   string
	found bool
	err   error
}

func (s staticStore) LookupServiceType(string) (string, bool, error) { return s.typ, s.found, s.err }

type erroringRegistry struct{}

func (erroringRegistry) GetTautulliClient(string) (*tautulli.Client, error) {
	return nil, errors.New("client cache exploded")
}

// TestInfrastructureErrorsMapTo500 pins the other half of the failure
// boundary: store/registry failures are our fault (500), not the upstream's
// (502).
func TestInfrastructureErrorsMapTo500(t *testing.T) {
	cases := []struct {
		name    string
		handler *tautulli.Handler
	}{
		{"store error", tautulli.NewHandler(staticStore{err: errors.New("db locked")}, erroringRegistry{})},
		{"registry error", tautulli.NewHandler(staticStore{typ: "tautulli", found: true}, erroringRegistry{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := chi.NewRouter()
			router.Get("/tautulli/{instanceID}/activity", tc.handler.GetActivity)
			req := httptest.NewRequest(http.MethodGet, "/tautulli/tautulli-x/activity", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}
