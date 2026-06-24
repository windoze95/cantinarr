package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// arrToolDefinitions are the Radarr/Sonarr management tools. They are appended
// to toolDefinitions in init below.
var arrToolDefinitions = []Tool{
	{
		Name:        "get_queue",
		Permission:  auth.PermissionArrRead,
		Description: "Get the current download queue from Radarr/Sonarr/Chaptarr with progress, time left, protocol, and any errors per item. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "all"},
					"description": "Which queue to fetch (default: all)",
				},
			},
		},
	},
	{
		Name:        "get_calendar",
		Permission:  auth.PermissionArrRead,
		Description: "Get upcoming movie releases and TV episode air dates, grouped by date. Books have no calendar in Chaptarr, so media_type=book is not supported. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "all"},
					"description": "Which calendar to fetch (default: all). Books have no calendar.",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"maximum":     60,
					"description": "How many days ahead to look (default: 14)",
				},
			},
		},
	},
	{
		Name:        "get_library",
		Permission:  auth.PermissionArrRead,
		Description: "Browse the Radarr/Sonarr/Chaptarr library. Filter for missing (monitored but not downloaded) or unmonitored items, optionally narrowed by a title query. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book"},
					"description": "Whether to list movies, TV series, or books",
				},
				"filter": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"all", "missing", "unmonitored"},
					"description": "Subset of the library to list (default: all)",
				},
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Optional case-insensitive title substring filter",
				},
			},
			"required": []string{"media_type"},
		},
	},
	{
		Name:        "get_history",
		Permission:  auth.PermissionArrRead,
		Description: "Get recent download activity (grabs, imports, failures) from Radarr/Sonarr/Chaptarr. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book"},
					"description": "Whether to fetch movie, TV, or book history",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Number of records to return (default: 20, max: 100)",
				},
			},
			"required": []string{"media_type"},
		},
	},
	{
		Name:        "trigger_search",
		Permission:  auth.PermissionArrSearch,
		Description: "Trigger an automatic indexer search for a movie, series, or book that is already in the library. For movies/TV pass tmdb_id (and, for TV, season_number to search a single season). For books pass book_id to search one book or author_id to search all of an author's monitored books (books have no tmdb_id). Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "Movie/TV only: the TMDB ID of the movie or TV show",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book"},
					"description": "Whether this is a movie, TV show, or book",
				},
				"season_number": map[string]interface{}{
					"type":        "integer",
					"description": "TV only: limit the search to this season",
				},
				"author_id": map[string]interface{}{
					"type":        "integer",
					"description": "Book only: search all monitored books of this Chaptarr author id (used when book_id is absent)",
				},
				"book_id": map[string]interface{}{
					"type":        "integer",
					"description": "Book only: search this single Chaptarr book id",
				},
			},
			"required": []string{"media_type"},
		},
	},
	{
		Name:        "search_releases",
		AdminOnly:   true,
		Permission:  auth.PermissionArrSearch,
		Description: "Interactively search indexers for downloadable releases of a library item and list them with the guid and indexer_id needed to grab one. For movies/TV pass tmdb_id (TV also requires season_number). For books pass book_id (books have no tmdb_id). Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "Movie/TV only: the TMDB ID of the movie or TV show",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book"},
					"description": "Whether this is a movie, TV show, or book",
				},
				"season_number": map[string]interface{}{
					"type":        "integer",
					"description": "TV only: the season to search releases for (required for tv)",
				},
				"book_id": map[string]interface{}{
					"type":        "integer",
					"description": "Book only: the Chaptarr book id to search releases for (required for book)",
				},
			},
			"required": []string{"media_type"},
		},
	},
	{
		Name:        "grab_release",
		AdminOnly:   true,
		Permission:  auth.PermissionDownloadsManage,
		Description: "Send a specific release from a previous search_releases call to the download client. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"guid": map[string]interface{}{
					"type":        "string",
					"description": "The guid of the release, from search_releases",
				},
				"indexer_id": map[string]interface{}{
					"type":        "integer",
					"description": "The indexer_id of the release, from search_releases",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book"},
					"description": "Whether the release is for a movie, TV show, or book",
				},
			},
			"required": []string{"guid", "indexer_id", "media_type"},
		},
	},
	{
		Name:        "remove_queue_item",
		AdminOnly:   true,
		Permission:  auth.PermissionDownloadsManage,
		Description: "Remove an item from the download queue (also removes the download from the client). Optionally blocklist the release so it is not grabbed again. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"queue_id": map[string]interface{}{
					"type":        "integer",
					"description": "The queue item id, from get_queue",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book"},
					"description": "Whether the queue item is a movie, TV, or book download",
				},
				"blocklist": map[string]interface{}{
					"type":        "boolean",
					"description": "Also blocklist the release (default: false)",
				},
			},
			"required": []string{"queue_id", "media_type"},
		},
	},
	{
		Name:        "get_disk_space",
		AdminOnly:   true,
		Permission:  auth.PermissionSystemRead,
		Description: "Get free and total disk space for the Radarr, Sonarr, and Chaptarr volumes. Admin only",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "get_arr_health",
		Permission:  auth.PermissionArrRead,
		Description: "Check Radarr/Sonarr/Chaptarr system health for config-level problems (download client unreachable, remote path mapping, indexers down, disk, no root folder). Use this when diagnose_queue shows path/permission/client errors to confirm the root cause that per-item queue diagnosis can only guess at. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "all"},
					"description": "Which service's health to fetch (default: all)",
				},
			},
		},
	},
	{
		Name:        "diagnose_queue",
		Permission:  auth.PermissionArrRead,
		Description: "Import Doctor: scan the Radarr/Sonarr/Chaptarr download queue for items that are stuck, failed, or blocked from importing, and explain each problem in plain language with the queue_id and suggested fix actions (process, manual_import, force_import, remove, blocklist_search, change_category, rescan). For each problem it also prints the exact next MCP tool call to run. Use this before the fix tools. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "all"},
					"description": "Which queue to diagnose (default: all)",
				},
			},
		},
	},
	{
		Name:        "get_manual_import_candidates",
		AdminOnly:   true,
		Permission:  auth.PermissionDownloadsManage,
		Description: "List the files Radarr/Sonarr/Chaptarr found for a stuck download (from its queue_id), including each file's mapped movie/series/episodes/book and any rejection reasons that blocked an automatic import. Use this to understand why an item won't import before calling execute_manual_import. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"queue_id": map[string]interface{}{
					"type":        "integer",
					"description": "The queue item id, from get_queue or diagnose_queue",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book"},
					"description": "Whether the queue item is a movie, TV, or book download",
				},
			},
			"required": []string{"queue_id", "media_type"},
		},
	},
	{
		Name:        "execute_manual_import",
		AdminOnly:   true,
		Permission:  auth.PermissionDownloadsManage,
		Description: "Force the files of a stuck download (from its queue_id) into the library via a manual import. By default skips candidates with permanent rejections; set force=true to import them anyway. Choose this when an item is blocked but the file is actually correct. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"queue_id": map[string]interface{}{
					"type":        "integer",
					"description": "The queue item id, from get_queue or diagnose_queue",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book"},
					"description": "Whether the queue item is a movie, TV, or book download",
				},
				"force": map[string]interface{}{
					"type":        "boolean",
					"description": "Import even candidates with permanent rejections (default: false)",
				},
			},
			"required": []string{"queue_id", "media_type"},
		},
	},
	{
		Name:        "remediate_queue_item",
		AdminOnly:   true,
		Permission:  auth.PermissionDownloadsManage,
		Description: "Apply a one-click fix to a stuck queue item: remove (delete it and the download), blocklist_search (remove, blocklist the release, and start a fresh search for a different one), or change_category (hand the download to the client's post-import category for tools like Unpackerr). Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"queue_id": map[string]interface{}{
					"type":        "integer",
					"description": "The queue item id, from get_queue or diagnose_queue",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book"},
					"description": "Whether the queue item is a movie, TV, or book download",
				},
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"remove", "blocklist_search", "change_category"},
					"description": "The remediation to apply",
				},
			},
			"required": []string{"queue_id", "media_type", "action"},
		},
	},
	{
		Name:        "rescan_media",
		AdminOnly:   true,
		Permission:  auth.PermissionArrSearch,
		Description: "Rescan the files on disk for a library movie, series, or author, then run the import pass. Use this after fixing a disk-space, path, or permissions problem so the service picks up files that are already there. For movies/TV pass tmdb_id; for books pass author_id (books have no tmdb_id). Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "Movie/TV only: the TMDB ID of the movie or TV show",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book"},
					"description": "Whether this is a movie, TV show, or book",
				},
				"author_id": map[string]interface{}{
					"type":        "integer",
					"description": "Book only: the Chaptarr author id to rescan (required for book)",
				},
			},
			"required": []string{"media_type"},
		},
	},
}

