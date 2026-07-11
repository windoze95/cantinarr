package remediation

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// This file owns the typed shape of every proposable action: its params schema,
// validation, and the canonical fingerprint used for at-most-once execution. The
// agent proposes a {kind, params}; the server validates and canonicalizes it here,
// then fingerprints and stores that immutable proposal. Release capabilities are
// replaced by one-way references before persistence. On approval the Executor
// revalidates the same proposal against fresh arr state and resolves any release
// capability only in memory for the immediate dispatch. The model never touches
// the params again after proposing.

// Typed params per ActionKind. Only the fields the Executor replays are modeled;
// unknown JSON fields are rejected by validation so a hijacked model can't smuggle
// extra arguments past the schema.

// GrabReleaseParams downloads a specific release, optionally replacing a queue item.
type GrabReleaseParams struct {
	MediaType        string   `json:"media_type"`
	GUID             string   `json:"guid"`
	IndexerID        int      `json:"indexer_id"`
	QueueIDToReplace int      `json:"queue_id_to_replace,omitempty"`
	ReleaseTitle     string   `json:"release_title"`
	Quality          string   `json:"quality,omitempty"`
	Size             int64    `json:"size"`
	Protocol         string   `json:"protocol"`
	Indexer          string   `json:"indexer"`
	Rejected         bool     `json:"rejected,omitempty"`
	Rejections       []string `json:"rejections,omitempty"`
}

// RemediateQueueParams acts on a stuck queue item.
type RemediateQueueParams struct {
	MediaType string `json:"media_type"`
	QueueID   int    `json:"queue_id"`
	Action    string `json:"action"` // remove | blocklist_search | change_category
}

// ManualImportParams imports a download's files.
type ManualImportParams struct {
	MediaType string `json:"media_type"`
	QueueID   int    `json:"queue_id"`
	Force     bool   `json:"force,omitempty"`
}

// TriggerSearchParams starts an automatic search. Movies/TV target a library
// item by tmdb_id (TV optionally narrowed to a season or episode); books carry no TMDB id,
// so they target a single book by book_id or all of an author's monitored books
// by author_id. The book fields are omitempty so a movie/TV action's canonical
// JSON (and therefore its fingerprint) is unchanged by their addition.
type TriggerSearchParams struct {
	MediaType string `json:"media_type"`
	TmdbID    int    `json:"tmdb_id,omitempty"`
	Season    *int   `json:"season,omitempty"`
	Episode   *int   `json:"episode,omitempty"`
	AuthorID  int    `json:"author_id,omitempty"`
	BookID    int    `json:"book_id,omitempty"`
}

// RescanParams rescans the media on disk and runs the import pass. Movies/TV are
// addressed by tmdb_id; books carry no TMDB id and are addressed by author_id.
// author_id is omitempty so a movie/TV action's canonical JSON (and fingerprint)
// is unchanged by its addition.
type RescanParams struct {
	MediaType string `json:"media_type"`
	TmdbID    int    `json:"tmdb_id,omitempty"`
	AuthorID  int    `json:"author_id,omitempty"`
}

// validMediaType reports whether m is a supported media type.
func validMediaType(m string) bool { return m == "movie" || m == "tv" || m == "book" }

