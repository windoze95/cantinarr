package remediation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type testFileState struct {
	hasFile          bool
	fileID           int
	importDownloadID string
	importDate       time.Time
	historyFileID    int
	historyMovieID   int
}

func setupObservationService(t *testing.T, movieHasFile bool) (*Service, *fakeNotifier, int64) {
	state := &testFileState{hasFile: movieHasFile, fileID: 10}
	return setupObservationServiceWithState(t, state)
}

func setupObservationServiceWithState(t *testing.T, fileState *testFileState) (*Service, *fakeNotifier, int64) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/movie":
			fmt.Fprintf(w, `[{"id":1,"title":"Example","tmdbId":42,"hasFile":%t,"movieFileId":%d}]`, fileState.hasFile, fileState.fileID)
		case "/api/v3/series":
			fmt.Fprint(w, `[{"id":2,"title":"Series","tvdbId":100}]`)
		case "/api/v3/episode":
			if r.URL.Query().Get("seasonNumber") == "0" {
				fmt.Fprintf(w, `[{"id":4,"seriesId":2,"seasonNumber":0,"episodeNumber":1,"hasFile":%t,"episodeFileId":%d}]`, fileState.hasFile, fileState.fileID)
			} else {
				fmt.Fprint(w, `[{"id":3,"seriesId":2,"seasonNumber":1,"episodeNumber":2,"hasFile":false}]`)
			}
		case "/api/v3/queue":
			fmt.Fprint(w, `{"totalRecords":0,"records":[]}`)
		case "/api/v3/history":
			if fileState.importDownloadID == "" {
				fmt.Fprint(w, `{"records":[]}`)
			} else {
				historyFileID := fileState.historyFileID
				if historyFileID == 0 {
					historyFileID = fileState.fileID
				}
				historyMovieID := fileState.historyMovieID
				if historyMovieID == 0 {
					historyMovieID = 1
				}
				fmt.Fprintf(w, `{"totalRecords":1,"records":[{"id":77,"movieId":%d,"episodeId":4,"eventType":"downloadFolderImported","downloadId":%q,"date":%q,"data":{"FileId":%q},"movie":{"id":%d,"tmdbId":42},"series":{"tvdbId":100},"episode":{"id":4,"seasonNumber":0,"episodeNumber":1}}]}`,
					historyMovieID, fileState.importDownloadID, fileState.importDate.Format(time.RFC3339), fmt.Sprint(historyFileID), historyMovieID)
			}
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
	reporterID := seedUser(t, database, "observer")
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{0x37}, 32))
	store := instance.NewStore(database, cipher)
	for _, value := range []*instance.Instance{
		{ID: "radarr-observe", ServiceType: "radarr", Name: "Movies", URL: server.URL, APIKey: "key"},
		{ID: "sonarr-observe", ServiceType: "sonarr", Name: "TV", URL: server.URL, APIKey: "key"},
	} {
		if err := store.Create(value); err != nil {
			t.Fatal(err)
		}
	}
	notifier := &fakeNotifier{}
	return NewService(database, instance.NewRegistry(store), nil, notifier), notifier, reporterID
}

func TestUserIssueRequiresCanonicalMediaIdentity(t *testing.T) {
	svc, _, reporterID := setupObservationService(t, false)
	for _, req := range []*CreateIssueRequest{
		{InstanceID: "radarr-observe", MediaType: "movie", Category: CategoryOther},
		{InstanceID: "sonarr-observe", MediaType: "tv", Category: CategoryOther},
	} {
		if _, err := svc.CreateUserIssue(reporterID, req); err == nil {
			t.Fatalf("accepted identity-less report: %+v", req)
		}
	}
}

