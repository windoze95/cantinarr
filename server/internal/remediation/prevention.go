package remediation

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// Telling an admin that a problem keeps happening, and what setting would stop
// it.
//
// The agent repairs incidents one at a time and closes them. Nobody in the
// product has ever said "this is the fourth time this month, and repairing it
// again will not help" — the admin has to notice the pattern themselves, across
// issues they mostly never see because the agent handled them.
//
// The evidence is already in the database: issues.problem_kind carries a
// server-authored diagnosis label on every auto-detected incident. What was
// missing is a definition of "keeps happening" that is actually true, and a way
// to say it once instead of every hour.

const (
	// preventionWindow bounds how far back a pattern is assembled from.
	preventionWindow = 90 * 24 * time.Hour
	// preventionMinIssues, preventionMinMedia and preventionMinDays are the
	// three conditions a pattern must meet together.
	//
	// preventionMinDays is the one that carries the meaning. An auto issue is
	// opened per exact media scope, so ONE five-minute event — the download
	// client drops, a disk fills, a path mapping is wrong — fans out into dozens
	// of issues across dozens of titles within the same minute. Counting issues
	// and distinct titles alone, Cantinarr would announce "this keeps happening"
	// about something that happened once, which is precisely the lie this
	// feature exists to avoid. Separate calendar days is recurrence; fan-out is
	// not. Distinct media stays as a secondary guard so one title flapping for a
	// week cannot pass either.
	preventionMinIssues = 3
	preventionMinMedia  = 2
	preventionMinDays   = 3
	// preventionDedupePrefix namespaces the notice's issue key away from every
	// other system issue and from queue incidents.
	preventionDedupePrefix = "system:prevention:"
)

// Cooldowns, chosen by what the admin DID about the last notice. The thread
// screen already offers three closures on a needs_admin issue, and "Close
// without fix" is literally an administrator saying they have decided not to
// fix this — so it is the mute, and it needs no new control anywhere.
const (
	preventionCooldownResolved  = 60 * 24 * time.Hour
	preventionCooldownDismissed = 180 * 24 * time.Hour
	preventionCooldownWontFix   = 365 * 24 * time.Hour
	// preventionRaiseCap is the raise count past which every cooldown becomes
	// the longest one. A cause nobody has fixed after this many tellings is not
	// going to be fixed by telling them more often.
	preventionRaiseCap = 3
)

// preventionCandidate is one (instance, problem label) pattern as measured over
// the window.
type preventionCandidate struct {
	instanceID    string
	problemKind   string
	issueCount    int
	distinctMedia int
	distinctDays  int
	firstSeen     string
	lastSeen      string
}

// preventionScopeKey identifies one notice. Hashing the label keeps a key that
// contains spaces, apostrophes and an em dash out of a string that is compared
// by equality everywhere.
func preventionScopeKey(instanceID, problemKind string) string {
	sum := sha256.Sum256([]byte(instanceID + "|" + problemKind))
	return preventionDedupePrefix + instanceID + ":" + hex.EncodeToString(sum[:8])
}

// SweepPreventionNotices looks for problems that keep coming back on one
// instance and, where there is honest advice to give, tells the admin once.
func (s *Service) SweepPreventionNotices() {
	s.sweepPreventionLiveChanges(time.Now().UTC())
	now := time.Now().UTC()
	candidates, err := s.preventionCandidates(now)
	if err != nil {
		log.Printf("remediation: prevention sweep: %v", err)
		return
	}
	for _, c := range candidates {
		if err := s.considerPreventionNotice(c, now); err != nil {
			log.Printf("remediation: prevention notice for %s/%s: %v", c.instanceID, c.problemKind, err)
		}
	}
}