func init() {
	toolDefinitions = append(toolDefinitions, arrToolDefinitions...)
}

// --- helpers ---

func humanBytes(b float64) string {
	if b <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}

func normalizeMediaType(mediaType string) string {
	if mediaType == "" {
		return "all"
	}
	return mediaType
}

// findSeriesByTMDB resolves a Sonarr series from a TMDB ID: first via the
// TMDB->TVDB bridge, then by scanning the library for a tmdbId match.
// Returns (nil, nil) when the series is not in the library.
func (s *ToolServer) findSeriesByTMDB(client *sonarr.Client, tmdbID int) (*sonarr.Series, error) {
	return seriesByTMDB(s.bridge, client, tmdbID)
}

// --- get_queue ---

// maxQueueItems caps how many queue items are rendered per service.
const maxQueueItems = 30

// renderQueueSection renders up to maxQueueItems lines with a truncation
// notice when the queue is longer.
func renderQueueSection(label string, total int, lines []string) string {
	section := fmt.Sprintf("%s (%d items):\n%s", label, total, strings.Join(lines, "\n"))
	if total > len(lines) {
		section += fmt.Sprintf("\n…and %d more (%d total)", total-len(lines), total)
	}
	return section
}

func formatRadarrQueueItem(item radarr.DetailedQueueItem) string {
	title := item.Title
	if item.Movie != nil {
		title = fmt.Sprintf("%s (%d)", item.Movie.Title, item.Movie.Year)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "- [queue %d] %s — %s", item.ID, title, item.Status)
	if item.Size > 0 {
		fmt.Fprintf(&sb, ", %.1f%% done", (item.Size-item.Sizeleft)/item.Size*100)
	}
	if item.Timeleft != "" {
		fmt.Fprintf(&sb, ", %s left", item.Timeleft)
	}
	if item.Protocol != "" {
		fmt.Fprintf(&sb, ", %s", item.Protocol)
	}
	if item.DownloadClient != "" {
		fmt.Fprintf(&sb, " via %s", item.DownloadClient)
	}
	if item.TrackedDownloadStatus != "" && !strings.EqualFold(item.TrackedDownloadStatus, "ok") {
		fmt.Fprintf(&sb, " [%s/%s]", item.TrackedDownloadStatus, item.TrackedDownloadState)
	}
	if item.ErrorMessage != "" {
		fmt.Fprintf(&sb, "\n  error: %s", item.ErrorMessage)
	}
	for _, msg := range item.StatusMessages {
		if len(msg.Messages) > 0 {
			fmt.Fprintf(&sb, "\n  issue: %s", strings.Join(msg.Messages, "; "))
		}
	}
	return sb.String()
}

