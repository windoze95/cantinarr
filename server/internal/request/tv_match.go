package request

import (
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

//go:embed tv_matches.v1.json
var bundledTVMatchesJSON []byte

var bundledTVMatches = func() struct {
	Version     int       `json:"version"`
	Corrections []TVMatch `json:"corrections"`
} {
	var data struct {
		Version     int       `json:"version"`
		Corrections []TVMatch `json:"corrections"`
	}
	if err := json.Unmarshal(bundledTVMatchesJSON, &data); err != nil {
		panic(err)
	}
	return data
}()

var (
	ErrTVMatchStale = errors.New("TV match changed; refresh and review the correction")
	ErrTVMatchAdmin = errors.New("only admins can manage TV matches")
)

// TVMatch is the server-owned identity. SeasonMap always maps TMDB to Sonarr;
// an empty map on an ordinary bridge result means identity numbering. Client
// TVDB ids are deliberately absent from the resolver's inputs.
type TVMatch struct {
	TmdbID      int         `json:"tmdb_id"`
	Title       string      `json:"title,omitempty"`
	TVDBID      int         `json:"tvdb_id,omitempty"`
	TargetTitle string      `json:"target_title,omitempty"`
	SeasonMap   map[int]int `json:"season_map"`
	Provenance  string      `json:"provenance"`
	Revision    string      `json:"revision"`
	State       string      `json:"state"`
	Message     string      `json:"message,omitempty"`
	SeriesID    int         `json:"series_id,omitempty"`
}

type tvMatchError struct{ code string }

func (e *tvMatchError) Error() string {
	switch e.code {
	case "tv_match_paused":
		return "TV matching is paused for this title. Ask an admin to review its TV match."
	case "tv_seasons_unmapped":
		return "This title has missing or unmapped seasons. An admin must correct its TV match before requesting it."
	case "tv_metadata_unavailable":
		return "Could not verify this TV title's metadata. Retry, or ask an admin to review its TV match."
	case "tv_match_ambiguous":
		return "Could not find one verified TV match. An admin can use Correct TV match to select the series and seasons."
	default:
		return "Could not verify the TV match. Retry, or ask an admin to use Correct TV match."
	}
}

func tvMatchFailure(code string) error { return &tvMatchError{code: code} }

type tvMatchQuerier interface{ QueryRow(string, ...any) *sql.Row }

// configuredTVMatch never reads the network. A reset keeps the local counter,
// and bundled upgrades cannot replace an override or resurrect a paused title.
func configuredTVMatch(q tvMatchQuerier, tmdbID int) (*TVMatch, int64, error) {
	m := &TVMatch{TmdbID: tmdbID, State: "resolved", Provenance: "default", SeasonMap: map[int]int{}}
	for _, bundled := range bundledTVMatches.Corrections {
		if bundled.TmdbID == tmdbID {
			m.Title, m.TVDBID, m.TargetTitle, m.Provenance = bundled.Title, bundled.TVDBID, bundled.TargetTitle, "bundled"
			for source, target := range bundled.SeasonMap {
				m.SeasonMap[source] = target
			}
		}
	}
	var revision int64
	var mode, raw string
	var target int
	err := q.QueryRow(`SELECT mode,tvdb_id,season_map,revision FROM tv_match_overrides WHERE tmdb_id=?`, tmdbID).Scan(&mode, &target, &raw, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, err
	}
	if mode == "paused" {
		m.State, m.Provenance, m.Message = "paused", "custom", (&tvMatchError{code: "tv_match_paused"}).Error()
	}
	if mode == "custom" || (mode == "paused" && target > 0) {
		m.TVDBID, m.TargetTitle, m.Provenance = target, "", "custom"
		// Unmarshal merges into a non-nil map; local corrections must instead
		// replace every bundled key, including after bundled-data upgrades.
		m.SeasonMap = map[int]int{}
		if err = json.Unmarshal([]byte(raw), &m.SeasonMap); err != nil {
			return nil, 0, err
		}
	}
	stampTVMatch(m, revision)
	return m, revision, nil
}

func stampTVMatch(m *TVMatch, localRevision int64) {
	// Title/localized metadata changes do not silently retarget queued work.
	// A bundled-data upgrade cannot revise a locally owned correction.
	bundleRevision := 0
	if m.Provenance == "bundled" {
		bundleRevision = bundledTVMatches.Version
	}
	data, _ := json.Marshal([]any{bundleRevision, localRevision, m.State, m.Provenance, m.TVDBID, m.SeasonMap})
	m.Revision = fmt.Sprintf("%x", sha256.Sum256(data))[:24]
}

func (s *Service) tvSource(tmdbID int) (*tmdb.TVDetails, error) {
	if tmdbID <= 0 || s.bridge == nil || s.bridge.TMDB() == nil {
		return nil, tvMatchFailure("tv_metadata_unavailable")
	}
	d, err := s.bridge.TMDB().GetTVDetails(tmdbID)
	if err != nil || d == nil || d.ID != tmdbID || strings.TrimSpace(d.Name) == "" {
		return nil, tvMatchFailure("tv_metadata_unavailable")
	}
	return d, nil
}

func sourceSeasonNumbers(d *tmdb.TVDetails) []int {
	var numbers []int
	for _, season := range d.Seasons {
		if season.SeasonNumber > 0 {
			numbers = append(numbers, season.SeasonNumber)
		}
	}
	return normalizeSeasonNumbers(numbers)
}

func validateTVSeasons(mapping map[int]int, source []int, target []sonarr.SeasonResource) error {
	if len(source) == 0 || len(mapping) != len(source) {
		return tvMatchFailure("tv_seasons_unmapped")
	}
	known := map[int]bool{}
	for _, season := range target {
		if season.SeasonNumber > 0 {
			known[season.SeasonNumber] = true
		}
	}
	used := map[int]bool{}
	for _, n := range source {
		dest := mapping[n]
		if dest <= 0 || used[dest] || !known[dest] {
			return tvMatchFailure("tv_seasons_unmapped")
		}
		used[dest] = true
	}
	return nil
}

// resolveTVMatch is shared by intake, approval, status, history and badges.
// Corrected titles require complete live source and target season metadata.
// Ordinary ID bridging does not depend on Sonarr search being available.
func (s *Service) resolveTVMatch(client *sonarr.Client, tmdbID int) (*TVMatch, error) {
	m, rev, err := configuredTVMatch(s.db, tmdbID)
	if err != nil {
		return nil, err
	}
	if m.State == "paused" {
		return m, tvMatchFailure("tv_match_paused")
	}
	if m.Provenance == "bundled" || m.Provenance == "custom" {
		d, e := s.tvSource(tmdbID)
		if e != nil {
			return m, e
		}
		m.Title = d.Name
		if client == nil {
			return m, tvMatchFailure("tv_metadata_unavailable")
		}
		target, e := client.LookupByTVDB(m.TVDBID)
		if e != nil {
			return m, tvMatchFailure("tv_metadata_unavailable")
		}
		m.TargetTitle = target.Title
		if e = validateTVSeasons(m.SeasonMap, sourceSeasonNumbers(d), target.Seasons); e != nil {
			return m, e
		}
		return m, nil
	}
	if s.bridge == nil {
		return m, tvMatchFailure("tv_metadata_unavailable")
	}
	bridge, e := s.bridge.ResolveTVDBID(tmdbID)
	if e == nil && bridge != nil && bridge.TVDBID > 0 {
		m.TVDBID, m.Provenance = bridge.TVDBID, "metadata"
	} else {
		if !errors.Is(e, tmdb.ErrNoTVDBMapping) || client == nil {
			return m, tvMatchFailure("tv_metadata_unavailable")
		}
		d, e := s.tvSource(tmdbID)
		if e != nil {
			return m, e
		}
		candidates, e := client.LookupByTitle(d.Name)
		if e != nil {
			return m, tvMatchFailure("tv_metadata_unavailable")
		}
		target, e := strictTVTitleMatch(d, candidates)
		if e != nil {
			return m, e
		}
		m.TVDBID, m.Title, m.TargetTitle, m.Provenance = target.TvdbID, d.Name, target.Title, "title"
	}
	d, e := s.tvSource(tmdbID)
	if e != nil {
		return m, e
	}
	m.Title = d.Name
	for _, n := range sourceSeasonNumbers(d) {
		m.SeasonMap[n] = n
	}
	if len(m.SeasonMap) == 0 {
		return m, tvMatchFailure("tv_seasons_unmapped")
	}
	stampTVMatch(m, rev)
	return m, nil
}

func canonicalTVTitle(title string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, title)
}