func TestTVDBOnlyReportsDoNotDedupeAcrossSeries(t *testing.T) {
	svc, _, reporterID := setupObservationService(t, false)
	makeReq := func(tvdbID int) *CreateIssueRequest {
		return &CreateIssueRequest{InstanceID: "sonarr-observe", MediaType: "tv", TvdbID: tvdbID,
			SeasonNumber: 1, EpisodeNumber: 1, Category: CategoryOther}
	}
	first, err := svc.CreateUserIssue(reporterID, makeReq(111))
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateUserIssue(reporterID, makeReq(222))
	if err != nil {
		t.Fatal(err)
	}
	if first.IssueID == second.IssueID {
		t.Fatalf("distinct TVDB series collapsed into issue %d", first.IssueID)
	}
	duplicate, err := svc.CreateUserIssue(reporterID, makeReq(111))
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.IssueID != first.IssueID {
		t.Fatalf("same TVDB scope did not dedupe: first=%d duplicate=%d", first.IssueID, duplicate.IssueID)
	}
}

func TestUserReportAdoptsMatchingSilentAutoObservation(t *testing.T) {
	svc, _, reporterID := setupObservationService(t, false)
	settings := Defaults()
	settings.Enabled = true
	settings.AutoDispatch = true
	if _, err := svc.SetSettings(settings); err != nil {
		t.Fatal(err)
	}

	item := observedProblem("download-1", 1, 100)
	item.Media.Title = "Example"
	item.Media.TmdbID = 42
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{item}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	before, err := svc.ListIssues("")
	if err != nil || len(before) != 1 || before[0].Source != SourceAuto || before[0].Status != IssueObserving {
		t.Fatalf("initial automatic observation=%+v err=%v", before, err)
	}

	created, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: "radarr-observe", MediaType: "movie", TmdbID: 42,
		Title: "Example", Category: CategoryBadCopy, Reason: "The copy glitches.",
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := svc.ListIssues("")
	if err != nil || len(after) != 1 {
		t.Fatalf("adopted issues=%+v err=%v", after, err)
	}
	issue := after[0]
	if created.IssueID != before[0].ID || issue.ID != before[0].ID || issue.Source != SourceUser ||
		issue.ReporterID == nil || *issue.ReporterID != reporterID || issue.Category == nil || *issue.Category != CategoryBadCopy ||
		issue.Detail != "The copy glitches." || issue.Status != IssueObserving {
		t.Fatalf("adopted user issue=%+v response=%+v", issue, created)
	}
	var dedupe sql.NullString
	if err := svc.db.QueryRow("SELECT dedupe_key FROM issues WHERE id=?", issue.ID).Scan(&dedupe); err != nil || dedupe.Valid {
		t.Fatalf("adopted dedupe key=%v err=%v, want NULL", dedupe, err)
	}
}

func TestMovieReportIgnoresStrayTVDBIdentity(t *testing.T) {
	svc, _, reporterID := setupObservationService(t, false)
	created, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: "radarr-observe", MediaType: "movie", TmdbID: 42, TvdbID: 999, Category: CategoryOther,
	})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := svc.GetIssue(created.IssueID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.TvdbID != 0 {
		t.Fatalf("stored meaningless movie tvdb_id=%d", issue.TvdbID)
	}
}

func TestEmptySnapshotSettlesThenMissingPromotes(t *testing.T) {
	svc, notifier, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{observedProblem("old", 1, 100)}, base); err != nil {
		t.Fatal(err)
	}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", nil, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	issues, _ := svc.ListIssues("")
	if len(issues) != 1 || issues[0].ClosedAt != nil || issues[0].Status != IssueRecovering {
		t.Fatalf("settling issue = %+v", issues)
	}
	if len(notifier.adminEvents) != 0 {
		t.Fatalf("settling notified: %v", notifier.adminEvents)
	}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", nil, base.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issue, _ := svc.GetIssue(issues[0].ID)
	if issue.Status != IssueOpen || issue.ClosedAt != nil {
		t.Fatalf("missing-file issue = %+v", issue)
	}
	if len(notifier.adminEvents) != 1 || notifier.adminEvents[0] != "issue_created" {
		t.Fatalf("events=%v", notifier.adminEvents)
	}
	drainJobs(svc)
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", nil, base.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issue, _ = svc.GetIssue(issues[0].ID)
	if issue.Status != IssueOpen || issue.ClosedAt != nil {
		t.Fatalf("repeated empty snapshot restarted settled issue: %+v", issue)
	}
	if len(notifier.adminEvents) != 1 || drainJobs(svc) != 0 {
		t.Fatalf("repeated empty snapshot re-alerted/re-enqueued: %v", notifier.adminEvents)
	}
}

