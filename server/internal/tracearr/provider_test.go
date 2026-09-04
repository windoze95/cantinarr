package tracearr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamTypeUsesTautulliVocabulary(t *testing.T) {
	cases := []struct {
		isTranscode  bool
		video, audio string
		want         string
	}{
		{false, "directplay", "directplay", "direct play"},
		{false, "", "", "direct play"},
		{false, "copy", "directplay", "copy"},
		{false, "directplay", "copy", "copy"},
		{false, "transcode", "copy", "transcode"},
		{false, "copy", "transcode", "transcode"},
		{true, "directplay", "directplay", "transcode"},
	}
	for _, tc := range cases {
		if got := streamType(tc.isTranscode, tc.video, tc.audio); got != tc.want {
			t.Errorf("streamType(%v,%q,%q) = %q, want %q", tc.isTranscode, tc.video, tc.audio, got, tc.want)
		}
	}
}

func TestQualityAndTitles(t *testing.T) {
	if got := quality("1080p", "", "HEVC"); got != "1080p HEVC" {
		t.Errorf("quality = %q, want source codec fallback", got)
	}
	if got := quality("4K", "H.264", "HEVC"); got != "4K H.264" {
		t.Errorf("quality = %q, want the delivered codec", got)
	}
	if got := quality("", "", ""); got != "" {
		t.Errorf("quality = %q, want empty", got)
	}
	cases := []struct {
		mediaType, title, show string
		season, episode        int
		artist, want           string
	}{
		{"movie", "Heat", "", 0, 0, "", "Heat"},
		{"episode", "Pilot", "Breaking Bad", 1, 1, "", "Breaking Bad - S01E01 - Pilot"},
		{"episode", "Special", "Breaking Bad", 0, 0, "", "Breaking Bad - Special"},
		{"episode", "Pilot", "", 1, 2, "", "S01E02 - Pilot"},
		{"track", "Hurt", "", 0, 0, "Johnny Cash", "Johnny Cash - Hurt"},
		{"track", "Hurt", "", 0, 0, "", "Hurt"},
		{"live", "Channel 4", "", 0, 0, "", "Channel 4"},
	}
	for _, tc := range cases {
		if got := fullTitle(tc.mediaType, tc.title, tc.show, tc.season, tc.episode, tc.artist); got != tc.want {
			t.Errorf("fullTitle(%s) = %q, want %q", tc.mediaType, got, tc.want)
		}
	}
}

func TestPercentAndTimes(t *testing.T) {
	if got := percent(2520000, 6000000); got != 42 {
		t.Errorf("percent = %d, want 42", got)
	}
	if got := percent(9, 0); got != 0 {
		t.Errorf("percent with no duration = %d, want 0", got)
	}
	if got := percent(7000, 6000); got != 100 {
		t.Errorf("percent past the end = %d, want 100", got)
	}
	if got := roundPercent(98.7); got != 99 {
		t.Errorf("roundPercent = %d, want 99", got)
	}
	if got := roundPercent(101); got != 100 {
		t.Errorf("roundPercent over = %d, want 100", got)
	}
	want := time.Date(2026, 8, 30, 20, 0, 0, 0, time.UTC)
	if got := parseTime("2026-08-30T20:00:00.000Z"); !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("parseTime = %v, want %v UTC", got, want)
	}
	if got := parseTime("yesterday"); !got.IsZero() {
		t.Errorf("parseTime junk = %v, want zero", got)
	}
}

