package remediation

// Saying "this keeps happening" is a claim, and these tests are mostly about
// the cases where it would be a lie. One five-minute download-client outage
// fans out into dozens of issues across dozens of titles inside a single
// minute; a sweep that counted issues and titles would announce a pattern about
// something that happened once. So most fixtures here produce SILENCE, and each
// one is guarding a different half of the claim.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/db"
)

const (
	// preventionTestProblem is a label the catalog HAS advice for, so a pattern
	// that meets the thresholds is expected to speak.
	preventionTestProblem = arr.ProblemNoFreeSpace
	// preventionSilentProblem is a real, reachable diagnosis label the catalog
	// deliberately withholds advice for.
	preventionSilentProblem = arr.ProblemWaitingToImport
)

// preventionBase is a fixed clock. Every cooldown in this feature is measured
// in months, so the tests drive the sweep's own `now` rather than the calendar.
var preventionBase = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

// --- driving the sweep ---

// sweepPreventionAt is SweepPreventionNotices with the clock supplied: the
// exported sweep reads time.Now(), which no test can move a year forward.
//
// A candidate-scan failure is fatal here on purpose. Production logs it and
// returns, which would turn "the roll-up broke" into "the roll-up stayed
// quiet" — and quiet is exactly what most of these tests assert.
func sweepPreventionAt(t *testing.T, svc *Service, now time.Time) {
	t.Helper()
	candidates, err := svc.preventionCandidates(now)
	if err != nil {
		t.Fatalf("preventionCandidates(%s): %v", now.Format(time.RFC3339), err)
	}
	for _, c := range candidates {
		if err := svc.considerPreventionNotice(c, now); err != nil {
			t.Fatalf("considerPreventionNotice(%s/%s): %v", c.instanceID, c.problemKind, err)
		}
	}
}

// --- fixtures ---

// seedIncident inserts one historical auto issue in the shape the pipeline
// leaves behind: closed, carrying its diagnosis label in problem_kind and the
// dedupe key that names the exact media scope it was about.
//
// Closed matters. idx_issues_open_dedupe permits only ONE open issue per key,
// so a fixture built from open rows could not express the same title failing
// twice — which is the shape the distinct-media guard exists to reject.
func seedIncident(t *testing.T, svc *Service, instanceID, source string, problemKind any, dedupeKey string, createdAt time.Time) int64 {
	t.Helper()
	res, err := svc.db.Exec(
		`INSERT INTO issues (source, status, tmdb_id, media_type, title, instance_id,
		                     problem_kind, dedupe_key, created_at, updated_at, closed_at)
		 VALUES (?, ?, 0, 'movie', 'Example', ?, ?, ?, ?, ?, ?)`,
		source, IssueResolved, instanceID, problemKind, dedupeKey, createdAt, createdAt, createdAt,
	)
	if err != nil {
		t.Fatalf("seed %s incident: %v", source, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed incident id: %v", err)
	}
	return id
}

// seedPattern lays `issues` incidents across `days` distinct calendar days and
// `media` distinct media scopes, all strictly before `end`. Each row is pinned
// to noon on its day so a fixture can never accidentally fold two days into one
// (or split one into two) because of what time the test happens to run at.
// `tag` namespaces the dedupe keys so two batches never share a scope by
// accident.
func seedPattern(t *testing.T, svc *Service, instanceID, problemKind, tag string, end time.Time, issues, media, days int) {
	t.Helper()
	if issues < days || issues < media {
		t.Fatalf("fixture asks %d incidents to cover %d days and %d media", issues, days, media)
	}
	for i := 0; i < issues; i++ {
		day := end.Add(-time.Duration(i%days+1) * 24 * time.Hour)
		at := time.Date(day.Year(), day.Month(), day.Day(), 12, i%60, i/60, 0, time.UTC)
		seedIncident(t, svc, instanceID, SourceAuto, problemKind,
			fmt.Sprintf("%s:media-%d", tag, i%media), at)
	}
}

// seedBurst is the five-minute outage: one cause, `count` incidents, every one
// on its own title, every one inside a SINGLE calendar day.
func seedBurst(t *testing.T, svc *Service, instanceID, problemKind, tag string, day time.Time, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		at := time.Date(day.Year(), day.Month(), day.Day(), 3, i%60, i/60, 0, time.UTC)
		seedIncident(t, svc, instanceID, SourceAuto, problemKind, fmt.Sprintf("%s:title-%d", tag, i), at)
	}
}

// seedPreventionAdmin creates the admin who closes a notice; issue_messages
// .author_id references users(id), so the closing audit needs a real row.
func seedPreventionAdmin(t *testing.T, svc *Service) {
	t.Helper()
	if _, err := svc.db.Exec(
		"INSERT INTO users (id, username, password_hash, role) VALUES (?, 'prevention-admin', '', 'admin')",
		testAdminID,
	); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
}

// --- reading the result ---

// noticeIssues returns the notices only, identified by the namespaced dedupe
// key rather than by source: a fixture may seed system-sourced incidents too,
// and the prefix is what actually distinguishes a notice from everything else
// in the table.
func noticeIssues(t *testing.T, svc *Service) []Issue {
	t.Helper()
	all, _, err := svc.ListIssues("", 0)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	var out []Issue
	for _, issue := range all {
		if strings.HasPrefix(issueDedupeKey(t, svc, issue.ID), preventionDedupePrefix) {
			out = append(out, issue)
		}
	}
	return out
}

func soleNoticeIssue(t *testing.T, svc *Service) Issue {
	t.Helper()
	issues := noticeIssues(t, svc)
	if len(issues) != 1 {
		t.Fatalf("prevention notices = %d, want exactly one", len(issues))
	}
	return issues[0]
}

