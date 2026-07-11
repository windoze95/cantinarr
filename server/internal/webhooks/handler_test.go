package webhooks

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	ws "github.com/windoze95/cantinarr-server/internal/websocket"
)

type fakeBroadcaster struct{ events []ws.Event }

func (f *fakeBroadcaster) Broadcast(e ws.Event) { f.events = append(f.events, e) }

type fakeInvalidator struct{ instanceIDs []string }

func (f *fakeInvalidator) InvalidateAvailabilityDigests(id string) {
	f.instanceIDs = append(f.instanceIDs, id)
}

type fakeContent struct {
	movies   []string
	episodes []string
}

func (f *fakeContent) NotifyNewMovie(title string, tmdbID int) { f.movies = append(f.movies, title) }
func (f *fakeContent) NotifyNewEpisode(seriesTitle string, tmdbID int) {
	f.episodes = append(f.episodes, seriesTitle)
}

type fixture struct {
	handler   *Handler
	hub       *fakeBroadcaster
	requests  *fakeInvalidator
	content   *fakeContent
	store     *instance.Store
	radarrID  string
	sonarrID  string
	radarrTok string
	sonarrTok string
}

// newFixture builds a handler over an in-memory DB with one Radarr and one
// Sonarr instance pointed at the given fake servers.
func newFixture(t *testing.T, radarrURL, sonarrURL string) *fixture {
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

	radarr := &instance.Instance{ServiceType: "radarr", Name: "Movies", URL: radarrURL, APIKey: "key"}
	if err := store.Create(radarr); err != nil {
		t.Fatalf("create radarr: %v", err)
	}
	sonarr := &instance.Instance{ServiceType: "sonarr", Name: "TV", URL: sonarrURL, APIKey: "key"}
	if err := store.Create(sonarr); err != nil {
		t.Fatalf("create sonarr: %v", err)
	}

	radarrTok, err := store.WebhookToken(radarr.ID)
	if err != nil {
		t.Fatalf("radarr token: %v", err)
	}
	sonarrTok, err := store.WebhookToken(sonarr.ID)
	if err != nil {
		t.Fatalf("sonarr token: %v", err)
	}

	f := &fixture{
		hub:       &fakeBroadcaster{},
		requests:  &fakeInvalidator{},
		content:   &fakeContent{},
		store:     store,
		radarrID:  radarr.ID,
		sonarrID:  sonarr.ID,
		radarrTok: radarrTok,
		sonarrTok: sonarrTok,
	}
	f.handler = NewHandler(store, instance.NewRegistry(store), f.hub, f.requests, f.content)
	return f
}

// post sends a webhook payload through a chi router so URL params resolve.
func (f *fixture) post(t *testing.T, path, body string, auth func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/api/webhooks/arr/{instanceID}", f.handler.HandleArr)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if auth != nil {
		auth(req)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func basicWebhookAuth(token string) func(*http.Request) {
	return func(r *http.Request) { r.SetBasicAuth("cantinarr", token) }
}

func (f *fixture) eventTypes() []string {
	var types []string
	for _, e := range f.hub.events {
		types = append(types, e.Type)
	}
	return types
}

func TestWebhookRejectsBadToken(t *testing.T) {
	f := newFixture(t, "http://unused", "http://unused")

	cases := []struct {
		name string
		path string
		auth func(*http.Request)
	}{
		{"no token", "/api/webhooks/arr/" + f.radarrID, nil},
		{"legacy query token", "/api/webhooks/arr/" + f.radarrID + "?token=" + f.radarrTok, nil},
		{"wrong token", "/api/webhooks/arr/" + f.radarrID, basicWebhookAuth("wrong")},
		{"other instance's token", "/api/webhooks/arr/" + f.radarrID, basicWebhookAuth(f.sonarrTok)},
	}
	for _, c := range cases {
		rec := f.post(t, c.path, `{"eventType":"Download"}`, c.auth)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", c.name, rec.Code)
		}
	}
	if len(f.hub.events) != 0 || len(f.requests.instanceIDs) != 0 {
		t.Errorf("unauthorized requests caused side effects: %v %v", f.hub.events, f.requests.instanceIDs)
	}

	rec := f.post(t, "/api/webhooks/arr/nope", `{}`, basicWebhookAuth(f.radarrTok))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown instance: status = %d, want 404", rec.Code)
	}
}

func TestWebhookAcceptsBasicAuthPassword(t *testing.T) {
	f := newFixture(t, "http://unused", "http://unused")
	rec := f.post(t, "/api/webhooks/arr/"+f.radarrID, `{"eventType":"Test"}`, func(r *http.Request) {
		r.SetBasicAuth("cantinarr", f.radarrTok)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.hub.events) != 0 {
		t.Errorf("Test event broadcast something: %v", f.hub.events)
	}
}

func TestWebhookRotationAcceptsPendingWithoutBreakingCurrent(t *testing.T) {
	f := newFixture(t, "http://unused", "http://unused")
	pending, err := f.store.PrepareWebhookToken(f.radarrID)
	if err != nil {
		t.Fatalf("PrepareWebhookToken: %v", err)
	}
	for name, token := range map[string]string{"current": f.radarrTok, "pending": pending} {
		rec := f.post(t, "/api/webhooks/arr/"+f.radarrID, `{"eventType":"Test"}`, basicWebhookAuth(token))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s credential status = %d, want 200", name, rec.Code)
		}
	}
	if err := f.store.PromoteWebhookToken(f.radarrID, pending); err != nil {
		t.Fatalf("PromoteWebhookToken: %v", err)
	}
	if rec := f.post(t, "/api/webhooks/arr/"+f.radarrID, `{"eventType":"Test"}`, basicWebhookAuth(f.radarrTok)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old credential after promotion status = %d, want 401", rec.Code)
	}
	if rec := f.post(t, "/api/webhooks/arr/"+f.radarrID, `{"eventType":"Test"}`, basicWebhookAuth(pending)); rec.Code != http.StatusOK {
		t.Fatalf("promoted credential status = %d, want 200", rec.Code)
	}
}