func TestAggregatorKeysByIdAndRanks(t *testing.T) {
	agg := newAggregator()
	rec := func(mediaType, mediaID, title, showID, show, userID, user string) HistoryRecord {
		return HistoryRecord{MediaType: mediaType, MediaID: mediaID, MediaTitle: title, ShowMediaID: showID, ShowTitle: show, User: HistoryUser{ID: userID, Username: user}}
	}
	// Two distinct movies that share a title stay two rows.
	agg.add(rec("movie", "m1", "Heat", "", "", "u1", "kylo"))
	agg.add(rec("movie", "m2", "Heat", "", "", "u2", "rey"))
	agg.add(rec("movie", "m1", "Heat", "", "", "u1", "kylo"))
	// Episodes roll up to their show; a track counts for its viewer only.
	agg.add(rec("episode", "e1", "Pilot", "s1", "Breaking Bad", "u2", "rey"))
	agg.add(rec("episode", "e2", "Cat's in the Bag", "s1", "Breaking Bad", "u2", "rey"))
	agg.add(rec("track", "t1", "Hurt", "", "", "u3", "finn"))

	stats := agg.result()
	if agg.plays != 6 {
		t.Errorf("plays = %d, want 6 (every row counts)", agg.plays)
	}
	if len(stats.TopMovies) != 2 || stats.TopMovies[0].Plays != 2 || stats.TopMovies[1].Plays != 1 || stats.TopMovies[0].Title != "Heat" {
		t.Errorf("top movies = %+v, want two Heat rows ranked 2 then 1", stats.TopMovies)
	}
	if len(stats.TopShows) != 1 || stats.TopShows[0].Title != "Breaking Bad" || stats.TopShows[0].Plays != 2 {
		t.Errorf("top shows = %+v, want Breaking Bad with 2 plays", stats.TopShows)
	}
	// rey 3, kylo 2, finn 1.
	if len(stats.TopUsers) != 3 || stats.TopUsers[0].User != "rey" || stats.TopUsers[1].User != "kylo" || stats.TopUsers[2].User != "finn" {
		t.Errorf("top users = %+v, want rey, kylo, finn", stats.TopUsers)
	}
}

func TestRankedTiesBreakByLabelAndCutAtTopN(t *testing.T) {
	m := map[string]*counter{}
	for i := 0; i < topN+5; i++ {
		m[string(rune('a'+i))] = &counter{label: string(rune('a' + i)), plays: 1}
	}
	m["zz"] = &counter{label: "zz", plays: 9}
	out := ranked(m)
	if len(out) != topN {
		t.Fatalf("ranked = %d rows, want %d", len(out), topN)
	}
	if out[0].label != "zz" || out[1].label != "a" || out[2].label != "b" {
		t.Errorf("ranked = %+v, want plays desc then label asc", out[:3])
	}
}

// providerFake serves health, streams and history with scripted cursors and
// counts requests so caching can be observed.
type providerFake struct {
	t       *testing.T
	streams string
	pages   map[string]string // cursor -> page body
	calls   atomic.Int32
	queries []string
}

func (f *providerFake) provider(now func() time.Time) *Provider {
	f.t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case healthPath:
			_, _ = w.Write([]byte(`{"status":"ok","version":"2.2.3","servers":[{"id":"s1","name":"Den","type":"jellyfin","online":true,"activeStreams":1},{"id":"s2","name":"Loft","type":"plex","online":false,"activeStreams":0}]}`))
		case streamsPath:
			_, _ = w.Write([]byte(f.streams))
		case historyPath:
			f.queries = append(f.queries, r.URL.RawQuery)
			body, ok := f.pages[r.URL.Query().Get("cursor")]
			if !ok {
				f.t.Errorf("unexpected cursor %q", r.URL.Query().Get("cursor"))
				body = `{"data":[],"meta":{"nextCursor":null}}`
			}
			_, _ = w.Write([]byte(body))
		default:
			f.t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	f.t.Cleanup(srv.Close)
	return newProvider(NewClient(srv.URL, testToken), now)
}

func TestProviderServerInfoListsMonitoredServers(t *testing.T) {
	f := &providerFake{t: t}
	p := f.provider(time.Now)
	info, err := p.ServerInfo(context.Background())
	if err != nil {
		t.Fatalf("ServerInfo: %v", err)
	}
	if info.Name != "Tracearr" || info.Version != "2.2.3" || len(info.Servers) != 2 || info.Servers[1].Type != "plex" || info.Servers[1].Online {
		t.Errorf("info = %+v, want version and both servers", info)
	}
}

