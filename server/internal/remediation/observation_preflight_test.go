package remediation

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// setupSeasonPackService serves a Sonarr instance whose queue holds one season
// pack: two episodes of the same series stuck at importPending under a single
// shared download id. Series/episode endpoints report neither episode has a
// file, so neither member can prove recovery.
func setupSeasonPackService(t *testing.T) *Service {
	t.Helper()
	queueRecord := func(queueID, episodeID, episodeNumber int) string {
		return fmt.Sprintf(`{"id":%d,"seriesId":2,"episodeId":%d,"title":"Tremors.S01.1080p",
		 "status":"completed","trackedDownloadStatus":"warning","trackedDownloadState":"importPending",
		 "statusMessages":[{"title":"Tremors.S01.1080p","messages":["One or more episodes expected in this release were not imported or missing"]}],
		 "downloadId":"pack-1","protocol":"usenet","size":1000,"sizeleft":0,"added":"2026-07-27T10:00:00Z",
		 "series":{"id":2,"title":"Tremors","tvdbId":100},
		 "episode":{"id":%d,"seriesId":2,"seasonNumber":1,"episodeNumber":%d,"hasFile":false,"episodeFileId":0}}`,
			queueID, episodeID, episodeID, episodeNumber)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/queue":
			fmt.Fprintf(w, `{"totalRecords":2,"records":[%s,%s]}`,
				queueRecord(11, 101, 1), queueRecord(12, 102, 2))
		case "/api/v3/series":
			fmt.Fprint(w, `[{"id":2,"title":"Tremors","tvdbId":100}]`)
		case "/api/v3/episode":
			fmt.Fprint(w, `[{"id":101,"seriesId":2,"seasonNumber":1,"episodeNumber":1,"hasFile":false,"episodeFileId":0},
			 {"id":102,"seriesId":2,"seasonNumber":1,"episodeNumber":2,"hasFile":false,"episodeFileId":0}]`)
		case "/api/v3/history":
			fmt.Fprint(w, `{"totalRecords":0,"records":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{0x37}, 32))
	store := instance.NewStore(database, cipher)
	if err := store.Create(&instance.Instance{
		ID: "sonarr-pack", ServiceType: "sonarr", Name: "TV", URL: server.URL, APIKey: "key",
	}); err != nil {
		t.Fatal(err)
	}
	return NewService(database, instance.NewRegistry(store), nil, &fakeNotifier{})
}

// Both reads must partition the same real Sonarr response identically: otherwise
// an unchanged promoted download bounces back to tracking on every preflight.
func TestPreflightKeepsPromotedSeasonDownloadActionable(t *testing.T) {
	svc := setupSeasonPackService(t)
	enableAutoDispatch(t, svc, 5)

	// Fetch through the real client so the sweep sees byte-identical
	// observations to the ones the preflight will re-fetch itself.
	items, err := svc.fetchQueueSnapshot("sonarr", "sonarr-pack")
	if err != nil || len(items) != 2 {
		t.Fatalf("queue snapshot = %d items, err=%v", len(items), err)
	}
	base := time.Now().UTC().Add(-30 * time.Minute)
	if err := svc.observeQueueSnapshot("sonarr", "sonarr-pack", items, base); err != nil {
		t.Fatal(err)
	}
	issues, _, _ := svc.ListIssues("", 0)
	if len(issues) != 1 {
		t.Fatalf("one shared download created %d issues", len(issues))
	}
	for _, issue := range issues {
		if issue.Status != IssueObserving {
			t.Fatalf("issue %d status = %s, want observing", issue.ID, issue.Status)
		}
	}
	if err := svc.observeQueueSnapshot("sonarr", "sonarr-pack", items, base.Add(16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	drainJobs(svc)

	for _, issue := range issues {
		promoted, err := svc.GetIssue(issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		if promoted.Status != IssueOpen {
			t.Fatalf("issue %d status = %s, want open after the observation window", issue.ID, promoted.Status)
		}
		var before string
		if err := svc.db.QueryRow("SELECT signature FROM issue_observations WHERE issue_id=?", issue.ID).Scan(&before); err != nil {
			t.Fatal(err)
		}

		recovering, err := svc.preflightArrRecovery(issue.ID)
		if err != nil {
			t.Fatalf("preflight issue %d: %v", issue.ID, err)
		}
		if recovering {
			t.Fatalf("issue %d: unchanged stuck import classified as arr recovery in flight", issue.ID)
		}
		after, err := svc.GetIssue(issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.Status != IssueOpen {
			t.Fatalf("issue %d bounced back to %s during preflight", issue.ID, after.Status)
		}
		var signature string
		if err := svc.db.QueryRow("SELECT signature FROM issue_observations WHERE issue_id=?", issue.ID).Scan(&signature); err != nil {
			t.Fatal(err)
		}
		if signature != before {
			t.Fatalf("issue %d: preflight rewrote the sweep's download signature", issue.ID)
		}
	}
}

func TestPassiveSeasonDownloadAlertsWithoutAgentWork(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("agent=%v", enabled), func(t *testing.T) {
			svc := setupSeasonPackService(t)
			settings := Defaults()
			settings.Enabled = enabled
			if _, err := svc.SetSettings(settings); err != nil {
				t.Fatal(err)
			}
			items, err := svc.fetchQueueSnapshot("sonarr", "sonarr-pack")
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now().UTC().Add(-20 * time.Minute)
			for _, elapsed := range []time.Duration{0, 11 * time.Minute, 15 * time.Minute} {
				if err := svc.observeQueueSnapshot("sonarr", "sonarr-pack", items, start.Add(elapsed)); err != nil {
					t.Fatal(err)
				}
				svc.flushIssueAlerts(start.Add(elapsed))
			}
			issues, _, err := svc.ListIssues("", 0)
			if err != nil || len(issues) != 1 || issues[0].Status != IssueOpen {
				t.Fatalf("promoted issues: %+v, %v", issues, err)
			}
			svc.recoverWork(&Runner{svc: svc, db: svc.db, procToken: "new-process"})
			if got := drainJobs(svc); got != 0 {
				t.Fatalf("passive incident scheduled %d jobs", got)
			}
			if events := svc.notifier.(*fakeNotifier).adminEvents; len(events) != 1 || events[0] != "issue_created" {
				t.Fatalf("admin events: %v", events)
			}
		})
	}
}