// validateActionParams validates params against the kind's schema and returns the
// CANONICAL JSON form to store + fingerprint. Canonicalization is by struct-field
// order: the raw JSON is decoded into the kind's typed struct and re-marshalled,
// so an identical action always fingerprints identically regardless of the key
// order the model sent. It rejects unknown fields and out-of-range values so only
// well-formed, replayable actions are ever recorded.
func validateActionParams(kind ActionKind, raw json.RawMessage) (canonical json.RawMessage, err error) {
	switch kind {
	case ActionGrabRelease:
		var p GrabReleaseParams
		if err := strictUnmarshal(raw, &p); err != nil {
			return nil, err
		}
		if !validMediaType(p.MediaType) {
			return nil, fmt.Errorf("media_type must be \"movie\", \"tv\", or \"book\"")
		}
		if p.GUID == "" || p.IndexerID <= 0 {
			return nil, fmt.Errorf("grab_release requires guid and indexer_id (from search_releases)")
		}
		p.GUID = normalizeReleaseGUIDReference(p.GUID)
		if p.QueueIDToReplace < 0 {
			return nil, fmt.Errorf("queue_id_to_replace must be positive")
		}
		if p.ReleaseTitle == "" || p.Size < 0 || p.Protocol == "" || p.Indexer == "" {
			return nil, fmt.Errorf("grab_release requires server-observed release metadata")
		}
		return canonicalJSON(p)

	case ActionRemediateQueue:
		var p RemediateQueueParams
		if err := strictUnmarshal(raw, &p); err != nil {
			return nil, err
		}
		if !validMediaType(p.MediaType) {
			return nil, fmt.Errorf("media_type must be \"movie\", \"tv\", or \"book\"")
		}
		if p.QueueID <= 0 {
			return nil, fmt.Errorf("remediate_queue requires a positive queue_id")
		}
		switch p.Action {
		case "remove", "blocklist_search", "change_category":
		default:
			return nil, fmt.Errorf("action must be \"remove\", \"blocklist_search\", or \"change_category\"")
		}
		return canonicalJSON(p)

	case ActionManualImport:
		var p ManualImportParams
		if err := strictUnmarshal(raw, &p); err != nil {
			return nil, err
		}
		if !validMediaType(p.MediaType) {
			return nil, fmt.Errorf("media_type must be \"movie\", \"tv\", or \"book\"")
		}
		if p.QueueID <= 0 {
			return nil, fmt.Errorf("manual_import requires a positive queue_id")
		}
		return canonicalJSON(p)

	case ActionTriggerSearch:
		var p TriggerSearchParams
		if err := strictUnmarshal(raw, &p); err != nil {
			return nil, err
		}
		if !validMediaType(p.MediaType) {
			return nil, fmt.Errorf("media_type must be \"movie\", \"tv\", or \"book\"")
		}
		if p.MediaType == "book" {
			// Books carry no TMDB id: target a single book by book_id or all of
			// an author's monitored books by author_id. Reject a stray tmdb_id so
			// only the documented book params are ever stored/fingerprinted.
			if p.TmdbID != 0 || p.Season != nil || p.Episode != nil {
				return nil, fmt.Errorf("trigger_search for a book must not set tmdb_id")
			}
			if p.AuthorID <= 0 && p.BookID <= 0 {
				return nil, fmt.Errorf("trigger_search for a book requires a positive author_id or book_id")
			}
		} else {
			// Movies/TV are addressed by tmdb_id; the book fields don't apply.
			if p.AuthorID != 0 || p.BookID != 0 {
				return nil, fmt.Errorf("author_id and book_id apply only to media_type book")
			}
			if p.TmdbID <= 0 {
				return nil, fmt.Errorf("trigger_search requires a positive tmdb_id")
			}
			if p.MediaType == "movie" && (p.Season != nil || p.Episode != nil) {
				return nil, fmt.Errorf("season and episode apply only to media_type tv")
			}
			if p.Episode != nil && (*p.Episode <= 0 || p.Season == nil) {
				return nil, fmt.Errorf("an episode search requires a positive episode and a season")
			}
		}
		return canonicalJSON(p)

	case ActionRescan:
		var p RescanParams
		if err := strictUnmarshal(raw, &p); err != nil {
			return nil, err
		}
		if !validMediaType(p.MediaType) {
			return nil, fmt.Errorf("media_type must be \"movie\", \"tv\", or \"book\"")
		}
		if p.MediaType == "book" {
			// Books carry no TMDB id and are rescanned by author_id.
			if p.TmdbID != 0 {
				return nil, fmt.Errorf("rescan for a book must not set tmdb_id")
			}
			if p.AuthorID <= 0 {
				return nil, fmt.Errorf("rescan for a book requires a positive author_id")
			}
		} else {
			if p.AuthorID != 0 {
				return nil, fmt.Errorf("author_id applies only to media_type book")
			}
			if p.TmdbID <= 0 {
				return nil, fmt.Errorf("rescan requires a positive tmdb_id")
			}
		}
		return canonicalJSON(p)

	default:
		return nil, fmt.Errorf("unknown action kind: %s", kind)
	}
}

// validateActionScope binds a canonical proposal to the authoritative issue
// scope. The model may choose a fix, but it may not choose a different arr,
// media type, queue row, or title than the incident it was assigned.
type actionScopeQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func (s *Service) validateActionScope(issueID int64, kind ActionKind, canonical json.RawMessage) error {
	return validateActionScopeWith(s.db, issueID, kind, canonical)
}

