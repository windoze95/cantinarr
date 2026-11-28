package remediation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

func TestSharedDownloadRecoveryRequiresEveryMember(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	w := newScenarioWorld(t, start)
	first := importPendingQueueRow(31, "same", 0, 1, 0, start)
	second := importPendingQueueRow(32, "same", 0, 2, 0, start)
	w.fake.setQueue([]map[string]any{first})
	w.observe()
	id := w.soleOpenAutoIssueID()
	w.fake.setQueue([]map[string]any{second, first})
	w.advance(15 * time.Minute)
	if got := w.soleOpenAutoIssueID(); got != id {
		t.Fatalf("late sibling replaced %d with %d", id, got)
	}
	issue := w.issueByID(id)
	if issue.memberCount != 2 || issue.ArrQueueID != 0 || issue.EpisodeNumber != 0 {
		t.Fatalf("pack has representative episode: %+v", issue)
	}
	if err := w.svc.validateActionScope(id, ActionRescan, json.RawMessage(`{"media_type":"tv"}`)); err == nil {
		t.Fatal("pack allowed a rescan")
	}
	r := w.runner(&scriptedTurn{})
	_, refused, _ := r.dispatchTool(context.Background(), &issue, 99, toolUse("widen", mcp.ToolProposeAction))
	if !refused {
		t.Fatal("unoffered proposal tool dispatched")
	}
	w.fake.setQueue(nil)
	a, af := scenarioEpisode(0, 1, 101, start)
	b, bf := scenarioEpisode(0, 2, 102, start)
	w.fake.setLibrary([]map[string]any{a, b}, []map[string]any{af, bf})
	history := importReceiptHistory(41, "same", 0, 1, 101, start)
	history = append(history, importReceiptHistory(42, "same", 0, 2, 102, start.Add(-time.Second))...)
	history = append(history, importReceiptHistory(43, "other", 0, 2, 102, start)...)
	history = append(history, importReceiptHistory(44, "same", 0, 2, 999, start)...)
	w.fake.setHistory(history)
	w.advance(6 * time.Minute)
	if w.issueByID(id).ClosedAt != nil {
		t.Fatal("partial, unrelated or stale receipts closed the pack")
	}
	var receipts int
	if err := w.svc.db.QueryRow("SELECT COUNT(*) FROM issue_sonarr_members WHERE history_id IS NOT NULL AND issue_id=?", id).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("verified %d members: %v", receipts, err)
	}
	w.fake.setHistory(append(history, importReceiptHistory(45, "same", 0, 2, 102, start)...))
	w.fake.setHistoryStatus(503)
	w.advance(2 * time.Minute)
	if w.issueByID(id).ClosedAt != nil {
		t.Fatal("unreadable history closed pack")
	}
	w.fake.setHistoryStatus(0)
	// Native ids remain positive, but an unreadable season is not season 0.
	delete(b, "seasonNumber")
	w.fake.setLibrary([]map[string]any{a, b}, []map[string]any{af, bf})
	w.advance(2 * time.Minute)
	if w.issueByID(id).ClosedAt != nil {
		t.Fatal("missing live season was treated as an exact special")
	}
	b["seasonNumber"] = 0
	w.fake.setLibrary([]map[string]any{a, b}, []map[string]any{af, bf})
	w.advance(3 * time.Minute)
	if issue := w.issueByID(id); issue.Status != IssueResolved || issue.ResolutionKind != ResolutionArrStateCleared {
		t.Fatalf("complete recovery: %+v", issue)
	}
}

// Sonarr can copy the same "already imported" message to every episode
// queue row after importing ONE file. The rows remain importPending, and
// their embedded file state can lag the actual /episode endpoint.
func TestSharedDownloadWithNativePartialImportMessages(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	w := newScenarioWorld(t, start)
	a := importPendingQueueRow(1, "same", 1, 1, 0, start)
	b := importPendingQueueRow(2, "same", 1, 2, 0, start)
	w.fake.setQueue([]map[string]any{a, b})
	w.observe()
	id := w.soleOpenAutoIssueID()
	w.advance(16 * time.Minute)
	for _, row := range []map[string]any{a, b} {
		row["trackedDownloadStatus"] = "warning"
		row["statusMessages"] = []map[string]any{{"title": "Fixture S01", "messages": []string{"Episode file already imported at 11/28/2026 07:56:02"}}}
	}
	w.fake.setQueue([]map[string]any{a, b})
	ep1, file1 := scenarioEpisode(1, 1, 101, start)
	ep2, file2 := scenarioEpisode(1, 2, 102, start)
	w.fake.setLibrary([]map[string]any{ep1}, []map[string]any{file1})
	hist := importReceiptHistory(91, "same", 1, 1, 101, start)
	w.fake.setHistory(hist)
	w.advance(15 * time.Minute)
	issue := w.issueByID(id)
	if issue.ClosedAt != nil || (issue.Status != IssueOpen && issue.Status != IssueNeedsAdmin) {
		t.Fatalf("partial import stranded the promoted issue at %s", issue.Status)
	}
	var count int
	if err := w.svc.db.QueryRow("select count(history_id) from issue_sonarr_members where issue_id=?", id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial receipts: %d, %v", count, err)
	}
	w.fake.setLibrary([]map[string]any{ep1, ep2}, []map[string]any{file1, file2})
	w.fake.setHistory(append(hist, importReceiptHistory(92, "same", 1, 2, 102, start)...))
	w.advance(2 * time.Minute)
	if w.issueByID(id).Status != IssueResolved {
		t.Fatal("unchanged lingering queue rows hid the final import")
	}
}

