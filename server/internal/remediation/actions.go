package remediation

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
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
	Action    string `json:"action"` // remove | blocklist_search | blocklist_only | change_category
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
//
// There is deliberately no aired-only variant here. Replacing what a bad import
// destroyed is part of delete_media_files, not a fix of its own: one problem
// gets one proposal, and an admin who approved "delete the wrong files and get
// the right ones" is not asked to approve the second half of that sentence.
// Leaving the option in this vocabulary would let the agent split the repair
// back apart, so the guarantee is structural rather than a line of prompt.
type TriggerSearchParams struct {
	MediaType string `json:"media_type"`
	TmdbID    int    `json:"tmdb_id,omitempty"`
	Season    *int   `json:"season,omitempty"`
	Episode   *int   `json:"episode,omitempty"`
	AuthorID  int    `json:"author_id,omitempty"`
	BookID    int    `json:"book_id,omitempty"`
	// Music mirrors books over Lidarr's record ids: one album by album_id, or
	// all of an artist's monitored albums by artist_id. Omitempty for the same
	// fingerprint-stability rule as the book fields.
	ArtistID int `json:"artist_id,omitempty"`
	AlbumID  int `json:"album_id,omitempty"`
}

// DeleteMediaFilesParams removes files the *arr already imported. TV names an
// exact season plus the episode numbers whose files are wrong; movies address
// the single library file by tmdb_id. Blocklist additionally marks the grab that
// delivered each deleted file as failed, which is the *arr's own "Mark as
// Failed" button and the only way to blocklist a release that already imported —
// there is no add-to-blocklist API. What happens next is then the admin's own
// failed-download policy, not Cantinarr's choice (see PR #363).
type DeleteMediaFilesParams struct {
	MediaType string `json:"media_type"`
	// TmdbID addresses movies/TV; books carry no TMDB id and are addressed by
	// the issue's durable Chaptarr book_id instead. Both omitempty-adjacent
	// rules hold: a movie/TV action's canonical JSON is unchanged by BookID's
	// addition, and vice versa.
	TmdbID    int   `json:"tmdb_id,omitempty"`
	BookID    int   `json:"book_id,omitempty"`
	AlbumID   int   `json:"album_id,omitempty"`
	Season    *int  `json:"season,omitempty"`
	Episodes  []int `json:"episodes,omitempty"`
	Blocklist bool  `json:"blocklist,omitempty"`
}

// RescanParams rescans the media on disk and runs the import pass. Movies/TV are
// addressed by tmdb_id; books carry no TMDB id and are addressed by author_id.
// author_id is omitempty so a movie/TV action's canonical JSON (and fingerprint)
// is unchanged by its addition.
type RescanParams struct {
	MediaType string `json:"media_type"`
	TmdbID    int    `json:"tmdb_id,omitempty"`
	AuthorID  int    `json:"author_id,omitempty"`
	ArtistID  int    `json:"artist_id,omitempty"`
}

// validMediaType reports whether m is a supported media type.
func validMediaType(m string) bool {
	return m == "movie" || m == "tv" || m == "book" || m == "music"
}

// maxDeleteEpisodes bounds one delete_media_files proposal. A pre-air fill is a
// season-shaped problem, so a whole long season must fit; anything past that is
// a model that has stopped reasoning about one incident, and an admin should see
// a validation error rather than a hundred-line approval card.
const maxDeleteEpisodes = 60

// sortedUniqueEpisodes canonicalizes an episode list so an identical set of
// episodes always produces identical bytes — and therefore an identical
// fingerprint — regardless of the order the model listed them in.
func sortedUniqueEpisodes(in []int) []int {
	seen := make(map[int]struct{}, len(in))
	out := make([]int, 0, len(in))
	for _, ep := range in {
		if _, dup := seen[ep]; dup {
			continue
		}
		seen[ep] = struct{}{}
		out = append(out, ep)
	}
	sort.Ints(out)
	return out
}

