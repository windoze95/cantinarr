package remediation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const (
	testRadarrInstanceID  = "radarr-test"
	testRadarrInstanceID2 = "radarr-other"
	testSonarrInstanceID  = "sonarr-test"
)

// fakeNotifier records the events fired so a test can assert that creating an
// issue notifies admins. It satisfies the Notifier interface.
type fakeNotifier struct {
	adminEvents []string
	// adminData is adminEvents' payload, index-aligned, for the assertions that
	// care what an alert said (a coalesced wave carries a count, not an issue id).
	adminData  []map[string]interface{}
	userEvents []string
}

func (f *fakeNotifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	f.userEvents = append(f.userEvents, eventType)
}

func (f *fakeNotifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	f.adminEvents = append(f.adminEvents, eventType)
	f.adminData = append(f.adminData, data)
}

// setupTestService builds a remediation service over an in-memory DB (which runs
// the real initSQL, so the issues/issue_messages tables exist) plus a seeded
// reporter user and concrete Radarr/Sonarr instances. User reports must be
// bound to an existing instance of the media's matching service type.
func setupTestService(t *testing.T) (*Service, *fakeNotifier, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	reporterID := seedUser(t, database, "reporter")
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	for _, inst := range []*instance.Instance{
		{ID: testRadarrInstanceID, ServiceType: "radarr", Name: "Movies", URL: "http://radarr.test", APIKey: "key"},
		{ID: testRadarrInstanceID2, ServiceType: "radarr", Name: "Other Movies", URL: "http://other-radarr.test", APIKey: "key"},
		{ID: testSonarrInstanceID, ServiceType: "sonarr", Name: "TV", URL: "http://sonarr.test", APIKey: "key"},
	} {
		if err := store.Create(inst); err != nil {
			t.Fatalf("create %s test instance: %v", inst.ServiceType, err)
		}
	}
	// Reports are scoped to libraries the reporter can see; grant them every
	// fixture instance so tests exercise reporting itself, not access.
	if err := store.SetUserGrants(reporterID, map[string][]string{
		"radarr": {testRadarrInstanceID, testRadarrInstanceID2},
		"sonarr": {testSonarrInstanceID},
	}); err != nil {
		t.Fatalf("grant fixture instances: %v", err)
	}
	notif := &fakeNotifier{}
	return NewService(database, instance.NewRegistry(store), nil, notif), notif, reporterID
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
// ISS-005: Repeated reports dedupe only within the same exact incident scope.
func TestUserIssueLifecycle(t *testing.T) {
	svc, notif, reporterID := setupTestService(t)

	req := &CreateIssueRequest{
		InstanceID:    testSonarrInstanceID,
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
	if resp.IssueID == 0 || resp.Status != IssueObserving {
		t.Fatalf("CreateUserIssue resp = %+v, want non-zero id + observing", resp)
	}
	if len(notif.adminEvents) != 0 {
		t.Fatalf("admin events = %v, want silent observation", notif.adminEvents)
	}

	// Duplicate report (same reporter, scope, category): bumps occurrences,
	// appends the new reason to the thread, and returns the SAME issue without
	// notifying as though a second incident had opened.
	duplicateReq := *req
	duplicateReq.Reason = "The commentary track is now selected by default too."
	dup, err := svc.CreateUserIssue(reporterID, &duplicateReq)
	if err != nil {
		t.Fatalf("CreateUserIssue duplicate: %v", err)
	}
	if dup.IssueID != resp.IssueID {
		t.Fatalf("duplicate issue id = %d, want same as original %d", dup.IssueID, resp.IssueID)
	}
	if len(notif.adminEvents) != 0 {
		t.Fatalf("admin events after duplicate = %v, want still silent", notif.adminEvents)
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
	if issue.InstanceID != testSonarrInstanceID {
		t.Fatalf("instance_id = %q, want %q", issue.InstanceID, testSonarrInstanceID)
	}

	// A distinct category opens a NEW issue (dedupe is per scope+category).
	other, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: testSonarrInstanceID, MediaType: "tv", TmdbID: 42, SeasonNumber: 2, EpisodeNumber: 4,
		Category: CategoryBadCopy,
	})
	if err != nil {
		t.Fatalf("CreateUserIssue other category: %v", err)
	}
	if other.IssueID == resp.IssueID {
		t.Fatalf("different-category report reused issue %d, want a new one", resp.IssueID)
	}

	// Thread: the duplicate reason remains visible before later replies, with
	// oldest-first ordering preserved.
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
	if len(thread) != 3 {
		t.Fatalf("thread length = %d, want 3", len(thread))
	}
	if thread[0].AuthorKind != AuthorUser || thread[1].AuthorKind != AuthorAdmin || thread[2].AuthorKind != AuthorUser {
		t.Fatalf("thread author kinds = %q,%q,%q, want user,admin,user", thread[0].AuthorKind, thread[1].AuthorKind, thread[2].AuthorKind)
	}
	if thread[0].Body != duplicateReq.Reason {
		t.Fatalf("duplicate reason = %q, want %q", thread[0].Body, duplicateReq.Reason)
	}
	if thread[2].Body != "Thanks!" {
		t.Fatalf("reporter message body = %q, want verbatim", thread[2].Body)
	}

	// List (all + filtered) and open count.
	all, _, err := svc.ListIssues("", 0)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListIssues all = %d, want 2 open issues", len(all))
	}
	openOnly, _, err := svc.ListIssues(IssueObserving, 0)
	if err != nil {
		t.Fatalf("ListIssues(open): %v", err)
	}
	if len(openOnly) != 2 {
		t.Fatalf("ListIssues(open) = %d, want 2", len(openOnly))
	}
	if n, err := svc.OpenIssueCount(); err != nil || n != 0 {
		t.Fatalf("OpenIssueCount = %d (err %v), want 0 passive", n, err)
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
	if n, err := svc.OpenIssueCount(); err != nil || n != 0 {
		t.Fatalf("OpenIssueCount after dismiss = %d (err %v), want 0", n, err)
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

func TestDuplicateReportReasonIsConsumedByActiveRun(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	request := &CreateIssueRequest{
		InstanceID: testSonarrInstanceID,
		MediaType:  "tv",
		TmdbID:     314,
		Category:   CategoryWrongAudio,
		Reason:     "The dialogue is out of sync.",
		Title:      "Active Show",
	}
	created, err := svc.CreateUserIssue(reporterID, request)
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	runRes, err := svc.db.Exec(
		`INSERT INTO agent_runs (issue_id, trigger, status, model, transcript_json)
		 VALUES (?, 'user_report', ?, 'test', '[]')`,
		created.IssueID, runStatusRunning,
	)
	if err != nil {
		t.Fatalf("seed active run: %v", err)
	}
	runID, _ := runRes.LastInsertId()
	if _, err := svc.db.Exec(
		"UPDATE issues SET status = ?, active_run_id = ? WHERE id = ?",
		IssueInvestigating, runID, created.IssueID,
	); err != nil {
		t.Fatalf("claim issue: %v", err)
	}

	second := *request
	second.Reason = "New detail: only the Spanish audio track is affected."
	duplicate, err := svc.CreateUserIssue(reporterID, &second)
	if err != nil {
		t.Fatalf("CreateUserIssue duplicate: %v", err)
	}
	if duplicate.IssueID != created.IssueID || duplicate.Status != IssueInvestigating {
		t.Fatalf("duplicate response = %+v, want active issue %d", duplicate, created.IssueID)
	}
	issue, err := svc.GetIssue(created.IssueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Occurrences != 2 {
		t.Fatalf("occurrences = %d, want 2", issue.Occurrences)
	}

	runner := &Runner{db: svc.db, svc: svc}
	state := &loopState{runID: runID}
	changed, err := runner.syncThreadUpdates(state, created.IssueID)
	if err != nil {
		t.Fatalf("syncThreadUpdates: %v", err)
	}
	if !changed {
		t.Fatal("active run did not observe duplicate report message")
	}
	encoded, _ := json.Marshal(state.history)
	if !strings.Contains(string(encoded), second.Reason) {
		t.Fatalf("active transcript omitted duplicate reason: %s", encoded)
	}
	if threadCursor(state.history) == 0 {
		t.Fatal("active transcript did not persist an issue-message cursor")
	}
}

// TestMovieIssueScopeNormalized confirms a movie report stores season/episode 0
// regardless of what the client sent (movies have no season scope).
func TestMovieIssueScopeNormalized(t *testing.T) {
	svc, _, reporterID := setupTestService(t)

	resp, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: testRadarrInstanceID, MediaType: "movie", TmdbID: 7, SeasonNumber: 3, EpisodeNumber: 9,
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
// category before any row is written; a non-admin's instance errors speak
// requester vocabulary (no existence oracle), while an admin keeps the
// precise diagnostics — the same shape request routing uses.
func TestCreateUserIssueValidation(t *testing.T) {
	svc, _, reporterID := setupTestService(t)

	if _, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: testRadarrInstanceID, MediaType: "podcast", TmdbID: 1, Category: CategoryOther}); err == nil {
		t.Fatal("expected error for unsupported media type")
	}
	// Music passed the media-type gate in the issue-parity wave. This call
	// still fails — the fixture instance is a Radarr, not a music library —
	// but it must no longer be refused as an unsupported type.
	if _, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: testRadarrInstanceID, MediaType: "music", TmdbID: 1, Category: CategoryOther}); err == nil || strings.Contains(err.Error(), "unsupported media type") {
		t.Fatalf("music must pass the media-type gate (got %v)", err)
	}
	if _, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: testRadarrInstanceID, MediaType: "movie", TmdbID: 1, Category: "nonsense"}); err == nil {
		t.Fatal("expected error for invalid category")
	}
	if _, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{MediaType: "movie", TmdbID: 1, Category: CategoryOther}); err == nil || !strings.Contains(err.Error(), "instance_id is required") {
		t.Fatalf("missing instance_id error = %v", err)
	}
	// A non-admin's unknown or wrong-type instance reads as forbidden — the
	// message must not confirm what exists outside their visible set.
	if _, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: "missing", MediaType: "movie", TmdbID: 1, Category: CategoryOther}); !errors.Is(err, ErrInstanceForbidden) {
		t.Fatalf("unknown instance_id error = %v, want ErrInstanceForbidden", err)
	}
	if _, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: testSonarrInstanceID, MediaType: "movie", TmdbID: 1, Category: CategoryOther}); !errors.Is(err, ErrInstanceForbidden) {
		t.Fatalf("movie report with Sonarr instance error = %v, want ErrInstanceForbidden", err)
	}
	if _, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{InstanceID: testRadarrInstanceID, MediaType: "tv", TmdbID: 1, Category: CategoryOther}); !errors.Is(err, ErrInstanceForbidden) {
		t.Fatalf("TV report with Radarr instance error = %v, want ErrInstanceForbidden", err)
	}
	// A rowless user sees only the default-chain library; a sibling is
	// forbidden even though it exists.
	rowless := seedUser(t, svc.db, "rowless")
	if _, err := svc.CreateUserIssue(rowless, &CreateIssueRequest{InstanceID: testRadarrInstanceID2, MediaType: "movie", TmdbID: 1, Category: CategoryOther}); !errors.Is(err, ErrInstanceForbidden) {
		t.Fatalf("ungranted sibling report error = %v, want ErrInstanceForbidden", err)
	}
	if _, err := svc.CreateUserIssue(rowless, &CreateIssueRequest{InstanceID: testRadarrInstanceID, MediaType: "movie", TmdbID: 1, Category: CategoryOther}); err != nil {
		t.Fatalf("default-library report should pass access: %v", err)
	}

	// Admins bypass the access check and keep the precise diagnostics.
	adminID := seedUser(t, svc.db, "boss")
	if _, err := svc.db.Exec("UPDATE users SET role = 'admin' WHERE id = ?", adminID); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	if _, err := svc.CreateUserIssue(adminID, &CreateIssueRequest{InstanceID: "missing", MediaType: "movie", TmdbID: 1, Category: CategoryOther}); err == nil || !strings.Contains(err.Error(), "instance not found") {
		t.Fatalf("admin unknown instance_id error = %v", err)
	}
	if _, err := svc.CreateUserIssue(adminID, &CreateIssueRequest{InstanceID: testSonarrInstanceID, MediaType: "movie", TmdbID: 1, Category: CategoryOther}); err == nil || !strings.Contains(err.Error(), "not a radarr") {
		t.Fatalf("admin movie report with Sonarr instance error = %v", err)
	}
	if _, err := svc.CreateUserIssue(adminID, &CreateIssueRequest{InstanceID: testRadarrInstanceID, MediaType: "tv", TmdbID: 1, Category: CategoryOther}); err == nil || !strings.Contains(err.Error(), "not a sonarr") {
		t.Fatalf("admin TV report with Radarr instance error = %v", err)
	}
}

