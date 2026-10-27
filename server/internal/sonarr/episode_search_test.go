package sonarr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestExactEpisodeSearchReceiptAndReconciliation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v3/command" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			posts++
			var body struct {
				Name string `json:"name"`
				IDs  []int  `json:"episodeIds"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Name != "EpisodeSearch" || !reflect.DeepEqual(body.IDs, []int{91}) {
				t.Errorf("broad search: %+v", body)
			}
			w.Write([]byte(`{"id":19}`))
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "name": "EpisodeSearch", "queued": now.Add(-time.Hour), "body": map[string]any{"episodeIds": []int{91}}},
			{"id": 2, "name": "EpisodeSearch", "queued": now, "body": map[string]any{"episodeIds": []int{91, 92}}},
			{"id": 3, "name": "SeasonSearch", "queued": now, "body": map[string]any{"episodeIds": []int{91}}},
			{"id": 19, "name": "EpisodeSearch", "queued": now.Add(time.Second), "body": map[string]any{"episodeIds": []int{91}}},
		})
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "key")
	id, err := client.SubmitEpisodeSearch([]int{91})
	if err != nil || id != 19 {
		t.Fatalf("receipt: %d %v", id, err)
	}
	id, err = client.FindEpisodeSearch(91, now)
	if err != nil || id != 19 {
		t.Fatalf("exact matching receipt: %d %v", id, err)
	}
	id, err = client.FindEpisodeSearch(92, now)
	if err != nil || id != 0 {
		t.Fatalf("multi-episode/foreign command accepted: %d %v", id, err)
	}
	if _, err = client.SubmitEpisodeSearch([]int{0}); err == nil || posts != 1 {
		t.Fatal("invalid IDs reached provider")
	}
}
func TestEpisodeSearchWithoutReceiptIsUncertain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer srv.Close()
	if _, err := NewClient(srv.URL, "key").SubmitEpisodeSearch([]int{42}); err == nil {
		t.Fatal("missing receipt was acknowledged")
	}
}
