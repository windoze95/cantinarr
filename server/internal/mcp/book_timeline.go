package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// get_book_timeline is the book sibling of the episode timeline — the
// strongest evidence read the system has, previously TV-only. It joins what
// the library HOLDS (the record's files, with import dates) to what HAPPENED
// (the grab and import history, newest first, with download identities), so a
// wrong-book report is judged from receipts instead of a title string.
func (s *ToolServer) getBookTimeline(input json.RawMessage, instanceID string) (*ToolResult, error) {
	var params struct {
		MediaType string `json:"media_type"`
		BookID    int    `json:"book_id"`
	}
	if err := json.Unmarshal(nonEmptyJSON(input), &params); err != nil {
		return nil, fmt.Errorf("invalid get_book_timeline input: %w", err)
	}
	if params.MediaType != "" && params.MediaType != "book" {
		return &ToolResult{Text: "A book timeline exists only for books; TV has get_episode_timeline."}, nil
	}
	if params.BookID <= 0 {
		return &ToolResult{Text: "get_book_timeline needs the issue's book_id."}, nil
	}
	client := s.GetChaptarrFor(instanceID)
	if client == nil {
		return &ToolResult{Text: "Chaptarr is not configured."}, nil
	}
	return s.getBookTimelineWithClient(client, input)
}

func (s *ToolServer) getBookTimelineWithClient(client *chaptarr.Client, input json.RawMessage) (*ToolResult, error) {
	var params struct {
		MediaType string `json:"media_type"`
		BookID    int    `json:"book_id"`
	}
	if err := json.Unmarshal(nonEmptyJSON(input), &params); err != nil {
		return nil, fmt.Errorf("invalid get_book_timeline input: %w", err)
	}
	book, err := client.GetBook(params.BookID)
	if err != nil {
		return nil, fmt.Errorf("resolve book: %w", err)
	}
	if book == nil {
		return &ToolResult{Text: "This book record is not in the library. This read asked Chaptarr for the exact record id."}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — timeline:\n", book.Title)

	files, err := client.GetBookFilesForBook(params.BookID)
	if err != nil {
		return nil, fmt.Errorf("read book files: %w", err)
	}
	if len(files) == 0 {
		sb.WriteString("Files: NONE on disk right now. This read listed the record's own file rows; an empty answer here is genuine absence.\n")
	} else {
		fmt.Fprintf(&sb, "Files (%d):\n", len(files))
		for _, f := range files {
			fmt.Fprintf(&sb, "- %s (%.1f MB", f.Path, float64(f.Size)/1e6)
			if f.DateAdded != nil {
				fmt.Fprintf(&sb, ", imported %s", f.DateAdded.UTC().Format("2006-01-02"))
			}
			sb.WriteString(")\n")
		}
	}

	grabs, gerr := client.GetBookGrabs(params.BookID, 30)
	imports, ierr := client.GetImportHistory(params.BookID, "", 30)
	if gerr != nil && ierr != nil {
		sb.WriteString("History: UNREADABLE right now — this says nothing about whether events exist (blindness, not absence).\n")
		return &ToolResult{Text: sb.String()}, nil
	}
	// A one-sided failure must not be rendered as the whole history. The import
	// read errors BY DESIGN when the record holds more import events than one
	// page can prove complete; swallowing that produced a grabs-only list under
	// a "grabs + imports" heading, which reads as "no imports" and stops an
	// investigation cold.
	scope := "grabs + imports"
	if gerr != nil {
		scope = "imports only — GRAB history is UNREADABLE right now, so missing grabs are blindness, not absence"
	} else if ierr != nil {
		scope = "grabs only — IMPORT history is UNREADABLE right now, so missing imports are blindness, not absence"
	}
	events := append([]chaptarr.HistoryRecord{}, grabs...)
	events = append(events, imports...)
	if len(events) == 0 {
		fmt.Fprintf(&sb, "History (%s): no events for this record in the newest pages. This read asked for the record's own history; older events beyond those pages are not ruled out.\n", scope)
	} else {
		fmt.Fprintf(&sb, "History (newest pages, %s):\n", scope)
		for _, ev := range events {
			when := "undated"
			if ev.Date != nil {
				when = ev.Date.UTC().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(&sb, "- %s %s %q download=%s\n", when, ev.EventType, ev.SourceTitle, ev.DownloadID)
		}
	}
	return &ToolResult{Text: sb.String()}, nil
}
