package remediation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

func prepareUnairedQueueProposal(t *testing.T, airsIn time.Duration) (*pipelineHarness, *Runner, int64, int64) {
	t.Helper()
	h := newPipelineHarness(t)
	episodes, files := buildPreAirSeason(28, 2, []preAirEpisode{{number: 3, airsIn: airsIn}})
	h.fake.setLibrary(episodes, files)
	base := time.Now().UTC().Add(-30 * time.Minute)
	row := stalledUpgradeQueueRow(41, "UNAIREDDOWNLOAD", base.Add(-30*time.Minute))
	row["episode"].(map[string]any)["episodeFileId"] = 0
	row["episode"].(map[string]any)["hasFile"] = false
	row["episodeHasFile"] = false
	h.fake.setQueue([]map[string]any{row})
	h.fake.setHistory([]map[string]any{{"id": 1, "seriesId": 28, "episodeId": 28203, "eventType": "grabbed", "downloadId": "UNAIREDDOWNLOAD", "date": base.Format(time.RFC3339)}})
	h.observe(t, base)
	h.observe(t, base.Add(11*time.Minute))
	issue := soleIssue(t, h.svc)
	r := h.runner(&scriptedTurn{turns: []ai.TranscriptMessage{
		toolCall("r1", "get_queue", `{}`),
		toolCall("p1", mcp.ToolProposeAction, `{"issue_id":0,"kind":"remediate_queue","params":{"media_type":"tv","queue_id":41,"action":"blocklist_search"},"rationale":"Remove this mismatched release."}`),
		toolCall("r2", "get_queue", `{}`),
		toolCall("c1", mcp.ToolConcludeIssue, `{"issue_id":0,"status":"resolved","resolution":"Bad download removed; still waiting for the episode to air."}`),
	}})
	if err := r.Run(context.Background(), issue.ID); err != nil {
		t.Fatal(err)
	}
	var actionID int64
	if err := h.svc.db.QueryRow("SELECT id FROM agent_actions WHERE issue_id=? AND status='proposed'", issue.ID).Scan(&actionID); err != nil {
		t.Fatalf("proposal not recorded: %v; issue=%+v", err, soleIssue(t, h.svc))
	}
	return h, r, issue.ID, actionID
}

func TestUnairedQueueApprovalAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		old    bool
		auto   bool
		final  bool
		resume bool
	}{
		{name: "new proposal normalizes before review"},
		{name: "existing pending proposal downgrades on approval", old: true},
		{name: "automatic approval uses the same guard", old: true, auto: true},
		{name: "air date changes immediately before dispatch", old: true, final: true},
		{name: "resume verifies intended absence", resume: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			air := 48 * time.Hour
			wantProposal := "blocklist_only"
			if tc.old {
				air = -48 * time.Hour
				wantProposal = "blocklist_search"
			}
			h, r, issueID, actionID := prepareUnairedQueueProposal(t, air)
			act, _ := h.svc.GetAction(actionID)
			var original RemediateQueueParams
			if json.Unmarshal(act.Params, &original) != nil || original.Action != wantProposal {
				t.Fatalf("original params=%s, want %s", act.Params, wantProposal)
			}
			episodes, files := buildPreAirSeason(28, 2, []preAirEpisode{{number: 3, airsIn: 48 * time.Hour}})
			if tc.final {
				reads := 0
				h.svc.recoveryProbe = func(*Issue) (arrRecoveryProbe, error) {
					reads++
					if reads == 2 {
						h.fake.setLibrary(episodes, files)
					}
					return arrRecoveryProbe{}, nil
				}
			} else {
				h.fake.setLibrary(episodes, files)
			}
			var ruleID int64
			if tc.auto {
				var err error
				ruleID, err = h.svc.createOrReactivateApprovalRule(1, 0, "Download stalled", ActionRemediateQueue, "blocklist_search")
				if err != nil {
					t.Fatal(err)
				}
				h.svc.sweepAutoApprovals(time.Now().UTC())
			} else if _, err := h.svc.ApproveAction(1, actionID, nil); err != nil {
				t.Fatal(err)
			}
			act, _ = h.svc.GetAction(actionID)
			var effective RemediateQueueParams
			if act.Status != ActionExecuted || act.ApprovedParams == nil || json.Unmarshal(*act.ApprovedParams, &effective) != nil || effective.Action != "blocklist_only" {
				t.Fatalf("unexpected action: %+v", act)
			}
			if !strings.Contains(string(act.Params), wantProposal) || act.ResultText == nil || !strings.Contains(*act.ResultText, "has not aired yet") {
				t.Fatalf("audit lost original proposal or suppression reason: %+v", act)
			}
			if deletes := h.fake.queueDeletesSeen(); len(deletes) != 1 || !strings.Contains(deletes[0], "skipRedownload=true") {
				t.Fatalf("wrong deletes: %v", deletes)
			}
			// A lost approval response and a restarted service cannot replay DELETE.
			restarted := NewService(h.svc.db, h.svc.registry, h.svc.bridge, h.notifier)
			if _, err := restarted.ApproveAction(1, actionID, nil); err != nil || len(h.fake.queueDeletesSeen()) != 1 {
				t.Fatalf("approval replay: %v", err)
			}
			h.svc = restarted
			r.svc = restarted
			h.observe(t, time.Now().UTC())
			if got := soleIssue(t, h.svc); got.Status == IssueResolved {
				t.Fatal("resolved before queue absence settled")
			}
			if _, err := h.svc.db.Exec("UPDATE issue_observations SET settling_since=? WHERE issue_id=?", time.Now().UTC().Add(-3*time.Minute), issueID); err != nil {
				t.Fatal(err)
			}
			if tc.resume {
				// A persisted resume can be eligible before the next observer
				// pass (for example after upgrading with a staged old handoff).
				if _, err := h.svc.db.Exec("UPDATE issues SET status='open' WHERE id=?", issueID); err != nil {
					t.Fatal(err)
				}
				if err := r.Resume(context.Background(), issueID); err != nil {
					t.Fatal(err)
				}
			} else {
				h.observe(t, time.Now().UTC())
			}
			got := soleIssue(t, h.svc)
			if got.Status != IssueResolved || got.ResolutionKind != ResolutionRemovedWaitingForAir || got.Resolution != removedWaitingForAirResolution {
				t.Fatalf("cleanup not resolved honestly: %+v", got)
			}
			// Neither a queued resume nor later observation may search/re-promote.
			if err := r.Resume(context.Background(), issueID); err != nil {
				t.Fatal(err)
			}
			h.observe(t, time.Now().UTC().Add(15*time.Minute))
			if got := soleIssue(t, h.svc); got.Status != IssueResolved {
				t.Fatalf("cleanup re-promoted: %s", got.Status)
			}
			if tc.auto {
				rule := ruleRow(t, h.svc, ruleID)
				if rule.Status != ApprovalRuleActive || rule.ResolvedCount != 1 {
					t.Fatalf("successful rule was penalized: %+v", rule)
				}
			}
		})
	}
}