// preventionCandidates rolls recent auto issues up per (instance, problem) and
// returns the ones that meet all three thresholds.
//
// The window's lower bound per row is the LATER of "the last 90 days" and "when
// we last raised this notice", applied in considerPreventionNotice. Assembling
// the wider window here and narrowing per notice keeps this to one indexed pass
// rather than one query per known notice.
func (s *Service) preventionCandidates(now time.Time) ([]preventionCandidate, error) {
	rows, err := s.db.Query(
		// source = 'auto' is a LITERAL on purpose, against this package's usual
		// habit of binding the constant: it is the predicate on
		// idx_issues_problem_recurrence, and only a literal lets SQLite use that
		// partial index as a covering index and satisfy the GROUP BY from index
		// order instead of scanning issues into a temp B-tree.
		//
		// substr(created_at, 1, 10) rather than date(): the sqlite driver stores
		// a bound time.Time as Go's own String() form ("... +0000 UTC"), while a
		// row that omits the column gets SQLite's CURRENT_TIMESTAMP. Both
		// encodings live in this column, and date() returns NULL for the first —
		// which would silently collapse the distinct-day count to 1 and make the
		// day threshold meaningless. Both share the leading YYYY-MM-DD.
		//
		// The EXISTS clause is not decoration: deleting an instance does not
		// clean up its issues, so without it a notice could be raised about the
		// settings of an *arr that was removed last week.
		`SELECT i.instance_id, i.problem_kind,
		        COUNT(*),
		        COUNT(DISTINCT i.dedupe_key),
		        COUNT(DISTINCT substr(i.created_at, 1, 10)),
		        MIN(i.created_at), MAX(i.created_at)
		   FROM issues i
		  WHERE i.source = 'auto'
		    AND i.problem_kind IS NOT NULL AND i.problem_kind != ''
		    AND i.instance_id IS NOT NULL AND i.instance_id != ''
		    AND i.created_at >= ?
		    AND EXISTS (SELECT 1 FROM service_instances s WHERE s.id = i.instance_id)
		  GROUP BY i.instance_id, i.problem_kind
		 HAVING COUNT(*) >= ? AND COUNT(DISTINCT i.dedupe_key) >= ? AND COUNT(DISTINCT substr(i.created_at, 1, 10)) >= ?`,
		now.Add(-preventionWindow), preventionMinIssues, preventionMinMedia, preventionMinDays,
	)
	if err != nil {
		return nil, fmt.Errorf("roll up recurring problems: %w", err)
	}
	defer rows.Close()
	var out []preventionCandidate
	for rows.Next() {
		var (
			c           preventionCandidate
			instanceID  sql.NullString
			problemKind sql.NullString
			first, last sql.NullString
		)
		// Nullable scan targets even though the WHERE clause excludes NULLs.
		// A plain string here makes that filter load-bearing far beyond its
		// purpose: one unlabelled group would fail this scan and abort the
		// ENTIRE pass, silently suppressing every legitimate notice rather than
		// just its own.
		if err := rows.Scan(&instanceID, &problemKind, &c.issueCount,
			&c.distinctMedia, &c.distinctDays, &first, &last); err != nil {
			return nil, fmt.Errorf("scan recurring problem: %w", err)
		}
		if !instanceID.Valid || !problemKind.Valid {
			continue
		}
		c.instanceID, c.problemKind = instanceID.String, problemKind.String
		c.firstSeen, c.lastSeen = first.String, last.String
		out = append(out, c)
	}
	return out, rows.Err()
}

// preventionNotice is the stored memory of what we have already said.
type preventionNotice struct {
	id           int64
	raiseCount   int
	lastRaisedAt sql.NullTime
	issueID      sql.NullInt64
}

