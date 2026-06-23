package remediation

import (
	"database/sql"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
)

// fakeNotifier records the events fired so a test can assert that creating an
// issue notifies admins. It satisfies the Notifier interface.
type fakeNotifier struct {
	adminEvents []string
	userEvents  []string
}

func (f *fakeNotifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	f.userEvents = append(f.userEvents, eventType)
}

func (f *fakeNotifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	f.adminEvents = append(f.adminEvents, eventType)
}

// setupTestService builds a remediation service over an in-memory DB (which runs
// the real initSQL, so the issues/issue_messages tables exist) plus a seeded
// reporter user. registry/bridge are nil: no Wave-1 issue method dereferences
// them.
func setupTestService(t *testing.T) (*Service, *fakeNotifier, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	reporterID := seedUser(t, database, "reporter")
	notif := &fakeNotifier{}
	return NewService(database, nil, nil, notif), notif, reporterID
}

func seedUser(t *testing.T, database *sql.DB, username string) int64 {
	t.Helper()
	res, err := database.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, '', 'user')", username)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user id: %v", err)
	}
	return id
}

// TestUserIssueLifecycle walks the Wave-1 contract end to end: create a user
// issue (admins notified), dedupe a duplicate report (occurrences bumps, no
// second row), thread a reply, then list + dismiss.
func TestUserIssueLifecycle(t *testing.T) {
	svc, notif, reporterID := setupTestService(t)

	req := &CreateIssueRequest{
		MediaType:     "tv",
		TmdbID:        42,
		SeasonNumber:  2,
		EpisodeNumber: 4,
		Category:      CategoryWrongAudio,
		Reason:        "Default audio track is Russian, I want English.",
		Title:         "Some Show",
	}

	resp, err := svc.CreateUserIssue(reporterID, req)
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	if resp.IssueID == 0 || resp.Status != IssueOpen {
		t.Fatalf("CreateUserIssue resp = %+v, want non-zero id + open", resp)
	}
	if len(notif.adminEvents) != 1 || notif.adminEvents[0] != "issue_created" {
		t.Fatalf("admin events = %v, want one issue_created", notif.adminEvents)
	}

	// Duplicate report (same reporter, scope, category): bumps occurrences and
	// returns the SAME issue, without notifying again.
	dup, err := svc.CreateUserIssue(reporterID, req)
	if err != nil {
		t.Fatalf("CreateUserIssue duplicate: %v", err)
	}
	if dup.IssueID != resp.IssueID {
		t.Fatalf("duplicate issue id = %d, want same as original %d", dup.IssueID, resp.IssueID)
	}
	if len(notif.adminEvents) != 1 {
		t.Fatalf("admin events after duplicate = %v, want still just one", notif.adminEvents)
	}

	issue, err := svc.GetIssue(resp.IssueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Occurrences != 2 {
		t.Fatalf("occurrences = %d, want 2 after a duplicate report", issue.Occurrences)
	}
	if issue.Category == nil || *issue.Category != CategoryWrongAudio {
		t.Fatalf("category = %v, want wrong_audio", issue.Category)
	}
	if issue.ReporterName == nil || *issue.ReporterName != "reporter" {
		t.Fatalf("reporter_name = %v, want reporter", issue.ReporterName)
	}
	if issue.Detail != req.Reason {
		t.Fatalf("detail = %q, want the verbatim reason", issue.Detail)
	}

	// A distinct category opens a NEW issue (dedupe is per scope+category).
	other, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		MediaType: "tv", TmdbID: 42, SeasonNumber: 2, EpisodeNumber: 4,
		Category: CategoryBadCopy,
	})
	if err != nil {
		t.Fatalf("CreateUserIssue other category: %v", err)
	}
	if other.IssueID == resp.IssueID {
		t.Fatalf("different-category report reused issue %d, want a new one", resp.IssueID)
	}

	// Thread: admin reply then reporter reply, oldest-first ordering preserved.
	if err := svc.PostReply(resp.IssueID, AuthorAdmin, 0, "Looking into it."); err != nil {
		t.Fatalf("PostReply admin: %v", err)
	}
	if err := svc.PostReply(resp.IssueID, AuthorUser, reporterID, "Thanks!"); err != nil {
		t.Fatalf("PostReply user: %v", err)
	}
	thread, err := svc.IssueThread(resp.IssueID)
	if err != nil {
		t.Fatalf("IssueThread: %v", err)
	}
	if len(thread) != 2 {
		t.Fatalf("thread length = %d, want 2", len(thread))
	}
	if thread[0].AuthorKind != AuthorAdmin || thread[1].AuthorKind != AuthorUser {
		t.Fatalf("thread author kinds = %q,%q, want admin,user", thread[0].AuthorKind, thread[1].AuthorKind)
	}
	if thread[1].Body != "Thanks!" {
		t.Fatalf("reporter message body = %q, want verbatim", thread[1].Body)
	}

	// List (all + filtered) and open count.
	all, err := svc.ListIssues("")
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListIssues all = %d, want 2 open issues", len(all))
	}
	openOnly, err := svc.ListIssues(IssueOpen)
	if err != nil {
		t.Fatalf("ListIssues(open): %v", err)
	}
	if len(openOnly) != 2 {
		t.Fatalf("ListIssues(open) = %d, want 2", len(openOnly))
	}
	if n, err := svc.OpenIssueCount(); err != nil || n != 2 {
		t.Fatalf("OpenIssueCount = %d (err %v), want 2", n, err)
	}

	// Dismiss closes the issue: it leaves the open set and is idempotent.
	if err := svc.DismissIssue(resp.IssueID); err != nil {
		t.Fatalf("DismissIssue: %v", err)
	}
	if err := svc.DismissIssue(resp.IssueID); err != nil {
		t.Fatalf("DismissIssue (second, idempotent): %v", err)
	}
	dismissed, err := svc.GetIssue(resp.IssueID)
	if err != nil {
		t.Fatalf("GetIssue after dismiss: %v", err)
	}
	if dismissed.Status != IssueDismissed {
		t.Fatalf("status after dismiss = %q, want dismissed", dismissed.Status)
	}
	if n, err := svc.OpenIssueCount(); err != nil || n != 1 {
		t.Fatalf("OpenIssueCount after dismiss = %d (err %v), want 1", n, err)
	}

	// A dismissed issue may be re-reported, opening a fresh row (closed rows no
	// longer dedupe).
	reopened, err := svc.CreateUserIssue(reporterID, req)
	if err != nil {
		t.Fatalf("CreateUserIssue after dismiss: %v", err)
	}
	if reopened.IssueID == resp.IssueID {
		t.Fatalf("re-report reused dismissed issue %d, want a new row", resp.IssueID)
	}
}