func strictTVTitleMatch(source *tmdb.TVDetails, candidates []sonarr.LookupResult) (*sonarr.LookupResult, error) {
	year := 0
	if len(source.FirstAir) >= 4 {
		year, _ = strconv.Atoi(source.FirstAir[:4])
	}
	if year <= 0 {
		return nil, tvMatchFailure("tv_metadata_unavailable")
	}
	titles := map[string]bool{}
	for _, title := range []string{source.Name, source.OriginalName} {
		if name := canonicalTVTitle(title); name != "" {
			titles[name] = true
		}
	}
	var match *sonarr.LookupResult
	for i := range candidates {
		c := &candidates[i]
		if !titles[canonicalTVTitle(c.Title)] || c.TvdbID <= 0 || c.Year <= 0 || c.Year-year < -1 || c.Year-year > 1 {
			continue
		}
		if match != nil {
			return nil, tvMatchFailure("tv_match_ambiguous")
		}
		match = c
	}
	if match == nil {
		return nil, tvMatchFailure("tv_match_ambiguous")
	}
	return match, nil
}

type TVMatchEdit struct {
	Revision   string      `json:"revision"`
	Mode       string      `json:"mode"`
	TVDBID     int         `json:"tvdb_id"`
	SeasonMap  map[int]int `json:"season_map"`
	InstanceID string      `json:"instance_id"`
}

