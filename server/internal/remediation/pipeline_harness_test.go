package remediation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// The real-pipeline harness: detection through typed verification with no test
// seams in the path. The fake is an HTTP Sonarr; everything between it and the
// assertions is production code — fetchQueueSnapshot's parse and Import Doctor
// verdict, observation promotion windows, every recovery preflight, the REAL
// mcp.ToolServer (scope injection, typed verification), ProposeAction's
// validation, the approvals decision core, the REAL Executor's arr mutations,
// resume, and the server-side recovery proofs. The runner-level stubs
// (recoveryProbe, fakeToolHost) exist for focused unit tests; this file exists
// because those seams are exactly where the pre-air preflight defect hid.

// pipelineHarness bundles the service, the fake Sonarr, and a Runner factory
// that shares one scripted-turn sequence across Run and Resume — the script
// simply continues where the previous segment stopped, the way a real model
// transcript does.
type pipelineHarness struct {
	svc      *Service
	notifier *fakeNotifier
	fake     *preAirFake
}

func newPipelineHarness(t *testing.T) *pipelineHarness {
	t.Helper()
	svc, notifier, fake := setupPreAirService(t)
	if _, err := svc.SetSettings(Settings{
		Enabled: true, AutoDispatch: true, Mode: ModeSupervised,
		MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, DailyRunCap: 50,
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	if _, err := svc.db.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin', '', 'admin')"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return &pipelineHarness{svc: svc, notifier: notifier, fake: fake}
}

// runner builds a Runner over the REAL tool server — reads and agent tools
// dispatch into production mcp code wired to the harness service and registry.
func (h *pipelineHarness) runner(script *scriptedTurn) *Runner {
	toolServer := mcp.NewToolServer(nil, nil, h.svc.registry, nil)
	toolServer.SetIssueStore(h.svc)
	return &Runner{
		db:         h.svc.db,
		svc:        h.svc,
		toolServer: toolServer,
		turns:      scriptedTurnResolver(script),
		procToken:  "test",
	}
}

// observe ingests the fake's CURRENT queue through the production fetch+parse
// path at an explicit clock, exactly as the poller/sweeper feed production.
func (h *pipelineHarness) observe(t *testing.T, at time.Time) {
	t.Helper()
	items, err := h.svc.fetchQueueSnapshot("sonarr", preAirSonarrID)
	if err != nil {
		t.Fatalf("fetch queue snapshot: %v", err)
	}
	if err := h.svc.observeQueueSnapshot("sonarr", preAirSonarrID, items, at); err != nil {
		t.Fatalf("observe queue snapshot: %v", err)
	}
}

func toolCall(id, name, input string) ai.TranscriptMessage {
	return ai.TranscriptMessage{Role: ai.RoleAssistant, Content: []ai.TranscriptBlock{{
		Type: ai.BlockToolUse, ID: id, Name: name, Input: json.RawMessage(input),
	}}}
}

// stalledUpgradeQueueRow is a Sonarr queue row for a stalled torrent that is an
// UPGRADE: the episode already holds a file, which is what makes the abandoned
// repair provable later (the unchanged library file IS the success).
func stalledUpgradeQueueRow(queueID int, downloadID string, added time.Time) map[string]any {
	return map[string]any{
		"id":        queueID,
		"seriesId":  28,
		"episodeId": 28203,
		"series":    map[string]any{"id": 28, "title": "Futurama", "tvdbId": 73871, "tmdbId": 615},
		"episode": map[string]any{
			"id": 28203, "seriesId": 28, "seasonNumber": 2, "episodeNumber": 3,
			"episodeFileId": 50203, "hasFile": true, "title": "S02E03",
		},
		"episodeHasFile":        true,
		"title":                 "Futurama.S02E03.1080p.WEB-DL",
		"status":                "queued",
		"trackedDownloadStatus": "warning",
		"trackedDownloadState":  "downloading",
		"errorMessage":          "stalled with no connections",
		"downloadId":            downloadID,
		"protocol":              "torrent",
		"size":                  1000000000.0,
		"sizeleft":              500000000.0,
		"added":                 added.Format(time.RFC3339),
	}
}

// stalledUpgradeQueueRowFor is stalledUpgradeQueueRow with a chosen episode,
// for scenarios needing two incidents of the same problem class.
func stalledUpgradeQueueRowFor(queueID int, downloadID string, episode int, added time.Time) map[string]any {
	row := stalledUpgradeQueueRow(queueID, downloadID, added)
	epID := 28200 + episode
	fileID := 50200 + episode
	row["episodeId"] = epID
	row["episode"] = map[string]any{
		"id": epID, "seriesId": 28, "seasonNumber": 2, "episodeNumber": episode,
		"episodeFileId": fileID, "hasFile": true, "title": fmt.Sprintf("S02E%02d", episode),
	}
	return row
}

// TestPipelineStalledUpgradeFullLoop drives the complete production loop for a
// stalled upgrade: HTTP intake + Doctor verdict → silent observation →
// promotion → real preflight → real tool reads → real proposal → real approval
// core → real Executor mutation against the fake arr → resume → typed
// queue-target verification → upgradeAbandonProven → resolved. One incident,
// one approval, zero test seams.
func TestPipelineStalledUpgradeFullLoop(t *testing.T) {
	h := newPipelineHarness(t)

	// Season 2 is ordinary: E3 aired three weeks ago and holds file 50203
	// (imported after air). The queue holds a stalled upgrade for that episode.
	episodes, files := buildPreAirSeason(28, 2, []preAirEpisode{
		{number: 3, airsIn: -21 * 24 * time.Hour, hasFile: true},
	})
	h.fake.setLibrary(episodes, files)
	base := time.Now().UTC().Add(-30 * time.Minute)
	// Added well before the first observation: a torrent that has been stalled
	// for half an hour is past the stalled dwell, so the incident is born on
	// first sighting exactly as this pipeline expects.
	h.fake.setQueue([]map[string]any{stalledUpgradeQueueRow(41, "TORRENTABC123", base.Add(-30*time.Minute))})

	// Intake twice through the production path: first sighting starts the quiet
	// observation; the second, past the min/quiet windows, promotes it.
	h.observe(t, base)
	h.observe(t, base.Add(11*time.Minute))

	issue := soleIssue(t, h.svc)
	if issue.Status != IssueOpen {
		t.Fatalf("issue after promotion = %q, want %q", issue.Status, IssueOpen)
	}
	problemKind := issueProblemKind(t, h.svc, issue.ID)
	if problemKind == "" {
		t.Fatalf("promoted issue has no problem_kind; the Doctor verdict was lost")
	}

	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("r1", "get_queue", `{}`),
		toolCall("p1", mcp.ToolProposeAction, `{"issue_id":0,"kind":"remediate_queue","params":{"media_type":"tv","queue_id":41,"action":"blocklist_only"},"rationale":"Stalled with no seeders; the library already holds a copy nobody asked to replace."}`),
		toolCall("r2", "get_queue", `{}`),
		toolCall("c1", mcp.ToolConcludeIssue, `{"issue_id":0,"status":"resolved","resolution":"The stalled upgrade was removed and blocklisted; the existing copy is intact."}`),
	}}
	r := h.runner(script)

	if err := r.Run(context.Background(), issue.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	after := soleIssue(t, h.svc)
	if after.Status != IssueAwaitingApproval {
		t.Fatalf("issue after run = %q, want %q", after.Status, IssueAwaitingApproval)
	}

	var actionID int64
	if err := h.svc.db.QueryRow(
		"SELECT id FROM agent_actions WHERE issue_id = ? AND status = ?", issue.ID, ActionProposed,
	).Scan(&actionID); err != nil {
		t.Fatalf("find proposed action: %v", err)
	}

	act, err := h.svc.ApproveAction(1, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if act.Status != ActionExecuted {
		t.Fatalf("approved action status = %q (result %v), want %q", act.Status, act.ResultText, ActionExecuted)
	}
	deletes := h.fake.queueDeletesSeen()
	if len(deletes) != 1 || !strings.HasPrefix(deletes[0], "/api/v3/queue/41?") ||
		!strings.Contains(deletes[0], "blocklist=true") || !strings.Contains(deletes[0], "skipRedownload=true") {
		t.Fatalf("arr queue mutations = %v, want one blocklist_only delete of row 41", deletes)
	}
	var targetDownload string
	if err := h.svc.db.QueryRow(
		"SELECT COALESCE(target_download_id,'') FROM agent_actions WHERE id = ?", actionID,
	).Scan(&targetDownload); err != nil {
		t.Fatalf("read target_download_id: %v", err)
	}
	if targetDownload != "TORRENTABC123" {
		t.Fatalf("target_download_id = %q, want the dispatched download identity", targetDownload)
	}

	// The executor emptied the queue, and production deliberately waits out the
	// absence settle window before any conclusion — the first complete no-match
	// snapshot is never permission to close. Drive that real timeline: the first
	// absent snapshot starts settling (suspending the issue back to recovering),
	// the timer is backdated past the window, and the next snapshot re-promotes
	// the issue for the staged resume.
	h.observe(t, time.Now().UTC())
	if _, err := h.svc.db.Exec(
		"UPDATE issue_observations SET settling_since = ? WHERE issue_id = ?",
		time.Now().UTC().Add(-3*time.Minute), issue.ID,
	); err != nil {
		t.Fatalf("backdate settle window: %v", err)
	}
	h.observe(t, time.Now().UTC())
	if mid := soleIssue(t, h.svc); mid.Status != IssueOpen {
		t.Fatalf("issue after settled absence = %q, want re-promoted %q", mid.Status, IssueOpen)
	}

	// The staged resume survives its own fix's success: re-promotion lands the
	// issue at `open`, and the resume claim accepts it — the preserved
	// transcript continues (scoped read, then conclude), and
	// upgradeAbandonProven supplies the server-side proof: the library file is
	// unchanged and the server itself dispatched blocklist_only. One incident,
	// one approval, ONE run.
	if err := r.Resume(context.Background(), issue.ID); err != nil {
		t.Fatalf("Resume after settle: %v", err)
	}

	final := soleIssue(t, h.svc)
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionArrStateCleared {
		t.Fatalf("final issue = status %q / kind %q, want %q / %q",
			final.Status, final.ResolutionKind, IssueResolved, ResolutionArrStateCleared)
	}
	count, runStatus := agentRunRows(t, h.svc, issue.ID)
	if count != 1 || runStatus != "succeeded" {
		t.Fatalf("agent_runs = %d rows (last %q), want the ONE resumed, succeeded run", count, runStatus)
	}
}

// TestPipelinePreAirSeasonRepairFullLoop drives the flagship season repair the
// way production runs it: the webhook detector opens ONE season issue, the
// agent investigates and proposes delete_media_files, ONE approval deletes all
// nine impossible files and marks their grabs failed at the real arr boundary,
// and — with the failed-download policy ON — the service owns the replacement
// search, so Cantinarr posts no command of its own (the fake treats any
// /command call as an unexpected request). The issue then closes through the
// class's own recovery proof at the resume preflight: nothing unaired holds a
// file. This is ISS-044's hermetic twin.
func TestPipelinePreAirSeasonRepairFullLoop(t *testing.T) {
	h := newPipelineHarness(t)

	// The default library IS the incident: nine files imported thirteen days
	// before their episodes air. History carries the grab+import pair per file,
	// which is the join the blocklist walk stands releases down by.
	var hist []map[string]any
	for n := 1; n <= 9; n++ {
		downloadID := fmt.Sprintf("NZB-%d", n)
		epID := 28*1000 + 11*100 + n
		hist = append(hist,
			map[string]any{"id": 9000 + n, "eventType": "downloadFolderImported", "episodeId": epID, "downloadId": downloadID},
			map[string]any{"id": 8000 + n, "eventType": "grabbed", "episodeId": epID, "downloadId": downloadID},
		)
	}
	h.fake.setSeriesHistory(hist)

	if err := h.svc.recordPreAirSeason(preAirSonarrID, 73871, 615, 11, "Futurama"); err != nil {
		t.Fatalf("record pre-air season: %v", err)
	}
	issue := soleIssue(t, h.svc)
	if issue.Status != IssueOpen || issueProblemKind(t, h.svc, issue.ID) == "" {
		t.Fatalf("pre-air issue = status %q kind %q, want open with a problem kind", issue.Status, issueProblemKind(t, h.svc, issue.ID))
	}

	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("r1", "get_episode_timeline", `{}`),
		toolCall("p1", mcp.ToolProposeAction, `{"issue_id":0,"kind":"delete_media_files","params":{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[1,2,3,4,5,6,7,8,9],"blocklist":true},"rationale":"Nine files imported thirteen days before their episodes air cannot be those episodes."}`),
	}}
	r := h.runner(script)

	if err := r.Run(context.Background(), issue.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if after := soleIssue(t, h.svc); after.Status != IssueAwaitingApproval {
		t.Fatalf("issue after run = %q, want %q", after.Status, IssueAwaitingApproval)
	}

	var actionID int64
	if err := h.svc.db.QueryRow(
		"SELECT id FROM agent_actions WHERE issue_id = ? AND status = ?", issue.ID, ActionProposed,
	).Scan(&actionID); err != nil {
		t.Fatalf("find proposed action: %v", err)
	}
	act, err := h.svc.ApproveAction(1, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if act.Status != ActionExecuted {
		t.Fatalf("approved action = %q (result %v), want %q", act.Status, act.ResultText, ActionExecuted)
	}

	fileDeletes, failedGrabs := h.fake.mutationsSeen()
	if len(fileDeletes) != 9 || len(failedGrabs) != 9 {
		t.Fatalf("arr mutations = %d file deletes / %d failed grabs, want 9 / 9", len(fileDeletes), len(failedGrabs))
	}

	// The resume preflight closes the issue through preAirRepairProven: the
	// live season no longer holds any unaired file. The staged resume itself is
	// never consumed — this class recovers by its own proof, not by the model
	// narrating one.
	if err := r.Resume(context.Background(), issue.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	final := soleIssue(t, h.svc)
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionArrStateCleared {
		t.Fatalf("final issue = status %q / kind %q, want %q / %q",
			final.Status, final.ResolutionKind, IssueResolved, ResolutionArrStateCleared)
	}
	// The aggregate close aborts the staged resume the proof outran — a truthful
	// terminal state, not a dangling handoff.
	if count, last := agentRunRows(t, h.svc, issue.ID); count != 1 || last != "aborted" {
		t.Fatalf("agent_runs = %d rows (last %q), want one investigation aborted by its own proof", count, last)
	}
}

// TestPipelineUserComplaintReporterCloseFullLoop drives the reported path: a
// household member reports wrong content on an episode whose download finished
// weeks ago (the queue is EMPTY — the expected reading for a content
// complaint, never a dead end), the agent diagnoses from the library, ONE
// approval deletes the file and stands its grab down, and the REPORTER — never
// an admin adjudicating content they haven't watched — closes their own
// report. A user issue can never machine-close: the typed proofs are refused
// for subjective reports, so reporter_confirmed is the only terminal this
// test's happy path may reach.
func TestPipelineUserComplaintReporterCloseFullLoop(t *testing.T) {
	h := newPipelineHarness(t)
	if _, err := h.svc.db.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (2, 'viewer', '', 'user')"); err != nil {
		t.Fatalf("seed reporter: %v", err)
	}

	// S02E03 aired three weeks ago and holds file 50203, imported post-air —
	// a perfectly healthy-looking library entry that happens to be the wrong
	// content. Only the person who watched it can know that.
	episodes, files := buildPreAirSeason(28, 2, []preAirEpisode{
		{number: 3, airsIn: -21 * 24 * time.Hour, hasFile: true},
	})
	h.fake.setLibrary(episodes, files)
	h.fake.setSeriesHistory([]map[string]any{
		{"id": 9203, "eventType": "downloadFolderImported", "episodeId": 28203, "downloadId": "NZB-WRONG"},
		{"id": 8203, "eventType": "grabbed", "episodeId": 28203, "downloadId": "NZB-WRONG"},
	})

	resp, err := h.svc.CreateUserIssue(2, &CreateIssueRequest{
		InstanceID: preAirSonarrID, MediaType: "tv", TmdbID: 615, TvdbID: 73871,
		SeasonNumber: 2, EpisodeNumber: 3, Category: CategoryWrongContent,
		Reason: "This is a different episode entirely.", Title: "Futurama",
	})
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	issueID := resp.IssueID

	// A content complaint starts in the same quiet observation every report
	// does. Its scope was never in the queue, so promotion runs the absence
	// path: backdate the report's clock past the min window, let one empty
	// snapshot start settling, backdate the settle, and the next promotes.
	if _, err := h.svc.db.Exec(
		"UPDATE issue_observations SET first_seen_at = ?, updated_at = ? WHERE issue_id = ?",
		time.Now().UTC().Add(-30*time.Minute), time.Now().UTC(), issueID,
	); err != nil {
		t.Fatalf("backdate observation: %v", err)
	}
	h.observe(t, time.Now().UTC())
	if _, err := h.svc.db.Exec(
		"UPDATE issue_observations SET settling_since = ? WHERE issue_id = ?",
		time.Now().UTC().Add(-3*time.Minute), issueID,
	); err != nil {
		t.Fatalf("backdate settle: %v", err)
	}
	h.observe(t, time.Now().UTC())
	promoted, err := h.svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("load promoted issue: %v", err)
	}
	if promoted.Status != IssueOpen {
		t.Fatalf("user issue after absence settle = %q, want promoted %q", promoted.Status, IssueOpen)
	}

	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("r1", "get_history", `{}`),
		toolCall("p1", mcp.ToolProposeAction, `{"issue_id":0,"kind":"delete_media_files","params":{"media_type":"tv","tmdb_id":615,"season":2,"episodes":[3],"blocklist":true},"rationale":"The reporter watched it; the file is the wrong episode. Delete it and stand the release down."}`),
	}}
	r := h.runner(script)
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var actionID int64
	if err := h.svc.db.QueryRow(
		"SELECT id FROM agent_actions WHERE issue_id = ? AND status = ?", issueID, ActionProposed,
	).Scan(&actionID); err != nil {
		t.Fatalf("find proposed action: %v", err)
	}
	act, err := h.svc.ApproveAction(1, actionID, nil)
	if err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	if act.Status != ActionExecuted {
		t.Fatalf("approved action = %q (result %v), want %q", act.Status, act.ResultText, ActionExecuted)
	}
	fileDeletes, failedGrabs := h.fake.mutationsSeen()
	if len(fileDeletes) != 1 || len(failedGrabs) != 1 {
		t.Fatalf("arr mutations = %d deletes / %d failed grabs, want 1 / 1", len(fileDeletes), len(failedGrabs))
	}

	// The reporter's verdict is the only closure a subjective report accepts.
	loaded, err := h.svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	canConfirm, err := h.svc.CanReporterConfirmFix(loaded)
	if err != nil || !canConfirm {
		t.Fatalf("CanReporterConfirmFix = (%v, %v), want (true, nil) after an executed fix", canConfirm, err)
	}
	if err := h.svc.ReporterConfirmFix(context.Background(), issueID, 2); err != nil {
		t.Fatalf("ReporterConfirmFix: %v", err)
	}
	final, err := h.svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("load final issue: %v", err)
	}
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionReporterConfirmed {
		t.Fatalf("final issue = status %q / kind %q, want %q / %q",
			final.Status, final.ResolutionKind, IssueResolved, ResolutionReporterConfirmed)
	}
}

