// Handler tests live in watchhistory_test because they drive the handler
// through the real instance store/registry, and the instance package imports
// this one.
package watchhistory_test

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
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/watchhistory"
)

const (
	tautulliKey = "TAUTULLI_HANDLER_KEY_SENTINEL"
	tracearrKey = "trr_pub_HANDLER_KEY_SENTINEL"
)

// --- test environment ---

type env struct {
	store    *instance.Store
	registry *instance.Registry
	router   chi.Router
}

// newEnv builds the handler over a real in-memory instance store and mounts it
// on both route families the API router serves. Auth middleware is
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
	handler := watchhistory.NewHandler(store, registry)

	router := chi.NewRouter()
	for _, prefix := range []string{"/watch-history", "/tautulli"} {
		router.Get(prefix+"/{instanceID}/activity", handler.GetActivity)
		router.Get(prefix+"/{instanceID}/history", handler.GetHistory)
		router.Get(prefix+"/{instanceID}/stats", handler.GetStats)
	}

	return &env{store: store, registry: registry, router: router}
}

func (e *env) mkInstance(t *testing.T, serviceType, baseURL, apiKey string) string {
	t.Helper()
	inst := &instance.Instance{
		ServiceType: serviceType,
		Name:        serviceType + " test",
		URL:         baseURL,
		APIKey:      apiKey,
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

func decode(t *testing.T, rec *httptest.ResponseRecorder, out interface{}) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
}

// --- Tautulli fake ---

// tautulliFake serves Tautulli's command-based API: every request hits
// /api/v2 and is dispatched on the cmd query parameter.
type tautulliFake struct {
	t    *testing.T
	data map[string]string // cmd -> data payload wrapped in a success envelope

	// status, when non-zero, forces a bare HTTP error whose body echoes the
	// apikey, proving the handler never copies upstream bodies into
	// client-facing errors.
	status int

	mu    sync.Mutex
	calls []url.Values
}

func (f *tautulliFake) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2" {
			f.t.Errorf("tautulli path = %s, want /api/v2", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("apikey") != tautulliKey {
			f.t.Errorf("tautulli apikey = %q, want %q", q.Get("apikey"), tautulliKey)
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
	}))
	t.Cleanup(srv.Close)
	return srv.URL
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

// --- Tracearr fake ---

// tracearrFake serves the three public API paths the provider reads, with
// scripted history cursors, and asserts the bearer key on every request.
type tracearrFake struct {
	t       *testing.T
	streams string
	pages   map[string]string // cursor -> history page body
	status  int               // non-zero forces an error whose body echoes the key
	headers http.Header

	mu    sync.Mutex
	calls []*url.URL
}

