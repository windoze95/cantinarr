package sonarr

import (
	"testing"
	"time"
)

func TestUnairedReplacementBoundary(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	before, after := now.Add(-time.Nanosecond), now.Add(time.Nanosecond)
	for _, tc := range []struct {
		name string
		air  *time.Time
		want bool
	}{
		{name: "unknown"},
		{name: "already aired", air: &before},
		{name: "air instant", air: &now},
		{name: "just before air", air: &after, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasUnairedEpisode([]Episode{{AirDateUtc: tc.air}}, now); got != tc.want {
				t.Fatalf("unaired=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestQueueAirTimeRequiresExactEpisodeIdentity(t *testing.T) {
	air := time.Now().UTC().Add(time.Hour)
	row := DetailedQueueItem{SeriesID: 3, EpisodeID: 55, Episode: &EpisodeContext{
		ID: 55, SeriesID: 3, SeasonNumber: 0, EpisodeNumber: 1, AirDateUtc: &air,
	}}
	if row.AirTimeAtSnapshot() == nil {
		t.Fatal("a consistently identified special lost its air date")
	}
	row.Episode.ID = 56
	if row.AirTimeAtSnapshot() != nil {
		t.Fatal("another episode's date escaped into diagnosis")
	}
}
