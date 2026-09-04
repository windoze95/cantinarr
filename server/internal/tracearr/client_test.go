package tracearr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "trr_pub_TEST_TOKEN_SENTINEL"

// fake records every request and answers from a path-keyed map. status,
// when non-zero, forces an error whose body echoes the token, proving the
// client never surfaces upstream bodies.
type fake struct {
	t       *testing.T
	answers map[string]string // path -> JSON body
	status  int
	headers http.Header

	mu   sync.Mutex
	reqs []*http.Request
}

func (f *fake) serve() *Client {
	f.t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Clone(context.Background()))
		f.mu.Unlock()
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			f.t.Errorf("Authorization = %q, want bearer token", got)
		}
		for k, vs := range f.headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		if f.status != 0 {
			http.Error(w, "upstream failure while checking "+testToken, f.status)
			return
		}
		body, ok := f.answers[r.URL.Path]
		if !ok {
			f.t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	f.t.Cleanup(srv.Close)
	return NewClient(srv.URL+"/", testToken)
}

func (f *fake) last() *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		f.t.Fatal("no requests reached the fake")
	}
	return f.reqs[len(f.reqs)-1]
}

func (f *fake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func TestHealthUsesV1PathAndBearerToken(t *testing.T) {
	f := &fake{t: t, answers: map[string]string{
		"/api/v1/public/health": `{"status":"ok","version":"2.2.3","timestamp":"2026-09-01T00:00:00Z","servers":[{"id":"s1","name":"Den","type":"jellyfin","online":true,"activeStreams":2}]}`,
	}}
	c := f.serve()
	health, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Version != "2.2.3" || len(health.Servers) != 1 || health.Servers[0].Name != "Den" || !health.Servers[0].Online || health.Servers[0].ActiveStreams != 2 {
		t.Errorf("health = %+v, want version and server decoded", health)
	}
	if got := f.last().URL.Path; got != "/api/v1/public/health" {
		t.Errorf("path = %s, want /api/v1/public/health (trailing base slash trimmed)", got)
	}
	if got := f.last().Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
}

func TestStreamsDecodesNullsAsZero(t *testing.T) {
	f := &fake{t: t, answers: map[string]string{
		"/api/v2/public/streams": `{"data":[{"id":"x","server_name":"Den","server_type":"jellyfin","username":"kylo","media_type":"movie","media_title":"Heat","duration_ms":6000000,"progress_ms":2520000,"state":"playing","is_transcode":false,"video_decision":null,"audio_decision":"copy","bitrate":null,"resolution":"1080p","stream_video_codec_display":null,"source_video_codec_display":"HEVC","device":"Shield","player":null,"product":"Jellyfin Android TV","platform":"Android"}],"summary":{"total":1,"total_bitrate":"0 kbps"}}`,
	}}
	c := f.serve()
	streams, err := c.Streams(context.Background())
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	if len(streams.Data) != 1 {
		t.Fatalf("streams = %d, want 1", len(streams.Data))
	}
	s := streams.Data[0]
	if s.Bitrate != 0 || s.VideoDecision != "" || s.Player != "" || s.AudioDecision != "copy" || s.ProgressMS != 2520000 {
		t.Errorf("stream = %+v, want nulls decoded as zero values", s)
	}
}

// TestHistoryDecodesStringNumbers pins what the live Tracearr taught us:
// its Postgres driver serves bigint columns (progress_ms, total_duration_ms)
// as JSON strings, and a page must still decode; the first live pass
// answered 502 on every history row before this.
func TestHistoryDecodesStringNumbers(t *testing.T) {
	f := &fake{t: t, answers: map[string]string{
		"/api/v2/public/history": `{"data":[{"id":"h1","media_type":"episode","media_title":"Pilot","show_title":"Breaking Bad","season_number":"1","episode_number":"1","year":"2008","duration_ms":"61912","progress_ms":"1609","total_duration_ms":"2000","percent_complete":"80.5","bitrate":null,"segment_count":"1","user":{"id":"u1","username":"luke"}}],"meta":{"nextCursor":null,"pageSize":1}}`,
	}}
	c := f.serve()
	page, err := c.HistoryPage(context.Background(), HistoryQuery{PageSize: 1})
	if err != nil {
		t.Fatalf("HistoryPage: %v", err)
	}
	r := page.Data[0]
	if r.SeasonNumber != 1 || r.EpisodeNumber != 1 || r.Year != 2008 || r.DurationMS != 61912 || r.ProgressMS != 1609 || r.TotalDurationMS != 2000 || r.PercentComplete != 80.5 || r.Bitrate != 0 || r.SegmentCount != 1 {
		t.Errorf("record = %+v, want string numbers decoded and null as zero", r)
	}
}

func TestHistoryPageEncodesQuery(t *testing.T) {
	f := &fake{t: t, answers: map[string]string{
		"/api/v2/public/history": `{"data":[{"id":"h1","media_type":"episode","media_title":"Pilot","show_title":"Breaking Bad","season_number":1,"episode_number":1,"duration_ms":120000,"percent_complete":98.7,"started_at":"2026-08-30T20:00:00.000Z","watched":true,"user":{"id":"u1","username":"kylo"}}],"meta":{"nextCursor":"c2","pageSize":25}}`,
	}}
	c := f.serve()
	since := time.Date(2026, 8, 25, 1, 2, 3, 0, time.FixedZone("x", 3600))
	page, err := c.HistoryPage(context.Background(), HistoryQuery{Cursor: "c1", PageSize: 25, Since: since})
	if err != nil {
		t.Fatalf("HistoryPage: %v", err)
	}
	q := f.last().URL.Query()
	if q.Get("cursor") != "c1" || q.Get("pageSize") != "25" || q.Get("since") != "2026-08-25T00:02:03Z" || q.Has("until") {
		t.Errorf("query = %v, want cursor, pageSize, since in UTC RFC3339 and no until", q)
	}
	if page.Meta.NextCursor != "c2" || len(page.Data) != 1 || page.Data[0].PercentComplete != 98.7 || page.Data[0].User.Username != "kylo" {
		t.Errorf("page = %+v, want cursor and record decoded", page)
	}

	// Page sizes above the API maximum are clamped; zero means the maximum.
	for _, size := range []int{0, 500} {
		if _, err := c.HistoryPage(context.Background(), HistoryQuery{PageSize: size}); err != nil {
			t.Fatalf("HistoryPage(%d): %v", size, err)
		}
		if got := f.last().URL.Query().Get("pageSize"); got != "100" {
			t.Errorf("pageSize for %d = %q, want 100", size, got)
		}
	}
}

func TestKeyRejectionIsClassifiedAndNeverEchoesTheToken(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "tracearr rejected the API key"},
		{http.StatusForbidden, "must belong to the owner account"},
	}
	for _, tc := range cases {
		f := &fake{t: t, status: tc.status}
		c := f.serve()
		_, err := c.Health(context.Background())
		if err == nil {
			t.Fatalf("status %d: want error", tc.status)
		}
		if !errors.Is(err, ErrKeyRejected) {
			t.Errorf("status %d: errors.Is(ErrKeyRejected) = false for %v", tc.status, err)
		}
		if !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), testToken) {
			t.Errorf("status %d: error = %q, want %q without the token", tc.status, err, tc.want)
		}
	}
}