func formatSonarrQueueItem(item sonarr.DetailedQueueItem) string {
	title := item.Title
	if item.Series != nil {
		title = item.Series.Title
		if item.Episode != nil {
			title = fmt.Sprintf("%s S%02dE%02d", item.Series.Title, item.Episode.SeasonNumber, item.Episode.EpisodeNumber)
			if item.Episode.Title != "" {
				title += fmt.Sprintf(" %q", item.Episode.Title)
			}
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "- [queue %d] %s — %s", item.ID, title, item.Status)
	if item.Size > 0 {
		fmt.Fprintf(&sb, ", %.1f%% done", (item.Size-item.Sizeleft)/item.Size*100)
	}
	if item.Timeleft != "" {
		fmt.Fprintf(&sb, ", %s left", item.Timeleft)
	}
	if item.Protocol != "" {
		fmt.Fprintf(&sb, ", %s", item.Protocol)
	}
	if item.DownloadClient != "" {
		fmt.Fprintf(&sb, " via %s", item.DownloadClient)
	}
	if item.TrackedDownloadStatus != "" && !strings.EqualFold(item.TrackedDownloadStatus, "ok") {
		fmt.Fprintf(&sb, " [%s/%s]", item.TrackedDownloadStatus, item.TrackedDownloadState)
	}
	if item.ErrorMessage != "" {
		fmt.Fprintf(&sb, "\n  error: %s", item.ErrorMessage)
	}
	for _, msg := range item.StatusMessages {
		if len(msg.Messages) > 0 {
			fmt.Fprintf(&sb, "\n  issue: %s", strings.Join(msg.Messages, "; "))
		}
	}
	return sb.String()
}

func formatChaptarrQueueItem(item chaptarr.DetailedQueueItem) string {
	title := item.Title
	if item.Book != nil && item.Book.Title != "" {
		title = item.Book.Title
		if item.Author != nil && item.Author.AuthorName != "" {
			title = fmt.Sprintf("%s — %s", item.Author.AuthorName, item.Book.Title)
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "- [queue %d] %s — %s", item.ID, title, item.Status)
	if item.Size > 0 {
		fmt.Fprintf(&sb, ", %.1f%% done", (item.Size-item.Sizeleft)/item.Size*100)
	}
	if item.Timeleft != "" {
		fmt.Fprintf(&sb, ", %s left", item.Timeleft)
	}
	if item.Protocol != "" {
		fmt.Fprintf(&sb, ", %s", item.Protocol)
	}
	if item.DownloadClient != "" {
		fmt.Fprintf(&sb, " via %s", item.DownloadClient)
	}
	if item.TrackedDownloadStatus != "" && !strings.EqualFold(item.TrackedDownloadStatus, "ok") {
		fmt.Fprintf(&sb, " [%s/%s]", item.TrackedDownloadStatus, item.TrackedDownloadState)
	}
	if item.ErrorMessage != "" {
		fmt.Fprintf(&sb, "\n  error: %s", item.ErrorMessage)
	}
	for _, msg := range item.StatusMessages {
		if len(msg.Messages) > 0 {
			fmt.Fprintf(&sb, "\n  issue: %s", strings.Join(msg.Messages, "; "))
		}
	}
	return sb.String()
}

func (s *ToolServer) getQueue(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	mediaType := normalizeMediaType(params.MediaType)

	var sections []string

	if mediaType == "movie" || mediaType == "all" {
		radarrClient := s.GetRadarr()
		if radarrClient == nil {
			if mediaType == "movie" {
				return &ToolResult{Text: "Radarr is not configured."}, nil
			}
			sections = append(sections, "Radarr is not configured.")
		} else {
			items, err := radarrClient.GetQueueDetailed()
			if err != nil {
				return nil, err
			}
			if len(items) == 0 {
				sections = append(sections, "Movie queue: empty.")
			} else {
				shown := items
				if len(shown) > maxQueueItems {
					shown = shown[:maxQueueItems]
				}
				lines := make([]string, 0, len(shown))
				for _, item := range shown {
					lines = append(lines, formatRadarrQueueItem(item))
				}
				sections = append(sections, renderQueueSection("Movie queue", len(items), lines))
			}
		}
	}

	if mediaType == "tv" || mediaType == "all" {
		sonarrClient := s.GetSonarr()
		if sonarrClient == nil {
			if mediaType == "tv" {
				return &ToolResult{Text: "Sonarr is not configured."}, nil
			}
			sections = append(sections, "Sonarr is not configured.")
		} else {
			items, err := sonarrClient.GetQueueDetailed()
			if err != nil {
				return nil, err
			}
			if len(items) == 0 {
				sections = append(sections, "TV queue: empty.")
			} else {
				shown := items
				if len(shown) > maxQueueItems {
					shown = shown[:maxQueueItems]
				}
				lines := make([]string, 0, len(shown))
				for _, item := range shown {
					lines = append(lines, formatSonarrQueueItem(item))
				}
				sections = append(sections, renderQueueSection("TV queue", len(items), lines))
			}
		}
	}

	if mediaType == "book" || mediaType == "all" {
		chaptarrClient := s.GetChaptarr()
		if chaptarrClient == nil {
			if mediaType == "book" {
				return &ToolResult{Text: "Chaptarr is not configured."}, nil
			}
			sections = append(sections, "Chaptarr is not configured.")
		} else {
			items, err := chaptarrClient.GetQueueDetailed(chaptarrQueuePage, chaptarrQueuePageSize)
			if err != nil {
				return nil, err
			}
			if len(items) == 0 {
				sections = append(sections, "Book queue: empty.")
			} else {
				shown := items
				if len(shown) > maxQueueItems {
					shown = shown[:maxQueueItems]
				}
				lines := make([]string, 0, len(shown))
				for _, item := range shown {
					lines = append(lines, formatChaptarrQueueItem(item))
				}
				sections = append(sections, renderQueueSection("Book queue", len(items), lines))
			}
		}
	}

	return &ToolResult{Text: strings.Join(sections, "\n\n")}, nil
}

// --- get_calendar ---

type calendarEntry struct {
	date string
	line string
}

func (s *ToolServer) getCalendar(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		MediaType string `json:"media_type"`
		Days      int    `json:"days"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	mediaType := normalizeMediaType(params.MediaType)
	// Chaptarr has no calendar endpoint (books carry no air/release schedule the
	// way episodes and theatrical/digital releases do), so book is unsupported
	// here. Return a graceful explanation rather than an error.
	if mediaType == "book" {
		return &ToolResult{Text: "get_calendar is not supported for books — Chaptarr has no calendar. Use get_library (filter=missing) to see monitored books without a file."}, nil
	}
	days := params.Days
	if days < 1 {
		days = 14
	}
	if days > 60 {
		days = 60
	}
	start := time.Now()
	end := start.AddDate(0, 0, days)

	var entries []calendarEntry
	var notes []string
	if mediaType == "all" {
		// The combined view still skips books; note it so the omission is explicit.
		notes = append(notes, "Books have no calendar.")
	}

	if mediaType == "movie" || mediaType == "all" {
		radarrClient := s.GetRadarr()
		if radarrClient == nil {
			if mediaType == "movie" {
				return &ToolResult{Text: "Radarr is not configured."}, nil
			}
			notes = append(notes, "Radarr is not configured.")
		} else {
			items, err := radarrClient.GetCalendar(start, end)
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				for _, rel := range []struct {
					label string
					t     *time.Time
				}{
					{"in cinemas", item.InCinemas},
					{"digital release", item.DigitalRelease},
					{"physical release", item.PhysicalRelease},
				} {
					if rel.t == nil || rel.t.Before(start.Add(-24*time.Hour)) || rel.t.After(end) {
						continue
					}
					line := fmt.Sprintf("- [movie] %s (%d) — %s", item.Title, item.Year, rel.label)
					if item.HasFile {
						line += " (already downloaded)"
					}
					entries = append(entries, calendarEntry{date: rel.t.Format("2006-01-02"), line: line})
				}
			}
		}
	}

	if mediaType == "tv" || mediaType == "all" {
		sonarrClient := s.GetSonarr()
		if sonarrClient == nil {
			if mediaType == "tv" {
				return &ToolResult{Text: "Sonarr is not configured."}, nil
			}
			notes = append(notes, "Sonarr is not configured.")
		} else {
			items, err := sonarrClient.GetCalendar(start, end)
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				if item.AirDateUtc == nil {
					continue
				}
				seriesTitle := fmt.Sprintf("series %d", item.SeriesID)
				if item.Series != nil {
					seriesTitle = item.Series.Title
				}
				line := fmt.Sprintf("- [tv] %s S%02dE%02d", seriesTitle, item.SeasonNumber, item.EpisodeNumber)
				if item.Title != "" {
					line += fmt.Sprintf(" %q", item.Title)
				}
				line += fmt.Sprintf(" — airs %s UTC", item.AirDateUtc.UTC().Format("15:04"))
				if item.HasFile {
					line += " (already downloaded)"
				}
				entries = append(entries, calendarEntry{date: item.AirDateUtc.UTC().Format("2006-01-02"), line: line})
			}
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Upcoming releases over the next %d days:", days)
	if len(notes) > 0 {
		fmt.Fprintf(&sb, " (%s)", strings.Join(notes, " "))
	}
	if len(entries) == 0 {
		sb.WriteString("\nNothing scheduled in this window.")
		return &ToolResult{Text: sb.String()}, nil
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].date != entries[j].date {
			return entries[i].date < entries[j].date
		}
		return entries[i].line < entries[j].line
	})
	lastDate := ""
	for _, e := range entries {
		if e.date != lastDate {
			fmt.Fprintf(&sb, "\n\n%s:", e.date)
			lastDate = e.date
		}
		sb.WriteString("\n" + e.line)
	}
	return &ToolResult{Text: sb.String()}, nil
}

// --- get_library ---

const maxLibraryItems = 50

func (s *ToolServer) getLibrary(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		MediaType string `json:"media_type"`
		Filter    string `json:"filter"`
		Query     string `json:"query"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	filter := params.Filter
	if filter == "" {
		filter = "all"
	}
	query := strings.ToLower(strings.TrimSpace(params.Query))

	switch params.MediaType {
	case "movie":
		radarrClient := s.GetRadarr()
		if radarrClient == nil {
			return &ToolResult{Text: "Radarr is not configured."}, nil
		}
		movies, err := radarrClient.GetMovies()
		if err != nil {
			return nil, err
		}
		total := len(movies)
		var matched []radarr.Movie
		for _, m := range movies {
			switch filter {
			case "missing":
				if !m.Monitored || m.HasFile {
					continue
				}
			case "unmonitored":
				if m.Monitored {
					continue
				}
			}
			if query != "" && !strings.Contains(strings.ToLower(m.Title), query) {
				continue
			}
			matched = append(matched, m)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Movie library: %d total, %d matching (filter: %s", total, len(matched), filter)
		if query != "" {
			fmt.Fprintf(&sb, ", query: %q", params.Query)
		}
		sb.WriteString(")")
		shown := matched
		if len(shown) > maxLibraryItems {
			shown = shown[:maxLibraryItems]
			fmt.Fprintf(&sb, ", showing first %d of %d matches for filter %q", maxLibraryItems, len(matched), filter)
		}
		for _, m := range shown {
			status := "missing"
			if m.HasFile {
				status = "downloaded"
			} else if !m.Monitored {
				status = "unmonitored"
			}
			fmt.Fprintf(&sb, "\n- %s (%d) [ID %d, TMDB %d] — %s", m.Title, m.Year, m.ID, m.TmdbID, status)
		}
		return &ToolResult{Text: sb.String()}, nil

	case "tv":
		sonarrClient := s.GetSonarr()
		if sonarrClient == nil {
			return &ToolResult{Text: "Sonarr is not configured."}, nil
		}
		series, err := sonarrClient.GetAllSeries()
		if err != nil {
			return nil, err
		}
		total := len(series)
		var matched []sonarr.Series
		for _, sr := range series {
			switch filter {
			case "missing":
				if !sr.Monitored {
					continue
				}
				if sr.Statistics != nil && sr.Statistics.PercentOfEpisodes >= 100 {
					continue
				}
			case "unmonitored":
				if sr.Monitored {
					continue
				}
			}
			if query != "" && !strings.Contains(strings.ToLower(sr.Title), query) {
				continue
			}
			matched = append(matched, sr)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "TV library: %d total, %d matching (filter: %s", total, len(matched), filter)
		if query != "" {
			fmt.Fprintf(&sb, ", query: %q", params.Query)
		}
		sb.WriteString(")")
		shown := matched
		if len(shown) > maxLibraryItems {
			shown = shown[:maxLibraryItems]
			fmt.Fprintf(&sb, ", showing first %d of %d matches for filter %q", maxLibraryItems, len(matched), filter)
		}
		for _, sr := range shown {
			fmt.Fprintf(&sb, "\n- %s (%d) [ID %d, TVDB %d", sr.Title, sr.Year, sr.ID, sr.TvdbID)
			if sr.TmdbID != 0 {
				fmt.Fprintf(&sb, ", TMDB %d", sr.TmdbID)
			}
			sb.WriteString("]")
			if sr.Statistics != nil {
				fmt.Fprintf(&sb, " — %d/%d episodes", sr.Statistics.EpisodeFileCount, sr.Statistics.EpisodeCount)
			}
			if !sr.Monitored {
				sb.WriteString(" — unmonitored")
			}
		}
		return &ToolResult{Text: sb.String()}, nil

	case "book":
		chaptarrClient := s.GetChaptarr()
		if chaptarrClient == nil {
			return &ToolResult{Text: "Chaptarr is not configured."}, nil
		}
		authors, err := chaptarrClient.GetAllAuthors()
		if err != nil {
			return nil, err
		}
		total := len(authors)
		var matched []chaptarr.Author
		for _, a := range authors {
			switch filter {
			case "missing":
				if !a.Monitored {
					continue
				}
				if a.Statistics.PercentOfBooks >= 100 {
					continue
				}
			case "unmonitored":
				if a.Monitored {
					continue
				}
			}
			if query != "" && !strings.Contains(strings.ToLower(a.AuthorName), query) {
				continue
			}
			matched = append(matched, a)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Book library: %d author(s) total, %d matching (filter: %s", total, len(matched), filter)
		if query != "" {
			fmt.Fprintf(&sb, ", query: %q", params.Query)
		}
		sb.WriteString(")")
		shown := matched
		if len(shown) > maxLibraryItems {
			shown = shown[:maxLibraryItems]
			fmt.Fprintf(&sb, ", showing first %d of %d matches for filter %q", maxLibraryItems, len(matched), filter)
		}
		for _, a := range shown {
			fmt.Fprintf(&sb, "\n- %s [author ID %d]", a.AuthorName, a.ID)
			fmt.Fprintf(&sb, " — %d/%d books", a.Statistics.BookFileCount, a.Statistics.BookCount)
			if !a.Monitored {
				sb.WriteString(" — unmonitored")
			}
		}
		sb.WriteString("\n\nUse get_queue, search_releases (book_id), or trigger_search (author_id/book_id) for per-book actions; book ids come from the Chaptarr library, not this summary.")
		return &ToolResult{Text: sb.String()}, nil

	default:
		return &ToolResult{Text: "media_type must be \"movie\", \"tv\", or \"book\"."}, nil
	}
}

// --- get_history ---

func (s *ToolServer) getHistory(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		MediaType string `json:"media_type"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	limit := params.Limit
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	switch params.MediaType {
	case "movie":
		radarrClient := s.GetRadarr()
		if radarrClient == nil {
			return &ToolResult{Text: "Radarr is not configured."}, nil
		}
		records, err := radarrClient.GetHistory(limit)
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			return &ToolResult{Text: "No movie history found."}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Recent movie history (%d records):", len(records))
		for _, rec := range records {
			fmt.Fprintf(&sb, "\n- %s %s", rec.Date.UTC().Format("2006-01-02 15:04"), rec.EventType)
			if rec.Movie != nil {
				fmt.Fprintf(&sb, ": %s (%d)", rec.Movie.Title, rec.Movie.Year)
			}
			if rec.Quality.Quality.Name != "" {
				fmt.Fprintf(&sb, " [%s]", rec.Quality.Quality.Name)
			}
			if rec.SourceTitle != "" {
				fmt.Fprintf(&sb, " — %s", rec.SourceTitle)
			}
		}
		return &ToolResult{Text: sb.String()}, nil

	case "tv":
		sonarrClient := s.GetSonarr()
		if sonarrClient == nil {
			return &ToolResult{Text: "Sonarr is not configured."}, nil
		}
		records, err := sonarrClient.GetHistory(limit)
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			return &ToolResult{Text: "No TV history found."}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Recent TV history (%d records):", len(records))
		for _, rec := range records {
			fmt.Fprintf(&sb, "\n- %s %s", rec.Date.UTC().Format("2006-01-02 15:04"), rec.EventType)
			if rec.Series != nil {
				fmt.Fprintf(&sb, ": %s", rec.Series.Title)
				if rec.Episode != nil {
					fmt.Fprintf(&sb, " S%02dE%02d", rec.Episode.SeasonNumber, rec.Episode.EpisodeNumber)
				}
			}
			if rec.Quality.Quality.Name != "" {
				fmt.Fprintf(&sb, " [%s]", rec.Quality.Quality.Name)
			}
			if rec.SourceTitle != "" {
				fmt.Fprintf(&sb, " — %s", rec.SourceTitle)
			}
		}
		return &ToolResult{Text: sb.String()}, nil

	case "book":
		chaptarrClient := s.GetChaptarr()
		if chaptarrClient == nil {
			return &ToolResult{Text: "Chaptarr is not configured."}, nil
		}
		page, err := chaptarrClient.GetHistory(1, limit)
		if err != nil {
			return nil, err
		}
		if page == nil || len(page.Records) == 0 {
			return &ToolResult{Text: "No book history found."}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Recent book history (%d records):", len(page.Records))
		for _, rec := range page.Records {
			when := "unknown date"
			if rec.Date != nil {
				when = rec.Date.UTC().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(&sb, "\n- %s %s", when, rec.EventType)
			if rec.Author != nil && rec.Author.AuthorName != "" {
				fmt.Fprintf(&sb, ": %s", rec.Author.AuthorName)
				if rec.Book != nil && rec.Book.Title != "" {
					fmt.Fprintf(&sb, " — %s", rec.Book.Title)
				}
			} else if rec.Book != nil && rec.Book.Title != "" {
				fmt.Fprintf(&sb, ": %s", rec.Book.Title)
			}
			if rec.SourceTitle != "" {
				fmt.Fprintf(&sb, " — %s", rec.SourceTitle)
			}
		}
		return &ToolResult{Text: sb.String()}, nil

	default:
		return &ToolResult{Text: "media_type must be \"movie\", \"tv\", or \"book\"."}, nil
	}
}

// --- trigger_search ---

func (s *ToolServer) triggerSearch(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		TmdbID       int    `json:"tmdb_id"`
		MediaType    string `json:"media_type"`
		SeasonNumber *int   `json:"season_number"`
		AuthorID     int    `json:"author_id"`
		BookID       int    `json:"book_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	var bookIDs []int
	if params.BookID != 0 {
		bookIDs = []int{params.BookID}
	}
	text, err := TriggerSearchHelper(s.bridge, s.GetRadarr(), s.GetSonarr(), s.GetChaptarr(), params.MediaType, params.TmdbID, params.SeasonNumber, params.AuthorID, bookIDs)
	if err != nil {
		return nil, err
	}
	return &ToolResult{Text: text}, nil
}

// --- search_releases (admin) ---

const maxReleaseResults = 15

func formatRadarrReleases(releases []radarr.Release) string {
	sort.SliceStable(releases, func(i, j int) bool {
		if releases[i].Rejected != releases[j].Rejected {
			return !releases[i].Rejected
		}
		if releases[i].Seeders != releases[j].Seeders {
			return releases[i].Seeders > releases[j].Seeders
		}
		return releases[i].Size > releases[j].Size
	})
	if len(releases) > maxReleaseResults {
		releases = releases[:maxReleaseResults]
	}
	var sb strings.Builder
	for i, rel := range releases {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, rel.Title)
		fmt.Fprintf(&sb, "   quality: %s | size: %s | %s", rel.Quality.Quality.Name, humanBytes(float64(rel.Size)), rel.Protocol)
		if rel.Protocol == "torrent" {
			fmt.Fprintf(&sb, " (%d seeders / %d leechers)", rel.Seeders, rel.Leechers)
		}
		fmt.Fprintf(&sb, " | indexer: %s (indexer_id: %d) | age: %.1f days\n", rel.Indexer, rel.IndexerID, rel.AgeHours/24)
		if rel.Rejected {
			fmt.Fprintf(&sb, "   rejected: %s\n", strings.Join(rel.Rejections, "; "))
		}
		fmt.Fprintf(&sb, "   guid: %s\n", rel.GUID)
	}
	return sb.String()
}

func formatSonarrReleases(releases []sonarr.Release) string {
	sort.SliceStable(releases, func(i, j int) bool {
		if releases[i].Rejected != releases[j].Rejected {
			return !releases[i].Rejected
		}
		if releases[i].Seeders != releases[j].Seeders {
			return releases[i].Seeders > releases[j].Seeders
		}
		return releases[i].Size > releases[j].Size
	})
	if len(releases) > maxReleaseResults {
		releases = releases[:maxReleaseResults]
	}
	var sb strings.Builder
	for i, rel := range releases {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, rel.Title)
		fmt.Fprintf(&sb, "   quality: %s | size: %s | %s", rel.Quality.Quality.Name, humanBytes(float64(rel.Size)), rel.Protocol)
		if rel.Protocol == "torrent" {
			fmt.Fprintf(&sb, " (%d seeders / %d leechers)", rel.Seeders, rel.Leechers)
		}
		fmt.Fprintf(&sb, " | indexer: %s (indexer_id: %d) | age: %.1f days\n", rel.Indexer, rel.IndexerID, rel.AgeHours/24)
		if rel.Rejected {
			fmt.Fprintf(&sb, "   rejected: %s\n", strings.Join(rel.Rejections, "; "))
		}
		fmt.Fprintf(&sb, "   guid: %s\n", rel.GUID)
	}
	return sb.String()
}

// chaptarrSeeders dereferences a Chaptarr release's optional seeder/leecher
// count (the API omits them for usenet), returning 0 when absent.
func chaptarrSeeders(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

func formatChaptarrReleases(releases []chaptarr.Release) string {
	sort.SliceStable(releases, func(i, j int) bool {
		if releases[i].Rejected != releases[j].Rejected {
			return !releases[i].Rejected
		}
		si, sj := chaptarrSeeders(releases[i].Seeders), chaptarrSeeders(releases[j].Seeders)
		if si != sj {
			return si > sj
		}
		return releases[i].Size > releases[j].Size
	})
	if len(releases) > maxReleaseResults {
		releases = releases[:maxReleaseResults]
	}
	var sb strings.Builder
	for i, rel := range releases {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, rel.Title)
		fmt.Fprintf(&sb, "   size: %s | %s", humanBytes(float64(rel.Size)), rel.Protocol)
		if rel.Protocol == "torrent" {
			fmt.Fprintf(&sb, " (%d seeders / %d leechers)", chaptarrSeeders(rel.Seeders), chaptarrSeeders(rel.Leechers))
		}
		fmt.Fprintf(&sb, " | indexer: %s (indexer_id: %d) | age: %.1f days\n", rel.Indexer, rel.IndexerID, rel.AgeHours/24)
		if rel.Rejected {
			fmt.Fprintf(&sb, "   rejected: %s\n", strings.Join(rel.Rejections, "; "))
		}
		fmt.Fprintf(&sb, "   guid: %s\n", rel.GUID)
	}
	return sb.String()
}

func (s *ToolServer) searchReleases(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		TmdbID       int    `json:"tmdb_id"`
		MediaType    string `json:"media_type"`
		SeasonNumber *int   `json:"season_number"`
		BookID       int    `json:"book_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	switch params.MediaType {
	case "movie":
		radarrClient := s.GetRadarr()
		if radarrClient == nil {
			return &ToolResult{Text: "Radarr is not configured."}, nil
		}
		movie, err := radarrClient.GetMovieByTMDB(params.TmdbID)
		if err != nil {
			return nil, err
		}
		if movie == nil {
			return &ToolResult{Text: "This movie is not in the library yet. Use request_media to add it first."}, nil
		}
		releases, err := radarrClient.SearchReleases(movie.ID)
		if err != nil {
			return nil, err
		}
		if len(releases) == 0 {
			return &ToolResult{Text: fmt.Sprintf("No releases found for %s (%d).", movie.Title, movie.Year)}, nil
		}
		header := fmt.Sprintf("Found %d release(s) for %s (%d), showing top %d. Use grab_release with a guid and indexer_id to download one.\n",
			len(releases), movie.Title, movie.Year, min(len(releases), maxReleaseResults))
		return &ToolResult{Text: header + formatRadarrReleases(releases)}, nil

	case "tv":
		sonarrClient := s.GetSonarr()
		if sonarrClient == nil {
			return &ToolResult{Text: "Sonarr is not configured."}, nil
		}
		if params.SeasonNumber == nil {
			return &ToolResult{Text: "season_number is required when searching TV releases."}, nil
		}
		series, err := s.findSeriesByTMDB(sonarrClient, params.TmdbID)
		if err != nil {
			return nil, err
		}
		if series == nil {
			return &ToolResult{Text: "This show is not in the library yet. Use request_media to add it first."}, nil
		}
		releases, err := sonarrClient.SearchReleases(series.ID, *params.SeasonNumber)
		if err != nil {
			return nil, err
		}
		if len(releases) == 0 {
			return &ToolResult{Text: fmt.Sprintf("No releases found for %s season %d.", series.Title, *params.SeasonNumber)}, nil
		}
		header := fmt.Sprintf("Found %d release(s) for %s season %d, showing top %d. Use grab_release with a guid and indexer_id to download one.\n",
			len(releases), series.Title, *params.SeasonNumber, min(len(releases), maxReleaseResults))
		return &ToolResult{Text: header + formatSonarrReleases(releases)}, nil

	case "book":
		chaptarrClient := s.GetChaptarr()
		if chaptarrClient == nil {
			return &ToolResult{Text: "Chaptarr is not configured."}, nil
		}
		if params.BookID == 0 {
			return &ToolResult{Text: "book_id is required when searching book releases."}, nil
		}
		releases, err := chaptarrClient.SearchReleases(params.BookID)
		if err != nil {
			return nil, err
		}
		if len(releases) == 0 {
			return &ToolResult{Text: fmt.Sprintf("No releases found for book id %d.", params.BookID)}, nil
		}
		header := fmt.Sprintf("Found %d release(s) for book id %d, showing top %d. Use grab_release with a guid and indexer_id to download one.\n",
			len(releases), params.BookID, min(len(releases), maxReleaseResults))
		return &ToolResult{Text: header + formatChaptarrReleases(releases)}, nil

	default:
		return &ToolResult{Text: "media_type must be \"movie\", \"tv\", or \"book\"."}, nil
	}
}

// --- grab_release (admin) ---

func (s *ToolServer) grabRelease(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		GUID      string `json:"guid"`
		IndexerID int    `json:"indexer_id"`
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	text, err := GrabReleaseHelper(s.GetRadarr(), s.GetSonarr(), s.GetChaptarr(), params.MediaType, params.GUID, params.IndexerID, 0)
	if err != nil {
		return nil, err
	}
	return &ToolResult{Text: text}, nil
}

// --- remove_queue_item (admin) ---

func (s *ToolServer) removeQueueItem(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		QueueID   int    `json:"queue_id"`
		MediaType string `json:"media_type"`
		Blocklist bool   `json:"blocklist"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	text, err := RemoveQueueItemHelper(s.GetRadarr(), s.GetSonarr(), s.GetChaptarr(), params.MediaType, params.QueueID, params.Blocklist)
	if err != nil {
		return nil, err
	}
	return &ToolResult{Text: text}, nil
}

// --- get_disk_space (admin) ---

func formatDiskLines(sb *strings.Builder, path, label string, free, total int64) {
	name := path
	if name == "" {
		name = label
	}
	pct := 0.0
	if total > 0 {
		pct = float64(free) / float64(total) * 100
	}
	fmt.Fprintf(sb, "\n- %s: %s free of %s (%.0f%% free)", name, humanBytes(float64(free)), humanBytes(float64(total)), pct)
}

func (s *ToolServer) getDiskSpace() (*ToolResult, error) {
	radarrClient := s.GetRadarr()
	sonarrClient := s.GetSonarr()
	chaptarrClient := s.GetChaptarr()
	if radarrClient == nil && sonarrClient == nil && chaptarrClient == nil {
		return &ToolResult{Text: "Radarr/Sonarr/Chaptarr is not configured."}, nil
	}

	var sb strings.Builder
	if radarrClient != nil {
		disks, err := radarrClient.GetDiskSpace()
		if err != nil {
			return nil, err
		}
		sb.WriteString("Radarr disk space:")
		for _, d := range disks {
			formatDiskLines(&sb, d.Path, d.Label, d.FreeSpace, d.TotalSpace)
		}
	} else {
		sb.WriteString("Radarr is not configured.")
	}

	sb.WriteString("\n\n")

	if sonarrClient != nil {
		disks, err := sonarrClient.GetDiskSpace()
		if err != nil {
			return nil, err
		}
		sb.WriteString("Sonarr disk space:")
		for _, d := range disks {
			formatDiskLines(&sb, d.Path, d.Label, d.FreeSpace, d.TotalSpace)
		}
	} else {
		sb.WriteString("Sonarr is not configured.")
	}

	sb.WriteString("\n\n")

	if chaptarrClient != nil {
		disks, err := chaptarrClient.GetDiskSpace()
		if err != nil {
			return nil, err
		}
		sb.WriteString("Chaptarr disk space:")
		for _, d := range disks {
			formatDiskLines(&sb, d.Path, d.Label, d.FreeSpace, d.TotalSpace)
		}
	} else {
		sb.WriteString("Chaptarr is not configured.")
	}

	return &ToolResult{Text: sb.String()}, nil
}