type TVMatchView struct {
	Match         *TVMatch                `json:"match"`
	SourceSeasons []tmdb.TVSeason         `json:"source_seasons"`
	TargetSeasons []sonarr.SeasonResource `json:"target_seasons"`
	InstanceID    string                  `json:"instance_id,omitempty"`
}

func (s *Service) TVMatchDetail(adminID int64, tmdbID int, instanceID string) (*TVMatchView, error) {
	if !s.userIsAdmin(adminID) {
		return nil, ErrTVMatchAdmin
	}
	client, id, err := s.resolveSonarr(adminID, instanceID)
	if err != nil {
		return nil, err
	}
	m, err := s.resolveTVMatch(client, tmdbID)
	if m == nil {
		return nil, err
	}
	if err != nil {
		if m.State != "paused" {
			m.State = "unresolved"
		}
		m.Message = err.Error()
	}
	view := &TVMatchView{Match: m, InstanceID: id, SourceSeasons: []tmdb.TVSeason{}, TargetSeasons: []sonarr.SeasonResource{}}
	if d, e := s.tvSource(tmdbID); e == nil {
		m.Title = d.Name
		for _, season := range d.Seasons {
			if season.SeasonNumber > 0 {
				view.SourceSeasons = append(view.SourceSeasons, season)
			}
		}
	}
	if m.TVDBID > 0 && client != nil {
		if target, e := client.LookupByTVDB(m.TVDBID); e == nil {
			view.TargetSeasons = target.Seasons
			m.TargetTitle = target.Title
		}
	}
	return view, nil
}

func (s *Service) ListTVMatches(adminID int64) ([]TVMatch, error) {
	if !s.userIsAdmin(adminID) {
		return nil, ErrTVMatchAdmin
	}
	return s.configuredTVMatches()
}

