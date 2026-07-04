package push

import "testing"

// TestClaimContentAlertDedupes pins the new-content dedupe: the queue-poll
// witness and the arr webhook receiver can both report the same import (and a
// season pack arrives as one webhook per file), but only the first claim in
// the window may send. Distinct content is never suppressed.
func TestClaimContentAlertDedupes(t *testing.T) {
	n := &Notifier{}

	if !n.claimContentAlert(CategoryNewEpisode, "tv", "Gappy Show", 700) {
		t.Fatal("first claim must send")
	}
	if n.claimContentAlert(CategoryNewEpisode, "tv", "Gappy Show", 700) {
		t.Error("duplicate claim within the window must be suppressed")
	}
	if !n.claimContentAlert(CategoryNewMovie, "movie", "Gappy Show", 700) {
		t.Error("a different category is different content")
	}
	if !n.claimContentAlert(CategoryNewEpisode, "tv", "Other Show", 0) {
		t.Error("tmdb 0 with a different title is different content")
	}
	if n.claimContentAlert(CategoryNewEpisode, "tv", "Other Show", 0) {
		t.Error("tmdb 0 duplicates of the same title must still dedupe")
	}
}
