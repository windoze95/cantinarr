package tmdb

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEpisodeCatalogRejectsUnstableIdentity(t *testing.T) {
	for name, body := range map[string]string{
		"wrong season":     `{"season_number":2,"episodes":[]}`,
		"missing ID":       `{"season_number":1,"episodes":[{"season_number":1,"episode_number":1}]}`,
		"duplicate number": `{"season_number":1,"episodes":[{"id":10,"season_number":1,"episode_number":1},{"id":11,"season_number":1,"episode_number":1}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer srv.Close()
			if _, err := NewClientWithBaseURL("key", srv.URL).GetTVSeason(200, 1); err == nil {
				t.Fatal("unstable canonical episode identity accepted")
			}
		})
	}
}
func TestEpisodeFindKeepsShowAndSpecialsIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/find/123" || r.URL.Query().Get("external_source") != "tvdb_id" {
			t.Errorf("incorrect external-ID lookup %s", r.URL.String())
		}
		w.Write([]byte(`{"tv_episode_results":[{"id":456,"show_id":200,"season_number":0}]}`))
	}))
	defer srv.Close()
	show, id, season, err := NewClientWithBaseURL("key", srv.URL).FindTVEpisode(123)
	if err != nil || show != 200 || id != 456 || season == nil || *season != 0 {
		t.Fatalf("special identity lost: %d %d %v %v", show, id, season, err)
	}
}