func TestRateLimitParsesRetryAfter(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		want    string
		minWait time.Duration
	}{
		{"seconds", "7", "retry after 7s", 7 * time.Second},
		{"http date", time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat), "retry after", time.Minute},
		{"absent", "", "retry later", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{t: t, status: http.StatusTooManyRequests, headers: http.Header{}}
			if tc.header != "" {
				f.headers.Set("Retry-After", tc.header)
			}
			c := f.serve()
			_, err := c.Streams(context.Background())
			if !errors.Is(err, ErrRateLimited) {
				t.Fatalf("errors.Is(ErrRateLimited) = false for %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
			var se *statusError
			if !errors.As(err, &se) {
				t.Fatalf("error %T is not a *statusError", err)
			}
			if se.RetryAfter() < tc.minWait {
				t.Errorf("RetryAfter = %v, want at least %v", se.RetryAfter(), tc.minWait)
			}
		})
	}
}

func TestOtherStatusesSurfaceOnlyTheCode(t *testing.T) {
	f := &fake{t: t, status: http.StatusInternalServerError}
	c := f.serve()
	_, err := c.Streams(context.Background())
	if err == nil || !strings.Contains(err.Error(), "streams: server returned status 500") || strings.Contains(err.Error(), testToken) {
		t.Fatalf("error = %v, want the status only", err)
	}
	if errors.Is(err, ErrKeyRejected) || errors.Is(err, ErrRateLimited) {
		t.Errorf("500 must not classify as a key or rate error: %v", err)
	}
}