func TestAbsentToPresentExactFileClosesUnpromotedObservationSilently(t *testing.T) {
	fileState := &testFileState{hasFile: false}
	svc, notifier, _ := setupObservationServiceWithState(t, fileState)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{observedProblem("old", 1, 100)}, base); err != nil {
		t.Fatal(err)
	}
	fileState.hasFile = true
	fileState.fileID = 20
	fileState.importDownloadID = "old"
	fileState.importDate = base // equal-second evidence is valid (SQLite timestamps are second precision).
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", nil, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", nil, base.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issues, _ := svc.ListIssues("")
	if len(issues) != 1 || issues[0].Status != IssueResolved || issues[0].ClosedAt == nil || issues[0].ResolutionKind != ResolutionArrStateCleared {
		t.Fatalf("resolved issue = %+v", issues)
	}
	if len(notifier.adminEvents) != 1 || notifier.adminEvents[0] != "issue_updated" || drainJobs(svc) != 0 {
		t.Fatalf("silent recovery invalidation/events = %v", notifier.adminEvents)
	}
	var historyID, fileID int64
	var downloadID string
	if err := svc.db.QueryRow("SELECT import_history_id,import_download_id,import_file_id FROM issue_observations WHERE issue_id=?", issues[0].ID).Scan(&historyID, &downloadID, &fileID); err != nil || historyID != 77 || downloadID != "old" || fileID != 20 {
		t.Fatalf("receipt history=%d download=%q file=%d err=%v", historyID, downloadID, fileID, err)
	}
}

func TestWrongImportWitnessNeverSilentlyCloses(t *testing.T) {
	cases := []struct {
		name, downloadID              string
		historyFileID, historyMovieID int
	}{
		{name: "wrong download", downloadID: "someone-else"},
		{name: "wrong file", downloadID: "old", historyFileID: 99},
		{name: "wrong media", downloadID: "old", historyMovieID: 2},
		{name: "no receipt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := &testFileState{hasFile: false}
			svc, notifier, _ := setupObservationServiceWithState(t, state)
			enableAutoDispatch(t, svc, 5)
			base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
			if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{observedProblem("old", 1, 100)}, base); err != nil {
				t.Fatal(err)
			}
			state.hasFile, state.fileID = true, 20
			state.importDownloadID, state.importDate = tc.downloadID, base
			state.historyFileID, state.historyMovieID = tc.historyFileID, tc.historyMovieID
			if err := svc.observeQueueSnapshot("radarr", "radarr-observe", nil, base.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := svc.observeQueueSnapshot("radarr", "radarr-observe", nil, base.Add(11*time.Minute)); err != nil {
				t.Fatal(err)
			}
			issues, _ := svc.ListIssues("")
			if len(issues) != 1 || issues[0].Status != IssueNeedsAdmin || issues[0].ClosedAt != nil || drainJobs(svc) != 0 {
				t.Fatalf("issue=%+v", issues)
			}
			if len(notifier.adminEvents) != 1 || notifier.adminEvents[0] != "issue_created" {
				t.Fatalf("events=%v", notifier.adminEvents)
			}
		})
	}
}

func TestPreexistingFileDoesNotProveUpgradeRecovery(t *testing.T) {
	fileState := &testFileState{hasFile: true, fileID: 10}
	svc, notifier, _ := setupObservationServiceWithState(t, fileState)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{observedProblem("upgrade", 1, 100)}, base); err != nil {
		t.Fatal(err)
	}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", nil, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", nil, base.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issues, _ := svc.ListIssues("")
	if len(issues) != 1 || issues[0].Status != IssueOpen || issues[0].ClosedAt != nil {
		t.Fatalf("preexisting file falsely proved recovery: %+v", issues)
	}
	if len(notifier.adminEvents) != 1 || notifier.adminEvents[0] != "issue_created" {
		t.Fatalf("upgrade failure did not promote: %v", notifier.adminEvents)
	}
}