func (f *tracearrFake) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+tracearrKey {
			f.t.Errorf("tracearr Authorization = %q, want bearer key", got)
		}
		f.mu.Lock()
		f.calls = append(f.calls, r.URL)
		f.mu.Unlock()
		for k, vs := range f.headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		if f.status != 0 {
			http.Error(w, "upstream error while handling "+tracearrKey, f.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/public/health":
			_, _ = io.WriteString(w, `{"status":"ok","version":"2.2.3","servers":[]}`)
		case "/api/v2/public/streams":
			_, _ = io.WriteString(w, f.streams)
		case "/api/v2/public/history":
			body, ok := f.pages[r.URL.Query().Get("cursor")]
			if !ok {
				f.t.Errorf("unexpected history cursor %q", r.URL.Query().Get("cursor"))
				body = `{"data":[],"meta":{"nextCursor":null,"pageSize":100}}`
			}
			_, _ = io.WriteString(w, body)
		default:
			f.t.Errorf("unexpected tracearr path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *tracearrFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *tracearrFake) call(t *testing.T, i int) *url.URL {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.calls) {
		t.Fatalf("only %d calls reached the tracearr fake, want index %d", len(f.calls), i)
	}
	return f.calls[i]
}

// --- shared response shapes ---

type streamJSON struct {
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
	MediaType       string `json:"media_type"`
	Server          string `json:"server"`
	ServerType      string `json:"server_type"`
}

type activityJSON struct {
	StreamCount        int          `json:"stream_count"`
	TotalBandwidthKbps int          `json:"total_bandwidth_kbps"`
	Streams            []streamJSON `json:"streams"`
}

type coverageJSON struct {
	Plays     int    `json:"plays"`
	Since     string `json:"since"`
	Until     string `json:"until"`
	Truncated bool   `json:"truncated"`
	Note      string `json:"note"`
}

type historyJSON struct {
	Items []struct {
		User            string `json:"user"`
		FullTitle       string `json:"full_title"`
		Date            string `json:"date"`
		DurationSeconds int    `json:"duration_seconds"`
		PercentComplete int    `json:"percent_complete"`
		Player          string `json:"player"`
		Platform        string `json:"platform"`
		MediaType       string `json:"media_type"`
		Server          string `json:"server"`
		ServerType      string `json:"server_type"`
	} `json:"items"`
	Coverage coverageJSON `json:"coverage"`
}

type statJSON struct {
	Title string `json:"title"`
	User  string `json:"user"`
	Plays int    `json:"plays"`
}

type statsJSON struct {
	TopMovies []statJSON   `json:"top_movies"`
	TopShows  []statJSON   `json:"top_shows"`
	TopUsers  []statJSON   `json:"top_users"`
	Coverage  coverageJSON `json:"coverage"`
}

// --- Tautulli through the neutral handler ---

func TestTautulliActivityShapesResponse(t *testing.T) {
	fake := &tautulliFake{t: t, data: map[string]string{
		"get_activity": `{
			"stream_count": "1",
			"total_bandwidth": "9500",
			"sessions": [
				{"user":"julian","title":"Heat","full_title":"Heat (1995)","player":"Living Room TV","product":"Plex for Apple TV","state":"playing","progress_percent":"42","quality_profile":"1080p","transcode_decision":"transcode","bandwidth":"9500","media_type":"movie"}
			]
		}`,
	}}
	e := newEnv(t)
	id := e.mkInstance(t, "tautulli", fake.serve(t), tautulliKey)

	var resp activityJSON
	decode(t, e.do(t, "/watch-history/"+id+"/activity"), &resp)
	if resp.StreamCount != 1 || resp.TotalBandwidthKbps != 9500 || len(resp.Streams) != 1 {
		t.Fatalf("activity = %+v, want one stream at 9500 kbps", resp)
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
	if s.MediaType != "movie" || s.ServerType != "plex" || s.Server != "" {
		t.Errorf("stream = %+v, want media_type, server_type plex and no server name", s)
	}
}

func TestTautulliHistoryShapesRowsAndLimit(t *testing.T) {
	fake := &tautulliFake{t: t, data: map[string]string{
		"get_history": `{"data":[
			{"user":"julian","full_title":"Heat (1995)","date":"1720000000","duration":"3600","percent_complete":87,"player":"TV","platform":"tvOS"},
			{"user":"dex","full_title":"Andor - S02E03","date":0,"duration":600,"percent_complete":10,"player":"phone","platform":"iOS"}
		]}`,
	}}
	e := newEnv(t)
	id := e.mkInstance(t, "tautulli", fake.serve(t), tautulliKey)

	var resp historyJSON
	decode(t, e.do(t, "/watch-history/"+id+"/history"), &resp)
	if got := fake.lastCall(t).Get("length"); got != "50" {
		t.Errorf("default length = %q, want 50", got)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(resp.Items))
	}
	if resp.Items[0].Date != "2024-07-03T09:46:40Z" {
		t.Errorf("date = %q, want unix seconds rendered as RFC3339 UTC", resp.Items[0].Date)
	}
	if resp.Items[0].DurationSeconds != 3600 || resp.Items[0].PercentComplete != 87 || resp.Items[0].ServerType != "plex" {
		t.Errorf("item[0] = %+v, want numbers mapped and plex marked", resp.Items[0])
	}
	if resp.Items[1].Date != "" {
		t.Errorf("unknown date = %q, want empty string", resp.Items[1].Date)
	}
	if resp.Coverage.Plays != 2 || resp.Coverage.Note == "" || resp.Coverage.Truncated {
		t.Errorf("coverage = %+v, want the row count and a note", resp.Coverage)
	}

	// ?limit=N is forwarded; junk and non-positive limits fall back to 50.
	e.do(t, "/watch-history/"+id+"/history?limit=10")
	if got := fake.lastCall(t).Get("length"); got != "10" {
		t.Errorf("length = %q, want 10", got)
	}
	e.do(t, "/watch-history/"+id+"/history?limit=abc")
	if got := fake.lastCall(t).Get("length"); got != "50" {
		t.Errorf("length for junk limit = %q, want 50", got)
	}
	e.do(t, "/watch-history/"+id+"/history?limit=-3")
	if got := fake.lastCall(t).Get("length"); got != "50" {
		t.Errorf("length for negative limit = %q, want 50", got)
	}
}

func TestTautulliStatsBucketsRowsByStatID(t *testing.T) {
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
	id := e.mkInstance(t, "tautulli", fake.serve(t), tautulliKey)

	var resp statsJSON
	decode(t, e.do(t, "/watch-history/"+id+"/stats?days=7"), &resp)
	if got := fake.lastCall(t).Get("time_range"); got != "7" {
		t.Errorf("time_range = %q, want 7", got)
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
	if !strings.Contains(resp.Coverage.Note, "last 7 days") || resp.Coverage.Since == "" || resp.Coverage.Until == "" {
		t.Errorf("coverage = %+v, want the window described", resp.Coverage)
	}

	// Default window is 30 days.
	e.do(t, "/watch-history/"+id+"/stats")
	if got := fake.lastCall(t).Get("time_range"); got != "30" {
		t.Errorf("default time_range = %q, want 30", got)
	}
}

func TestStatsEmptyBucketsAreArraysNotNull(t *testing.T) {
	fake := &tautulliFake{t: t, data: map[string]string{"get_home_stats": `[]`}}
	e := newEnv(t)
	id := e.mkInstance(t, "tautulli", fake.serve(t), tautulliKey)

	rec := e.do(t, "/watch-history/"+id+"/stats")
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

// --- Tracearr through the neutral handler ---

const tracearrStreams = `{"data":[
	{"server_name":"Den","server_type":"jellyfin","username":"kylo","media_type":"movie","media_title":"Heat","duration_ms":6000000,"progress_ms":2520000,"state":"playing","is_transcode":false,"video_decision":"directplay","audio_decision":"directplay","bitrate":12000,"resolution":"1080p","source_video_codec_display":"HEVC","device":"Shield","player":"Living room","product":"Jellyfin Android TV","platform":"Android"},
	{"server_name":"Den","server_type":"jellyfin","username":"rey","media_type":"episode","media_title":"Pilot","show_title":"Breaking Bad","season_number":1,"episode_number":1,"duration_ms":3480000,"progress_ms":348000,"state":"paused","is_transcode":false,"video_decision":"copy","audio_decision":"directplay","bitrate":8000,"resolution":"1080p","source_video_codec_display":"H.264","player":"Bedroom","product":"Jellyfin Web","platform":"Chrome"},
	{"server_name":"Loft","server_type":"plex","username":"finn","media_type":"track","media_title":"Hurt","artist_name":"Johnny Cash","duration_ms":200000,"progress_ms":100000,"state":"playing","is_transcode":true,"video_decision":null,"audio_decision":"transcode","bitrate":320,"resolution":null,"player":null,"device":"iPhone","product":"Plexamp","platform":"iOS"}
]}`

func TestTracearrActivityShapesResponse(t *testing.T) {
	fake := &tracearrFake{t: t, streams: tracearrStreams}
	e := newEnv(t)
	id := e.mkInstance(t, "tracearr", fake.serve(t), tracearrKey)

	var resp activityJSON
	decode(t, e.do(t, "/watch-history/"+id+"/activity"), &resp)
	if resp.StreamCount != 3 || resp.TotalBandwidthKbps != 20320 {
		t.Errorf("counts = (%d, %d), want (3, 20320: bitrates summed)", resp.StreamCount, resp.TotalBandwidthKbps)
	}
	if len(resp.Streams) != 3 {
		t.Fatalf("streams = %d, want 3", len(resp.Streams))
	}
	movie, episode, track := resp.Streams[0], resp.Streams[1], resp.Streams[2]
	if movie.StreamType != "direct play" || movie.Quality != "1080p HEVC" || movie.ProgressPercent != 42 || movie.FullTitle != "Heat" || movie.Title != "Heat" {
		t.Errorf("movie = %+v, want direct play, quality, 42%%, plain title", movie)
	}
	if movie.Server != "Den" || movie.ServerType != "jellyfin" || movie.MediaType != "movie" || movie.User != "kylo" || movie.Player != "Living room" || movie.Product != "Jellyfin Android TV" {
		t.Errorf("movie = %+v, want server, media type, user, player, product", movie)
	}
	if episode.StreamType != "copy" || episode.FullTitle != "Breaking Bad - S01E01 - Pilot" || episode.State != "paused" || episode.ProgressPercent != 10 {
		t.Errorf("episode = %+v, want copy, composed title, paused, 10%%", episode)
	}
	if track.StreamType != "transcode" || track.FullTitle != "Johnny Cash - Hurt" || track.Player != "iPhone" || track.Quality != "" || track.ServerType != "plex" || track.BandwidthKbps != 320 {
		t.Errorf("track = %+v, want transcode, artist title, device fallback, no quality", track)
	}
}

func tracearrHistoryRow(id, title, startedAt string) string {
	return `{"id":"` + id + `","server_name":"Den","server_type":"jellyfin","media_type":"movie","media_id":"m-` + title + `","media_title":"` + title + `","duration_ms":5400000,"percent_complete":98.7,"started_at":"` + startedAt + `","player":"TV","platform":"Android","user":{"id":"u1","username":"kylo"}}`
}

func TestTracearrHistoryPagesAndShapes(t *testing.T) {
	var rows []string
	for i := 0; i < 100; i++ {
		rows = append(rows, tracearrHistoryRow("a"+string(rune('0'+i%10)), "Heat", "2026-08-30T20:00:00.000Z"))
	}
	fake := &tracearrFake{t: t, pages: map[string]string{
		"":   `{"data":[` + strings.Join(rows, ",") + `],"meta":{"nextCursor":"c1","pageSize":100}}`,
		"c1": `{"data":[` + strings.Join(rows[:50], ",") + `],"meta":{"nextCursor":"c2","pageSize":100}}`,
		"c2": `{"data":[],"meta":{"nextCursor":null,"pageSize":100}}`,
	}}
	e := newEnv(t)
	id := e.mkInstance(t, "tracearr", fake.serve(t), tracearrKey)

	var resp historyJSON
	decode(t, e.do(t, "/watch-history/"+id+"/history?limit=150"), &resp)
	if len(resp.Items) != 150 || resp.Coverage.Plays != 150 || resp.Coverage.Truncated {
		t.Errorf("items = %d, coverage = %+v; want 150 rows and no truncation", len(resp.Items), resp.Coverage)
	}
	if fake.count() != 2 {
		t.Errorf("upstream calls = %d, want 2 (150 rows in pages of 100)", fake.count())
	}
	first, second := fake.call(t, 0).Query(), fake.call(t, 1).Query()
	if first.Get("pageSize") != "100" || first.Has("cursor") || second.Get("cursor") != "c1" {
		t.Errorf("queries = %v then %v, want pageSize=100 then cursor=c1", first, second)
	}
	item := resp.Items[0]
	if item.Date != "2026-08-30T20:00:00Z" || item.DurationSeconds != 5400 || item.PercentComplete != 99 || item.User != "kylo" || item.Server != "Den" || item.ServerType != "jellyfin" || item.MediaType != "movie" {
		t.Errorf("item = %+v, want fields mapped", item)
	}
	if !strings.Contains(resp.Coverage.Note, "150 most recent plays") {
		t.Errorf("note = %q, want the row count", resp.Coverage.Note)
	}

	// Junk limits fall back to 50, which fits one page.
	e.do(t, "/watch-history/"+id+"/history?limit=abc")
	if got := fake.call(t, fake.count()-1).Query().Get("pageSize"); got != "50" {
		t.Errorf("pageSize for junk limit = %q, want 50", got)
	}
}

func TestTracearrStatsAreDerivedAndCached(t *testing.T) {
	play := func(mediaType, mediaID, title, showID, show, userID, user string) string {
		return `{"media_type":"` + mediaType + `","media_id":"` + mediaID + `","media_title":"` + title + `","show_media_id":"` + showID + `","show_title":"` + show + `","user":{"id":"` + userID + `","username":"` + user + `"}}`
	}
	fake := &tracearrFake{t: t, pages: map[string]string{
		"": `{"data":[` + strings.Join([]string{
			play("movie", "m1", "Heat", "", "", "u1", "kylo"),
			play("movie", "m1", "Heat", "", "", "u2", "rey"),
			play("movie", "m2", "Alien", "", "", "u2", "rey"),
			play("episode", "e1", "Pilot", "s1", "Breaking Bad", "u2", "rey"),
			play("episode", "e2", "Cat's in the Bag", "s1", "Breaking Bad", "u3", "finn"),
			play("episode", "e3", "Winter Is Coming", "s2", "Game of Thrones", "u3", "finn"),
			play("track", "t1", "Hurt", "", "", "u1", "kylo"),
			play("movie", "m1", "Heat", "", "", "u1", "kylo"),
			play("movie", "m3", "Blade Runner", "", "", "u3", "finn"),
		}, ",") + `],"meta":{"nextCursor":null,"pageSize":100}}`,
	}}
	e := newEnv(t)
	id := e.mkInstance(t, "tracearr", fake.serve(t), tracearrKey)

	var resp statsJSON
	decode(t, e.do(t, "/watch-history/"+id+"/stats?days=7"), &resp)
	since := fake.call(t, 0).Query().Get("since")
	sinceAt, err := time.Parse(time.RFC3339, since)
	if err != nil {
		t.Fatalf("since = %q, want RFC3339: %v", since, err)
	}
	if drift := time.Until(sinceAt.AddDate(0, 0, 7)); drift < -time.Minute || drift > time.Minute {
		t.Errorf("since = %s, want now minus 7 days", since)
	}
	if len(resp.TopMovies) != 3 || resp.TopMovies[0].Title != "Heat" || resp.TopMovies[0].Plays != 3 || resp.TopMovies[1].Title != "Alien" || resp.TopMovies[2].Title != "Blade Runner" {
		t.Errorf("top_movies = %+v, want Heat 3 then Alien, Blade Runner", resp.TopMovies)
	}
	if len(resp.TopShows) != 2 || resp.TopShows[0].Title != "Breaking Bad" || resp.TopShows[0].Plays != 2 {
		t.Errorf("top_shows = %+v, want Breaking Bad 2 first", resp.TopShows)
	}
	// Every viewer has three plays, so the tie breaks on the label.
	if len(resp.TopUsers) != 3 || resp.TopUsers[0].User != "finn" || resp.TopUsers[0].Plays != 3 || resp.TopUsers[1].User != "kylo" || resp.TopUsers[2].User != "rey" {
		t.Errorf("top_users = %+v, want finn, kylo, rey at 3 plays each in label order", resp.TopUsers)
	}
	if resp.Coverage.Plays != 9 || resp.Coverage.Truncated || !strings.Contains(resp.Coverage.Note, "Based on 9 plays Tracearr recorded since") {
		t.Errorf("coverage = %+v, want nine plays described", resp.Coverage)
	}

	// The second read of the same window comes from the provider's cache.
	decode(t, e.do(t, "/watch-history/"+id+"/stats?days=7"), &resp)
	if fake.count() != 1 {
		t.Errorf("upstream calls = %d, want 1 (second read cached)", fake.count())
	}
}

func TestTracearrStatsPageCapIsReportedAsAFloor(t *testing.T) {
	fake := &tracearrFake{t: t, pages: map[string]string{}}
	for i := 0; i <= 25; i++ {
		cursor := ""
		if i > 0 {
			cursor = "c" + string(rune('a'+i))
		}
		next := "c" + string(rune('a'+i+1))
		fake.pages[cursor] = `{"data":[{"media_type":"movie","media_id":"m","media_title":"Heat","user":{"id":"u","username":"kylo"}}],"meta":{"nextCursor":"` + next + `","pageSize":100}}`
	}
	e := newEnv(t)
	id := e.mkInstance(t, "tracearr", fake.serve(t), tracearrKey)

	var resp statsJSON
	decode(t, e.do(t, "/watch-history/"+id+"/stats"), &resp)
	if fake.count() != 20 {
		t.Errorf("upstream calls = %d, want the 20-page cap", fake.count())
	}
	if !resp.Coverage.Truncated || !strings.Contains(resp.Coverage.Note, "counts are a floor") {
		t.Errorf("coverage = %+v, want truncation reported", resp.Coverage)
	}
}

func TestTracearrStatsEmptyWindowSaysWhatWasSearched(t *testing.T) {
	fake := &tracearrFake{t: t, pages: map[string]string{"": `{"data":[],"meta":{"nextCursor":null,"pageSize":100}}`}}
	e := newEnv(t)
	id := e.mkInstance(t, "tracearr", fake.serve(t), tracearrKey)

	rec := e.do(t, "/watch-history/"+id+"/stats?days=7")
	var resp statsJSON
	decode(t, rec, &resp)
	for _, key := range []string{`"top_movies":[]`, `"top_shows":[]`, `"top_users":[]`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Errorf("body = %s, want %s", rec.Body.String(), key)
		}
	}
	if !strings.Contains(resp.Coverage.Note, "No plays recorded by Tracearr since") || !strings.Contains(resp.Coverage.Note, "nothing older than that was searched") {
		t.Errorf("note = %q, want the empty-window explanation", resp.Coverage.Note)
	}
}

// --- the alias and the failure boundaries ---

func TestTautulliPrefixAliasesTheNeutralRoutes(t *testing.T) {
	tautulli := &tautulliFake{t: t, data: map[string]string{
		"get_activity": `{"stream_count":0,"total_bandwidth":0,"sessions":[]}`,
	}}
	tracearr := &tracearrFake{t: t, streams: tracearrStreams}
	e := newEnv(t)
	tautulliID := e.mkInstance(t, "tautulli", tautulli.serve(t), tautulliKey)
	tracearrID := e.mkInstance(t, "tracearr", tracearr.serve(t), tracearrKey)

	for _, id := range []string{tautulliID, tracearrID} {
		neutral := e.do(t, "/watch-history/"+id+"/activity")
		alias := e.do(t, "/tautulli/"+id+"/activity")
		if neutral.Code != http.StatusOK || alias.Code != http.StatusOK {
			t.Fatalf("%s: statuses = %d / %d, want 200", id, neutral.Code, alias.Code)
		}
		if neutral.Body.String() != alias.Body.String() {
			t.Errorf("%s: alias body differs:\n%s\n%s", id, neutral.Body.String(), alias.Body.String())
		}
	}
}

func TestLegacyWireKeysSurvive(t *testing.T) {
	tautulli := &tautulliFake{t: t, data: map[string]string{
		"get_activity":   `{"stream_count":1,"total_bandwidth":1,"sessions":[{"user":"u","title":"t","full_title":"ft","player":"p","product":"pr","state":"playing","progress_percent":1,"quality_profile":"q","transcode_decision":"copy","bandwidth":1}]}`,
		"get_history":    `{"data":[{"user":"u","full_title":"ft","date":1,"duration":1,"percent_complete":1,"player":"p","platform":"pl"}]}`,
		"get_home_stats": `[{"stat_id":"top_movies","rows":[{"title":"t","total_plays":1}]},{"stat_id":"top_users","rows":[{"user":"u","total_plays":1}]}]`,
	}}
	e := newEnv(t)
	id := e.mkInstance(t, "tautulli", tautulli.serve(t), tautulliKey)

	want := map[string][]string{
		"activity": {"stream_count", "total_bandwidth_kbps", "streams"},
		"history":  {"items"},
		"stats":    {"top_movies", "top_shows", "top_users"},
	}
	wantNested := map[string][]string{
		"streams":    {"user", "title", "full_title", "player", "product", "state", "progress_percent", "quality", "stream_type", "bandwidth_kbps"},
		"items":      {"user", "full_title", "date", "duration_seconds", "percent_complete", "player", "platform"},
		"top_movies": {"title", "plays"},
		"top_users":  {"user", "plays"},
	}
	for route, keys := range want {
		var body map[string]json.RawMessage
		decode(t, e.do(t, "/watch-history/"+id+"/"+route), &body)
		for _, key := range keys {
			raw, ok := body[key]
			if !ok {
				t.Errorf("%s: key %q missing from %v", route, key, body)
				continue
			}
			nested, ok := wantNested[key]
			if !ok {
				continue
			}
			var rows []map[string]json.RawMessage
			if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
				t.Errorf("%s.%s: want a non-empty array, got %s (%v)", route, key, raw, err)
				continue
			}
			for _, field := range nested {
				if _, ok := rows[0][field]; !ok {
					t.Errorf("%s.%s[0]: legacy field %q missing from %v", route, key, field, rows[0])
				}
			}
		}
	}
}

// TestUpstreamFailureMapsTo502 pins the failure boundary: a reachable-but-
// broken provider is the upstream's fault (502), and the error body never
// contains the instance credential even when the upstream echoes it.
func TestUpstreamFailureMapsTo502(t *testing.T) {
	e := newEnv(t)
	tautulliID := e.mkInstance(t, "tautulli", (&tautulliFake{t: t, status: http.StatusInternalServerError}).serve(t), tautulliKey)
	tracearrID := e.mkInstance(t, "tracearr", (&tracearrFake{t: t, status: http.StatusInternalServerError}).serve(t), tracearrKey)

	for _, tc := range []struct{ id, key string }{{tautulliID, tautulliKey}, {tracearrID, tracearrKey}} {
		for _, route := range []string{"activity", "history", "stats"} {
			rec := e.do(t, "/watch-history/"+tc.id+"/"+route)
			if rec.Code != http.StatusBadGateway {
				t.Errorf("%s %s status = %d, want 502", tc.id, route, rec.Code)
			}
			var resp map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("%s: decode error body: %v", route, err)
			}
			if !strings.Contains(resp["error"], "status 500") {
				t.Errorf("%s %s error = %q, want the upstream status surfaced", tc.id, route, resp["error"])
			}
			if strings.Contains(rec.Body.String(), tc.key) {
				t.Fatalf("%s %s error body leaked the credential: %s", tc.id, route, rec.Body.String())
			}
		}
	}
}

