package remediation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// Executor is the ONLY code in the whole feature that mutates Radarr/Sonarr. It
// runs solely from ApproveAction. It revalidates an admin-approved proposal's
// stored scope and metadata against fresh arr state; release capabilities are
// resolved only in memory immediately before dispatch. The model is long out of
// the loop by the time Execute runs. Each kind dispatches to a SHARED mcp mutation
// helper (the same body the manual chat tool uses), so the propose→approve→execute
// path and the manual fix path can never drift.
type Executor struct {
	registry *instance.Registry
	bridge   *tmdb.Bridge
	db       *sql.DB
}

// mutationNotStartedError distinguishes a failed invariant/read-only preflight
// from an ambiguous error returned after an arr mutation was dispatched. The
// approval service can safely mark this failed: no remote state changed.
type mutationNotStartedError struct{ err error }

func (e *mutationNotStartedError) Error() string            { return e.err.Error() }
func (e *mutationNotStartedError) Unwrap() error            { return e.err }
func (e *mutationNotStartedError) MutationNotStarted() bool { return true }
func beforeMutation(err error) error                        { return &mutationNotStartedError{err: err} }

// NewExecutor builds the Executor over the instance registry (for resolving the
// right arr client), the id bridge (tmdb<->tvdb resolution for search/rescan),
// and the database (for the stable-invariant validation gate).
func NewExecutor(registry *instance.Registry, bridge *tmdb.Bridge, db *sql.DB) *Executor {
	return &Executor{registry: registry, bridge: bridge, db: db}
}

// chaptarrQueuePage / chaptarrQueuePageSize drive the paginated
// chaptarr.GetQueueDetailed call used by the book validation gate (the client
// loops from this page until every record is fetched). Radarr/Sonarr expose a
// non-paginated GetQueueDetailed, so these are book-only.
const (
	chaptarrQueuePage     = 1
	chaptarrQueuePageSize = 100
)

// issueContext is the stable, cheap-to-fetch identity of an issue's media used to
// resolve the arr client and to validate a queue-targeting action still applies
// to THIS issue. User issues usually have no exact queue/download identity, so
// their TMDB/TVDB and episode scope is the mandatory execution-time invariant.
type issueContext struct {
	instanceID    string
	downloadID    string
	mediaType     string
	tmdbID        int
	tvdbID        int
	seasonNumber  int
	episodeNumber int
	arrQueueID    int
}