func TestParallelSeasonDownloadsStaySeparate(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	w := newScenarioWorld(t, start)
	var rows []map[string]any
	for i, hash := range []string{"one", "two"} {
		for ep := 1; ep <= 2; ep++ {
			rows = append(rows, importPendingQueueRow(int64(10*i+ep), hash, 1, ep, 0, start))
		}
	}
	w.fake.setQueue(rows)
	w.observe()
	w.advance(12 * time.Minute)
	issues, _, err := w.svc.ListIssues("", 0)
	if err != nil || len(issues) != 2 {
		t.Fatalf("parallel issues=%d, err=%v", len(issues), err)
	}
	for _, issue := range issues {
		current := w.issueByID(issue.ID)
		if current.memberCount != 2 {
			t.Fatalf("download %s lost its members", issue.DownloadID)
		}
		text, err := w.svc.describeSeasonQueue(&current, true)
		if err != nil || !strings.Contains(text, "2 current queue rows") || strings.Count(text, "S01E") != 2 {
			t.Fatalf("parallel download read widened: %q %v", text, err)
		}
	}
}

func TestConcurrentClientsCannotShareAnUnattributedReceipt(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	w := newScenarioWorld(t, start)
	var rows, episodes, files, history []map[string]any
	for ep := 1; ep <= 2; ep++ {
		for i, client := range []string{"first", "second"} {
			row := importPendingQueueRow(int64(i*10+ep), "same", 1, ep, 0, start.Add(time.Duration(i)*time.Second))
			row["downloadClient"] = client
			rows = append(rows, row)
		}
		e, f := scenarioEpisode(1, ep, int64(100+ep), start)
		episodes, files = append(episodes, e), append(files, f)
		history = append(history, importReceiptHistory(int64(ep), "same", 1, ep, int64(100+ep), start.Add(10*time.Minute))...)
	}
	w.fake.setQueue(rows)
	w.observe()
	w.advance(14 * time.Minute)
	w.fake.setLibrary(episodes, files)
	w.fake.setHistory(history)
	w.advance(time.Minute)
	var closed int
	if err := w.svc.db.QueryRow("SELECT COUNT(*) FROM issues WHERE closed_at IS NOT NULL").Scan(&closed); err != nil || closed != 0 {
		t.Fatalf("receipt without a client closed %d of two concurrent attempts: %v", closed, err)
	}
	for _, h := range history {
		h["data"].(map[string]any)["downloadClientName"] = "second"
	}
	w.fake.setHistory(history)
	w.advance(time.Minute)
	if err := w.svc.db.QueryRow(`SELECT COUNT(*) FROM issues i JOIN issue_sonarr_members m ON i.id=m.issue_id
		WHERE closed_at IS NOT NULL AND m.client_name='second'`).Scan(&closed); err != nil || closed != 2 {
		t.Fatalf("exact client should have both members verified: %d %v", closed, err)
	}
}

func TestActiveSiblingDoesNotResetBlockedMembersClock(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	w := newScenarioWorld(t, start)
	stuck := importPendingQueueRow(1, "mixed", 1, 1, 0, start)
	active := importPendingQueueRow(2, "mixed", 1, 2, 0, start)
	active["status"] = "downloading"
	active["trackedDownloadState"] = "downloading"
	active["trackedDownloadStatus"] = "ok"
	delete(active, "statusMessages")
	active["sizeleft"] = 900
	w.fake.setQueue([]map[string]any{stuck, active})
	w.observe()
	id := w.soleOpenAutoIssueID()
	minutes := w.svc.Settings().ObservationMinMinutes
	for n := 1; n <= minutes; n++ {
		active["sizeleft"] = 900 - n
		w.fake.setQueue([]map[string]any{active, stuck})
		w.advance(time.Minute)
		if n < minutes && w.issueByID(id).Status != IssueObserving {
			t.Fatal("sibling advanced promotion before minimum persistence")
		}
	}
	if got := w.issueByID(id); got.Status != IssueOpen || got.memberCount != 2 {
		t.Fatalf("healthy sibling masked a persistent blocker: %+v", got)
	}
	active["sizeleft"] = 500
	w.fake.setQueue([]map[string]any{active, stuck})
	if withheld, err := w.svc.preflightArrRecovery(id); err != nil || withheld {
		t.Fatalf("healthy sibling prevented investigation of a persistent blocker: %t, %v", withheld, err)
	}
}