// rawProblemKind reads the column without collapsing NULL into "": the notice's
// NULL problem_kind is the whole reason it can never be counted as an
// occurrence of the problem it describes.
func rawProblemKind(t *testing.T, svc *Service, issueID int64) sql.NullString {
	t.Helper()
	var kind sql.NullString
	if err := svc.db.QueryRow("SELECT problem_kind FROM issues WHERE id = ?", issueID).Scan(&kind); err != nil {
		t.Fatalf("read problem_kind: %v", err)
	}
	return kind
}

type preventionNoticeRow struct {
	instanceID    string
	problemKind   string
	raiseCount    int
	occurrences   int
	distinctMedia int
	distinctDays  int
	firstSeenAt   sql.NullTime
	lastSeenAt    sql.NullTime
	lastRaisedAt  sql.NullTime
	issueID       sql.NullInt64
}

func readPreventionNotices(t *testing.T, svc *Service) []preventionNoticeRow {
	t.Helper()
	rows, err := svc.db.Query(
		`SELECT instance_id, problem_kind, raise_count, occurrences, distinct_media, distinct_days,
		        first_seen_at, last_seen_at, last_raised_at, issue_id
		   FROM prevention_notices ORDER BY id`)
	if err != nil {
		t.Fatalf("read prevention notices: %v", err)
	}
	defer rows.Close()
	var out []preventionNoticeRow
	for rows.Next() {
		var n preventionNoticeRow
		if err := rows.Scan(&n.instanceID, &n.problemKind, &n.raiseCount, &n.occurrences,
			&n.distinctMedia, &n.distinctDays, &n.firstSeenAt, &n.lastSeenAt, &n.lastRaisedAt, &n.issueID); err != nil {
			t.Fatalf("scan prevention notice: %v", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate prevention notices: %v", err)
	}
	return out
}

func solePreventionNotice(t *testing.T, svc *Service) preventionNoticeRow {
	t.Helper()
	notices := readPreventionNotices(t, svc)
	if len(notices) != 1 {
		t.Fatalf("prevention_notices rows = %d, want exactly one", len(notices))
	}
	return notices[0]
}

// --- A. the threshold ---

// TestPreventionBurstIsNotRecurrence is the most important test here. Forty
// issues, forty titles, one afternoon: that is a download client dropping for
// five minutes, and it satisfies the issue-count and distinct-media guards on
// its own. Separate calendar days is the only one of the three that can tell
// recurrence from fan-out.
func TestPreventionBurstIsNotRecurrence(t *testing.T) {
	svc, notifier, _ := setupTestService(t)

	seedBurst(t, svc, testRadarrInstanceID, preventionTestProblem, "outage", preventionBase.Add(-24*time.Hour), 40)

	candidates, err := svc.preventionCandidates(preventionBase)
	if err != nil {
		t.Fatalf("preventionCandidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want none: 40 issues in one day is one event", candidates)
	}
	sweepPreventionAt(t, svc, preventionBase)
	if issues := noticeIssues(t, svc); len(issues) != 0 {
		t.Fatalf("a single burst raised %d notices: %+v", len(issues), issues)
	}
	if n := countRows(t, svc, "prevention_notices"); n != 0 {
		t.Fatalf("prevention_notices rows = %d, want 0", n)
	}
	if len(notifier.adminEvents) != 0 {
		t.Fatalf("admin events = %v, want silence", notifier.adminEvents)
	}

	// The control: the same instance and label, now genuinely returning on two
	// further days. Everything else about the fixture is unchanged, so what
	// speaks here is the day count and nothing else.
	seedIncident(t, svc, testRadarrInstanceID, SourceAuto, preventionTestProblem, "outage:later-a",
		preventionBase.Add(-48*time.Hour))
	seedIncident(t, svc, testRadarrInstanceID, SourceAuto, preventionTestProblem, "outage:later-b",
		preventionBase.Add(-72*time.Hour))
	sweepPreventionAt(t, svc, preventionBase)
	if len(noticeIssues(t, svc)) != 1 {
		t.Fatal("three separate days did not raise a notice; the fixture proves nothing")
	}
}

// TestPreventionRaisesOnceForARecurringProblem is the positive case, kept
// deliberately minimal: three incidents, two titles, three days.
func TestPreventionRaisesOnceForARecurringProblem(t *testing.T) {
	svc, _, _ := setupTestService(t)
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "recurring", preventionBase, 3, 2, 3)

	sweepPreventionAt(t, svc, preventionBase)

	issue := soleNoticeIssue(t, svc)
	notice := solePreventionNotice(t, svc)
	if notice.instanceID != testRadarrInstanceID || notice.problemKind != preventionTestProblem {
		t.Fatalf("notice scope = %s/%s", notice.instanceID, notice.problemKind)
	}
	if !notice.issueID.Valid || notice.issueID.Int64 != issue.ID {
		t.Fatalf("notice issue_id = %+v, want the raised issue %d", notice.issueID, issue.ID)
	}
}

// TestPreventionThresholdsFailIndependently walks each guard's near miss. Two
// incidents cannot span three days, so that case is the honest "not enough
// incidents" shape; the other two isolate exactly one guard each.
func TestPreventionThresholdsFailIndependently(t *testing.T) {
	for _, tc := range []struct {
		name                string
		issues, media, days int
		why                 string
	}{
		{name: "too few incidents", issues: 2, media: 2, days: 2,
			why: "two repairs is not yet a pattern"},
		{name: "too few days", issues: 3, media: 2, days: 2,
			why: "three incidents over two days is still close to one event"},
		{name: "one title flapping", issues: 3, media: 1, days: 3,
			why: "one title failing all week is that title's problem"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := setupTestService(t)
			seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "near-miss",
				preventionBase, tc.issues, tc.media, tc.days)

			sweepPreventionAt(t, svc, preventionBase)

			if issues := noticeIssues(t, svc); len(issues) != 0 {
				t.Fatalf("%s raised a notice (%s): %+v", tc.name, tc.why, issues)
			}
			if n := countRows(t, svc, "prevention_notices"); n != 0 {
				t.Fatalf("prevention_notices rows = %d, want 0", n)
			}
		})
	}
}