func TestWebhookMovieDownload(t *testing.T) {
	radarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/movie/7" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"title":"Manually Imported","tmdbId":600,"hasFile":true,"monitored":true}`))
	}))
	defer radarrSrv.Close()

	f := newFixture(t, radarrSrv.URL, "http://unused")
	rec := f.post(t, "/api/webhooks/arr/"+f.radarrID,
		`{"eventType":"Download","movie":{"id":7,"title":"Manually Imported","tmdbId":600}}`, basicWebhookAuth(f.radarrTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.requests.instanceIDs) != 1 || f.requests.instanceIDs[0] != f.radarrID {
		t.Errorf("digests invalidated = %v, want [%s]", f.requests.instanceIDs, f.radarrID)
	}
	if len(f.hub.events) != 1 || f.hub.events[0].Type != "request_status_changed" {
		t.Fatalf("events = %v, want one request_status_changed", f.eventTypes())
	}
	data := f.hub.events[0].Data
	if data["status"] != "available" || data["tmdb_id"] != 600 || data["media_type"] != "movie" {
		t.Errorf("event data = %v", data)
	}
	if len(f.content.movies) != 1 || f.content.movies[0] != "Manually Imported" {
		t.Errorf("movie pushes = %v, want [Manually Imported]", f.content.movies)
	}
}

func TestWebhookSeriesDownloadPartial(t *testing.T) {
	sonarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/series/9":
			_, _ = w.Write([]byte(`{"id":9,"title":"Gappy Show","tvdbId":500,"tmdbId":700,"monitored":true}`))
		case "/api/v3/episode":
			// One episode on disk, one aired without a file -> partial.
			_, _ = w.Write([]byte(`[
				{"id":1,"seriesId":9,"seasonNumber":1,"episodeNumber":1,"hasFile":true,"monitored":true},
				{"id":2,"seriesId":9,"seasonNumber":1,"episodeNumber":2,"hasFile":false,"monitored":true,"airDateUtc":"2020-01-01T00:00:00Z"}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer sonarrSrv.Close()

	f := newFixture(t, "http://unused", sonarrSrv.URL)
	rec := f.post(t, "/api/webhooks/arr/"+f.sonarrID,
		`{"eventType":"Download","series":{"id":9,"title":"Gappy Show","tvdbId":500}}`, basicWebhookAuth(f.sonarrTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.hub.events) != 1 || f.hub.events[0].Data["status"] != "partially_available" {
		t.Fatalf("events = %+v, want one partially_available", f.hub.events)
	}
	if f.hub.events[0].Data["tmdb_id"] != 700 {
		t.Errorf("tmdb_id = %v, want 700 (from live series, not payload)", f.hub.events[0].Data["tmdb_id"])
	}
	if len(f.content.episodes) != 1 || f.content.episodes[0] != "Gappy Show" {
		t.Errorf("episode pushes = %v, want [Gappy Show]", f.content.episodes)
	}
}

func TestWebhookDeleteAndGrab(t *testing.T) {
	f := newFixture(t, "http://unused", "http://unused")

	rec := f.post(t, "/api/webhooks/arr/"+f.sonarrID,
		`{"eventType":"SeriesDelete","series":{"id":9,"title":"Gone","tvdbId":500,"tmdbId":700}}`, basicWebhookAuth(f.sonarrTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("SeriesDelete status = %d", rec.Code)
	}
	if len(f.hub.events) != 1 || f.hub.events[0].Data["status"] != "unavailable" {
		t.Fatalf("SeriesDelete events = %+v, want unavailable", f.hub.events)
	}
	if len(f.content.episodes) != 0 {
		t.Errorf("delete must not push new-content alerts: %v", f.content.episodes)
	}
	if len(f.requests.instanceIDs) != 1 {
		t.Errorf("delete should invalidate digests once, got %v", f.requests.instanceIDs)
	}

	rec = f.post(t, "/api/webhooks/arr/"+f.radarrID, `{"eventType":"Grab"}`, basicWebhookAuth(f.radarrTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("Grab status = %d", rec.Code)
	}
	last := f.hub.events[len(f.hub.events)-1]
	if last.Type != "arr_queue_changed" || last.Data["service_type"] != "radarr" {
		t.Errorf("Grab event = %+v, want arr_queue_changed/radarr", last)
	}
}

func TestWebhookTokenStable(t *testing.T) {
	f := newFixture(t, "http://unused", "http://unused")
	again, err := f.store.WebhookToken(f.radarrID)
	if err != nil {
		t.Fatalf("WebhookToken: %v", err)
	}
	if again != f.radarrTok {
		t.Errorf("token changed across reads: %q vs %q", again, f.radarrTok)
	}
	if len(f.radarrTok) < 32 {
		t.Errorf("token too short: %q", f.radarrTok)
	}
	if f.radarrTok == f.sonarrTok {
		t.Error("instances share a webhook token")
	}
}
