package arr

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// ts parses a fixture timestamp. A bad literal is a bug in the test itself.
func ts(s string) *time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic("bad fixture time " + s + ": " + err.Error())
	}
	return &v
}

// tp is the shorthand for pointing at a computed instant.
func tp(v time.Time) *time.Time { return &v }

// futuramaS11 rebuilds the live season this detector was written for: Sonarr
// series 28 season 11, ten episodes airing weekly from 2026-08-04 with E1 and
// E2 as a double premiere. Nine of them (E1-E9) already held files on
// 2026-07-21, thirteen days before the first one aired; E10 has no file.
func futuramaS11() []EpisodeState {
	airs := []string{
		"2026-08-04T00:00:00Z", // E1 ┐ double premiere
		"2026-08-04T00:00:00Z", // E2 ┘
		"2026-08-11T00:00:00Z",
		"2026-08-18T00:00:00Z",
		"2026-08-25T00:00:00Z",
		"2026-09-01T00:00:00Z",
		"2026-09-08T00:00:00Z",
		"2026-09-15T00:00:00Z",
		"2026-09-22T00:00:00Z",
		"2026-09-29T00:00:00Z",
	}
	eps := make([]EpisodeState, 0, len(airs))
	for i, a := range airs {
		e := EpisodeState{Number: i + 1, Title: fmt.Sprintf("Episode %d", i+1), AirsAt: ts(a)}
		if e.Number <= 9 {
			e.HasFile = true
			e.FileID = 6000 + e.Number
			e.ImportedAt = ts("2026-07-21T09:30:00Z")
		}
		eps = append(eps, e)
	}
	return eps
}