// runStalledUpgradeToProposal drives one stalled-upgrade incident from HTTP
// intake to a parked proposal: the reusable front half of the rule scenarios.
func runStalledUpgradeToProposal(t *testing.T, h *pipelineHarness, queueID int, downloadID string) (issueID, actionID int64) {
	t.Helper()
	// Intake at the real clock (an approval preflight may have stored a fresher
	// watermark moments ago), then age the observation ROW past the promotion
	// windows and let the next snapshot promote it.
	now := time.Now().UTC()
	h.fake.setQueue([]map[string]any{stalledUpgradeQueueRow(queueID, downloadID, now.Add(-30*time.Minute))})
	h.observe(t, now)
	if err := h.svc.db.QueryRow(
		"SELECT id FROM issues WHERE closed_at IS NULL AND source = ? ORDER BY id DESC LIMIT 1", SourceAuto,
	).Scan(&issueID); err != nil {
		t.Fatalf("find observed auto issue: %v", err)
	}
	if _, err := h.svc.db.Exec(
		`UPDATE issue_observations SET first_seen_at = ?, problem_since_at = ?, last_activity_at = ? WHERE issue_id = ?`,
		now.Add(-30*time.Minute), now.Add(-30*time.Minute), now.Add(-11*time.Minute), issueID,
	); err != nil {
		t.Fatalf("age observation: %v", err)
	}
	h.observe(t, time.Now().UTC())
	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("r1", "get_queue", `{}`),
		toolCall("p1", mcp.ToolProposeAction, fmt.Sprintf(`{"issue_id":0,"kind":"remediate_queue","params":{"media_type":"tv","queue_id":%d,"action":"blocklist_only"},"rationale":"Stalled upgrade; the library copy stays."}`, queueID)),
	}}
	if err := h.runner(script).Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run to proposal: %v", err)
	}
	if err := h.svc.db.QueryRow(
		"SELECT id FROM agent_actions WHERE issue_id = ? AND status = ?", issueID, ActionProposed,
	).Scan(&actionID); err != nil {
		t.Fatalf("find proposed action: %v", err)
	}
	return issueID, actionID
}