func TestProviderActivityMapsStreams(t *testing.T) {
	f := &providerFake{t: t, streams: `{"data":[
		{"server_name":"Den","server_type":"jellyfin","username":"kylo","media_type":"episode","media_title":"Pilot","show_title":"Breaking Bad","season_number":1,"episode_number":1,"duration_ms":3480000,"progress_ms":1740000,"state":"paused","is_transcode":true,"video_decision":"transcode","audio_decision":"copy","bitrate":8000,"resolution":"1080p","stream_video_codec_display":"H.264","source_video_codec_display":"HEVC","device":"Shield","player":"Living room","product":"Jellyfin Android TV","platform":"Android"},
		{"server_name":"Loft","server_type":"plex","username":"rey","media_type":"track","media_title":"Hurt","artist_name":"Johnny Cash","duration_ms":200000,"progress_ms":50000,"state":"playing","video_decision":null,"audio_decision":"directplay","bitrate":320,"player":null,"device":"iPhone","product":"Plexamp","platform":"iOS"}
	]}`}
	p := f.provider(time.Now)
	activity, err := p.Activity(context.Background())
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if activity.StreamCount != 2 || activity.TotalBandwidthKbps != 8320 {
		t.Errorf("activity = (%d streams, %d kbps), want 2 and 8320", activity.StreamCount, activity.TotalBandwidthKbps)
	}
	tv := activity.Streams[0]
	if tv.FullTitle != "Breaking Bad - S01E01 - Pilot" || tv.Title != "Pilot" || tv.State != "paused" || tv.ProgressPercent != 50 {
		t.Errorf("tv stream = %+v, want composed title, state and progress", tv)
	}
	if tv.StreamType != "transcode" || tv.Quality != "1080p H.264" || tv.BandwidthKbps != 8000 || tv.Player != "Living room" {
		t.Errorf("tv stream = %+v, want decision, quality, bandwidth, player", tv)
	}
	if tv.Server != "Den" || tv.ServerType != "jellyfin" || tv.MediaType != "episode" || tv.User != "kylo" {
		t.Errorf("tv stream = %+v, want server fields and user", tv)
	}
	music := activity.Streams[1]
	if music.FullTitle != "Johnny Cash - Hurt" || music.StreamType != "direct play" || music.Player != "iPhone" || music.Quality != "" || music.ProgressPercent != 25 {
		t.Errorf("music stream = %+v, want artist title, direct play, device fallback, no quality", music)
	}
}

func TestProviderHistoryPagesUpToLimit(t *testing.T) {
	row := func(id string) string {
		return `{"id":"` + id + `","server_name":"Den","server_type":"jellyfin","media_type":"movie","media_title":"Heat","duration_ms":5400000,"percent_complete":98.7,"started_at":"2026-08-30T20:00:00.000Z","player":"TV","platform":"Android","user":{"id":"u1","username":"kylo"}}`
	}
	f := &providerFake{t: t, pages: map[string]string{
		"":   `{"data":[` + row("a") + `,` + row("b") + `],"meta":{"nextCursor":"c1","pageSize":2}}`,
		"c1": `{"data":[` + row("c") + `,` + row("d") + `],"meta":{"nextCursor":"c2","pageSize":2}}`,
		"c2": `{"data":[` + row("e") + `],"meta":{"nextCursor":null,"pageSize":2}}`,
	}}
	p := f.provider(time.Now)

	history, err := p.History(context.Background(), 3)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history.Items) != 3 || history.Coverage.Plays != 3 || history.Coverage.Truncated {
		t.Errorf("history = %d items, coverage %+v; want 3 rows and no truncation", len(history.Items), history.Coverage)
	}
	if !strings.Contains(history.Coverage.Note, "3 most recent plays") {
		t.Errorf("note = %q, want the row count", history.Coverage.Note)
	}
	item := history.Items[0]
	if item.User != "kylo" || item.FullTitle != "Heat" || item.DurationSeconds != 5400 || item.PercentComplete != 99 || item.Server != "Den" || item.ServerType != "jellyfin" || item.Platform != "Android" {
		t.Errorf("item = %+v, want fields mapped", item)
	}
	if want := time.Date(2026, 8, 30, 20, 0, 0, 0, time.UTC); !item.Date.Equal(want) {
		t.Errorf("date = %v, want %v", item.Date, want)
	}
	// The fake serves two rows a page whatever was asked, so the limit of 3
	// took two pages and the walk stopped on the limit, not the page cap.
	if len(f.queries) != 2 || !strings.Contains(f.queries[0], "pageSize=3") {
		t.Errorf("first query = %q, want pageSize=3", f.queries[0])
	}
}