// considerPreventionNotice decides whether this pattern is worth saying now.
func (s *Service) considerPreventionNotice(c preventionCandidate, now time.Time) error {
	advice, ok := arr.PreventionFor(c.problemKind)
	if !ok {
		// No honest advice for this label. Staying quiet is the feature: a
		// notice that says "this keeps happening" and then has nothing to
		// suggest is worse than never speaking.
		return nil
	}
	notice, err := s.loadPreventionNotice(c.instanceID, c.problemKind)
	if err != nil {
		return err
	}
	if notice != nil {
		open, err := s.preventionIssueStillOpen(notice.issueID)
		if err != nil {
			return err
		}
		if open {
			// The last notice is still in front of the admin, so there is
			// nothing new to say — but the counts ARE the argument for it, and
			// an admin who leaves it open for a month should not still be
			// reading the three occurrences it was raised on. Refresh them
			// without touching `read`: making it unread again would be the nag
			// this whole design is built to avoid.
			return s.refreshOpenPreventionIssue(notice, c, advice, now)
		}
		ready, err := s.preventionReadyToRaise(notice, c, now)
		if err != nil || !ready {
			return err
		}
	}
	return s.raisePreventionNotice(notice, c, advice, now)
}

func (s *Service) loadPreventionNotice(instanceID, problemKind string) (*preventionNotice, error) {
	var n preventionNotice
	err := s.db.QueryRow(
		`SELECT id, raise_count, last_raised_at, issue_id FROM prevention_notices
		 WHERE instance_id = ? AND problem_kind = ?`, instanceID, problemKind,
	).Scan(&n.id, &n.raiseCount, &n.lastRaisedAt, &n.issueID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load prevention notice: %w", err)
	}
	return &n, nil
}

// preventionReadyToRaise answers whether an already-told pattern deserves to be
// told again.
//
// Two independent conditions, and the first is the one that stops this being a
// nag. The threshold must be met AGAIN since we last spoke — not merely be
// visible in a sliding 90-day history, which would re-raise forever on the
// strength of incidents the admin already saw. A cause that actually stopped
// never comes back, whatever the cooldown says.
func (s *Service) preventionReadyToRaise(notice *preventionNotice, c preventionCandidate, now time.Time) (bool, error) {
	if !notice.lastRaisedAt.Valid {
		return true, nil
	}
	status, kind, closed, err := s.preventionIssueDisposition(notice.issueID)
	if err != nil {
		return false, err
	}
	if !closed {
		// Belt and braces: the caller already refreshed and returned for an open
		// notice. Saying it again would only be a second copy of the same row.
		return false, nil
	}
	if now.Before(notice.lastRaisedAt.Time.Add(preventionCooldown(status, kind, notice.raiseCount))) {
		return false, nil
	}
	// Cooldown served: require the pattern to have re-formed since we spoke.
	fresh, err := s.preventionGrewSince(c, notice.lastRaisedAt.Time)
	if err != nil {
		return false, err
	}
	return fresh, nil
}

// preventionCooldown maps what the admin did about the last notice onto how
// long to wait before raising it again.
func preventionCooldown(status, resolutionKind string, raiseCount int) time.Duration {
	if raiseCount >= preventionRaiseCap {
		return preventionCooldownWontFix
	}
	switch {
	case status == IssueWontFix:
		// "Close without fix" is a decision, not an oversight.
		return preventionCooldownWontFix
	case status == IssueDismissed || resolutionKind == ResolutionAdminDismissed:
		return preventionCooldownDismissed
	default:
		return preventionCooldownResolved
	}
}

// preventionIssueDisposition reads how the last notice's issue ended. A missing
// or deleted issue counts as closed: there is nothing in front of the admin.
func (s *Service) preventionIssueDisposition(issueID sql.NullInt64) (status, kind string, closed bool, err error) {
	if !issueID.Valid {
		return "", "", true, nil
	}
	var closedAt sql.NullTime
	err = s.db.QueryRow(
		"SELECT status, COALESCE(resolution_kind, ''), closed_at FROM issues WHERE id = ?", issueID.Int64,
	).Scan(&status, &kind, &closedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", true, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("read prevention issue disposition: %w", err)
	}
	return status, kind, closedAt.Valid, nil
}