func TestBuildSeasonTimeline(t *testing.T) {
	cases := []struct {
		name          string
		season        int
		eps           []EpisodeState
		now           string
		wantTotal     int
		wantAired     int
		wantFiles     int
		wantUnaired   []int
		wantEarly     []int
		wantMissing   []int
		wantPreAir    bool
		wantFirstAir  string // RFC3339; "" means the timeline should carry none
		wantLastAir   string
		wantEarliest  string
		wantSaysParts []string
	}{
		{
			// The live case. Nothing in the season has aired, yet nine
			// episodes hold files imported nearly two weeks before the
			// premiere.
			name:   "nine files for a season that has not started",
			season: 11,
			eps:    futuramaS11(),
			// A day and a half before the premiere: every unaired margin
			// clears PreAirMarginFloor, so this case keeps testing the pure
			// disease. The floor's own edge lives in the boundary tests.
			now: "2026-08-02T14:03:00Z",
			wantTotal:     10,
			wantAired:     0,
			wantFiles:     9,
			wantUnaired:   []int{1, 2, 3, 4, 5, 6, 7, 8, 9},
			wantEarly:     []int{1, 2, 3, 4, 5, 6, 7, 8, 9},
			wantMissing:   nil,
			wantPreAir:    true,
			wantFirstAir:  "2026-08-04T00:00:00Z",
			wantLastAir:   "2026-09-29T00:00:00Z",
			wantEarliest:  "2026-07-21T09:30:00Z",
			wantSaysParts: []string{"9 of its 10 episodes", "imported 2026-07-21", "first air date of 2026-08-04"},
		},
		{
			// A week on, the double premiere and E3 have aired — E3 exactly at
			// this instant, which counts as aired. The finding shrinks with
			// the calendar but does not go away, and ImportedBeforeAir does not
			// move at all: it compares a file against its OWN episode's air
			// time, not against now.
			name:          "a week in, the aired ones drop out of the finding",
			season:        11,
			eps:           futuramaS11(),
			now:           "2026-08-11T00:00:00Z",
			wantTotal:     10,
			wantAired:     3,
			wantFiles:     9,
			wantUnaired:   []int{4, 5, 6, 7, 8, 9},
			wantEarly:     []int{1, 2, 3, 4, 5, 6, 7, 8, 9},
			wantMissing:   nil,
			wantPreAir:    true,
			wantFirstAir:  "2026-08-04T00:00:00Z",
			wantLastAir:   "2026-09-29T00:00:00Z",
			wantEarliest:  "2026-07-21T09:30:00Z",
			wantSaysParts: []string{"6 of those episodes have not aired yet"},
		},
		{
			// Long after the season ended the same episode list is unremarkable
			// apart from one ordinary gap, and the sentence must read that way.
			name:          "long after the season ended only a gap is left",
			season:        11,
			eps:           futuramaS11(),
			now:           "2027-01-01T00:00:00Z",
			wantTotal:     10,
			wantAired:     10,
			wantFiles:     9,
			wantUnaired:   nil,
			wantEarly:     []int{1, 2, 3, 4, 5, 6, 7, 8, 9},
			wantMissing:   []int{10},
			wantPreAir:    false,
			wantFirstAir:  "2026-08-04T00:00:00Z",
			wantLastAir:   "2026-09-29T00:00:00Z",
			wantEarliest:  "2026-07-21T09:30:00Z",
			wantSaysParts: []string{"Season 11 has 10 episodes: 10 aired, 9 on disk.", "1 episode aired without getting a file"},
		},
		{
			// The boundary that keeps a stale air date from proposing a
			// destructive fix.
			name:   "exactly one unaired episode with a file is not a finding",
			season: 4,
			eps: []EpisodeState{
				{Number: 1, AirsAt: ts("2026-08-04T00:00:00Z"), HasFile: true, FileID: 11, ImportedAt: ts("2026-08-04T02:00:00Z")},
				{Number: 2, AirsAt: ts("2026-08-11T00:00:00Z"), HasFile: true, FileID: 12, ImportedAt: ts("2026-08-05T00:00:00Z")},
			},
			now:           "2026-08-06T00:00:00Z",
			wantTotal:     2,
			wantAired:     1,
			wantFiles:     2,
			wantUnaired:   []int{2},
			wantEarly:     []int{2},
			wantMissing:   nil,
			wantPreAir:    false,
			wantFirstAir:  "2026-08-04T00:00:00Z",
			wantLastAir:   "2026-08-11T00:00:00Z",
			wantEarliest:  "2026-08-04T02:00:00Z",
			wantSaysParts: []string{"Episode 2 already has a file", "air date the service has slightly off"},
		},
		{
			name:   "a fully aired season with every episode on disk",
			season: 3,
			eps: []EpisodeState{
				{Number: 1, AirsAt: ts("2026-01-05T00:00:00Z"), HasFile: true, FileID: 21, ImportedAt: ts("2026-01-05T03:12:00Z")},
				{Number: 2, AirsAt: ts("2026-01-12T00:00:00Z"), HasFile: true, FileID: 22, ImportedAt: ts("2026-01-12T02:40:00Z")},
				{Number: 3, AirsAt: ts("2026-01-19T00:00:00Z"), HasFile: true, FileID: 23, ImportedAt: ts("2026-01-19T04:05:00Z")},
			},
			now:           "2026-08-03T14:03:00Z",
			wantTotal:     3,
			wantAired:     3,
			wantFiles:     3,
			wantUnaired:   nil,
			wantEarly:     nil,
			wantMissing:   nil,
			wantPreAir:    false,
			wantFirstAir:  "2026-01-05T00:00:00Z",
			wantLastAir:   "2026-01-19T00:00:00Z",
			wantEarliest:  "2026-01-05T03:12:00Z",
			wantSaysParts: []string{"Season 3 has 3 episodes: 3 aired, 3 on disk."},
		},
		{
			// A service that imports the moment an episode lands is fast, not
			// impossible: only a file stamped strictly before its air time is
			// evidence.
			name:   "a file stamped at the air instant is not early",
			season: 2,
			eps: []EpisodeState{
				{Number: 1, AirsAt: ts("2026-08-04T00:00:00Z"), HasFile: true, FileID: 31, ImportedAt: ts("2026-08-04T00:00:00Z")},
				{Number: 2, AirsAt: ts("2026-08-04T00:00:00Z"), HasFile: true, FileID: 32, ImportedAt: ts("2026-08-04T00:00:01Z")},
			},
			now:          "2026-08-05T00:00:00Z",
			wantTotal:    2,
			wantAired:    2,
			wantFiles:    2,
			wantUnaired:  nil,
			wantEarly:    nil,
			wantMissing:  nil,
			wantPreAir:   false,
			wantFirstAir: "2026-08-04T00:00:00Z",
			wantLastAir:  "2026-08-04T00:00:00Z",
			wantEarliest: "2026-08-04T00:00:00Z",
		},
		{
			// No air date is evidence of nothing, so these episodes belong to
			// no bucket at all — not aired, not unaired, not missing.
			name:   "unscheduled episodes fall out of every bucket",
			season: 7,
			eps: []EpisodeState{
				{Number: 1, HasFile: true, FileID: 41, ImportedAt: ts("2026-07-22T09:30:00Z")},
				{Number: 2, HasFile: true, FileID: 42, ImportedAt: ts("2026-07-21T09:30:00Z")},
				{Number: 3},
			},
			now:           "2026-08-03T14:03:00Z",
			wantTotal:     3,
			wantAired:     0,
			wantFiles:     2,
			wantUnaired:   nil,
			wantEarly:     nil,
			wantMissing:   nil,
			wantPreAir:    false,
			wantFirstAir:  "",
			wantLastAir:   "",
			wantEarliest:  "2026-07-21T09:30:00Z",
			wantSaysParts: []string{"Season 7 has 3 episodes: 0 aired, 2 on disk."},
		},
		{
			// Episodes arrive in whatever order the service listed them, and a
			// caller may have merged two pages. The counts stay honest about
			// the records handed in — nothing is merged away — while the
			// episode numbers leave in the canonical form a repair needs.
			name:   "unsorted, repeated input still yields sorted unique numbers",
			season: 5,
			eps: []EpisodeState{
				{Number: 5, AirsAt: ts("2026-09-01T00:00:00Z"), HasFile: true, FileID: 55, ImportedAt: ts("2026-07-01T00:00:00Z")},
				{Number: 3, AirsAt: ts("2026-08-02T00:00:00Z")},
				{Number: 2, AirsAt: ts("2026-08-25T00:00:00Z"), HasFile: true, FileID: 52, ImportedAt: ts("2026-07-01T00:00:00Z")},
				{Number: 5, AirsAt: ts("2026-09-01T00:00:00Z"), HasFile: true, FileID: 55, ImportedAt: ts("2026-07-01T00:00:00Z")},
				{Number: 1, AirsAt: ts("2026-08-01T00:00:00Z")},
				{Number: 3, AirsAt: ts("2026-08-02T00:00:00Z")},
			},
			now:          "2026-08-06T00:00:00Z",
			wantTotal:    6,
			wantAired:    3,
			wantFiles:    3,
			wantUnaired:  []int{2, 5},
			wantEarly:    []int{2, 5},
			wantMissing:  []int{1, 3},
			wantPreAir:   true,
			wantFirstAir: "2026-08-01T00:00:00Z",
			wantLastAir:  "2026-09-01T00:00:00Z",
			wantEarliest: "2026-07-01T00:00:00Z",
		},
		{
			name:          "empty season",
			season:        11,
			eps:           nil,
			now:           "2026-08-03T14:03:00Z",
			wantPreAir:    false,
			wantSaysParts: []string{"Season 11 has no episodes."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildSeasonTimeline(tc.season, tc.eps, *ts(tc.now))
			if got.Season != tc.season {
				t.Errorf("Season = %d, want %d", got.Season, tc.season)
			}
			if got.Total != tc.wantTotal {
				t.Errorf("Total = %d, want %d", got.Total, tc.wantTotal)
			}
			if got.Aired != tc.wantAired {
				t.Errorf("Aired = %d, want %d", got.Aired, tc.wantAired)
			}
			if got.FilesTotal != tc.wantFiles {
				t.Errorf("FilesTotal = %d, want %d", got.FilesTotal, tc.wantFiles)
			}
			if !slices.Equal(got.UnairedWithFile, tc.wantUnaired) {
				t.Errorf("UnairedWithFile = %v, want %v", got.UnairedWithFile, tc.wantUnaired)
			}
			if !slices.Equal(got.ImportedBeforeAir, tc.wantEarly) {
				t.Errorf("ImportedBeforeAir = %v, want %v", got.ImportedBeforeAir, tc.wantEarly)
			}
			if !slices.Equal(got.AiredMissing, tc.wantMissing) {
				t.Errorf("AiredMissing = %v, want %v", got.AiredMissing, tc.wantMissing)
			}
			if got.PreAirFill != tc.wantPreAir {
				t.Errorf("PreAirFill = %v, want %v", got.PreAirFill, tc.wantPreAir)
			}
			checkInstant(t, "FirstAir", got.FirstAir, tc.wantFirstAir)
			checkInstant(t, "LastAir", got.LastAir, tc.wantLastAir)
			checkInstant(t, "EarliestImport", got.EarliestImport, tc.wantEarliest)
			if got.Transparency == "" {
				t.Error("every season deserves a sentence; got none")
			}
			for _, part := range tc.wantSaysParts {
				if !strings.Contains(got.Transparency, part) {
					t.Errorf("transparency %q does not mention %q", got.Transparency, part)
				}
			}
		})
	}
}