func TestTracearrKeyAndRateFailuresAreNamed(t *testing.T) {
	cases := []struct {
		status  int
		headers http.Header
		want    string
	}{
		{http.StatusUnauthorized, nil, "rejected the API key"},
		{http.StatusForbidden, nil, "owner account"},
		{http.StatusTooManyRequests, http.Header{"Retry-After": {"7"}}, "retry after 7s"},
	}
	for _, tc := range cases {
		fake := &tracearrFake{t: t, status: tc.status, headers: tc.headers}
		e := newEnv(t)
		id := e.mkInstance(t, "tracearr", fake.serve(t), tracearrKey)
		rec := e.do(t, "/watch-history/"+id+"/activity")
		if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), tc.want) || strings.Contains(rec.Body.String(), tracearrKey) {
			t.Errorf("status %d: response = %d %s, want 502 naming %q without the key", tc.status, rec.Code, rec.Body.String(), tc.want)
		}
	}
}

// TestUnreachableUpstreamLeaksNoSecretOrHost pins that transport-level
// errors reach the client without the credential (Tautulli puts its key in
// the URL that *url.Error embeds) and without the dialed host (Tracearr).
func TestUnreachableUpstreamLeaksNoSecretOrHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()

	e := newEnv(t)
	tautulliID := e.mkInstance(t, "tautulli", addr, tautulliKey)
	tracearrID := e.mkInstance(t, "tracearr", addr, tracearrKey)

	rec := e.do(t, "/watch-history/"+tautulliID+"/activity")
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), tautulliKey) || !strings.Contains(rec.Body.String(), "[redacted]") {
		t.Errorf("tautulli response = %d %s, want 502 with the key redacted", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "/watch-history/"+tracearrID+"/activity")
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), tracearrKey) || strings.Contains(rec.Body.String(), "127.0.0.1") {
		t.Errorf("tracearr response = %d %s, want 502 with no key and no host", rec.Code, rec.Body.String())
	}
}