// TestPipelineStandingRuleAutoApproveThenRepeatGuard proves the earned-autonomy
// lane end to end, then its safety valve. Incident 1: the admin approves with
// remember, arming the (problem, remediate_queue, blocklist_only) rule.
// Incident 2 (a DIFFERENT release, same problem): the sweeper approves it with
// no human — decided_by null, auto_rule_id set — and the real Executor
// dispatches. Incident 3: the arr re-adds the SAME download the rule already
// acted on; the repeat guard refuses to replay the remedy unattended, pauses
// the rule, and leaves the proposal for a human.
func TestPipelineStandingRuleAutoApproveThenRepeatGuard(t *testing.T) {
	h := newPipelineHarness(t)
	episodes, files := buildPreAirSeason(28, 2, []preAirEpisode{
		{number: 3, airsIn: -21 * 24 * time.Hour, hasFile: true},
	})
	h.fake.setLibrary(episodes, files)

	// Incident 1: manual approve with remember arms the rule.
	issue1, action1 := runStalledUpgradeToProposal(t, h, 41, "TORRENT-AAA")
	act, err := h.svc.ApproveActionRemembering(1, action1, nil)
	if err != nil || act.Status != ActionExecuted {
		t.Fatalf("remembering approve = (%v, %v), want executed", act, err)
	}
	var ruleID int64
	var ruleStatus string
	if err := h.svc.db.QueryRow(
		"SELECT id, status FROM agent_approval_rules ORDER BY id DESC LIMIT 1",
	).Scan(&ruleID, &ruleStatus); err != nil || ruleStatus != "active" {
		t.Fatalf("armed rule = (%d, %q, %v), want an active rule", ruleID, ruleStatus, err)
	}
	if _, err := h.svc.db.Exec(
		"UPDATE issues SET status = ?, resolution_kind = ?, closed_at = CURRENT_TIMESTAMP WHERE id = ?",
		IssueResolved, ResolutionArrStateCleared, issue1,
	); err != nil {
		t.Fatalf("close incident 1: %v", err)
	}

	// Incident 2: same problem, different release. The sweep, not a human,
	// approves — and the real Executor dispatches against the fake arr.
	_, action2 := runStalledUpgradeToProposal(t, h, 42, "TORRENT-BBB")
	h.svc.sweepAutoApprovals(time.Now().UTC())
	var status2 string
	var decidedBy, autoRule *int64
	if err := h.svc.db.QueryRow(
		"SELECT status, decided_by, auto_rule_id FROM agent_actions WHERE id = ?", action2,
	).Scan(&status2, &decidedBy, &autoRule); err != nil {
		t.Fatalf("read auto-approved action: %v", err)
	}
	if status2 != ActionExecuted || decidedBy != nil || autoRule == nil || *autoRule != ruleID {
		t.Fatalf("auto approval = status %q decided_by %v rule %v, want executed by rule %d with no human", status2, decidedBy, autoRule, ruleID)
	}
	if deletes := h.fake.queueDeletesSeen(); len(deletes) != 2 {
		t.Fatalf("queue deletes after auto-dispatch = %d, want 2", len(deletes))
	}
	var issue2 int64
	if err := h.svc.db.QueryRow("SELECT issue_id FROM agent_actions WHERE id = ?", action2).Scan(&issue2); err != nil {
		t.Fatalf("read issue 2 id: %v", err)
	}

	// The arr re-adds the EXACT download the rule just acted on — the original
	// #359 loop: a title-matched blocklist misses the release and it is back in
	// the queue in seconds, on the SAME still-open issue. Re-attach it, age the
	// activity windows, re-promote, and let the agent propose the same fix.
	now := time.Now().UTC()
	h.fake.setQueue([]map[string]any{stalledUpgradeQueueRow(43, "TORRENT-BBB", now)})
	h.observe(t, now)
	if _, err := h.svc.db.Exec(
		"UPDATE issue_observations SET last_activity_at = ? WHERE issue_id = ?",
		now.Add(-11*time.Minute), issue2,
	); err != nil {
		t.Fatalf("age re-added observation: %v", err)
	}
	h.observe(t, time.Now().UTC())
	script3 := &scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("r3", "get_queue", `{}`),
		toolCall("p3", mcp.ToolProposeAction, `{"issue_id":0,"kind":"remediate_queue","params":{"media_type":"tv","queue_id":43,"action":"blocklist_only"},"rationale":"Same stall, back again."}`),
	}}
	if err := h.runner(script3).Run(context.Background(), issue2); err != nil {
		t.Fatalf("Run on re-added download: %v", err)
	}
	var action3 int64
	if err := h.svc.db.QueryRow(
		"SELECT id FROM agent_actions WHERE issue_id = ? AND status = ?", issue2, ActionProposed,
	).Scan(&action3); err != nil {
		t.Fatalf("find repeat proposal: %v", err)
	}
	h.svc.sweepAutoApprovals(time.Now().UTC())
	var status3 string
	if err := h.svc.db.QueryRow("SELECT status FROM agent_actions WHERE id = ?", action3).Scan(&status3); err != nil {
		t.Fatalf("read repeat proposal: %v", err)
	}
	if status3 != ActionProposed {
		t.Fatalf("repeat proposal = %q, want left %q for a human", status3, ActionProposed)
	}
	var pausedStatus string
	var pausedReason *string
	if err := h.svc.db.QueryRow(
		"SELECT status, paused_reason FROM agent_approval_rules WHERE id = ?", ruleID,
	).Scan(&pausedStatus, &pausedReason); err != nil {
		t.Fatalf("read rule after repeat: %v", err)
	}
	if pausedStatus != "paused" || pausedReason == nil {
		t.Fatalf("rule after repeat = (%q, %v), want paused with a reason", pausedStatus, pausedReason)
	}
}