// checkInstant compares an optional instant against an RFC3339 literal, where
// "" means the timeline should not carry one at all.
func checkInstant(t *testing.T, label string, got *time.Time, want string) {
	t.Helper()
	switch {
	case want == "" && got != nil:
		t.Errorf("%s = %s, want none", label, got.Format(time.RFC3339))
	case want != "" && got == nil:
		t.Errorf("%s = none, want %s", label, want)
	case want != "" && got != nil && !got.Equal(*ts(want)):
		t.Errorf("%s = %s, want %s", label, got.Format(time.RFC3339), want)
	}
}

// The threshold has teeth — the finding proposes deleting files — so the
// boundary is pinned. One unaired episode holding a file is an everyday wrong
// air date; two is a batch of content that does not exist.
func TestPreAirFillNeedsTwoUnairedEpisodesHoldingFiles(t *testing.T) {
	now := *ts("2026-08-03T14:03:00Z")
	for n := 0; n <= 3; n++ {
		t.Run(fmt.Sprintf("%d unaired with files", n), func(t *testing.T) {
			eps := make([]EpisodeState, 0, n)
			for i := 1; i <= n; i++ {
				eps = append(eps, EpisodeState{
					Number:     i,
					AirsAt:     ts("2026-09-01T00:00:00Z"),
					HasFile:    true,
					FileID:     100 + i,
					ImportedAt: ts("2026-07-21T09:30:00Z"),
				})
			}
			got := BuildSeasonTimeline(11, eps, now)
			if want := n >= 2; got.PreAirFill != want {
				t.Fatalf("PreAirFill = %v, want %v", got.PreAirFill, want)
			}
		})
	}
}

