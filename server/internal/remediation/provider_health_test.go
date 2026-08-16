package remediation

import (
	"context"
	"fmt"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
)

// An unconfigured shared provider is a standing condition, not an agent
// failure. Before this guard, every provider-less run gave up the issue at
// needs_admin (a dead end recoverWork never retries) and fed the auto-dispatch
// circuit breaker — five detected problems on a fresh, provider-less install
// silently reverted the admin's own auto_dispatch setting.

func unavailableResolver() autonomousTurnResolver {
	return autonomousTurnResolverFunc(func(context.Context, ai.AutonomousModelOverride) (ai.AutonomousTurn, error) {
		return ai.AutonomousTurn{}, fmt.Errorf("%w: nothing configured", ai.ErrSharedAIUnavailable)
	})
}

func openProviderIssues(t *testing.T, svc *Service) []Issue {
	t.Helper()
	var out []Issue
	issues, _, err := svc.ListIssues("", 0)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	for _, iss := range issues {
		if iss.Source == SourceSystem && iss.Title == remediationProviderTitle && iss.ClosedAt == nil {
			out = append(out, iss)
		}
	}
	return out
}

// TestRunWithoutProviderLeavesIssueWaiting: the issue must stay open (retryable
// by recoverWork), spend no run, and raise exactly ONE deduped system issue no
// matter how many issues retry.
func TestRunWithoutProviderLeavesIssueWaiting(t *testing.T) {
	r, svc, issueID := newTestRunner(t, &fakeToolHost{}, &scriptedTurn{})
	r.turns = unavailableResolver()

	for i := 0; i < 3; i++ {
		if err := r.Run(context.Background(), issueID); err != nil {
			t.Fatalf("Run %d without provider: %v", i, err)
		}
	}

	var status string
	if err := svc.db.QueryRow("SELECT status FROM issues WHERE id = ?", issueID).Scan(&status); err != nil {
		t.Fatalf("read issue status: %v", err)
	}
	if status != IssueOpen {
		t.Fatalf("issue status = %q, want still %q (retryable, never a needs_admin dead end)", status, IssueOpen)
	}
	if count, _ := agentRunRows(t, svc, issueID); count != 0 {
		t.Fatalf("agent_runs = %d rows, want none for a provider-less attempt", count)
	}
	provider := openProviderIssues(t, svc)
	if len(provider) != 1 {
		t.Fatalf("provider system issues = %d, want exactly one deduped", len(provider))
	}
	if provider[0].Status != IssueNeedsAdmin {
		t.Fatalf("provider issue status = %q, want %q", provider[0].Status, IssueNeedsAdmin)
	}
}

// TestProviderUnavailableRefreshThrottledHourly: recoverWork retries every
// waiting issue once a minute, so the standing system issue must not absorb a
// write (and an occurrences bump) per tick — repeats inside an hour are
// no-ops, and the first repeat past the hour refreshes exactly once.
func TestProviderUnavailableRefreshThrottledHourly(t *testing.T) {
	service, _, _ := setupTestService(t)
	for i := 0; i < 3; i++ {
		if err := service.RecordRemediationProviderHealth(false); err != nil {
			t.Fatalf("record unavailable %d: %v", i, err)
		}
	}
	var occurrences int
	if err := service.db.QueryRow(
		"SELECT occurrences FROM issues WHERE dedupe_key = ?",
		remediationProviderDedupeKey,
	).Scan(&occurrences); err != nil {
		t.Fatalf("read provider issue: %v", err)
	}
	if occurrences != 1 {
		t.Fatalf("occurrences = %d after minutely repeats, want 1 (throttled)", occurrences)
	}

	if _, err := service.db.Exec(
		"UPDATE issues SET updated_at = datetime('now', '-61 minutes') WHERE dedupe_key = ?",
		remediationProviderDedupeKey,
	); err != nil {
		t.Fatalf("backdate provider issue: %v", err)
	}
	if err := service.RecordRemediationProviderHealth(false); err != nil {
		t.Fatalf("record unavailable after an hour: %v", err)
	}
	if err := service.db.QueryRow(
		"SELECT occurrences FROM issues WHERE dedupe_key = ?",
		remediationProviderDedupeKey,
	).Scan(&occurrences); err != nil {
		t.Fatalf("re-read provider issue: %v", err)
	}
	if occurrences != 2 {
		t.Fatalf("occurrences = %d after backdated repeat, want 2", occurrences)
	}
}

// TestProviderRestoredResolvesSystemIssue: the first successful resolve closes
// the standing system issue with its own resolution kind.
func TestProviderRestoredResolvesSystemIssue(t *testing.T) {
	r, svc, issueID := newTestRunner(t, &fakeToolHost{}, &scriptedTurn{})
	r.turns = unavailableResolver()
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run without provider: %v", err)
	}
	if got := openProviderIssues(t, svc); len(got) != 1 {
		t.Fatalf("provider system issues = %d, want one before recovery", len(got))
	}

	r.turns = scriptedTurnResolver(&scriptedTurn{})
	if err := r.Run(context.Background(), issueID); err != nil {
		t.Fatalf("Run with provider restored: %v", err)
	}

	if got := openProviderIssues(t, svc); len(got) != 0 {
		t.Fatalf("provider system issue still open after recovery: %#v", got)
	}
	var kind string
	if err := svc.db.QueryRow(
		"SELECT resolution_kind FROM issues WHERE dedupe_key = ? ORDER BY id DESC LIMIT 1",
		remediationProviderDedupeKey,
	).Scan(&kind); err != nil {
		t.Fatalf("read provider issue resolution kind: %v", err)
	}
	if kind != ResolutionRemediationProviderConfigured {
		t.Fatalf("resolution_kind = %q, want %q", kind, ResolutionRemediationProviderConfigured)
	}
}