func TestFailedPendingAtRestartCreatesRecoveringTracking(t *testing.T) {
	svc, notifier, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	signal := arr.QueueSignal{TrackedDownloadStatus: "warning", TrackedDownloadState: "failedPending"}
	item := arr.QueueObservation{DownloadID: "failed", Media: arr.QueueMediaContext{QueueID: 2, TmdbID: 42, Title: "Example"}, Signal: signal, Diagnosis: arr.Diagnose(signal)}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{item}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	issues, _ := svc.ListIssues("")
	if len(issues) != 1 || issues[0].Status != IssueRecovering || !issues[0].Read {
		t.Fatalf("issues=%+v", issues)
	}
	if len(notifier.adminEvents) != 0 || drainJobs(svc) != 0 {
		t.Fatalf("attention leaked: %v", notifier.adminEvents)
	}
}

func TestIDLessAutoIncidentEscalatesAfterUncertainSettleWithoutAgent(t *testing.T) {
	svc, notifier, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	item := observedProblem("fallback-only", 3, 100)
	item.Media.TmdbID = 0
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{item}, base); err != nil {
		t.Fatal(err)
	}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", nil, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", nil, base.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issues, _ := svc.ListIssues("")
	jobs := drainJobs(svc)
	if len(issues) != 1 || issues[0].Status != IssueNeedsAdmin || jobs != 0 || len(notifier.adminEvents) != 1 {
		t.Fatalf("issues=%+v jobs=%d events=%v", issues, jobs, notifier.adminEvents)
	}
}

func TestInformationalImportRejectionsAreNotActiveRecovery(t *testing.T) {
	cases := []string{
		"Not an upgrade for existing movie file",
		"Not a Custom Format upgrade for existing episode file",
		"Already imported",
		"Series is not monitored",
	}
	for _, message := range cases {
		signal := arr.QueueSignal{
			Status: "completed", TrackedDownloadStatus: "warning", TrackedDownloadState: "importPending",
			StatusMessages: []arr.StatusMessage{{Messages: []string{message}}},
		}
		item := arr.QueueObservation{Signal: signal, Diagnosis: arr.Diagnose(signal)}
		if item.Diagnosis.Severity != arr.SeverityInfo {
			t.Fatalf("%q diagnosis=%+v, want info fixture", message, item.Diagnosis)
		}
		if recoveryObservation(item) {
			t.Fatalf("%q was incorrectly classified as active recovery", message)
		}
	}
}

func TestRecentScopedSnapshotMakesUserReportSilentAndPreservesSeasonScope(t *testing.T) {
	svc, notifier, reporterID := setupObservationService(t, false)
	// AI/remediation stays disabled: observation safety is independent of model
	// execution, so an active arr retry must still suppress premature attention.
	now := time.Now().UTC()
	signal := arr.QueueSignal{TrackedDownloadStatus: "warning", TrackedDownloadState: "importPending"}
	item := arr.QueueObservation{DownloadID: "episode", Media: arr.QueueMediaContext{QueueID: 9, Title: "Series", TmdbID: 55, TvdbID: 100, SeasonNumber: 1, EpisodeNumber: 2}, Signal: signal, Diagnosis: arr.Diagnose(signal)}
	if err := svc.observeQueueSnapshot("sonarr", "sonarr-observe", []arr.QueueObservation{item}, now); err != nil {
		t.Fatal(err)
	}
	response, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: "sonarr-observe", MediaType: "tv", TmdbID: 55, TvdbID: 100, SeasonNumber: 1, EpisodeNumber: 0, Category: CategoryBadCopy, Reason: "audio cuts out", Title: "Series"})
	if err != nil {
		t.Fatal(err)
	}
	issue, _ := svc.GetIssue(response.IssueID)
	if issue.Status != IssueObserving || issue.SeasonNumber != 1 || issue.EpisodeNumber != 0 || issue.Detail != "audio cuts out" || !issue.Read {
		t.Fatalf("tracked report = %+v", issue)
	}
	if len(notifier.adminEvents) != 0 || drainJobs(svc) != 0 {
		t.Fatalf("report emitted attention: %v", notifier.adminEvents)
	}
}