func TestUnairedQueueProofRejectsUnprovenEndings(t *testing.T) {
	h, _, _, actionID := prepareUnairedQueueProposal(t, 48*time.Hour)
	if _, err := h.svc.ApproveAction(1, actionID, nil); err != nil {
		t.Fatal(err)
	}
	issue := soleIssue(t, h.svc)
	if proven, err := h.svc.unairedQueueRemovalProven(&issue); err != nil || !proven {
		t.Fatalf("valid proof=%v err=%v", proven, err)
	}
	user := issue
	user.Source = SourceUser
	if proven, err := h.svc.unairedQueueRemovalProven(&user); err != nil || proven {
		t.Fatalf("user report auto-closed: proof=%v err=%v", proven, err)
	}
	for _, tc := range []struct {
		name string
		air  time.Duration
		file bool
	}{
		{name: "episode has now aired", air: -time.Hour},
		{name: "file arrived independently", air: time.Hour, file: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eps, files := buildPreAirSeason(28, 2, []preAirEpisode{{number: 3, airsIn: tc.air, hasFile: tc.file}})
			h.fake.setLibrary(eps, files)
			if proven, err := h.svc.unairedQueueRemovalProven(&issue); err != nil || proven {
				t.Fatalf("unproven ending accepted: proof=%v err=%v", proven, err)
			}
		})
	}
	eps, files := buildPreAirSeason(28, 2, []preAirEpisode{{number: 3, airsIn: 48 * time.Hour}, {number: 4, airsIn: -time.Hour}})
	h.fake.setLibrary(eps, files)
	h.fake.setHistory([]map[string]any{
		{"id": 1, "seriesId": 28, "episodeId": 28203, "eventType": "grabbed", "downloadId": "UNAIREDDOWNLOAD"},
		{"id": 2, "seriesId": 28, "episodeId": 28204, "eventType": "grabbed", "downloadId": "UNAIREDDOWNLOAD"},
	})
	if proven, err := h.svc.unairedQueueRemovalProven(&issue); err != nil || proven {
		t.Fatalf("mixed pack's aired gap was ignored: proof=%v err=%v", proven, err)
	}
}

func TestUnairedRemediationCannotFollowCleanupWithSearch(t *testing.T) {
	h, _, issueID, _ := prepareUnairedQueueProposal(t, 48*time.Hour)
	e := NewExecutor(h.svc.registry, h.svc.bridge, h.svc.db)
	_, err := e.Execute(context.Background(), issueID, ActionTriggerSearch, json.RawMessage(`{"media_type":"tv","tmdb_id":615,"season":2,"episode":3}`), time.Now())
	if err == nil || !strings.Contains(err.Error(), "has not aired yet") {
		t.Fatalf("unaired search was not refused: %v", err)
	}
	if h.fake.countRequests("/api/v3/command") != 0 {
		t.Fatal("unaired search dispatched a Sonarr command")
	}
}

func TestApprovedBlocklistOnlyStaysOnlyAfterAir(t *testing.T) {
	h, _, _, actionID := prepareUnairedQueueProposal(t, 48*time.Hour)
	eps, files := buildPreAirSeason(28, 2, []preAirEpisode{{number: 3, airsIn: -time.Hour}})
	h.fake.setLibrary(eps, files)
	act, err := h.svc.ApproveAction(1, actionID, nil)
	if err != nil || act.Status != ActionExecuted {
		t.Fatalf("approve: %v %+v", err, act)
	}
	if deletes := h.fake.queueDeletesSeen(); len(deletes) != 1 || !strings.Contains(deletes[0], "skipRedownload=true") {
		t.Fatalf("blocklist-only was upgraded after air: %v", deletes)
	}
}