// Execute replays one approved action against the arr and returns the
// plain-language outcome. issueID scopes the stable-invariant validation gate +
// client resolution. A non-nil error is a DEFINITIVE failure (the caller marks
// the action failed); a benign "not configured / not found" outcome is returned
// as resultText with a nil error.
func (e *Executor) Execute(ctx context.Context, issueID int64, kind ActionKind, params json.RawMessage) (resultText string, err error) {
	ic, err := e.loadIssueContext(issueID)
	if err != nil {
		return "", beforeMutation(err)
	}

	rc, sc, cc, err := e.clientsFor(ic)
	if err != nil {
		return "", beforeMutation(err)
	}

	switch kind {
	case ActionGrabRelease:
		var p GrabReleaseParams
		if err := json.Unmarshal(params, &p); err != nil {
			return "", beforeMutation(fmt.Errorf("decode grab_release params: %w", err))
		}
		if err := requireConfiguredClient(p.MediaType, rc, sc, cc); err != nil {
			return "", beforeMutation(err)
		}
		// Stable-invariant gate: if replacing a queue item, confirm it still belongs
		// to this issue's media before removing it. Then repeat an issue-scoped
		// release search and require the exact GUID/indexer tuple immediately before
		// dispatch, so stale or invented release identifiers fail before mutation.
		if p.QueueIDToReplace > 0 {
			if err := e.validateQueueItem(p.MediaType, p.QueueIDToReplace, ic, rc, sc, cc); err != nil {
				return "", beforeMutation(err)
			}
		}
		liveGUID, err := e.validateGrabReleaseCandidate(p, ic, rc, sc, cc)
		if err != nil {
			return "", beforeMutation(err)
		}
		// The model/API may only have seen a credential-scrubbed reference. Use
		// the raw GUID from this fresh scoped search for the immediate dispatch;
		// never persist or return that capability.
		p.GUID = liveGUID
		return mcp.GrabReleaseHelper(rc, sc, cc, p.MediaType, p.GUID, p.IndexerID, p.QueueIDToReplace)

	case ActionRemediateQueue:
		var p RemediateQueueParams
		if err := json.Unmarshal(params, &p); err != nil {
			return "", beforeMutation(fmt.Errorf("decode remediate_queue params: %w", err))
		}
		if err := requireConfiguredClient(p.MediaType, rc, sc, cc); err != nil {
			return "", beforeMutation(err)
		}
		if err := e.validateQueueItem(p.MediaType, p.QueueID, ic, rc, sc, cc); err != nil {
			return "", beforeMutation(err)
		}
		return mcp.RemediateQueueItemHelper(rc, sc, cc, p.MediaType, p.QueueID, p.Action)

	case ActionManualImport:
		var p ManualImportParams
		if err := json.Unmarshal(params, &p); err != nil {
			return "", beforeMutation(fmt.Errorf("decode manual_import params: %w", err))
		}
		if err := requireConfiguredClient(p.MediaType, rc, sc, cc); err != nil {
			return "", beforeMutation(err)
		}
		if err := e.validateQueueItem(p.MediaType, p.QueueID, ic, rc, sc, cc); err != nil {
			return "", beforeMutation(err)
		}
		importScope, err := e.manualImportScope(p.MediaType, p.QueueID, ic, rc, sc, cc)
		if err != nil {
			return "", beforeMutation(err)
		}
		return mcp.ExecuteManualImportHelper(rc, sc, cc, p.MediaType, p.QueueID, p.Force, importScope)

	case ActionTriggerSearch:
		var p TriggerSearchParams
		if err := json.Unmarshal(params, &p); err != nil {
			return "", beforeMutation(fmt.Errorf("decode trigger_search params: %w", err))
		}
		if err := requireConfiguredClient(p.MediaType, rc, sc, cc); err != nil {
			return "", beforeMutation(err)
		}
		var bookIDs []int
		if p.BookID != 0 {
			bookIDs = []int{p.BookID}
		}
		return mcp.TriggerSearchHelper(e.bridge, rc, sc, cc, p.MediaType, p.TmdbID, p.Season, p.Episode, p.AuthorID, bookIDs)

	case ActionRescan:
		var p RescanParams
		if err := json.Unmarshal(params, &p); err != nil {
			return "", beforeMutation(fmt.Errorf("decode rescan params: %w", err))
		}
		if err := requireConfiguredClient(p.MediaType, rc, sc, cc); err != nil {
			return "", beforeMutation(err)
		}
		return mcp.RescanMediaHelper(e.bridge, rc, sc, cc, p.MediaType, p.TmdbID, p.AuthorID)

	default:
		return "", fmt.Errorf("unknown action kind: %s", kind)
	}
}

func requireConfiguredClient(mediaType string, rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client) error {
	switch mediaType {
	case "movie":
		if rc == nil {
			return fmt.Errorf("the issue's Radarr instance is not configured or no longer exists")
		}
	case "tv":
		if sc == nil {
			return fmt.Errorf("the issue's Sonarr instance is not configured or no longer exists")
		}
	case "book":
		if cc == nil {
			return fmt.Errorf("the issue's Chaptarr instance is not configured or no longer exists")
		}
	default:
		return fmt.Errorf("unsupported media_type %q", mediaType)
	}
	return nil
}

// loadIssueContext fetches the issue's instance/download identity for client
// resolution + the validation gate.
func (e *Executor) loadIssueContext(issueID int64) (issueContext, error) {
	var instanceID, downloadID sql.NullString
	var tvdbID, arrQueueID sql.NullInt64
	var ic issueContext
	err := e.db.QueryRow(
		`SELECT instance_id, download_id, media_type, tmdb_id, tvdb_id,
		        season_number, episode_number, arr_queue_id
		 FROM issues WHERE id = ?`, issueID,
	).Scan(&instanceID, &downloadID, &ic.mediaType, &ic.tmdbID, &tvdbID,
		&ic.seasonNumber, &ic.episodeNumber, &arrQueueID)
	if err == sql.ErrNoRows {
		return issueContext{}, fmt.Errorf("issue %d not found", issueID)
	}
	if err != nil {
		return issueContext{}, fmt.Errorf("load issue context: %w", err)
	}
	ic.instanceID = instanceID.String
	ic.downloadID = downloadID.String
	ic.tvdbID = int(tvdbID.Int64)
	ic.arrQueueID = int(arrQueueID.Int64)
	return ic, nil
}