func validateActionScopeWith(q actionScopeQuerier, issueID int64, kind ActionKind, canonical json.RawMessage) error {
	var (
		mediaType string
		tmdbID    int
		season    int
		episode   int
		queueID   int
		closedAt  any
	)
	if err := q.QueryRow(
		`SELECT media_type, tmdb_id, season_number, episode_number, COALESCE(arr_queue_id, 0), closed_at
		 FROM issues WHERE id = ?`, issueID,
	).Scan(&mediaType, &tmdbID, &season, &episode, &queueID, &closedAt); err != nil {
		return fmt.Errorf("load issue scope: %w", err)
	}
	if closedAt != nil {
		return fmt.Errorf("issue is already closed")
	}
	if mediaType == "book" {
		switch kind {
		case ActionGrabRelease, ActionTriggerSearch, ActionRescan:
			// The current issue schema has no durable author_id/book_id. Accepting
			// either from the model would let an auto book incident mutate a wholly
			// unrelated title. Queue/manual-import actions remain safe because they
			// are bound to the detector's exact queue + download identity.
			return fmt.Errorf("%s is unavailable for book issues until an authoritative book id is stored", kind)
		case ActionRemediateQueue, ActionManualImport:
			if queueID <= 0 {
				return fmt.Errorf("%s requires the book issue's exact detector queue id", kind)
			}
		}
	}

	checkMedia := func(got string) error {
		if got != mediaType {
			return fmt.Errorf("media_type %q does not match issue media_type %q", got, mediaType)
		}
		return nil
	}
	checkQueue := func(got int) error {
		if queueID > 0 && got != queueID {
			return fmt.Errorf("queue_id %d does not match issue queue_id %d", got, queueID)
		}
		return nil
	}
	checkMediaID := func(got int, actionSeason, actionEpisode *int) error {
		if tmdbID <= 0 {
			return fmt.Errorf("issue has no authoritative tmdb_id for %s", kind)
		}
		if got != tmdbID {
			return fmt.Errorf("tmdb_id %d does not match issue tmdb_id %d", got, tmdbID)
		}
		if mediaType == "tv" && season > 0 && (actionSeason == nil || *actionSeason != season) {
			return fmt.Errorf("season %v does not match issue season %d", actionSeason, season)
		}
		if mediaType == "tv" && episode > 0 && (actionEpisode == nil || *actionEpisode != episode) {
			return fmt.Errorf("episode %v does not match issue episode %d", actionEpisode, episode)
		}
		return nil
	}

	switch kind {
	case ActionGrabRelease:
		var p GrabReleaseParams
		if err := json.Unmarshal(canonical, &p); err != nil {
			return err
		}
		if err := checkMedia(p.MediaType); err != nil {
			return err
		}
		if queueID > 0 || p.QueueIDToReplace > 0 {
			return checkQueue(p.QueueIDToReplace)
		}
	case ActionRemediateQueue:
		var p RemediateQueueParams
		if err := json.Unmarshal(canonical, &p); err != nil {
			return err
		}
		if err := checkMedia(p.MediaType); err != nil {
			return err
		}
		return checkQueue(p.QueueID)
	case ActionManualImport:
		var p ManualImportParams
		if err := json.Unmarshal(canonical, &p); err != nil {
			return err
		}
		if err := checkMedia(p.MediaType); err != nil {
			return err
		}
		return checkQueue(p.QueueID)
	case ActionTriggerSearch:
		var p TriggerSearchParams
		if err := json.Unmarshal(canonical, &p); err != nil {
			return err
		}
		if err := checkMedia(p.MediaType); err != nil {
			return err
		}
		if p.MediaType != "book" {
			return checkMediaID(p.TmdbID, p.Season, p.Episode)
		}
	case ActionRescan:
		var p RescanParams
		if err := json.Unmarshal(canonical, &p); err != nil {
			return err
		}
		if err := checkMedia(p.MediaType); err != nil {
			return err
		}
		if p.MediaType != "book" {
			return checkMediaID(p.TmdbID, nil, nil)
		}
	}
	return nil
}

// strictUnmarshal decodes raw into v, rejecting unknown fields so a proposal can
// carry only the documented params for its kind.
func strictUnmarshal(raw json.RawMessage, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("invalid params: trailing JSON value")
	}
	return nil
}

// canonicalJSON marshals a typed params value to its canonical JSON form. These
// param types are flat structs, so Go's json.Marshal (which emits fields in
// declaration order and omits empty omitempty fields) is already deterministic:
// an identical action always produces identical bytes regardless of the key order
// the model sent, because validation routes through the struct first.
func canonicalJSON(v interface{}) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}
	return json.RawMessage(b), nil
}

// fingerprint identifies one model tool gate, not an action for the lifetime of
// an issue. Retrying the same tool call is idempotent, while a later, explicitly
// re-proposed action after a denial/failure gets a fresh auditable row.
func fingerprint(issueID, runID int64, toolUseID string, kind ActionKind, canonicalParams json.RawMessage) string {
	h := sha256.New()
	h.Write([]byte(strconv.FormatInt(issueID, 10)))
	h.Write([]byte("|"))
	h.Write([]byte(strconv.FormatInt(runID, 10)))
	h.Write([]byte("|"))
	h.Write([]byte(toolUseID))
	h.Write([]byte("|"))
	h.Write([]byte(kind))
	h.Write([]byte("|"))
	h.Write(canonicalParams)
	return hex.EncodeToString(h.Sum(nil))
}