func TestProgressSignatureResetsQuietWithoutGrowingAttemptAudit(t *testing.T) {
	svc, _, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	settings := svc.Settings()
	settings.ObservationMinMinutes = 10
	settings.ObservationQuietMinutes = 5
	_, _ = svc.SetSettings(settings)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	item := observedProblem("same", 1, 100)
	item.Signal.Status = "queued"
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{item}, base); err != nil {
		t.Fatal(err)
	}
	item.Signal.Status = "paused"
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{item}, base.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	issue, _ := svc.ListIssues("")
	if issue[0].Status != IssueObserving {
		t.Fatalf("promoted before quiet window: %+v", issue[0])
	}
	var attempts int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM issue_observation_attempts WHERE issue_id=?", issue[0].ID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("attempt rows=%d, want transition-only 1", attempts)
	}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{item}, base.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetIssue(issue[0].ID)
	if got.Status != IssueOpen {
		t.Fatalf("quiet problem did not promote: %+v", got)
	}
}

func TestRecoverySupersedesProposalAndAbortsParkedRun(t *testing.T) {
	svc, _, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	problem := observedProblem("old", 1, 100)
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{problem}, base); err != nil {
		t.Fatal(err)
	}
	issues, _ := svc.ListIssues("")
	issueID := issues[0].ID
	run, _ := svc.db.Exec("INSERT INTO agent_runs(issue_id,trigger,status,model) VALUES (?,'auto','waiting_approval','m')", issueID)
	runID, _ := run.LastInsertId()
	_, _ = svc.db.Exec("UPDATE issues SET status=?,read=0 WHERE id=?", IssueAwaitingApproval, issueID)
	_, _ = svc.db.Exec("UPDATE issue_observations SET promoted_at=? WHERE issue_id=?", base, issueID)
	_, err := svc.db.Exec("INSERT INTO agent_actions(issue_id,run_id,kind,params,rationale,status,fingerprint) VALUES (?,?,?,'{}','r',?,?)", issueID, runID, string(ActionTriggerSearch), ActionProposed, "fp")
	if err != nil {
		t.Fatal(err)
	}
	replacement := problem
	replacement.DownloadID = "new"
	replacement.Media.QueueID = 2
	replacement.Signal = arr.QueueSignal{Status: "downloading", TrackedDownloadStatus: "ok", Size: 100, SizeLeft: 50}
	replacement.Diagnosis = arr.Diagnose(replacement.Signal)
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{replacement}, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var issueStatus, actionStatus, runStatus string
	_ = svc.db.QueryRow("SELECT status FROM issues WHERE id=?", issueID).Scan(&issueStatus)
	_ = svc.db.QueryRow("SELECT status FROM agent_actions WHERE issue_id=?", issueID).Scan(&actionStatus)
	_ = svc.db.QueryRow("SELECT status FROM agent_runs WHERE id=?", runID).Scan(&runStatus)
	if issueStatus != IssueRecovering || actionStatus != ActionSuperseded || runStatus != "aborted" {
		t.Fatalf("states issue=%s action=%s run=%s", issueStatus, actionStatus, runStatus)
	}
}