// TestIssueCreatedAtHoldsTwoTimestampEncodings proves the premise the day count
// rests on. issues.created_at genuinely holds two formats, and date() silently
// returns NULL for one of them — which would collapse the distinct-day count to
// 1 and make the day threshold meaningless.
func TestIssueCreatedAtHoldsTwoTimestampEncodings(t *testing.T) {
	svc, _, _ := setupTestService(t)

	bound := time.Date(2026, 3, 10, 12, 0, 0, 123456789, time.UTC)
	boundID := seedIncident(t, svc, testRadarrInstanceID, SourceAuto, preventionTestProblem, "encoding:bound", bound)
	// A row that omits the column takes SQLite's own CURRENT_TIMESTAMP.
	res, err := svc.db.Exec(
		`INSERT INTO issues (source, status, tmdb_id, media_type, title, instance_id, problem_kind, dedupe_key)
		 VALUES (?, ?, 0, 'movie', 'Example', ?, ?, 'encoding:default')`,
		SourceAuto, IssueResolved, testRadarrInstanceID, preventionTestProblem,
	)
	if err != nil {
		t.Fatalf("seed CURRENT_TIMESTAMP incident: %v", err)
	}
	defaultID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed CURRENT_TIMESTAMP id: %v", err)
	}

	read := func(id int64) (raw string, date sql.NullString, prefix string) {
		t.Helper()
		// CAST(...) so the driver hands back the stored text instead of parsing
		// the DATETIME column into a time.Time.
		if err := svc.db.QueryRow(
			"SELECT CAST(created_at AS TEXT), date(created_at), substr(created_at, 1, 10) FROM issues WHERE id = ?", id,
		).Scan(&raw, &date, &prefix); err != nil {
			t.Fatalf("read created_at of %d: %v", id, err)
		}
		return raw, date, prefix
	}

	raw, date, prefix := read(boundID)
	if raw != "2026-03-10 12:00:00.123456789 +0000 UTC" {
		t.Fatalf("bound time.Time stored as %q, want Go's own String() form", raw)
	}
	if date.Valid {
		t.Fatalf("date(%q) = %q, want NULL — this is the trap substr() exists for", raw, date.String)
	}
	if prefix != "2026-03-10" {
		t.Fatalf("substr(%q, 1, 10) = %q", raw, prefix)
	}

	raw, date, prefix = read(defaultID)
	if len(raw) != len("2006-01-02 15:04:05") || strings.Contains(raw, "UTC") {
		t.Fatalf("CURRENT_TIMESTAMP stored as %q, want SQLite's plain form", raw)
	}
	if !date.Valid || date.String != prefix || prefix != raw[:10] {
		t.Fatalf("date(%q) = %+v, substr = %q — both should be the calendar date", raw, date, prefix)
	}
}

// TestPreventionCountsDaysAcrossBothTimestampEncodings is the same trap seen
// from the feature's side: a pattern whose three days are written in two
// different formats must still count as three days.
func TestPreventionCountsDaysAcrossBothTimestampEncodings(t *testing.T) {
	svc, _, _ := setupTestService(t)
	now := time.Now().UTC()

	// Two days the way Go writes a bound time.Time...
	seedIncident(t, svc, testRadarrInstanceID, SourceAuto, preventionTestProblem, "mixed:media-0", now.Add(-48*time.Hour))
	seedIncident(t, svc, testRadarrInstanceID, SourceAuto, preventionTestProblem, "mixed:media-1", now.Add(-24*time.Hour))
	// ...and today the way SQLite writes it when the caller omits the column.
	if _, err := svc.db.Exec(
		`INSERT INTO issues (source, status, tmdb_id, media_type, title, instance_id, problem_kind, dedupe_key)
		 VALUES (?, ?, 0, 'movie', 'Example', ?, ?, 'mixed:media-2')`,
		SourceAuto, IssueResolved, testRadarrInstanceID, preventionTestProblem,
	); err != nil {
		t.Fatalf("seed CURRENT_TIMESTAMP incident: %v", err)
	}

	sweepPreventionAt(t, svc, time.Now().UTC())

	issue := soleNoticeIssue(t, svc)
	if !strings.Contains(issue.Detail, "on 3 separate days") {
		t.Fatalf("detail = %q, want three days counted across both encodings", issue.Detail)
	}
	if notice := solePreventionNotice(t, svc); notice.distinctDays != 3 {
		t.Fatalf("recorded distinct_days = %d, want 3", notice.distinctDays)
	}
}

// --- B. what never counts ---

// TestPreventionCountsOnlyAutoIssues. A person reporting the same complaint
// three times is not evidence about an *arr setting, and a system notice is
// Cantinarr's own voice.
func TestPreventionCountsOnlyAutoIssues(t *testing.T) {
	svc, _, _ := setupTestService(t)
	day := func(n int) time.Time { return preventionBase.Add(-time.Duration(n) * 24 * time.Hour) }

	seedIncident(t, svc, testRadarrInstanceID, SourceAuto, preventionTestProblem, "auto:title-a", day(1))
	seedIncident(t, svc, testRadarrInstanceID, SourceAuto, preventionTestProblem, "auto:title-b", day(2))
	// Deliberately adversarial: production never labels a user report, so these
	// carry the label anyway to prove `source` alone excludes them.
	seedIncident(t, svc, testRadarrInstanceID, SourceUser, preventionTestProblem, "user:title-c", day(3))
	seedIncident(t, svc, testRadarrInstanceID, SourceSystem, preventionTestProblem, "system:title-d", day(3))

	sweepPreventionAt(t, svc, preventionBase)
	if issues := noticeIssues(t, svc); len(issues) != 0 {
		t.Fatalf("user/system rows completed a pattern: %+v", issues)
	}

	// One genuine incident on the third day, and the same fixture speaks.
	seedIncident(t, svc, testRadarrInstanceID, SourceAuto, preventionTestProblem, "auto:title-c", day(3))
	sweepPreventionAt(t, svc, preventionBase)

	issue := soleNoticeIssue(t, svc)
	if !strings.Contains(issue.Detail, "This came up 3 times across 3 different titles on 3 separate days.") {
		t.Fatalf("detail = %q, want only the three auto incidents counted", issue.Detail)
	}
}