// TestPipelineOutcomeUnknownIsAHardStop: the arr answers 500 AFTER the
// mutating request was sent, so the remote outcome is unknowable. That is a
// human-verification boundary: the action lands outcome_unknown, is never
// retried, and the model is never resumed over state the first attempt may
// already have changed — the parked run stays parked for an admin.
func TestPipelineOutcomeUnknownIsAHardStop(t *testing.T) {
	h := newPipelineHarness(t)
	episodes, files := buildPreAirSeason(28, 2, []preAirEpisode{
		{number: 3, airsIn: -21 * 24 * time.Hour, hasFile: true},
	})
	h.fake.setLibrary(episodes, files)
	issueID, actionID := runStalledUpgradeToProposal(t, h, 44, "TORRENT-CCC")
	h.fake.setQueueDeleteStatus(500)

	if _, err := h.svc.ApproveAction(1, actionID, nil); err != nil {
		t.Logf("approve surfaced error (durable outcome still recorded): %v", err)
	}
	var status string
	if err := h.svc.db.QueryRow("SELECT status FROM agent_actions WHERE id = ?", actionID).Scan(&status); err != nil {
		t.Fatalf("read action: %v", err)
	}
	if status != ActionOutcomeUnknown {
		t.Fatalf("action after mid-mutation 500 = %q, want %q", status, ActionOutcomeUnknown)
	}
	if _, last := agentRunRows(t, h.svc, issueID); last == "resume_pending" || last == "running" {
		t.Fatalf("run after outcome_unknown = %q; the model must never be resumed over unknown remote state", last)
	}
	// A repeat approval returns the durable outcome without a second dispatch.
	before := len(h.fake.queueDeletesSeen())
	act, err := h.svc.ApproveAction(1, actionID, nil)
	if err != nil || act.Status != ActionOutcomeUnknown {
		t.Fatalf("repeat approve = (%v, %v), want the durable outcome_unknown", act, err)
	}
	if after := len(h.fake.queueDeletesSeen()); after != before {
		t.Fatalf("repeat approval dispatched again (%d -> %d deletes); outcome_unknown must never retry", before, after)
	}
}