// TestMovieIssueScopeNormalized confirms a movie report stores season/episode 0
// regardless of what the client sent (movies have no season scope).
func TestMovieIssueScopeNormalized(t *testing.T) {
	svc, _, reporterID := setupTestService(t)

	resp, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		MediaType: "movie", TmdbID: 7, SeasonNumber: 3, EpisodeNumber: 9,
		Category: CategoryWrongContent,
	})
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	issue, err := svc.GetIssue(resp.IssueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.SeasonNumber != 0 || issue.EpisodeNumber != 0 {
		t.Fatalf("movie scope = S%d E%d, want 0/0", issue.SeasonNumber, issue.EpisodeNumber)
	}
}

// TestCreateUserIssueValidation rejects an unsupported media type and an unknown
// category before any row is written.
func TestCreateUserIssueValidation(t *testing.T) {
	svc, _, reporterID := setupTestService(t)

	if _, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{MediaType: "music", TmdbID: 1, Category: CategoryOther}); err == nil {
		t.Fatal("expected error for unsupported media type")
	}
	if _, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{MediaType: "movie", TmdbID: 1, Category: "nonsense"}); err == nil {
		t.Fatal("expected error for invalid category")
	}
}

// TestSettingsRoundTrip proves the remediation settings persist and reload
// (mirroring request_settings), defaults apply for an unset blob, and out-of-
// range bound fields normalize while empty provider/model stay empty (inherit).
func TestSettingsRoundTrip(t *testing.T) {
	svc, _, _ := setupTestService(t)

	// Defaults before anything is stored.
	d := svc.Settings()
	if d.Enabled || d.AutoDispatch || !d.AllowReporting {
		t.Fatalf("default switches = %+v, want enabled=false auto_dispatch=false allow_reporting=true", d)
	}
	if d.Autonomy != AutonomyPropose {
		t.Fatalf("default autonomy = %q, want propose", d.Autonomy)
	}
	if d.Provider != "" || d.Model != "" {
		t.Fatalf("default provider/model = %q/%q, want empty (inherit)", d.Provider, d.Model)
	}
	if d.MaxCostMicros != 500000 || d.DailyCostCeilingMicros != 5000000 {
		t.Fatalf("default cost ceilings = %d/%d, want 500000/5000000", d.MaxCostMicros, d.DailyCostCeilingMicros)
	}

	// Round-trip a populated settings value.
	in := Settings{
		Enabled:                true,
		AutoDispatch:           true,
		AllowReporting:         false,
		Autonomy:               AutonomyInvestigateOnly,
		Provider:               "openai",
		Model:                  "gpt-x",
		MaxSteps:               20,
		MaxTurnTokens:          8000,
		MaxWallClockSecs:       600,
		MaxCostMicros:          123456,
		DailyRunCap:            10,
		DailyCostCeilingMicros: 999999,
		CircuitBreakerGiveups:  3,
	}
	saved, err := svc.SetSettings(in)
	if err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if saved != in {
		t.Fatalf("SetSettings returned %+v, want the input echoed", saved)
	}
	got := svc.Settings()
	if got != in {
		t.Fatalf("reloaded settings = %+v, want %+v", got, in)
	}

	// Out-of-range bounds normalize back to defaults; an unknown autonomy too;
	// empty provider/model are left empty (inherit), not defaulted.
	norm, err := svc.SetSettings(Settings{Autonomy: "bogus", MaxSteps: 0, MaxCostMicros: -5})
	if err != nil {
		t.Fatalf("SetSettings normalize: %v", err)
	}
	def := Defaults()
	if norm.Autonomy != def.Autonomy {
		t.Fatalf("normalized autonomy = %q, want %q", norm.Autonomy, def.Autonomy)
	}
	if norm.MaxSteps != def.MaxSteps || norm.MaxCostMicros != def.MaxCostMicros {
		t.Fatalf("normalized bounds = steps %d cost %d, want %d/%d", norm.MaxSteps, norm.MaxCostMicros, def.MaxSteps, def.MaxCostMicros)
	}
	if norm.Provider != "" || norm.Model != "" {
		t.Fatalf("normalized provider/model = %q/%q, want empty (inherit)", norm.Provider, norm.Model)
	}
}