func TestReappearingAndNewMembersAfterVerifiedRecovery(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	w := newScenarioWorld(t, start)
	var rows, episodes, files, history []map[string]any
	for ep := 1; ep <= 3; ep++ {
		rows = append(rows, importPendingQueueRow(int64(ep), "late", 1, ep, 0, start))
		e, f := scenarioEpisode(1, ep, int64(100+ep), start)
		episodes = append(episodes, e)
		files = append(files, f)
		if ep < 3 {
			history = append(history, importReceiptHistory(int64(ep), "late", 1, ep, int64(100+ep), start)...)
		}
	}
	w.fake.setQueue(rows[:2])
	w.observe()
	id := w.soleOpenAutoIssueID()
	w.advance(15 * time.Minute)
	w.fake.setLibrary(episodes, files)
	w.fake.setHistory(history)
	w.fake.setQueue(nil)
	w.advance(5 * time.Minute)
	if w.issueByID(id).ClosedAt == nil {
		t.Fatal("two proven members did not close")
	}
	w.fake.setQueue(rows[:2])
	w.advance(2 * time.Minute)
	if w.issueByID(id).ClosedAt == nil || countRows(t, w.svc, "issues") != 1 {
		t.Fatal("lingering rows reopened or duplicated the proven incident")
	}
	w.fake.setQueue(rows[2:])
	w.advance(15 * time.Minute)
	got := w.issueByID(id)
	if got.ClosedAt != nil || got.memberCount != 3 || got.ArrQueueID != 0 || countRows(t, w.svc, "issues") != 1 {
		t.Fatalf("new sibling lost prior history: %+v", got)
	}
	if n := len(w.rec.pages("issue_created")); n != 1 {
		t.Fatalf("one ongoing attempt paged %d times", n)
	}
}

type turnCallback func(context.Context, ai.TurnParams) (ai.TurnResult, error)

func (f turnCallback) NextTurn(ctx context.Context, p ai.TurnParams) (ai.TurnResult, error) {
	return f(ctx, p)
}

func TestDisableAndReenableDiscardsDetectedTurn(t *testing.T) {
	host := &fakeToolHost{}
	r, svc, id := newTestRunner(t, host, &scriptedTurn{})
	if _, err := svc.db.Exec("UPDATE issues SET source='auto' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r.turns = scriptedTurnResolver(turnCallback(func(ctx context.Context, _ ai.TurnParams) (ai.TurnResult, error) {
		calls++
		settings := svc.Settings()
		settings.AutoDispatch = false
		if _, err := svc.SetSettings(settings); err != nil {
			t.Fatal(err)
		}
		settings.AutoDispatch = true
		if _, err := svc.SetSettings(settings); err != nil {
			t.Fatal(err)
		}
		if ctx.Err() == nil {
			t.Fatal("disabled generation still owns its provider call")
		}
		return ai.TurnResult{Message: toolCall("late", mcp.ToolProposeAction, `{}`), StopReason: ai.StopReasonToolUse}, nil
	}))
	if err := r.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || host.proposeCalled || countRows(t, svc, "agent_actions") != 0 {
		t.Fatalf("stale turn consumed: calls=%d, propose=%v", calls, host.proposeCalled)
	}
	issue, err := svc.GetIssue(id)
	if err != nil || issue.Status != IssueOpen || svc.readAutoGiveupStreak() != 0 {
		t.Fatalf("canceled run outcome: %+v %v", issue, err)
	}
	// The run can lose its claim one instruction after the runner's ownership
	// read. A late tool result cannot append to that retired audit trail.
	var run int64
	if err := svc.db.QueryRow("SELECT id FROM agent_runs WHERE issue_id=?", id).Scan(&run); err != nil {
		t.Fatal(err)
	}
	r.persistStep(run, id, 99, stepToolResult, "get_queue", "late", "", "late response", false)
	if n := countRows(t, svc, "agent_steps"); n != 0 {
		t.Fatalf("stale response wrote %d steps", n)
	}
}