func (s *Service) configuredTVMatches() ([]TVMatch, error) {
	ids := map[int]bool{}
	for _, m := range bundledTVMatches.Corrections {
		ids[m.TmdbID] = true
	}
	rows, err := s.db.Query(`SELECT tmdb_id FROM tv_match_overrides`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	ordered := make([]int, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Ints(ordered)
	out := []TVMatch{}
	for _, id := range ordered {
		m, _, e := configuredTVMatch(s.db, id)
		if e != nil {
			return nil, e
		}
		out = append(out, *m)
	}
	return out, nil
}

func (s *Service) TVMatchCandidates(adminID int64, instanceID, query string) ([]sonarr.LookupResult, error) {
	if !s.userIsAdmin(adminID) {
		return nil, ErrTVMatchAdmin
	}
	client, _, err := s.resolveSonarr(adminID, instanceID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, ErrArrInstanceInvalid
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("enter a series title or TVDB ID")
	}
	if id, e := strconv.Atoi(strings.TrimPrefix(query, "tvdb:")); e == nil && id > 0 {
		target, e := client.LookupByTVDB(id)
		if e != nil {
			return nil, tvMatchFailure("tv_metadata_unavailable")
		}
		return []sonarr.LookupResult{*target}, nil
	}
	return client.LookupByTitle(query)
}

func (s *Service) SaveTVMatch(adminID int64, tmdbID int, edit TVMatchEdit) (*TVMatchView, error) {
	if !s.userIsAdmin(adminID) {
		return nil, ErrTVMatchAdmin
	}
	if tmdbID <= 0 {
		return nil, errors.New("invalid TMDB ID")
	}
	if edit.Mode != "custom" && edit.Mode != "paused" && edit.Mode != "default" {
		return nil, errors.New("choose a custom match, pause, or restore default")
	}
	client, _, err := s.resolveSonarr(adminID, edit.InstanceID)
	if err != nil {
		return nil, err
	}
	// Read the configured revision, not a network-dependent resolved identity;
	// metadata/title matches use their resolved revision on the editor surface.
	current, _, err := configuredTVMatch(s.db, tmdbID)
	if err != nil {
		return nil, err
	}
	if current.Provenance == "default" && current.State != "paused" {
		if resolved, e := s.resolveTVMatch(client, tmdbID); e == nil {
			current = resolved
		}
	}
	if edit.Revision == "" || edit.Revision != current.Revision {
		return nil, ErrTVMatchStale
	}
	if edit.Mode == "custom" {
		d, e := s.tvSource(tmdbID)
		if e != nil {
			return nil, e
		}
		if client == nil || edit.TVDBID <= 0 {
			return nil, tvMatchFailure("tv_match_ambiguous")
		}
		target, e := client.LookupByTVDB(edit.TVDBID)
		if e != nil {
			return nil, tvMatchFailure("tv_metadata_unavailable")
		}
		if e = validateTVSeasons(edit.SeasonMap, sourceSeasonNumbers(d), target.Seasons); e != nil {
			return nil, e
		}
	}
	s.tvMatchMu.Lock()
	defer s.tvMatchMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	before, rev, err := configuredTVMatch(tx, tmdbID)
	if err != nil {
		return nil, err
	}
	if before.Provenance == "default" && before.State != "paused" {
		before.TVDBID, before.Provenance, before.SeasonMap = current.TVDBID, current.Provenance, current.SeasonMap
		stampTVMatch(before, rev)
	}
	if before.Revision != edit.Revision {
		return nil, ErrTVMatchStale
	}
	raw, _ := json.Marshal(edit.SeasonMap)
	if edit.Mode == "paused" {
		// Pausing blocks delivery but retains the reviewed target for editing;
		// only Restore default discards the local target behavior.
		edit.TVDBID = current.TVDBID
		raw, _ = json.Marshal(current.SeasonMap)
	} else if edit.Mode == "default" {
		edit.TVDBID = 0
		raw = []byte(`{}`)
	}
	_, err = tx.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision,updated_by) VALUES (?,?,?,?,?,?)
 ON CONFLICT(tmdb_id) DO UPDATE SET mode=excluded.mode,tvdb_id=excluded.tvdb_id,season_map=excluded.season_map,revision=excluded.revision,updated_by=excluded.updated_by,updated_at=CURRENT_TIMESTAMP`, tmdbID, edit.Mode, edit.TVDBID, string(raw), rev+1, adminID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.TVMatchDetail(adminID, tmdbID, edit.InstanceID)
}
