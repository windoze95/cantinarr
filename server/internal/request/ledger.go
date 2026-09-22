package request

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// The ledger is the request_log read the Seerr-compatible API serves: every
// movie and TV request row across users, plus the live library state of each
// title those rows name. It exists because integrators (Maintainerr and its
// kin) ask "who requested this, when, and is it there now?" about the whole
// server at once, which no per-user history read answers. Books and music
// stay out: the Seerr vocabulary has no type for them.
//
// Live state follows the rest of the package: it is read from the arr
// digests, never from a stored copy, and a library that cannot be read is an
// error (ErrLedgerLibraryUnreadable), never a title that quietly reads absent.

// ErrLedgerLibraryUnreadable means a Radarr or Sonarr library the read needed
// could not be fetched. Callers answer with a retryable failure rather than a
// thinner ledger that looks complete.
var ErrLedgerLibraryUnreadable = errors.New("a library the ledger needs could not be read")

// LedgerFilter narrows a ledger read. Zero values mean "any".
type LedgerFilter struct {
	MediaType string
	TmdbID    int
	UserID    int64
	RequestID int64
}

// LedgerRow is one movie or TV request as the ledger reports it.
type LedgerRow struct {
	ID        int64
	UserID    int64
	TmdbID    int
	TVDBID    int
	MediaType string
	Title     string
	// Stored is request_log.status as written: pending, requested, or
	// denied. Live availability is the title's, in LedgerRead.Titles.
	Stored string
	// AddFailed marks an approved row whose automatic arr add already failed
	// and now waits for an admin.
	AddFailed        bool
	InstanceID       string
	QualityProfileID int
	// Seasons are the seasons a TV request covers, in the numbering the
	// library (and so the media server) uses. Explicit selections and target
	// snapshots resolve exactly; a coarse legacy scope resolves against the
	// live series when the title is in a library and stays empty otherwise.
	Seasons     []int
	ApprovedBy  int64
	RequestedAt time.Time
	DecidedAt   *time.Time

	seasonScope string
}

// LedgerKey identifies a title across the ledger.
type LedgerKey struct {
	MediaType string
	TmdbID    int
}

// LedgerTitleState is one title's live library state, the best answer across
// every configured library of its kind: a title available in any of them is
// available.
type LedgerTitleState struct {
	InLibrary bool
	// Status uses the package vocabulary: StatusAvailable, StatusPartial,
	// StatusRequested (in a library, monitored, no file yet), or
	// StatusUnavailable.
	Status string
	// AddedAt is when the movie's file was imported, when a library says.
	AddedAt *time.Time
	// Seasons is the per-season state of a series, keyed by season number.
	Seasons map[int]LedgerSeasonState
}

// LedgerSeasonState is one season's slice of LedgerTitleState.
type LedgerSeasonState struct {
	Files  int
	Total  int
	Status string
}

// LedgerRead is one consistent read: the rows a filter matched and the live
// state of every title they name (plus the filter's own title, so a read
// scoped to one title answers about it even when nobody has requested it).
type LedgerRead struct {
	Rows   []LedgerRow
	Titles map[LedgerKey]LedgerTitleState
}

// ReadLedger loads the matching rows newest-first and the live state of
// their titles. Radarr is read only when a movie title is in play, Sonarr
// only for TV, and each configured library of that kind once (through the
// shared availability digests, so a sweep over thousands of rows costs one
// library fetch per instance per cache window).
func (s *Service) ReadLedger(f LedgerFilter) (*LedgerRead, error) {
	rows, err := s.ledgerRows(f)
	if err != nil {
		return nil, err
	}
	keys := map[LedgerKey]int{}
	for _, r := range rows {
		key := LedgerKey{MediaType: r.MediaType, TmdbID: r.TmdbID}
		if r.MediaType == "tv" && r.TVDBID != 0 && keys[key] == 0 {
			keys[key] = r.TVDBID
		} else if _, seen := keys[key]; !seen {
			keys[key] = 0
		}
	}
	if f.TmdbID != 0 && f.MediaType != "" {
		key := LedgerKey{MediaType: f.MediaType, TmdbID: f.TmdbID}
		if _, seen := keys[key]; !seen {
			keys[key] = 0
		}
	}
	titles, err := s.ledgerTitles(keys)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		s.completeLedgerSeasons(&rows[i], titles[LedgerKey{MediaType: rows[i].MediaType, TmdbID: rows[i].TmdbID}])
	}
	return &LedgerRead{Rows: rows, Titles: titles}, nil
}