func TestUnknownInstanceReturns404(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "/watch-history/tautulli-nope/activity")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "instance not found") {
		t.Errorf("body = %s, want instance-not-found error", rec.Body.String())
	}
}

func TestWrongServiceTypeReturns400(t *testing.T) {
	e := newEnv(t)
	id := e.mkInstance(t, "radarr", "http://radarr.internal", "radarr-key")
	for _, prefix := range []string{"/watch-history", "/tautulli"} {
		rec := e.do(t, prefix+"/"+id+"/activity")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400, body = %s", prefix, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "not a watch-history instance (tautulli, tracearr)") {
			t.Errorf("%s body = %s, want a wrong-service-type error naming the accepted types", prefix, rec.Body.String())
		}
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

type erroringProviders struct{}

func (erroringProviders) GetWatchHistoryProvider(string) (watchhistory.Provider, error) {
	return nil, errors.New("provider cache exploded")
}

// TestInfrastructureErrorsMapTo500 pins the other half of the failure
// boundary: store/registry failures are our fault (500), not the upstream's
// (502).
func TestInfrastructureErrorsMapTo500(t *testing.T) {
	cases := []struct {
		name    string
		handler *watchhistory.Handler
	}{
		{"store error", watchhistory.NewHandler(staticStore{err: errors.New("db locked")}, erroringProviders{})},
		{"provider error", watchhistory.NewHandler(staticStore{typ: "tracearr", found: true}, erroringProviders{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := chi.NewRouter()
			router.Get("/watch-history/{instanceID}/activity", tc.handler.GetActivity)
			req := httptest.NewRequest(http.MethodGet, "/watch-history/tracearr-x/activity", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}
