package mcp

import (
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

func TestDisplayMediaMismatch(t *testing.T) {
	if got := displayMediaMismatch("Toy Story 5", "2026", "Toy Story 5", "2026"); got != "" {
		t.Fatalf("matching item rejected: %s", got)
	}
	if got := displayMediaMismatch("I Will Find You", "2026", "Kaliveedu", "1996"); got == "" {
		t.Fatal("mismatched title was not rejected")
	}
	if got := displayMediaMismatch("Toy Story 5", "2026", "Toy Story 5", "1995"); got == "" {
		t.Fatal("mismatched year was not rejected")
	}
}

func TestFormatSearchResultsIncludesMediaType(t *testing.T) {
	text := formatSearchResults([]tmdb.SearchResult{
		{
			ID:          123,
			Title:       "Example",
			ReleaseDate: "2026-01-01",
			MediaType:   "movie",
		},
	}, 10)

	if !strings.Contains(text, "[media_type: movie]") {
		t.Fatalf("formatted result missing media_type: %s", text)
	}
}