// ledgerSnapshot is the slice of a request_tv_targets snapshot the ledger
// reads: the seasons in library numbering and the matched TVDB identity.
type ledgerSnapshot struct {
	TargetSeasons []int `json:"target_seasons"`
	Match         struct {
		TVDBID int `json:"tvdb_id"`
	} `json:"match"`
}

func (s *Service) ledgerRows(f LedgerFilter) ([]LedgerRow, error) {
	query := `SELECT r.id, COALESCE(r.user_id, 0), r.tmdb_id, COALESCE(r.tvdb_id, 0), r.media_type, r.title, r.status,
	       COALESCE(r.add_failure_reason, ''), COALESCE(r.instance_id, ''), COALESCE(r.quality_profile_id, 0),
	       COALESCE(r.season_scope, ''), COALESCE(r.approved_by, 0), r.requested_at, r.decided_at,
	       COALESCE(t.snapshot, ''), COALESCE(c.tvdb_id, 0)
	FROM request_log r
	LEFT JOIN request_tv_targets t ON t.request_id = r.id
	LEFT JOIN tmdb_tvdb_cache c ON r.media_type = 'tv' AND c.tmdb_id = r.tmdb_id
	WHERE r.media_type IN ('movie', 'tv')`
	var args []interface{}
	if f.MediaType != "" {
		query += " AND r.media_type = ?"
		args = append(args, f.MediaType)
	}
	if f.TmdbID != 0 {
		query += " AND r.tmdb_id = ?"
		args = append(args, f.TmdbID)
	}
	if f.UserID != 0 {
		query += " AND r.user_id = ?"
		args = append(args, f.UserID)
	}
	if f.RequestID != 0 {
		query += " AND r.id = ?"
		args = append(args, f.RequestID)
	}
	query += " ORDER BY r.id DESC"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("ledger rows: %w", err)
	}
	defer rows.Close()
	var out []LedgerRow
	for rows.Next() {
		var (
			r          LedgerRow
			addFailure string
			snapshot   string
			cachedTVDB int
			decidedAt  sql.NullTime
		)
		if err := rows.Scan(&r.ID, &r.UserID, &r.TmdbID, &r.TVDBID, &r.MediaType, &r.Title, &r.Stored,
			&addFailure, &r.InstanceID, &r.QualityProfileID, &r.seasonScope, &r.ApprovedBy, &r.RequestedAt, &decidedAt,
			&snapshot, &cachedTVDB); err != nil {
			return nil, fmt.Errorf("ledger scan: %w", err)
		}
		r.AddFailed = addFailure != ""
		if decidedAt.Valid {
			t := decidedAt.Time
			r.DecidedAt = &t
		}
		if r.MediaType == "tv" {
			if snapshot != "" {
				var snap ledgerSnapshot
				if json.Unmarshal([]byte(snapshot), &snap) == nil {
					if snap.Match.TVDBID != 0 {
						r.TVDBID = snap.Match.TVDBID
					}
					// A decoded snapshot is authoritative even when it names no
					// season, so it must not read as a coarse scope below.
					r.Seasons = append([]int{}, snap.TargetSeasons...)
					sort.Ints(r.Seasons)
				}
			}
			if r.TVDBID == 0 {
				r.TVDBID = cachedTVDB
			}
			if r.Seasons == nil {
				r.Seasons = explicitSeasonNumbers(r.seasonScope)
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// explicitSeasonNumbers decodes a season_scope column that holds an explicit
// JSON selection; a coarse scope word (all, first, latest, pilot) or an empty
// column yields nil so the caller resolves it against the live series.
func explicitSeasonNumbers(scope string) []int {
	scope = strings.TrimSpace(scope)
	if !strings.HasPrefix(scope, "[") {
		return nil
	}
	var seasons []int
	if err := json.Unmarshal([]byte(scope), &seasons); err != nil {
		return nil
	}
	sort.Ints(seasons)
	return seasons
}

// completeLedgerSeasons resolves a coarse legacy scope against the seasons
// the live series holds. A title not in any library keeps an empty list: the
// row still counts as a request for the show, it just names no season.
func (s *Service) completeLedgerSeasons(r *LedgerRow, state LedgerTitleState) {
	if r.MediaType != "tv" || r.Seasons != nil || len(state.Seasons) == 0 {
		return
	}
	all := make([]int, 0, len(state.Seasons))
	for n := range state.Seasons {
		all = append(all, n)
	}
	sort.Ints(all)
	switch r.seasonScope {
	case SeasonScopeFirst, SeasonScopePilot:
		r.Seasons = all[:1]
	case SeasonScopeLatest:
		r.Seasons = all[len(all)-1:]
	default:
		r.Seasons = all
	}
}

// ledgerTitles reads the live state of each title, best across every library
// of its kind. tvdbByKey supplies a TV title's TVDB id when a row already
// carries it; 0 asks the ID bridge's cache (and then the bridge itself).
func (s *Service) ledgerTitles(tvdbByKey map[LedgerKey]int) (map[LedgerKey]LedgerTitleState, error) {
	titles := make(map[LedgerKey]LedgerTitleState, len(tvdbByKey))
	var (
		movieDigests  []map[int]movieAvailability
		seriesDigests []map[int]seriesAvailability
		moviesLoaded  bool
		seriesLoaded  bool
	)
	for key, tvdbID := range tvdbByKey {
		switch key.MediaType {
		case "movie":
			if !moviesLoaded {
				digests, err := s.ledgerMovieDigests()
				if err != nil {
					return nil, err
				}
				movieDigests, moviesLoaded = digests, true
			}
			titles[key] = ledgerMovieState(movieDigests, key.TmdbID)
		case "tv":
			if !seriesLoaded {
				digests, err := s.ledgerSeriesDigests()
				if err != nil {
					return nil, err
				}
				seriesDigests, seriesLoaded = digests, true
			}
			if tvdbID == 0 {
				tvdbID = s.ledgerTVDB(key.TmdbID)
			}
			titles[key] = ledgerSeriesState(seriesDigests, tvdbID)
		}
	}
	return titles, nil
}

// ledgerTVDB resolves a show's TVDB id for a title no row carries one for:
// the bridge's cache first, then the bridge itself when one is wired.
func (s *Service) ledgerTVDB(tmdbID int) int {
	var cached sql.NullInt64
	if err := s.db.QueryRow("SELECT tvdb_id FROM tmdb_tvdb_cache WHERE tmdb_id = ?", tmdbID).Scan(&cached); err == nil && cached.Valid && cached.Int64 != 0 {
		return int(cached.Int64)
	}
	if s.bridge == nil {
		return 0
	}
	if res, err := s.bridge.ResolveTVDBID(tmdbID); err == nil && res != nil {
		return res.TVDBID
	}
	return 0
}

// ledgerMovieDigests reads every Radarr library's digest; one unreadable
// library fails the read.
func (s *Service) ledgerMovieDigests() ([]map[int]movieAvailability, error) {
	if s.registry == nil {
		return nil, nil
	}
	instances, err := s.registry.ListInstanceSummaries("radarr")
	if err != nil {
		return nil, fmt.Errorf("list radarr instances: %w", err)
	}
	digests := make([]map[int]movieAvailability, 0, len(instances))
	for _, inst := range instances {
		digest, ok := s.movieAvailabilityDigestForInstance(inst.ID)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrLedgerLibraryUnreadable, inst.Name)
		}
		digests = append(digests, digest)
	}
	return digests, nil
}

// ledgerSeriesDigests is ledgerMovieDigests for Sonarr.
func (s *Service) ledgerSeriesDigests() ([]map[int]seriesAvailability, error) {
	if s.registry == nil {
		return nil, nil
	}
	instances, err := s.registry.ListInstanceSummaries("sonarr")
	if err != nil {
		return nil, fmt.Errorf("list sonarr instances: %w", err)
	}
	digests := make([]map[int]seriesAvailability, 0, len(instances))
	for _, inst := range instances {
		digest, ok := s.seriesAvailabilityDigestForInstance(inst.ID)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrLedgerLibraryUnreadable, inst.Name)
		}
		digests = append(digests, digest)
	}
	return digests, nil
}

