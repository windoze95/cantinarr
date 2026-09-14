package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

func TestTVQueueReplacementAirDateGuard(t *testing.T) {
	future := time.Now().UTC().Add(72 * time.Hour)
	nearFuture := time.Now().UTC().Add(10 * time.Minute)
	past := time.Now().UTC().Add(-72 * time.Hour)
	for _, tc := range []struct {
		name        string
		air         *time.Time
		action      string
		sibling     bool
		historyOnly bool
		badHistory  bool
		badIdentity bool
		readError   bool
		guardError  bool
		wantSkip    bool
		wantError   bool
	}{
		{name: "unaired without a library copy", air: &future, wantSkip: true},
		{name: "ten minutes before air still suppresses search", air: &nearFuture, wantSkip: true},
		{name: "aired uses Sonarr policy", air: &past},
		{name: "unknown date is not evidence of unaired"},
		{name: "explicit blocklist only never upgrades", air: &past, action: "blocklist_only", wantSkip: true},
		{name: "mixed pack queue sibling", air: &past, sibling: true, wantSkip: true},
		{name: "pack sibling already left queue", air: &past, historyOnly: true, wantSkip: true},
		{name: "truncated history cannot authorize search", air: &future, badHistory: true, wantError: true},
		{name: "mismatched episode identity", air: &future, badIdentity: true, wantError: true},
		{name: "episode read failed", air: &future, readError: true, wantError: true},
		{name: "audit must persist before mutation", air: &future, guardError: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queue := []map[string]any{{"id": 8, "seriesId": 3, "episodeId": 55, "downloadId": "download-8"}}
			episodes := []sonarr.Episode{{ID: 55, SeriesID: 3, SeasonNumber: 0, EpisodeNumber: 7, AirDateUtc: tc.air}}
			history := []sonarr.HistoryRecord{{ID: 1, EpisodeID: 55, SeriesID: 3, EventType: "grabbed", DownloadID: "download-8"}}
			if tc.sibling || tc.historyOnly {
				episodes = append(episodes, sonarr.Episode{ID: 56, SeriesID: 3, SeasonNumber: 0, EpisodeNumber: 8, AirDateUtc: &future})
				if tc.sibling {
					queue = append(queue, map[string]any{"id": 9, "seriesId": 3, "episodeId": 56, "downloadId": "download-8"})
				} else {
					history = append(history, sonarr.HistoryRecord{ID: 2, EpisodeID: 56, SeriesID: 3, EventType: "grabbed", DownloadID: "download-8"})
				}
			}
			if tc.badIdentity {
				episodes[0].SeriesID = 4
			}
			var mu sync.Mutex
			var mutations []string
			guardCalled := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "GET" {
					mutations = append(mutations, r.Method+" "+r.URL.RequestURI())
					if !guardCalled {
						t.Error("mutation reached Sonarr before effective action was recorded")
					}
					_, _ = w.Write([]byte(`{}`))
					return
				}
				switch r.URL.Path {
				case "/api/v3/queue":
					_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": len(queue), "records": queue})
				case "/api/v3/episode":
					if tc.readError {
						w.WriteHeader(503)
						return
					}
					_ = json.NewEncoder(w).Encode(episodes)
				case "/api/v3/history":
					if r.URL.Query().Get("downloadId") != "download-8" || r.URL.Query().Get("eventType") != "1" {
						t.Error("history read was not scoped to this download's grabs")
					}
					total := len(history)
					if tc.badHistory {
						total++
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": total, "records": history})
				default:
					t.Errorf("unexpected read: %s", r.URL)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			action := tc.action
			if action == "" {
				action = "blocklist_search"
			}
			result, err := RemediateQueueItemHelper(nil, sonarr.NewClient(srv.URL, "key"), nil, nil, "tv", 8, action, func(effective string, _ *sonarr.DetailedQueueItem) error {
				mu.Lock()
				defer mu.Unlock()
				guardCalled = true
				if (effective == "blocklist_only") != tc.wantSkip && !tc.wantError {
					t.Errorf("effective action = %s", effective)
				}
				if tc.guardError {
					return errors.New("audit write failed")
				}
				return nil
			})
			mu.Lock()
			defer mu.Unlock()
			if tc.wantError {
				var before interface{ MutationNotStarted() bool }
				if err == nil || !errors.As(err, &before) || !before.MutationNotStarted() || len(mutations) != 0 {
					t.Fatalf("expected no mutation: err=%v mutations=%v", err, mutations)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			flag := "false"
			if tc.wantSkip {
				flag = "true"
			}
			want := "DELETE /api/v3/queue/8?removeFromClient=true&blocklist=true&skipRedownload=" + flag + "&changeCategory=false"
			if len(mutations) != 1 || mutations[0] != want {
				t.Fatalf("mutations = %v, want only %s", mutations, want)
			}
			if tc.wantSkip && !strings.Contains(result, "without searching for a replacement") {
				t.Fatalf("misleading result: %s", result)
			}
			if tc.wantSkip && tc.action != "blocklist_only" && !strings.Contains(result, "has not aired yet") {
				t.Fatalf("missing suppression reason: %s", result)
			}
		})
	}
}

func TestMCPRemediationSuppressesUnairedReplacement(t *testing.T) {
	var mu sync.Mutex
	var deleteURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "DELETE":
			deleteURI = r.URL.RequestURI()
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/api/v3/queue":
			_, _ = w.Write([]byte(`{"totalRecords":1,"records":[{"id":8,"seriesId":3,"episodeId":55}]}`))
		case r.URL.Path == "/api/v3/episode":
			future := time.Now().UTC().Add(48 * time.Hour)
			_ = json.NewEncoder(w).Encode([]sonarr.Episode{{ID: 55, SeriesID: 3, SeasonNumber: 22, EpisodeNumber: 12, AirDateUtc: &future}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	server := newDefaultInstanceToolServer(t, map[string]string{"sonarr": srv.URL})
	_, err := server.ExecuteTool(context.Background(), "remediate_queue_item", json.RawMessage(`{"media_type":"tv","queue_id":8,"action":"blocklist_search"}`), adminCallContext())
	mu.Lock()
	defer mu.Unlock()
	if err != nil || !strings.Contains(deleteURI, "skipRedownload=true") {
		t.Fatalf("err=%v delete=%s", err, deleteURI)
	}
}