// A file stamped at the very instant its episode aired is a service that
// imported the moment the release landed. Only strictly-before is evidence, and
// an episode with no import stamp is no evidence either way.
func TestImportedBeforeAirExcludesTheAirInstantItself(t *testing.T) {
	air := ts("2026-08-04T00:00:00Z")
	eps := []EpisodeState{
		// Beyond the margin floor: the finding.
		{Number: 1, AirsAt: air, HasFile: true, FileID: 1, ImportedAt: tp(air.Add(-PreAirMarginFloor - time.Second))},
		// Inside the floor, at the instant, and after: a premiere on a
		// runtime-staggered calendar, an import the moment the episode
		// landed, and an ordinary post-air import — none are the finding.
		{Number: 2, AirsAt: air, HasFile: true, FileID: 2, ImportedAt: tp(air.Add(-time.Nanosecond))},
		{Number: 3, AirsAt: air, HasFile: true, FileID: 3, ImportedAt: air},
		{Number: 4, AirsAt: air, HasFile: true, FileID: 4, ImportedAt: tp(air.Add(time.Nanosecond))},
		{Number: 5, AirsAt: air, HasFile: true, FileID: 5},
	}
	got := BuildSeasonTimeline(11, eps, *ts("2026-08-05T00:00:00Z"))
	if want := []int{1}; !slices.Equal(got.ImportedBeforeAir, want) {
		t.Fatalf("ImportedBeforeAir = %v, want %v", got.ImportedBeforeAir, want)
	}
}