func countEvents(events []string, want string) int {
	n := 0
	for _, e := range events {
		if e == want {
			n++
		}
	}
	return n
}

// TestPipelineConfirmWaitLoop drives the full confirm-wait timeline: after the
// fix executes and the model cannot type-prove a subjective report, the issue
// parks at awaiting_confirmation (reporter paged, admin queue untouched, no
// rule bookkeeping), the day-3 sweep re-asks exactly once, and the reporter's
// tap still closes it — with no issue_closed push for a close they made
// themselves.
func TestPipelineConfirmWaitLoop(t *testing.T) {
	h := newPipelineHarness(t)
	if _, err := h.svc.db.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (2, 'viewer', '', 'user')"); err != nil {
		t.Fatalf("seed reporter: %v", err)
	}
	episodes, files := buildPreAirSeason(28, 2, []preAirEpisode{
		{number: 3, airsIn: -21 * 24 * time.Hour, hasFile: true},
	})
	h.fake.setLibrary(episodes, files)
	h.fake.setSeriesHistory([]map[string]any{
		{"id": 9203, "eventType": "downloadFolderImported", "episodeId": 28203, "downloadId": "NZB-WRONG"},
		{"id": 8203, "eventType": "grabbed", "episodeId": 28203, "downloadId": "NZB-WRONG"},
	})
	resp, err := h.svc.CreateUserIssue(2, &CreateIssueRequest{
		InstanceID: preAirSonarrID, MediaType: "tv", TmdbID: 615, TvdbID: 73871,
		SeasonNumber: 2, EpisodeNumber: 3, Category: CategoryWrongContent,
		Reason: "Wrong episode.", Title: "Futurama",
	})
	if err != nil {
		t.Fatalf("CreateUserIssue: %v", err)
	}
	issueID := resp.IssueID
	if _, err := h.svc.db.Exec(
		"UPDATE issue_observations SET first_seen_at = ? WHERE issue_id = ?",
		time.Now().UTC().Add(-30*time.Minute), issueID,
	); err != nil {
		t.Fatalf("backdate observation: %v", err)
	}
	h.observe(t, time.Now().UTC())
	if _, err := h.svc.db.Exec(
		"UPDATE issue_observations SET settling_since = ? WHERE issue_id = ?",
		time.Now().UTC().Add(-3*time.Minute), issueID,
	); err != nil {
		t.Fatalf("backdate settle: %v", err)
	}
	h.observe(t, time.Now().UTC())

	script := &scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("r1", "get_history", `{}`),
		toolCall("p1", mcp.ToolProposeAction, `{"issue_id":0,"kind":"delete_media_files","params":{"media_type":"tv","tmdb_id":615,"season":2,"episodes":[3],"blocklist":true},"rationale":"Wrong content per the reporter."}`),
		toolCall("r2", "get_queue", `{}`),
		toolCall("c1", mcp.ToolConcludeIssue, `{"issue_id":0,"status":"resolved","resolution":"Deleted and replaced."}`),
	}}
	r := h.runner(script)
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var actionID int64
	if err := h.svc.db.QueryRow(
		"SELECT id FROM agent_actions WHERE issue_id = ? AND status = ?", issueID, ActionProposed,
	).Scan(&actionID); err != nil {
		t.Fatalf("find proposal: %v", err)
	}
	if _, err := h.svc.ApproveAction(1, actionID, nil); err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}

	// The resume reads, then tries to conclude a SUBJECTIVE report resolved.
	// The gate refuses, and with the fix executed the escalation must land at
	// awaiting_confirmation — reporter paged, admin queue untouched.
	if err := r.Resume(context.Background(), issueID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	parked, err := h.svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("load parked issue: %v", err)
	}
	if parked.Status != IssueAwaitingConfirmation {
		dumpSteps(t, h.svc, issueID)
		t.Fatalf("issue after refused conclude = %q, want %q", parked.Status, IssueAwaitingConfirmation)
	}
	if !parked.Read {
		t.Fatalf("awaiting_confirmation flagged the admin queue (read=false); this state belongs to the reporter")
	}
	if got := countEvents(h.notifier.userEvents, "issue_fix_confirm"); got != 1 {
		t.Fatalf("issue_fix_confirm pushes after park = %d, want exactly 1", got)
	}

	// Day 3: one nudge, exactly once, without resetting the 7-day clock.
	if _, err := h.svc.db.Exec(
		"UPDATE issues SET updated_at = datetime('now', '-80 hours') WHERE id = ?", issueID,
	); err != nil {
		t.Fatalf("age confirm wait: %v", err)
	}
	nudged, escalated, err := h.svc.SweepAwaitingConfirmation(context.Background())
	if err != nil || nudged != 1 || escalated != 0 {
		t.Fatalf("first confirm sweep = (%d, %d, %v), want one nudge", nudged, escalated, err)
	}
	nudged, _, err = h.svc.SweepAwaitingConfirmation(context.Background())
	if err != nil || nudged != 0 {
		t.Fatalf("second confirm sweep nudged %d (err %v); the stamp must make it exactly once", nudged, err)
	}
	if got := countEvents(h.notifier.userEvents, "issue_fix_confirm"); got != 2 {
		t.Fatalf("issue_fix_confirm pushes after nudge = %d, want 2", got)
	}

	// The reporter answers late but in time: their tap closes it, and a close
	// they made themselves sends them no issue_closed page.
	if err := h.svc.ReporterConfirmFix(context.Background(), issueID, 2); err != nil {
		t.Fatalf("ReporterConfirmFix from awaiting_confirmation: %v", err)
	}
	final, err := h.svc.GetIssue(issueID)
	if err != nil {
		t.Fatalf("load final issue: %v", err)
	}
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionReporterConfirmed {
		t.Fatalf("final = %q/%q, want resolved/reporter_confirmed", final.Status, final.ResolutionKind)
	}
	if got := countEvents(h.notifier.userEvents, "issue_closed"); got != 0 {
		t.Fatalf("issue_closed pushes after the reporter's own close = %d, want 0", got)
	}
}

