package mcp

import (
	"strings"
	"testing"
)

// The distinction these tests defend: an empty read must say whether the thing
// is absent or whether the tool could not see it. Issue #814 was misdiagnosed
// because "No TV history found." meant the second and read like the first.

func TestNoHistoryTextSeparatesAbsenceFromBlindness(t *testing.T) {
	scoped := mediaReadScope{TmdbID: 615, TvdbID: 73871, SeasonNumber: 11}

	perTitle := noHistoryText("TV", scoped, true, 100)
	if !strings.Contains(perTitle, "season 11 of this series") {
		t.Errorf("per-title text does not name what was searched: %q", perTitle)
	}
	if !strings.Contains(perTitle, "no older event to find") {
		t.Errorf("per-title text does not rule out an older event: %q", perTitle)
	}
	if strings.Contains(perTitle, "whole library") {
		t.Errorf("per-title text describes a global window: %q", perTitle)
	}

	// The fallback is the #814 shape exactly: a scoped question answered from a
	// global window. It has to admit that.
	fellBack := noHistoryText("TV", scoped, false, 100)
	if !strings.Contains(fellBack, "fell back") || !strings.Contains(fellBack, "100") {
		t.Errorf("fallback text hides that it read a global window: %q", fellBack)
	}
	if !strings.Contains(fellBack, "does not mean it has no history") {
		t.Errorf("fallback text does not warn the answer is inconclusive: %q", fellBack)
	}
	if perTitle == fellBack {
		t.Error("a per-title miss and a global-window miss read identically")
	}

	unscoped := noHistoryText("TV", mediaReadScope{}, false, 20)
	if strings.Contains(unscoped, "fell back") {
		t.Errorf("an unscoped read claims to have fallen back: %q", unscoped)
	}
	if !strings.Contains(unscoped, "20") || !strings.Contains(unscoped, "outside this window") {
		t.Errorf("unscoped text does not bound its window: %q", unscoped)
	}
}

func TestEmptyQueueTextWarnsOnlyWhenInvestigating(t *testing.T) {
	// A scoped read is an issue investigation, and there the empty queue is the
	// EXPECTED state for a content complaint — the single inference that broke
	// #814.
	scoped := emptyQueueText("TV queue", mediaReadScope{TmdbID: 615, SeasonNumber: 11})
	if !strings.Contains(scoped, "season 11 of this series") {
		t.Errorf("scoped text does not name what was searched: %q", scoped)
	}
	for _, want := range []string{"already in the library", "EXPECTED", "history"} {
		if !strings.Contains(scoped, want) {
			t.Errorf("scoped text missing %q: %q", want, scoped)
		}
	}

	// An admin browsing the whole queue is not investigating anything, and does
	// not need the lecture.
	unscoped := emptyQueueText("TV queue", mediaReadScope{})
	if strings.Contains(unscoped, "EXPECTED") {
		t.Errorf("unscoped text carries investigation guidance: %q", unscoped)
	}
	if !strings.Contains(unscoped, "nothing is downloading") {
		t.Errorf("unscoped text = %q", unscoped)
	}
}

func TestNoQueueProblemsTextSaysWhatTheDoctorCannotSee(t *testing.T) {
	// Healthy items in flight: the Doctor really did look at something.
	withWork := noQueueProblemsText(3, mediaReadScope{TmdbID: 615})
	if !strings.Contains(withWork, "3 item(s)") {
		t.Errorf("healthy count lost: %q", withWork)
	}
	if strings.Contains(withWork, "nothing to diagnose") {
		t.Errorf("a queue with work in it claimed there was nothing to diagnose: %q", withWork)
	}

	// Nothing in the queue at all: "no problems found" is vacuous, and saying so
	// is the difference between an agent that stops and one that keeps looking.
	empty := noQueueProblemsText(0, mediaReadScope{TmdbID: 615, SeasonNumber: 11})
	if !strings.Contains(empty, "nothing to diagnose") {
		t.Errorf("empty-queue pass did not say it diagnosed nothing: %q", empty)
	}
	if !strings.Contains(empty, "already imported") {
		t.Errorf("empty-queue pass did not name what it cannot see: %q", empty)
	}
	if empty == withWork {
		t.Error("a queue with healthy work and an empty queue read identically")
	}
}

func TestSearchedScopeNarrowsToTheMostExactThingKnown(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scope mediaReadScope
		want  string
	}{
		{"queue id wins", mediaReadScope{QueueID: 41, TmdbID: 615, SeasonNumber: 11}, "queue item 41"},
		{"episode", mediaReadScope{TmdbID: 615, SeasonNumber: 11, EpisodeNumber: 3}, "season 11 episode 3 of this series"},
		{"season", mediaReadScope{TmdbID: 615, SeasonNumber: 11}, "season 11 of this series"},
		{"title only", mediaReadScope{TmdbID: 615}, "this title"},
		{"book", mediaReadScope{BookID: 9}, "this book"},
		{"author", mediaReadScope{AuthorID: 4}, "this author"},
		{"nothing", mediaReadScope{}, ""},
	} {
		if got := tc.scope.searchedScope(); got != tc.want {
			t.Errorf("%s: searchedScope() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
