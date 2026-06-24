package request

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// Request status values stored in request_log.status and returned to clients.
const (
	StatusUnavailable = "unavailable"
	StatusRequested   = "requested"
	StatusDownloading = "downloading"
	StatusAvailable   = "available"
	StatusPartial     = "partial"
	StatusPending     = "pending"
	StatusDenied      = "denied"
)

// Season scope choices a user (or admin) can attach to a TV request.
const (
	SeasonScopeAll    = "all"
	SeasonScopeFirst  = "first"
	SeasonScopeLatest = "latest"
	SeasonScopePilot  = "pilot"
)

// Book format choices a user can attach to a Chaptarr request.
const (
	BookFormatEbook     = "ebook"
	BookFormatAudiobook = "audiobook"
	BookFormatBoth      = "both"
)

// requestSettingsKey is the settings-table key holding the global request
// defaults (JSON blob), mirroring the toolsettings storage pattern.
const requestSettingsKey = "request_settings"

// Notifier delivers realtime events about request decisions. The websocket
// hub satisfies this; it is optional and may be nil.
type Notifier interface {
	NotifyUser(userID int64, eventType string, data map[string]interface{})
	NotifyAdmins(eventType string, data map[string]interface{})
}

type Service struct {
	db       *sql.DB
	registry *instance.Registry
	bridge   *tmdb.Bridge
	notifier Notifier
}

func NewService(db *sql.DB, registry *instance.Registry, bridge *tmdb.Bridge, notifier Notifier) *Service {
	return &Service{
		db:       db,
		registry: registry,
		bridge:   bridge,
		notifier: notifier,
	}
}

// getRadarr returns the Radarr client to use as a given user's source: their
// per-user default override when set, else the global default. A userID of 0
// (no specific user / admin-global context) resolves to the global default.
func (s *Service) getRadarr(userID int64) *radarr.Client {
	if s.registry != nil {
		client, _, err := s.registry.GetUserDefaultRadarrClient(userID)
		if err == nil && client != nil {
			return client
		}
	}
	return nil
}

// getSonarr returns the Sonarr client to use as a given user's source: their
// per-user default override when set, else the global default. A userID of 0
// (no specific user / admin-global context) resolves to the global default.
func (s *Service) getSonarr(userID int64) *sonarr.Client {
	if s.registry != nil {
		client, _, err := s.registry.GetUserDefaultSonarrClient(userID)
		if err == nil && client != nil {
			return client
		}
	}
	return nil
}

// getChaptarr returns the Chaptarr client for the user's granted instance, or
// nil when the user has no grant (Chaptarr has no global default — access is
// admin-granted per user).
func (s *Service) getChaptarr(userID int64) *chaptarr.Client {
	if s.registry != nil {
		client, _, err := s.registry.GetUserChaptarrClient(userID)
		if err == nil && client != nil {
			return client
		}
	}
	return nil
}

type CreateRequest struct {
	TmdbID    int    `json:"tmdb_id"`
	MediaType string `json:"media_type"`
	Title     string `json:"title"`
	TvdbID    int    `json:"tvdb_id"`
	// ForeignID is the Chaptarr/Readarr foreignBookId for book requests, which
	// have no TMDB id. Required when media_type == "book"; ignored otherwise.
	ForeignID        string `json:"foreign_id"`
	BookFormat       string `json:"book_format"`
	SeasonScope      string `json:"season_scope"`
	QualityProfileID int    `json:"quality_profile_id"`
	// Seasons is an optional explicit list of season numbers (TV only). When
	// present & non-empty it takes the seasonpass code path (monitor exactly
	// these seasons), overriding the coarse SeasonScope. Empty/absent keeps the
	// existing season_scope behavior.
	Seasons []int `json:"seasons,omitempty"`
}

type CreateResponse struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
	Title   string `json:"title"`
}

type StatusResponse struct {
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	// Seasons carries per-season availability for TV titles (omitted for
	// movies and for series not yet in the library). Season 0 / Specials are
	// excluded, matching the rest of the app's season handling.
	Seasons []SeasonStatus `json:"seasons,omitempty"`
}

// SeasonStatus is one season's availability, mirroring the title-level status
// vocabulary (available / partial / downloading / requested / unavailable).
type SeasonStatus struct {
	SeasonNumber     int     `json:"season_number"`
	EpisodeFileCount int     `json:"episode_file_count"`
	EpisodeCount     int     `json:"episode_count"`
	Status           string  `json:"status"`
	Progress         float64 `json:"progress"`
}