func TestRedirectsAreRefused(t *testing.T) {
	var hits atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	t.Cleanup(dest.Close)
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/api/v1/public/health?token="+testToken, http.StatusFound)
	}))
	t.Cleanup(src.Close)

	c := NewClient(src.URL, testToken)
	_, err := c.Health(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redirects are not followed") {
		t.Fatalf("error = %v, want a refused-redirect error", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error leaked the token: %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("redirect destination received %d requests, want 0", hits.Load())
	}
}

func TestTransportErrorsNameNoHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()

	c := NewClient(addr, testToken)
	_, err := c.Health(context.Background())
	if err == nil {
		t.Fatal("want error from a closed server")
	}
	for _, leak := range []string{"127.0.0.1", "http://", testToken} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q leaked %q", err, leak)
		}
	}
}

func TestWalkHistoryStopConditions(t *testing.T) {
	pages := map[string]string{
		"":   `{"data":[{"id":"a"},{"id":"b"}],"meta":{"nextCursor":"c1","pageSize":2}}`,
		"c1": `{"data":[{"id":"c"},{"id":"d"}],"meta":{"nextCursor":"c2","pageSize":2}}`,
		"c2": `{"data":[{"id":"e"}],"meta":{"nextCursor":null,"pageSize":2}}`,
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(pages[r.URL.Query().Get("cursor")]))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, testToken)

	walk := func(maxPages int, stopAt int) (ids []string, truncated bool) {
		calls.Store(0)
		truncated, err := c.WalkHistory(context.Background(), HistoryQuery{PageSize: 2}, maxPages, func(r HistoryRecord) bool {
			ids = append(ids, r.ID)
			return stopAt == 0 || len(ids) < stopAt
		})
		if err != nil {
			t.Fatalf("WalkHistory: %v", err)
		}
		return ids, truncated
	}

	if ids, truncated := walk(10, 0); strings.Join(ids, "") != "abcde" || truncated || calls.Load() != 3 {
		t.Errorf("null cursor: ids=%v truncated=%v calls=%d, want abcde/false/3", ids, truncated, calls.Load())
	}
	if ids, truncated := walk(2, 0); strings.Join(ids, "") != "abcd" || !truncated || calls.Load() != 2 {
		t.Errorf("page cap: ids=%v truncated=%v calls=%d, want abcd/true/2", ids, truncated, calls.Load())
	}
	if ids, truncated := walk(10, 3); strings.Join(ids, "") != "abc" || truncated || calls.Load() != 2 {
		t.Errorf("visit stop: ids=%v truncated=%v calls=%d, want abc/false/2", ids, truncated, calls.Load())
	}
}

func TestWalkHistoryPropagatesErrors(t *testing.T) {
	f := &fake{t: t, status: http.StatusBadGateway}
	c := f.serve()
	visited := 0
	_, err := c.WalkHistory(context.Background(), HistoryQuery{}, 3, func(HistoryRecord) bool { visited++; return true })
	if err == nil || visited != 0 {
		t.Fatalf("err=%v visited=%d, want the upstream error and no visits", err, visited)
	}
}
