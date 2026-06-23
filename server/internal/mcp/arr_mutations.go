package mcp

import (
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// This file holds the SHARED arr-mutation helpers. Each is the single, canonical
// body for one consequential Radarr/Sonarr mutation. Two callers invoke them and
// ONLY these two:
//
//   - the manual MCP fix tools (grab_release / remove_queue_item /
//     remediate_queue_item / execute_manual_import / trigger_search / rescan_media)
//     an admin drives from the AI chat, and
//   - remediation.Executor, which replays an admin-APPROVED proposal.
//
// Extracting the bodies here means there is exactly one code path per mutation:
// the chat tool and the approve→execute path can never drift. The helpers take
// already-resolved typed clients (the caller resolves the right instance) plus
// the typed args, and return the same plain-language result string the tools
// always returned. They never read settings or check permissions — that gating
// happens in the caller (the chat tool's RBAC, or the approval gate).
//
// A nil client for the relevant media_type yields the same "<service> is not
// configured." message the tools produced, so behavior is identical.

// seriesByTMDB resolves a Sonarr series from a TMDB id, preferring the cached
// tmdb<->tvdb mapping and falling back to a full series scan. It is the single
// implementation behind both the read tools' (s *ToolServer).findSeriesByTMDB
// wrapper and the mutation helpers below.
func seriesByTMDB(bridge *tmdb.Bridge, client *sonarr.Client, tmdbID int) (*sonarr.Series, error) {
	if bridge != nil {
		if res, err := bridge.ResolveTVDBID(tmdbID); err == nil && res.TVDBID != 0 {
			if series, err := client.GetSeriesByTVDB(res.TVDBID); err == nil && series != nil {
				return series, nil
			}
		}
	}
	all, err := client.GetAllSeries()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].TmdbID != 0 && all[i].TmdbID == tmdbID {
			return &all[i], nil
		}
	}
	return nil, nil
}

// GrabReleaseHelper sends a specific release (by guid + indexer id) to the
// download client, optionally replacing an existing queue item first. It is the
// shared body of the grab_release tool and the Executor's grab_release kind.
//
// queueIDToReplace > 0 removes that queue item (deleting the download from the
// client, no blocklist) before grabbing, so a "grab a different release" fix
// doesn't leave the old one downloading alongside the new one.
func GrabReleaseHelper(rc *radarr.Client, sc *sonarr.Client, mediaType, guid string, indexerID, queueIDToReplace int) (string, error) {
	if guid == "" {
		return "guid is required (from search_releases).", nil
	}
	switch mediaType {
	case "movie":
		if rc == nil {
			return "Radarr is not configured.", nil
		}
		if queueIDToReplace > 0 {
			if err := rc.RemoveQueueItem(queueIDToReplace, true, false, false, false); err != nil {
				return "", err
			}
		}
		if err := rc.GrabRelease(guid, indexerID); err != nil {
			return "", err
		}
	case "tv":
		if sc == nil {
			return "Sonarr is not configured.", nil
		}
		if queueIDToReplace > 0 {
			if err := sc.RemoveQueueItem(queueIDToReplace, true, false, false, false); err != nil {
				return "", err
			}
		}
		if err := sc.GrabRelease(guid, indexerID); err != nil {
			return "", err
		}
	default:
		return "media_type must be \"movie\" or \"tv\".", nil
	}
	msg := "Release sent to the download client. It should show up in get_queue shortly."
	if queueIDToReplace > 0 {
		msg = fmt.Sprintf("Removed queue item %d and sent the new release to the download client. It should show up in get_queue shortly.", queueIDToReplace)
	}
	return msg, nil
}

// RemoveQueueItemHelper removes a queue item (deleting the download from the
// client), optionally blocklisting the release so it is not re-grabbed. Shared
// body of the remove_queue_item tool.
func RemoveQueueItemHelper(rc *radarr.Client, sc *sonarr.Client, mediaType string, queueID int, blocklist bool) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return "Radarr is not configured.", nil
		}
		if err := rc.RemoveQueueItem(queueID, true, blocklist, false, false); err != nil {
			return "", err
		}
	case "tv":
		if sc == nil {
			return "Sonarr is not configured.", nil
		}
		if err := sc.RemoveQueueItem(queueID, true, blocklist, false, false); err != nil {
			return "", err
		}
	default:
		return "media_type must be \"movie\" or \"tv\".", nil
	}
	text := fmt.Sprintf("Removed queue item %d and deleted the download from the client.", queueID)
	if blocklist {
		text += " The release was blocklisted so it will not be grabbed again."
	}
	return text, nil
}