func TestChangedProblemSignatureReclaimsPendingProposal(t *testing.T) {
	svc, _, _ := setupObservationService(t, false)
	enableAutoDispatch(t, svc, 5)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	problem := observedProblem("same", 1, 100)
	problem.Signal.Status = "queued"
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{problem}, base); err != nil {
		t.Fatal(err)
	}
	issues, _ := svc.ListIssues("")
	issueID := issues[0].ID
	run, _ := svc.db.Exec("INSERT INTO agent_runs(issue_id,trigger,status,model) VALUES (?,'auto','waiting_approval','m')", issueID)
	runID, _ := run.LastInsertId()
	_, _ = svc.db.Exec("UPDATE issues SET status=?,read=0 WHERE id=?", IssueAwaitingApproval, issueID)
	_, _ = svc.db.Exec("UPDATE issue_observations SET promoted_at=? WHERE issue_id=?", base, issueID)
	if _, err := svc.db.Exec("INSERT INTO agent_actions(issue_id,run_id,kind,params,rationale,status,fingerprint) VALUES (?,?,?,'{}','r',?,?)", issueID, runID, string(ActionTriggerSearch), ActionProposed, "changed-warning"); err != nil {
		t.Fatal(err)
	}
	problem.Signal.Status = "paused" // still a problem, but a new live diagnosis.
	problem.Diagnosis = arr.Diagnose(problem.Signal)
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{problem}, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var issueStatus, actionStatus, runStatus string
	_ = svc.db.QueryRow("SELECT status FROM issues WHERE id=?", issueID).Scan(&issueStatus)
	_ = svc.db.QueryRow("SELECT status FROM agent_actions WHERE issue_id=?", issueID).Scan(&actionStatus)
	_ = svc.db.QueryRow("SELECT status FROM agent_runs WHERE id=?", runID).Scan(&runStatus)
	if issueStatus != IssueRecovering || actionStatus != ActionSuperseded || runStatus != "aborted" {
		t.Fatalf("changed warning left stale work active: issue=%s action=%s run=%s", issueStatus, actionStatus, runStatus)
	}
}

func TestUserIssueRecoveryAbortsInFlightRunWithoutDuplicateAutoIssue(t *testing.T) {
	svc, _, reporterID := setupObservationService(t, false)
	response, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: "radarr-observe", MediaType: "movie", TmdbID: 42, Category: CategoryOther})
	if err != nil {
		t.Fatal(err)
	}
	runRes, _ := svc.db.Exec("INSERT INTO agent_runs(issue_id,trigger,status,model) VALUES (?,'user_report','running','m')", response.IssueID)
	runID, _ := runRes.LastInsertId()
	_, _ = svc.db.Exec("UPDATE issues SET status=?,active_run_id=?,read=0 WHERE id=?", IssueInvestigating, runID, response.IssueID)
	_, _ = svc.db.Exec("UPDATE issue_observations SET promoted_at=CURRENT_TIMESTAMP WHERE issue_id=?", response.IssueID)
	signal := arr.QueueSignal{Status: "downloading", TrackedDownloadStatus: "ok", Size: 100, SizeLeft: 50}
	item := arr.QueueObservation{DownloadID: "retry", Media: arr.QueueMediaContext{QueueID: 9, TmdbID: 42}, Signal: signal, Diagnosis: arr.Diagnose(signal)}
	if err := svc.observeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{item}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	issue, _ := svc.GetIssue(response.IssueID)
	var runStatus string
	_ = svc.db.QueryRow("SELECT status FROM agent_runs WHERE id=?", runID).Scan(&runStatus)
	var autoCount int
	_ = svc.db.QueryRow("SELECT COUNT(*) FROM issues WHERE source=?", SourceAuto).Scan(&autoCount)
	if issue.Status != IssueRecovering || runStatus != "aborted" || autoCount != 0 {
		t.Fatalf("issue=%+v run=%s auto=%d", issue, runStatus, autoCount)
	}
}

func TestPostClaimRecoveryCancelsApprovalBeforeExecutor(t *testing.T) {
	svc, executor, issueID, actionID := approvalFixture(t)
	calls := 0
	svc.recoveryProbe = func(*Issue) (arrRecoveryProbe, error) {
		calls++
		if calls == 1 {
			return arrRecoveryProbe{}, nil
		}
		return arrRecoveryProbe{active: true, item: arr.QueueObservation{DownloadID: "retry", Media: arr.QueueMediaContext{QueueID: 88, TmdbID: 42}}}, nil
	}
	_, err := svc.ApproveAction(testAdminID, actionID, nil)
	if !errors.Is(err, ErrActionDecisionConflict) {
		t.Fatalf("approve err=%v", err)
	}
	if executor.count() != 0 {
		t.Fatalf("executor calls=%d", executor.count())
	}
	action, _ := svc.GetAction(actionID)
	issue, _ := svc.GetIssue(issueID)
	if action.Status != ActionSuperseded || action.ExecutedAt != nil || issue.Status != IssueRecovering {
		t.Fatalf("action=%+v issue=%+v", action, issue)
	}
}

