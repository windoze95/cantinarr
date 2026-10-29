package request

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// Sonarr can omit a season that TMDB has announced but has not aired. That
// absence must not obscure the files already verified for the aired season,
// while missing or contradictory evidence must still produce unknown status.
func TestTVCardStatusMissingFutureSeason(t *testing.T) {
	for _, tc := range []struct {
		name          string
		seasonAirDate string
		episodeCount  int
		episodeDates  []string
		seasonDown    bool
		seasonPayload string
		episodesDown  bool
		nativeEpisode bool
		customMatch   bool
		wantKnown     bool
	}{
		{name: "announced empty undated season", wantKnown: true},
		{name: "announced empty future season", seasonAirDate: "2099-01-01", wantKnown: true},
		{name: "future episodes still require target mapping", seasonAirDate: "2099-01-01", episodeCount: 2, episodeDates: []string{"2099-01-01", "2099-01-08"}},
		{name: "empty aired season is not a placeholder", seasonAirDate: "2020-01-01"},
		{name: "malformed air date", seasonAirDate: "not-a-date"},
		{name: "missing aired season", seasonAirDate: "2020-01-01", episodeCount: 1, episodeDates: []string{"2020-01-01"}},
		{name: "unknown episode date", episodeCount: 1, episodeDates: []string{""}},
		{name: "zero summary count contradicts returned episode", episodeDates: []string{""}},
		{name: "future season contradicts aired episode", seasonAirDate: "2099-01-01", episodeCount: 1, episodeDates: []string{"2020-01-01"}},
		{name: "incomplete episode response", seasonAirDate: "2099-01-01", episodeCount: 1},
		{name: "unreadable empty season", seasonDown: true},
		{name: "missing episode list", seasonPayload: `{"id":202,"season_number":2}`},
		{name: "null episode list", seasonPayload: `{"id":202,"season_number":2,"episodes":null}`},
		{name: "wrong season identity", seasonPayload: `{"id":203,"season_number":3,"episodes":[]}`},
		{name: "unreadable Sonarr episodes", episodesDown: true},
		{name: "native episode contradicts empty placeholder", nativeEpisode: true},
		{name: "custom mapping still needs target season", seasonAirDate: "2099-01-01", customMatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutations := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet {
					mutations++
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
				switch r.URL.Path {
				case "/tv/200/external_ids":
					write(map[string]any{"tvdb_id": 455755})
				case "/tv/200":
					write(map[string]any{"id": 200, "name": "Aired and announced seasons", "first_air_date": "2020-01-01", "seasons": []map[string]any{
						{"season_number": 1, "episode_count": 8, "air_date": "2020-01-01"},
						{"season_number": 2, "episode_count": tc.episodeCount, "air_date": tc.seasonAirDate},
					}})
				case "/tv/200/season/2":
					if tc.seasonDown {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					if tc.seasonPayload != "" {
						_, _ = w.Write([]byte(tc.seasonPayload))
						return
					}
					episodes := []map[string]any{}
					for n, date := range tc.episodeDates {
						episodes = append(episodes, map[string]any{"id": 20000 + n, "season_number": 2, "episode_number": n + 1, "air_date": date})
					}
					write(map[string]any{"id": 202, "season_number": 2, "episodes": episodes})
				case "/api/v3/series":
					write([]map[string]any{{"id": 283, "tvdbId": 455755, "title": "Aired and announced seasons", "monitored": true, "seasons": []map[string]any{{"seasonNumber": 1, "monitored": true}}}})
				case "/api/v3/series/lookup":
					write([]map[string]any{{"tvdbId": 455755, "title": "Aired and announced seasons", "seasons": []map[string]any{{"seasonNumber": 1}}}})
				case "/api/v3/episode":
					if tc.episodesDown {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					episodes := []map[string]any{}
					for n := 1; n <= 8; n++ {
						episodes = append(episodes, map[string]any{"id": n, "seriesId": 283, "seasonNumber": 1, "episodeNumber": n, "airDateUtc": "2020-01-01T00:00:00Z", "hasFile": true, "episodeFileId": n, "monitored": true})
					}
					if tc.nativeEpisode {
						episodes = append(episodes, map[string]any{"id": 9, "seriesId": 283, "seasonNumber": 2, "episodeNumber": 1, "airDateUtc": "2099-01-01T00:00:00Z", "hasFile": false})
					}
					write(episodes)
				default:
					t.Errorf("unexpected status read: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(upstream.Close)
			s, uid := newHistoryTestService(t, "", upstream.URL, "")
			s.bridge = tmdb.NewBridge(bridgeClients{tmdb.NewClientWithBaseURL("fixture", upstream.URL)}, s.db)
			if tc.customMatch {
				_, err := s.db.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(200,'custom',455755,'{"1":1,"2":2}',1)`)
				if err != nil {
					t.Fatal(err)
				}
			}
			out, err := s.tvLiveStatus(uid, 200, "")
			if err != nil || out == nil || out.StatusKnown == nil {
				t.Fatalf("status: %+v, %v", out, err)
			}
			if *out.StatusKnown != tc.wantKnown {
				t.Fatalf("status_known=%v, want %v: %+v", *out.StatusKnown, tc.wantKnown, out)
			}
			if tc.wantKnown {
				if out.Status != StatusAvailable || out.Progress != 1 || out.StatusUnknownReason != "" || len(out.Seasons) != 2 || out.Seasons[0].EpisodeFileCount != 8 || out.Seasons[0].EpisodeCount != 8 {
					t.Fatalf("verified aired content was obscured: %+v", out)
				}
			} else if out.StatusUnknownReason != "tv_seasons_unmapped" && !(tc.episodesDown && out.StatusUnknownReason == "library_unavailable") {
				t.Fatalf("missing evidence did not preserve mapping uncertainty: %+v", out)
			}
			if mutations != 0 {
				t.Fatal("status read mutated the library")
			}
		})
	}
}