// Two unscheduled episodes holding files must not read as two unaired episodes
// holding files. That difference is why unaired is its own rule rather than
// !aired: an absent air date proves nothing, and inverting the aired test would
// let missing metadata propose deleting perfectly good files.
func TestUnscheduledEpisodesNeverTripTheFinding(t *testing.T) {
	eps := []EpisodeState{
		{Number: 1, HasFile: true, FileID: 1, ImportedAt: ts("2026-07-21T09:30:00Z")},
		{Number: 2, HasFile: true, FileID: 2, ImportedAt: ts("2026-07-21T09:30:00Z")},
		{Number: 3, HasFile: true, FileID: 3, ImportedAt: ts("2026-07-21T09:30:00Z")},
	}
	got := BuildSeasonTimeline(11, eps, *ts("2026-08-03T14:03:00Z"))
	if got.PreAirFill {
		t.Fatalf("a season with no air dates was called impossible: %q", got.Transparency)
	}
	if len(got.UnairedWithFile) != 0 {
		t.Fatalf("UnairedWithFile = %v, want none — these episodes have no air date", got.UnairedWithFile)
	}
	if got.Aired != 0 {
		t.Fatalf("Aired = %d, want 0 — a file is not proof an episode aired", got.Aired)
	}
}

// Aired is air-date-only on purpose. Reusing the availability rollup's notion of
// aired (which counts a file on disk as proof the episode exists) would fold
// every impossible file into the aired count and report a healthy season.
func TestAiredIgnoresFilesOnDisk(t *testing.T) {
	got := BuildSeasonTimeline(11, futuramaS11(), *ts("2026-08-03T14:03:00Z"))
	if got.Aired != 0 {
		t.Fatalf("Aired = %d, want 0 — nine files on disk, nothing aired yet", got.Aired)
	}
}

// The sentence an admin actually reads when the live case fires. Pinned
// verbatim because it is this detector's entire output for a non-technical
// reader: a rewrite that loses the counts or either date is a regression, not a
// change of style.
func TestTransparencyPinnedForTheLiveCase(t *testing.T) {
	got := BuildSeasonTimeline(11, futuramaS11(), *ts("2026-08-02T14:03:00Z")).Transparency
	want := "Season 11 already has files for 9 of its 10 episodes, and 9 of those episodes have not aired yet — " +
		"the earliest file was imported 2026-07-21, before the season's first air date of 2026-08-04. " +
		"Content that has not been released yet cannot be what those files hold, so they are the wrong content."
	if got != want {
		t.Fatalf("transparency =\n%q\nwant\n%q", got, want)
	}
}

// The finding still stands when the services return no import stamps — the
// files exist and the content does not — so the sentence drops the clause it
// cannot fill, not the finding.
func TestTransparencySurvivesMissingImportStamps(t *testing.T) {
	eps := []EpisodeState{
		{Number: 1, AirsAt: ts("2026-08-04T00:00:00Z"), HasFile: true, FileID: 1},
		{Number: 2, AirsAt: ts("2026-08-11T00:00:00Z"), HasFile: true, FileID: 2},
	}
	got := BuildSeasonTimeline(11, eps, *ts("2026-08-02T14:03:00Z"))
	if !got.PreAirFill {
		t.Fatal("two unaired episodes holding files is the finding, with or without import stamps")
	}
	if !strings.Contains(got.Transparency, "first air date is 2026-08-04") {
		t.Fatalf("transparency should still name the first air date: %q", got.Transparency)
	}
	if strings.Contains(got.Transparency, "imported") {
		t.Fatalf("transparency invented an import date it does not have: %q", got.Transparency)
	}
}

