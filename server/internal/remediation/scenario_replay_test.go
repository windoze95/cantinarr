package remediation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// Replay tier: every scenario in this file reconstructs a 2026-08-10-forensics
// incident from its recorded inputs (incidents.jsonl + Sonarr's hist.json) and
// freezes the CORRECT observable behavior as assertions. Per the reliability
// program, a replay scenario must FAIL on the unfixed system; the red baseline
// report records which do. Assertions may not be weakened after this commit.

// raceStart mirrors the incident clock: 2026-08-10T07:01:39Z was the grab,
// 07:02:00Z the import AND the first Cantinarr sighting of the stale queue row.
var raceStart = time.Date(2026, 8, 10, 7, 1, 39, 0, time.UTC)

const (
	raceOldFileID = 10257 // the 480p file the upgrade replaced (queue-time state)
	raceNewFileID = 12669 // the file the import created
	raceQueueID   = 603194671
	raceDownload  = "3ab4db2c-bef6-4691-9f4d-71bc288f22e7"
)

// stageUpgradeRace arranges the exact 2026-08-10 mechanism on the fake: the
// queue still shows the pre-import row (old file id) while library and history
// already hold the completed import — the state Cantinarr's poll+baseline pair
// observed whenever an upgrade import finished inside one poll period.
func stageUpgradeRace(w *scenarioWorld, season, episode int) (importAt time.Time) {
	importAt = raceStart.Add(21 * time.Second)
	ep, file := scenarioEpisode(season, episode, raceNewFileID, importAt)
	w.fake.setLibrary([]map[string]any{ep}, []map[string]any{file})
	w.fake.setQueue([]map[string]any{
		importPendingQueueRow(raceQueueID, raceDownload, season, episode, raceOldFileID, raceStart),
	})
	w.fake.setHistory(importReceiptHistory(39031, raceDownload, season, episode, raceNewFileID, importAt))
	return importAt
}

// TestScenarioReplay815UpgradeRaceStaysSilent replays issue 815 (The Big Comfy
// Couch S02E01) end to end through the production pipeline. Sonarr's history
// held a valid downloadFolderImported receipt for the tracked download from the
// first minute; the observation machinery must therefore prove recovery during
// the absence settle and close silently. On 2026-08-10 it instead promoted,
// spent an agent run, refused the agent's correct conclusion, and paged the
// admin at 2:16 AM for an episode that was already on disk.
func TestScenarioReplay815UpgradeRaceStaysSilent(t *testing.T) {
	w := newScenarioWorld(t, raceStart)
	stageUpgradeRace(w, 2, 1)

	// 07:02:00 — production first sights the stale row; the issue is created and
	// its baseline is captured from the already-imported library state.
	w.clock.step(21 * time.Second)
	w.observe()
	issueID := w.soleOpenAutoIssueID()
	w.requireObservedDownload(issueID, raceDownload, raceOldFileID)
	if kind := issueProblemKind(t, w.svc, issueID); kind != "Waiting to import" {
		t.Fatalf("staged issue problem_kind = %q, want the incident's %q", kind, "Waiting to import")
	}

	// 07:02:30 — the import's queue departure is visible; absence settling begins.
	w.fake.setQueue(nil)
	w.clock.step(30 * time.Second)
	w.observe()

	// Run out the settle and observation windows on production cadence, plus
	// enough slack to cover the promotion, alert hold-down, and any re-pages.
	w.advance(20 * time.Minute)

	final := w.issueByID(issueID)
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionArrStateCleared {
		t.Errorf("issue = status %q / kind %q, want silent %q / %q — the import receipt was in Sonarr's history the whole time",
			final.Status, final.ResolutionKind, IssueResolved, ResolutionArrStateCleared)
	}
	if pages := w.rec.pages("issue_created"); len(pages) != 0 {
		t.Errorf("admin pages for a recovered import = %d (%s), want 0", len(pages), pageTimes(pages))
	}
	var runs int
	if err := w.svc.db.QueryRow("SELECT COUNT(*) FROM agent_runs WHERE issue_id = ?", issueID).Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != 0 {
		t.Errorf("agent runs spent on a recovered import = %d, want 0", runs)
	}
}