// TestConfirmWaitEscalatesToAdminAtSevenDays: an unanswered confirm-wait is
// handed to an admin — the verdict is never fabricated, and the issue may not
// stay open forever.
func TestConfirmWaitEscalatesToAdminAtSevenDays(t *testing.T) {
	h := newPipelineHarness(t)
	if _, err := h.svc.db.Exec(
		`INSERT INTO issues (source, status, category, reporter_id, media_type, tmdb_id, title, detail, read, updated_at)
		 VALUES ('user', ?, 'wrong_content', 1, 'tv', 615, 'Futurama', 'wrong', 1, datetime('now', '-170 hours'))`,
		IssueAwaitingConfirmation,
	); err != nil {
		t.Fatalf("seed confirm wait: %v", err)
	}
	nudged, escalated, err := h.svc.SweepAwaitingConfirmation(context.Background())
	if err != nil || escalated != 1 {
		t.Fatalf("sweep = (%d, %d, %v), want one escalation", nudged, escalated, err)
	}
	var status string
	var read bool
	if err := h.svc.db.QueryRow("SELECT status, read FROM issues ORDER BY id DESC LIMIT 1").Scan(&status, &read); err != nil {
		t.Fatalf("read escalated issue: %v", err)
	}
	if status != IssueNeedsAdmin || read {
		t.Fatalf("escalated issue = (%q, read=%v), want unread needs_admin", status, read)
	}
}