// ledgerStatusRank orders the vocabulary so the best answer across libraries
// wins: available beats partial beats requested beats unavailable.
func ledgerStatusRank(status string) int {
	switch status {
	case StatusAvailable:
		return 3
	case StatusPartial:
		return 2
	case StatusRequested:
		return 1
	default:
		return 0
	}
}

func ledgerMovieState(digests []map[int]movieAvailability, tmdbID int) LedgerTitleState {
	state := LedgerTitleState{Status: StatusUnavailable}
	for _, digest := range digests {
		a, found := digest[tmdbID]
		if !found {
			continue
		}
		state.InLibrary = true
		if status := movieAvailabilityStatus(a, true); ledgerStatusRank(status) > ledgerStatusRank(state.Status) {
			state.Status = status
		}
		if a.AddedAt != nil && (state.AddedAt == nil || a.AddedAt.Before(*state.AddedAt)) {
			added := *a.AddedAt
			state.AddedAt = &added
		}
	}
	return state
}

func ledgerSeriesState(digests []map[int]seriesAvailability, tvdbID int) LedgerTitleState {
	state := LedgerTitleState{Status: StatusUnavailable}
	if tvdbID == 0 {
		return state
	}
	for _, digest := range digests {
		a, found := digest[tvdbID]
		if !found {
			continue
		}
		state.InLibrary = true
		if status := seriesAvailabilityStatus(a, true); ledgerStatusRank(status) > ledgerStatusRank(state.Status) {
			state.Status = status
		}
		for n, season := range a.Seasons {
			status, _ := statusFromCompletion(sonarr.Completion{Files: season.Files, Aired: season.Total}, a.Monitored && season.Monitored)
			if state.Seasons == nil {
				state.Seasons = map[int]LedgerSeasonState{}
			}
			if prior, seen := state.Seasons[n]; !seen || ledgerStatusRank(status) > ledgerStatusRank(prior.Status) {
				state.Seasons[n] = LedgerSeasonState{Files: season.Files, Total: season.Total, Status: status}
			}
		}
	}
	return state
}

