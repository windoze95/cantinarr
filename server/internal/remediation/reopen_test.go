package remediation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/db"
)

func TestReopenPreservesHistoryAndRequiresFreshManualReview(t *testing.T) {
	for _, status := range []string{IssueResolved, IssueWontFix, IssueDismissed, "failed"} {
		t.Run(status, func(t *testing.T) {
			svc, fx, issueID, actionID := approvalFixture(t)
			const note = "Checked the original replacement."
			if _, err := svc.ResolveIssueByAdmin(context.Background(), testAdminID, issueID, AdminDispositionResolved, note); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec("UPDATE issues SET status = ? WHERE id = ?", status, issueID); err != nil {
				t.Fatal(err)
			}
			before, _ := svc.IssueThread(issueID)
			issue, err := svc.ReopenIssueByAdmin(context.Background(), testAdminID, issueID)
			if err != nil {
				t.Fatal(err)
			}
			if issue.Status != IssueNeedsAdmin || issue.ClosedAt != nil || issue.Resolution != "" || issue.ResolutionKind != "" || !issue.Read {
				t.Fatalf("reopened issue = %+v", issue)
			}
			after, _ := svc.IssueThread(issueID)
			if len(after) != len(before)+1 || after[0].Body != before[0].Body {
				t.Fatalf("history changed: before=%+v after=%+v", before, after)
			}
			audit := after[len(after)-1]
			if audit.AuthorKind != AuthorAdmin || audit.AuthorName == nil || *audit.AuthorName != "admin" || !strings.Contains(audit.Body, "Reopened issue.") || !strings.Contains(audit.Body, note) || !strings.Contains(audit.Body, "Previous closure:") {
				t.Fatalf("reopen audit = %+v", audit)
			}
			action, _ := svc.GetAction(actionID)
			if action.Status != ActionSuperseded || action.CanDecide || fx.count() != 0 || len(svc.jobs) != 0 {
				t.Fatalf("old work revived: action=%+v executions=%d jobs=%d", action, fx.count(), len(svc.jobs))
			}
			if err := svc.PostReply(issueID, AuthorAdmin, testAdminID, "Still needs a look."); err != nil {
				t.Fatalf("reply after reopen: %v", err)
			}
			if err := svc.ConcludeIssue(context.Background(), issueID, IssueResolved, "Old automated result"); err != nil {
				t.Fatal(err)
			}
			if closed, _ := svc.GetIssue(issueID); closed.ClosedAt != nil {
				t.Fatal("background conclusion closed the reopened issue")
			}
			if count, err := svc.OpenIssueCount(); err != nil || count != 1 {
				t.Fatalf("attention count = %d, %v", count, err)
			}
			if err := svc.DismissIssue(issueID); err != nil {
				t.Fatalf("manual close after reopening: %v", err)
			}
			if _, err := svc.ReopenIssueByAdmin(context.Background(), testAdminID, issueID); err != nil {
				t.Fatalf("second lifecycle: %v", err)
			}
		})
	}
}