// preventionGrewSince re-measures the same pattern using only incidents newer
// than the last raise.
func (s *Service) preventionGrewSince(c preventionCandidate, since time.Time) (bool, error) {
	var issues, media, days int
	if err := s.db.QueryRow(
		`SELECT COUNT(*), COUNT(DISTINCT dedupe_key), COUNT(DISTINCT substr(created_at, 1, 10))
		   FROM issues
		  WHERE source = 'auto' AND instance_id = ? AND problem_kind = ? AND created_at > ?`,
		c.instanceID, c.problemKind, since,
	).Scan(&issues, &media, &days); err != nil {
		return false, fmt.Errorf("measure prevention growth: %w", err)
	}
	return issues >= preventionMinIssues && media >= preventionMinMedia && days >= preventionMinDays, nil
}

// raisePreventionNotice opens (or refreshes) the admin-facing issue and records
// that we said it.
func (s *Service) raisePreventionNotice(notice *preventionNotice, c preventionCandidate, advice arr.Prevention, now time.Time) error {
	instanceName := s.preventionInstanceName(c.instanceID)
	issueID, err := s.openPreventionIssue(c, advice, instanceName, now)
	if err != nil {
		return err
	}
	if notice == nil {
		if _, err := s.db.Exec(
			`INSERT INTO prevention_notices
			 (instance_id, problem_kind, raise_count, occurrences, distinct_media, distinct_days,
			  first_seen_at, last_seen_at, last_raised_at, issue_id)
			 VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?)`,
			c.instanceID, c.problemKind, c.issueCount, c.distinctMedia, c.distinctDays,
			c.firstSeen, c.lastSeen, now, issueID,
		); err != nil {
			return fmt.Errorf("record prevention notice: %w", err)
		}
		return nil
	}
	if _, err := s.db.Exec(
		`UPDATE prevention_notices
		    SET raise_count = raise_count + 1, occurrences = ?, distinct_media = ?, distinct_days = ?,
		        last_seen_at = ?, last_raised_at = ?, issue_id = ?
		  WHERE id = ?`,
		c.issueCount, c.distinctMedia, c.distinctDays, c.lastSeen, now, issueID, notice.id,
	); err != nil {
		return fmt.Errorf("update prevention notice: %w", err)
	}
	return nil
}

// refreshOpenPreventionIssue keeps a notice the admin has not dealt with yet
// telling the truth about how often the problem has happened since.
//
// Deliberately leaves `read` alone and does not bump raise_count: this is the
// same notice saying the same thing, only with current numbers.
func (s *Service) refreshOpenPreventionIssue(notice *preventionNotice, c preventionCandidate, advice arr.Prevention, now time.Time) error {
	if !notice.issueID.Valid {
		return nil
	}
	if _, err := s.db.Exec(
		`UPDATE issues SET detail = ?, resolution = ?, updated_at = ?
		  WHERE id = ? AND closed_at IS NULL`,
		preventionDetail(c, advice), preventionSteps(advice), now, notice.issueID.Int64,
	); err != nil {
		return fmt.Errorf("refresh open prevention issue: %w", err)
	}
	if _, err := s.db.Exec(
		`UPDATE prevention_notices
		    SET occurrences = ?, distinct_media = ?, distinct_days = ?, last_seen_at = ?
		  WHERE id = ?`,
		c.issueCount, c.distinctMedia, c.distinctDays, c.lastSeen, notice.id,
	); err != nil {
		return fmt.Errorf("refresh prevention notice counts: %w", err)
	}
	s.pingIssueUpdated(notice.issueID.Int64)
	return nil
}

// preventionIssueStillOpen reports whether the notice's issue is in front of an
// admin right now. A missing or deleted issue is not.
func (s *Service) preventionIssueStillOpen(issueID sql.NullInt64) (bool, error) {
	_, _, closed, err := s.preventionIssueDisposition(issueID)
	return !closed, err
}

