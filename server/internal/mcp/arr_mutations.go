package mcp

import (
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// PartialMutationError reports a compound operation whose first mutation was
// accepted by the arr but whose follow-up failed. Callers must not describe this
// as a clean failure or retry the whole sequence blindly.
type PartialMutationError struct {
	Completed string
	Pending   string
	Err       error
}

func (e *PartialMutationError) Error() string {
	return fmt.Sprintf("%s; %s failed: %v", e.Completed, e.Pending, e.Err)
}

func (e *PartialMutationError) Unwrap() error         { return e.Err }
func (e *PartialMutationError) PartialMutation() bool { return true }

// MutationNotStartedError is a definitive preflight/no-op outcome. It tells the
// approval service that no remote mutation was dispatched, so the action may be
// recorded as failed (never "executed" and never outcome-unknown).
type MutationNotStartedError struct{ Detail string }

func (e *MutationNotStartedError) Error() string            { return e.Detail }
func (e *MutationNotStartedError) MutationNotStarted() bool { return true }

func mutationNotStarted(detail string) (string, error) {
	return "", &MutationNotStartedError{Detail: detail}
}

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
func GrabReleaseHelper(rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType, guid string, indexerID, queueIDToReplace int) (string, error) {
	if guid == "" {
		return mutationNotStarted("guid is required (from search_releases)")
	}
	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		if queueIDToReplace > 0 {
			if err := rc.RemoveQueueItem(queueIDToReplace, true, false, false, false); err != nil {
				return "", err
			}
		}
		if err := rc.GrabRelease(guid, indexerID); err != nil {
			if queueIDToReplace > 0 {
				return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed", queueIDToReplace), Pending: "sending the replacement release", Err: err}
			}
			return "", err
		}
	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		if queueIDToReplace > 0 {
			if err := sc.RemoveQueueItem(queueIDToReplace, true, false, false, false); err != nil {
				return "", err
			}
		}
		if err := sc.GrabRelease(guid, indexerID); err != nil {
			if queueIDToReplace > 0 {
				return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed", queueIDToReplace), Pending: "sending the replacement release", Err: err}
			}
			return "", err
		}
	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		if queueIDToReplace > 0 {
			if err := cc.RemoveQueueItem(queueIDToReplace, true, false, false, false); err != nil {
				return "", err
			}
		}
		if err := cc.GrabRelease(guid, indexerID); err != nil {
			if queueIDToReplace > 0 {
				return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed", queueIDToReplace), Pending: "sending the replacement release", Err: err}
			}
			return "", err
		}
	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
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
func RemoveQueueItemHelper(rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType string, queueID int, blocklist bool) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		if err := rc.RemoveQueueItem(queueID, true, blocklist, false, false); err != nil {
			return "", err
		}
	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		if err := sc.RemoveQueueItem(queueID, true, blocklist, false, false); err != nil {
			return "", err
		}
	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		if err := cc.RemoveQueueItem(queueID, true, blocklist, false, false); err != nil {
			return "", err
		}
	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
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
func RemediateQueueItemHelper(rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType string, queueID int, action string) (string, error) {
	switch action {
	case "remove", "blocklist_search", "change_category":
	default:
		return mutationNotStarted("action must be \"remove\", \"blocklist_search\", or \"change_category\"")
	}

	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		item, err := findRadarrQueueItem(rc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no movie queue item with id %d", queueID))
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
					return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: err}
				}
				return fmt.Sprintf("Removed and blocklisted queue item %d (%s) and started a fresh search for a different release.", queueID, radarrQueueTitle(*item)), nil
			}
			return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: fmt.Errorf("the queue item had no movie id")}
		case "change_category":
			if err := rc.RemoveQueueItem(queueID, false, false, false, true); err != nil {
				return "", err
			}
			return fmt.Sprintf("Handed queue item %d (%s) to the download client's post-import category. It stays in the client for tools like Unpackerr.", queueID, radarrQueueTitle(*item)), nil
		}

	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		item, err := findSonarrQueueItem(sc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no TV queue item with id %d", queueID))
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
					return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: err}
				}
				return fmt.Sprintf("Removed and blocklisted queue item %d (%s) and started a fresh search for a different release.", queueID, sonarrQueueTitle(*item)), nil
			}
			return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: fmt.Errorf("the queue item had no episode id")}
		case "change_category":
			if err := sc.RemoveQueueItem(queueID, false, false, false, true); err != nil {
				return "", err
			}
			return fmt.Sprintf("Handed queue item %d (%s) to the download client's post-import category. It stays in the client for tools like Unpackerr.", queueID, sonarrQueueTitle(*item)), nil
		}

	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		item, err := findChaptarrQueueItem(cc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no book queue item with id %d", queueID))
		}
		switch action {
		case "remove":
			if err := cc.RemoveQueueItem(queueID, true, false, false, false); err != nil {
				return "", err
			}
			return fmt.Sprintf("Removed queue item %d (%s) and deleted the download.", queueID, chaptarrQueueTitle(*item)), nil
		case "blocklist_search":
			if err := cc.RemoveQueueItem(queueID, true, true, false, false); err != nil {
				return "", err
			}
			if item.BookID != 0 {
				if err := cc.TriggerBookSearch([]int{item.BookID}); err != nil {
					return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: err}
				}
				return fmt.Sprintf("Removed and blocklisted queue item %d (%s) and started a fresh search for a different release.", queueID, chaptarrQueueTitle(*item)), nil
			}
			return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: fmt.Errorf("the queue item had no book id")}
		case "change_category":
			if err := cc.RemoveQueueItem(queueID, false, false, false, true); err != nil {
				return "", err
			}
			return fmt.Sprintf("Handed queue item %d (%s) to the download client's post-import category. It stays in the client for tools like Unpackerr.", queueID, chaptarrQueueTitle(*item)), nil
		}

	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
	}
	return mutationNotStarted("no remediation was applied")
}