// clientsFor resolves the Radarr, Sonarr, and Chaptarr clients for the issue. If
// the issue carries a specific instance_id, that instance's client is used;
// otherwise the default instance for each service is resolved. A nil client is
// fine — the shared helpers return a "<service> is not configured" message for
// the wrong media_type. An error is only returned when a NAMED instance fails to
// resolve as ANY of the three service kinds.
func (e *Executor) clientsFor(ic issueContext) (*radarr.Client, *sonarr.Client, *chaptarr.Client, error) {
	if e.registry == nil {
		return nil, nil, nil, fmt.Errorf("instance registry not configured")
	}
	if ic.instanceID != "" {
		// A specific instance was recorded (auto issue). Try it as a Radarr, a
		// Sonarr, and a Chaptarr instance; whichever matches yields a client, the
		// others stay nil.
		rc, rErr := e.registry.GetRadarrClient(ic.instanceID)
		sc, sErr := e.registry.GetSonarrClient(ic.instanceID)
		cc, cErr := e.registry.GetChaptarrClient(ic.instanceID)
		if rErr != nil && sErr != nil && cErr != nil {
			return nil, nil, nil, fmt.Errorf("resolve instance %q: %v / %v / %v", ic.instanceID, rErr, sErr, cErr)
		}
		return rc, sc, cc, nil
	}
	// User issue with no instance: fall back to the default Radarr + Sonarr +
	// Chaptarr.
	rc, _, _ := e.registry.GetDefaultRadarrClient()
	sc, _, _ := e.registry.GetDefaultSonarrClient()
	cc, _, _ := e.registry.GetDefaultChaptarrClient()
	return rc, sc, cc, nil
}

// validateQueueItem is the stable-invariant gate for a queue-targeting action.
// Before any mutation it re-confirms the row, download identity (when recorded),
// and authoritative media scope. For user issues without an arr_queue_id this
// title/season/episode check is what prevents a model-selected queue id from
// targeting an unrelated download.
func (e *Executor) validateQueueItem(mediaType string, queueID int, ic issueContext, rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client) error {
	if queueID <= 0 {
		return fmt.Errorf("queue_id must be positive")
	}
	if ic.mediaType != "" && mediaType != ic.mediaType {
		return fmt.Errorf("queue media_type %q does not match issue media_type %q; not executing", mediaType, ic.mediaType)
	}
	if ic.arrQueueID > 0 && queueID != ic.arrQueueID {
		return fmt.Errorf("queue item %d does not match this issue's queue item %d; not executing", queueID, ic.arrQueueID)
	}
	switch mediaType {
	case "movie":
		if rc == nil {
			return nil // not configured; the helper returns a benign message.
		}
		items, err := rc.GetQueueDetailed()
		if err != nil {
			return fmt.Errorf("validate queue item: %w", err)
		}
		for i := range items {
			if items[i].ID == queueID {
				if err := validateDownloadIdentity(queueID, ic.downloadID, items[i].DownloadID); err != nil {
					return err
				}
				if err := validateRadarrMediaIdentity(queueID, ic, items[i], rc); err != nil {
					return err
				}
				return nil
			}
		}
		return fmt.Errorf("queue item %d is no longer in the Radarr queue (already handled or removed); not executing", queueID)

	case "tv":
		if sc == nil {
			return nil
		}
		items, err := sc.GetQueueDetailed()
		if err != nil {
			return fmt.Errorf("validate queue item: %w", err)
		}
		for i := range items {
			if items[i].ID == queueID {
				if err := validateDownloadIdentity(queueID, ic.downloadID, items[i].DownloadID); err != nil {
					return err
				}
				if err := e.validateSonarrMediaIdentity(queueID, ic, items[i], sc); err != nil {
					return err
				}
				return nil
			}
		}
		return fmt.Errorf("queue item %d is no longer in the Sonarr queue (already handled or removed); not executing", queueID)

	case "book":
		if cc == nil {
			return nil
		}
		items, err := cc.GetQueueDetailed(chaptarrQueuePage, chaptarrQueuePageSize)
		if err != nil {
			return fmt.Errorf("validate queue item: %w", err)
		}
		for i := range items {
			if items[i].ID == queueID {
				if err := validateDownloadIdentity(queueID, ic.downloadID, items[i].DownloadID); err != nil {
					return err
				}
				// User-reported issues currently support movie/TV only. Book queue
				// actions therefore require the detector's exact queue + download
				// identity; there is no user-supplied book id to fall back to.
				if ic.arrQueueID == 0 || ic.downloadID == "" {
					return fmt.Errorf("queue item %d cannot be proven to belong to this book issue; not executing", queueID)
				}
				return nil
			}
		}
		return fmt.Errorf("queue item %d is no longer in the Chaptarr queue (already handled or removed); not executing", queueID)

	default:
		return fmt.Errorf("media_type must be \"movie\", \"tv\", or \"book\"")
	}
}

