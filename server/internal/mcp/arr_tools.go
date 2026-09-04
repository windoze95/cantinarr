package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/lidarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// arrToolDefinitions are the Radarr/Sonarr management tools. They are appended
// to toolDefinitions in init below.
var arrToolDefinitions = []Tool{
	{
		Name:        "get_queue",
		Permission:  auth.PermissionArrRead,
		Description: "Get the current download queue from Radarr/Sonarr/Chaptarr/Lidarr with progress, time left, protocol, and any errors per item. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music", "all"},
					"description": "Which queue to fetch (default: all)",
				},
			},
		},
	},
	{
		Name:        "get_calendar",
		Permission:  auth.PermissionArrRead,
		Description: "Get upcoming movie releases, TV episode air dates, and album release dates, grouped by date. Books have no calendar in Chaptarr, so media_type=book is not supported. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music", "all"},
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
		Description: "Browse the Radarr/Sonarr/Chaptarr/Lidarr library. For music, pass artist_id to list one artist's albums with their album ids, or album_id for one exact album. Filter for missing (monitored but not downloaded) or unmonitored items, optionally narrowed by a title query. For books, pass author_id to list one author's books with their book ids (the ids search_releases/trigger_search need), or book_id for one exact book. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music"},
					"description": "Whether to list movies, TV series, books, or music",
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
				"author_id": map[string]interface{}{
					"type":        "integer",
					"description": "Book only: list this Chaptarr author's books with their book ids",
				},
				"book_id": map[string]interface{}{
					"type":        "integer",
					"description": "Book only: show this exact Chaptarr book record",
				},
				"artist_id": map[string]interface{}{
					"type":        "integer",
					"description": "Music only: list this Lidarr artist's albums with their album ids",
				},
				"album_id": map[string]interface{}{
					"type":        "integer",
					"description": "Music only: show this exact Lidarr album record",
				},
			},
			"required": []string{"media_type"},
		},
	},
	{
		Name:        "get_history",
		Permission:  auth.PermissionArrRead,
		Description: "Get recent download activity (grabs, imports, failures) from Radarr/Sonarr/Chaptarr/Lidarr. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music"},
					"description": "Whether to fetch movie, TV, book, or music history",
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
		Description: "Trigger an automatic indexer search for a movie, series, book, or album that is already in the library. For movies/TV pass tmdb_id (and, for TV, season_number to search a single season). For books pass book_id to search one book or author_id to search all of an author's monitored books (books have no tmdb_id). For music pass album_id to search one album or artist_id to search all of an artist's monitored albums (music has no tmdb_id). Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "Movie/TV only: the TMDB ID of the movie or TV show",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music"},
					"description": "Whether this is a movie, TV show, book, or album",
				},
				"season_number": map[string]interface{}{
					"type":        "integer",
					"description": "TV only: limit the search to this season",
				},
				"episode_number": map[string]interface{}{
					"type":        "integer",
					"description": "TV only: limit the search to this episode (requires season_number)",
				},
				"aired_only": map[string]interface{}{
					"type":        "boolean",
					"description": "TV season search only: search just the episodes that have already aired and are missing a file, leaving the rest of the season for the service to grab as it airs",
				},
				"author_id": map[string]interface{}{
					"type":        "integer",
					"description": "Book only: search all monitored books of this Chaptarr author id (used when book_id is absent)",
				},
				"book_id": map[string]interface{}{
					"type":        "integer",
					"description": "Book only: search this single Chaptarr book id",
				},
				"artist_id": map[string]interface{}{
					"type":        "integer",
					"description": "Music only: search all monitored albums of this Lidarr artist id (used when album_id is absent)",
				},
				"album_id": map[string]interface{}{
					"type":        "integer",
					"description": "Music only: search this single Lidarr album id",
				},
			},
			"required": []string{"media_type"},
		},
	},
	{
		Name:        "search_releases",
		AdminOnly:   true,
		Permission:  auth.PermissionArrSearch,
		Description: "Interactively search indexers for downloadable releases of a library item and list them with a one-way release reference and indexer_id. Raw release GUID capabilities are never exposed. For movies/TV pass tmdb_id (TV also requires season_number and may include episode_number). For books pass book_id and for music pass album_id (neither has a tmdb_id). Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "Movie/TV only: the TMDB ID of the movie or TV show",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music"},
					"description": "Whether this is a movie, TV show, book, or album",
				},
				"season_number": map[string]interface{}{
					"type":        "integer",
					"description": "TV only: the season to search releases for (required for tv)",
				},
				"episode_number": map[string]interface{}{
					"type":        "integer",
					"description": "TV only: the episode to search releases for (requires season_number)",
				},
				"book_id": map[string]interface{}{
					"type":        "integer",
					"description": "Book only: the Chaptarr book id to search releases for (required for book)",
				},
				"album_id": map[string]interface{}{
					"type":        "integer",
					"description": "Music only: the Lidarr album id to search releases for (required for music)",
				},
			},
			"required": []string{"media_type"},
		},
	},
	{
		Name:        "grab_release",
		AdminOnly:   true,
		Permission:  auth.PermissionDownloadsManage,
		Description: "Freshly re-search the exact movie, TV season/episode, book, or album scope and send the release matching a one-way reference from search_releases to the download client. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id — the same library search_releases searched; omit for the default library",
				},
				"guid": map[string]interface{}{
					"type":        "string",
					"pattern":     `^\[REDACTED release sha256:[0-9a-f]{16}\]$`,
					"description": "The exact one-way release reference from search_releases (raw GUIDs are rejected)",
				},
				"indexer_id": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"description": "The indexer_id of the release, from search_releases",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music"},
					"description": "Whether the release is for a movie, TV show, book, or album",
				},
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"description": "Required for movie/TV: the exact TMDB id used for the fresh scoped search",
				},
				"season_number": map[string]interface{}{
					"type":        "integer",
					"minimum":     0,
					"description": "Required for TV: the exact season used for the fresh scoped search (0 is Specials)",
				},
				"episode_number": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"description": "TV only: the exact episode used for the fresh scoped search",
				},
				"book_id": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"description": "Required for book: the exact Chaptarr book id used for the fresh scoped search",
				},
				"album_id": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"description": "Required for music: the exact Lidarr album id used for the fresh scoped search",
				},
			},
			"required": []string{"guid", "indexer_id", "media_type"},
			"oneOf": []interface{}{
				map[string]interface{}{
					"properties": map[string]interface{}{"media_type": map[string]interface{}{"const": "movie"}},
					"required":   []string{"tmdb_id"},
				},
				map[string]interface{}{
					"properties": map[string]interface{}{"media_type": map[string]interface{}{"const": "tv"}},
					"required":   []string{"tmdb_id", "season_number"},
				},
				map[string]interface{}{
					"properties": map[string]interface{}{"media_type": map[string]interface{}{"const": "book"}},
					"required":   []string{"book_id"},
				},
				map[string]interface{}{
					"properties": map[string]interface{}{"media_type": map[string]interface{}{"const": "music"}},
					"required":   []string{"album_id"},
				},
			},
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
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"queue_id": map[string]interface{}{
					"type":        "integer",
					"description": "The queue item id, from get_queue",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music"},
					"description": "Whether the queue item is a movie, TV, book, or music download",
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
		Description: "Get free and total disk space for the Radarr, Sonarr, Chaptarr, and Lidarr volumes. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Limit to one library instance id, from list_arr_instances; omit for every configured library",
				},
			},
		},
	},
	{
		Name:        "get_arr_health",
		Permission:  auth.PermissionArrRead,
		Description: "Check Radarr/Sonarr/Chaptarr/Lidarr system health for config-level problems (download client unreachable, remote path mapping, indexers down, disk, no root folder). Use this when diagnose_queue shows path/permission/client errors to confirm the root cause that per-item queue diagnosis can only guess at. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music", "all"},
					"description": "Which service's health to fetch (default: all)",
				},
			},
		},
	},
	{
		Name:        "get_episode_timeline",
		Permission:  auth.PermissionArrRead,
		Description: "Show one TV season episode by episode: air date, whether it has aired yet, and the file the library holds for it (release name, quality, and when it was imported). Flags files the service imported BEFORE that episode aired — content that cannot be what it claims to be — and lists the aired episodes that are missing. Use this for any \"wrong episode\", \"wrong season\", or \"this isn't what I asked for\" report, and after a fix to confirm the season is clean. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"tv"},
					"description": "Only TV has an episode timeline",
				},
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "The series' TMDB id, from get_library",
				},
				"season_number": map[string]interface{}{
					"type":        "integer",
					"description": "Which season to lay out; omit for a per-season rollup of the whole series",
				},
			},
			"required": []string{"media_type"},
		},
	},
	{
		Name:        "get_media_file_details",
		Permission:  auth.PermissionArrRead,
		Description: "Inspect the file(s) the library actually holds for one movie, one TV season, or one album: resolution, video codec and dynamic range, audio codec/channels/languages, embedded subtitles, runtime, size, quality label, scene name, and import date — the arr's own analysis of what is on disk (for music: Lidarr's codec, bit depth, sample rate, channels, and bitrate per track file). Use this for any \"wrong audio\", \"no subtitles\", \"bad quality\", or \"upscaled\" report: it is the difference between judging a release NAME and judging the file. A file the arr has not analyzed yet says so explicitly (that is blindness, not absence). Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "music"},
					"description": "Movie, TV, or music; book files carry no media-property analysis in Chaptarr",
				},
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "Movie/TV: the title's TMDB id, from get_library (required for movie and tv)",
				},
				"season_number": map[string]interface{}{
					"type":        "integer",
					"description": "TV: which season's files to inspect",
				},
				"album_id": map[string]interface{}{
					"type":        "integer",
					"description": "Music: the Lidarr album id whose track files to inspect, from get_library (required for music; music has no tmdb_id)",
				},
			},
			// tmdb_id is required for movie/tv and album_id for music; that is
			// enforced server-side rather than with a root oneOf, which the
			// Anthropic converter would have to strip anyway (#497).
			"required": []string{"media_type"},
		},
	},
	{
		Name:        "get_service_config",
		Permission:  auth.PermissionArrRead,
		Description: "Read-only summary of one settings section on Radarr, Sonarr, Chaptarr, or Lidarr: indexers (protocol, rss/auto-search, priority, min seeders), delay_profiles, release_profiles (Sonarr/Chaptarr/Lidarr only), download_clients (protocol, enabled, category), or remote_path_mappings. These are the settings recurring problems trace back to; values are bounded summaries and credentials/URLs are never included. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"service": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"radarr", "sonarr", "chaptarr", "lidarr"},
					"description": "Which service to read",
				},
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact instance; omit for the default",
				},
				"section": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"indexers", "delay_profiles", "release_profiles", "download_clients", "remote_path_mappings"},
					"description": "Which settings section to summarize",
				},
			},
			"required": []string{"service", "section"},
		},
	},
	{
		Name:        "get_book_timeline",
		Permission:  auth.PermissionArrRead,
		Description: "Join what the library HOLDS for one book (its files, with import dates) to what HAPPENED (grab and import history with download identities, newest first). Use this for any \"wrong book\", \"wrong edition\", or \"bad copy\" book report — it is the receipts, where a title string proves nothing — and after a fix to confirm the record is clean. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"book"},
					"description": "Only books have a book timeline",
				},
				"book_id": map[string]interface{}{
					"type":        "integer",
					"description": "The issue's durable Chaptarr book record id",
				},
			},
			"required": []string{"media_type", "book_id"},
		},
	},
	{
		Name:        "get_album_timeline",
		Permission:  auth.PermissionArrRead,
		Description: "Join what the library HOLDS for one album (its track files, with import dates) to what HAPPENED (grab and import history with download identities, newest first). Use this for any \"wrong album\", \"wrong release\", or \"bad copy\" music report — it is the receipts, where a title string proves nothing — and after a fix to confirm the record is clean. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"music"},
					"description": "Only music has an album timeline",
				},
				"album_id": map[string]interface{}{
					"type":        "integer",
					"description": "The issue's durable Lidarr album record id",
				},
			},
			"required": []string{"media_type", "album_id"},
		},
	},
	{
		Name:        "diagnose_queue",
		Permission:  auth.PermissionArrRead,
		Description: "Import Doctor: scan the Radarr/Sonarr/Chaptarr/Lidarr download queue for items that are stuck, failed, or blocked from importing, and explain each problem in plain language with the queue_id and suggested fix actions (process, manual_import, force_import, remove, blocklist_search, blocklist_only, change_category, rescan). For each problem it also prints the exact next MCP tool call to run. Use this before the fix tools. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music", "all"},
					"description": "Which queue to diagnose (default: all)",
				},
			},
		},
	},
	{
		Name:        "get_manual_import_candidates",
		AdminOnly:   true,
		Permission:  auth.PermissionDownloadsManage,
		Description: "List the files Radarr/Sonarr/Chaptarr/Lidarr found for a stuck download (from its queue_id), including each file's mapped movie/series/episodes/book/album tracks and any rejection reasons that blocked an automatic import. Use this to understand why an item won't import before calling execute_manual_import. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"queue_id": map[string]interface{}{
					"type":        "integer",
					"description": "The queue item id, from get_queue or diagnose_queue",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music"},
					"description": "Whether the queue item is a movie, TV, book, or music download",
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
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"queue_id": map[string]interface{}{
					"type":        "integer",
					"description": "The queue item id, from get_queue or diagnose_queue",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music"},
					"description": "Whether the queue item is a movie, TV, book, or music download",
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
		Description: "Apply a one-click fix to a stuck queue item: remove (delete it and the download), blocklist_search (remove and blocklist the release, leaving the replacement to the service's own failed-download settings), blocklist_only (remove and blocklist, and suppress the service's replacement search too — the right choice when the library already holds a copy AND nobody asked for this download), or change_category (hand the download to the client's post-import category for tools like Unpackerr). Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"queue_id": map[string]interface{}{
					"type":        "integer",
					"description": "The queue item id, from get_queue or diagnose_queue",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music"},
					"description": "Whether the queue item is a movie, TV, book, or music download",
				},
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"remove", "blocklist_search", "blocklist_only", "change_category"},
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
		Description: "Rescan the files on disk for a library movie, series, author, or artist, then run the import pass. Use this after fixing a disk-space, path, or permissions problem so the service picks up files that are already there. For movies/TV pass tmdb_id; for books pass author_id and for music pass artist_id (neither has a tmdb_id). Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact library instance id, from list_arr_instances; omit for the default library",
				},
				"tmdb_id": map[string]interface{}{
					"type":        "integer",
					"description": "Movie/TV only: the TMDB ID of the movie or TV show",
				},
				"media_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"movie", "tv", "book", "music"},
					"description": "Whether this is a movie, TV show, book, or album",
				},
				"author_id": map[string]interface{}{
					"type":        "integer",
					"description": "Book only: the Chaptarr author id to rescan (required for book)",
				},
				"artist_id": map[string]interface{}{
					"type":        "integer",
					"description": "Music only: the Lidarr artist id to rescan (required for music)",
				},
			},
			"required": []string{"media_type"},
		},
	},
}