// ExecuteManualImportHelper imports the manual-import candidates for a queue
// item's download, honoring force (force imports despite permanent rejections).
// Shared body of the execute_manual_import tool and the Executor's manual_import
// kind.
type ManualImportScope struct {
	DownloadID    string
	MovieID       int
	SeriesID      int
	BookID        int
	SeasonNumber  int
	EpisodeNumber int
}

type ManualImportScopeError struct{ Detail string }

func (e *ManualImportScopeError) Error() string            { return e.Detail }
func (e *ManualImportScopeError) MutationNotStarted() bool { return true }

func ExecuteManualImportHelper(rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType string, queueID int, force bool, scope *ManualImportScope) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		item, err := findRadarrQueueItem(rc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no movie queue item with id %d", queueID))
		}
		if item.DownloadID == "" {
			return mutationNotStarted(fmt.Sprintf("queue item %d has no download-client id yet; nothing to import", queueID))
		}
		if scope != nil && scope.DownloadID != "" && item.DownloadID != scope.DownloadID {
			return "", &ManualImportScopeError{Detail: "the queue item now points to a different download; not importing"}
		}
		candidates, err := rc.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return "", err
		}
		var files []radarr.ManualImportFile
		var skipped []string
		scopedCandidates := 0
		for _, c := range candidates {
			if scope != nil && (scope.MovieID <= 0 || c.MovieID != scope.MovieID) {
				skipped = append(skipped, fmt.Sprintf("%s (mapped to a different movie)", c.Name))
				continue
			}
			scopedCandidates++
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
			if scope != nil && len(candidates) > 0 && scopedCandidates == 0 {
				return "", &ManualImportScopeError{Detail: "no manual-import candidate maps exclusively to this issue's movie; not importing"}
			}
			return mutationNotStarted(importSkippedMessage(skipped, force))
		}
		importMode := importModeFor(item.Protocol)
		if err := rc.ExecuteManualImport(files, importMode); err != nil {
			return "", err
		}
		return importResultMessage(len(files), importMode, skipped), nil

	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		item, err := findSonarrQueueItem(sc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no TV queue item with id %d", queueID))
		}
		if item.DownloadID == "" {
			return mutationNotStarted(fmt.Sprintf("queue item %d has no download-client id yet; nothing to import", queueID))
		}
		if scope != nil && scope.DownloadID != "" && item.DownloadID != scope.DownloadID {
			return "", &ManualImportScopeError{Detail: "the queue item now points to a different download; not importing"}
		}
		candidates, err := sc.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return "", err
		}
		var files []sonarr.ManualImportFile
		var skipped []string
		scopedCandidates := 0
		for _, c := range candidates {
			if scope != nil && !manualImportCandidateMatchesTVScope(c, *scope) {
				skipped = append(skipped, fmt.Sprintf("%s (mapped outside this issue's series/episode scope)", c.Name))
				continue
			}
			scopedCandidates++
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
			if scope != nil && len(candidates) > 0 && scopedCandidates == 0 {
				return "", &ManualImportScopeError{Detail: "no manual-import candidate maps exclusively to this issue's series/episode; not importing"}
			}
			return mutationNotStarted(importSkippedMessage(skipped, force))
		}
		importMode := importModeFor(item.Protocol)
		if err := sc.ExecuteManualImport(files, importMode); err != nil {
			return "", err
		}
		return importResultMessage(len(files), importMode, skipped), nil

	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		item, err := findChaptarrQueueItem(cc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no book queue item with id %d", queueID))
		}
		if item.DownloadID == "" {
			return mutationNotStarted(fmt.Sprintf("queue item %d has no download-client id yet; nothing to import", queueID))
		}
		if scope != nil && scope.DownloadID != "" && item.DownloadID != scope.DownloadID {
			return "", &ManualImportScopeError{Detail: "the queue item now points to a different download; not importing"}
		}
		candidates, err := cc.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return "", err
		}
		var files []chaptarr.ManualImportFile
		var skipped []string
		scopedCandidates := 0
		for _, c := range candidates {
			if scope != nil && (scope.BookID <= 0 || c.BookID != scope.BookID) {
				skipped = append(skipped, fmt.Sprintf("%s (mapped to a different book)", c.Name))
				continue
			}
			scopedCandidates++
			rejections := toRejectionViews(c.Rejections)
			if !force && hasPermanentRejection(rejections) {
				skipped = append(skipped, fmt.Sprintf("%s (%s)", c.Name, formatRejections(rejections)))
				continue
			}
			if c.BookID == 0 {
				skipped = append(skipped, fmt.Sprintf("%s (not matched to a book)", c.Name))
				continue
			}
			files = append(files, chaptarr.ManualImportFile{
				Path:         c.Path,
				FolderName:   c.FolderName,
				AuthorID:     c.AuthorID,
				BookID:       c.BookID,
				Quality:      c.Quality,
				ReleaseGroup: c.ReleaseGroup,
				DownloadID:   c.DownloadID,
			})
		}
		if len(files) == 0 {
			if scope != nil && len(candidates) > 0 && scopedCandidates == 0 {
				return "", &ManualImportScopeError{Detail: "no manual-import candidate maps exclusively to this issue's book; not importing"}
			}
			return mutationNotStarted(importSkippedMessage(skipped, force))
		}
		// Chaptarr's ManualImport command sets importMode itself (auto); the
		// helper still reports the protocol-derived mode (copy for torrent, else
		// move) so the result message matches the movie/TV path.
		importMode := importModeFor(item.Protocol)
		if err := cc.ExecuteManualImport(files); err != nil {
			return "", err
		}
		return importResultMessage(len(files), importMode, skipped), nil

	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
	}
}