func validateDownloadIdentity(queueID int, expected, actual string) error {
	if expected != "" && actual != expected {
		return fmt.Errorf("queue item %d no longer matches this issue's download (it was reassigned); not executing", queueID)
	}
	return nil
}

func validateRadarrMediaIdentity(queueID int, ic issueContext, item radarr.DetailedQueueItem, client *radarr.Client) error {
	if ic.tmdbID <= 0 {
		if ic.arrQueueID > 0 && ic.downloadID != "" {
			return nil
		}
		return fmt.Errorf("queue item %d cannot be proven to belong to this movie issue; not executing", queueID)
	}
	actual := 0
	if item.Movie != nil {
		actual = item.Movie.TmdbID
	}
	if actual == 0 && item.MovieID > 0 {
		movie, err := client.GetMovie(item.MovieID)
		if err != nil {
			return fmt.Errorf("validate queue item %d movie identity: %w", queueID, err)
		}
		if movie != nil {
			actual = movie.TmdbID
		}
	}
	if actual == 0 {
		return fmt.Errorf("queue item %d has no verifiable TMDB identity; not executing", queueID)
	}
	if actual != ic.tmdbID {
		return fmt.Errorf("queue item %d belongs to TMDB %d, not this issue's TMDB %d; not executing", queueID, actual, ic.tmdbID)
	}
	return nil
}

func (e *Executor) validateSonarrMediaIdentity(queueID int, ic issueContext, item sonarr.DetailedQueueItem, client *sonarr.Client) error {
	if ic.tmdbID <= 0 && ic.tvdbID <= 0 {
		if ic.arrQueueID > 0 && ic.downloadID != "" && ic.seasonNumber == 0 && ic.episodeNumber == 0 {
			return nil
		}
		return fmt.Errorf("queue item %d cannot be proven to belong to this TV issue; not executing", queueID)
	}

	var actualTMDB, actualTVDB int
	seriesID := item.SeriesID
	if item.Series != nil {
		actualTMDB = item.Series.TmdbID
		actualTVDB = item.Series.TvdbID
		if seriesID == 0 {
			seriesID = item.Series.ID
		}
	}
	if (actualTMDB == 0 && ic.tmdbID > 0) || (actualTVDB == 0 && ic.tvdbID > 0) {
		if seriesID > 0 {
			series, err := client.GetSeries(seriesID)
			if err != nil {
				return fmt.Errorf("validate queue item %d series identity: %w", queueID, err)
			}
			if series != nil {
				if actualTMDB == 0 {
					actualTMDB = series.TmdbID
				}
				if actualTVDB == 0 {
					actualTVDB = series.TvdbID
				}
			}
		}
	}

	expectedTVDB := ic.tvdbID
	if expectedTVDB == 0 && ic.tmdbID > 0 && actualTMDB == 0 && e.bridge != nil {
		if resolved, err := e.bridge.ResolveTVDBID(ic.tmdbID); err == nil && resolved != nil {
			expectedTVDB = resolved.TVDBID
		}
	}
	matched := false
	if ic.tmdbID > 0 && actualTMDB > 0 {
		if actualTMDB != ic.tmdbID {
			return fmt.Errorf("queue item %d belongs to TMDB %d, not this issue's TMDB %d; not executing", queueID, actualTMDB, ic.tmdbID)
		}
		matched = true
	}
	if expectedTVDB > 0 && actualTVDB > 0 {
		if actualTVDB != expectedTVDB {
			return fmt.Errorf("queue item %d belongs to TVDB %d, not this issue's TVDB %d; not executing", queueID, actualTVDB, expectedTVDB)
		}
		matched = true
	}
	if !matched {
		return fmt.Errorf("queue item %d has no verifiable series identity; not executing", queueID)
	}

	if ic.seasonNumber > 0 {
		if item.Episode == nil || item.Episode.SeasonNumber != ic.seasonNumber {
			return fmt.Errorf("queue item %d does not match this issue's season %d; not executing", queueID, ic.seasonNumber)
		}
	}
	if ic.episodeNumber > 0 {
		if item.Episode == nil || item.Episode.SeasonNumber != ic.seasonNumber || item.Episode.EpisodeNumber != ic.episodeNumber {
			return fmt.Errorf("queue item %d does not match this issue's episode S%02dE%02d; not executing", queueID, ic.seasonNumber, ic.episodeNumber)
		}
	}
	return nil
}