func TestDisabledDetectedRuleLeavesHumanDecisionAndBlocksResume(t *testing.T) {
	s, fx, _, id, action := autoApprovalFixture(t)
	rule, err := s.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
	if err != nil {
		t.Fatal(err)
	}
	settings := s.Settings()
	settings.AutoDispatch = false
	if _, err = s.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	s.sweepAutoApprovals(time.Now())
	if _, err = s.autoApproveAction(rule, action); err == nil || fx.count() != 0 {
		t.Fatal("rule bypassed disabled detection work")
	}
	if _, err = s.ApproveAction(testAdminID, action, nil); err != nil || fx.count() != 1 {
		t.Fatalf("human approval, calls=%d: %v", fx.count(), err)
	}
	r := &Runner{db: s.db, svc: s, procToken: "restart", turns: autonomousTurnResolverFunc(func(context.Context, ai.AutonomousModelOverride) (ai.AutonomousTurn, error) {
		t.Fatal("passive resume resolved a provider")
		return ai.AutonomousTurn{}, nil
	})}
	s.recoverWork(r)
	if err = r.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err = r.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := drainJobs(s); got != 0 {
		t.Fatalf("restart scheduled %d prohibited jobs", got)
	}
}

func TestRulePreflightCannotSurviveOffOn(t *testing.T) {
	for _, barrier := range []int{1, 2} {
		t.Run(fmt.Sprintf("preflight-%d", barrier), func(t *testing.T) {
			s, fx, _, _, action := autoApprovalFixture(t)
			rule, err := s.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, "")
			if err != nil {
				t.Fatal(err)
			}
			started, proceed := make(chan struct{}), make(chan struct{})
			calls := 0
			s.recoveryProbe = func(*Issue) (arrRecoveryProbe, error) {
				calls++
				if calls == barrier {
					close(started)
					<-proceed
				}
				return arrRecoveryProbe{}, nil
			}
			done := make(chan error, 1)
			go func() { _, err := s.autoApproveAction(rule, action); done <- err }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("rule did not reach preflight")
			}
			settings := s.Settings()
			for _, enabled := range []bool{false, true} {
				settings.AutoDispatch = enabled
				if _, err := s.SetSettings(settings); err != nil {
					close(proceed)
					t.Fatal(err)
				}
			}
			close(proceed)
			if err := <-done; !errors.Is(err, ErrActionDecisionConflict) {
				t.Fatalf("revoked decision = %v", err)
			}
			got, err := s.GetAction(action)
			if err != nil || got.Status != ActionProposed || got.AutoApproved || fx.count() != 0 || ruleRow(t, s, rule).ApprovedCount != 0 {
				t.Fatalf("revoked rule consumed decision: %+v, executions=%d err=%v", got, fx.count(), err)
			}
		})
	}
}

func TestUserReportRuleStillRunsWithoutAutoDispatch(t *testing.T) {
	s, fx, _, id, _ := autoApprovalFixture(t)
	if _, err := s.db.Exec("UPDATE issues SET source='user' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE agent_runs SET trigger='user_report' WHERE issue_id=?", id); err != nil {
		t.Fatal(err)
	}
	settings := s.Settings()
	settings.AutoDispatch = false
	if _, err := s.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.createOrReactivateApprovalRule(testAdminID, 0, "Waiting to import", ActionManualImport, ""); err != nil {
		t.Fatal(err)
	}
	s.sweepAutoApprovals(time.Now())
	if fx.count() != 1 {
		t.Fatalf("explicit report rule dispatched %d times", fx.count())
	}
}

func TestPreflightRetainsSiblingThatLeavesBeforeNextPoll(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	w := newScenarioWorld(t, start)
	a := importPendingQueueRow(1, "fixture", 1, 1, 0, start)
	b := importPendingQueueRow(2, "fixture", 1, 2, 0, start)
	w.fake.setQueue([]map[string]any{a})
	w.observe()
	id := w.soleOpenAutoIssueID()
	w.fake.setQueue([]map[string]any{a, b})
	if waiting, err := w.svc.preflightArrRecovery(id); err != nil || !waiting {
		t.Fatalf("new sibling: waiting=%v err=%v", waiting, err)
	}
	if got := w.issueByID(id); got.memberCount != 2 || got.EpisodeNumber != 0 || got.ArrQueueID != 0 {
		t.Fatalf("preflight lost sibling: %+v", got)
	}
	w.fake.setQueue([]map[string]any{a})
	if _, err := w.svc.preflightArrRecovery(id); err != nil {
		t.Fatal(err)
	}
	if got := w.issueByID(id); got.memberCount != 2 {
		t.Fatalf("shrinking queue erased member: %+v", got)
	}
	if err := w.svc.ConcludeIssue(context.Background(), id, IssueResolved, "verified one"); err == nil {
		t.Fatal("single-episode conclusion closed pack")
	}
	if _, err := (&Executor{db: w.svc.db}).Execute(context.Background(), id, ActionRescan, json.RawMessage(`{"media_type":"tv"}`), start); err == nil {
		t.Fatal("executor admitted direct grouped rescan")
	}
}

