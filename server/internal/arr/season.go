package arr

import (
	"fmt"
	"slices"
	"time"
)

// EpisodeState is one episode of one season reduced to the facts a pre-air
// diagnosis needs. It is deliberately service-neutral and holds no arr types:
// the caller maps a sonarr.Episode plus whatever file the library holds for it
// into this shape, so the detector below stays a pure function of typed facts
// and is provable without an arr to talk to.
//
// Title and FileID are carried for the caller's benefit — one renders the
// finding, the other deletes the file — and the detector never reads them.
type EpisodeState struct {
	Number int
	Title  string
	// AirsAt is the episode's air instant in UTC (Sonarr's airDateUtc, never
	// its airDate, which is the series-local calendar date and reads a day
	// early for US prime-time shows). nil means the service carries no air date
	// for this episode at all: unscheduled, which is neither aired nor unaired.
	AirsAt  *time.Time
	HasFile bool
	FileID  int
	// ImportedAt is when the service imported the file the library currently
	// holds for this episode. nil means unknown — never "imported long ago".
	ImportedAt *time.Time
}

// SeasonTimeline is the verdict for one season: counts that let an admin judge
// it at a glance, the exact episode numbers behind each finding, and a sentence
// explaining it in words that need no knowledge of how the services work.
//
// The counts are over the records handed in — this type merges nothing, because
// deciding which of two records for the same episode wins would throw a fact
// away. The episode-number slices below are nevertheless sorted ascending with
// duplicates removed, and that is not cosmetic: callers feed those numbers
// straight into a repair action, and asking a service to delete the same
// episode twice is a bug.
type SeasonTimeline struct {
	Season int
	// Total is every episode handed in; Aired counts those whose air time has
	// passed under the air-date-only rule below; FilesTotal counts those the
	// library holds a file for. Aired can legitimately be far smaller than
	// FilesTotal — that gap IS the finding.
	Total      int
	Aired      int
	FilesTotal int

	// UnairedWithFile lists episodes that already hold a file although their
	// air time is more than PreAirMarginFloor in the future. Enough of these is
	// content that does not exist yet. An episode within the floor of airing
	// never counts: that margin is TheTVDB's runtime-stagger convention for
	// binge premieres, not evidence.
	UnairedWithFile []int
	// ImportedBeforeAir lists episodes whose file was imported more than
	// PreAirMarginFloor earlier than that same episode's air time — a file
	// cannot be an episode that had not aired when the file arrived. It is the
	// sharper evidence of the two but the scarcer one: it needs an import stamp
	// as well as an air date, and the services do not always give one.
	ImportedBeforeAir []int
	// AiredMissing lists aired episodes with no file — an ordinary library gap,
	// reported so a repair can search exactly those and leave unaired episodes
	// alone for the service to grab as they are posted.
	AiredMissing []int

	// FirstAir and LastAir bracket the episodes that have an air date at all.
	// EarliestImport is the oldest import stamp among the episodes holding a
	// file. Each is nil when the season carries nothing to compute it from.
	FirstAir, LastAir *time.Time
	EarliestImport    *time.Time

	// PreAirFill is the conclusive finding: this season holds files for content
	// that has not been released.
	PreAirFill bool
	// Transparency explains the verdict in one or two plain sentences.
	Transparency string
}

// aired reports that this episode's air time has passed.
//
// This is deliberately NOT sonarr.SeriesCompletion's definition. That helper
// counts an episode as aired when it merely HAS A FILE, which is right for the
// question it answers — a file on disk is proof enough that a viewer can watch
// something — and is exactly the bug this detector exists to catch. Borrowing
// it here would fold every impossible file straight into the aired count and
// report a perfectly healthy season. The air date, and only the air date,
// decides whether an episode exists yet.
//
// An episode with no air date at all is NEITHER aired nor unaired: it is
// unscheduled. It is excluded from every bucket rather than guessed at in
// either direction, because a missing air date is evidence of nothing.
func (e EpisodeState) Aired(now time.Time) bool {
	return e.AirsAt != nil && !e.AirsAt.After(now)
}