func TestReopenConflictsAreAtomic(t *testing.T) {
	for _, scenario := range []string{"already open", "executing", "unknown status"} {
		t.Run(scenario, func(t *testing.T) {
			svc, _, issueID, actionID := approvalFixture(t)
			if scenario != "already open" {
				if err := svc.DismissIssue(issueID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "executing" {
				if _, err := svc.db.Exec("UPDATE agent_actions SET status = ? WHERE id = ?", ActionExecuting, actionID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "unknown status" {
				if _, err := svc.db.Exec("UPDATE issues SET status = 'future_state' WHERE id = ?", issueID); err != nil {
					t.Fatal(err)
				}
			}
			_, err := svc.ReopenIssueByAdmin(context.Background(), testAdminID, issueID)
			var conflict *IssueReopenConflict
			if !errors.As(err, &conflict) {
				t.Fatalf("reopen error = %v", err)
			}
			var reopened bool
			if err := svc.db.QueryRow("SELECT reopened_at IS NOT NULL FROM issues WHERE id = ?", issueID).Scan(&reopened); err != nil || reopened {
				t.Fatalf("failed reopen left a marker: %v, %v", reopened, err)
			}
			thread, _ := svc.IssueThread(issueID)
			if len(thread) != 0 {
				t.Fatalf("failed reopen changed history: %+v", thread)
			}
		})
	}
}

func TestReopenMatchesCreationDuplicateBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, change string
		conflict     bool
	}{
		{"same report", "", true},
		{"other instance", "instance_id = 'sonarr-other'", false},
		{"other episode", "episode_number = 5", false},
		{"other category", "category = 'bad_copy'", false},
		{"other reporter", "reporter_id = NULL", false},
		{"other media", "media_type = 'movie'", false},
		{"other title", "tmdb_id = 99, tvdb_id = 99, book_id = 99", false},
		{"TVDB identity", "tmdb_id = 0", true},
		{"book identity", "tmdb_id = 0, tvdb_id = 0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, reporterID := setupTestService(t)
			res, err := svc.db.Exec(`INSERT INTO issues
				(source,status,media_type,tmdb_id,tvdb_id,book_id,instance_id,season_number,episode_number,category,reporter_id,closed_at)
				VALUES ('user','dismissed','tv',42,43,44,'sonarr-test',2,4,'wrong_audio',?,CURRENT_TIMESTAMP)`, reporterID)
			if err != nil {
				t.Fatal(err)
			}
			originalID, _ := res.LastInsertId()
			res, err = svc.db.Exec(`INSERT INTO issues
				(source,status,media_type,tmdb_id,tvdb_id,book_id,instance_id,season_number,episode_number,category,reporter_id)
				SELECT source,'open',media_type,tmdb_id,tvdb_id,book_id,instance_id,season_number,episode_number,category,reporter_id
				FROM issues WHERE id = ?`, originalID)
			if err != nil {
				t.Fatal(err)
			}
			existingID, _ := res.LastInsertId()
			if tc.change != "" {
				if _, err := svc.db.Exec("UPDATE issues SET "+tc.change+" WHERE id = ?", existingID); err != nil {
					t.Fatal(err)
				}
			}
			_, err = svc.ReopenIssueByAdmin(context.Background(), reporterID, originalID)
			var conflict *IssueReopenConflict
			if tc.conflict {
				if !errors.As(err, &conflict) || conflict.ExistingIssueID != existingID {
					t.Fatalf("duplicate error = %v", err)
				}
				original, _ := svc.GetIssue(originalID)
				if original.ClosedAt == nil {
					t.Fatal("duplicate refusal reopened the original")
				}
			} else if err != nil {
				t.Fatalf("distinct report blocked: %v", err)
			}
		})
	}
}

func TestReopenAutoDedupeConflict(t *testing.T) {
	svc, _, issueID, _ := approvalFixture(t)
	if err := svc.DismissIssue(issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec("UPDATE issues SET source = 'auto', dedupe_key = 'same-incident' WHERE id = ?", issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO issues (source,status,media_type,tmdb_id,dedupe_key)
		VALUES ('auto','observing','movie',42,'same-incident')`); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ReopenIssueByAdmin(context.Background(), testAdminID, issueID)
	var conflict *IssueReopenConflict
	if !errors.As(err, &conflict) || conflict.ExistingIssueID == 0 {
		t.Fatalf("auto duplicate error = %v", err)
	}
}

func TestConcurrentReopenRecordsOneTransition(t *testing.T) {
	svc, _, issueID, _ := approvalFixture(t)
	if err := svc.DismissIssue(issueID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.ReopenIssueByAdmin(context.Background(), testAdminID, issueID)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			var conflict *IssueReopenConflict
			if !errors.As(err, &conflict) {
				t.Fatal(err)
			}
		}
	}
	thread, _ := svc.IssueThread(issueID)
	if successes != 1 || len(thread) != 1 {
		t.Fatalf("successes=%d thread=%+v", successes, thread)
	}
}

func TestReopenedHealthIssuesStayInManualReview(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record func(*Service, bool) error
	}{
		{"AI", func(s *Service, healthy bool) error { return s.RecordSharedAIHealth("test", "test", healthy) }},
		{"push", func(s *Service, healthy bool) error { return s.RecordPushDeliveryHealth(healthy, "test failure") }},
		{"books", func(s *Service, healthy bool) error {
			return s.RecordBookImportStall("book-test", "Books", []string{"A book"}, healthy)
		}},
		{"provider", func(s *Service, healthy bool) error { return s.RecordRemediationProviderHealth(healthy) }},
		{"breaker", func(s *Service, healthy bool) error { return s.RecordAutoDispatchBreaker(!healthy, 5, 5) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, adminID := setupTestService(t)
			if err := tc.record(svc, false); err != nil {
				t.Fatal(err)
			}
			issues, _, err := svc.ListIssues("", 0)
			if err != nil || len(issues) != 1 {
				t.Fatalf("issues=%+v err=%v", issues, err)
			}
			id := issues[0].ID
			if err := tc.record(svc, true); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.ReopenIssueByAdmin(context.Background(), adminID, id); err != nil {
				t.Fatal(err)
			}
			before, _ := svc.IssueThread(id)
			for _, healthy := range []bool{true, false, true} {
				if err := tc.record(svc, healthy); err != nil {
					t.Fatal(err)
				}
			}
			issue, _ := svc.GetIssue(id)
			after, _ := svc.IssueThread(id)
			if issue.Status != IssueNeedsAdmin || issue.ClosedAt != nil || len(after) != len(before) {
				t.Fatalf("background processing changed manual review: %+v", issue)
			}
		})
	}
}

func TestReopenedReportPreventsDuplicateAutoIncidentsAndStaysManual(t *testing.T) {
	svc, _, adminID := setupObservationService(t, false)
	settings := Defaults()
	settings.Enabled, settings.AutoDispatch = true, true
	if _, err := svc.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateUserIssue(adminID, &CreateIssueRequest{
		InstanceID: "radarr-observe", MediaType: "movie", TmdbID: 42,
		Title: "Example", Category: CategoryBadCopy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DismissIssue(created.IssueID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReopenIssueByAdmin(context.Background(), adminID, created.IssueID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	records, err := svc.loadObservationRecords("radarr", "radarr-observe", now)
	if err != nil || len(records) != 1 || !records[0].manualReview {
		t.Fatalf("observations=%+v err=%v", records, err)
	}
	item := observedProblem("download-1", 1, 100)
	item.Media.Title, item.Media.TmdbID = "Example", 42
	for _, snapshot := range [][]arr.QueueObservation{{item}, nil, {item}} {
		now = now.Add(time.Minute)
		if err := svc.observeQueueSnapshot("radarr", "radarr-observe", snapshot, now); err != nil {
			t.Fatal(err)
		}
	}
	issues, _, err := svc.ListIssues("", 0)
	if err != nil || len(issues) != 1 || issues[0].ID != created.IssueID || issues[0].Status != IssueNeedsAdmin {
		t.Fatalf("issues after observation=%+v err=%v", issues, err)
	}
}

func TestReopenMigrationAndManualReviewSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issues.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-feature schema to exercise the in-code ALTER migration.
	if _, err := database.Exec("ALTER TABLE issues DROP COLUMN reopened_at"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	adminID := seedUser(t, database, "admin")
	res, err := database.Exec(`INSERT INTO issues (source,status,media_type,tmdb_id,closed_at)
		VALUES ('user','resolved','movie',42,CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	svc := NewService(database, nil, nil, nil)
	if _, err := svc.ReopenIssueByAdmin(context.Background(), adminID, id); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	svc = NewService(database, nil, nil, nil)
	if err := svc.ConcludeIssue(context.Background(), id, IssueResolved, "Old recovery"); err != nil {
		t.Fatal(err)
	}
	issue, err := svc.GetIssue(id)
	if err != nil || issue.ClosedAt != nil || issue.Status != IssueNeedsAdmin {
		t.Fatalf("after restart: %+v %v", issue, err)
	}
	if _, err := svc.ResolveIssueByAdmin(context.Background(), adminID, id, AdminDispositionResolved, ""); err != nil {
		t.Fatal(err)
	}
}

func TestReopenRetiresLegacyLiveWork(t *testing.T) {
	svc, fx, issueID, actionID := approvalFixture(t)
	// A legacy non-atomic close may have left its proposal and parked run live.
	if _, err := svc.db.Exec("UPDATE issues SET status = 'dismissed', closed_at = CURRENT_TIMESTAMP WHERE id = ?", issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReopenIssueByAdmin(context.Background(), testAdminID, issueID); err != nil {
		t.Fatal(err)
	}
	action, err := svc.GetAction(actionID)
	if err != nil || action.Status != ActionSuperseded || action.CanDecide || fx.count() != 0 {
		t.Fatalf("legacy action=%+v err=%v", action, err)
	}
	var runStatus string
	if err := svc.db.QueryRow("SELECT status FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runStatus); err != nil || runStatus != "aborted" {
		t.Fatalf("legacy run=%s err=%v", runStatus, err)
	}
}