func TestAbortedRunCannotWriteIntoNewRunOwnership(t *testing.T) {
	svc, notifier, _ := setupObservationService(t, false)
	res, err := svc.db.Exec("INSERT INTO issues(source,status,media_type,tmdb_id,title,instance_id) VALUES ('user',?,'movie',42,'Example','radarr-observe')", IssueInvestigating)
	if err != nil {
		t.Fatal(err)
	}
	issueID, _ := res.LastInsertId()
	oldRes, _ := svc.db.Exec("INSERT INTO agent_runs(issue_id,trigger,status,model,finished_at) VALUES (?,'user_report','aborted','m',CURRENT_TIMESTAMP)", issueID)
	oldRun, _ := oldRes.LastInsertId()
	newRes, _ := svc.db.Exec("INSERT INTO agent_runs(issue_id,trigger,status,model) VALUES (?,'user_report','running','m')", issueID)
	newRun, _ := newRes.LastInsertId()
	_, _ = svc.db.Exec("UPDATE issues SET active_run_id=? WHERE id=?", newRun, issueID)
	ctx := mcp.WithAgentRunOwnership(context.Background(), oldRun)
	if err := svc.PostIssueMessage(ctx, issueID, "stale finding"); err == nil {
		t.Fatal("stale run posted a message")
	}
	params := json.RawMessage(`{"media_type":"movie","tmdb_id":42}`)
	if _, _, err := svc.ProposeAction(ctx, issueID, string(ActionTriggerSearch), params, "stale proposal", "old-tool"); err == nil {
		t.Fatal("stale run recorded a proposal")
	}
	var messages, actions int
	_ = svc.db.QueryRow("SELECT COUNT(*) FROM issue_messages WHERE issue_id=?", issueID).Scan(&messages)
	_ = svc.db.QueryRow("SELECT COUNT(*) FROM agent_actions WHERE issue_id=?", issueID).Scan(&actions)
	var runStatus string
	_ = svc.db.QueryRow("SELECT status FROM agent_runs WHERE id=?", newRun).Scan(&runStatus)
	if messages != 0 || actions != 0 || runStatus != "running" || len(notifier.adminEvents) != 0 {
		t.Fatalf("messages=%d actions=%d run=%s events=%v", messages, actions, runStatus, notifier.adminEvents)
	}
}

func TestStaleSnapshotKeepsUserReportQuietPendingFreshRead(t *testing.T) {
	svc, notifier, reporterID := setupObservationService(t, false)
	settings := Defaults()
	settings.Enabled = true
	settings.AutoDispatch = false
	_, _ = svc.SetSettings(settings)
	item := observedProblem("old", 1, 100)
	if err := svc.storeQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{item}, time.Now().Add(-2*queueSnapshotFreshness)); err != nil {
		t.Fatal(err)
	}
	response, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: "radarr-observe", MediaType: "movie", TmdbID: 42, Category: CategoryOther, Reason: "still bad"})
	if err != nil {
		t.Fatal(err)
	}
	issue, _ := svc.GetIssue(response.IssueID)
	if issue.Status != IssueObserving || !issue.Read || len(notifier.adminEvents) != 0 {
		t.Fatalf("issue=%+v events=%v", issue, notifier.adminEvents)
	}
}

func TestContinuousQueueFailureEscalatesQuietReportWithoutAgent(t *testing.T) {
	svc, notifier, reporterID := setupObservationService(t, false)
	response, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: "radarr-observe", MediaType: "movie", TmdbID: 42, Category: CategoryOther})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	_, _ = svc.db.Exec("UPDATE issue_observations SET first_seen_at=? WHERE issue_id=?", base, response.IssueID)
	svc.noteObservationFailure("radarr", "radarr-observe", context.DeadlineExceeded, base)
	svc.noteObservationFailure("radarr", "radarr-observe", context.DeadlineExceeded, base.Add(11*time.Minute))
	issue, _ := svc.GetIssue(response.IssueID)
	if issue.Status != IssueNeedsAdmin || drainJobs(svc) != 0 || len(notifier.adminEvents) != 1 {
		t.Fatalf("issue=%+v jobs=%d events=%v", issue, drainJobs(svc), notifier.adminEvents)
	}
}