// RemediateQueueItemHelper applies one of the structured queue remediations
// (remove | blocklist_search | change_category) to a queue item. Shared body of
// the remediate_queue_item tool and the Executor's remediate_queue kind.
func RemediateQueueItemHelper(rc *radarr.Client, sc *sonarr.Client, mediaType string, queueID int, action string) (string, error) {
	switch action {
	case "remove", "blocklist_search", "change_category":
	default:
		return "action must be \"remove\", \"blocklist_search\", or \"change_category\".", nil
	}

	switch mediaType {
	case "movie":
		if rc == nil {
			return "Radarr is not configured.", nil
		}
		item, err := findRadarrQueueItem(rc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return fmt.Sprintf("No movie queue item with id %d.", queueID), nil
		}
		switch action {
		case "remove":
			if err := rc.RemoveQueueItem(queueID, true, false, false, false); err != nil {
				return "", err
			}
			return fmt.Sprintf("Removed queue item %d (%s) and deleted the download.", queueID, radarrQueueTitle(*item)), nil
		case "blocklist_search":
			if err := rc.RemoveQueueItem(queueID, true, true, false, false); err != nil {
				return "", err
			}
			if item.MovieID != 0 {
				if err := rc.TriggerMoviesSearch([]int{item.MovieID}); err != nil {
					return "", err
				}
				return fmt.Sprintf("Removed and blocklisted queue item %d (%s) and started a fresh search for a different release.", queueID, radarrQueueTitle(*item)), nil
			}
			return fmt.Sprintf("Removed and blocklisted queue item %d (%s). Could not start a search: no movie id on the item.", queueID, radarrQueueTitle(*item)), nil
		case "change_category":
			if err := rc.RemoveQueueItem(queueID, false, false, false, true); err != nil {
				return "", err
			}
			return fmt.Sprintf("Handed queue item %d (%s) to the download client's post-import category. It stays in the client for tools like Unpackerr.", queueID, radarrQueueTitle(*item)), nil
		}

	case "tv":
		if sc == nil {
			return "Sonarr is not configured.", nil
		}
		item, err := findSonarrQueueItem(sc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return fmt.Sprintf("No TV queue item with id %d.", queueID), nil
		}
		switch action {
		case "remove":
			if err := sc.RemoveQueueItem(queueID, true, false, false, false); err != nil {
				return "", err
			}
			return fmt.Sprintf("Removed queue item %d (%s) and deleted the download.", queueID, sonarrQueueTitle(*item)), nil
		case "blocklist_search":
			if err := sc.RemoveQueueItem(queueID, true, true, false, false); err != nil {
				return "", err
			}
			if item.EpisodeID != 0 {
				if err := sc.TriggerEpisodeSearch([]int{item.EpisodeID}); err != nil {
					return "", err
				}
				return fmt.Sprintf("Removed and blocklisted queue item %d (%s) and started a fresh search for a different release.", queueID, sonarrQueueTitle(*item)), nil
			}
			return fmt.Sprintf("Removed and blocklisted queue item %d (%s). Could not start a search: no episode id on the item.", queueID, sonarrQueueTitle(*item)), nil
		case "change_category":
			if err := sc.RemoveQueueItem(queueID, false, false, false, true); err != nil {
				return "", err
			}
			return fmt.Sprintf("Handed queue item %d (%s) to the download client's post-import category. It stays in the client for tools like Unpackerr.", queueID, sonarrQueueTitle(*item)), nil
		}

	default:
		return "media_type must be \"movie\" or \"tv\".", nil
	}
	return "No remediation was applied.", nil
}

// ExecuteManualImportHelper imports the manual-import candidates for a queue
// item's download, honoring force (force imports despite permanent rejections).
// Shared body of the execute_manual_import tool and the Executor's manual_import
// kind.
func ExecuteManualImportHelper(rc *radarr.Client, sc *sonarr.Client, mediaType string, queueID int, force bool) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return "Radarr is not configured.", nil
		}
		item, err := findRadarrQueueItem(rc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return fmt.Sprintf("No movie queue item with id %d.", queueID), nil
		}
		if item.DownloadID == "" {
			return fmt.Sprintf("Queue item %d has no download-client id yet; nothing to import.", queueID), nil
		}
		candidates, err := rc.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return "", err
		}
		var files []radarr.ManualImportFile
		var skipped []string
		for _, c := range candidates {
			rejections := toRejectionViews(c.Rejections)
			if !force && hasPermanentRejection(rejections) {
				skipped = append(skipped, fmt.Sprintf("%s (%s)", c.Name, formatRejections(rejections)))
				continue
			}
			if c.MovieID == 0 {
				skipped = append(skipped, fmt.Sprintf("%s (not matched to a movie)", c.Name))
				continue
			}
			files = append(files, radarr.ManualImportFile{
				Path:         c.Path,
				FolderName:   c.FolderName,
				MovieID:      c.MovieID,
				Quality:      c.Quality,
				Languages:    c.Languages,
				ReleaseGroup: c.ReleaseGroup,
				DownloadID:   c.DownloadID,
				IndexerFlags: c.IndexerFlags,
			})
		}
		if len(files) == 0 {
			return importSkippedMessage(skipped, force), nil
		}
		importMode := importModeFor(item.Protocol)
		if err := rc.ExecuteManualImport(files, importMode); err != nil {
			return "", err
		}
		return importResultMessage(len(files), importMode, skipped), nil

	case "tv":
		if sc == nil {
			return "Sonarr is not configured.", nil
		}
		item, err := findSonarrQueueItem(sc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return fmt.Sprintf("No TV queue item with id %d.", queueID), nil
		}
		if item.DownloadID == "" {
			return fmt.Sprintf("Queue item %d has no download-client id yet; nothing to import.", queueID), nil
		}
		candidates, err := sc.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return "", err
		}
		var files []sonarr.ManualImportFile
		var skipped []string
		for _, c := range candidates {
			rejections := toRejectionViews(c.Rejections)
			if !force && hasPermanentRejection(rejections) {
				skipped = append(skipped, fmt.Sprintf("%s (%s)", c.Name, formatRejections(rejections)))
				continue
			}
			episodeIDs := make([]int, 0, len(c.Episodes))
			for _, e := range c.Episodes {
				if e.ID != 0 {
					episodeIDs = append(episodeIDs, e.ID)
				}
			}
			if len(episodeIDs) == 0 {
				skipped = append(skipped, fmt.Sprintf("%s (no episode mapping)", c.Name))
				continue
			}
			files = append(files, sonarr.ManualImportFile{
				Path:         c.Path,
				FolderName:   c.FolderName,
				SeriesID:     c.SeriesID,
				EpisodeIDs:   episodeIDs,
				Quality:      c.Quality,
				Languages:    c.Languages,
				ReleaseGroup: c.ReleaseGroup,
				DownloadID:   c.DownloadID,
				IndexerFlags: c.IndexerFlags,
				ReleaseType:  c.ReleaseType,
			})
		}
		if len(files) == 0 {
			return importSkippedMessage(skipped, force), nil
		}
		importMode := importModeFor(item.Protocol)
		if err := sc.ExecuteManualImport(files, importMode); err != nil {
			return "", err
		}
		return importResultMessage(len(files), importMode, skipped), nil

	default:
		return "media_type must be \"movie\" or \"tv\".", nil
	}
}