func init() {
	toolDefinitions = append(toolDefinitions, arrToolDefinitions...)
	// Appended here rather than in an init of their own file so tool ordering
	// never depends on compilation file order.
	toolDefinitions = append(toolDefinitions, arrSettingsToolDefinitions...)
	toolDefinitions = append(toolDefinitions, arrProfileToolDefinitions...)
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

func formatLidarrQueueItem(item lidarr.DetailedQueueItem) string {
	title := item.Title
	if item.Album != nil && item.Album.Title != "" {
		title = item.Album.Title
		if item.Artist != nil && item.Artist.ArtistName != "" {
			title = fmt.Sprintf("%s — %s", item.Artist.ArtistName, item.Album.Title)
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

func (s *ToolServer) getQueue(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	var params struct {
		MediaType     string `json:"media_type"`
		InstanceID    string `json:"instance_id"`
		QueueID       int    `json:"queue_id"`
		DownloadID    string `json:"download_id"`
		TmdbID        int    `json:"tmdb_id"`
		TvdbID        int    `json:"tvdb_id"`
		SeasonNumber  int    `json:"season_number"`
		EpisodeNumber int    `json:"episode_number"`
		AuthorID      int    `json:"author_id"`
		BookID        int    `json:"book_id"`
		ArtistID      int    `json:"artist_id"`
		AlbumID       int    `json:"album_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	mediaType := normalizeMediaType(params.MediaType)
	scope := mediaReadScope{
		QueueID: params.QueueID, DownloadID: params.DownloadID, TmdbID: params.TmdbID, TvdbID: params.TvdbID,
		SeasonNumber: params.SeasonNumber, EpisodeNumber: params.EpisodeNumber,
		AuthorID: params.AuthorID, BookID: params.BookID,
		ArtistID: params.ArtistID, AlbumID: params.AlbumID,
	}

	var sections []string
	matchedTargets := 0
	exactQueueScope := mediaType != "all" && (params.QueueID > 0 || params.DownloadID != "")

	if mediaType == "movie" || mediaType == "all" {
		radarrClient, radarrLabel, refusal := s.radarrTargetFor(params.InstanceID, callInstanceID)
		if radarrClient == nil {
			if mediaType == "movie" {
				return &ToolResult{Text: refusal}, nil
			}
			sections = append(sections, refusal)
		} else {
			items, err := radarrClient.GetQueueDetailed()
			if err != nil {
				return nil, err
			}
			items, err = filterRadarrQueue(radarrClient, items, scope)
			if err != nil {
				return nil, err
			}
			if len(items) == 0 {
				sections = append(sections, emptyQueueText("Movie queue on "+radarrLabel, scope))
			} else {
				matchedTargets += len(items)
				shown := items
				if len(shown) > maxQueueItems {
					shown = shown[:maxQueueItems]
				}
				lines := make([]string, 0, len(shown))
				for _, item := range shown {
					lines = append(lines, formatRadarrQueueItem(item))
				}
				sections = append(sections, renderQueueSection("Movie queue on "+radarrLabel, len(items), lines))
			}
		}
	}

	if mediaType == "tv" || mediaType == "all" {
		sonarrClient, sonarrLabel, refusal := s.sonarrTargetFor(params.InstanceID, callInstanceID)
		if sonarrClient == nil {
			if mediaType == "tv" {
				return &ToolResult{Text: refusal}, nil
			}
			sections = append(sections, refusal)
		} else {
			items, err := sonarrClient.GetQueueDetailed()
			if err != nil {
				return nil, err
			}
			items, err = filterSonarrQueue(sonarrClient, items, scope)
			if err != nil {
				return nil, err
			}
			if len(items) == 0 {
				sections = append(sections, emptyQueueText("TV queue on "+sonarrLabel, scope))
			} else {
				matchedTargets += len(items)
				shown := items
				if len(shown) > maxQueueItems {
					shown = shown[:maxQueueItems]
				}
				lines := make([]string, 0, len(shown))
				for _, item := range shown {
					lines = append(lines, formatSonarrQueueItem(item))
				}
				sections = append(sections, renderQueueSection("TV queue on "+sonarrLabel, len(items), lines))
			}
		}
	}

	if mediaType == "book" || mediaType == "all" {
		chaptarrClient, chaptarrLabel, refusal := s.chaptarrTargetFor(params.InstanceID, callInstanceID)
		if chaptarrClient == nil {
			if mediaType == "book" {
				return &ToolResult{Text: refusal}, nil
			}
			sections = append(sections, refusal)
		} else {
			items, err := chaptarrClient.GetQueueDetailed()
			if err != nil {
				return nil, err
			}
			items = filterChaptarrQueue(items, scope)
			if len(items) == 0 {
				sections = append(sections, emptyQueueText("Book queue on "+chaptarrLabel, scope))
			} else {
				matchedTargets += len(items)
				shown := items
				if len(shown) > maxQueueItems {
					shown = shown[:maxQueueItems]
				}
				lines := make([]string, 0, len(shown))
				for _, item := range shown {
					lines = append(lines, formatChaptarrQueueItem(item))
				}
				sections = append(sections, renderQueueSection("Book queue on "+chaptarrLabel, len(items), lines))
			}
		}
	}

	if mediaType == "music" || mediaType == "all" {
		lidarrClient, lidarrLabel, refusal := s.lidarrTargetFor(params.InstanceID, callInstanceID)
		if lidarrClient == nil {
			if mediaType == "music" {
				return &ToolResult{Text: refusal}, nil
			}
			sections = append(sections, refusal)
		} else {
			items, err := lidarrClient.GetQueueDetailed()
			if err != nil {
				return nil, err
			}
			items = filterLidarrQueue(items, scope)
			if len(items) == 0 {
				sections = append(sections, emptyQueueText("Music queue on "+lidarrLabel, scope))
			} else {
				matchedTargets += len(items)
				shown := items
				if len(shown) > maxQueueItems {
					shown = shown[:maxQueueItems]
				}
				lines := make([]string, 0, len(shown))
				for _, item := range shown {
					lines = append(lines, formatLidarrQueueItem(item))
				}
				sections = append(sections, renderQueueSection("Music queue on "+lidarrLabel, len(items), lines))
			}
		}
	}

	result := &ToolResult{Text: strings.Join(sections, "\n\n")}
	result.Verification = queueTargetVerification(exactQueueScope, matchedTargets)
	return result, nil
}

func queueTargetVerification(exactScope bool, matchedTargets int) *ToolVerification {
	if !exactScope {
		return nil
	}
	return &ToolVerification{
		Kind:          VerificationQueueTarget,
		ExactScope:    true,
		TargetPresent: matchedTargets > 0,
	}
}

// --- get_calendar ---

type calendarEntry struct {
	date string
	line string
}

func (s *ToolServer) getCalendar(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	var params struct {
		MediaType  string `json:"media_type"`
		InstanceID string `json:"instance_id"`
		Days       int    `json:"days"`
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
		radarrClient, _, refusal := s.radarrTargetFor(params.InstanceID, callInstanceID)
		if radarrClient == nil {
			if mediaType == "movie" {
				return &ToolResult{Text: refusal}, nil
			}
			notes = append(notes, refusal)
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
		sonarrClient, _, refusal := s.sonarrTargetFor(params.InstanceID, callInstanceID)
		if sonarrClient == nil {
			if mediaType == "tv" {
				return &ToolResult{Text: refusal}, nil
			}
			notes = append(notes, refusal)
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

	if mediaType == "music" || mediaType == "all" {
		lidarrClient, _, refusal := s.lidarrTargetFor(params.InstanceID, callInstanceID)
		if lidarrClient == nil {
			if mediaType == "music" {
				return &ToolResult{Text: refusal}, nil
			}
			notes = append(notes, refusal)
		} else {
			items, err := lidarrClient.GetCalendar(start, end)
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				if item.ReleaseDate == nil {
					continue
				}
				title := item.Title
				if item.Artist != nil && item.Artist.ArtistName != "" {
					title = fmt.Sprintf("%s — %s", item.Artist.ArtistName, item.Title)
				}
				line := fmt.Sprintf("- [music] %s — album release", title)
				if item.Statistics.TrackFileCount > 0 {
					line += " (already downloaded)"
				}
				// Album release dates are calendar dates (no meaningful
				// time-of-day): read the Y/M/D components directly, the movie
				// convention, so a midnight date never shifts a day.
				entries = append(entries, calendarEntry{date: item.ReleaseDate.Format("2006-01-02"), line: line})
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

func (s *ToolServer) getLibrary(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	var params struct {
		MediaType  string `json:"media_type"`
		InstanceID string `json:"instance_id"`
		Filter     string `json:"filter"`
		Query      string `json:"query"`
		TmdbID     int    `json:"tmdb_id"`
		TvdbID     int    `json:"tvdb_id"`
		AuthorID   int    `json:"author_id"`
		BookID     int    `json:"book_id"`
		ArtistID   int    `json:"artist_id"`
		AlbumID    int    `json:"album_id"`
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
		radarrClient, radarrLabel, refusal := s.radarrTargetFor(params.InstanceID, callInstanceID)
		if radarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		movies, err := radarrClient.GetMovies()
		if err != nil {
			return nil, err
		}
		total := len(movies)
		var matched []radarr.Movie
		for _, m := range movies {
			if params.TmdbID > 0 && m.TmdbID != params.TmdbID {
				continue
			}
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
		fmt.Fprintf(&sb, "Movie library on %s: %d total, %d matching (filter: %s", radarrLabel, total, len(matched), filter)
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
		sonarrClient, sonarrLabel, refusal := s.sonarrTargetFor(params.InstanceID, callInstanceID)
		if sonarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		series, err := sonarrClient.GetAllSeries()
		if err != nil {
			return nil, err
		}
		total := len(series)
		var matched []sonarr.Series
		for _, sr := range series {
			if !(mediaReadScope{TmdbID: params.TmdbID, TvdbID: params.TvdbID}).matchesSonarrIdentity(sr.TmdbID, sr.TvdbID) {
				continue
			}
			switch filter {
			case "missing":
				if !sr.Monitored {
					continue
				}
				// Skip only truly complete series. percentOfEpisodes would
				// also skip incomplete series whose few monitored episodes
				// are all downloaded (it only counts monitored episodes).
				if files, total := sr.EpisodeTotals(); total > 0 && files >= total {
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
		fmt.Fprintf(&sb, "TV library on %s: %d total, %d matching (filter: %s", sonarrLabel, total, len(matched), filter)
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
			if files, total := sr.EpisodeTotals(); total > 0 || files > 0 {
				fmt.Fprintf(&sb, " — %d/%d episodes", files, total)
			}
			if !sr.Monitored {
				sb.WriteString(" — unmonitored")
			}
		}
		return &ToolResult{Text: sb.String()}, nil

	case "book":
		chaptarrClient, chaptarrLabel, refusal := s.chaptarrTargetFor(params.InstanceID, callInstanceID)
		if chaptarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		if params.BookID > 0 || params.AuthorID > 0 {
			return chaptarrBookScopedLibrary(chaptarrClient, chaptarrLabel, params.BookID, params.AuthorID, filter, query)
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
		fmt.Fprintf(&sb, "Book library on %s: %d author(s) total, %d matching (filter: %s", chaptarrLabel, total, len(matched), filter)
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
		sb.WriteString("\n\nCall get_library again with author_id to list one author's books with their book ids, then use get_queue, search_releases (book_id), or trigger_search (author_id/book_id) for per-book actions.")
		return &ToolResult{Text: sb.String()}, nil

	case "music":
		lidarrClient, lidarrLabel, refusal := s.lidarrTargetFor(params.InstanceID, callInstanceID)
		if lidarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		if params.AlbumID > 0 || params.ArtistID > 0 {
			return lidarrAlbumScopedLibrary(lidarrClient, lidarrLabel, params.AlbumID, params.ArtistID, filter, query)
		}
		artists, err := lidarrClient.GetArtists()
		if err != nil {
			return nil, err
		}
		total := len(artists)
		var matched []lidarr.Artist
		for _, a := range artists {
			switch filter {
			case "missing":
				if !a.Monitored {
					continue
				}
				if a.Statistics.PercentOfTracks >= 100 {
					continue
				}
			case "unmonitored":
				if a.Monitored {
					continue
				}
			}
			if query != "" && !strings.Contains(strings.ToLower(a.ArtistName), query) {
				continue
			}
			matched = append(matched, a)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Music library on %s: %d artist(s) total, %d matching (filter: %s", lidarrLabel, total, len(matched), filter)
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
			fmt.Fprintf(&sb, "\n- %s [artist ID %d]", a.ArtistName, a.ID)
			fmt.Fprintf(&sb, " — %d/%d tracks across %d album(s)", a.Statistics.TrackFileCount, a.Statistics.TrackCount, a.Statistics.AlbumCount)
			if !a.Monitored {
				sb.WriteString(" — unmonitored")
			}
		}
		sb.WriteString("\n\nCall get_library again with artist_id to list one artist's albums with their album ids.")
		return &ToolResult{Text: sb.String()}, nil

	default:
		return &ToolResult{Text: "media_type must be \"movie\", \"tv\", \"book\", or \"music\"."}, nil
	}
}

// lidarrAlbumScopedLibrary renders the album-level library view: one exact
// album record, or one artist's albums. This is the in-tool source of the
// album ids the per-album reads (get_queue, get_album_timeline) take. label
// names the library that was read so a multi-library empty answer cannot be
// mistaken for absence in a sibling library.
func lidarrAlbumScopedLibrary(client *lidarr.Client, label string, albumID, artistID int, filter, query string) (*ToolResult, error) {
	formatAlbum := func(a lidarr.Album) string {
		var sb strings.Builder
		fmt.Fprintf(&sb, "- %s [album ID %d", a.Title, a.ID)
		if a.ArtistID > 0 {
			fmt.Fprintf(&sb, ", artist ID %d", a.ArtistID)
		}
		sb.WriteString("]")
		if a.AlbumType != "" {
			fmt.Fprintf(&sb, " (%s)", a.AlbumType)
		}
		fmt.Fprintf(&sb, " — %d/%d tracks on disk", a.Statistics.TrackFileCount, a.Statistics.TrackCount)
		if !a.Monitored {
			sb.WriteString(" — unmonitored")
		}
		return sb.String()
	}
	if albumID > 0 {
		album, err := client.GetAlbum(albumID)
		if err != nil {
			return nil, err
		}
		if album == nil {
			return &ToolResult{Text: fmt.Sprintf("Album id %d was not found on %s.", albumID, label)}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Album record on %s:\n", label)
		sb.WriteString(formatAlbum(*album))
		if album.Artist != nil && album.Artist.ArtistName != "" {
			fmt.Fprintf(&sb, "\n  artist: %s", album.Artist.ArtistName)
		}
		return &ToolResult{Text: sb.String()}, nil
	}
	albums, err := client.GetAlbumsForArtist(artistID)
	if err != nil {
		return nil, err
	}
	var matched []lidarr.Album
	for _, a := range albums {
		switch filter {
		case "missing":
			if !a.Monitored || a.Statistics.TrackFileCount >= a.Statistics.TrackCount && a.Statistics.TrackCount > 0 {
				continue
			}
		case "unmonitored":
			if a.Monitored {
				continue
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(a.Title), query) {
			continue
		}
		matched = append(matched, a)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Albums for artist %d on %s: %d total, %d matching (filter: %s)", artistID, label, len(albums), len(matched), filter)
	shown := matched
	if len(shown) > maxLibraryItems {
		shown = shown[:maxLibraryItems]
		fmt.Fprintf(&sb, ", showing first %d", maxLibraryItems)
	}
	for _, a := range shown {
		sb.WriteString("\n")
		sb.WriteString(formatAlbum(a))
	}
	if len(matched) == 0 {
		sb.WriteString("\n(no albums matched)")
	}
	sb.WriteString("\n\nUse these album ids with get_queue (album_id) or get_album_timeline.")
	return &ToolResult{Text: sb.String()}, nil
}

// chaptarrBookScopedLibrary renders the book-level library view: one exact book
// record, or one author's books. This is the in-tool source of the book ids the
// per-book action tools (search_releases, grab_release, trigger_search) require.
// label names the library that was read so a multi-library empty answer cannot
// be mistaken for absence in a sibling library.
func chaptarrBookScopedLibrary(client *chaptarr.Client, label string, bookID, authorID int, filter, query string) (*ToolResult, error) {
	formatBook := func(b chaptarr.Book) string {
		var sb strings.Builder
		fmt.Fprintf(&sb, "- %s [book ID %d", b.Title, b.ID)
		if b.AuthorID > 0 {
			fmt.Fprintf(&sb, ", author ID %d", b.AuthorID)
		}
		sb.WriteString("]")
		if b.MediaType != "" {
			fmt.Fprintf(&sb, " (%s)", b.MediaType)
		}
		fmt.Fprintf(&sb, " — %d file(s)", b.Statistics.BookFileCount)
		if !b.Monitored {
			sb.WriteString(" — unmonitored")
		}
		return sb.String()
	}
	if bookID > 0 {
		book, err := client.GetBook(bookID)
		if err != nil {
			return nil, err
		}
		if book == nil {
			return &ToolResult{Text: fmt.Sprintf("Book id %d was not found on %s.", bookID, label)}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Book record on %s:\n", label)
		sb.WriteString(formatBook(*book))
		if book.Author != nil && book.Author.AuthorName != "" {
			fmt.Fprintf(&sb, "\n  author: %s", book.Author.AuthorName)
		}
		return &ToolResult{Text: sb.String()}, nil
	}
	books, err := client.GetBooks(authorID)
	if err != nil {
		return nil, err
	}
	var matched []chaptarr.Book
	for _, b := range books {
		switch filter {
		case "missing":
			if !b.Monitored || b.Statistics.BookFileCount > 0 {
				continue
			}
		case "unmonitored":
			if b.Monitored {
				continue
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(b.Title), query) {
			continue
		}
		matched = append(matched, b)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Books for author %d on %s: %d total, %d matching (filter: %s)", authorID, label, len(books), len(matched), filter)
	shown := matched
	if len(shown) > maxLibraryItems {
		shown = shown[:maxLibraryItems]
		fmt.Fprintf(&sb, ", showing first %d", maxLibraryItems)
	}
	for _, b := range shown {
		sb.WriteString("\n")
		sb.WriteString(formatBook(b))
	}
	if len(matched) == 0 {
		sb.WriteString("\n(no books matched)")
	}
	sb.WriteString("\n\nUse these book ids with search_releases (book_id), grab_release, or trigger_search (book_id).")
	return &ToolResult{Text: sb.String()}, nil
}

// --- get_history ---

func (s *ToolServer) getHistory(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	var params struct {
		MediaType     string `json:"media_type"`
		InstanceID    string `json:"instance_id"`
		Limit         int    `json:"limit"`
		TmdbID        int    `json:"tmdb_id"`
		TvdbID        int    `json:"tvdb_id"`
		SeasonNumber  int    `json:"season_number"`
		EpisodeNumber int    `json:"episode_number"`
		AuthorID      int    `json:"author_id"`
		BookID        int    `json:"book_id"`
		ArtistID      int    `json:"artist_id"`
		AlbumID       int    `json:"album_id"`
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
	scope := mediaReadScope{
		TmdbID: params.TmdbID, TvdbID: params.TvdbID,
		SeasonNumber: params.SeasonNumber, EpisodeNumber: params.EpisodeNumber,
		AuthorID: params.AuthorID, BookID: params.BookID,
		ArtistID: params.ArtistID, AlbumID: params.AlbumID,
	}
	fetchLimit := limit
	if scope.hasTitleIdentity() || scope.SeasonNumber > 0 || scope.EpisodeNumber > 0 || scope.BookID > 0 || scope.AuthorID > 0 || scope.AlbumID > 0 || scope.ArtistID > 0 {
		// Filtering after fetching only the requested 20 records can falsely hide
		// a title whose last event is slightly older. Fetch the bounded maximum,
		// then return at most the caller's requested count.
		//
		// For a scoped title this is still only a mitigation: a busy library
		// generates 100 global records in days, and a scoped read of anything
		// older then returns "no history found" — indistinguishable from a title
		// that genuinely has none. Radarr and Sonarr both expose a per-title
		// history endpoint, so a scoped read asks the service for that title's
		// records instead of sifting a global page for them. The client-side
		// filter still runs afterwards as the identity gate.
		fetchLimit = 100
	}

	switch params.MediaType {
	case "movie":
		radarrClient, radarrLabel, refusal := s.radarrTargetFor(params.InstanceID, callInstanceID)
		if radarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		records, perTitle, err := scopedRadarrHistory(radarrClient, scope, fetchLimit)
		if err != nil {
			return nil, err
		}
		records, err = filterRadarrHistory(radarrClient, records, scope)
		if err != nil {
			return nil, err
		}
		if len(records) > limit {
			records = records[:limit]
		}
		if len(records) == 0 {
			return &ToolResult{Text: radarrLabel + ": " + noHistoryText("movie", scope, perTitle, fetchLimit)}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Recent movie history on %s (%d records):", radarrLabel, len(records))
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
		sonarrClient, sonarrLabel, refusal := s.sonarrTargetFor(params.InstanceID, callInstanceID)
		if sonarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		records, perTitle, err := scopedSonarrHistory(s.bridge, sonarrClient, scope, fetchLimit)
		if err != nil {
			return nil, err
		}
		records, err = filterSonarrHistory(sonarrClient, records, scope)
		if err != nil {
			return nil, err
		}
		if len(records) > limit {
			records = records[:limit]
		}
		if len(records) == 0 {
			return &ToolResult{Text: sonarrLabel + ": " + noHistoryText("TV", scope, perTitle, fetchLimit)}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Recent TV history on %s (%d records):", sonarrLabel, len(records))
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
		chaptarrClient, chaptarrLabel, refusal := s.chaptarrTargetFor(params.InstanceID, callInstanceID)
		if chaptarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		page, err := chaptarrClient.GetHistory(1, fetchLimit)
		if err != nil {
			return nil, err
		}
		var records []chaptarr.HistoryRecord
		if page != nil {
			records = filterChaptarrHistory(page.Records, scope)
		}
		if len(records) > limit {
			records = records[:limit]
		}
		if len(records) == 0 {
			return &ToolResult{Text: chaptarrLabel + ": " + noHistoryText("book", scope, false, fetchLimit)}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Recent book history on %s (%d records):", chaptarrLabel, len(records))
		for _, rec := range records {
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

	case "music":
		lidarrClient, lidarrLabel, refusal := s.lidarrTargetFor(params.InstanceID, callInstanceID)
		if lidarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		page, err := lidarrClient.GetHistory(1, fetchLimit)
		if err != nil {
			return nil, err
		}
		var records []lidarr.HistoryRecord
		if page != nil {
			records = filterLidarrHistory(page.Records, scope)
		}
		if len(records) > limit {
			records = records[:limit]
		}
		if len(records) == 0 {
			// Lidarr history is only a global page: perTitle=false, so a scoped
			// empty answer says an older event would not appear here.
			return &ToolResult{Text: lidarrLabel + ": " + noHistoryText("music", scope, false, fetchLimit)}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Recent music history on %s (%d records):", lidarrLabel, len(records))
		for _, rec := range records {
			when := "unknown date"
			if rec.Date != nil {
				when = rec.Date.UTC().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(&sb, "\n- %s %s", when, rec.EventType)
			if rec.Artist != nil && rec.Artist.ArtistName != "" {
				fmt.Fprintf(&sb, ": %s", rec.Artist.ArtistName)
				if rec.Album != nil && rec.Album.Title != "" {
					fmt.Fprintf(&sb, " — %s", rec.Album.Title)
				}
			} else if rec.Album != nil && rec.Album.Title != "" {
				fmt.Fprintf(&sb, ": %s", rec.Album.Title)
			}
			if rec.SourceTitle != "" {
				fmt.Fprintf(&sb, " — %s", rec.SourceTitle)
			}
		}
		return &ToolResult{Text: sb.String()}, nil

	default:
		return &ToolResult{Text: "media_type must be \"movie\", \"tv\", \"book\", or \"music\"."}, nil
	}
}

// --- trigger_search ---

func (s *ToolServer) triggerSearch(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	var params struct {
		TmdbID        int    `json:"tmdb_id"`
		MediaType     string `json:"media_type"`
		InstanceID    string `json:"instance_id"`
		SeasonNumber  *int   `json:"season_number"`
		EpisodeNumber *int   `json:"episode_number"`
		AiredOnly     bool   `json:"aired_only"`
		AuthorID      int    `json:"author_id"`
		BookID        int    `json:"book_id"`
		ArtistID      int    `json:"artist_id"`
		AlbumID       int    `json:"album_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	var bookIDs []int
	if params.BookID != 0 {
		bookIDs = []int{params.BookID}
	}
	var albumIDs []int
	if params.AlbumID != 0 {
		albumIDs = []int{params.AlbumID}
	}
	radarrClient, sonarrClient, chaptarrClient, lidarrClient, refusal := s.arrClientsFor(params.InstanceID, callInstanceID)
	if refusal != "" {
		return &ToolResult{Text: refusal}, nil
	}
	text, err := TriggerSearchHelper(s.bridge, radarrClient, sonarrClient, chaptarrClient, lidarrClient, params.MediaType, params.TmdbID, params.SeasonNumber, params.EpisodeNumber, params.AiredOnly, params.AuthorID, bookIDs, params.ArtistID, albumIDs)
	if err != nil {
		return nil, err
	}
	return &ToolResult{Text: text}, nil
}

// --- search_releases (admin) ---

const maxReleaseResults = 15

func formatRadarrReleases(releases []radarr.Release) string {
	capabilities := radarrReleaseCapabilities(releases)
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
		fmt.Fprintf(&sb, "   reference: %s\n", releaseGUIDReference(rel.GUID))
	}
	return scrubRawReleaseGUIDs(sb.String(), capabilities)
}

func formatSonarrReleases(releases []sonarr.Release) string {
	capabilities := sonarrReleaseCapabilities(releases)
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
		fmt.Fprintf(&sb, "   reference: %s\n", releaseGUIDReference(rel.GUID))
	}
	return scrubRawReleaseGUIDs(sb.String(), capabilities)
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
	capabilities := chaptarrReleaseCapabilities(releases)
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
		fmt.Fprintf(&sb, "   reference: %s\n", releaseGUIDReference(rel.GUID))
	}
	return scrubRawReleaseGUIDs(sb.String(), capabilities)
}

// formatLidarrReleases renders an album's release search like the book one:
// Lidarr's release resource has the same shape as Chaptarr's, including the
// optional seeder counts usenet omits.
func formatLidarrReleases(releases []lidarr.Release) string {
	capabilities := lidarrReleaseCapabilities(releases)
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
		fmt.Fprintf(&sb, "   reference: %s\n", releaseGUIDReference(rel.GUID))
	}
	return scrubRawReleaseGUIDs(sb.String(), capabilities)
}

func safeReleaseText(value string, releases []releaseCapability) string {
	return secrets.RedactText(scrubRawReleaseGUIDs(value, releases))
}

func safeReleaseRejections(values []string, releases []releaseCapability) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = safeReleaseText(value, releases)
	}
	return out
}