// openPreventionIssue creates the admin-facing surface, or refreshes the one
// already open for this key.
//
// Modelled on the system health sinks, with two deliberate differences:
// problem_kind is left NULL so the notice can never be counted as an occurrence
// of the very problem it describes, and the alert goes through the hold-down
// queue rather than pushing immediately — none of this is urgent, and an issue
// closed inside the window should never have paged at all.
// sweepPreventionLiveChanges auto-resolves open recurrence notices whose
// NAMED settings changed since the notice was raised — advice that notices
// being taken. The problem label is deliberately not stored on the row (its
// NULL problem_kind is what keeps a notice from matching rules or counting as
// its own occurrence), so it is recovered by matching each live-section
// catalog label's scope key against the row's dedupe key. Resolution happens
// only on CHANGE from a baseline the notice actually captured: a notice
// raised while the service was unreadable has no baseline and never resolves
// on first sight.
func (s *Service) sweepPreventionLiveChanges(now time.Time) {
	rows, err := s.db.Query(
		`SELECT id, instance_id, dedupe_key, COALESCE(detail, '')
		 FROM issues WHERE closed_at IS NULL AND source = ? AND dedupe_key LIKE ?`,
		SourceSystem, preventionDedupePrefix+"%",
	)
	if err != nil {
		return
	}
	type openNotice struct {
		id         int64
		instanceID string
		dedupeKey  string
		detail     string
	}
	var notices []openNotice
	for rows.Next() {
		var n openNotice
		if err := rows.Scan(&n.id, &n.instanceID, &n.dedupeKey, &n.detail); err == nil {
			notices = append(notices, n)
		}
	}
	rows.Close()
	for _, n := range notices {
		problem := ""
		for _, candidate := range arr.PreventionProblems() {
			if _, live := arr.PreventionLiveSection(candidate); !live {
				continue
			}
			if preventionScopeKey(n.instanceID, candidate) == n.dedupeKey {
				problem = candidate
				break
			}
		}
		if problem == "" {
			continue
		}
		if !strings.Contains(n.detail, "Current ") {
			continue // no baseline captured at raise; nothing to compare.
		}
		current := s.preventionLiveBlock(n.instanceID, problem)
		if current == "" || strings.Contains(n.detail, current) {
			continue // unreadable now, or unchanged.
		}
		resolution := "The settings this notice pointed at have changed since it was raised. If the problem re-forms from newer incidents, a fresh notice will say so."
		if _, err := s.db.Exec(
			`UPDATE issues SET status = ?, read = 0, resolution = ?, resolution_kind = ?,
			 updated_at = ?, closed_at = ? WHERE id = ? AND closed_at IS NULL`,
			IssueResolved, resolution, ResolutionPreventionSettingChanged, now, now, n.id,
		); err != nil {
			continue
		}
		_, _ = s.db.Exec(
			"INSERT INTO issue_messages (issue_id, author_kind, body) VALUES (?, ?, ?)",
			n.id, AuthorSystem, resolution,
		)
		s.pingIssueUpdated(n.id)
	}
}

// preventionLiveBlock renders the CURRENT values of the settings a notice
// names, when the problem maps to a readable config section. This is the
// difference between "check your indexer's minimum seeders" and "NZBgeek min
// seeders 0". Secret-free by construction (the client's GetConfigSummary
// allowlist), redacted anyway, and best-effort: an unreadable service costs
// the quote, never the notice. The fixed Steps stay untouched — live values
// join the measurement, never the instructions.
func (s *Service) preventionLiveBlock(instanceID, problemKind string) string {
	section, ok := arr.PreventionLiveSection(problemKind)
	if !ok || s.registry == nil || instanceID == "" {
		return ""
	}
	var entries []arr.ConfigEntry
	var err error
	if client, cerr := s.registry.GetSonarrClient(instanceID); cerr == nil {
		entries, err = client.GetConfigSummary(section)
	} else if client, cerr := s.registry.GetRadarrClient(instanceID); cerr == nil {
		entries, err = client.GetConfigSummary(section)
	} else if client, cerr := s.registry.GetChaptarrClient(instanceID); cerr == nil {
		entries, err = client.GetConfigSummary(section)
	} else {
		return ""
	}
	if err != nil || len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Current %s:", strings.ReplaceAll(section, "_", " "))
	for _, e := range entries {
		fmt.Fprintf(&sb, "\n- %s: %s", e.Name, e.Detail)
	}
	return secrets.RedactText(sb.String())
}