// validateGrabReleaseCandidate performs a fresh interactive search scoped from
// the issue (or its already-validated replacement queue row) and requires the
// exact GUID/indexer tuple to be present. A GUID is otherwise just opaque model
// input: sending it directly would let a compromised model grab a release for a
// different title. Search-result expiry now fails closed before any removal or
// grab is dispatched.
func (e *Executor) validateGrabReleaseCandidate(p GrabReleaseParams, ic issueContext, rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client) (string, error) {
	switch p.MediaType {
	case "movie":
		movieID := 0
		if p.QueueIDToReplace > 0 {
			items, err := rc.GetQueueDetailed()
			if err != nil {
				return "", fmt.Errorf("re-read replacement queue item: %w", err)
			}
			for _, item := range items {
				if item.ID == p.QueueIDToReplace {
					movieID = item.MovieID
					if movieID == 0 && item.Movie != nil {
						movieID = item.Movie.ID
					}
					break
				}
			}
		}
		if movieID == 0 && ic.tmdbID > 0 {
			movie, err := rc.GetMovieByTMDB(ic.tmdbID)
			if err != nil {
				return "", fmt.Errorf("resolve issue movie for release validation: %w", err)
			}
			if movie != nil {
				movieID = movie.ID
			}
		}
		if movieID == 0 {
			return "", fmt.Errorf("cannot establish the issue's Radarr movie for release validation; not executing")
		}
		releases, err := rc.SearchReleases(movieID)
		if err != nil {
			return "", fmt.Errorf("refresh scoped movie releases: %w", err)
		}
		for _, release := range releases {
			if release.IndexerID == p.IndexerID && releaseReferenceMatches(p.GUID, release.GUID) {
				if err := validateObservedReleaseMetadata(p, release.Title, release.Quality.Quality.Name,
					release.Size, release.Protocol, release.Indexer, release.Rejected, release.Rejections); err != nil {
					return "", err
				}
				return release.GUID, nil
			}
		}

	case "tv":
		seriesID, episodeID, seasonNumber := 0, 0, ic.seasonNumber
		if p.QueueIDToReplace > 0 {
			items, err := sc.GetQueueDetailed()
			if err != nil {
				return "", fmt.Errorf("re-read replacement queue item: %w", err)
			}
			for _, item := range items {
				if item.ID != p.QueueIDToReplace {
					continue
				}
				seriesID, episodeID = item.SeriesID, item.EpisodeID
				if item.Series != nil && seriesID == 0 {
					seriesID = item.Series.ID
				}
				if item.Episode != nil {
					if episodeID == 0 {
						episodeID = item.Episode.ID
					}
					if seasonNumber == 0 {
						seasonNumber = item.Episode.SeasonNumber
					}
				}
				break
			}
		}
		if seriesID == 0 {
			series, err := e.resolveIssueSeries(ic, sc)
			if err != nil {
				return "", err
			}
			if series != nil {
				seriesID = series.ID
			}
		}
		if seriesID == 0 {
			return "", fmt.Errorf("cannot establish the issue's Sonarr series for release validation; not executing")
		}
		if episodeID == 0 && ic.episodeNumber > 0 {
			if seasonNumber < 0 {
				return "", fmt.Errorf("an episode-scoped release requires an authoritative season; not executing")
			}
			episodes, err := sc.GetEpisodes(seriesID, seasonNumber)
			if err != nil {
				return "", fmt.Errorf("resolve issue episode for release validation: %w", err)
			}
			for _, episode := range episodes {
				if episode.EpisodeNumber == ic.episodeNumber {
					episodeID = episode.ID
					break
				}
			}
			if episodeID == 0 {
				return "", fmt.Errorf("cannot establish issue episode S%02dE%02d for release validation; not executing", seasonNumber, ic.episodeNumber)
			}
		}

		var releases []sonarr.Release
		var err error
		if episodeID > 0 {
			releases, err = sc.SearchEpisodeReleases(episodeID)
		} else if seasonNumber > 0 {
			releases, err = sc.SearchReleases(seriesID, seasonNumber)
		} else {
			return "", fmt.Errorf("whole-series release validation is ambiguous; narrow the issue to a season or episode before grabbing")
		}
		if err != nil {
			return "", fmt.Errorf("refresh scoped TV releases: %w", err)
		}
		for _, release := range releases {
			if release.IndexerID == p.IndexerID && releaseReferenceMatches(p.GUID, release.GUID) {
				if err := validateObservedReleaseMetadata(p, release.Title, release.Quality.Quality.Name,
					release.Size, release.Protocol, release.Indexer, release.Rejected, release.Rejections); err != nil {
					return "", err
				}
				return release.GUID, nil
			}
		}

	case "book":
		// Book issues do not yet persist a durable author/book id. Proposals of
		// this kind are rejected earlier; retain this fail-closed guard in case a
		// legacy row reaches the executor.
		return "", fmt.Errorf("book release grabs are disabled until issues carry an authoritative book id; not executing")

	default:
		return "", fmt.Errorf("unsupported media_type %q", p.MediaType)
	}
	return "", fmt.Errorf("the approved release is not present in a fresh search scoped to this issue; not executing")
}