// validateActionParams validates params against the kind's schema and returns the
// CANONICAL JSON form to store + fingerprint. Canonicalization is by struct-field
// order: the raw JSON is decoded into the kind's typed struct and re-marshalled,
// so an identical action always fingerprints identically regardless of the key
// order the model sent. It rejects unknown fields and out-of-range values so only
// well-formed, replayable actions are ever recorded.
func validateActionParams(kind ActionKind, raw json.RawMessage) (canonical json.RawMessage, err error) {
	// Membership in ProposableActionKinds is checked FIRST so the slice stays
	// load-bearing: a kind with a case below but missing from the canonical list
	// is rejected here and fails its feature tests, instead of shipping a
	// vocabulary the schema enum and correction text don't know about.
	if !slices.Contains(ProposableActionKinds, kind) {
		return nil, fmt.Errorf("unknown action kind: %s", kind)
	}
	switch kind {
	case ActionGrabRelease:
		var p GrabReleaseParams
		if err := strictUnmarshal(raw, &p); err != nil {
			return nil, err
		}
		if !validMediaType(p.MediaType) {
			return nil, fmt.Errorf("media_type must be \"movie\", \"tv\", \"book\", or \"music\"")
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
			return nil, fmt.Errorf("media_type must be \"movie\", \"tv\", \"book\", or \"music\"")
		}
		if p.QueueID <= 0 {
			return nil, fmt.Errorf("remediate_queue requires a positive queue_id")
		}
		switch p.Action {
		case "remove", "blocklist_search", "blocklist_only", "change_category":
		default:
			return nil, fmt.Errorf("action must be \"remove\", \"blocklist_search\", \"blocklist_only\", or \"change_category\"")
		}
		return canonicalJSON(p)

	case ActionManualImport:
		var p ManualImportParams
		if err := strictUnmarshal(raw, &p); err != nil {
			return nil, err
		}
		if !validMediaType(p.MediaType) {
			return nil, fmt.Errorf("media_type must be \"movie\", \"tv\", \"book\", or \"music\"")
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
			return nil, fmt.Errorf("media_type must be \"movie\", \"tv\", \"book\", or \"music\"")
		}
		if p.MediaType == "book" {
			// Books carry no TMDB id: target a single book by book_id or all of
			// an author's monitored books by author_id. Reject a stray tmdb_id so
			// only the documented book params are ever stored/fingerprinted.
			if p.TmdbID != 0 || p.Season != nil || p.Episode != nil {
				return nil, fmt.Errorf("trigger_search for a book must not set tmdb_id")
			}
			if p.ArtistID != 0 || p.AlbumID != 0 {
				return nil, fmt.Errorf("artist_id and album_id apply only to media_type music")
			}
			if p.AuthorID <= 0 && p.BookID <= 0 {
				return nil, fmt.Errorf("trigger_search for a book requires a positive author_id or book_id")
			}
		} else if p.MediaType == "music" {
			// Music carries no TMDB id either: one album by album_id, or all of
			// an artist's monitored albums by artist_id.
			if p.TmdbID != 0 || p.Season != nil || p.Episode != nil {
				return nil, fmt.Errorf("trigger_search for music must not set tmdb_id")
			}
			if p.AuthorID != 0 || p.BookID != 0 {
				return nil, fmt.Errorf("author_id and book_id apply only to media_type book")
			}
			if p.ArtistID <= 0 && p.AlbumID <= 0 {
				return nil, fmt.Errorf("trigger_search for music requires a positive artist_id or album_id")
			}
		} else {
			// Movies/TV are addressed by tmdb_id; the book and music fields
			// don't apply.
			if p.AuthorID != 0 || p.BookID != 0 {
				return nil, fmt.Errorf("author_id and book_id apply only to media_type book")
			}
			if p.ArtistID != 0 || p.AlbumID != 0 {
				return nil, fmt.Errorf("artist_id and album_id apply only to media_type music")
			}
			if p.TmdbID <= 0 {
				return nil, fmt.Errorf("trigger_search requires a positive tmdb_id")
			}
			if p.MediaType == "movie" && (p.Season != nil || p.Episode != nil) {
				return nil, fmt.Errorf("season and episode apply only to media_type tv")
			}
			if p.Season != nil && *p.Season < 0 {
				return nil, fmt.Errorf("season must not be negative")
			}
			if p.Episode != nil && (*p.Episode <= 0 || p.Season == nil) {
				return nil, fmt.Errorf("an episode search requires a positive episode and a season")
			}
		}
		return canonicalJSON(p)

	case ActionDeleteMediaFiles:
		var p DeleteMediaFilesParams
		if err := strictUnmarshal(raw, &p); err != nil {
			return nil, err
		}
		if !validMediaType(p.MediaType) {
			return nil, fmt.Errorf("delete_media_files supports media_type \"movie\", \"tv\", \"book\", or \"music\"")
		}
		if p.MediaType == "book" {
			// Books are addressed by the durable Chaptarr record id; the wrong-book
			// repair deletes the record's file(s) and stands its grabs down.
			if p.BookID <= 0 {
				return nil, fmt.Errorf("delete_media_files for a book requires the issue's book_id")
			}
			if p.TmdbID != 0 || p.AlbumID != 0 || p.Season != nil || len(p.Episodes) > 0 {
				return nil, fmt.Errorf("book deletes take only book_id and blocklist")
			}
			return canonicalJSON(p)
		}
		if p.MediaType == "music" {
			// The wrong-album repair mirrors the book one over the durable
			// Lidarr record id: delete the album's track files and stand its
			// grabs down.
			if p.AlbumID <= 0 {
				return nil, fmt.Errorf("delete_media_files for music requires the issue's album_id")
			}
			if p.TmdbID != 0 || p.BookID != 0 || p.Season != nil || len(p.Episodes) > 0 {
				return nil, fmt.Errorf("music deletes take only album_id and blocklist")
			}
			return canonicalJSON(p)
		}
		if p.BookID != 0 {
			return nil, fmt.Errorf("book_id applies only to media_type book")
		}
		if p.AlbumID != 0 {
			return nil, fmt.Errorf("album_id applies only to media_type music")
		}
		if p.TmdbID <= 0 {
			return nil, fmt.Errorf("delete_media_files requires a positive tmdb_id")
		}
		if p.MediaType == "movie" {
			if p.Season != nil || len(p.Episodes) > 0 {
				return nil, fmt.Errorf("season and episodes apply only to media_type tv")
			}
			return canonicalJSON(p)
		}
		if p.Season == nil || *p.Season < 0 {
			return nil, fmt.Errorf("delete_media_files for TV requires a season")
		}
		if len(p.Episodes) == 0 {
			return nil, fmt.Errorf("delete_media_files for TV requires at least one episode number")
		}
		// Sort and dedupe so the same set of episodes always fingerprints
		// identically no matter what order the model listed them in — the same
		// canonicalization guarantee canonicalJSON gives the other kinds.
		p.Episodes = sortedUniqueEpisodes(p.Episodes)
		for _, ep := range p.Episodes {
			if ep <= 0 {
				return nil, fmt.Errorf("episode numbers must be positive")
			}
		}
		if len(p.Episodes) > maxDeleteEpisodes {
			return nil, fmt.Errorf("delete_media_files is limited to %d episodes per proposal", maxDeleteEpisodes)
		}
		return canonicalJSON(p)

	case ActionRescan:
		var p RescanParams
		if err := strictUnmarshal(raw, &p); err != nil {
			return nil, err
		}
		if !validMediaType(p.MediaType) {
			return nil, fmt.Errorf("media_type must be \"movie\", \"tv\", \"book\", or \"music\"")
		}
		if p.MediaType == "book" {
			// Books carry no TMDB id and are rescanned by author_id.
			if p.TmdbID != 0 || p.ArtistID != 0 {
				return nil, fmt.Errorf("rescan for a book must not set tmdb_id")
			}
			if p.AuthorID <= 0 {
				return nil, fmt.Errorf("rescan for a book requires a positive author_id")
			}
		} else if p.MediaType == "music" {
			// Music rescans by artist_id, mirroring the book rule.
			if p.TmdbID != 0 || p.AuthorID != 0 {
				return nil, fmt.Errorf("rescan for music must not set tmdb_id")
			}
			if p.ArtistID <= 0 {
				return nil, fmt.Errorf("rescan for music requires a positive artist_id")
			}
		} else {
			if p.AuthorID != 0 {
				return nil, fmt.Errorf("author_id applies only to media_type book")
			}
			if p.ArtistID != 0 {
				return nil, fmt.Errorf("artist_id applies only to media_type music")
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
		authorID  int
		bookID    int
		closedAt  any
	)
	if err := q.QueryRow(
		`SELECT media_type, tmdb_id, season_number, episode_number, COALESCE(arr_queue_id, 0),
		        COALESCE(author_id, 0), COALESCE(book_id, 0), closed_at
		 FROM issues WHERE id = ?`, issueID,
	).Scan(&mediaType, &tmdbID, &season, &episode, &queueID, &authorID, &bookID, &closedAt); err != nil {
		return fmt.Errorf("load issue scope: %w", err)
	}
	if closedAt != nil {
		return fmt.Errorf("issue is already closed")
	}
	if mediaType == "book" {
		switch kind {
		case ActionDeleteMediaFiles:
			// The wrong-book repair: bound to the issue's own durable record id,
			// exactly like the other book-scoped kinds.
			if bookID <= 0 {
				return fmt.Errorf("%s is unavailable for book issues until an authoritative book id is stored", kind)
			}
			var p DeleteMediaFilesParams
			if err := json.Unmarshal(canonical, &p); err != nil {
				return fmt.Errorf("decode delete_media_files params: %w", err)
			}
			if p.MediaType != "book" || p.BookID != bookID {
				return fmt.Errorf("delete_media_files must target this issue's own book record")
			}
			return nil
		case ActionGrabRelease, ActionTriggerSearch:
			// Without a stored book record id, accepting one from the model would
			// let a book incident mutate a wholly unrelated title. Queue/manual-
			// import actions remain safe either way because they are bound to the
			// detector's exact queue + download identity.
			if bookID <= 0 {
				return fmt.Errorf("%s is unavailable for book issues until an authoritative book id is stored", kind)
			}
		case ActionRescan:
			if authorID <= 0 {
				return fmt.Errorf("%s is unavailable for book issues until an authoritative author id is stored", kind)
			}
		case ActionRemediateQueue, ActionManualImport:
			if queueID <= 0 {
				return fmt.Errorf("%s requires the book issue's exact detector queue id", kind)
			}
		}
	}
	if mediaType == "music" {
		switch kind {
		case ActionDeleteMediaFiles:
			// The wrong-album repair: bound to the issue's own durable record
			// id, exactly like the book rule (the ids ride the generic
			// author_id/book_id columns).
			if bookID <= 0 {
				return fmt.Errorf("%s is unavailable for music issues until an authoritative album id is stored", kind)
			}
			var p DeleteMediaFilesParams
			if err := json.Unmarshal(canonical, &p); err != nil {
				return fmt.Errorf("decode delete_media_files params: %w", err)
			}
			if p.MediaType != "music" || p.AlbumID != bookID {
				return fmt.Errorf("delete_media_files must target this issue's own album record")
			}
			return nil
		case ActionGrabRelease, ActionTriggerSearch:
			if bookID <= 0 {
				return fmt.Errorf("%s is unavailable for music issues until an authoritative album id is stored", kind)
			}
		case ActionRescan:
			if authorID <= 0 {
				return fmt.Errorf("%s is unavailable for music issues until an authoritative artist id is stored", kind)
			}
		case ActionRemediateQueue, ActionManualImport:
			if queueID <= 0 {
				return fmt.Errorf("%s requires the music issue's exact detector queue id", kind)
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
		if mediaType == "tv" && (season > 0 || episode > 0) && (actionSeason == nil || *actionSeason != season) {
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
		if p.MediaType == "book" {
			// A single-book search must target the issue's exact book record;
			// an author-wide search must target the issue's exact author.
			if p.BookID > 0 {
				if p.BookID != bookID {
					return fmt.Errorf("book_id %d does not match issue book_id %d", p.BookID, bookID)
				}
				return nil
			}
			if authorID <= 0 || p.AuthorID != authorID {
				return fmt.Errorf("author_id %d does not match issue author_id %d", p.AuthorID, authorID)
			}
			return nil
		}
		if p.MediaType == "music" {
			// The album/artist ids ride the generic book/author columns.
			if p.AlbumID > 0 {
				if p.AlbumID != bookID {
					return fmt.Errorf("album_id %d does not match issue album_id %d", p.AlbumID, bookID)
				}
				return nil
			}
			if authorID <= 0 || p.ArtistID != authorID {
				return fmt.Errorf("artist_id %d does not match issue artist_id %d", p.ArtistID, authorID)
			}
			return nil
		}
		return checkMediaID(p.TmdbID, p.Season, p.Episode)
	case ActionDeleteMediaFiles:
		var p DeleteMediaFilesParams
		if err := json.Unmarshal(canonical, &p); err != nil {
			return err
		}
		if err := checkMedia(p.MediaType); err != nil {
			return err
		}
		// This kind carries a LIST where the others carry one episode, so it can't
		// reuse checkMediaID's single-episode comparison. The rule is the same:
		// an issue that names one exact episode may delete that episode's file and
		// no other, while a season- or series-scoped issue is free to name the
		// episodes the timeline found inside the season it already owns.
		if tmdbID <= 0 {
			return fmt.Errorf("issue has no authoritative tmdb_id for %s", kind)
		}
		if p.TmdbID != tmdbID {
			return fmt.Errorf("tmdb_id %d does not match issue tmdb_id %d", p.TmdbID, tmdbID)
		}
		if mediaType == "tv" {
			if (season > 0 || episode > 0) && (p.Season == nil || *p.Season != season) {
				return fmt.Errorf("season %s does not match issue season %d", intPtrForError(p.Season), season)
			}
			if episode > 0 && (len(p.Episodes) != 1 || p.Episodes[0] != episode) {
				return fmt.Errorf("episodes %v do not match issue episode %d", p.Episodes, episode)
			}
		}
	case ActionRescan:
		var p RescanParams
		if err := json.Unmarshal(canonical, &p); err != nil {
			return err
		}
		if err := checkMedia(p.MediaType); err != nil {
			return err
		}
		if p.MediaType == "book" {
			if p.AuthorID != authorID {
				return fmt.Errorf("author_id %d does not match issue author_id %d", p.AuthorID, authorID)
			}
			return nil
		}
		if p.MediaType == "music" {
			if p.ArtistID != authorID {
				return fmt.Errorf("artist_id %d does not match issue artist_id %d", p.ArtistID, authorID)
			}
			return nil
		}
		return checkMediaID(p.TmdbID, nil, nil)
	}
	return nil
}

// intPtrForError renders an optional int for a validation message. Formatting a
// *int with %v prints its address, which tells an admin reading the failure
// nothing about what the model actually proposed.
func intPtrForError(v *int) string {
	if v == nil {
		return "(unset)"
	}
	return strconv.Itoa(*v)
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