// seedPromotedRaceIssue writes the DB state the seven incidents were in at
// promotion time — copied from the production snapshot's rows — so gate-level
// scenarios keep exercising the conclusion gate even after the settle-time
// proof is fixed (when the full pipeline will no longer reach promotion).
// receiptSkew shifts the history receipt's date relative to the queue row's
// added time; queueFileID controls the observed download's queue-time file.
func seedPromotedRaceIssue(w *scenarioWorld, queueFileID int64, receiptSkew time.Duration) (issueID int64) {
	w.t.Helper()
	season, episode := 2, 1
	importAt := raceStart.Add(21 * time.Second).Add(receiptSkew)
	ep, file := scenarioEpisode(season, episode, raceNewFileID, importAt)
	w.fake.setLibrary([]map[string]any{ep}, []map[string]any{file})
	w.fake.setQueue(nil)
	w.fake.setHistory(importReceiptHistory(39031, raceDownload, season, episode, raceNewFileID, importAt))

	created := raceStart.Add(21 * time.Second)
	promoted := created.Add(10*time.Minute + 30*time.Second)
	res, err := w.svc.db.Exec(
		`INSERT INTO issues (source, status, tmdb_id, tvdb_id, media_type, title,
		   season_number, episode_number, instance_id, download_id, arr_queue_id,
		   detail, problem_kind, dedupe_key, created_at, updated_at)
		 VALUES ('auto', ?, ?, ?, 'tv', 'The Big Comfy Couch', ?, ?, ?, ?, ?,
		   'Waiting to import', 'Waiting to import', ?, ?, ?)`,
		IssueOpen, scenarioTmdbID, scenarioTvdbID, season, episode,
		scenarioSonarrID, raceDownload, raceQueueID,
		fmt.Sprintf("replay|%d|%d", season, episode), created, promoted,
	)
	if err != nil {
		w.t.Fatalf("seed issue: %v", err)
	}
	issueID, _ = res.LastInsertId()
	scope := fmt.Sprintf("%s|tv|tvdb:%d|s:%d|e:%d", scenarioSonarrID, scenarioTvdbID, season, episode)
	if _, err := w.svc.db.Exec(
		`INSERT INTO issue_observations (issue_id, service_type, scope_key, state, signature,
		   first_seen_at, last_activity_at, promoted_at,
		   baseline_has_file, baseline_file_id, baseline_captured_at, updated_at)
		 VALUES (?, 'sonarr', ?, 'settling', 'replay-sig', ?, ?, ?, 1, ?, ?, ?)`,
		issueID, scope, created, promoted, promoted, raceNewFileID, created, promoted,
	); err != nil {
		w.t.Fatalf("seed observation: %v", err)
	}
	if _, err := w.svc.db.Exec(
		`INSERT INTO issue_observation_downloads (issue_id, download_id, first_seen_at, arr_added_at, queue_file_id)
		 VALUES (?, ?, ?, ?, ?)`,
		issueID, raceDownload, created, raceStart, queueFileID,
	); err != nil {
		w.t.Fatalf("seed observed download: %v", err)
	}
	return issueID
}

// run57Script is the recorded tool sequence of production run 57 (plus the
// scoped queue read runs 58-63 also made): diagnose, read the season timeline,
// take the typed scoped queue observation, tell the reporter thread, conclude.
func run57Script(issueID int64) *scriptedTurn {
	return &scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("d1", "diagnose_queue", `{}`),
		toolCall("t1", "get_episode_timeline", `{}`),
		toolCall("q1", "get_queue", `{}`),
		toolCall("m1", "post_issue_message", `{"body":"The original queue item is no longer in Sonarr and the episode's file is imported."}`),
		toolCall("c1", mcp.ToolConcludeIssue, fmt.Sprintf(
			`{"issue_id":%d,"status":"resolved","resolution":"Verified the queue item is gone and the episode is imported; the auto-detected import issue has cleared."}`, issueID)),
	}}
}

// TestScenarioReplayGateAcceptsProvenConclusion replays runs 57-63's decision
// point in isolation: the issue is already (wrongly or rightly) promoted, the
// live arr shows the episode imported, Sonarr's history binds the tracked
// download to the live file, and the agent reads all of it and concludes. The
// conclusion gate must accept — the evidence it demands exists. On 2026-08-10
// it refused seven out of seven and escalated every one to a 2 AM page.
func TestScenarioReplayGateAcceptsProvenConclusion(t *testing.T) {
	w := newScenarioWorld(t, raceStart)
	issueID := seedPromotedRaceIssue(w, raceOldFileID, 0)

	if err := w.runner(run57Script(issueID)).Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	final := w.issueByID(issueID)
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionArrStateCleared {
		t.Errorf("issue after verified conclusion = %q/%q, want %q/%q",
			final.Status, final.ResolutionKind, IssueResolved, ResolutionArrStateCleared)
	}
	// AMENDED AT PHASE 3 (disclosed in the program report): the frozen draft
	// demanded a succeeded run row. The fixed system resolves this state in the
	// pre-claim preflight — zero model spend — so a run may legitimately never
	// exist. The defect this scenario guards is still fully detected: a refusal
	// would land the issue at needs_admin (caught above) with a gave_up run
	// (caught here). What may never appear is exactly that refusal shape.
	var status, stopReason string
	err := w.svc.db.QueryRow(
		"SELECT status, COALESCE(stop_reason,'') FROM agent_runs WHERE issue_id = ? ORDER BY id DESC LIMIT 1", issueID,
	).Scan(&status, &stopReason)
	if err == nil && (status == "gave_up" || stopReason == "unverified_conclusion") {
		t.Errorf("run = %q (stop %q) — a provable recovery was refused", status, stopReason)
	}
	if pages := w.rec.pages("issue_created"); len(pages) != 0 {
		t.Errorf("pages for a provable recovery = %d (%s), want 0", len(pages), pageTimes(pages))
	}
}

