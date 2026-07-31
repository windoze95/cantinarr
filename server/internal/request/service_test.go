package request

import (
	"reflect"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// ptrStats is a tiny helper to build a season statistics pointer inline.
func seasonStats(fileCount, epCount int) *sonarr.SeasonStatistics {
	return &sonarr.SeasonStatistics{EpisodeFileCount: fileCount, EpisodeCount: epCount}
}

// TestStatusFromCompletion maps completeness onto the request status
// vocabulary, including the reported trap: a series whose few monitored
// episodes are all downloaded must read partial, not available.
func TestStatusFromCompletion(t *testing.T) {
	cases := []struct {
		name      string
		c         sonarr.Completion
		monitored bool
		want      string
	}{
		{"complete", sonarr.Completion{Files: 4, Aired: 4}, true, StatusAvailable},
		{"trap: files < aired stays partial even when monitored subset is done", sonarr.Completion{Files: 2, Aired: 4}, true, StatusPartial},
		{"partial while unmonitored (action optional)", sonarr.Completion{Files: 2, Aired: 4}, false, StatusPartial},
		{"nothing on disk, monitored", sonarr.Completion{Files: 0, Aired: 4}, true, StatusRequested},
		{"nothing on disk, unmonitored", sonarr.Completion{Files: 0, Aired: 4}, false, StatusUnavailable},
		{"nothing aired yet, monitored", sonarr.Completion{}, true, StatusRequested},
	}
	for _, c := range cases {
		if got, _ := statusFromCompletion(c.c, c.monitored); got != c.want {
			t.Errorf("%s: status = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestSeasonStatusesFromCompletion builds season rows from real episode
// counts: Specials dropped, x/y = files/aired, unknown seasons tolerated.
func TestSeasonStatusesFromCompletion(t *testing.T) {
	series := &sonarr.Series{
		Seasons: []sonarr.SeasonResource{
			{SeasonNumber: 0, Monitored: true},
			{SeasonNumber: 1, Monitored: true},
			{SeasonNumber: 2, Monitored: false},
		},
	}
	bySeason := map[int]sonarr.Completion{
		0: {Files: 1, Aired: 1},
		1: {Files: 2, Aired: 4},
		3: {Files: 5, Aired: 5}, // not in series.Seasons (defensive)
	}
	got := seasonStatusesFromCompletion(series, bySeason)
	want := map[int]struct {
		status string
		files  int
		eps    int
	}{
		1: {StatusPartial, 2, 4},
		2: {StatusUnavailable, 0, 0},
		3: {StatusAvailable, 5, 5},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d seasons, want %d (Specials excluded): %+v", len(got), len(want), got)
	}
	for _, ss := range got {
		w, ok := want[ss.SeasonNumber]
		if !ok {
			t.Errorf("unexpected season %d", ss.SeasonNumber)
			continue
		}
		if ss.Status != w.status || ss.EpisodeFileCount != w.files || ss.EpisodeCount != w.eps {
			t.Errorf("season %d = %s %d/%d, want %s %d/%d",
				ss.SeasonNumber, ss.Status, ss.EpisodeFileCount, ss.EpisodeCount, w.status, w.files, w.eps)
		}
	}
}

// TestSeasonStatuses covers the per-season status derivation: Specials are
// dropped, and each real season's status maps from its file counts + monitoring
// onto the title status vocabulary.
func TestSeasonStatuses(t *testing.T) {
	series := &sonarr.Series{
		Seasons: []sonarr.SeasonResource{
			// Specials: must be excluded regardless of stats.
			{SeasonNumber: 0, Monitored: true, Statistics: seasonStats(5, 5)},
			// Fully available.
			{SeasonNumber: 1, Monitored: true, Statistics: seasonStats(10, 10)},
			// Partially available.
			{SeasonNumber: 2, Monitored: true, Statistics: seasonStats(3, 10)},
			// Monitored, nothing downloaded yet -> requested.
			{SeasonNumber: 3, Monitored: true, Statistics: seasonStats(0, 8)},
			// Unmonitored, nothing downloaded -> unavailable.
			{SeasonNumber: 4, Monitored: false, Statistics: seasonStats(0, 8)},
		},
	}

	got := seasonStatuses(series)
	if len(got) != 4 {
		t.Fatalf("got %d seasons, want 4 (Specials excluded): %+v", len(got), got)
	}

	want := map[int]string{
		1: StatusAvailable,
		2: StatusPartial,
		3: StatusRequested,
		4: StatusUnavailable,
	}
	for _, s := range got {
		if s.SeasonNumber == 0 {
			t.Errorf("season 0 (Specials) should have been excluded")
		}
		if want[s.SeasonNumber] != s.Status {
			t.Errorf("season %d status = %q, want %q", s.SeasonNumber, s.Status, want[s.SeasonNumber])
		}
	}

	// Spot-check counts + progress on the partial season.
	for _, s := range got {
		if s.SeasonNumber == 2 {
			if s.EpisodeFileCount != 3 || s.EpisodeCount != 10 {
				t.Errorf("season 2 counts = %d/%d, want 3/10", s.EpisodeFileCount, s.EpisodeCount)
			}
			if s.Progress < 0.29 || s.Progress > 0.31 {
				t.Errorf("season 2 progress = %v, want ~0.30", s.Progress)
			}
		}
		if s.SeasonNumber == 1 && s.Progress != 1.0 {
			t.Errorf("season 1 progress = %v, want 1.0", s.Progress)
		}
	}
}

// TestSeasonStatusesNoSeasons returns nil when the series carries no seasons[]
// (e.g. an older Sonarr response or a series not yet refreshed).
func TestSeasonStatusesNoSeasons(t *testing.T) {
	if got := seasonStatuses(&sonarr.Series{}); got != nil {
		t.Errorf("seasonStatuses(empty) = %+v, want nil", got)
	}
	if got := seasonStatuses(nil); got != nil {
		t.Errorf("seasonStatuses(nil) = %+v, want nil", got)
	}
}

// TestSeasonNumbersRoundTrip proves the explicit season list survives the
// season_scope persistence used by the approval flow: encode -> store -> decode
// yields the same normalized list, and a coarse scope decodes to nil so the
// coarse path is preserved.
func TestSeasonNumbersRoundTrip(t *testing.T) {
	// Unsorted + duplicate input normalizes to a sorted, de-duped list.
	encoded := encodeSeasonNumbers([]int{5, 3, 5, 1})
	if encoded != "[1,3,5]" {
		t.Errorf("encodeSeasonNumbers = %q, want [1,3,5]", encoded)
	}
	if got := decodeSeasonNumbers(encoded); !reflect.DeepEqual(got, []int{1, 3, 5}) {
		t.Errorf("decodeSeasonNumbers(%q) = %v, want [1 3 5]", encoded, got)
	}

	// An empty list encodes to "" (stored as NULL season_scope).
	if got := encodeSeasonNumbers(nil); got != "" {
		t.Errorf("encodeSeasonNumbers(nil) = %q, want empty", got)
	}

	// Coarse scopes and empties decode to nil (no explicit list -> coarse path).
	for _, scope := range []string{"", SeasonScopeAll, SeasonScopeFirst, SeasonScopeLatest, SeasonScopePilot, "garbage"} {
		if got := decodeSeasonNumbers(scope); got != nil {
			t.Errorf("decodeSeasonNumbers(%q) = %v, want nil", scope, got)
		}
	}
}

// TestNormalizeSeasonNumbers drops negatives and de-dups while keeping season 0
// when explicitly present (the caller decides whether Specials are offered).
func TestNormalizeSeasonNumbers(t *testing.T) {
	got := normalizeSeasonNumbers([]int{-1, 2, 0, 2, 4, -3})
	want := []int{0, 2, 4}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeSeasonNumbers = %v, want %v", got, want)
	}
}

// TestScopeSeasonNumbers expands the coarse scopes against a series' real
// seasons, excluding Specials.
func TestScopeSeasonNumbers(t *testing.T) {
	series := &sonarr.Series{
		Seasons: []sonarr.SeasonResource{
			{SeasonNumber: 3},
			{SeasonNumber: 0},
			{SeasonNumber: 1},
			{SeasonNumber: 2},
		},
	}
	cases := []struct {
		scope string
		want  []int
	}{
		{SeasonScopeAll, []int{1, 2, 3}},
		{SeasonScopeFirst, []int{1}},
		{SeasonScopeLatest, []int{3}},
		{"", []int{1, 2, 3}}, // unknown scope behaves like all
	}
	for _, c := range cases {
		if got := scopeSeasonNumbers(series, c.scope); !reflect.DeepEqual(got, c.want) {
			t.Errorf("scopeSeasonNumbers(%q) = %v, want %v", c.scope, got, c.want)
		}
	}
	if got := scopeSeasonNumbers(&sonarr.Series{}, SeasonScopeAll); got != nil {
		t.Errorf("scopeSeasonNumbers(no seasons) = %v, want nil", got)
	}
}

// TestSeasonHasAllFiles prefers totalEpisodeCount so an in-progress season
// (aired episodes all downloaded, more announced) still counts as incomplete.
func TestSeasonHasAllFiles(t *testing.T) {
	series := &sonarr.Series{
		Seasons: []sonarr.SeasonResource{
			{SeasonNumber: 1, Statistics: &sonarr.SeasonStatistics{EpisodeFileCount: 10, EpisodeCount: 10, TotalEpisodeCount: 10}},
			{SeasonNumber: 2, Statistics: &sonarr.SeasonStatistics{EpisodeFileCount: 2, EpisodeCount: 2, TotalEpisodeCount: 10}},
			{SeasonNumber: 3, Statistics: &sonarr.SeasonStatistics{EpisodeFileCount: 4, EpisodeCount: 4}},
			{SeasonNumber: 4},
		},
	}
	cases := []struct {
		season int
		want   bool
	}{
		{1, true},
		// The trap season from the reported bug: 2 files, 2 tracked episodes
		// (percent reads 100) but 10 known episodes -> incomplete.
		{2, false},
		{3, true},  // no totalEpisodeCount reported -> fall back to episodeCount
		{4, false}, // no statistics at all
		{9, false}, // unknown season
	}
	for _, c := range cases {
		if got := seasonHasAllFiles(series, c.season); got != c.want {
			t.Errorf("seasonHasAllFiles(season %d) = %v, want %v", c.season, got, c.want)
		}
	}
}

// TestSeasonSelection builds the explicit-season add payload: every known
// season present with monitored set only for the chosen ones (Specials stay
// unmonitored unless chosen), plus defensive entries for chosen seasons the
// lookup didn't list.
func TestSeasonSelection(t *testing.T) {
	known := []sonarr.SeasonResource{
		{SeasonNumber: 0, Monitored: true}, // lookup flags must not leak through
		{SeasonNumber: 1, Monitored: true},
		{SeasonNumber: 2},
	}
	got := seasonSelection(known, []int{2, 7})
	want := map[int]bool{0: false, 1: false, 2: true, 7: true}
	if len(got) != len(want) {
		t.Fatalf("seasonSelection returned %d seasons, want %d: %+v", len(got), len(want), got)
	}
	for _, ss := range got {
		mon, ok := want[ss.SeasonNumber]
		if !ok {
			t.Errorf("unexpected season %d in selection", ss.SeasonNumber)
			continue
		}
		if ss.Monitored != mon {
			t.Errorf("season %d monitored = %v, want %v", ss.SeasonNumber, ss.Monitored, mon)
		}
	}
}