// This text is read by an admin judging a proposed fix, not by an operator of
// the services: it may say imported and aired, never their vocabulary. And only
// a conclusive finding may accuse the library of holding the wrong content.
func TestTransparencySpeaksTheAdminsLanguage(t *testing.T) {
	timelines := []SeasonTimeline{
		BuildSeasonTimeline(11, futuramaS11(), *ts("2026-08-03T14:03:00Z")),
		BuildSeasonTimeline(11, futuramaS11(), *ts("2026-08-11T00:00:00Z")),
		BuildSeasonTimeline(11, futuramaS11(), *ts("2027-01-01T00:00:00Z")),
		BuildSeasonTimeline(11, nil, *ts("2026-08-03T14:03:00Z")),
		BuildSeasonTimeline(4, []EpisodeState{
			{Number: 1, AirsAt: ts("2026-08-11T00:00:00Z"), HasFile: true, FileID: 1, ImportedAt: ts("2026-08-05T00:00:00Z")},
		}, *ts("2026-08-06T00:00:00Z")),
	}
	jargon := []string{"cutoff", "monitored", "unmet", "custom format", "quality profile"}
	for _, tl := range timelines {
		lower := strings.ToLower(tl.Transparency)
		for _, word := range jargon {
			if strings.Contains(lower, word) {
				t.Errorf("transparency uses arr jargon %q: %q", word, tl.Transparency)
			}
		}
		if tl.PreAirFill {
			continue
		}
		for _, accusation := range []string{"wrong content", "cannot be"} {
			if strings.Contains(lower, accusation) {
				t.Errorf("an unflagged season should not say %q: %q", accusation, tl.Transparency)
			}
		}
	}
}

// A pure function must not hand back pointers into the caller's episodes, or a
// caller that reuses its own fixtures quietly rewrites a finished verdict.
func TestTimelineDoesNotAliasTheCallersInstants(t *testing.T) {
	eps := futuramaS11()
	got := BuildSeasonTimeline(11, eps, *ts("2026-08-03T14:03:00Z"))
	*eps[0].AirsAt = *ts("2030-01-01T00:00:00Z")
	*eps[0].ImportedAt = *ts("2030-01-01T00:00:00Z")
	if !got.FirstAir.Equal(*ts("2026-08-04T00:00:00Z")) {
		t.Errorf("FirstAir followed the caller's edit: %s", got.FirstAir.Format(time.RFC3339))
	}
	if !got.EarliestImport.Equal(*ts("2026-07-21T09:30:00Z")) {
		t.Errorf("EarliestImport followed the caller's edit: %s", got.EarliestImport.Format(time.RFC3339))
	}
}

// reacherS4Premiere rebuilds the 2026-08-12 false positive (issue 858): Amazon
// released three episodes at once, TheTVDB staggered their air times one
// runtime apart (07:00 / 07:49 / 08:38Z), and the grab imported E02/E03 at
// 07:27 — 22 and 71 minutes "before air" under that calendar. Five more
// episodes air weekly with no files.
func reacherS4Premiere() []EpisodeState {
	airs := []string{
		"2026-08-12T07:00:00Z",
		"2026-08-12T07:49:00Z",
		"2026-08-12T08:38:00Z",
		"2026-08-19T07:00:00Z",
		"2026-08-26T07:00:00Z",
		"2026-09-02T07:00:00Z",
		"2026-09-09T07:00:00Z",
		"2026-09-16T07:00:00Z",
	}
	eps := make([]EpisodeState, 0, len(airs))
	for i, a := range airs {
		e := EpisodeState{Number: i + 1, Title: fmt.Sprintf("Episode %d", i+1), AirsAt: ts(a)}
		if e.Number == 2 || e.Number == 3 {
			e.HasFile = true
			e.FileID = 7000 + e.Number
			e.ImportedAt = ts("2026-08-12T07:27:37Z")
		}
		eps = append(eps, e)
	}
	return eps
}