// TestScenarioReplay855SecondGrabAfterProvenUpgrade replays the 90 Day Fiancé
// pair through the full pipeline: the FIRST grab of a new episode (no prior
// file — queue_file_id 0) imports, is proven, and closes silently, exactly as
// issue 854 did in production. Minutes later Sonarr grabs an upgrade of the
// same episode and its import also lands inside the poll window. Both issues
// must close silently; in production the second became the 3:51 AM page.
func TestScenarioReplay855SecondGrabAfterProvenUpgrade(t *testing.T) {
	start := time.Date(2026, 8, 10, 8, 23, 30, 0, time.UTC)
	w := newScenarioWorld(t, start)
	season, episode := 12, 14
	const (
		firstFileID  = 12722
		secondFileID = 12723
		firstDL      = "dd24d73d-3632-4fe5-a111-000000000001"
		secondDL     = "681cea63-3219-4224-9fbb-7b7ebb6b4658"
	)

	// Grab #1: brand-new episode, no file at queue time (queue_file_id 0), row
	// sighted BEFORE the import lands — the transition-provable ordering.
	ep, _ := scenarioEpisode(season, episode, 0, start)
	w.fake.setLibrary([]map[string]any{ep}, nil)
	w.fake.setQueue([]map[string]any{
		importPendingQueueRow(358822900, firstDL, season, episode, 0, start),
	})
	w.observe()
	firstIssue := w.soleOpenAutoIssueID()

	// The import lands: library gains the file, history the receipt, queue empties.
	importAt := start.Add(30 * time.Second)
	ep2, file2 := scenarioEpisode(season, episode, firstFileID, importAt)
	w.fake.setLibrary([]map[string]any{ep2}, []map[string]any{file2})
	w.fake.setHistory(importReceiptHistory(39157, firstDL, season, episode, firstFileID, importAt))
	w.fake.setQueue(nil)
	w.clock.step(30 * time.Second)
	w.observe()
	w.advance(12 * time.Minute)
	if got := w.issueByID(firstIssue); got.Status != IssueResolved || got.ResolutionKind != ResolutionArrStateCleared {
		t.Fatalf("first grab = %q/%q, want silent resolve (this leg was CORRECT in production — if it breaks, the scenario drifted)",
			got.Status, got.ResolutionKind)
	}

	// Grab #2: an upgrade of the now-present file, import racing the poll — the
	// snapshot shows the OLD file id while library/history already hold the new.
	upgradeAt := w.clock.now
	ep3, file3 := scenarioEpisode(season, episode, secondFileID, upgradeAt)
	w.fake.setLibrary([]map[string]any{ep3}, []map[string]any{file3})
	w.fake.setQueue([]map[string]any{
		importPendingQueueRow(358822901, secondDL, season, episode, firstFileID, upgradeAt),
	})
	w.fake.setHistory(append(
		importReceiptHistory(39170, secondDL, season, episode, secondFileID, upgradeAt),
		importReceiptHistory(39157, firstDL, season, episode, firstFileID, importAt)...,
	))
	w.observe()
	secondIssue := w.soleOpenAutoIssueID()
	if secondIssue == firstIssue {
		t.Fatalf("second grab did not open a new issue; scenario drifted")
	}
	w.fake.setQueue(nil)
	w.clock.step(30 * time.Second)
	w.observe()
	w.advance(20 * time.Minute)

	if got := w.issueByID(secondIssue); got.Status != IssueResolved || got.ResolutionKind != ResolutionArrStateCleared {
		t.Errorf("upgrade grab = %q/%q, want silent resolve — its import receipt is in history like the first one's",
			got.Status, got.ResolutionKind)
	}
	if pages := w.rec.pages("issue_created"); len(pages) != 0 {
		t.Errorf("pages for two healthy imports = %d (%s), want 0", len(pages), pageTimes(pages))
	}
}