func (s *Service) openPreventionIssue(c preventionCandidate, advice arr.Prevention, instanceName string, now time.Time) (int64, error) {
	key := preventionScopeKey(c.instanceID, c.problemKind)
	title := preventionTitle(c.problemKind, instanceName)
	detail := preventionDetail(c, advice)
	if live := s.preventionLiveBlock(c.instanceID, c.problemKind); live != "" {
		detail = detail + "\n\n" + live
	}
	steps := preventionSteps(advice)

	var issueID int64
	err := s.db.QueryRow(
		"SELECT id FROM issues WHERE dedupe_key = ? AND closed_at IS NULL", key,
	).Scan(&issueID)
	if err == nil {
		if _, uerr := s.db.Exec(
			`UPDATE issues SET detail = ?, resolution = ?, read = 0,
			        occurrences = occurrences + 1, updated_at = ?
			  WHERE id = ? AND closed_at IS NULL`,
			detail, steps, now, issueID,
		); uerr != nil {
			return 0, fmt.Errorf("refresh prevention issue: %w", uerr)
		}
		s.pingIssueUpdated(issueID)
		return issueID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("find prevention issue: %w", err)
	}

	res, err := s.db.Exec(
		`INSERT INTO issues
		 (source, status, tmdb_id, media_type, title, detail, resolution, dedupe_key, instance_id, read, created_at, updated_at)
		 VALUES (?, ?, 0, 'system', ?, ?, ?, ?, ?, 0, ?, ?)`,
		SourceSystem, IssueNeedsAdmin, title, detail, steps, key, c.instanceID, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("open prevention issue: %w", err)
	}
	issueID, err = res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read prevention issue id: %w", err)
	}
	if _, err := s.db.Exec(
		"INSERT INTO issue_messages (issue_id, author_kind, body) VALUES (?, ?, ?)",
		issueID, AuthorSystem, detail+"\n\n"+steps,
	); err != nil {
		return 0, fmt.Errorf("record prevention finding: %w", err)
	}
	s.queueIssueAlert(issueID, now)
	return issueID, nil
}

// preventionInstanceName resolves a display name, falling back to the id. Never
// a URL: an instance host must not reach a non-admin surface, and this text is
// carried into a thread.
func (s *Service) preventionInstanceName(instanceID string) string {
	if s.registry == nil {
		return instanceID
	}
	for _, svc := range []string{"sonarr", "radarr", "chaptarr"} {
		summaries, err := s.registry.ListInstanceSummaries(svc)
		if err != nil {
			continue
		}
		for _, sum := range summaries {
			if sum.ID == instanceID && strings.TrimSpace(sum.Name) != "" {
				return sum.Name
			}
		}
	}
	return instanceID
}

func preventionTitle(problemKind, instanceName string) string {
	return fmt.Sprintf("%q keeps happening on %s", problemKind, instanceName)
}

// preventionDetail states the measurement first and the reason second. The
// counts are the whole argument for the notice existing, so they lead.
func preventionDetail(c preventionCandidate, advice arr.Prevention) string {
	return fmt.Sprintf(
		"This came up %d times across %d different titles on %d separate days. %s",
		c.issueCount, c.distinctMedia, c.distinctDays, advice.Why,
	)
}

// preventionSteps renders the advice into the issue's resolution field, which is
// what the thread already shows above the detail on a needs_admin issue.
func preventionSteps(advice arr.Prevention) string {
	var sb strings.Builder
	switch advice.Scope {
	case arr.PreventionScopeClient:
		sb.WriteString("This is about your download client, not this media service — if several services share it, they are all affected.\n")
	case arr.PreventionScopeDisk:
		sb.WriteString("This is about storage on the host rather than any media service setting.\n")
	}
	sb.WriteString("Worth checking:")
	for _, step := range advice.Steps {
		sb.WriteString("\n• " + step)
	}
	sb.WriteString("\n\nCantinarr does not change these settings for you.")
	return sb.String()
}