// TestPreventionIgnoresUnlabelledIssues. An issue with no diagnosis label has
// nothing to advise on, and a NULL label reaching the roll-up would break the
// scan for every OTHER pattern in the same pass — the sweep logs and returns,
// so a real notice elsewhere would silently never be raised.
func TestPreventionIgnoresUnlabelledIssues(t *testing.T) {
	svc, _, _ := setupTestService(t)
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "genuine", preventionBase, 3, 2, 3)
	for i := 0; i < 3; i++ {
		day := preventionBase.Add(-time.Duration(i+1) * 24 * time.Hour)
		at := time.Date(day.Year(), day.Month(), day.Day(), 9, i, 0, 0, time.UTC)
		// NULL on one instance, empty string on another: both are "we never
		// diagnosed this", and neither can key advice.
		seedIncident(t, svc, testRadarrInstanceID2, SourceAuto, nil, fmt.Sprintf("null:media-%d", i%2), at)
		seedIncident(t, svc, testSonarrInstanceID, SourceAuto, "", fmt.Sprintf("blank:media-%d", i%2), at)
	}

	sweepPreventionAt(t, svc, preventionBase)

	notice := solePreventionNotice(t, svc)
	if notice.instanceID != testRadarrInstanceID || notice.problemKind != preventionTestProblem {
		t.Fatalf("notice = %+v, want only the labelled pattern", notice)
	}
	if notice.occurrences != 3 || notice.distinctMedia != 2 || notice.distinctDays != 3 {
		t.Fatalf("counts = %d/%d/%d, want the labelled incidents only", notice.occurrences, notice.distinctMedia, notice.distinctDays)
	}
	if issue := soleNoticeIssue(t, svc); issue.InstanceID != testRadarrInstanceID {
		t.Fatalf("notice raised on %q", issue.InstanceID)
	}
}

// TestPreventionIgnoresIssuesOfADeletedInstance. Deleting an instance does not
// clean up its issues, so without the existence check Cantinarr would advise an
// admin about the settings of an *arr that was removed last week.
func TestPreventionIgnoresIssuesOfADeletedInstance(t *testing.T) {
	svc, _, _ := setupTestService(t)
	seedPattern(t, svc, testRadarrInstanceID2, preventionTestProblem, "orphan", preventionBase, 6, 3, 3)
	if _, err := svc.db.Exec("DELETE FROM service_instances WHERE id = ?", testRadarrInstanceID2); err != nil {
		t.Fatalf("delete instance: %v", err)
	}
	// A live instance with an identical pattern, so the test cannot pass by the
	// sweep simply doing nothing.
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "live", preventionBase, 6, 3, 3)

	sweepPreventionAt(t, svc, preventionBase)

	notice := solePreventionNotice(t, svc)
	if notice.instanceID != testRadarrInstanceID {
		t.Fatalf("notice raised for %q, want only the instance that still exists", notice.instanceID)
	}
	issue := soleNoticeIssue(t, svc)
	if issue.InstanceID != testRadarrInstanceID {
		t.Fatalf("issue instance = %q", issue.InstanceID)
	}
}

// TestPreventionNoticeNeverCountsItself. The notice is an issue about a
// problem, on the same instance, and it must never become evidence for itself —
// which is why it is written with a NULL problem_kind.
func TestPreventionNoticeNeverCountsItself(t *testing.T) {
	svc, _, _ := setupTestService(t)
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "self", preventionBase, 3, 2, 3)

	sweepPreventionAt(t, svc, preventionBase)
	issue := soleNoticeIssue(t, svc)
	if kind := rawProblemKind(t, svc, issue.ID); kind.Valid {
		t.Fatalf("notice problem_kind = %q, want NULL so it can never count as an occurrence", kind.String)
	}

	// Re-measure with the notice sitting in the same table: the counts must not
	// have moved.
	candidates, err := svc.preventionCandidates(preventionBase)
	if err != nil {
		t.Fatalf("preventionCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want the one original pattern", candidates)
	}
	if c := candidates[0]; c.issueCount != 3 || c.distinctMedia != 2 || c.distinctDays != 3 {
		t.Fatalf("re-measured counts = %d/%d/%d, want the original 3/2/3", c.issueCount, c.distinctMedia, c.distinctDays)
	}

	sweepPreventionAt(t, svc, preventionBase)
	notice := solePreventionNotice(t, svc)
	if notice.occurrences != 3 || notice.distinctMedia != 2 || notice.distinctDays != 3 {
		t.Fatalf("recorded counts after a second sweep = %d/%d/%d, want 3/2/3",
			notice.occurrences, notice.distinctMedia, notice.distinctDays)
	}
}

// --- C. the catalog gate ---

