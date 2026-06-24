package remediation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// Executor is the ONLY code in the whole feature that mutates Radarr/Sonarr. It
// runs solely from ApproveAction, replaying an admin-approved proposal's stored
// params verbatim — the model is long out of the loop by the time Execute runs.
// Each kind dispatches to a SHARED mcp mutation helper (the same body the manual
// chat tool uses), so the propose→approve→execute path and the manual fix path
// can never drift.
type Executor struct {
	registry *instance.Registry
	bridge   *tmdb.Bridge
	db       *sql.DB
}

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
// to THIS issue. instance_id/download_id are set for auto issues; for user issues
// they are empty and the default instance + a plain queue-existence check are used.
type issueContext struct {
	instanceID string
	downloadID string
}

// Execute replays one approved action against the arr and returns the
// plain-language outcome. issueID scopes the stable-invariant validation gate +
// client resolution. A non-nil error is a DEFINITIVE failure (the caller marks
// the action failed); a benign "not configured / not found" outcome is returned
// as resultText with a nil error.
func (e *Executor) Execute(ctx context.Context, issueID int64, kind ActionKind, params json.RawMessage) (resultText string, err error) {
	ic, err := e.loadIssueContext(issueID)
	if err != nil {
		return "", err
	}

	rc, sc, cc, err := e.clientsFor(ic)
	if err != nil {
		return "", err
	}

	switch kind {
	case ActionGrabRelease:
		var p GrabReleaseParams
		if err := json.Unmarshal(params, &p); err != nil {
			return "", fmt.Errorf("decode grab_release params: %w", err)
		}
		// Stable-invariant gate: if replacing a queue item, confirm it still belongs
		// to this issue's media before removing it. The grab guid itself is NOT
		// re-validated against a live search (indexer results are non-deterministic
		// and age out — re-running search would false-reject the admin-approved
		// release and re-hammer indexers); the arr returns a clean error on a stale
		// guid, which Execute surfaces as a definitive failure.
		if p.QueueIDToReplace > 0 {
			if err := e.validateQueueItem(p.MediaType, p.QueueIDToReplace, ic, rc, sc, cc); err != nil {
				return "", err
			}
		}
		return mcp.GrabReleaseHelper(rc, sc, cc, p.MediaType, p.GUID, p.IndexerID, p.QueueIDToReplace)

	case ActionRemediateQueue:
		var p RemediateQueueParams
		if err := json.Unmarshal(params, &p); err != nil {
			return "", fmt.Errorf("decode remediate_queue params: %w", err)
		}
		if err := e.validateQueueItem(p.MediaType, p.QueueID, ic, rc, sc, cc); err != nil {
			return "", err
		}
		return mcp.RemediateQueueItemHelper(rc, sc, cc, p.MediaType, p.QueueID, p.Action)

	case ActionManualImport:
		var p ManualImportParams
		if err := json.Unmarshal(params, &p); err != nil {
			return "", fmt.Errorf("decode manual_import params: %w", err)
		}
		if err := e.validateQueueItem(p.MediaType, p.QueueID, ic, rc, sc, cc); err != nil {
			return "", err
		}
		return mcp.ExecuteManualImportHelper(rc, sc, cc, p.MediaType, p.QueueID, p.Force)

	case ActionTriggerSearch:
		var p TriggerSearchParams
		if err := json.Unmarshal(params, &p); err != nil {
			return "", fmt.Errorf("decode trigger_search params: %w", err)
		}
		var bookIDs []int
		if p.BookID != 0 {
			bookIDs = []int{p.BookID}
		}
		return mcp.TriggerSearchHelper(e.bridge, rc, sc, cc, p.MediaType, p.TmdbID, p.Season, p.AuthorID, bookIDs)

	case ActionRescan:
		var p RescanParams
		if err := json.Unmarshal(params, &p); err != nil {
			return "", fmt.Errorf("decode rescan params: %w", err)
		}
		return mcp.RescanMediaHelper(e.bridge, rc, sc, cc, p.MediaType, p.TmdbID, p.AuthorID)

	default:
		return "", fmt.Errorf("unknown action kind: %s", kind)
	}
}

// loadIssueContext fetches the issue's instance/download identity for client
// resolution + the validation gate.
func (e *Executor) loadIssueContext(issueID int64) (issueContext, error) {
	var instanceID, downloadID sql.NullString
	err := e.db.QueryRow("SELECT instance_id, download_id FROM issues WHERE id = ?", issueID).
		Scan(&instanceID, &downloadID)
	if err == sql.ErrNoRows {
		return issueContext{}, fmt.Errorf("issue %d not found", issueID)
	}
	if err != nil {
		return issueContext{}, fmt.Errorf("load issue context: %w", err)
	}
	return issueContext{instanceID: instanceID.String, downloadID: downloadID.String}, nil
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

// validateQueueItem is the stable-invariant gate for a queue-targeting action: it
// re-confirms the queue item still exists in the resolved client's queue and,
// when the issue recorded a download_id (auto issue), that the item's download id
// matches — so an approved action can't act on a queue slot that was reassigned
// to a different download since the proposal. Cheap (one detailed-queue fetch)
// and stable. A definitive mismatch returns an error so the action is marked
// failed rather than executing against the wrong item.
func (e *Executor) validateQueueItem(mediaType string, queueID int, ic issueContext, rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client) error {
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
				if ic.downloadID != "" && items[i].DownloadID != "" && items[i].DownloadID != ic.downloadID {
					return fmt.Errorf("queue item %d no longer matches this issue's download (it was reassigned); not executing", queueID)
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
				if ic.downloadID != "" && items[i].DownloadID != "" && items[i].DownloadID != ic.downloadID {
					return fmt.Errorf("queue item %d no longer matches this issue's download (it was reassigned); not executing", queueID)
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
				if ic.downloadID != "" && items[i].DownloadID != "" && items[i].DownloadID != ic.downloadID {
					return fmt.Errorf("queue item %d no longer matches this issue's download (it was reassigned); not executing", queueID)
				}
				return nil
			}
		}
		return fmt.Errorf("queue item %d is no longer in the Chaptarr queue (already handled or removed); not executing", queueID)

	default:
		return fmt.Errorf("media_type must be \"movie\", \"tv\", or \"book\"")
	}
}