func TestOldInstanceFailureDoesNotImmediatelyEscalateLateReport(t *testing.T) {
	svc, notifier, reporterID := setupObservationService(t, false)
	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	svc.noteObservationFailure("radarr", "radarr-observe", context.DeadlineExceeded, base)
	response, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: "radarr-observe", MediaType: "movie", TmdbID: 42, Category: CategoryOther})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = svc.db.Exec("UPDATE issue_observations SET first_seen_at=? WHERE issue_id=?", base.Add(9*time.Minute), response.IssueID)
	svc.noteObservationFailure("radarr", "radarr-observe", context.DeadlineExceeded, base.Add(10*time.Minute))
	issue, _ := svc.GetIssue(response.IssueID)
	if issue.Status != IssueObserving || len(notifier.adminEvents) != 0 {
		t.Fatalf("late report escalated early: %+v events=%v", issue, notifier.adminEvents)
	}
	svc.noteObservationFailure("radarr", "radarr-observe", context.DeadlineExceeded, base.Add(20*time.Minute))
	issue, _ = svc.GetIssue(response.IssueID)
	if issue.Status != IssueNeedsAdmin || len(notifier.adminEvents) != 1 {
		t.Fatalf("late report did not age independently: %+v events=%v", issue, notifier.adminEvents)
	}
}

func TestSpecialEpisodeScopeIsExactAndHasFileCapable(t *testing.T) {
	special := arr.QueueMediaContext{TmdbID: 55, TvdbID: 100, SeasonNumber: 0, EpisodeNumber: 1}
	regular := arr.QueueMediaContext{TmdbID: 55, TvdbID: 100, SeasonNumber: 1, EpisodeNumber: 1}
	keyA := incidentScopeKey("sonarr-observe", "tv", "first-download", special)
	keyB := incidentScopeKey("sonarr-observe", "tv", "replacement-download", special)
	if keyA == "" || keyA != keyB {
		t.Fatalf("special episode was not stable across replacements: %q %q", keyA, keyB)
	}
	if keyA == incidentScopeKey("sonarr-observe", "tv", "regular", regular) {
		t.Fatal("S00E01 merged with S01E01")
	}
	if mediaScopeMatches(special, regular, "tv") {
		t.Fatal("special episode scope matched a regular-season episode")
	}

	svc, _, reporterID := setupObservationService(t, true)
	issue := &Issue{InstanceID: "sonarr-observe", MediaType: "tv", TvdbID: 100, SeasonNumber: 0, EpisodeNumber: 1}
	hasFile, known, err := svc.exactIssueHasFile(issue)
	if err != nil || !known || !hasFile {
		t.Fatalf("special episode hasFile = %v known=%v err=%v", hasFile, known, err)
	}
	response, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: "sonarr-observe", MediaType: "tv", TmdbID: 55, TvdbID: 100,
		SeasonNumber: 0, EpisodeNumber: 1, Category: CategoryOther,
	})
	if err != nil || response.Status != IssueObserving {
		t.Fatalf("special report response=%+v err=%v", response, err)
	}
}

func TestObservationWorkerDropsNothingAsEmptyOnCancel(t *testing.T) {
	svc, _, _ := setupObservationService(t, false)
	dispatcher := NewAutoDispatcher(svc)
	ctx, cancel := context.WithCancel(context.Background())
	dispatcher.StartObservationSweeper(ctx)
	cancel()
	// A callback after cancellation can only enqueue/drop the supplied snapshot;
	// it has no synchronous DB or network side effect and cannot invent absence.
	dispatcher.ObserveQueueSnapshot("radarr", "radarr-observe", []arr.QueueObservation{observedProblem("x", 1, 100)})
}