func TestProviderStatsDerivesRanksCachesAndReportsCoverage(t *testing.T) {
	play := func(id, mediaType, mediaID, title, showID, show, userID, user string) string {
		return `{"id":"` + id + `","media_type":"` + mediaType + `","media_id":"` + mediaID + `","media_title":"` + title + `","show_media_id":"` + showID + `","show_title":"` + show + `","started_at":"2026-08-30T20:00:00Z","user":{"id":"` + userID + `","username":"` + user + `"}}`
	}
	f := &providerFake{t: t, pages: map[string]string{
		"": `{"data":[` + strings.Join([]string{
			play("1", "movie", "m1", "Heat", "", "", "u1", "kylo"),
			play("2", "movie", "m1", "Heat", "", "", "u2", "rey"),
			play("3", "movie", "m2", "Blade Runner", "", "", "u1", "kylo"),
			play("4", "episode", "e1", "Pilot", "s1", "Breaking Bad", "u2", "rey"),
		}, ",") + `],"meta":{"nextCursor":"c1","pageSize":100}}`,
		"c1": `{"data":[` + strings.Join([]string{
			play("5", "episode", "e2", "Cat's in the Bag", "s1", "Breaking Bad", "u2", "rey"),
			play("6", "episode", "e3", "Winter Is Coming", "s2", "Game of Thrones", "u3", "finn"),
			play("7", "track", "t1", "Hurt", "", "", "u3", "finn"),
			play("8", "movie", "m1", "Heat", "", "", "u2", "rey"),
			play("9", "movie", "m3", "Alien", "", "", "u2", "rey"),
		}, ",") + `],"meta":{"nextCursor":null,"pageSize":100}}`,
	}}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	p := f.provider(func() time.Time { return clock })

	stats, err := p.Stats(context.Background(), 7)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if f.calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 pages", f.calls.Load())
	}
	if !strings.Contains(f.queries[0], "since=2026-08-25T12%3A00%3A00Z") || !strings.Contains(f.queries[0], "pageSize=100") {
		t.Errorf("first query = %q, want since=now-7d and pageSize=100", f.queries[0])
	}
	if len(stats.TopMovies) != 3 || stats.TopMovies[0].Title != "Heat" || stats.TopMovies[0].Plays != 3 || stats.TopMovies[1].Title != "Alien" || stats.TopMovies[2].Title != "Blade Runner" {
		t.Errorf("top movies = %+v, want Heat 3 then Alien and Blade Runner by label", stats.TopMovies)
	}
	if len(stats.TopShows) != 2 || stats.TopShows[0].Title != "Breaking Bad" || stats.TopShows[0].Plays != 2 || stats.TopShows[1].Title != "Game of Thrones" {
		t.Errorf("top shows = %+v, want Breaking Bad 2 then Game of Thrones", stats.TopShows)
	}
	if len(stats.TopUsers) != 3 || stats.TopUsers[0].User != "rey" || stats.TopUsers[0].Plays != 5 || stats.TopUsers[1].User != "finn" || stats.TopUsers[2].User != "kylo" {
		t.Errorf("top users = %+v, want rey 5, finn 2, kylo 2 (label tie-break)", stats.TopUsers)
	}
	c := stats.Coverage
	if c.Plays != 9 || c.Truncated || !c.Since.Equal(now.AddDate(0, 0, -7)) || !c.Until.Equal(now) {
		t.Errorf("coverage = %+v, want 9 plays over the window", c)
	}
	if !strings.Contains(c.Note, "Based on 9 plays Tracearr recorded since 25 Aug 2026") {
		t.Errorf("note = %q, want the play count and since date", c.Note)
	}

	// Within the TTL the answer is served from cache; after it, refetched.
	clock = now.Add(statsCacheTTL - time.Second)
	if _, err := p.Stats(context.Background(), 7); err != nil {
		t.Fatalf("cached Stats: %v", err)
	}
	if f.calls.Load() != 2 {
		t.Errorf("calls after cached read = %d, want 2", f.calls.Load())
	}
	// A different window is its own cache entry.
	if _, err := p.Stats(context.Background(), 30); err != nil {
		t.Fatalf("Stats(30): %v", err)
	}
	if f.calls.Load() != 4 {
		t.Errorf("calls after a second window = %d, want 4", f.calls.Load())
	}
	clock = now.Add(statsCacheTTL + time.Second)
	if _, err := p.Stats(context.Background(), 7); err != nil {
		t.Fatalf("expired Stats: %v", err)
	}
	if f.calls.Load() != 6 {
		t.Errorf("calls after expiry = %d, want 6", f.calls.Load())
	}
}