func TestReviewedQueueIDCannotRemoveSiblingFromGrabHistory(t *testing.T) {
	h, _, _, actionID := prepareUnairedQueueProposal(t, -48*time.Hour)
	episodes, files := buildPreAirSeason(28, 2, []preAirEpisode{{number: 3, airsIn: -48 * time.Hour}, {number: 4, airsIn: -48 * time.Hour, hasFile: true}})
	h.fake.setLibrary(episodes, files)
	// Only episode 3 remains in the queue. Failed-download handling for its
	// queue id would also affect episode 4, already missing from that read.
	h.fake.setHistory([]map[string]any{
		{"id": 1, "seriesId": 28, "episodeId": 28203, "eventType": "grabbed", "downloadId": "UNAIREDDOWNLOAD"},
		{"id": 2, "seriesId": 28, "episodeId": 28204, "eventType": "grabbed", "downloadId": "UNAIREDDOWNLOAD"},
	})
	override := json.RawMessage(`{"media_type":"tv","queue_id":41,"action":"remove"}`)
	h.svc.ApproveAction(1, actionID, &override)
	act, err := h.svc.GetAction(actionID)
	if err != nil || act.Status != ActionFailed || act.ResultText == nil || !strings.Contains(*act.ResultText, "reviewed single episode") {
		t.Fatalf("wider remote effect not refused at dispatch: %+v, %v", act, err)
	}
	if got := h.fake.queueDeletesSeen(); len(got) != 0 {
		t.Fatalf("unreviewed sibling was affected: %v", got)
	}
}

func TestUpgradeKeepsVisibleEpisodeHistories(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	w := newScenarioWorld(t, start)
	w.fake.setQueue([]map[string]any{importPendingQueueRow(1, "old-pack", 1, 1, 0, start), importPendingQueueRow(2, "old-pack", 1, 2, 0, start)})
	items, err := w.svc.fetchQueueSnapshot("sonarr", scenarioSonarrID)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the previous release's per-episode keys, including actual
	// promotion, page and thread histories. An upgrade must not merge them.
	groups := groupQueueObservations("sonarr", scenarioSonarrID, items)
	var ids []int64
	for _, g := range groups {
		r, err := w.svc.createAutoObservation("sonarr", scenarioSonarrID, g, start)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.svc.promoteObservedIssue(r.issue.ID, start.Add(11*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := w.svc.PostIssueMessage(context.Background(), r.issue.ID, "Original episode history"); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.issue.ID)
	}
	w.svc.flushIssueAlerts(start.Add(16 * time.Minute))
	if len(ids) != 2 || len(w.rec.pages("issue_created")) != 1 {
		t.Fatal("expected two previously paged episode issues")
	}
	s := NewService(w.svc.db, w.svc.registry, nil, w.rec)
	for _, on := range []bool{false, true} {
		settings := s.Settings()
		settings.AutoDispatch = on
		if _, err := s.SetSettings(settings); err != nil {
			t.Fatal(err)
		}
		at := start.Add(25 * time.Minute)
		if on {
			at = at.Add(25 * time.Minute)
		}
		if err := s.observeQueueSnapshot("sonarr", scenarioSonarrID, items, at); err != nil {
			t.Fatal(err)
		}
		s.flushIssueAlerts(at.Add(issueAlertHoldDown))
	}
	if countRows(t, s, "issues") != 2 || countRows(t, s, "issue_sonarr_members") != 0 || len(w.rec.pages("issue_created")) != 1 {
		t.Fatal("upgrade duplicated, merged or re-paged the legacy histories")
	}
	for _, id := range ids {
		i, err := s.GetIssue(id)
		if err != nil || i.ClosedAt != nil || i.EpisodeNumber < 1 || i.ArrQueueID < 1 {
			t.Fatalf("legacy scope changed: %+v %v", i, err)
		}
		var msgs int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM issue_messages WHERE issue_id=? AND body='Original episode history'", id).Scan(&msgs); err != nil || msgs != 1 {
			t.Fatalf("lost original issue %d history", id)
		}
	}
}