// TestPreventionStaysSilentWithoutCatalogAdvice. "Waiting to import" is an
// ordinary state with no honest preventative answer. A notice that says this
// keeps happening and then has nothing to suggest is worse than never speaking,
// however strong the pattern is.
func TestPreventionStaysSilentWithoutCatalogAdvice(t *testing.T) {
	svc, notifier, _ := setupTestService(t)
	if _, ok := arr.PreventionFor(preventionSilentProblem); ok {
		t.Fatalf("%q has catalog advice; this test needs a label that does not", preventionSilentProblem)
	}
	seedPattern(t, svc, testRadarrInstanceID, preventionSilentProblem, "unadvisable", preventionBase, 30, 10, 10)

	candidates, err := svc.preventionCandidates(preventionBase)
	if err != nil {
		t.Fatalf("preventionCandidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].issueCount != 30 {
		t.Fatalf("candidates = %+v, want the pattern to be measured but unspoken", candidates)
	}

	sweepPreventionAt(t, svc, preventionBase)

	if issues := noticeIssues(t, svc); len(issues) != 0 {
		t.Fatalf("advice-less label raised %+v", issues)
	}
	if n := countRows(t, svc, "prevention_notices"); n != 0 {
		t.Fatalf("prevention_notices rows = %d, want 0 — nothing was said, so nothing is remembered", n)
	}
	if len(notifier.adminEvents) != 0 || countRows(t, svc, "issue_alert_queue") != 0 {
		t.Fatalf("silence still paged: %v", notifier.adminEvents)
	}
}

// --- D. the notice and its issue ---

// TestPreventionNoticeShape pins every field an admin, the thread screen and
// the alert queue each depend on.
func TestPreventionNoticeShape(t *testing.T) {
	svc, notifier, _ := setupTestService(t)
	advice, ok := arr.PreventionFor(preventionTestProblem)
	if !ok {
		t.Fatalf("no catalog advice for %q", preventionTestProblem)
	}
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "shape", preventionBase, 3, 2, 3)

	sweepPreventionAt(t, svc, preventionBase)

	issue := soleNoticeIssue(t, svc)
	if issue.Status != IssueNeedsAdmin || issue.MediaType != "system" || issue.Read {
		t.Fatalf("issue = %+v, want an unread needs_admin system row", issue)
	}
	if issue.InstanceID != testRadarrInstanceID {
		t.Fatalf("issue instance = %q, want the instance the pattern is about", issue.InstanceID)
	}
	if kind := rawProblemKind(t, svc, issue.ID); kind.Valid {
		t.Fatalf("problem_kind = %q, want NULL", kind.String)
	}
	key := issueDedupeKey(t, svc, issue.ID)
	if !strings.HasPrefix(key, preventionDedupePrefix) || key != preventionScopeKey(testRadarrInstanceID, preventionTestProblem) {
		t.Fatalf("dedupe_key = %q, want the namespaced scope key", key)
	}
	// The instance's NAME, never its URL: this text is carried into a thread.
	if want := fmt.Sprintf("%q keeps happening on Movies", preventionTestProblem); issue.Title != want {
		t.Fatalf("title = %q, want %q", issue.Title, want)
	}
	if strings.Contains(issue.Title+issue.Detail+issue.Resolution, "radarr.test") {
		t.Fatalf("an instance host reached the notice: %q / %q", issue.Title, issue.Detail)
	}
	// The measurement leads, because it is the whole argument for the notice.
	if !strings.HasPrefix(issue.Detail, "This came up 3 times across 2 different titles on 3 separate days.") {
		t.Fatalf("detail = %q, want the counts first", issue.Detail)
	}
	if !strings.Contains(issue.Detail, advice.Why) {
		t.Fatalf("detail = %q, want the catalog's reason", issue.Detail)
	}
	for _, step := range advice.Steps {
		if !strings.Contains(issue.Resolution, step) {
			t.Fatalf("resolution = %q, missing step %q", issue.Resolution, step)
		}
	}
	if !strings.Contains(issue.Resolution, "This is about storage on the host rather than any media service setting.") {
		t.Fatalf("resolution = %q, want the disk-scope preamble", issue.Resolution)
	}
	if !strings.Contains(issue.Resolution, "Cantinarr does not change these settings for you.") {
		t.Fatalf("resolution = %q, want the advice-not-action disclaimer", issue.Resolution)
	}

	thread, err := svc.IssueThread(issue.ID)
	if err != nil {
		t.Fatalf("IssueThread: %v", err)
	}
	if len(thread) != 1 || thread[0].AuthorKind != AuthorSystem {
		t.Fatalf("thread = %+v, want one system message", thread)
	}
	if thread[0].Body != issue.Detail+"\n\n"+issue.Resolution {
		t.Fatalf("thread body = %q, want the finding and its steps", thread[0].Body)
	}

	notice := solePreventionNotice(t, svc)
	if notice.raiseCount != 1 || notice.occurrences != 3 || notice.distinctMedia != 2 || notice.distinctDays != 3 {
		t.Fatalf("notice = %+v, want raise 1 and the measured 3/2/3", notice)
	}
	if !notice.lastRaisedAt.Valid || !notice.lastRaisedAt.Time.Equal(preventionBase) {
		t.Fatalf("last_raised_at = %+v, want the sweep's clock", notice.lastRaisedAt)
	}
	if !notice.firstSeenAt.Valid || !notice.lastSeenAt.Valid || !notice.firstSeenAt.Time.Before(notice.lastSeenAt.Time) {
		t.Fatalf("window = %+v .. %+v, want the incident span", notice.firstSeenAt, notice.lastSeenAt)
	}

	// None of this is urgent, and an issue closed inside the hold-down should
	// never have paged at all — so the alert is QUEUED, not sent.
	if len(notifier.adminEvents) != 0 {
		t.Fatalf("admin events = %v, want the alert held", notifier.adminEvents)
	}
	if n := countRows(t, svc, "issue_alert_queue"); n != 1 {
		t.Fatalf("queued alerts = %d, want 1", n)
	}
	deliverIssueAlerts(svc, preventionBase)
	if len(notifier.adminEvents) != 1 || notifier.adminEvents[0] != "issue_created" {
		t.Fatalf("admin events after the hold-down = %v", notifier.adminEvents)
	}
}