// stalledQueueRow is a genuinely stuck torrent — the incident class where a
// page IS the right outcome. Used by the stagger scenarios so their assertions
// survive the recovery-proof fix (unlike the race class, these never resolve
// on their own).
func stalledQueueRow(queueID int64, downloadID string, season, episode int, fileID int64, added time.Time) map[string]any {
	row := importPendingQueueRow(queueID, downloadID, season, episode, fileID, added)
	row["status"] = "queued"
	row["trackedDownloadStatus"] = "warning"
	row["trackedDownloadState"] = "downloading"
	row["errorMessage"] = "stalled with no connections"
	row["protocol"] = "torrent"
	row["sizeleft"] = 200000000.0
	return row
}

// TestScenarioReplaySeasonPackWavePageBudget replays the wave shape of the
// 2026-08-10 burst with issues that genuinely need attention: four stalled
// downloads surfacing ~90s apart, promoting ~90s apart — one correlated event.
// The stagger policy under test (acceptance criteria, Phase 1): ONE admin push
// per source per 15-minute window, carrying the wave's count; follow-on issues
// join the outstanding page instead of buzzing the phone again. Production
// behavior on 2026-08-10 was six pushes in twenty minutes.
func TestScenarioReplaySeasonPackWavePageBudget(t *testing.T) {
	start := time.Date(2026, 8, 10, 7, 2, 0, 0, time.UTC)
	w := newScenarioWorld(t, start)
	var lib []map[string]any
	var files []map[string]any
	var rows []map[string]any
	for i := 0; i < 4; i++ {
		ep, file := scenarioEpisode(2, i+1, int64(10250+i), start.Add(-30*24*time.Hour))
		lib = append(lib, ep)
		files = append(files, file)
	}
	w.fake.setLibrary(lib, files)

	for i := 0; i < 4; i++ {
		rows = append(rows, stalledQueueRow(int64(700000+i), fmt.Sprintf("STALL-%04d", i), 2, i+1, int64(10250+i), w.clock.now.Add(-30*time.Minute)))
		w.fake.setQueue(rows)
		w.observe()
		w.advance(90 * time.Second)
	}
	// Let every member cross the observation windows, promote, and flush.
	w.advance(25 * time.Minute)

	pages := w.rec.pages("issue_created")
	if len(pages) > 1 {
		t.Errorf("admin pushes for one four-issue wave = %d (%s), want 1 push carrying the wave", len(pages), pageTimes(pages))
	}
	if len(pages) == 1 {
		covered := 0
		for _, key := range []string{"count", "open_count"} {
			switch n := pages[0].Data[key].(type) {
			case int:
				if n > covered {
					covered = n
				}
			case int64:
				if int(n) > covered {
					covered = int(n)
				}
			}
		}
		if covered < 4 {
			t.Errorf("the single wave push covered %d issue(s) (data %v), want a count reflecting all 4", covered, pages[0].Data)
		}
	}
	var open int
	if err := w.svc.db.QueryRow(
		"SELECT COUNT(*) FROM issues WHERE closed_at IS NULL AND status IN (?, ?)", IssueOpen, IssueNeedsAdmin,
	).Scan(&open); err != nil {
		t.Fatalf("count open: %v", err)
	}
	if open != 4 {
		t.Errorf("open stalled issues = %d, want all 4 still visible in the app regardless of push coalescing", open)
	}
}