func releaseReferenceMatches(reference, liveGUID string) bool {
	redactedLive := secrets.RedactText(liveGUID)
	return reference == liveGUID ||
		reference == redactedLive ||
		reference == releaseGUIDFingerprint(liveGUID) ||
		reference == releaseGUIDFingerprint(redactedLive)
}

func validateObservedReleaseMetadata(p GrabReleaseParams, title, quality string, size int64, protocol, indexer string, rejected bool, rejections []string) error {
	safeRejections := make([]string, len(rejections))
	for i, rejection := range rejections {
		safeRejections[i] = secrets.RedactText(rejection)
	}
	if p.ReleaseTitle != secrets.RedactText(title) ||
		p.Quality != secrets.RedactText(quality) ||
		p.Size != size ||
		p.Protocol != secrets.RedactText(protocol) ||
		p.Indexer != secrets.RedactText(indexer) ||
		p.Rejected != rejected ||
		!slices.Equal(p.Rejections, safeRejections) {
		return fmt.Errorf("the release metadata changed since the proposal; review a fresh candidate before executing")
	}
	return nil
}

func (e *Executor) resolveIssueSeries(ic issueContext, client *sonarr.Client) (*sonarr.Series, error) {
	if ic.tvdbID > 0 {
		series, err := client.GetSeriesByTVDB(ic.tvdbID)
		if err != nil {
			return nil, fmt.Errorf("resolve issue series by TVDB: %w", err)
		}
		if series != nil {
			if ic.tmdbID > 0 && series.TmdbID > 0 && series.TmdbID != ic.tmdbID {
				return nil, fmt.Errorf("resolved Sonarr series TMDB %d does not match issue TMDB %d", series.TmdbID, ic.tmdbID)
			}
			return series, nil
		}
	}
	if ic.tmdbID > 0 && e.bridge != nil {
		if resolved, err := e.bridge.ResolveTVDBID(ic.tmdbID); err == nil && resolved != nil && resolved.TVDBID > 0 {
			series, lookupErr := client.GetSeriesByTVDB(resolved.TVDBID)
			if lookupErr != nil {
				return nil, fmt.Errorf("resolve issue series from TMDB bridge: %w", lookupErr)
			}
			if series != nil {
				return series, nil
			}
		}
	}
	all, err := client.GetAllSeries()
	if err != nil {
		return nil, fmt.Errorf("scan Sonarr library for issue series: %w", err)
	}
	for i := range all {
		if (ic.tmdbID > 0 && all[i].TmdbID == ic.tmdbID) || (ic.tvdbID > 0 && all[i].TvdbID == ic.tvdbID) {
			return &all[i], nil
		}
	}
	return nil, nil
}

