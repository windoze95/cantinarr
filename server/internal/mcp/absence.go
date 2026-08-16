package mcp

import (
	"fmt"
	"strings"
)

// A read tool that finds nothing has said one of two completely different
// things: the thing is not there, or this tool could not see it. Rendered the
// same way — "No TV history found." — they are indistinguishable, and a reader
// that cannot tell them apart stops looking.
//
// That is not hypothetical. Issue #814 (a whole season of files imported before
// the episodes aired) went undiagnosed because three reads came back empty and
// were believed: history was filtering a page of recent GLOBAL events for a
// title whose last event was two weeks old, and the queue was empty because the
// download had finished — which is the expected state for every complaint about
// CONTENT, not evidence of anything. The agent had no way to know either.
//
// So every empty result in this package should answer two questions: what was
// actually searched, and what an empty answer does and does not rule out.

// searchedScope renders what a scoped read looked at, in the caller's own terms.
// Empty when the read was not narrowed to anything.
func (s mediaReadScope) searchedScope() string {
	switch {
	case s.QueueID > 0:
		return fmt.Sprintf("queue item %d", s.QueueID)
	case s.BookID > 0:
		return "this book"
	case s.AuthorID > 0:
		return "this author"
	case s.EpisodeNumber > 0:
		return fmt.Sprintf("season %d episode %d of this series", s.SeasonNumber, s.EpisodeNumber)
	case s.SeasonNumber > 0:
		return fmt.Sprintf("season %d of this series", s.SeasonNumber)
	case s.hasTitleIdentity():
		return "this title"
	default:
		return ""
	}
}

// noHistoryText explains an empty history read.
//
// perTitle distinguishes the two cases that matter. A per-title read asked the
// service for that title's own records, so empty means the service genuinely has
// none — there is no older event hiding behind a page boundary. A global read is
// a window on recent activity across the whole library, and an older event for
// one title simply would not appear in it. Reporting both as "no history found"
// is what made a two-week-old import invisible.
func noHistoryText(mediaLabel string, scope mediaReadScope, perTitle bool, fetchLimit int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "No %s history found", mediaLabel)
	searched := scope.searchedScope()
	if searched != "" {
		fmt.Fprintf(&sb, " for %s", searched)
	}
	sb.WriteString(".")
	switch {
	case perTitle:
		sb.WriteString(" This read asked the service for that title's own history rather than a slice of recent activity, so there is no older event to find: the service has no grab, import, or failure recorded for it.")
	case searched != "":
		fmt.Fprintf(&sb, " NOTE: the title could not be resolved in the library, so this fell back to the most recent %d events across the WHOLE library and filtered them. An older event for it would not appear here — an empty answer does not mean it has no history.", fetchLimit)
	default:
		fmt.Fprintf(&sb, " This is the most recent %d events across the whole library; anything older is outside this window.", fetchLimit)
	}
	return sb.String()
}

// emptyQueueText explains an empty queue read.
//
// The second sentence is the one that matters, and it only appears on a scoped
// read because that is an issue investigation. An empty queue is the EXPECTED
// state for any complaint about content — the download finished and imported,
// possibly weeks ago — so reading it as "nothing is wrong" is exactly backwards.
func emptyQueueText(label string, scope mediaReadScope) string {
	searched := scope.searchedScope()
	if searched == "" {
		return label + ": empty — nothing is downloading."
	}
	return fmt.Sprintf("%s: empty — nothing for %s is downloading right now. "+
		"That says nothing about the files already in the library: a download that finished and imported leaves the queue, "+
		"so a complaint about wrong or bad content is EXPECTED to show an empty queue. Look at the library and the history instead.",
		label, searched)
}

// noQueueProblemsText explains an Import Doctor pass that flagged nothing.
//
// Same trap in its strongest form: the Doctor only ever classifies queue rows,
// so "no queue problems" is a statement about downloads in flight and about
// nothing else at all. An empty queue makes it vacuous rather than reassuring.
func noQueueProblemsText(healthy int, scope mediaReadScope) string {
	var sb strings.Builder
	sb.WriteString("Import Doctor: no queue problems found.")
	if healthy > 0 {
		fmt.Fprintf(&sb, " %d item(s) are downloading or importing normally.", healthy)
		return sb.String()
	}
	if searched := scope.searchedScope(); searched != "" {
		fmt.Fprintf(&sb, " Nothing for %s is in the queue at all, so there was nothing to diagnose. "+
			"The Doctor reads downloads in flight and only those — it cannot see a problem with a file that already imported.", searched)
		return sb.String()
	}
	sb.WriteString(" The queue is empty, so there was nothing to diagnose; the Doctor reads downloads in flight and only those.")
	return sb.String()
}
