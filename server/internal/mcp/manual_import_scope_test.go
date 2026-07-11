package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

func TestScopedManualImportNeverDispatchesUnrelatedEpisodeCandidate(t *testing.T) {
	var imported []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/queue":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"totalRecords": 1,
				"records": []map[string]any{{
					"id": 7, "seriesId": 1, "episodeId": 27, "downloadId": "download-7", "protocol": "usenet",
					"series":  map[string]any{"id": 1, "tmdbId": 42},
					"episode": map[string]any{"id": 27, "seasonNumber": 2, "episodeNumber": 7},
				}},
			})
		case "/api/v3/manualimport":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"name": "matching.mkv", "path": "/matching.mkv", "downloadId": "download-7",
					"series":   map[string]any{"id": 1},
					"episodes": []map[string]any{{"id": 27, "seasonNumber": 2, "episodeNumber": 7}},
				},
				{
					"name": "unrelated.mkv", "path": "/unrelated.mkv", "downloadId": "download-7",
					"series":   map[string]any{"id": 99},
					"episodes": []map[string]any{{"id": 997, "seasonNumber": 2, "episodeNumber": 7}},
				},
			})
		case "/api/v3/command":
			var payload struct {
				Files []map[string]any `json:"files"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode command: %v", err)
			}
			imported = payload.Files
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := ExecuteManualImportHelper(nil, sonarr.NewClient(server.URL, "test"), nil,
		"tv", 7, true, &ManualImportScope{
			DownloadID: "download-7", SeriesID: 1, SeasonNumber: 2, EpisodeNumber: 7,
		})
	if err != nil {
		t.Fatalf("ExecuteManualImportHelper: %v", err)
	}
	if len(imported) != 1 || imported[0]["path"] != "/matching.mkv" {
		t.Fatalf("imported files = %#v, want only matching.mkv", imported)
	}
	if result == "" {
		t.Fatal("empty result")
	}
}

func TestScopedManualImportWithOnlyUnrelatedCandidatesFailsBeforeDispatch(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/queue":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"totalRecords": 1,
				"records": []map[string]any{{
					"id": 7, "seriesId": 1, "downloadId": "download-7",
				}},
			})
		case "/api/v3/manualimport":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"name": "unrelated.mkv", "series": map[string]any{"id": 99},
				"episodes": []map[string]any{{"id": 997, "seasonNumber": 2, "episodeNumber": 7}},
			}})
		case "/api/v3/command":
			posts++
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := ExecuteManualImportHelper(nil, sonarr.NewClient(server.URL, "test"), nil,
		"tv", 7, true, &ManualImportScope{DownloadID: "download-7", SeriesID: 1, SeasonNumber: 2, EpisodeNumber: 7})
	if err == nil {
		t.Fatal("unrelated-only candidates were accepted")
	}
	if safe, ok := err.(interface{ MutationNotStarted() bool }); !ok || !safe.MutationNotStarted() {
		t.Fatalf("scope error is not marked pre-dispatch: %T %v", err, err)
	}
	if posts != 0 {
		t.Fatalf("manual import dispatched %d command(s)", posts)
	}
}