func (e *Executor) manualImportScope(mediaType string, queueID int, ic issueContext, rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client) (*mcp.ManualImportScope, error) {
	scope := &mcp.ManualImportScope{
		SeasonNumber:  ic.seasonNumber,
		EpisodeNumber: ic.episodeNumber,
	}
	switch mediaType {
	case "movie":
		items, err := rc.GetQueueDetailed()
		if err != nil {
			return nil, fmt.Errorf("re-read queue before manual import: %w", err)
		}
		for _, item := range items {
			if item.ID != queueID {
				continue
			}
			if err := validateDownloadIdentity(queueID, ic.downloadID, item.DownloadID); err != nil {
				return nil, err
			}
			if err := validateRadarrMediaIdentity(queueID, ic, item, rc); err != nil {
				return nil, err
			}
			scope.DownloadID, scope.MovieID = item.DownloadID, item.MovieID
			if scope.MovieID == 0 && item.Movie != nil {
				scope.MovieID = item.Movie.ID
			}
			if scope.MovieID <= 0 {
				return nil, fmt.Errorf("queue item %d has no verifiable Radarr movie id; not importing", queueID)
			}
			return scope, nil
		}
	case "tv":
		items, err := sc.GetQueueDetailed()
		if err != nil {
			return nil, fmt.Errorf("re-read queue before manual import: %w", err)
		}
		for _, item := range items {
			if item.ID != queueID {
				continue
			}
			if err := validateDownloadIdentity(queueID, ic.downloadID, item.DownloadID); err != nil {
				return nil, err
			}
			if err := e.validateSonarrMediaIdentity(queueID, ic, item, sc); err != nil {
				return nil, err
			}
			scope.DownloadID, scope.SeriesID = item.DownloadID, item.SeriesID
			if scope.SeriesID == 0 && item.Series != nil {
				scope.SeriesID = item.Series.ID
			}
			if scope.SeriesID <= 0 {
				return nil, fmt.Errorf("queue item %d has no verifiable Sonarr series id; not importing", queueID)
			}
			return scope, nil
		}
	case "book":
		items, err := cc.GetQueueDetailed(chaptarrQueuePage, chaptarrQueuePageSize)
		if err != nil {
			return nil, fmt.Errorf("re-read queue before manual import: %w", err)
		}
		for _, item := range items {
			if item.ID != queueID {
				continue
			}
			if err := validateDownloadIdentity(queueID, ic.downloadID, item.DownloadID); err != nil {
				return nil, err
			}
			if ic.arrQueueID != queueID || ic.downloadID == "" {
				return nil, fmt.Errorf("book manual import lacks exact detector identity; not importing")
			}
			scope.DownloadID, scope.BookID = item.DownloadID, item.BookID
			if scope.BookID <= 0 {
				return nil, fmt.Errorf("queue item %d has no verifiable Chaptarr book id; not importing", queueID)
			}
			return scope, nil
		}
	default:
		return nil, fmt.Errorf("unsupported media_type %q", mediaType)
	}
	return nil, fmt.Errorf("queue item %d disappeared before manual import; not importing", queueID)
}