// --- E. re-raising ---

// TestPreventionDoesNotSpeakTwiceWhileTheNoticeIsOpen. The last notice is still
// in front of the admin; saying it again is a second copy of the same row, and
// no cooldown or fresh recurrence changes that.
func TestPreventionDoesNotSpeakTwiceWhileTheNoticeIsOpen(t *testing.T) {
	svc, _, _ := setupTestService(t)
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "first", preventionBase, 3, 2, 3)
	sweepPreventionAt(t, svc, preventionBase)
	first := soleNoticeIssue(t, svc)

	// Two months later: the longest cooldown for a resolved notice has lapsed
	// and the pattern has fully re-formed. The only thing holding it back is
	// that nobody has closed the issue.
	later := preventionBase.Add(70 * 24 * time.Hour)
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "again", later, 3, 2, 3)
	sweepPreventionAt(t, svc, later)

	issue := soleNoticeIssue(t, svc)
	if issue.ID != first.ID {
		t.Fatalf("issue id = %d, want the original %d", issue.ID, first.ID)
	}
	notice := solePreventionNotice(t, svc)
	if notice.raiseCount != 1 {
		t.Fatalf("raise_count = %d, want 1 while the notice is still open", notice.raiseCount)
	}
	// Nothing NEW is said, but the counts are the argument for the notice, so
	// an admin who leaves it open for two months must not still be reading the
	// three occurrences it was raised on.
	if !strings.HasPrefix(issue.Detail, "This came up 6 times") {
		t.Fatalf("detail = %q, want the counts brought up to date", issue.Detail)
	}
	if notice.occurrences != 6 || notice.distinctDays != 6 {
		t.Fatalf("recorded counts = %d/%d, want the refreshed measurement", notice.occurrences, notice.distinctDays)
	}
	// Refreshing must not re-flag it. Marking it unread again every hour is
	// precisely the nag this design avoids, and occurrences counts TELLINGS.
	if issue.Occurrences != 1 {
		t.Fatalf("occurrences = %d, want one telling", issue.Occurrences)
	}
	// The issue opens unread; once an admin has read it, an hourly refresh must
	// leave it read. Re-flagging it every hour is exactly the nag this design
	// exists to avoid.
	if _, err := svc.db.Exec("UPDATE issues SET read = 1 WHERE id = ?", issue.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	sweepPreventionAt(t, svc, later)
	if again := soleNoticeIssue(t, svc); !again.Read {
		t.Fatal("a refresh marked the notice unread again")
	}
	if n := countRows(t, svc, "issue_alert_queue"); n != 1 {
		t.Fatalf("queued alerts = %d, want the one original", n)
	}
}

// TestPreventionRequiresBothCooldownAndFreshRecurrence. Two independent
// conditions gate a re-raise, and each half is asserted alone: a cause that
// actually stopped never comes back, whatever the cooldown says, and a cause
// still running does not earn a second telling before the cooldown lapses.
func TestPreventionRequiresBothCooldownAndFreshRecurrence(t *testing.T) {
	svc, _, _ := setupTestService(t)
	seedPreventionAdmin(t, svc)
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "first", preventionBase, 3, 2, 3)
	sweepPreventionAt(t, svc, preventionBase)
	first := soleNoticeIssue(t, svc)
	if _, err := svc.ResolveIssueByAdmin(context.Background(), testAdminID, first.ID,
		AdminDispositionResolved, "Freed 900GB and raised the minimum free space."); err != nil {
		t.Fatalf("ResolveIssueByAdmin: %v", err)
	}

	// Cooldown served, nothing new since we spoke. The original incidents are
	// still inside the 90-day window, which is exactly the trap: a sliding
	// window alone would re-raise forever on evidence the admin already saw.
	served := preventionBase.Add(preventionCooldownResolved + 24*time.Hour)
	sweepPreventionAt(t, svc, served)
	if issues := noticeIssues(t, svc); len(issues) != 1 {
		t.Fatalf("re-raised on stale evidence: %+v", issues)
	}
	if notice := solePreventionNotice(t, svc); notice.raiseCount != 1 {
		t.Fatalf("raise_count = %d after a stale sweep", notice.raiseCount)
	}

	// The pattern re-forms, but the cooldown has not lapsed.
	early := preventionBase.Add(30 * 24 * time.Hour)
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "fresh", early, 3, 2, 3)
	sweepPreventionAt(t, svc, early)
	if issues := noticeIssues(t, svc); len(issues) != 1 {
		t.Fatalf("re-raised inside the cooldown: %+v", issues)
	}

	// Both conditions met.
	sweepPreventionAt(t, svc, served)
	issues := noticeIssues(t, svc)
	if len(issues) != 2 {
		t.Fatalf("system issues = %d, want a second telling once both halves are met", len(issues))
	}
	notice := solePreventionNotice(t, svc)
	if notice.raiseCount != 2 {
		t.Fatalf("raise_count = %d, want 2", notice.raiseCount)
	}
	if !notice.issueID.Valid || notice.issueID.Int64 == first.ID {
		t.Fatalf("notice issue_id = %+v, want the new issue", notice.issueID)
	}
}