// Unaired reports that this episode has a known air time still in the future.
// It is its own rule rather than !Aired precisely so an unscheduled episode
// falls through both.
func (e EpisodeState) Unaired(now time.Time) bool {
	return e.AirsAt != nil && e.AirsAt.After(now)
}

// ProblemPreAirSeasonFill is the problem label for a season holding files the
// service imported before those episodes aired.
//
// Like every label in doctor.go it is persisted verbatim as issues.problem_kind
// and becomes an agent_approval_rules key, so renaming it silently orphans any
// standing rule armed on it. Only display copy may change.
const ProblemPreAirSeasonFill = "Content that has not aired yet"

// PreAirMarginFloor is how far in the future an episode's air time must be
// before a file for it counts toward the pre-air finding at all.
//
// TheTVDB models a binge premiere as a linear broadcast: a season that drops
// several episodes at once is stamped with E01 at the release instant and each
// later episode one runtime further on. Reacher S4 (2026-08-12) released three
// episodes together, TheTVDB staggered them 07:00/07:49/08:38Z, the grab
// imported at 07:27 — and the detector proposed deleting two legitimate files
// that were "unaired" for another 22 and 71 minutes (issue 858). The disease
// this detector exists for (Futurama S11) had files THIRTEEN DAYS early.
// Twelve hours separates the two cleanly: runtime-stagger artifacts total a
// few hours even for a long binge season, while a genuinely mismatched grab
// beats its air date by days. Exported because the webhook fast path applies
// the same floor before waking the witness.
const PreAirMarginFloor = 12 * time.Hour

// preAirFillThreshold is how many unaired episodes must already hold files
// before a season is called impossible.
//
// It is two, not one, on purpose. A single unaired episode with a file is an
// everyday metadata slip — a wrong air date on TheTVDB, a service that posted
// early in one region — and the file is very probably the episode it claims to
// be. Two or more is a different animal: a batch of content that has not been
// released, which no air-date error explains and which only a mismatched grab
// produces.
//
// The number is a judgment call rather than a law, and what backstops it is an
// admin approving the repair it proposes — this detector deletes nothing on its
// own. Raise it and real mismatches sit undiagnosed for longer; lower it to one
// and a single stale air date can propose destroying a file the library
// legitimately holds.
const preAirFillThreshold = 2

// BuildSeasonTimeline classifies one season from typed episode facts.
//
// It is pure: no arr client, no clock of its own. now belongs to the caller,
// and that matters — the aired set has to be resolved at EXECUTION time, never
// when a fix is proposed. Episodes air between a proposal and its approval, and
// a repair working from an aired set frozen at proposal time either searches
// for content that still does not exist or skips content that now does.
func BuildSeasonTimeline(season int, eps []EpisodeState, now time.Time) SeasonTimeline {
	t := SeasonTimeline{Season: season, Total: len(eps)}
	for _, e := range eps {
		if e.Aired(now) {
			t.Aired++
		}
		if e.HasFile {
			t.FilesTotal++
		}
		// The finding buckets apply PreAirMarginFloor; the plain Aired/Unaired
		// helpers deliberately do not — display and counts keep reporting the
		// calendar as written, only the VERDICT ignores stagger-width margins.
		if e.HasFile && e.AirsAt != nil && e.AirsAt.After(now.Add(PreAirMarginFloor)) {
			t.UnairedWithFile = append(t.UnairedWithFile, e.Number)
		}
		if !e.HasFile && e.Aired(now) {
			t.AiredMissing = append(t.AiredMissing, e.Number)
		}
		// Before by more than the floor, never "at or before": an import
		// stamped at the air instant is a service that imported the moment the
		// episode landed, and one inside the floor is a binge premiere whose
		// calendar was staggered by runtime.
		if e.HasFile && e.AirsAt != nil && e.ImportedAt != nil && e.ImportedAt.Before(e.AirsAt.Add(-PreAirMarginFloor)) {
			t.ImportedBeforeAir = append(t.ImportedBeforeAir, e.Number)
		}
		if e.AirsAt != nil {
			t.FirstAir = earlier(t.FirstAir, e.AirsAt)
			t.LastAir = later(t.LastAir, e.AirsAt)
		}
		if e.HasFile && e.ImportedAt != nil {
			t.EarliestImport = earlier(t.EarliestImport, e.ImportedAt)
		}
	}
	t.UnairedWithFile = sortedUnique(t.UnairedWithFile)
	t.ImportedBeforeAir = sortedUnique(t.ImportedBeforeAir)
	t.AiredMissing = sortedUnique(t.AiredMissing)
	t.PreAirFill = len(t.UnairedWithFile) >= preAirFillThreshold
	t.Transparency = t.transparency()
	return t
}

