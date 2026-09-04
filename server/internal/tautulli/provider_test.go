package tautulli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// providerFake answers Tautulli's cmd-dispatched API with canned data.
func providerFake(t *testing.T, data map[string]string) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cmd := r.URL.Query().Get("cmd")
		body, ok := data[cmd]
		if !ok {
			t.Errorf("unexpected cmd %q", cmd)
			body = "null"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"result":"success","message":null,"data":` + body + `}}`))
	}))
	t.Cleanup(srv.Close)
	return NewProvider(srv.URL, "key")
}

func TestProviderServerInfo(t *testing.T) {
	p := providerFake(t, map[string]string{"get_server_info": `{"pms_name":"Cantina","pms_version":"1.41.0"}`})
	info, err := p.ServerInfo(context.Background())
	if err != nil {
		t.Fatalf("ServerInfo: %v", err)
	}
	if info.Name != "Cantina" || info.Version != "1.41.0" || len(info.Servers) != 0 {
		t.Errorf("info = %+v, want pms name/version and no server list", info)
	}
}

func TestProviderActivityMapsVocabularyAndMarksPlex(t *testing.T) {
	p := providerFake(t, map[string]string{"get_activity": `{
		"stream_count": "1",
		"total_bandwidth": "9500",
		"sessions": [{"user":"julian","title":"Heat","full_title":"Heat (1995)","player":"Living Room TV","product":"Plex for Apple TV","state":"playing","progress_percent":"42","quality_profile":"1080p","transcode_decision":"transcode","bandwidth":"9500","media_type":"movie"}]
	}`})
	activity, err := p.Activity(context.Background())
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if activity.StreamCount != 1 || activity.TotalBandwidthKbps != 9500 || len(activity.Streams) != 1 {
		t.Fatalf("activity = %+v, want one stream and 9500 kbps", activity)
	}
	s := activity.Streams[0]
	if s.Quality != "1080p" || s.StreamType != "transcode" || s.BandwidthKbps != 9500 || s.ProgressPercent != 42 {
		t.Errorf("stream = %+v, want quality_profile/transcode_decision/bandwidth mapped", s)
	}
	if s.MediaType != "movie" || s.ServerType != "plex" || s.Server != "" {
		t.Errorf("stream = %+v, want media_type passed through, server_type plex, no server name", s)
	}
}

func TestProviderHistoryRendersUnixDatesAsUTC(t *testing.T) {
	p := providerFake(t, map[string]string{"get_history": `{"data":[
		{"user":"julian","full_title":"Heat (1995)","date":"1720000000","duration":"3600","percent_complete":87,"player":"TV","platform":"tvOS"},
		{"user":"dex","full_title":"Andor - S02E03","date":0,"duration":600,"percent_complete":10,"player":"phone","platform":"iOS"}
	]}`})
	history, err := p.History(context.Background(), 50)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(history.Items))
	}
	want := time.Date(2024, 7, 3, 9, 46, 40, 0, time.UTC)
	if !history.Items[0].Date.Equal(want) || history.Items[0].Date.Location() != time.UTC {
		t.Errorf("date = %v, want %v in UTC", history.Items[0].Date, want)
	}
	if history.Items[0].DurationSeconds != 3600 || history.Items[0].PercentComplete != 87 || history.Items[0].ServerType != "plex" {
		t.Errorf("item[0] = %+v, want numbers mapped and plex marked", history.Items[0])
	}
	if !history.Items[1].Date.IsZero() {
		t.Errorf("unknown date = %v, want zero", history.Items[1].Date)
	}
	if history.Coverage.Plays != 2 || !strings.Contains(history.Coverage.Note, "2 most recent plays Tautulli recorded") {
		t.Errorf("coverage = %+v, want the row count and a Tautulli note", history.Coverage)
	}
}

func TestProviderStatsBucketsBlocksByStatID(t *testing.T) {
	p := providerFake(t, map[string]string{"get_home_stats": `[
		{"stat_id":"top_movies","rows":[{"title":"Heat","total_plays":"9"}]},
		{"stat_id":"top_tv","rows":[{"title":"Andor","total_plays":5}]},
		{"stat_id":"top_users","rows":[{"user":"julian","friendly_name":"Julian","total_plays":4},{"user":"dex","friendly_name":"","total_plays":2}]},
		{"stat_id":"top_platforms","rows":[{"title":"tvOS","total_plays":99}]}
	]`})
	stats, err := p.Stats(context.Background(), 7)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if len(stats.TopMovies) != 1 || stats.TopMovies[0].Title != "Heat" || stats.TopMovies[0].Plays != 9 {
		t.Errorf("top movies = %+v, want Heat with 9 plays", stats.TopMovies)
	}
	if len(stats.TopShows) != 1 || stats.TopShows[0].Title != "Andor" {
		t.Errorf("top shows = %+v, want top_tv bucketed as shows", stats.TopShows)
	}
	if len(stats.TopUsers) != 2 || stats.TopUsers[0].User != "Julian" || stats.TopUsers[1].User != "dex" {
		t.Errorf("top users = %+v, want friendly-name fallback applied and top_platforms dropped", stats.TopUsers)
	}
	if !strings.Contains(stats.Coverage.Note, "last 7 days") || stats.Coverage.Since.IsZero() || stats.Coverage.Until.IsZero() {
		t.Errorf("coverage = %+v, want the window named", stats.Coverage)
	}
	if got := stats.Coverage.Until.Sub(stats.Coverage.Since); got < 7*24*time.Hour-time.Minute || got > 7*24*time.Hour+time.Minute {
		t.Errorf("window = %v, want seven days", got)
	}
}

func TestProviderStatsEmptyBucketsAreNonNil(t *testing.T) {
	p := providerFake(t, map[string]string{"get_home_stats": `[]`})
	stats, err := p.Stats(context.Background(), 30)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TopMovies == nil || stats.TopShows == nil || stats.TopUsers == nil {
		t.Errorf("stats = %+v, want empty non-nil buckets", stats)
	}
}