// TestUserIssueDedupeIncludesInstance proves identical media/category reports
// against two Radarr installations remain distinct incidents.
// ISS-005: The same title on distinct instances remains distinct incidents.
func TestUserIssueDedupeIncludesInstance(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	first, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: testRadarrInstanceID, MediaType: "movie", TmdbID: 77, Category: CategoryBadCopy,
	})
	if err != nil {
		t.Fatalf("create first issue: %v", err)
	}
	second, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: testRadarrInstanceID2, MediaType: "movie", TmdbID: 77, Category: CategoryBadCopy,
	})
	if err != nil {
		t.Fatalf("create second issue: %v", err)
	}
	if first.IssueID == second.IssueID {
		t.Fatalf("different instances deduped to issue %d", first.IssueID)
	}
}

func TestAgentAuthoredTextLimits(t *testing.T) {
	svc, _, reporterID := setupTestService(t)
	created, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: testRadarrInstanceID, MediaType: "movie", TmdbID: 1, Category: CategoryOther,
	})
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	tooLong := strings.Repeat("x", maxIssueDetailBytes+1)
	if err := svc.PostIssueMessage(context.Background(), created.IssueID, tooLong); err == nil {
		t.Fatal("oversized agent message was accepted")
	}
	if err := svc.ConcludeIssue(context.Background(), created.IssueID, IssueResolved, tooLong); err == nil {
		t.Fatal("oversized resolution was accepted")
	}
	question := strings.Repeat("q", maxIssueReplyBytes+1)
	if ok, err := svc.AskReporter(context.Background(), created.IssueID, question, "tool-1"); err == nil || ok {
		t.Fatalf("oversized reporter question = ok %v err %v", ok, err)
	}
}