type RequestLog struct {
	TmdbID      int       `json:"tmdb_id"`
	MediaType   string    `json:"media_type"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	DenyReason  string    `json:"deny_reason,omitempty"`
	RequestedAt time.Time `json:"requested_at"`
}

// PendingRequest is one row of the admin approval queue.
type PendingRequest struct {
	ID               int64     `json:"id"`
	UserID           int64     `json:"user_id"`
	Username         string    `json:"username"`
	TmdbID           int       `json:"tmdb_id"`
	TvdbID           int       `json:"tvdb_id"`
	MediaType        string    `json:"media_type"`
	Title            string    `json:"title"`
	BookFormat       string    `json:"book_format"`
	SeasonScope      string    `json:"season_scope"`
	QualityProfileID int       `json:"quality_profile_id"`
	RequestedAt      time.Time `json:"requested_at"`
}

// QualityProfile is an arr quality profile offered for selection.
type QualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// RequestOptions tells the client what the current user may choose for a request.
type RequestOptions struct {
	CanChooseSeason    bool             `json:"can_choose_season"`
	CanChooseQuality   bool             `json:"can_choose_quality"`
	DefaultSeasonScope string           `json:"default_season_scope"`
	QualityProfiles    []QualityProfile `json:"quality_profiles"`
}

// DecisionOverride lets an admin tweak options when approving a request.
type DecisionOverride struct {
	SeasonScope      string `json:"season_scope"`
	QualityProfileID int    `json:"quality_profile_id"`
	BookFormat       string `json:"book_format"`
}

// GlobalSettings holds the system-wide request defaults (settings table).
type GlobalSettings struct {
	RequireApproval      bool   `json:"require_approval"`
	AllowSeasonChoice    bool   `json:"allow_season_choice"`
	DefaultSeasonScope   string `json:"default_season_scope"`
	AllowQualityChoice   bool   `json:"allow_quality_choice"`
	DefaultQualityRadarr int    `json:"default_quality_radarr"`
	DefaultQualitySonarr int    `json:"default_quality_sonarr"`
}

func defaultGlobalSettings() GlobalSettings {
	return GlobalSettings{
		RequireApproval:    false,
		AllowSeasonChoice:  true,
		DefaultSeasonScope: SeasonScopeAll,
		AllowQualityChoice: false,
	}
}

// UserSettingsDTO is the per-user override payload. A nil field means the user
// inherits the global default for that option.
type UserSettingsDTO struct {
	RequireApproval      *bool   `json:"require_approval"`
	AllowSeasonChoice    *bool   `json:"allow_season_choice"`
	SeasonScope          *string `json:"season_scope"`
	AllowQualityChoice   *bool   `json:"allow_quality_choice"`
	QualityProfileRadarr *int    `json:"quality_profile_radarr"`
	QualityProfileSonarr *int    `json:"quality_profile_sonarr"`
}

// AdminSettingsView is the global-settings editor payload: the current
// defaults plus the arr quality profiles an admin chooses among.
type AdminSettingsView struct {
	Settings       GlobalSettings   `json:"settings"`
	RadarrProfiles []QualityProfile `json:"radarr_profiles"`
	SonarrProfiles []QualityProfile `json:"sonarr_profiles"`
}

// effective is the resolved option set for one user: global default, then the
// per-user override, then the admin bypass.
type effective struct {
	RequiresApproval   bool
	AllowSeasonChoice  bool
	SeasonScope        string
	AllowQualityChoice bool
	QualityRadarr      int
	QualitySonarr      int
}

// resolvedRequest is a request whose options have all been resolved server-side.
type resolvedRequest struct {
	userID           int64
	tmdbID           int
	tvdbID           int
	foreignID        string // Chaptarr foreignBookId (book requests)
	bookFormat       string
	mediaType        string
	title            string
	seasonScope      string
	qualityProfileID int
	// seasonNumbers, when non-empty, is an explicit set of seasons to monitor
	// via the seasonpass path (overrides seasonScope). It round-trips through
	// the approval flow by being JSON-encoded into the season_scope column.
	seasonNumbers []int
}

// GetGlobalSettings returns the stored global request defaults, falling back
// to the built-in defaults for any missing field.
func (s *Service) GetGlobalSettings() GlobalSettings {
	g := defaultGlobalSettings()
	var v string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", requestSettingsKey).Scan(&v); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &g)
	}
	if !validSeasonScope(g.DefaultSeasonScope) {
		g.DefaultSeasonScope = SeasonScopeAll
	}
	return g
}

func (s *Service) SetGlobalSettings(g GlobalSettings) error {
	if !validSeasonScope(g.DefaultSeasonScope) {
		g.DefaultSeasonScope = SeasonScopeAll
	}
	data, err := json.Marshal(g)
	if err != nil {
		return fmt.Errorf("encode request settings: %w", err)
	}
	if _, err := s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", requestSettingsKey, string(data)); err != nil {
		return fmt.Errorf("save request settings: %w", err)
	}
	return nil
}

// GetUserSettingsDTO loads a user's per-user overrides; absent columns/rows
// are returned as nil (inherit).
func (s *Service) GetUserSettingsDTO(userID int64) (UserSettingsDTO, error) {
	var dto UserSettingsDTO
	var ra, asc, aqc sql.NullBool
	var ss sql.NullString
	var qr, qs sql.NullInt64
	err := s.db.QueryRow(
		"SELECT require_approval, allow_season_choice, season_scope_override, allow_quality_choice, quality_profile_radarr, quality_profile_sonarr FROM user_request_settings WHERE user_id = ?",
		userID,
	).Scan(&ra, &asc, &ss, &aqc, &qr, &qs)
	if err == sql.ErrNoRows {
		return dto, nil
	}
	if err != nil {
		return dto, fmt.Errorf("load user request settings: %w", err)
	}
	if ra.Valid {
		v := ra.Bool
		dto.RequireApproval = &v
	}
	if asc.Valid {
		v := asc.Bool
		dto.AllowSeasonChoice = &v
	}
	if ss.Valid {
		v := ss.String
		dto.SeasonScope = &v
	}
	if aqc.Valid {
		v := aqc.Bool
		dto.AllowQualityChoice = &v
	}
	if qr.Valid {
		v := int(qr.Int64)
		dto.QualityProfileRadarr = &v
	}
	if qs.Valid {
		v := int(qs.Int64)
		dto.QualityProfileSonarr = &v
	}
	return dto, nil
}

// SetUserSettings upserts a user's per-user overrides. Nil fields persist as
// NULL (inherit the global default).
func (s *Service) SetUserSettings(userID int64, dto UserSettingsDTO) error {
	if dto.SeasonScope != nil && *dto.SeasonScope != "" && !validSeasonScope(*dto.SeasonScope) {
		return fmt.Errorf("invalid season scope: %s", *dto.SeasonScope)
	}
	_, err := s.db.Exec(
		`INSERT INTO user_request_settings
			(user_id, require_approval, allow_season_choice, season_scope_override, allow_quality_choice, quality_profile_radarr, quality_profile_sonarr)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
			require_approval = excluded.require_approval,
			allow_season_choice = excluded.allow_season_choice,
			season_scope_override = excluded.season_scope_override,
			allow_quality_choice = excluded.allow_quality_choice,
			quality_profile_radarr = excluded.quality_profile_radarr,
			quality_profile_sonarr = excluded.quality_profile_sonarr`,
		userID, dto.RequireApproval, dto.AllowSeasonChoice, dto.SeasonScope, dto.AllowQualityChoice, dto.QualityProfileRadarr, dto.QualityProfileSonarr,
	)
	if err != nil {
		return fmt.Errorf("save user request settings: %w", err)
	}
	return nil
}

// userIsAdmin reports whether the user has the admin role.
func (s *Service) userIsAdmin(userID int64) bool {
	var role string
	if err := s.db.QueryRow("SELECT role FROM users WHERE id = ?", userID).Scan(&role); err != nil {
		return false
	}
	return role == "admin"
}

// effectiveSettings resolves the option set for a user: global default, then
// per-user override, then admin bypass (admins never need approval and may
// always choose options).
func (s *Service) effectiveSettings(userID int64, isAdmin bool) (effective, error) {
	g := s.GetGlobalSettings()
	dto, err := s.GetUserSettingsDTO(userID)
	if err != nil {
		return effective{}, err
	}
	eff := effective{
		RequiresApproval:   g.RequireApproval,
		AllowSeasonChoice:  g.AllowSeasonChoice,
		SeasonScope:        g.DefaultSeasonScope,
		AllowQualityChoice: g.AllowQualityChoice,
		QualityRadarr:      g.DefaultQualityRadarr,
		QualitySonarr:      g.DefaultQualitySonarr,
	}
	if dto.RequireApproval != nil {
		eff.RequiresApproval = *dto.RequireApproval
	}
	if dto.AllowSeasonChoice != nil {
		eff.AllowSeasonChoice = *dto.AllowSeasonChoice
	}
	if dto.SeasonScope != nil && *dto.SeasonScope != "" {
		eff.SeasonScope = *dto.SeasonScope
	}
	if dto.AllowQualityChoice != nil {
		eff.AllowQualityChoice = *dto.AllowQualityChoice
	}
	if dto.QualityProfileRadarr != nil && *dto.QualityProfileRadarr != 0 {
		eff.QualityRadarr = *dto.QualityProfileRadarr
	}
	if dto.QualityProfileSonarr != nil && *dto.QualityProfileSonarr != 0 {
		eff.QualitySonarr = *dto.QualityProfileSonarr
	}
	if !validSeasonScope(eff.SeasonScope) {
		eff.SeasonScope = SeasonScopeAll
	}
	if isAdmin {
		eff.RequiresApproval = false
		eff.AllowSeasonChoice = true
		eff.AllowQualityChoice = true
	}
	return eff, nil
}

func (s *Service) CreateMediaRequest(userID int64, req *CreateRequest) (*CreateResponse, error) {
	if req.MediaType != "movie" && req.MediaType != "tv" && req.MediaType != "book" {
		return nil, fmt.Errorf("unsupported media type: %s", req.MediaType)
	}
	if req.MediaType == "book" && req.ForeignID == "" {
		return nil, fmt.Errorf("foreign_id is required for book requests")
	}

	isAdmin := s.userIsAdmin(userID)
	eff, err := s.effectiveSettings(userID, isAdmin)
	if err != nil {
		return nil, err
	}

	resolved := &resolvedRequest{
		userID:     userID,
		tmdbID:     req.TmdbID,
		tvdbID:     req.TvdbID,
		foreignID:  req.ForeignID,
		bookFormat: normalizeBookFormat(req.BookFormat),
		mediaType:  req.MediaType,
		title:      req.Title,
	}

	// Season scope (TV only). Honor the client's choice only when allowed;
	// otherwise the resolved default stands. Movies keep an empty scope so the
	// stored row / admin queue don't show a meaningless value.
	//
	// An explicit season list (req.Seasons) takes precedence over the coarse
	// scope when season choice is allowed: it's normalized, captured on the
	// resolved request, and JSON-encoded into seasonScope so it persists through
	// the (pending -> approve) flow in the existing season_scope column. The
	// addSeries path then routes it to seasonpass instead of addOptions.Monitor.
	if req.MediaType == "tv" {
		resolved.seasonScope = eff.SeasonScope
		if req.SeasonScope != "" && eff.AllowSeasonChoice && validSeasonScope(req.SeasonScope) {
			resolved.seasonScope = req.SeasonScope
		}
		if eff.AllowSeasonChoice {
			if nums := normalizeSeasonNumbers(req.Seasons); len(nums) > 0 {
				resolved.seasonNumbers = nums
				resolved.seasonScope = encodeSeasonNumbers(nums)
			}
		}
	}

	// Quality profile. Default per service; honor the client's choice only
	// when allowed (out of the box non-admins cannot choose).
	switch req.MediaType {
	case "tv":
		resolved.qualityProfileID = eff.QualitySonarr
	case "movie":
		resolved.qualityProfileID = eff.QualityRadarr
	}
	// Books pick the Chaptarr instance's first quality/metadata profile at add
	// time (addToChaptarr), so they carry no per-user quality profile here.
	if req.QualityProfileID != 0 && eff.AllowQualityChoice && req.MediaType != "book" {
		resolved.qualityProfileID = req.QualityProfileID
	}

	if eff.RequiresApproval {
		return s.createPending(resolved)
	}

	status, title, err := s.addToArr(resolved)
	if err != nil {
		return nil, err
	}
	resolved.title = title
	s.logRequest(resolved, title, status)
	return &CreateResponse{Success: true, Status: status, Title: title}, nil
}

// createPending records a request awaiting admin approval without touching the
// arr services. The stored options are replayed verbatim on approval.
func (s *Service) createPending(r *resolvedRequest) (*CreateResponse, error) {
	// Insert atomically only when no pending row already exists for this user +
	// title, so a double-submit can't create duplicate queue entries (the check +
	// insert is one statement under the single-writer DB). Books have no tmdb_id,
	// so they dedupe/key on the Readarr foreignBookId instead.
	var res sql.Result
	var err error
	if r.mediaType == "book" {
		res, err = s.db.Exec(
			`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, media_type, title, status)
			 SELECT ?, 0, ?, ?, ?, ?, ?
			 WHERE NOT EXISTS (
			     SELECT 1 FROM request_log WHERE user_id = ? AND foreign_id = ? AND COALESCE(book_format, 'both') = ? AND media_type = ? AND status = ?
			 )`,
			r.userID, r.foreignID, normalizeBookFormat(r.bookFormat), r.mediaType, r.title, StatusPending,
			r.userID, r.foreignID, normalizeBookFormat(r.bookFormat), r.mediaType, StatusPending,
		)
	} else {
		res, err = s.db.Exec(
			`INSERT INTO request_log (user_id, tmdb_id, tvdb_id, media_type, title, status, season_scope, quality_profile_id)
			 SELECT ?, ?, ?, ?, ?, ?, ?, ?
			 WHERE NOT EXISTS (
			     SELECT 1 FROM request_log WHERE user_id = ? AND tmdb_id = ? AND media_type = ? AND status = ?
			 )`,
			r.userID, r.tmdbID, sqlNullInt(r.tvdbID), r.mediaType, r.title, StatusPending, sqlNullStr(r.seasonScope), sqlNullInt(r.qualityProfileID),
			r.userID, r.tmdbID, r.mediaType, StatusPending,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("save pending request: %w", err)
	}

	// Cache the tvdb mapping so TV status checks resolve while pending.
	if r.mediaType == "tv" && r.tvdbID != 0 {
		s.db.Exec("INSERT OR REPLACE INTO tmdb_tvdb_cache (tmdb_id, tvdb_id) VALUES (?, ?)", r.tmdbID, r.tvdbID)
	}

	// Only notify admins when a new row was actually queued (not a duplicate).
	if n, _ := res.RowsAffected(); n > 0 && s.notifier != nil {
		data := map[string]interface{}{
			"tmdb_id":    r.tmdbID,
			"media_type": r.mediaType,
			"title":      r.title,
		}
		// Carry the live queue depth so the in-app surface and the home-screen
		// icon can badge the count (push sets aps.badge, the WS client reads it
		// directly).
		if count, err := s.PendingCount(); err == nil {
			data["pending_count"] = count
		}
		s.notifier.NotifyAdmins("request_pending", data)
	}
	return &CreateResponse{Success: true, Status: StatusPending, Title: r.title}, nil
}

// addToArr performs the actual Radarr/Sonarr add and returns the resulting
// status + canonical title. It does NOT write to request_log; callers decide
// whether to insert a new row or update an existing (pending) one.
func (s *Service) addToArr(r *resolvedRequest) (status string, title string, err error) {
	switch r.mediaType {
	case "movie":
		return s.addMovie(r)
	case "tv":
		return s.addSeries(r)
	case "book":
		return s.addToChaptarr(r)
	default:
		return "", "", fmt.Errorf("unsupported media type: %s", r.mediaType)
	}
}

// addToChaptarr adds a book to the requesting user's granted Chaptarr instance.
// Books have no TMDB id, so the request carries the Readarr foreignBookId; we
// resolve it through Chaptarr's lookup (by title, matched on foreignBookId) and
// add the book with the instance's first quality/metadata profile + root folder,
// mirroring the addSeries flow. Verify the exact add payload against a live
// Chaptarr instance — the lookup/add field shapes are the Readarr v1 convention.
func (s *Service) addToChaptarr(r *resolvedRequest) (string, string, error) {
	client := s.getChaptarr(r.userID)
	if client == nil {
		return "", "", fmt.Errorf("chaptarr is not configured for you")
	}

	results, err := client.LookupBook(r.title)
	if err != nil {
		return "", "", fmt.Errorf("book lookup failed: %w", err)
	}
	var match *chaptarr.LookupResult
	for i := range results {
		if results[i].ForeignBookID == r.foreignID {
			match = &results[i]
			break
		}
	}
	if match == nil {
		return "", "", fmt.Errorf("book not found for foreign id %s", r.foreignID)
	}

	qps, err := client.GetQualityProfiles()
	if err != nil || len(qps) == 0 {
		return "", "", fmt.Errorf("no quality profiles available")
	}
	mps, err := client.GetMetadataProfiles()
	if err != nil || len(mps) == 0 {
		return "", "", fmt.Errorf("no metadata profiles available")
	}
	folders, err := client.GetRootFolders()
	if err != nil || len(folders) == 0 {
		return "", "", fmt.Errorf("no root folders available")
	}

	addReq := chaptarr.AddBookRequest{
		ForeignBookID: match.ForeignBookID,
		Monitored:     true,
		AnyEditionOk:  normalizeBookFormat(r.bookFormat) == BookFormatBoth,
	}
	authorName := match.AuthorName
	foreignAuthorID := match.ForeignAuthorID
	if match.Author != nil {
		if authorName == "" {
			authorName = match.Author.AuthorName
		}
		if foreignAuthorID == "" {
			foreignAuthorID = match.Author.ForeignAuthorID
		}
	}
	addReq.Author.AuthorName = authorName
	addReq.Author.ForeignAuthorID = foreignAuthorID
	addReq.Author.QualityProfileID = qps[0].ID
	addReq.Author.MetadataProfileID = mps[0].ID
	addReq.Author.RootFolderPath = folders[0].Path
	addReq.Author.Monitored = true
	addReq.Author.AddOptions.Monitor = "all"
	addReq.AddOptions.SearchForNewBook = true
	if len(match.Editions) > 0 {
		addReq.Editions = make([]chaptarr.Edition, 0, len(match.Editions))
		matchedFormat := false
		for _, edition := range match.Editions {
			edition.Monitored = editionMatchesBookFormat(edition, r.bookFormat)
			if edition.Monitored {
				matchedFormat = true
			}
			addReq.Editions = append(addReq.Editions, edition)
		}
		if !matchedFormat {
			return "", "", fmt.Errorf("no %s edition available for %s", normalizeBookFormat(r.bookFormat), match.Title)
		}
	}

	title := match.Title
	if title == "" {
		title = r.title
	}
	if _, err := client.AddBook(addReq); err != nil {
		return "", "", fmt.Errorf("add book failed: %w", err)
	}
	return StatusRequested, title, nil
}

func (s *Service) addMovie(r *resolvedRequest) (string, string, error) {
	radarrClient := s.getRadarr(r.userID)
	if radarrClient == nil {
		return "", "", fmt.Errorf("radarr is not configured")
	}

	existing, err := radarrClient.GetMovieByTMDB(r.tmdbID)
	if err == nil && existing != nil {
		status := StatusRequested
		if existing.HasFile {
			status = StatusAvailable
		}
		return status, existing.Title, nil
	}

	lookup, err := radarrClient.LookupByTMDB(r.tmdbID)
	if err != nil {
		return "", "", fmt.Errorf("movie lookup failed: %w", err)
	}

	profiles, err := radarrClient.GetQualityProfiles()
	if err != nil || len(profiles) == 0 {
		return "", "", fmt.Errorf("no quality profiles available")
	}
	folders, err := radarrClient.GetRootFolders()
	if err != nil || len(folders) == 0 {
		return "", "", fmt.Errorf("no root folders available")
	}

	profileID := r.qualityProfileID
	if profileID == 0 || !radarrProfileExists(profiles, profileID) {
		profileID = profiles[0].ID
	}

	addReq := &radarr.AddMovieRequest{
		Title:            lookup.Title,
		TmdbID:           lookup.TmdbID,
		Year:             lookup.Year,
		QualityProfileID: profileID,
		RootFolderPath:   folders[0].Path,
		Monitored:        true,
	}
	addReq.AddOptions.SearchForMovie = true

	if err := radarrClient.AddMovie(addReq); err != nil {
		return "", "", fmt.Errorf("add movie failed: %w", err)
	}
	return StatusRequested, lookup.Title, nil
}

func (s *Service) addSeries(r *resolvedRequest) (string, string, error) {
	sonarrClient := s.getSonarr(r.userID)
	if sonarrClient == nil {
		return "", "", fmt.Errorf("sonarr is not configured")
	}

	tvdbID := r.tvdbID
	// A request that arrives with only a TMDB ID — e.g. the AI assistant's
	// requestMedia tool, which sends just tmdb_id + media_type — has nothing for
	// Sonarr's series lookup to match. Resolve the TVDB ID the same way the
	// status path does (cache -> TMDB external IDs -> Trakt) so a TMDB ID alone
	// is enough to add a series.
	if tvdbID == 0 && s.bridge != nil {
		if res, err := s.bridge.ResolveTVDBID(r.tmdbID); err == nil && res != nil && res.TVDBID != 0 {
			tvdbID = res.TVDBID
		}
	}
	if tvdbID != 0 {
		s.db.Exec("INSERT OR REPLACE INTO tmdb_tvdb_cache (tmdb_id, tvdb_id) VALUES (?, ?)", r.tmdbID, tvdbID)
	}

	if tvdbID != 0 {
		existing, err := sonarrClient.GetSeriesByTVDB(tvdbID)
		if err == nil && existing != nil {
			// Series is already in the library. With an explicit season list this
			// is a "request more seasons" action: add the chosen seasons to the
			// existing monitor set (without unmonitoring what's already there) and
			// kick off a per-season search.
			if len(r.seasonNumbers) > 0 {
				if err := s.monitorAndSearchSeasons(sonarrClient, existing, r.seasonNumbers, false); err != nil {
					return "", "", err
				}
				return StatusRequested, existing.Title, nil
			}
			status := StatusRequested
			if existing.Statistics != nil && existing.Statistics.PercentOfEpisodes >= 100 {
				status = StatusAvailable
			} else if existing.Statistics != nil && existing.Statistics.EpisodeFileCount > 0 {
				status = StatusPartial
			}
			return status, existing.Title, nil
		}
	}

	var lookup *sonarr.LookupResult
	var err error
	if tvdbID != 0 {
		lookup, err = sonarrClient.LookupByTVDB(tvdbID)
	}
	if lookup == nil || err != nil {
		if r.title == "" {
			return "", "", fmt.Errorf("series lookup failed: could not resolve a TVDB ID for tmdb %d and no title was provided", r.tmdbID)
		}
		lookup, err = sonarrClient.LookupByTitle(r.title)
		if err != nil {
			return "", "", fmt.Errorf("series lookup failed: %w", err)
		}
		tvdbID = lookup.TvdbID
	}
	// Persist the resolved TVDB id so an approved title-only request stores it.
	r.tvdbID = tvdbID

	profiles, err := sonarrClient.GetQualityProfiles()
	if err != nil || len(profiles) == 0 {
		return "", "", fmt.Errorf("no quality profiles available")
	}
	folders, err := sonarrClient.GetRootFolders()
	if err != nil || len(folders) == 0 {
		return "", "", fmt.Errorf("no root folders available")
	}

	profileID := r.qualityProfileID
	if profileID == 0 || !sonarrProfileExists(profiles, profileID) {
		profileID = profiles[0].ID
	}

	addReq := &sonarr.AddSeriesRequest{
		Title:            lookup.Title,
		TvdbID:           tvdbID,
		Year:             lookup.Year,
		QualityProfileID: profileID,
		RootFolderPath:   folders[0].Path,
		Monitored:        true,
		SeasonFolder:     true,
	}

	// Explicit season list: Sonarr's add-series API has no field for an
	// arbitrary set of season numbers (addOptions.Monitor is a single enum), so
	// a "seasons: [...]" DTO would be silently ignored. Add the series
	// unmonitored with no search, then drive the exact selection via seasonpass
	// + a per-season search. The coarse season_scope path below stays the
	// default for everyone who doesn't pick specific seasons.
	if len(r.seasonNumbers) > 0 {
		addReq.AddOptions.SearchForMissingEpisodes = false
		addReq.AddOptions.Monitor = "none"
		if err := sonarrClient.AddSeries(addReq); err != nil {
			return "", "", fmt.Errorf("add series failed: %w", err)
		}
		added, err := sonarrClient.GetSeriesByTVDB(tvdbID)
		if err != nil || added == nil {
			return "", "", fmt.Errorf("add series failed: could not re-read added series for seasonpass: %w", err)
		}
		if err := s.monitorAndSearchSeasons(sonarrClient, added, r.seasonNumbers, true); err != nil {
			return "", "", err
		}
		return StatusRequested, lookup.Title, nil
	}

	addReq.AddOptions.SearchForMissingEpisodes = true
	addReq.AddOptions.Monitor = sonarrMonitor(r.seasonScope)

	if err := sonarrClient.AddSeries(addReq); err != nil {
		return "", "", fmt.Errorf("add series failed: %w", err)
	}
	return StatusRequested, lookup.Title, nil
}

// monitorAndSearchSeasons monitors exactly (or, when exclusive is false,
// additionally) the chosen seasons on an existing Sonarr series via seasonpass,
// then triggers a SeasonSearch for each chosen season. When exclusive is true
// the chosen seasons are monitored and every other real season is unmonitored;
// when false the chosen seasons are added to whatever is already monitored
// (used for "request more seasons" on a series already in the library). Season
// 0 / Specials is never touched unless explicitly chosen.
func (s *Service) monitorAndSearchSeasons(client *sonarr.Client, series *sonarr.Series, seasons []int, exclusive bool) error {
	chosen := make(map[int]bool, len(seasons))
	for _, n := range seasons {
		chosen[n] = true
	}
	monitored := make(map[int]bool, len(series.Seasons))
	for _, s := range series.Seasons {
		switch {
		case chosen[s.SeasonNumber]:
			monitored[s.SeasonNumber] = true
		case exclusive && s.SeasonNumber > 0:
			// Exclusive selection: unmonitor other real seasons (leave Specials).
			monitored[s.SeasonNumber] = false
		default:
			// Non-exclusive: preserve the season's current monitored state.
			monitored[s.SeasonNumber] = s.Monitored
		}
	}
	// Include any chosen season Sonarr didn't list (defensive; normally all
	// seasons are present once the series is added).
	for n := range chosen {
		if _, ok := monitored[n]; !ok {
			monitored[n] = true
		}
	}
	if err := client.SetSeasonsViaSeasonPass(series.ID, monitored); err != nil {
		return fmt.Errorf("set seasons failed: %w", err)
	}
	for _, n := range seasons {
		// Best-effort: a failed per-season search shouldn't undo the monitor
		// change. Sonarr will still pick up monitored seasons on its next cycle.
		_ = client.TriggerSeasonSearch(series.ID, n)
	}
	return nil
}

// GetStatus reports a title's availability against the GLOBAL default instance
// (userID 0). User-scoped checks go through GetUserStatus, which resolves the
// requesting user's source instance.
func (s *Service) GetStatus(tmdbID int, mediaType string) (*StatusResponse, error) {
	return s.statusFor(0, tmdbID, mediaType)
}

// statusFor reports a title's availability against userID's source instance
// (their per-user default override, else the global default).
func (s *Service) statusFor(userID int64, tmdbID int, mediaType string) (*StatusResponse, error) {
	switch mediaType {
	case "movie":
		return s.getMovieStatus(userID, tmdbID)
	case "tv":
		return s.getTVStatus(userID, tmdbID)
	default:
		return &StatusResponse{Status: StatusUnavailable}, nil
	}
}

// GetUserStatus surfaces a user's own pending/denied request first, then falls
// back to the live arr availability that GetStatus reports.
func (s *Service) GetUserStatus(userID int64, tmdbID int, mediaType string) (*StatusResponse, error) {
	var status string
	err := s.db.QueryRow(
		"SELECT status FROM request_log WHERE user_id = ? AND tmdb_id = ? AND media_type = ? ORDER BY requested_at DESC, id DESC LIMIT 1",
		userID, tmdbID, mediaType,
	).Scan(&status)
	if err == nil {
		// A pending request isn't in the arr yet, so always surface it.
		if status == StatusPending {
			return &StatusResponse{Status: StatusPending}, nil
		}
		// A denied request shows "denied" only while the title isn't otherwise
		// available; if it later lands in the arr, prefer the live state.
		if status == StatusDenied {
			if live, lerr := s.statusFor(userID, tmdbID, mediaType); lerr == nil && live != nil && live.Status != StatusUnavailable {
				return live, nil
			}
			return &StatusResponse{Status: StatusDenied}, nil
		}
	}
	return s.statusFor(userID, tmdbID, mediaType)
}

// GetUserBookStatus reports a user's request state for a book, keyed by the
// Readarr foreignBookId (books have no tmdb_id). It reflects the request_log
// row (pending / denied / requested); deeper library availability + download
// progress are shown in the Books module itself.
func (s *Service) GetUserBookStatus(userID int64, foreignID string) (*StatusResponse, error) {
	var status string
	err := s.db.QueryRow(
		"SELECT status FROM request_log WHERE user_id = ? AND foreign_id = ? AND media_type = 'book' ORDER BY requested_at DESC, id DESC LIMIT 1",
		userID, foreignID,
	).Scan(&status)
	if err != nil {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}
	switch status {
	case StatusPending:
		return &StatusResponse{Status: StatusPending}, nil
	case StatusDenied:
		return &StatusResponse{Status: StatusDenied}, nil
	default:
		return &StatusResponse{Status: StatusRequested}, nil
	}
}

func (s *Service) getMovieStatus(userID int64, tmdbID int) (*StatusResponse, error) {
	radarrClient := s.getRadarr(userID)
	if radarrClient == nil {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}

	movie, err := radarrClient.GetMovieByTMDB(tmdbID)
	if err != nil || movie == nil {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}

	if movie.HasFile {
		return &StatusResponse{Status: StatusAvailable, Progress: 1.0}, nil
	}

	queue, err := radarrClient.GetQueue()
	if err == nil {
		for _, item := range queue {
			if item.MovieID == movie.ID {
				progress := 0.0
				if item.Size > 0 {
					progress = (item.Size - item.Sizeleft) / item.Size
				}
				return &StatusResponse{Status: StatusDownloading, Progress: progress}, nil
			}
		}
	}

	if movie.Monitored {
		return &StatusResponse{Status: StatusRequested, Progress: 0}, nil
	}

	return &StatusResponse{Status: StatusUnavailable}, nil
}

func (s *Service) getTVStatus(userID int64, tmdbID int) (*StatusResponse, error) {
	sonarrClient := s.getSonarr(userID)
	if sonarrClient == nil {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}

	var tvdbID int
	err := s.db.QueryRow("SELECT tvdb_id FROM tmdb_tvdb_cache WHERE tmdb_id = ?", tmdbID).Scan(&tvdbID)
	if err != nil || tvdbID == 0 {
		bridgeResult, err := s.bridge.ResolveTVDBID(tmdbID)
		if err != nil {
			return &StatusResponse{Status: StatusUnavailable}, nil
		}
		tvdbID = bridgeResult.TVDBID
	}

	series, err := sonarrClient.GetSeriesByTVDB(tvdbID)
	if err != nil || series == nil {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}

	seasons := seasonStatuses(series)

	if series.Statistics != nil {
		if series.Statistics.PercentOfEpisodes >= 100 {
			return &StatusResponse{Status: StatusAvailable, Progress: 1.0, Seasons: seasons}, nil
		}
		if series.Statistics.EpisodeFileCount > 0 {
			progress := series.Statistics.PercentOfEpisodes / 100.0
			return &StatusResponse{Status: StatusPartial, Progress: progress, Seasons: seasons}, nil
		}
	}

	if series.Monitored {
		return &StatusResponse{Status: StatusRequested, Progress: 0, Seasons: seasons}, nil
	}

	return &StatusResponse{Status: StatusUnavailable, Seasons: seasons}, nil
}

// seasonStatuses builds the per-season availability breakdown for a series from
// its seasons[].statistics. Season 0 / Specials is excluded to match the rest
// of the app (the TMDB SeasonGrid filters it out too). Each season's status
// mirrors the title vocabulary, derived from file counts + monitoring (queue
// isn't consulted here, matching the title-level TV derivation, which keeps
// status checks to a single Sonarr call).
func seasonStatuses(series *sonarr.Series) []SeasonStatus {
	if series == nil || len(series.Seasons) == 0 {
		return nil
	}
	out := make([]SeasonStatus, 0, len(series.Seasons))
	for _, s := range series.Seasons {
		if s.SeasonNumber <= 0 {
			continue // skip Specials
		}
		ss := SeasonStatus{SeasonNumber: s.SeasonNumber, Status: StatusUnavailable}
		if s.Statistics != nil {
			ss.EpisodeFileCount = s.Statistics.EpisodeFileCount
			ss.EpisodeCount = s.Statistics.EpisodeCount
			switch {
			case s.Statistics.EpisodeCount > 0 && s.Statistics.EpisodeFileCount >= s.Statistics.EpisodeCount:
				ss.Status = StatusAvailable
				ss.Progress = 1.0
			case s.Statistics.EpisodeFileCount > 0:
				ss.Status = StatusPartial
				ss.Progress = float64(s.Statistics.EpisodeFileCount) / float64(s.Statistics.EpisodeCount)
			case s.Monitored:
				ss.Status = StatusRequested
			}
		} else if s.Monitored {
			ss.Status = StatusRequested
		}
		out = append(out, ss)
	}
	return out
}

func (s *Service) GetRequests(userID int64) ([]RequestLog, error) {
	rows, err := s.db.Query(
		"SELECT tmdb_id, media_type, title, status, COALESCE(deny_reason, ''), requested_at FROM request_log WHERE user_id = ? ORDER BY requested_at DESC",
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query requests: %w", err)
	}
	defer rows.Close()

	var requests []RequestLog
	for rows.Next() {
		var r RequestLog
		if err := rows.Scan(&r.TmdbID, &r.MediaType, &r.Title, &r.Status, &r.DenyReason, &r.RequestedAt); err != nil {
			return nil, fmt.Errorf("scan request: %w", err)
		}
		requests = append(requests, r)
	}
	return requests, rows.Err()
}

// ListPending returns the admin approval queue (oldest first).
// PendingCount returns the number of requests awaiting admin approval. It backs
// the badge on the admin approval surface (in-app drawer entry + home-screen
// app icon).
func (s *Service) PendingCount() (int, error) {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE status = ?", StatusPending).Scan(&n); err != nil {
		return 0, fmt.Errorf("count pending requests: %w", err)
	}
	return n, nil
}

func (s *Service) ListPending() ([]PendingRequest, error) {
	rows, err := s.db.Query(
		`SELECT r.id, r.user_id, COALESCE(u.username, ''), r.tmdb_id, COALESCE(r.tvdb_id, 0), r.media_type, r.title, COALESCE(r.book_format, ''), COALESCE(r.season_scope, ''), COALESCE(r.quality_profile_id, 0), r.requested_at
		 FROM request_log r LEFT JOIN users u ON u.id = r.user_id
		 WHERE r.status = ? ORDER BY r.requested_at ASC`,
		StatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("query pending requests: %w", err)
	}
	defer rows.Close()

	var out []PendingRequest
	for rows.Next() {
		var p PendingRequest
		if err := rows.Scan(&p.ID, &p.UserID, &p.Username, &p.TmdbID, &p.TvdbID, &p.MediaType, &p.Title, &p.BookFormat, &p.SeasonScope, &p.QualityProfileID, &p.RequestedAt); err != nil {
			return nil, fmt.Errorf("scan pending request: %w", err)
		}
		p.BookFormat = normalizeBookFormat(p.BookFormat)
		out = append(out, p)
	}
	return out, rows.Err()
}

// loadRequest reads a request_log row into a resolvedRequest plus its status.
func (s *Service) loadRequest(requestID int64) (*resolvedRequest, string, error) {
	var r resolvedRequest
	var status string
	err := s.db.QueryRow(
		"SELECT user_id, tmdb_id, COALESCE(tvdb_id, 0), COALESCE(foreign_id, ''), COALESCE(book_format, ''), media_type, title, status, COALESCE(season_scope, ''), COALESCE(quality_profile_id, 0) FROM request_log WHERE id = ?",
		requestID,
	).Scan(&r.userID, &r.tmdbID, &r.tvdbID, &r.foreignID, &r.bookFormat, &r.mediaType, &r.title, &status, &r.seasonScope, &r.qualityProfileID)
	if err == sql.ErrNoRows {
		return nil, "", fmt.Errorf("request not found")
	}
	if err != nil {
		return nil, "", fmt.Errorf("load request: %w", err)
	}
	r.bookFormat = normalizeBookFormat(r.bookFormat)
	// An explicit season list was stored as JSON in season_scope; decode it so
	// approval replays the seasonpass path the requester chose.
	r.seasonNumbers = decodeSeasonNumbers(r.seasonScope)
	return &r, status, nil
}

// ApproveRequest fulfills a pending request (optionally with admin overrides)
// and marks the row approved. The arr add reuses the normal add path.
func (s *Service) ApproveRequest(adminID, requestID int64, override *DecisionOverride) (*CreateResponse, error) {
	r, status, err := s.loadRequest(requestID)
	if err != nil {
		return nil, err
	}
	if status != StatusPending {
		return nil, fmt.Errorf("request is not pending")
	}
	if override != nil {
		// An admin choosing a coarse scope replaces any explicit season list the
		// requester had picked, so the coarse addOptions.Monitor path is used.
		if override.SeasonScope != "" && validSeasonScope(override.SeasonScope) {
			r.seasonScope = override.SeasonScope
			r.seasonNumbers = nil
		}
		if override.QualityProfileID != 0 {
			r.qualityProfileID = override.QualityProfileID
		}
		if r.mediaType == "book" && validBookFormat(override.BookFormat) {
			r.bookFormat = normalizeBookFormat(override.BookFormat)
		}
	}

	newStatus, title, err := s.addToArr(r)
	if err != nil {
		// Leave the row pending so the admin can retry after fixing config.
		return nil, err
	}

	res, err := s.db.Exec(
		"UPDATE request_log SET status = ?, title = ?, tvdb_id = ?, book_format = ?, season_scope = ?, quality_profile_id = ?, approved_by = ?, decided_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?",
		newStatus, title, sqlNullInt(r.tvdbID), sqlNullStr(r.bookFormat), sqlNullStr(r.seasonScope), sqlNullInt(r.qualityProfileID), adminID, requestID, StatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("update request: %w", err)
	}
	// Lost a race with a concurrent decision: skip the duplicate notification.
	if n, _ := res.RowsAffected(); n == 0 {
		return &CreateResponse{Success: true, Status: newStatus, Title: title}, nil
	}

	if s.notifier != nil {
		s.notifier.NotifyUser(r.userID, "request_decision", map[string]interface{}{
			"decision":   "approved",
			"tmdb_id":    r.tmdbID,
			"media_type": r.mediaType,
			"title":      title,
			"status":     newStatus,
		})
	}
	return &CreateResponse{Success: true, Status: newStatus, Title: title}, nil
}

// DenyRequest marks a pending request denied and notifies the requester.
func (s *Service) DenyRequest(adminID, requestID int64, reason string) error {
	r, status, err := s.loadRequest(requestID)
	if err != nil {
		return err
	}
	if status != StatusPending {
		return fmt.Errorf("request is not pending")
	}
	res, err := s.db.Exec(
		"UPDATE request_log SET status = ?, deny_reason = ?, approved_by = ?, decided_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?",
		StatusDenied, sqlNullStr(reason), adminID, requestID, StatusPending,
	)
	if err != nil {
		return fmt.Errorf("update request: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // already decided by a concurrent action
	}
	if s.notifier != nil {
		s.notifier.NotifyUser(r.userID, "request_decision", map[string]interface{}{
			"decision":   "denied",
			"tmdb_id":    r.tmdbID,
			"media_type": r.mediaType,
			"title":      r.title,
			"reason":     reason,
			"status":     StatusDenied,
		})
	}
	return nil
}

// GetRequestOptions reports what the current user may choose for a request and
// (when allowed) the available quality profiles for the relevant service.
func (s *Service) GetRequestOptions(userID int64, isAdmin bool, mediaType string) (*RequestOptions, error) {
	eff, err := s.effectiveSettings(userID, isAdmin)
	if err != nil {
		return nil, err
	}
	opts := &RequestOptions{
		CanChooseSeason:    eff.AllowSeasonChoice && mediaType == "tv",
		CanChooseQuality:   eff.AllowQualityChoice,
		DefaultSeasonScope: eff.SeasonScope,
		QualityProfiles:    []QualityProfile{},
	}
	if eff.AllowQualityChoice {
		opts.QualityProfiles = s.qualityProfiles(userID, mediaType)
	}
	return opts, nil
}

// qualityProfiles fetches the selectable quality profiles for a media type from
// userID's source instance (userID 0 = global default).
func (s *Service) qualityProfiles(userID int64, mediaType string) []QualityProfile {
	out := []QualityProfile{}
	if mediaType == "tv" {
		if c := s.getSonarr(userID); c != nil {
			if ps, err := c.GetQualityProfiles(); err == nil {
				for _, p := range ps {
					out = append(out, QualityProfile{ID: p.ID, Name: p.Name})
				}
			}
		}
		return out
	}
	if c := s.getRadarr(userID); c != nil {
		if ps, err := c.GetQualityProfiles(); err == nil {
			for _, p := range ps {
				out = append(out, QualityProfile{ID: p.ID, Name: p.Name})
			}
		}
	}
	return out
}

// GetAdminSettings returns the global defaults plus both arrs' quality profiles
// for the admin settings editor.
func (s *Service) GetAdminSettings() *AdminSettingsView {
	return &AdminSettingsView{
		Settings:       s.GetGlobalSettings(),
		RadarrProfiles: s.qualityProfiles(0, "movie"),
		SonarrProfiles: s.qualityProfiles(0, "tv"),
	}
}

// insertRequest writes a request_log row and returns its id.
func (s *Service) insertRequest(r *resolvedRequest, title, status string) (int64, error) {
	res, err := s.db.Exec(
		"INSERT INTO request_log (user_id, tmdb_id, tvdb_id, foreign_id, book_format, media_type, title, status, season_scope, quality_profile_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.userID, r.tmdbID, sqlNullInt(r.tvdbID), sqlNullStr(r.foreignID), sqlNullStr(r.bookFormat), r.mediaType, title, status, sqlNullStr(r.seasonScope), sqlNullInt(r.qualityProfileID),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// logRequest records a fulfilled request without failing the caller (the arr
// add already succeeded; a history-row failure should not surface as an error).
func (s *Service) logRequest(r *resolvedRequest, title, status string) {
	_, _ = s.insertRequest(r, title, status)
}

// sqlNullInt / sqlNullStr map zero values to NULL for nullable columns.
func sqlNullInt(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func sqlNullStr(v string) interface{} {
	if v == "" {
		return nil
	}
	return v
}

// encodeSeasonNumbers serializes an explicit season list for storage in the
// season_scope column (e.g. "[3,5]"). It sorts + de-dups so the stored value is
// stable and the admin queue shows a tidy list. Returns "" for an empty list.
func encodeSeasonNumbers(seasons []int) string {
	cleaned := normalizeSeasonNumbers(seasons)
	if len(cleaned) == 0 {
		return ""
	}
	data, err := json.Marshal(cleaned)
	if err != nil {
		return ""
	}
	return string(data)
}

// decodeSeasonNumbers parses a season_scope value that holds an explicit season
// list (a JSON array like "[3,5]"). A coarse scope ("all"/"first"/...) or any
// non-array value yields nil, so the caller falls back to the coarse path.
func decodeSeasonNumbers(scope string) []int {
	if len(scope) == 0 || scope[0] != '[' {
		return nil
	}
	var seasons []int
	if err := json.Unmarshal([]byte(scope), &seasons); err != nil {
		return nil
	}
	return normalizeSeasonNumbers(seasons)
}

// normalizeSeasonNumbers sorts ascending, de-dups, and drops negative season
// numbers. Season 0 (Specials) is allowed through if the caller explicitly
// selected it.
func normalizeSeasonNumbers(seasons []int) []int {
	seen := make(map[int]bool, len(seasons))
	out := make([]int, 0, len(seasons))
	for _, n := range seasons {
		if n < 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

func validSeasonScope(scope string) bool {
	switch scope {
	case SeasonScopeAll, SeasonScopeFirst, SeasonScopeLatest, SeasonScopePilot:
		return true
	}
	return false
}

func normalizeBookFormat(format string) string {
	switch format {
	case BookFormatEbook, BookFormatAudiobook, BookFormatBoth:
		return format
	default:
		return BookFormatBoth
	}
}

func validBookFormat(format string) bool {
	return format == BookFormatEbook ||
		format == BookFormatAudiobook ||
		format == BookFormatBoth
}

func editionMatchesBookFormat(edition chaptarr.Edition, format string) bool {
	switch normalizeBookFormat(format) {
	case BookFormatBoth:
		return true
	case BookFormatAudiobook:
		return chaptarrEditionFormat(edition) == BookFormatAudiobook
	default:
		return chaptarrEditionFormat(edition) == BookFormatEbook
	}
}

func chaptarrEditionFormat(edition chaptarr.Edition) string {
	if !edition.IsEbook {
		return BookFormatAudiobook
	}
	if chaptarr.FormatOf(edition.Format) == BookFormatAudiobook {
		return BookFormatAudiobook
	}
	return BookFormatEbook
}

// sonarrMonitor maps a season scope to Sonarr's addOptions.monitor enum.
func sonarrMonitor(scope string) string {
	switch scope {
	case SeasonScopeFirst:
		return "firstSeason"
	case SeasonScopeLatest:
		return "lastSeason"
	case SeasonScopePilot:
		return "pilot"
	default:
		return "all"
	}
}

func radarrProfileExists(profiles []radarr.QualityProfile, id int) bool {
	for _, p := range profiles {
		if p.ID == id {
			return true
		}
	}
	return false
}

func sonarrProfileExists(profiles []sonarr.QualityProfile, id int) bool {
	for _, p := range profiles {
		if p.ID == id {
			return true
		}
	}
	return false
}