func manualImportCandidateMatchesTVScope(candidate sonarr.ManualImportCandidate, scope ManualImportScope) bool {
	if scope.SeriesID <= 0 || candidate.SeriesID != scope.SeriesID || len(candidate.Episodes) == 0 {
		return false
	}
	for _, episode := range candidate.Episodes {
		if episode.ID == 0 {
			return false
		}
		if (scope.SeasonNumber > 0 || scope.EpisodeNumber > 0) && episode.SeasonNumber != scope.SeasonNumber {
			return false
		}
		if scope.EpisodeNumber > 0 && episode.EpisodeNumber != scope.EpisodeNumber {
			return false
		}
	}
	return true
}

// TriggerSearchHelper kicks off an automatic search for a movie, a whole series,
// a single season, or one episode. For books the search targets
// specific bookIDs when present, otherwise every monitored book of authorID;
// books carry no TMDB id, so tmdbID/seasonNumber are unused on the book path
// (and authorID/bookIDs are unused on the movie/TV paths). Shared body of the
// trigger_search tool and the Executor's trigger_search kind.
func TriggerSearchHelper(bridge *tmdb.Bridge, rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType string, tmdbID int, seasonNumber, episodeNumber *int, authorID int, bookIDs []int) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		movie, err := rc.GetMovieByTMDB(tmdbID)
		if err != nil {
			return "", err
		}
		if movie == nil {
			return mutationNotStarted("this movie is not in the library yet")
		}
		if err := rc.TriggerMoviesSearch([]int{movie.ID}); err != nil {
			return "", err
		}
		return fmt.Sprintf("Search started for %s (%d). Check get_queue in a bit to see if a release was grabbed.", movie.Title, movie.Year), nil

	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		series, err := seriesByTMDB(bridge, sc, tmdbID)
		if err != nil {
			return "", err
		}
		if series == nil {
			return mutationNotStarted("this show is not in the library yet")
		}
		if episodeNumber != nil {
			if seasonNumber == nil {
				return mutationNotStarted("season_number is required for an episode search")
			}
			episodes, err := sc.GetEpisodes(series.ID, *seasonNumber)
			if err != nil {
				return "", err
			}
			for _, episode := range episodes {
				if episode.EpisodeNumber != *episodeNumber {
					continue
				}
				if err := sc.TriggerEpisodeSearch([]int{episode.ID}); err != nil {
					return "", err
				}
				return fmt.Sprintf("Search started for %s S%02dE%02d. Check get_queue in a bit to see if a release was grabbed.", series.Title, *seasonNumber, *episodeNumber), nil
			}
			return mutationNotStarted(fmt.Sprintf("episode S%02dE%02d was not found in %s", *seasonNumber, *episodeNumber, series.Title))
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

	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		if len(bookIDs) > 0 {
			if err := cc.TriggerBookSearch(bookIDs); err != nil {
				return "", err
			}
			return fmt.Sprintf("Search started for %d book(s). Check get_queue in a bit to see if releases were grabbed.", len(bookIDs)), nil
		}
		if authorID == 0 {
			return mutationNotStarted("trigger_search for a book requires author_id or book_id")
		}
		if err := cc.TriggerAuthorSearch(authorID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Search started for all monitored books of author %d. Check get_queue in a bit to see if releases were grabbed.", authorID), nil

	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
	}
}