// TestPreventionCooldownFollowsTheAdminsVerb. The thread screen already offers
// three closures, and "Close without fix" is an administrator saying they have
// decided not to fix this — so it is the mute, and needs no new control
// anywhere. Past the raise cap every verb becomes the longest wait.
func TestPreventionCooldownFollowsTheAdminsVerb(t *testing.T) {
	resolve := func(disposition AdminIssueDisposition, note string) func(*testing.T, *Service, int64) {
		return func(t *testing.T, svc *Service, issueID int64) {
			t.Helper()
			if _, err := svc.ResolveIssueByAdmin(context.Background(), testAdminID, issueID, disposition, note); err != nil {
				t.Fatalf("ResolveIssueByAdmin(%s): %v", disposition, err)
			}
		}
	}
	for _, tc := range []struct {
		name       string
		close      func(*testing.T, *Service, int64)
		raiseCount int
		cooldown   time.Duration
	}{
		{
			name:     "resolved",
			close:    resolve(AdminDispositionResolved, "Freed 900GB on the array."),
			cooldown: preventionCooldownResolved,
		},
		{
			name: "dismissed",
			close: func(t *testing.T, svc *Service, id int64) {
				t.Helper()
				if err := svc.DismissIssue(id); err != nil {
					t.Fatalf("DismissIssue: %v", err)
				}
			},
			cooldown: preventionCooldownDismissed,
		},
		{
			name:     "closed without a fix",
			close:    resolve(AdminDispositionWontFix, "The array is being replaced next quarter."),
			cooldown: preventionCooldownWontFix,
		},
		{
			// Three tellings, written as a literal rather than as
			// preventionRaiseCap: a fixture that reads the constant it is
			// pinning moves with it and proves nothing. A cause nobody has
			// fixed after this many tellings will not be fixed by telling them
			// more often, so the admin's "resolved" stops earning its 60 days.
			name:       "already told three times",
			close:      resolve(AdminDispositionResolved, "Freed 900GB on the array."),
			raiseCount: 3,
			cooldown:   preventionCooldownWontFix,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := setupTestService(t)
			seedPreventionAdmin(t, svc)
			seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "first", preventionBase, 3, 2, 3)
			sweepPreventionAt(t, svc, preventionBase)
			issue := soleNoticeIssue(t, svc)
			tc.close(t, svc, issue.ID)

			wantRaises := 1
			if tc.raiseCount > 0 {
				if _, err := svc.db.Exec("UPDATE prevention_notices SET raise_count = ?", tc.raiseCount); err != nil {
					t.Fatalf("set raise_count: %v", err)
				}
				wantRaises = tc.raiseCount
			}

			// Two days short of the cooldown, with the pattern fully re-formed.
			early := preventionBase.Add(tc.cooldown - 2*24*time.Hour)
			seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "early", early, 3, 2, 3)
			sweepPreventionAt(t, svc, early)
			if issues := noticeIssues(t, svc); len(issues) != 1 {
				t.Fatalf("spoke two days before the %s cooldown lapsed: %+v", tc.cooldown, issues)
			}
			if notice := solePreventionNotice(t, svc); notice.raiseCount != wantRaises {
				t.Fatalf("raise_count = %d before the cooldown lapsed, want %d", notice.raiseCount, wantRaises)
			}

			// Two days past it, the same pattern earns a second telling.
			late := preventionBase.Add(tc.cooldown + 2*24*time.Hour)
			seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "late", late, 3, 2, 3)
			sweepPreventionAt(t, svc, late)
			if issues := noticeIssues(t, svc); len(issues) != 2 {
				t.Fatalf("system issues = %d after the cooldown lapsed, want 2", len(issues))
			}
			if notice := solePreventionNotice(t, svc); notice.raiseCount != wantRaises+1 {
				t.Fatalf("raise_count = %d, want %d", notice.raiseCount, wantRaises+1)
			}
		})
	}
}

// TestPreventionSurvivesADeletedNoticeIssue. issue_id is ON DELETE SET NULL, so
// an admin purge leaves the memory of what was said with nothing to point at.
// Nothing is in front of the admin any more, so that counts as closed.
func TestPreventionSurvivesADeletedNoticeIssue(t *testing.T) {
	svc, _, _ := setupTestService(t)
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "first", preventionBase, 3, 2, 3)
	sweepPreventionAt(t, svc, preventionBase)
	issue := soleNoticeIssue(t, svc)

	if _, err := svc.db.Exec("DELETE FROM issues WHERE id = ?", issue.ID); err != nil {
		t.Fatalf("delete notice issue: %v", err)
	}
	notice := solePreventionNotice(t, svc)
	if notice.issueID.Valid {
		t.Fatalf("issue_id = %+v after the row was deleted, want NULL", notice.issueID)
	}
	if notice.raiseCount != 1 {
		t.Fatalf("deleting the issue took the memory with it: %+v", notice)
	}

	// The default cooldown applies to a disposition nobody can read any more.
	late := preventionBase.Add(preventionCooldownResolved + 2*24*time.Hour)
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "late", late, 3, 2, 3)
	sweepPreventionAt(t, svc, late)

	if issues := noticeIssues(t, svc); len(issues) != 1 {
		t.Fatalf("system issues = %d, want one fresh notice", len(issues))
	}
	notice = solePreventionNotice(t, svc)
	if notice.raiseCount != 2 || !notice.issueID.Valid {
		t.Fatalf("notice after re-raise = %+v", notice)
	}
}

// --- F. robustness ---

// TestPreventionSweepIsIdempotent runs the exported sweep, on the real clock,
// twice in a row — which is what a restart or a doubled tick looks like.
func TestPreventionSweepIsIdempotent(t *testing.T) {
	svc, notifier, _ := setupTestService(t)
	seedPattern(t, svc, testRadarrInstanceID, preventionTestProblem, "live", time.Now().UTC(), 3, 2, 3)

	svc.SweepPreventionNotices()
	svc.SweepPreventionNotices()

	issue := soleNoticeIssue(t, svc)
	notice := solePreventionNotice(t, svc)
	if notice.raiseCount != 1 || notice.occurrences != 3 {
		t.Fatalf("notice = %+v, want one raise", notice)
	}
	if issue.Occurrences != 1 {
		t.Fatalf("occurrences = %d, want one telling from two passes", issue.Occurrences)
	}
	if n := countRows(t, svc, "issue_alert_queue"); n != 1 {
		t.Fatalf("queued alerts = %d, want 1", n)
	}
	// The second pass refreshes the open notice's counts, which pings the
	// socket. What must NOT happen is a second alert: the push is still held in
	// the queue above, and issue_updated is a WS refresh, never a page.
	for _, ev := range notifier.adminEvents {
		if ev != "issue_updated" {
			t.Fatalf("admin events = %v, want no alert beyond a socket refresh", notifier.adminEvents)
		}
	}
}