// transparency writes the verdict for an admin deciding whether to approve a
// repair. It may say "imported" and "aired" — both are plain English about
// their own library — but never the services' vocabulary (cutoff, monitored,
// custom format): the person on the other end of this asked for a show and got
// the wrong episodes.
func (t SeasonTimeline) transparency() string {
	switch {
	case t.Total == 0:
		return fmt.Sprintf("Season %d has no episodes.", t.Season)

	case t.PreAirFill:
		// The finding: state the counts, then the two dates that prove them. A
		// file that arrived before the season's first air date cannot hold
		// content the season had not released yet.
		// PreAirFill needs two unaired episodes holding files, so both counts in
		// this sentence are always plural.
		s := fmt.Sprintf("Season %d already has files for %d of its %d episodes, and %d of those episodes have not aired yet",
			t.Season, t.FilesTotal, t.Total, len(t.UnairedWithFile))
		switch {
		case t.EarliestImport != nil && t.FirstAir != nil:
			s += fmt.Sprintf(" — the earliest file was imported %s, before the season's first air date of %s",
				day(t.EarliestImport), day(t.FirstAir))
		case t.FirstAir != nil:
			// No import stamps came back. The finding still holds — the files
			// exist and the content does not — so drop the clause, not the
			// finding.
			s += fmt.Sprintf(" — the season's first air date is %s", day(t.FirstAir))
		}
		return s + ". Content that has not been released yet cannot be what those files hold, so they are the wrong content."

	default:
		// Nothing conclusive. An admin may well be reading this about a report
		// with nothing behind it, so state what was found and stop: never imply
		// a defect the counts do not support.
		s := fmt.Sprintf("Season %d has %s: %d aired, %d on disk.",
			t.Season, pluralEpisodes(t.Total), t.Aired, t.FilesTotal)
		switch {
		// One unaired episode holding a file is the near miss, and saying why
		// it was not flagged is more useful than either silence or alarm.
		case len(t.UnairedWithFile) == 1:
			s += fmt.Sprintf(" Episode %d already has a file although it has not aired yet — on its own that is usually an air date the service has slightly off, not a bad download.",
				t.UnairedWithFile[0])
		case len(t.AiredMissing) > 0:
			s += fmt.Sprintf(" %s aired without getting a file.", pluralEpisodes(len(t.AiredMissing)))
		}
		return s
	}
}

// earlier returns the earlier of two instants, or whichever one exists. It
// copies the value it keeps so a timeline never aliases the caller's episodes.
func earlier(a, b *time.Time) *time.Time {
	if b == nil {
		return a
	}
	if a == nil || b.Before(*a) {
		v := *b
		return &v
	}
	return a
}

// later mirrors earlier at the other end of the range.
func later(a, b *time.Time) *time.Time {
	if b == nil {
		return a
	}
	if a == nil || b.After(*a) {
		v := *b
		return &v
	}
	return a
}

// sortedUnique puts episode numbers into the canonical form a repair action
// needs: ascending, no repeats. Episodes arrive in whatever order the service
// listed them, and nothing stops a caller from merging two pages.
func sortedUnique(nums []int) []int {
	if len(nums) == 0 {
		return nil
	}
	slices.Sort(nums)
	return slices.Compact(nums)
}

// day renders an instant as a plain calendar date in UTC. The finding is about
// which side of an air date a file landed on, and a date carries that for a
// non-technical reader; the per-episode detail a caller renders alongside this
// sentence is where exact timestamps belong.
func day(t *time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// pluralEpisodes renders a count so no sentence ever reads "1 episodes".
func pluralEpisodes(n int) string {
	if n == 1 {
		return "1 episode"
	}
	return fmt.Sprintf("%d episodes", n)
}