// TestSettingsRoundTrip proves the remediation settings persist and reload
// (mirroring request_settings), defaults apply for an unset blob, and out-of-
// range bound fields normalize while legacy provider/model values are cleared.
func TestSettingsRoundTrip(t *testing.T) {
	svc, _, _ := setupTestService(t)

	// Defaults before anything is stored.
	d := svc.Settings()
	if d.Enabled || d.AutoDispatch || !d.AllowReporting {
		t.Fatalf("default switches = %+v, want enabled=false auto_dispatch=false allow_reporting=true", d)
	}
	if !d.MarkResolvedAsRead {
		t.Fatalf("default mark_resolved_as_read = false, want true (resolved issues marked read by default)")
	}
	if d.Mode != ModeSupervised {
		t.Fatalf("default mode = %q, want supervised", d.Mode)
	}
	if d.Provider != "" || d.Model != "" {
		t.Fatalf("default provider/model = %q/%q, want empty compatibility fields", d.Provider, d.Model)
	}
	if d.ModelOverride != "" || d.ModelOverrideProvider != "" {
		t.Fatalf("default model override = %q/%q, want empty", d.ModelOverrideProvider, d.ModelOverride)
	}
	if d.MaxUserWaitHours != 72 {
		t.Fatalf("default max_user_wait_hours = %d, want 72", d.MaxUserWaitHours)
	}

	// Round-trip a populated settings value. Provider and Model remain on the wire
	// for older clients but are normalized away; the provider-bound model override
	// is the only supported remediation-specific AI selection.
	in := Settings{
		Enabled:                  true,
		AutoDispatch:             true,
		AllowReporting:           false,
		MarkResolvedAsRead:       false, // explicit false must override the true default and round-trip
		Mode:                     ModeInvestigateOnly,
		Provider:                 "openai",
		Model:                    "gpt-x",
		ModelOverride:            "gpt-remediation",
		ModelOverrideProvider:    "openai",
		MaxSteps:                 20,
		MaxTurnTokens:            8000,
		MaxWallClockSecs:         600,
		DailyRunCap:              10,
		CircuitBreakerGiveups:    3,
		MaxUserWaitHours:         48,
		ObservationMinMinutes:    20,
		ObservationQuietMinutes:  8,
		ObservationSettleMinutes: 4,
	}
	saved, err := svc.SetSettings(in)
	if err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	want := in
	want.Provider = ""
	want.Model = ""
	if saved != want {
		t.Fatalf("SetSettings returned %+v, want %+v", saved, want)
	}
	got := svc.Settings()
	if got != want {
		t.Fatalf("reloaded settings = %+v, want %+v", got, want)
	}

	// Out-of-range bounds normalize back to defaults; an unknown mode too. A
	// model-only legacy write is cleared even without a legacy provider value.
	norm, err := svc.SetSettings(Settings{Mode: "bogus", Model: "stale-model", MaxSteps: 0})
	if err != nil {
		t.Fatalf("SetSettings normalize: %v", err)
	}
	def := Defaults()
	if norm.Mode != def.Mode {
		t.Fatalf("normalized mode = %q, want %q", norm.Mode, def.Mode)
	}
	if norm.MaxSteps != def.MaxSteps {
		t.Fatalf("normalized max steps = %d, want %d", norm.MaxSteps, def.MaxSteps)
	}
	if norm.Provider != "" || norm.Model != "" {
		t.Fatalf("normalized provider/model = %q/%q, want empty compatibility fields", norm.Provider, norm.Model)
	}
	if norm.ModelOverride != "" || norm.ModelOverrideProvider != "" {
		t.Fatalf("normalized model override = %q/%q, want empty", norm.ModelOverrideProvider, norm.ModelOverride)
	}

	high, err := svc.SetSettings(Settings{
		Mode: ModeSupervised, MaxSteps: 1 << 30, MaxTurnTokens: 1 << 30,
		MaxWallClockSecs: 1 << 30, DailyRunCap: 1 << 30,
		CircuitBreakerGiveups: 1 << 30, MaxUserWaitHours: 1 << 30,
		ObservationMinMinutes: 1 << 30, ObservationQuietMinutes: 1 << 30,
		ObservationSettleMinutes: 1 << 30,
	})
	if err != nil {
		t.Fatalf("SetSettings high values: %v", err)
	}
	if high.MaxSteps != maxConfiguredSteps || high.MaxTurnTokens != maxConfiguredTurnTokens ||
		high.MaxWallClockSecs != maxConfiguredWallClockSecs || high.DailyRunCap != maxConfiguredDailyRuns ||
		high.CircuitBreakerGiveups != maxConfiguredBreakerGiveups || high.MaxUserWaitHours != maxConfiguredUserWaitHours ||
		high.ObservationMinMinutes != maxObservationMinMinutes || high.ObservationQuietMinutes != maxObservationQuietMinutes ||
		high.ObservationSettleMinutes != maxObservationSettleMinutes {
		t.Fatalf("high settings were not safely capped: %+v", high)
	}
}