// TestPreventionNamesTheInstanceWithoutARegistry. The name is display sugar; a
// service with no registry still has to produce a notice, and it falls back to
// the instance id rather than inventing one or leaking a URL.
func TestPreventionNamesTheInstanceWithoutARegistry(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	const headless = "radarr-headless"
	if _, err := database.Exec(
		`INSERT INTO service_instances (id, service_type, name, url, api_key)
		 VALUES (?, 'radarr', 'Movies', 'http://radarr.test', 'key')`, headless,
	); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	svc := NewService(database, nil, nil, &fakeNotifier{})
	seedPattern(t, svc, headless, preventionTestProblem, "headless", preventionBase, 3, 2, 3)

	sweepPreventionAt(t, svc, preventionBase)

	issue := soleNoticeIssue(t, svc)
	if want := fmt.Sprintf("%q keeps happening on %s", preventionTestProblem, headless); issue.Title != want {
		t.Fatalf("title = %q, want %q", issue.Title, want)
	}
}

// A recurrence notice quotes the LIVE value of the setting it names, and the
// admin changing that setting is what resolves it — advice that notices being
// taken. Unchanged values never resolve; the change is the signal.
func TestPreventionNoticeQuotesLiveValuesAndSelfResolves(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	fake.setIndexers([]map[string]any{{
		"name": "NZBgeek", "protocol": "torrent", "enableRss": true, "priority": 25,
		"fields": []map[string]any{
			{"name": "apiKey", "value": "SECRET-XYZ"},
			{"name": "minimumSeeders", "value": 0},
		},
	}})

	advice, ok := arr.PreventionFor(arr.ProblemDownloadStalled)
	if !ok {
		t.Fatalf("catalog lost its stalled entry")
	}
	now := time.Now().UTC()
	issueID, err := svc.openPreventionIssue(preventionCandidate{
		instanceID: preAirSonarrID, problemKind: arr.ProblemDownloadStalled,
		issueCount: 4, distinctMedia: 3, distinctDays: 4,
	}, advice, "TV", now)
	if err != nil {
		t.Fatalf("open prevention issue: %v", err)
	}
	var detail string
	if err := svc.db.QueryRow("SELECT detail FROM issues WHERE id = ?", issueID).Scan(&detail); err != nil {
		t.Fatalf("read detail: %v", err)
	}
	if !strings.Contains(detail, "min seeders 0") || !strings.Contains(detail, "NZBgeek") {
		t.Fatalf("notice does not quote the live value:\n%s", detail)
	}
	if strings.Contains(detail, "SECRET-XYZ") {
		t.Fatalf("notice leaked a secret:\n%s", detail)
	}

	// Same values: the sweep must NOT resolve.
	svc.sweepPreventionLiveChanges(time.Now().UTC())
	var closed bool
	_ = svc.db.QueryRow("SELECT closed_at IS NOT NULL FROM issues WHERE id = ?", issueID).Scan(&closed)
	if closed {
		t.Fatalf("notice resolved with nothing changed")
	}

	// The admin raises min seeders: the named setting changed, the notice ends.
	fake.setIndexers([]map[string]any{{
		"name": "NZBgeek", "protocol": "torrent", "enableRss": true, "priority": 25,
		"fields": []map[string]any{
			{"name": "apiKey", "value": "SECRET-XYZ"},
			{"name": "minimumSeeders", "value": 25},
		},
	}})
	svc.sweepPreventionLiveChanges(time.Now().UTC())
	var kind string
	_ = svc.db.QueryRow("SELECT COALESCE(resolution_kind,''), closed_at IS NOT NULL FROM issues WHERE id = ?", issueID).Scan(&kind, &closed)
	if !closed || kind != ResolutionPreventionSettingChanged {
		t.Fatalf("after the setting changed: closed %v kind %q, want auto-resolved %q", closed, kind, ResolutionPreventionSettingChanged)
	}
}

// The wire flag that makes prevention recognizably first-class client-side.
func TestIsPreventionFlagOnWire(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	fake.setIndexers(nil)
	advice, _ := arr.PreventionFor(arr.ProblemDownloadStalled)
	noticeID, err := svc.openPreventionIssue(preventionCandidate{
		instanceID: preAirSonarrID, problemKind: arr.ProblemDownloadStalled,
		issueCount: 3, distinctMedia: 2, distinctDays: 3,
	}, advice, "TV", time.Now().UTC())
	if err != nil {
		t.Fatalf("open notice: %v", err)
	}
	notice, err := svc.GetIssue(noticeID)
	if err != nil {
		t.Fatalf("load notice: %v", err)
	}
	if !notice.IsPrevention {
		t.Fatalf("prevention notice not flagged on the wire")
	}
	res, _ := svc.db.Exec(`INSERT INTO issues (source, status, media_type, tmdb_id, title, detail) VALUES ('auto','open','movie',1,'M','d')`)
	plainID, _ := res.LastInsertId()
	plain, err := svc.GetIssue(plainID)
	if err != nil {
		t.Fatalf("load plain issue: %v", err)
	}
	if plain.IsPrevention {
		t.Fatalf("ordinary issue flagged as prevention")
	}
}
