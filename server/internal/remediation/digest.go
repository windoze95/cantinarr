package remediation

import (
	"fmt"
	"time"
)

// AgentDigest is the rolling scoreboard: what the agent pipeline did over a
// window, computed live from the tables that already record everything. The
// system pages reliably when it needs a decision and says nothing when
// automation works — this is the deliberate pull surface that makes the quiet
// weeks legible, so a well-tuned agent stops reading as "the thing that only
// ever brings me problems".
type AgentDigest struct {
	Days int `json:"days"`

	IssuesOpened int64 `json:"issues_opened"`
	// IssuesResolved counts resolved issues that were ever real work. An auto
	// incident that never promoted is deliberately NOT in this population: it
	// was observed, it cleared on its own before anyone was asked to look, and
	// it closed silently by design (closeObservedRecovery passes
	// silentNotifications for exactly these). Counting them made a busy queue
	// read as agent accomplishment — a live instance reported 680 "resolved"
	// against a single rule-approved fix.
	//
	// That excluded population is not reported anywhere, on purpose. It is
	// ordinary *arr life: an archive still extracting, an import that lands a
	// minute later, a download the client resumes. Some of it is the arr's own
	// retry machinery working, and some was never a problem at all — the sweep
	// simply looked mid-flight. Neither is something the agent did, and a large
	// number sitting next to a small honest one is read as the big number.
	IssuesResolved int64 `json:"issues_resolved"`
	// ZeroTouch counts resolved issues where automation carried the whole way:
	// at least one action actually EXECUTED and no human decided any of them.
	// The executed requirement is what keeps "earned autonomy doing its job end
	// to end" from silently absorbing every incident that fixed itself.
	ZeroTouch        int64 `json:"zero_touch"`
	ActionsExecuted  int64 `json:"actions_executed"`
	RuleApproved     int64 `json:"rule_approved"`
	ReporterClosed   int64 `json:"reporter_closed"`
	TokensIn         int64 `json:"tokens_in"`
	TokensOut        int64 `json:"tokens_out"`
	NeedsAdminOpen   int64 `json:"needs_admin_open"`
	PendingProposals int64 `json:"pending_proposals"`
	PausedRules      int64 `json:"paused_rules"`

	// RuleCounts names the rules that did the work: label -> executed count in
	// the window, newest-heavy rules first in the slice.
	RuleCounts []DigestRuleCount `json:"rule_counts"`

	GeneratedAt time.Time `json:"generated_at"`
}