func TestLegacyPriceSettingsAreNotExposedOrRepersisted(t *testing.T) {
	svc, _, _ := setupTestService(t)
	legacy := `{"enabled":true,"max_cost_micros":500000,"daily_cost_ceiling_micros":5000000}`
	if _, err := svc.db.Exec(
		"INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
		remediationSettingsKey, legacy,
	); err != nil {
		t.Fatalf("seed legacy remediation settings: %v", err)
	}

	settings := svc.Settings()
	wire, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if strings.Contains(string(wire), "cost") {
		t.Fatalf("settings JSON still exposes legacy price fields: %s", wire)
	}
	if _, err := svc.SetSettings(settings); err != nil {
		t.Fatalf("repersist settings: %v", err)
	}
	var stored string
	if err := svc.db.QueryRow(
		"SELECT value FROM settings WHERE key = ?", remediationSettingsKey,
	).Scan(&stored); err != nil {
		t.Fatalf("load repersisted settings: %v", err)
	}
	if strings.Contains(stored, "cost") {
		t.Fatalf("legacy price fields survived repersist: %s", stored)
	}
}

func TestLegacyAutonomySettingsCompatibility(t *testing.T) {
	svc, _, _ := setupTestService(t)

	for _, tc := range []struct {
		name     string
		autonomy string
		wantMode string
	}{
		{name: "investigate only remains restrictive", autonomy: "investigate_only", wantMode: ModeInvestigateOnly},
		{name: "propose becomes supervised", autonomy: "propose", wantMode: ModeSupervised},
		{name: "auto safe becomes supervised", autonomy: "auto_safe", wantMode: ModeSupervised},
		{name: "supervised compatibility", autonomy: "supervised", wantMode: ModeSupervised},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy := `{"enabled":true,"autonomy":"` + tc.autonomy + `"}`
			if _, err := svc.db.Exec(
				"INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
				remediationSettingsKey, legacy,
			); err != nil {
				t.Fatalf("store legacy settings: %v", err)
			}

			got := svc.Settings()
			if got.Mode != tc.wantMode {
				t.Fatalf("legacy autonomy %q loaded as mode %q, want %q", tc.autonomy, got.Mode, tc.wantMode)
			}
		})
	}
}