func TestProviderStatsPageCapReportsAFloor(t *testing.T) {
	f := &providerFake{t: t, pages: map[string]string{}}
	// Every page hands out another cursor, so only the cap stops the walk.
	for i := 0; i <= statsMaxPages; i++ {
		cursor := ""
		if i > 0 {
			cursor = "c" + string(rune('a'+i))
		}
		next := "c" + string(rune('a'+i+1))
		f.pages[cursor] = `{"data":[{"id":"` + next + `","media_type":"movie","media_id":"m","media_title":"Heat","user":{"id":"u","username":"kylo"}}],"meta":{"nextCursor":"` + next + `","pageSize":100}}`
	}
	p := f.provider(time.Now)
	stats, err := p.Stats(context.Background(), 30)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if f.calls.Load() != statsMaxPages {
		t.Errorf("calls = %d, want the %d-page cap", f.calls.Load(), statsMaxPages)
	}
	if !stats.Coverage.Truncated || stats.Coverage.Plays != statsMaxPages || !strings.Contains(stats.Coverage.Note, "counts are a floor") {
		t.Errorf("coverage = %+v, want truncation reported as a floor", stats.Coverage)
	}
}

func TestProviderStatsEmptyWindowSaysWhatWasSearched(t *testing.T) {
	f := &providerFake{t: t, pages: map[string]string{"": `{"data":[],"meta":{"nextCursor":null,"pageSize":100}}`}}
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	p := f.provider(func() time.Time { return now })
	stats, err := p.Stats(context.Background(), 7)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TopMovies == nil || stats.TopShows == nil || stats.TopUsers == nil || len(stats.TopUsers) != 0 {
		t.Errorf("stats = %+v, want empty non-nil buckets", stats)
	}
	if !strings.Contains(stats.Coverage.Note, "No plays recorded by Tracearr since 25 Aug 2026") || !strings.Contains(stats.Coverage.Note, "nothing older than that was searched") {
		t.Errorf("note = %q, want the empty-window explanation", stats.Coverage.Note)
	}
}

func TestProviderStatsErrorIsNotCachedAsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	p := newProvider(NewClient(srv.URL, testToken), time.Now)
	if _, err := p.Stats(context.Background(), 7); err == nil {
		t.Fatal("want an error from a broken upstream")
	}
	if _, cached := p.stats[7]; cached {
		t.Error("a failed walk must not populate the cache")
	}
}