func dumpSteps(t *testing.T, svc *Service, issueID int64) {
	rows, _ := svc.db.Query("SELECT run_id, seq, kind, COALESCE(tool_name,''), is_error, substr(COALESCE(tool_output,text,''),1,110) FROM agent_steps WHERE issue_id = ? ORDER BY run_id, seq", issueID)
	defer rows.Close()
	for rows.Next() {
		var runID, seq int64
		var kind, tool, out string
		var isErr bool
		rows.Scan(&runID, &seq, &kind, &tool, &isErr, &out)
		t.Logf("run %d step %d %s %s err=%v: %s", runID, seq, kind, tool, isErr, out)
	}
	var res string
	svc.db.QueryRow("SELECT COALESCE(resolution,'') FROM issues WHERE id = ?", issueID).Scan(&res)
	t.Logf("issue resolution: %s", res)
}

// TestPipelineUserReportSelfServiceLoop is the plan's end state, proven with
// zero seams: a household member's report rides the earned-autonomy lane. The
// FIRST user report of a diagnosed class is fixed with one admin approval
// (remember arms the rule — now offered on user issues whose diagnosis landed
// on a persisted label); the SECOND report of the same class dispatches with
// NO human decision at all, and the reporter still owns the close.
func TestPipelineUserReportSelfServiceLoop(t *testing.T) {
	h := newPipelineHarness(t)
	if _, err := h.svc.db.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (2, 'viewer', '', 'user')"); err != nil {
		t.Fatalf("seed reporter: %v", err)
	}
	episodes, files := buildPreAirSeason(28, 2, []preAirEpisode{
		{number: 3, airsIn: -21 * 24 * time.Hour, hasFile: true},
		{number: 4, airsIn: -14 * 24 * time.Hour, hasFile: true},
	})
	h.fake.setLibrary(episodes, files)

	reportAndPropose := func(queueID, episode int, downloadID string) (issueID, actionID int64) {
		t.Helper()
		now := time.Now().UTC()
		h.fake.setQueue([]map[string]any{stalledUpgradeQueueRowFor(queueID, downloadID, episode, now.Add(-30*time.Minute))})
		h.observe(t, now)
		resp, err := h.svc.CreateUserIssue(2, &CreateIssueRequest{
			InstanceID: preAirSonarrID, MediaType: "tv", TmdbID: 615, TvdbID: 73871,
			SeasonNumber: 2, EpisodeNumber: episode, Category: CategoryOther,
			Reason: "This download has been stuck forever.", Title: "Futurama",
		})
		if err != nil {
			t.Fatalf("CreateUserIssue: %v", err)
		}
		issueID = resp.IssueID
		if _, err := h.svc.db.Exec(
			`UPDATE issue_observations SET first_seen_at = ?, problem_since_at = ?, last_activity_at = ? WHERE issue_id = ?`,
			now.Add(-30*time.Minute), now.Add(-30*time.Minute), now.Add(-11*time.Minute), issueID,
		); err != nil {
			t.Fatalf("age observation: %v", err)
		}
		h.observe(t, time.Now().UTC())

		var kind string
		if err := h.svc.db.QueryRow("SELECT COALESCE(problem_kind,'') FROM issues WHERE id = ?", issueID).Scan(&kind); err != nil || kind == "" {
			t.Fatalf("user issue problem_kind = %q err %v; the Doctor's verdict was not stamped", kind, err)
		}
		script := &scriptedTurn{turns: []ai.TranscriptMessage{
			toolCall("r1", "get_queue", `{}`),
			toolCall("p1", mcp.ToolProposeAction, fmt.Sprintf(`{"issue_id":0,"kind":"remediate_queue","params":{"media_type":"tv","queue_id":%d,"action":"blocklist_only"},"rationale":"Stalled; the library copy stays."}`, queueID)),
		}}
		if err := h.runner(script).Run(context.Background(), issueID); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if err := h.svc.db.QueryRow(
			"SELECT id FROM agent_actions WHERE issue_id = ? AND status = ?", issueID, ActionProposed,
		).Scan(&actionID); err != nil {
			t.Fatalf("find proposal: %v", err)
		}
		return issueID, actionID
	}

	// Report 1: one admin approval, remembered — the rule is born from a USER
	// report's diagnosis.
	_, action1 := reportAndPropose(41, 3, "TORRENT-AAA")
	act, err := h.svc.ApproveActionRemembering(1, action1, nil)
	if err != nil || act.Status != ActionExecuted {
		t.Fatalf("remembering approve on a user report = (%v, %v), want executed", act, err)
	}
	var ruleID int64
	if err := h.svc.db.QueryRow("SELECT id FROM agent_approval_rules WHERE status = 'active'").Scan(&ruleID); err != nil {
		t.Fatalf("no rule armed from the user report's approval: %v", err)
	}

	// Report 2, same class: the sweep dispatches with NO human decision.
	issue2, action2 := reportAndPropose(42, 4, "TORRENT-BBB")
	h.svc.sweepAutoApprovals(time.Now().UTC())
	var status string
	var decidedBy, autoRule *int64
	if err := h.svc.db.QueryRow(
		"SELECT status, decided_by, auto_rule_id FROM agent_actions WHERE id = ?", action2,
	).Scan(&status, &decidedBy, &autoRule); err != nil {
		t.Fatalf("read auto decision: %v", err)
	}
	if status != ActionExecuted || decidedBy != nil || autoRule == nil || *autoRule != ruleID {
		t.Fatalf("second user report = status %q decided_by %v rule %v, want rule-executed with no human", status, decidedBy, autoRule)
	}

	// The reporter still owns the close — the machine never does.
	loaded, err := h.svc.GetIssue(issue2)
	if err != nil {
		t.Fatalf("load issue 2: %v", err)
	}
	if canConfirm, err := h.svc.CanReporterConfirmFix(loaded); err != nil || !canConfirm {
		t.Fatalf("CanReporterConfirmFix = (%v, %v) after the rule's fix", canConfirm, err)
	}
	if err := h.svc.ReporterConfirmFix(context.Background(), issue2, 2); err != nil {
		t.Fatalf("ReporterConfirmFix: %v", err)
	}
	final, _ := h.svc.GetIssue(issue2)
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionReporterConfirmed {
		t.Fatalf("final = %q/%q, want resolved/reporter_confirmed — zero admin touches end to end", final.Status, final.ResolutionKind)
	}
}
