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