func radarrReleaseCandidates(releases []radarr.Release) []ReleaseCandidate {
	capabilities := radarrReleaseCapabilities(releases)
	if len(releases) > maxReleaseResults {
		releases = releases[:maxReleaseResults]
	}
	out := make([]ReleaseCandidate, 0, len(releases))
	for _, release := range releases {
		out = append(out, ReleaseCandidate{
			Reference: releaseGUIDReference(release.GUID), IndexerID: release.IndexerID,
			Title: safeReleaseText(release.Title, capabilities), Quality: safeReleaseText(release.Quality.Quality.Name, capabilities),
			Size: release.Size, Protocol: safeReleaseText(release.Protocol, capabilities), Indexer: safeReleaseText(release.Indexer, capabilities),
			Rejected: release.Rejected, Rejections: safeReleaseRejections(release.Rejections, capabilities),
		})
	}
	return out
}

func sonarrReleaseCandidates(releases []sonarr.Release) []ReleaseCandidate {
	capabilities := sonarrReleaseCapabilities(releases)
	if len(releases) > maxReleaseResults {
		releases = releases[:maxReleaseResults]
	}
	out := make([]ReleaseCandidate, 0, len(releases))
	for _, release := range releases {
		out = append(out, ReleaseCandidate{
			Reference: releaseGUIDReference(release.GUID), IndexerID: release.IndexerID,
			Title: safeReleaseText(release.Title, capabilities), Quality: safeReleaseText(release.Quality.Quality.Name, capabilities),
			Size: release.Size, Protocol: safeReleaseText(release.Protocol, capabilities), Indexer: safeReleaseText(release.Indexer, capabilities),
			Rejected: release.Rejected, Rejections: safeReleaseRejections(release.Rejections, capabilities),
		})
	}
	return out
}