func TestLegacyInvestigateOnlySubmissionPersistsModeOnly(t *testing.T) {
	svc, _, _ := setupTestService(t)

	var submitted Settings
	if err := json.Unmarshal([]byte(`{"enabled":true,"autonomy":"investigate_only"}`), &submitted); err != nil {
		t.Fatalf("decode legacy settings submission: %v", err)
	}
	if submitted.Mode != ModeInvestigateOnly {
		t.Fatalf("legacy investigate_only submission decoded as %q; proposals would be enabled", submitted.Mode)
	}

	saved, err := svc.SetSettings(submitted)
	if err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if saved.Mode != ModeInvestigateOnly {
		t.Fatalf("saved mode = %q, want investigate_only", saved.Mode)
	}

	var stored string
	if err := svc.db.QueryRow("SELECT value FROM settings WHERE key = ?", remediationSettingsKey).Scan(&stored); err != nil {
		t.Fatalf("load stored settings JSON: %v", err)
	}
	if strings.Contains(stored, `"autonomy"`) {
		t.Fatalf("stored settings still emit legacy autonomy: %s", stored)
	}
	if !strings.Contains(stored, `"mode":"investigate_only"`) {
		t.Fatalf("stored settings do not emit restrictive mode: %s", stored)
	}
}

func TestExplicitModeTakesPrecedenceOverLegacyAutonomy(t *testing.T) {
	var settings Settings
	if err := json.Unmarshal([]byte(`{"mode":"investigate_only","autonomy":"auto_safe"}`), &settings); err != nil {
		t.Fatalf("decode mixed settings: %v", err)
	}
	if settings.Mode != ModeInvestigateOnly {
		t.Fatalf("explicit mode = %q, want investigate_only", settings.Mode)
	}
}

