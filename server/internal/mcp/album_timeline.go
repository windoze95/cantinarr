package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/lidarr"
)

// get_album_timeline is the music sibling of the book timeline. It joins what
// the library HOLDS (the album's track files, with import dates) to what
// HAPPENED (the grab and import history, newest first, with download
// identities), so a wrong-album report is judged from receipts instead of a
// title string.
func (s *ToolServer) getAlbumTimeline(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	var params struct {
		MediaType  string `json:"media_type"`
		InstanceID string `json:"instance_id"`
		AlbumID    int    `json:"album_id"`
	}
	if err := json.Unmarshal(nonEmptyJSON(input), &params); err != nil {
		return nil, fmt.Errorf("invalid get_album_timeline input: %w", err)
	}
	if params.MediaType != "" && params.MediaType != "music" {
		return &ToolResult{Text: "An album timeline exists only for music; TV has get_episode_timeline and books get_book_timeline."}, nil
	}
	if params.AlbumID <= 0 {
		return &ToolResult{Text: "get_album_timeline needs the issue's album_id."}, nil
	}
	client, _, refusal := s.lidarrTargetFor(params.InstanceID, callInstanceID)
	if client == nil {
		return &ToolResult{Text: refusal}, nil
	}
	return s.getAlbumTimelineWithClient(client, input)
}

func (s *ToolServer) getAlbumTimelineWithClient(client *lidarr.Client, input json.RawMessage) (*ToolResult, error) {
	var params struct {
		MediaType string `json:"media_type"`
		AlbumID   int    `json:"album_id"`
	}
	if err := json.Unmarshal(nonEmptyJSON(input), &params); err != nil {
		return nil, fmt.Errorf("invalid get_album_timeline input: %w", err)
	}
	album, err := client.GetAlbum(params.AlbumID)
	if err != nil {
		return nil, fmt.Errorf("resolve album: %w", err)
	}
	if album == nil {
		return &ToolResult{Text: "This album record is not in the library. This read asked Lidarr for the exact record id."}, nil
	}

	var sb strings.Builder
	title := album.Title
	if album.Artist != nil && album.Artist.ArtistName != "" {
		title += " by " + album.Artist.ArtistName
	}
	fmt.Fprintf(&sb, "%s — timeline:\n", title)

	files, err := client.GetTrackFilesForAlbum(params.AlbumID)
	if err != nil {
		return nil, fmt.Errorf("read album track files: %w", err)
	}
	if len(files) == 0 {
		sb.WriteString("Files: NONE on disk right now. This read listed the album's own track-file rows; an empty answer here is genuine absence.\n")
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

	grabs, gerr := client.GetAlbumGrabs(params.AlbumID, 30)
	imports, ierr := client.GetImportHistory(params.AlbumID, "", 30)
	if gerr != nil && ierr != nil {
		sb.WriteString("History: UNREADABLE right now — this says nothing about whether events exist (blindness, not absence).\n")
		return &ToolResult{Text: sb.String()}, nil
	}
	// A one-sided failure must not be rendered as the whole history. The import
	// read errors BY DESIGN when the record holds more import events than one
	// page can prove complete; swallowing that would list grabs under a
	// "grabs + imports" heading, which reads as "no imports" and stops an
	// investigation cold.
	scope := "grabs + imports"
	if gerr != nil {
		scope = "imports only — GRAB history is UNREADABLE right now, so missing grabs are blindness, not absence"
	} else if ierr != nil {
		scope = "grabs only — IMPORT history is UNREADABLE right now, so missing imports are blindness, not absence"
	}
	events := append([]lidarr.HistoryRecord{}, grabs...)
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