func chaptarrReleaseCandidates(releases []chaptarr.Release) []ReleaseCandidate {
	capabilities := chaptarrReleaseCapabilities(releases)
	if len(releases) > maxReleaseResults {
		releases = releases[:maxReleaseResults]
	}
	out := make([]ReleaseCandidate, 0, len(releases))
	for _, release := range releases {
		out = append(out, ReleaseCandidate{
			Reference: releaseGUIDReference(release.GUID), IndexerID: release.IndexerID,
			Title: safeReleaseText(release.Title, capabilities), Size: release.Size,
			Protocol: safeReleaseText(release.Protocol, capabilities), Indexer: safeReleaseText(release.Indexer, capabilities),
			Rejected: release.Rejected, Rejections: safeReleaseRejections(release.Rejections, capabilities),
		})
	}
	return out
}

func lidarrReleaseCandidates(releases []lidarr.Release) []ReleaseCandidate {
	capabilities := lidarrReleaseCapabilities(releases)
	if len(releases) > maxReleaseResults {
		releases = releases[:maxReleaseResults]
	}
	out := make([]ReleaseCandidate, 0, len(releases))
	for _, release := range releases {
		out = append(out, ReleaseCandidate{
			Reference: releaseGUIDReference(release.GUID), IndexerID: release.IndexerID,
			Title: safeReleaseText(release.Title, capabilities), Size: release.Size,
			Protocol: safeReleaseText(release.Protocol, capabilities), Indexer: safeReleaseText(release.Indexer, capabilities),
			Rejected: release.Rejected, Rejections: safeReleaseRejections(release.Rejections, capabilities),
		})
	}
	return out
}

