package remediation

import (
	"context"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// A webhook-opened pre-air issue is the first auto incident that is not born
// from a queue row: it deliberately has no issue_observations row and recovers
// by its own season proof. These tests pin the recovery preflight's behavior
// for that shape with the REAL probe path — no recoveryProbe stub — because the
// stub is exactly the seam that let the original defect ship: every runner test
// bypassed probeArrRecovery, so nothing noticed that the queue-shaped preflight
// read the missing observation row as a hard error and parked the issue at
// needs_admin before the agent ever ran.

// preAirRunner builds a Runner over the pre-air fixture service (real registry,
// fake Sonarr, feature enabled) with scripted turns and the shared fake tool
// host. The Service's recoveryProbe stays nil on purpose.
func preAirRunner(t *testing.T, svc *Service, script *scriptedTurn) *Runner {
	t.Helper()
	if _, err := svc.SetSettings(Settings{
		Enabled: true, Mode: ModeSupervised,
		MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, DailyRunCap: 50,
	}); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	return &Runner{
		db:         svc.db,
		svc:        svc,
		toolServer: &fakeToolHost{},
		turns:      scriptedTurnResolver(script),
		procToken:  "test",
	}
}

func agentRunRows(t *testing.T, svc *Service, issueID int64) (count int, lastStatus string) {
	t.Helper()
	rows, err := svc.db.Query("SELECT status FROM agent_runs WHERE issue_id = ? ORDER BY id", issueID)
	if err != nil {
		t.Fatalf("query agent_runs: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		count++
		if err := rows.Scan(&lastStatus); err != nil {
			t.Fatalf("scan agent_runs: %v", err)
		}
	}
	return count, lastStatus
}

// TestRunnerRunsWebhookOpenedPreAirIssue is the defect regression: the season
// is still impossible, so the preflight must let the investigation start and
// the run must park awaiting approval — never needs_admin with no run at all.
func TestRunnerRunsWebhookOpenedPreAirIssue(t *testing.T) {
	svc, _, _ := setupPreAirService(t)
	if err := svc.recordPreAirSeason(preAirSonarrID, 73871, 615, 11, "Futurama"); err != nil {
		t.Fatalf("record pre-air season: %v", err)
	}
	issue := soleIssue(t, svc)
	if issue.Status != IssueOpen {
		t.Fatalf("seed issue status = %q, want %q", issue.Status, IssueOpen)
	}

	script := &scriptedTurn{turns: []ai.TranscriptMessage{{
		Role:    ai.RoleAssistant,
		Content: []ai.TranscriptBlock{toolUse("p1", mcp.ToolProposeAction)},
	}}}
	r := preAirRunner(t, svc, script)

	if err := r.Run(context.Background(), issue.ID); err != nil {
		t.Fatalf("Run over a live pre-air issue: %v", err)
	}

	after := soleIssue(t, svc)
	if after.Status != IssueAwaitingApproval {
		t.Fatalf("issue status after run = %q, want %q (a parked proposal)", after.Status, IssueAwaitingApproval)
	}
	count, status := agentRunRows(t, svc, issue.ID)
	if count != 1 || status != "waiting_approval" {
		t.Fatalf("agent_runs = %d rows (last %q), want exactly one waiting_approval run", count, status)
	}
}

// TestPreAirPreflightClosesRepairedSeason: the season was cleaned (say, by an
// admin acting directly in Sonarr) before the agent got to it. The preflight's
// class proof must close the issue as recovered arr-side and spend no run.
func TestPreAirPreflightClosesRepairedSeason(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	if err := svc.recordPreAirSeason(preAirSonarrID, 73871, 615, 11, "Futurama"); err != nil {
		t.Fatalf("record pre-air season: %v", err)
	}
	issue := soleIssue(t, svc)

	// The repair happened out of band: aired episodes keep their (legitimate,
	// post-air) files, and nothing unaired holds one any more.
	episodes, files := buildPreAirSeason(28, 11, []preAirEpisode{
		{number: 1, airsIn: -21 * 24 * time.Hour, hasFile: true},
		{number: 2, airsIn: -14 * 24 * time.Hour, hasFile: true},
		{number: 3, airsIn: 7 * 24 * time.Hour},
		{number: 4, airsIn: 14 * 24 * time.Hour},
	})
	fake.setLibrary(episodes, files)

	r := preAirRunner(t, svc, &scriptedTurn{})
	if err := r.Run(context.Background(), issue.ID); err != nil {
		t.Fatalf("Run over a repaired pre-air issue: %v", err)
	}

	after := soleIssue(t, svc)
	if after.Status != IssueResolved || after.ResolutionKind != ResolutionArrStateCleared {
		t.Fatalf("issue after run = status %q / kind %q, want %q / %q",
			after.Status, after.ResolutionKind, IssueResolved, ResolutionArrStateCleared)
	}
	if count, _ := agentRunRows(t, svc, issue.ID); count != 0 {
		t.Fatalf("agent_runs = %d rows, want none for a preflight-closed issue", count)
	}
}

// TestPreflightDefersStaleObservation: a fresher snapshot committing between
// the probe's fetch and its store is a timing artifact, not evidence. The
// preflight must defer (report recovering) without touching issue state, so
// the next enqueue retries against current data instead of parking a healthy
// issue for an admin.
func TestPreflightDefersStaleObservation(t *testing.T) {
	svc, _, _ := setupPreAirService(t)
	res, err := svc.db.Exec(
		"INSERT INTO issues (source, status, media_type, tmdb_id, title, detail) VALUES ('user','open','movie',42,'Test Movie','wrong content')",
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()
	svc.recoveryProbe = func(*Issue) (arrRecoveryProbe, error) {
		return arrRecoveryProbe{}, errStaleObservation
	}

	recovering, err := svc.preflightArrRecovery(issueID)
	if err != nil || !recovering {
		t.Fatalf("preflight on stale observation = (%v, %v), want deferred (true, nil)", recovering, err)
	}
	var status string
	if err := svc.db.QueryRow("SELECT status FROM issues WHERE id = ?", issueID).Scan(&status); err != nil {
		t.Fatalf("read issue status: %v", err)
	}
	if status != IssueOpen {
		t.Fatalf("issue status after deferred preflight = %q, want untouched %q", status, IssueOpen)
	}
}