// TestScenarioReplay813RepromotionPagesOnce replays issue 813's shape: a
// stalled download promotes and pages, the arr grabs a replacement (the issue
// suspends back to recovery), the replacement stalls too, and the issue
// re-promotes. Production paged on every promotion edge — three times for one
// issue over one night. The policy under test: one page per issue per 24h;
// a re-promotion of already-paged work must not re-buzz the phone.
//
// RED-BASELINE HONESTY NOTE: this scenario PASSES on current code — the pure
// observation-level flap staged here re-promotes without re-queueing a second
// page inside the scenario window. Production's triple-page rode the agent
// preflight's suspend/re-promote loop (runs 53-54, arr_recovery_in_flight),
// which this scenario does not drive. Per the program's rule this is reported
// as "not reproducing the bug" in RED_BASELINE.md rather than silently kept;
// it stays as a green fence over the single-page property it does exercise,
// and the full preflight-flap replay is Phase 3 follow-up work.
func TestScenarioReplay813RepromotionPagesOnce(t *testing.T) {
	start := time.Date(2026, 8, 1, 20, 41, 0, 0, time.UTC)
	w := newScenarioWorld(t, start)
	ep, file := scenarioEpisode(3, 7, 10900, start.Add(-60*24*time.Hour))
	w.fake.setLibrary([]map[string]any{ep}, []map[string]any{file})

	w.fake.setQueue([]map[string]any{stalledQueueRow(800001, "STALL-FIRST", 3, 7, 10900, start.Add(-30*time.Minute))})
	w.observe()
	issueID := w.soleOpenAutoIssueID()
	w.advance(15 * time.Minute)
	if first := w.rec.pages("issue_created"); len(first) != 1 {
		t.Fatalf("pages after first promotion = %d (%s), want exactly 1 (this page is CORRECT)", len(first), pageTimes(first))
	}

	// The arr replaces the download: same scope, new id, healthy for a while —
	// the issue suspends into recovering and its alert debt re-arms.
	replacement := stalledQueueRow(800002, "STALL-SECOND", 3, 7, 10900, w.clock.now)
	replacement["errorMessage"] = ""
	replacement["trackedDownloadStatus"] = "ok"
	w.fake.setQueue([]map[string]any{replacement})
	w.observe()
	w.advance(2 * time.Minute)

	// The replacement stalls too; the issue re-promotes after the windows.
	stalled := stalledQueueRow(800002, "STALL-SECOND", 3, 7, 10900, w.clock.now.Add(-30*time.Minute))
	w.fake.setQueue([]map[string]any{stalled})
	w.observe()
	w.advance(20 * time.Minute)

	pages := w.rec.pages("issue_created")
	if len(pages) != 1 {
		t.Errorf("pages for one flapping issue = %d (%s), want 1 per 24h — production paged every promotion edge", len(pages), pageTimes(pages))
	}
	if got := w.issueByID(issueID); got.ClosedAt != nil {
		t.Errorf("issue closed = %v, want still open awaiting work", got.ClosedAt)
	}
}

// TestScenarioReplayStalePageThenSelfClearSinglePage replays the 2026-07-29
// cluster's arc (issues 128-140): a stall pages — defensibly, it WAS stuck —
// and hours later the torrent completes and imports on its own. Phase 1
// classified the page as "notified, no outcome" but the notification decision
// itself was sound at send time; what the replay freezes is the aftermath:
// exactly one page ever, and a silent, proof-backed close once the arr healed.
// EXPECTED TO PASS on current code (recorded as such in the red baseline): the
// self-heal proves via the old→new file transition, not the strict receipt.
func TestScenarioReplayStalePageThenSelfClearSinglePage(t *testing.T) {
	start := time.Date(2026, 7, 29, 13, 21, 0, 0, time.UTC)
	w := newScenarioWorld(t, start)
	ep, file := scenarioEpisode(4, 2, 11400, start.Add(-90*24*time.Hour))
	w.fake.setLibrary([]map[string]any{ep}, []map[string]any{file})
	w.fake.setQueue([]map[string]any{stalledQueueRow(900001, "STALL-SLOW", 4, 2, 11400, start.Add(-30*time.Minute))})
	w.observe()
	issueID := w.soleOpenAutoIssueID()
	w.advance(15 * time.Minute)
	if pages := w.rec.pages("issue_created"); len(pages) != 1 {
		t.Fatalf("pages after promotion = %d (%s), want the one defensible page", len(pages), pageTimes(pages))
	}

	// Hours later the stall un-sticks: import lands (new file id), receipt in
	// history, queue empties. The transition proof closes it without a human.
	w.advance(3 * time.Hour)
	healAt := w.clock.now
	ep2, file2 := scenarioEpisode(4, 2, 11401, healAt)
	w.fake.setLibrary([]map[string]any{ep2}, []map[string]any{file2})
	w.fake.setHistory(importReceiptHistory(41000, "STALL-SLOW", 4, 2, 11401, healAt))
	w.fake.setQueue(nil)
	w.observe()
	w.advance(15 * time.Minute)

	final := w.issueByID(issueID)
	if final.Status != IssueResolved || final.ResolutionKind != ResolutionArrStateCleared {
		t.Errorf("self-healed stall = %q/%q, want %q/%q", final.Status, final.ResolutionKind, IssueResolved, ResolutionArrStateCleared)
	}
	if pages := w.rec.pages("issue_created"); len(pages) != 1 {
		t.Errorf("total pages across the whole arc = %d (%s), want exactly the initial one", len(pages), pageTimes(pages))
	}
}