func (s *ToolServer) searchReleases(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	var params struct {
		TmdbID        int    `json:"tmdb_id"`
		MediaType     string `json:"media_type"`
		InstanceID    string `json:"instance_id"`
		SeasonNumber  *int   `json:"season_number"`
		EpisodeNumber *int   `json:"episode_number"`
		BookID        int    `json:"book_id"`
		AlbumID       int    `json:"album_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	// A grab must re-search the SAME library this search read, so a targeted
	// search tells the model to carry the instance id into grab_release.
	grabInstanceHint := ""
	if id := arrToolInstanceID(params.InstanceID, callInstanceID); id != "" {
		grabInstanceHint = fmt.Sprintf(" Pass instance_id=%s so the grab targets the same library.", id)
	}

	switch params.MediaType {
	case "movie":
		if params.TmdbID <= 0 || params.SeasonNumber != nil || params.EpisodeNumber != nil || params.BookID != 0 || params.AlbumID != 0 {
			return &ToolResult{Text: "Movie release search requires only a positive tmdb_id as its media scope."}, nil
		}
		radarrClient, _, refusal := s.radarrTargetFor(params.InstanceID, callInstanceID)
		if radarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		movie, err := radarrClient.GetMovieByTMDB(params.TmdbID)
		if err != nil {
			return nil, err
		}
		if movie == nil {
			return &ToolResult{Text: "This movie is not in the library yet. Use request_media to add it first."}, nil
		}
		if movie.ID <= 0 || movie.TmdbID != params.TmdbID {
			return nil, fmt.Errorf("Radarr returned a movie outside the requested TMDB scope")
		}
		releases, err := radarrClient.SearchReleases(movie.ID)
		if err != nil {
			return nil, err
		}
		if len(releases) == 0 {
			return &ToolResult{Text: fmt.Sprintf("No releases found for %s (%d).", movie.Title, movie.Year)}, nil
		}
		header := fmt.Sprintf("Found %d release(s) for %s (%d), showing top %d. Use grab_release with guid=<one-way reference>, indexer_id, media_type=movie, and tmdb_id=%d.%s\n",
			len(releases), movie.Title, movie.Year, min(len(releases), maxReleaseResults), params.TmdbID, grabInstanceHint)
		text := scrubRawReleaseGUIDs(header+formatRadarrReleases(releases), radarrReleaseCapabilities(releases))
		return &ToolResult{Text: text, ReleaseCandidates: radarrReleaseCandidates(releases)}, nil

	case "tv":
		if params.TmdbID <= 0 || params.SeasonNumber == nil || *params.SeasonNumber < 0 || params.BookID != 0 || params.AlbumID != 0 ||
			(params.EpisodeNumber != nil && *params.EpisodeNumber <= 0) {
			return &ToolResult{Text: "TV release search requires a positive tmdb_id, a non-negative season_number, and optionally a positive episode_number."}, nil
		}
		sonarrClient, _, refusal := s.sonarrTargetFor(params.InstanceID, callInstanceID)
		if sonarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		series, err := s.findSeriesByTMDB(sonarrClient, params.TmdbID)
		if err != nil {
			return nil, err
		}
		if series == nil {
			return &ToolResult{Text: "This show is not in the library yet. Use request_media to add it first."}, nil
		}
		if series.ID <= 0 || series.TmdbID != params.TmdbID {
			return nil, fmt.Errorf("Sonarr returned a series outside the requested TMDB scope")
		}
		var releases []sonarr.Release
		if params.EpisodeNumber != nil {
			episodes, err := sonarrClient.GetEpisodes(series.ID, *params.SeasonNumber)
			if err != nil {
				return nil, err
			}
			episodeIDs := make([]int, 0, 1)
			for _, episode := range episodes {
				if episode.ID > 0 && episode.SeasonNumber == *params.SeasonNumber && episode.EpisodeNumber == *params.EpisodeNumber {
					episodeIDs = append(episodeIDs, episode.ID)
				}
			}
			if len(episodeIDs) == 0 {
				return &ToolResult{Text: fmt.Sprintf("Episode S%02dE%02d was not found in %s.", *params.SeasonNumber, *params.EpisodeNumber, series.Title)}, nil
			}
			if len(episodeIDs) != 1 {
				return nil, fmt.Errorf("Sonarr returned an ambiguous episode inside the requested TV scope")
			}
			releases, err = sonarrClient.SearchEpisodeReleases(episodeIDs[0])
		} else {
			releases, err = sonarrClient.SearchReleases(series.ID, *params.SeasonNumber)
		}
		if err != nil {
			return nil, err
		}
		if len(releases) == 0 {
			if params.EpisodeNumber != nil {
				return &ToolResult{Text: fmt.Sprintf("No releases found for %s S%02dE%02d.", series.Title, *params.SeasonNumber, *params.EpisodeNumber)}, nil
			}
			return &ToolResult{Text: fmt.Sprintf("No releases found for %s season %d.", series.Title, *params.SeasonNumber)}, nil
		}
		if params.EpisodeNumber != nil {
			header := fmt.Sprintf("Found %d release(s) for %s S%02dE%02d, showing top %d. Use grab_release with guid=<one-way reference>, indexer_id, media_type=tv, tmdb_id=%d, season_number=%d, and episode_number=%d.%s\n",
				len(releases), series.Title, *params.SeasonNumber, *params.EpisodeNumber, min(len(releases), maxReleaseResults), params.TmdbID, *params.SeasonNumber, *params.EpisodeNumber, grabInstanceHint)
			text := scrubRawReleaseGUIDs(header+formatSonarrReleases(releases), sonarrReleaseCapabilities(releases))
			return &ToolResult{Text: text, ReleaseCandidates: sonarrReleaseCandidates(releases)}, nil
		}
		header := fmt.Sprintf("Found %d release(s) for %s season %d, showing top %d. Use grab_release with guid=<one-way reference>, indexer_id, media_type=tv, tmdb_id=%d, and season_number=%d.%s\n",
			len(releases), series.Title, *params.SeasonNumber, min(len(releases), maxReleaseResults), params.TmdbID, *params.SeasonNumber, grabInstanceHint)
		text := scrubRawReleaseGUIDs(header+formatSonarrReleases(releases), sonarrReleaseCapabilities(releases))
		return &ToolResult{Text: text, ReleaseCandidates: sonarrReleaseCandidates(releases)}, nil

	case "book":
		if params.BookID <= 0 || params.TmdbID != 0 || params.SeasonNumber != nil || params.EpisodeNumber != nil || params.AlbumID != 0 {
			return &ToolResult{Text: "Book release search requires only a positive book_id as its media scope."}, nil
		}
		chaptarrClient, _, refusal := s.chaptarrTargetFor(params.InstanceID, callInstanceID)
		if chaptarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		releases, err := chaptarrClient.SearchReleases(params.BookID)
		if err != nil {
			return nil, err
		}
		if len(releases) == 0 {
			return &ToolResult{Text: fmt.Sprintf("No releases found for book id %d.", params.BookID)}, nil
		}
		header := fmt.Sprintf("Found %d release(s) for book id %d, showing top %d. Use grab_release with guid=<one-way reference>, indexer_id, media_type=book, and book_id=%d.%s\n",
			len(releases), params.BookID, min(len(releases), maxReleaseResults), params.BookID, grabInstanceHint)
		text := scrubRawReleaseGUIDs(header+formatChaptarrReleases(releases), chaptarrReleaseCapabilities(releases))
		return &ToolResult{Text: text, ReleaseCandidates: chaptarrReleaseCandidates(releases)}, nil

	case "music":
		if params.AlbumID <= 0 || params.TmdbID != 0 || params.BookID != 0 || params.SeasonNumber != nil || params.EpisodeNumber != nil {
			return &ToolResult{Text: "Music release search requires only a positive album_id as its media scope."}, nil
		}
		lidarrClient, _, refusal := s.lidarrTargetFor(params.InstanceID, callInstanceID)
		if lidarrClient == nil {
			return &ToolResult{Text: refusal}, nil
		}
		releases, err := lidarrClient.SearchReleases(params.AlbumID)
		if err != nil {
			return nil, err
		}
		if len(releases) == 0 {
			return &ToolResult{Text: fmt.Sprintf("No releases found for album id %d.", params.AlbumID)}, nil
		}
		header := fmt.Sprintf("Found %d release(s) for album id %d, showing top %d. Use grab_release with guid=<one-way reference>, indexer_id, media_type=music, and album_id=%d.%s\n",
			len(releases), params.AlbumID, min(len(releases), maxReleaseResults), params.AlbumID, grabInstanceHint)
		text := scrubRawReleaseGUIDs(header+formatLidarrReleases(releases), lidarrReleaseCapabilities(releases))
		return &ToolResult{Text: text, ReleaseCandidates: lidarrReleaseCandidates(releases)}, nil

	default:
		return &ToolResult{Text: "media_type must be \"movie\", \"tv\", \"book\", or \"music\"."}, nil
	}
}

// --- grab_release (admin) ---

func (s *ToolServer) grabRelease(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	var params scopedReleaseGrabParams
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	radarrClient, sonarrClient, chaptarrClient, lidarrClient, refusal := s.arrClientsFor(params.InstanceID, callInstanceID)
	if refusal != "" {
		return &ToolResult{Text: refusal}, nil
	}
	text, err := grabFreshScopedRelease(
		radarrClient,
		sonarrClient,
		chaptarrClient,
		lidarrClient,
		params,
	)
	if err != nil {
		return nil, err
	}
	return &ToolResult{Text: text}, nil
}

// --- remove_queue_item (admin) ---

func (s *ToolServer) removeQueueItem(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	var params struct {
		QueueID    int    `json:"queue_id"`
		MediaType  string `json:"media_type"`
		InstanceID string `json:"instance_id"`
		Blocklist  bool   `json:"blocklist"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	radarrClient, sonarrClient, chaptarrClient, lidarrClient, refusal := s.arrClientsFor(params.InstanceID, callInstanceID)
	if refusal != "" {
		return &ToolResult{Text: refusal}, nil
	}
	text, err := RemoveQueueItemHelper(radarrClient, sonarrClient, chaptarrClient, lidarrClient, params.MediaType, params.QueueID, params.Blocklist)
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

// arrDiskSpaceEntry is the shared shape of the three services' disk records.
type arrDiskSpaceEntry struct {
	Path       string
	Label      string
	FreeSpace  int64
	TotalSpace int64
}

// diskSpaceFor reads one instance's volumes through the right typed client.
func (s *ToolServer) diskSpaceFor(service, instanceID string) ([]arrDiskSpaceEntry, error) {
	var entries []arrDiskSpaceEntry
	switch service {
	case "radarr":
		client := s.GetRadarrFor(instanceID)
		if client == nil {
			return nil, fmt.Errorf("client unavailable")
		}
		disks, err := client.GetDiskSpace()
		if err != nil {
			return nil, err
		}
		for _, d := range disks {
			entries = append(entries, arrDiskSpaceEntry{Path: d.Path, Label: d.Label, FreeSpace: d.FreeSpace, TotalSpace: d.TotalSpace})
		}
	case "sonarr":
		client := s.GetSonarrFor(instanceID)
		if client == nil {
			return nil, fmt.Errorf("client unavailable")
		}
		disks, err := client.GetDiskSpace()
		if err != nil {
			return nil, err
		}
		for _, d := range disks {
			entries = append(entries, arrDiskSpaceEntry{Path: d.Path, Label: d.Label, FreeSpace: d.FreeSpace, TotalSpace: d.TotalSpace})
		}
	case "chaptarr":
		client := s.GetChaptarrFor(instanceID)
		if client == nil {
			return nil, fmt.Errorf("client unavailable")
		}
		disks, err := client.GetDiskSpace()
		if err != nil {
			return nil, err
		}
		for _, d := range disks {
			entries = append(entries, arrDiskSpaceEntry{Path: d.Path, Label: d.Label, FreeSpace: d.FreeSpace, TotalSpace: d.TotalSpace})
		}
	case "lidarr":
		client := s.GetLidarrFor(instanceID)
		if client == nil {
			return nil, fmt.Errorf("client unavailable")
		}
		disks, err := client.GetDiskSpace()
		if err != nil {
			return nil, err
		}
		for _, d := range disks {
			entries = append(entries, arrDiskSpaceEntry{Path: d.Path, Label: d.Label, FreeSpace: d.FreeSpace, TotalSpace: d.TotalSpace})
		}
	}
	return entries, nil
}

// getDiskSpace lists every configured library's volumes, labeled per instance
// — a multi-library server has disks the old default-only read never showed.
// An optional instance_id narrows to one library.
func (s *ToolServer) getDiskSpace(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	var params struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(nonEmptyJSON(input), &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	only := arrToolInstanceID(params.InstanceID, callInstanceID)

	var sections []string
	matchedOnly := false
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		if s.registry == nil {
			break
		}
		summaries, err := s.registry.ListInstanceSummaries(service)
		if err != nil {
			continue
		}
		for _, summary := range summaries {
			if only != "" && summary.ID != only {
				continue
			}
			if only != "" {
				matchedOnly = true
			}
			label := s.arrInstanceLabel(service, summary.ID)
			disks, err := s.diskSpaceFor(service, summary.ID)
			if err != nil {
				// One unreachable library must not blank the others; the
				// detail stays in server logs (an error string can carry a
				// host).
				sections = append(sections, label+" disk space: could not be read.")
				continue
			}
			var sb strings.Builder
			sb.WriteString(label + " disk space:")
			for _, d := range disks {
				formatDiskLines(&sb, d.Path, d.Label, d.FreeSpace, d.TotalSpace)
			}
			sections = append(sections, sb.String())
		}
	}

	if len(sections) == 0 {
		if only != "" && !matchedOnly && callInstanceID == "" {
			return &ToolResult{Text: fmt.Sprintf("No Radarr, Sonarr, Chaptarr, or Lidarr instance with ID %q. Call list_arr_instances to see the configured instances.", only)}, nil
		}
		return &ToolResult{Text: "Radarr/Sonarr/Chaptarr/Lidarr is not configured."}, nil
	}
	return &ToolResult{Text: strings.Join(sections, "\n\n")}, nil
}