// The issue list is the one read that grows forever: closed history only
// accumulates, and the observation pipeline closes incidents nobody was ever
// asked to look at. History is therefore capped and open work never is — a
// client filtering "needs attention" must be filtering a complete set, and it
// must be able to say how much history it is not showing.
func TestListIssuesCapsHistoryAndNeverOpenWork(t *testing.T) {
	svc, _, _ := setupTestService(t)
	for i := 0; i < 5; i++ {
		if _, err := svc.db.Exec(
			`INSERT INTO issues (source, status, media_type, tmdb_id, title, detail, updated_at)
			 VALUES ('auto', ?, 'movie', ?, 'open', 'd', datetime('now', ?))`,
			IssueNeedsAdmin, 100+i, fmt.Sprintf("-%d minutes", i),
		); err != nil {
			t.Fatalf("seed open issue: %v", err)
		}
	}
	for i := 0; i < 10; i++ {
		if _, err := svc.db.Exec(
			`INSERT INTO issues (source, status, media_type, tmdb_id, title, detail, closed_at, updated_at)
			 VALUES ('auto', ?, 'movie', ?, 'closed', 'd', CURRENT_TIMESTAMP, datetime('now', ?))`,
			IssueResolved, 200+i, fmt.Sprintf("-%d minutes", i),
		); err != nil {
			t.Fatalf("seed closed issue: %v", err)
		}
	}

	got, closedTotal, err := svc.ListIssues("", 3)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	var open, closed []Issue
	for _, iss := range got {
		if iss.ClosedAt == nil {
			open = append(open, iss)
		} else {
			closed = append(closed, iss)
		}
	}
	if len(open) != 5 {
		t.Fatalf("open issues = %d, want all 5 — actionable work is never capped", len(open))
	}
	if len(closed) != 3 {
		t.Fatalf("closed issues = %d, want the 3 the cap allows", len(closed))
	}
	if closedTotal != 10 {
		t.Fatalf("closedTotal = %d, want 10 so the client can say what it is not showing", closedTotal)
	}
	// Newest-updated first, so a cap keeps the most recent history.
	if closed[0].TmdbID != 200 || closed[2].TmdbID != 202 {
		t.Fatalf("closed slice = %d..%d, want the newest three", closed[0].TmdbID, closed[2].TmdbID)
	}
	// An explicit open-status read stays uncapped for the same reason.
	needing, _, err := svc.ListIssues(IssueNeedsAdmin, 2)
	if err != nil {
		t.Fatalf("ListIssues(needs_admin): %v", err)
	}
	if len(needing) != 5 {
		t.Fatalf("needs_admin = %d, want 5 — a cap must never hide open work", len(needing))
	}
}
