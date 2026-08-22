package ai

import (
	"strings"

	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// carouselSourceTools are the media-listing tools whose successful results
// make a visual carousel expected: when a turn consumes one of these and the
// final answer names titles but display_media was never called, the user gets
// prose with no carousel. Hosted frontier models follow the system prompt's
// display_media MUST reliably; smaller local models (issue #484) drop it often
// enough that the server has to backstop the contract.
var carouselSourceTools = map[string]bool{
	"search_movies":       true,
	"search_tv_shows":     true,
	"search_books":        true,
	"get_trending":        true,
	"get_recommendations": true,
}

// displayMediaNudge is the one-time, server-injected user message that asks
// the model to post the carousel it owes. Text produced after the nudge is
// deliberately not streamed to the client (the user already has the full
// answer), so the message can demand a bare tool call without harming UX.
const displayMediaNudge = "System reminder: your answer named concrete titles taken from tool results, but you never called display_media, so the user sees no visual carousel. Call display_media now with exactly those items, in the order your answer mentions them, copying titles, years, media types, and TMDB IDs or foreign_book_ids from the tool results. None of the text you produce from now on will be shown to the user, so do not repeat your answer. If your answer truly named no concrete media items, reply with the single word: done."

// carouselWatch tracks, across one interactive agent loop, whether the turn
// owes the user a display_media carousel. It is fed from the tool-execution
// paths (success only — an errored source tool proved nothing) and consulted
// when a leg ends with plain text.
type carouselWatch struct {
	// sourceItems is set once any carousel-source tool returns at least one
	// structured media item; empty results never owe a carousel.
	sourceItems bool
	// displayed is set once display_media executes without a transport-level
	// error; from then on the turn owes nothing.
	displayed bool
	// nudged is set when the loop injects displayMediaNudge, so a model that
	// ignores the reminder gets its text answer and no retry storm.
	nudged bool
}

// observe records one successful tool execution. structured is the tool's
// StructuredData payload after ExecuteTool's redaction pass, which JSON
// round-trips the MCP layer's []mcp.MediaResultItem into []any of maps — so
// both shapes count.
func (w *carouselWatch) observe(name string, structured any) {
	if name == "display_media" {
		w.displayed = true
		return
	}
	if !carouselSourceTools[name] {
		return
	}
	if hasMediaItems(structured) {
		w.sourceItems = true
	}
}

// hasMediaItems reports whether structured holds at least one media item with
// a title, in either the typed or the redacted-JSON shape.
func hasMediaItems(structured any) bool {
	switch items := structured.(type) {
	case []mcp.MediaResultItem:
		return len(items) > 0
	case []any:
		for _, item := range items {
			if fields, ok := item.(map[string]any); ok {
				if title, ok := fields["title"].(string); ok && strings.TrimSpace(title) != "" {
					return true
				}
			}
		}
	}
	return false
}

// shouldNudge reports whether a leg that finished with plain text should be
// answered with the one-time display_media reminder instead of ending the
// turn. finalText is the leg's assistant text.
func (w *carouselWatch) shouldNudge(finalText string) bool {
	if w.nudged || w.displayed || !w.sourceItems {
		return false
	}
	return strings.TrimSpace(finalText) != ""
}

// markNudged flips the watch into its terminal reminded state and returns the
// nudge text, keeping the two mutations callers need atomic at the call site.
func (w *carouselWatch) markNudged() string {
	w.nudged = true
	return displayMediaNudge
}