// TestPremiereStaggerIsNotPreAirFill freezes the Reacher shape: files whose
// air times sit inside PreAirMarginFloor are a binge premiere on a staggered
// calendar, never the pre-air finding. On 2026-08-12 this exact season
// proposed deleting two legitimate files.
func TestPremiereStaggerIsNotPreAirFill(t *testing.T) {
	tl := BuildSeasonTimeline(4, reacherS4Premiere(), *ts("2026-08-12T07:27:44Z"))
	if tl.PreAirFill {
		t.Fatalf("premiere stagger read as pre-air fill: %+v", tl)
	}
	if len(tl.UnairedWithFile) != 0 {
		t.Errorf("UnairedWithFile = %v, want none inside the margin floor", tl.UnairedWithFile)
	}
	if len(tl.ImportedBeforeAir) != 0 {
		t.Errorf("ImportedBeforeAir = %v, want none inside the margin floor", tl.ImportedBeforeAir)
	}
	// The counts still report the calendar as written — only the verdict
	// applies the floor. One aired episode, two files, at 07:27.
	if tl.Aired != 1 || tl.FilesTotal != 2 {
		t.Errorf("Aired/FilesTotal = %d/%d, want 1/2 (display truth unchanged)", tl.Aired, tl.FilesTotal)
	}
}

// TestPreAirMarginFloorBoundary pins the floor's edge on both buckets: a file
// beating its air time by more than the floor is the finding, inside the floor
// is a premiere artifact. The real incident's margin was thirteen days.
func TestPreAirMarginFloorBoundary(t *testing.T) {
	now := *ts("2026-08-12T00:00:00Z")
	mk := func(airOffset, importOffset time.Duration) []EpisodeState {
		air := now.Add(airOffset)
		imp := now.Add(importOffset)
		eps := make([]EpisodeState, 0, 2)
		for n := 1; n <= 2; n++ {
			eps = append(eps, EpisodeState{
				Number: n, AirsAt: tp(air), HasFile: true, FileID: 8000 + n, ImportedAt: tp(imp),
			})
		}
		return eps
	}

	beyond := BuildSeasonTimeline(1, mk(PreAirMarginFloor+time.Minute, 0), now)
	if !beyond.PreAirFill || len(beyond.UnairedWithFile) != 2 {
		t.Errorf("air beyond the floor = fill %v unaired %v, want the finding", beyond.PreAirFill, beyond.UnairedWithFile)
	}
	inside := BuildSeasonTimeline(1, mk(PreAirMarginFloor-time.Minute, 0), now)
	if inside.PreAirFill || len(inside.UnairedWithFile) != 0 {
		t.Errorf("air inside the floor = fill %v unaired %v, want no finding", inside.PreAirFill, inside.UnairedWithFile)
	}

	// ImportedBeforeAir measures import-vs-air, independent of now: push the
	// air time past to isolate the bucket.
	sharperBeyond := BuildSeasonTimeline(1, mk(-time.Hour, -time.Hour-PreAirMarginFloor-time.Minute), now)
	if len(sharperBeyond.ImportedBeforeAir) != 2 {
		t.Errorf("import beyond the floor before air = %v, want both flagged", sharperBeyond.ImportedBeforeAir)
	}
	sharperInside := BuildSeasonTimeline(1, mk(-time.Hour, -time.Hour-PreAirMarginFloor+time.Minute), now)
	if len(sharperInside.ImportedBeforeAir) != 0 {
		t.Errorf("import inside the floor before air = %v, want none", sharperInside.ImportedBeforeAir)
	}
}