// ForgetRequests deletes movie/TV request rows by id, the integration-facing
// "this request is over" (Seerr's DELETE /request and /media). Allowance
// charges are released first so a forgotten request stops counting, with the
// same delivery-aware refund rule every cancel uses. A row whose delivery is
// being written right now (a processing dispatch, the same gate CancelRequest
// applies) is left alone and reported in skipped: deleting it under the
// worker would strand the library write it is making; queued and retrying
// deliveries simply lose their row and are never picked up. Ids that are not
// movie/TV rows are ignored, so an integrator that only ever saw movie/TV ids
// cannot remove a book or album request.
func (s *Service) ForgetRequests(ids []int64) (deleted int, skipped []int64, err error) {
	if len(ids) == 0 {
		return 0, nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		var mediaType string
		var active int
		err := tx.QueryRow(`SELECT r.media_type,
			(SELECT COUNT(*) FROM request_dispatch d WHERE d.request_id = r.id AND d.state = 'processing')
			FROM request_log r WHERE r.id = ?`, id).Scan(&mediaType, &active)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, nil, err
		}
		if mediaType != "movie" && mediaType != "tv" {
			continue
		}
		if active > 0 {
			skipped = append(skipped, id)
			continue
		}
		if err := s.Quotas.Release(tx, id, 0, "*", "", "forgotten"); err != nil {
			return 0, nil, err
		}
		res, err := tx.Exec(`DELETE FROM request_log WHERE id = ?`, id)
		if err != nil {
			return 0, nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			deleted++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return deleted, skipped, nil
}

// LedgerRequestIDs lists the request ids of one title, for a whole-title
// forget.
func (s *Service) LedgerRequestIDs(mediaType string, tmdbID int) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM request_log WHERE media_type = ? AND tmdb_id = ? ORDER BY id`, mediaType, tmdbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