// RescanMediaHelper rescans a movie or series on disk and runs the import pass
// (ProcessMonitoredDownloads). For books it rescans authorID (books carry no
// TMDB id, so tmdbID is unused on the book path and authorID is unused on the
// movie/TV paths). Shared body of the rescan_media tool and the Executor's
// rescan kind.
func RescanMediaHelper(bridge *tmdb.Bridge, rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType string, tmdbID, authorID int) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		movie, err := rc.GetMovieByTMDB(tmdbID)
		if err != nil {
			return "", err
		}
		if movie == nil {
			return mutationNotStarted("this movie is not in the library")
		}
		if err := rc.RescanMovie(movie.ID); err != nil {
			return "", err
		}
		if err := rc.ProcessMonitoredDownloads(); err != nil {
			return "", &PartialMutationError{Completed: fmt.Sprintf("a rescan of movie %d was started", movie.ID), Pending: "starting the monitored-download import pass", Err: err}
		}
		return fmt.Sprintf("Rescanning %s (%d) and running the import pass. Check diagnose_queue shortly.", movie.Title, movie.Year), nil

	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		series, err := seriesByTMDB(bridge, sc, tmdbID)
		if err != nil {
			return "", err
		}
		if series == nil {
			return mutationNotStarted("this show is not in the library")
		}
		if err := sc.RescanSeries(series.ID); err != nil {
			return "", err
		}
		if err := sc.ProcessMonitoredDownloads(); err != nil {
			return "", &PartialMutationError{Completed: fmt.Sprintf("a rescan of series %d was started", series.ID), Pending: "starting the monitored-download import pass", Err: err}
		}
		return fmt.Sprintf("Rescanning %s and running the import pass. Check diagnose_queue shortly.", series.Title), nil

	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		if authorID == 0 {
			return mutationNotStarted("rescan for a book requires author_id")
		}
		if err := cc.RescanAuthor(authorID); err != nil {
			return "", err
		}
		if err := cc.ProcessMonitoredDownloads(); err != nil {
			return "", &PartialMutationError{Completed: fmt.Sprintf("a rescan of author %d was started", authorID), Pending: "starting the monitored-download import pass", Err: err}
		}
		return fmt.Sprintf("Rescanning author %d and running the import pass. Check diagnose_queue shortly.", authorID), nil

	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
	}
}