// TriggerSearchHelper kicks off an automatic search for a movie, a whole series,
// or a single season (when seasonNumber != nil). Shared body of the
// trigger_search tool and the Executor's trigger_search kind.
func TriggerSearchHelper(bridge *tmdb.Bridge, rc *radarr.Client, sc *sonarr.Client, mediaType string, tmdbID int, seasonNumber *int) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return "Radarr is not configured.", nil
		}
		movie, err := rc.GetMovieByTMDB(tmdbID)
		if err != nil {
			return "", err
		}
		if movie == nil {
			return "This movie is not in the library yet. Use request_media to add it first.", nil
		}
		if err := rc.TriggerMoviesSearch([]int{movie.ID}); err != nil {
			return "", err
		}
		return fmt.Sprintf("Search started for %s (%d). Check get_queue in a bit to see if a release was grabbed.", movie.Title, movie.Year), nil

	case "tv":
		if sc == nil {
			return "Sonarr is not configured.", nil
		}
		series, err := seriesByTMDB(bridge, sc, tmdbID)
		if err != nil {
			return "", err
		}
		if series == nil {
			return "This show is not in the library yet. Use request_media to add it first.", nil
		}
		if seasonNumber != nil {
			if err := sc.TriggerSeasonSearch(series.ID, *seasonNumber); err != nil {
				return "", err
			}
			return fmt.Sprintf("Search started for %s season %d. Check get_queue in a bit to see if releases were grabbed.", series.Title, *seasonNumber), nil
		}
		if err := sc.TriggerSeriesSearch(series.ID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Search started for all monitored episodes of %s. Check get_queue in a bit to see if releases were grabbed.", series.Title), nil

	default:
		return "media_type must be \"movie\" or \"tv\".", nil
	}
}

// RescanMediaHelper rescans a movie or series on disk and runs the import pass
// (ProcessMonitoredDownloads). Shared body of the rescan_media tool and the
// Executor's rescan kind.
func RescanMediaHelper(bridge *tmdb.Bridge, rc *radarr.Client, sc *sonarr.Client, mediaType string, tmdbID int) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return "Radarr is not configured.", nil
		}
		movie, err := rc.GetMovieByTMDB(tmdbID)
		if err != nil {
			return "", err
		}
		if movie == nil {
			return "This movie is not in the library.", nil
		}
		if err := rc.RescanMovie(movie.ID); err != nil {
			return "", err
		}
		if err := rc.ProcessMonitoredDownloads(); err != nil {
			return "", err
		}
		return fmt.Sprintf("Rescanning %s (%d) and running the import pass. Check diagnose_queue shortly.", movie.Title, movie.Year), nil

	case "tv":
		if sc == nil {
			return "Sonarr is not configured.", nil
		}
		series, err := seriesByTMDB(bridge, sc, tmdbID)
		if err != nil {
			return "", err
		}
		if series == nil {
			return "This show is not in the library.", nil
		}
		if err := sc.RescanSeries(series.ID); err != nil {
			return "", err
		}
		if err := sc.ProcessMonitoredDownloads(); err != nil {
			return "", err
		}
		return fmt.Sprintf("Rescanning %s and running the import pass. Check diagnose_queue shortly.", series.Title), nil

	default:
		return "media_type must be \"movie\" or \"tv\".", nil
	}
}