// DigestRuleCount is one standing rule's contribution to the window.
type DigestRuleCount struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// Digest computes the agent scoreboard for the trailing N days (1..90).
func (s *Service) Digest(days int) (*AgentDigest, error) {
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	cutoff := fmt.Sprintf("-%d days", days)
	d := &AgentDigest{Days: days, GeneratedAt: time.Now().UTC()}

	// A never-promoted auto incident is observation noise, not work: it cleared
	// before promotion ever asked for an agent or a human. The source guard
	// matters — a USER-reported issue can also carry an observation row (see
	// cancelExecutingForRecovery), and a reporter's issue is real work by
	// definition, so it must never be filtered out by this clause.
	const observationNoise = `EXISTS (SELECT 1 FROM issue_observations o
		WHERE o.issue_id = i.id AND o.promoted_at IS NULL) AND i.source = ?3`

	row := s.db.QueryRow(`SELECT
		(SELECT COUNT(1) FROM issues WHERE created_at >= datetime('now', ?1)),
		(SELECT COUNT(1) FROM issues i WHERE i.closed_at >= datetime('now', ?1) AND i.status = 'resolved'
		   AND NOT (`+observationNoise+`)),
		(SELECT COUNT(1) FROM issues i WHERE i.closed_at >= datetime('now', ?1) AND i.status = 'resolved'
		   AND NOT (`+observationNoise+`)
		   AND EXISTS (SELECT 1 FROM agent_actions a WHERE a.issue_id = i.id AND a.status = 'executed')
		   AND NOT EXISTS (SELECT 1 FROM agent_actions a WHERE a.issue_id = i.id AND a.decided_by IS NOT NULL)),
		(SELECT COUNT(1) FROM agent_actions WHERE executed_at >= datetime('now', ?1) AND status = 'executed'),
		(SELECT COUNT(1) FROM agent_actions WHERE executed_at >= datetime('now', ?1) AND status = 'executed' AND auto_rule_id IS NOT NULL AND decided_by IS NULL),
		(SELECT COUNT(1) FROM issues WHERE closed_at >= datetime('now', ?1) AND resolution_kind = ?2),
		(SELECT COALESCE(SUM(input_tokens), 0) FROM agent_runs WHERE started_at >= datetime('now', ?1)),
		(SELECT COALESCE(SUM(output_tokens), 0) FROM agent_runs WHERE started_at >= datetime('now', ?1)),
		(SELECT COUNT(1) FROM issues WHERE closed_at IS NULL AND status = 'needs_admin'),
		(SELECT COUNT(1) FROM agent_actions a JOIN issues i ON i.id = a.issue_id
		   WHERE a.status = 'proposed' AND i.closed_at IS NULL AND i.status = 'awaiting_approval'),
		(SELECT COUNT(1) FROM agent_approval_rules WHERE status = 'paused')`,
		cutoff, ResolutionReporterConfirmed, SourceAuto,
	)
	if err := row.Scan(&d.IssuesOpened, &d.IssuesResolved, &d.ZeroTouch, &d.ActionsExecuted,
		&d.RuleApproved, &d.ReporterClosed, &d.TokensIn, &d.TokensOut,
		&d.NeedsAdminOpen, &d.PendingProposals, &d.PausedRules); err != nil {
		return nil, fmt.Errorf("compute agent digest: %w", err)
	}

	rows, err := s.db.Query(`
		SELECT r.problem_kind, r.action_kind, r.action_facet, COUNT(1)
		FROM agent_actions a
		JOIN agent_approval_rules r ON r.id = a.auto_rule_id
		WHERE a.executed_at >= datetime('now', ?) AND a.status = 'executed'
		GROUP BY r.id ORDER BY COUNT(1) DESC LIMIT 10`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("compute digest rule counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var problem, kind, facet string
		var count int64
		if err := rows.Scan(&problem, &kind, &facet, &count); err != nil {
			return nil, err
		}
		d.RuleCounts = append(d.RuleCounts, DigestRuleCount{
			Label: approvalRuleLabel(problem, ActionKind(kind), facet),
			Count: count,
		})
	}
	return d, rows.Err()
}

// digestPushStampKey remembers when the weekly scoreboard last paged, in the
// settings kv so it survives restarts without a schema change.
const digestPushStampKey = "remediation_digest_last_push"

// SweepWeeklyDigest pages the weekly scoreboard when a week has passed AND the
// week has something to say — a digest of zeros is the nag this system exists
// to avoid, so silence stays honest. Rides the existing hourly worker tick.
func (s *Service) SweepWeeklyDigest(now time.Time) {
	if !s.Settings().Enabled || s.notifier == nil {
		return
	}
	var last string
	_ = s.db.QueryRow("SELECT value FROM settings WHERE key = ?", digestPushStampKey).Scan(&last)
	if last != "" {
		if at, err := time.Parse(time.RFC3339, last); err == nil && now.Sub(at) < 7*24*time.Hour {
			return
		}
	}
	digest, err := s.Digest(7)
	if err != nil {
		return
	}
	if digest.IssuesResolved+digest.ZeroTouch+digest.RuleApproved+
		digest.NeedsAdminOpen+digest.PendingProposals == 0 {
		// Nothing happened and nothing waits: say nothing, but advance the
		// stamp so a later busy week is measured against a fresh window.
		_, _ = s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
			digestPushStampKey, now.UTC().Format(time.RFC3339))
		return
	}
	s.notifier.NotifyAdmins("agent_digest", map[string]interface{}{
		"issues_resolved":   digest.IssuesResolved,
		"zero_touch":        digest.ZeroTouch,
		"rule_approved":     digest.RuleApproved,
		"needs_admin_open":  digest.NeedsAdminOpen,
		"pending_proposals": digest.PendingProposals,
	})
	_, _ = s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
		digestPushStampKey, now.UTC().Format(time.RFC3339))
}
