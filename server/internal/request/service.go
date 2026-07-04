package request

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/windoze95/cantinarr-server/internal/cache"
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
	// libraryCache holds reduced Chaptarr library digests keyed by instance id,
	// so the owned-books digest doesn't refetch the whole library on every call.
	libraryCache *cache.Cache
}

func NewService(db *sql.DB, registry *instance.Registry, bridge *tmdb.Bridge, notifier Notifier) *Service {
	return &Service{
		db:           db,
		registry:     registry,
		bridge:       bridge,
		notifier:     notifier,
		libraryCache: cache.New(),
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

// getChaptarr returns the Chaptarr client for the user's granted instance. A
// user grant is still required for non-admins; admins fall back to the configured
// default/first Chaptarr instance so they can request books without assigning
// themselves a per-user grant.
func (s *Service) getChaptarr(userID int64) *chaptarr.Client {
	if s.registry != nil {
		client, _, err := s.registry.GetUserChaptarrClient(userID)
		if err == nil && client != nil {
			return client
		}
		if s.userIsAdmin(userID) {
			client, _, err := s.registry.GetDefaultChaptarrClient()
			if err == nil && client != nil {
				return client
			}
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
	// present & non-empty exactly these seasons are monitored (additively for a
	// series already in the library), overriding the coarse SeasonScope.
	// Empty/absent keeps the existing season_scope behavior.
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
	// BookFormats maps each already-requested book format ("ebook"/"audiobook")
	// to its request status, so the dashboard can still offer the other format
	// after one is requested. Only populated for book status; nil (omitted) for
	// movies/TV. A stored "both" request covers both formats.
	BookFormats map[string]string `json:"book_formats,omitempty"`
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
	// (overrides seasonScope). It round-trips through the approval flow by
	// being JSON-encoded into the season_scope column.
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
	// addSeries path then monitors exactly those seasons instead of using the
	// coarse addOptions.Monitor enum.
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
		// The foreignBookId belongs to a book already in the library (owned
		// records carry a library foreignBookId the metadata lookup can't match);
		// complete the missing format from the existing records instead of adding.
		return s.completeOwnedBook(client, r)
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

	title := match.Title
	if title == "" {
		title = r.title
	}
	titleSlug := match.TitleSlug
	if titleSlug == "" {
		titleSlug = fallbackTitleSlug(title)
	}

	// Chaptarr stores a title's ebook and audiobook as separate book records
	// (same foreignBookId, different mediaType), so a "both" request adds the
	// book once per format. Adding at least one record counts as requested; the
	// last error is surfaced only if every requested format failed.
	formats := chaptarrRequestFormats(r.bookFormat)
	var lastErr error
	added := 0
	for _, mediaType := range formats {
		if err := s.addChaptarrBookRecord(client, match, qps[0].ID, mps[0].ID, folders[0].Path, title, titleSlug, mediaType); err != nil {
			lastErr = err
			continue
		}
		added++
	}
	if added == 0 {
		return "", "", fmt.Errorf("add book failed: %w", lastErr)
	}
	return StatusRequested, title, nil
}

// completeOwnedBook fulfils a request whose foreignBookId is an owned library
// record (not a current metadata id, so the lookup above couldn't match it): it
// monitors and searches the existing record(s) for the requested format(s) to
// fetch the missing file, rather than adding a fresh book.
func (s *Service) completeOwnedBook(client *chaptarr.Client, r *resolvedRequest) (string, string, error) {
	books, err := client.GetAllBooks()
	if err != nil {
		return "", "", fmt.Errorf("book not found for foreign id %s", r.foreignID)
	}
	title, byFormat := recordsByForeignID(books, r.foreignID)
	if title == "" {
		return "", "", fmt.Errorf("book not found for foreign id %s", r.foreignID)
	}

	var lastErr error
	done := 0
	for _, mediaType := range chaptarrRequestFormats(r.bookFormat) {
		rec := byFormat[mediaType]
		if rec == nil {
			lastErr = fmt.Errorf("no %s edition of %q exists to complete", mediaType, title)
			continue
		}
		if err := client.SetBookMonitored([]int{rec.ID}, true); err != nil {
			lastErr = fmt.Errorf("monitor %s: %w", mediaType, err)
			continue
		}
		_ = client.TriggerBookSearch([]int{rec.ID})
		done++
	}
	if done == 0 {
		return "", "", fmt.Errorf("could not complete %q: %w", title, lastErr)
	}
	return StatusRequested, title, nil
}

// addChaptarrBookRecord adds one format record (ebook or audiobook) of a book and
// ensures it ends up monitored and searched. Chaptarr tracks format at the book
// level via mediaType, so each requested format is its own record.
func (s *Service) addChaptarrBookRecord(client *chaptarr.Client, match *chaptarr.LookupResult, qualityProfileID, metadataProfileID int, rootFolderPath, title, titleSlug, mediaType string) error {
	addReq := chaptarr.AddBookRequest{
		ForeignBookID: match.ForeignBookID,
		Title:         title,
		TitleSlug:     titleSlug,
		Monitored:     true,
	}
	setChaptarrMediaType(&addReq, mediaType)

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
	addReq.Author.QualityProfileID = qualityProfileID
	addReq.Author.MetadataProfileID = metadataProfileID
	addReq.Author.RootFolderPath = rootFolderPath
	addReq.Author.Monitored = true
	addReq.Author.AddOptions.Monitor = "all"
	addReq.AddOptions.SearchForNewBook = true

	// Round-trip the lookup's editions verbatim, marking them monitored so
	// Chaptarr tracks the book (an add with no editions stays unmonitored).
	// patchEditionForAdd also guards Chaptarr's NOT NULL links/images columns so
	// the add can't fail a SQLite constraint.
	if len(match.Editions) > 0 {
		addReq.Editions = make([]json.RawMessage, 0, len(match.Editions))
		for _, raw := range match.Editions {
			patched, ok, err := patchEditionForAdd(raw)
			if err != nil {
				return fmt.Errorf("prepare edition: %w", err)
			}
			if !ok {
				continue // skip a non-object edition element rather than emit junk
			}
			addReq.Editions = append(addReq.Editions, patched)
		}
	}

	book, err := client.AddBook(addReq)
	if err != nil {
		return err
	}
	// A book added under an author created by this same request comes back
	// unmonitored (Chaptarr's async author refresh hasn't applied monitoring),
	// so the format flags and searchForNewBook never take effect. Monitor +
	// search it explicitly; SetBookMonitored re-derives the format from the
	// mediaType set above. Books under an already-tracked author come back
	// monitored and need nothing further. Best-effort: the add already succeeded.
	if book != nil && book.ID != 0 && !book.Monitored {
		if err := client.SetBookMonitored([]int{book.ID}, true); err == nil {
			_ = client.TriggerBookSearch([]int{book.ID})
		}
	}
	return nil
}

func (s *Service) addMovie(r *resolvedRequest) (string, string, error) {
	radarrClient := s.getRadarr(r.userID)
	if radarrClient == nil {
		return "", "", fmt.Errorf("radarr is not configured")
	}

	existing, err := radarrClient.GetMovieByTMDB(r.tmdbID)
	if err == nil && existing != nil {
		if existing.HasFile {
			return StatusAvailable, existing.Title, nil
		}
		// The movie is in Radarr without a file. If it isn't monitored Radarr
		// will never grab it, so a fresh request revives it (monitor + search)
		// instead of reporting "requested" while nothing would ever happen.
		if !existing.Monitored {
			if err := radarrClient.SetMoviesMonitored([]int{existing.ID}, true); err != nil {
				return "", "", fmt.Errorf("monitor movie failed: %w", err)
			}
			// Best-effort: with the movie monitored again, RSS will still pick
			// it up even if this immediate search fails.
			_ = radarrClient.TriggerMoviesSearch([]int{existing.ID})
		}
		return StatusRequested, existing.Title, nil
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
				if err := s.monitorAndSearchSeasons(sonarrClient, existing, r.seasonNumbers); err != nil {
					return "", "", err
				}
				return StatusRequested, existing.Title, nil
			}
			return s.requestExistingSeries(sonarrClient, existing, r)
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

	// Explicit season list: Sonarr's addOptions.monitor enum has no "these
	// specific seasons" value, but the add payload's seasons[].monitored flags
	// survive the add and its async metadata refresh, and Sonarr applies
	// episode monitoring from them (and runs the missing-episode search) once
	// the refresh completes. Adding unmonitored and fixing monitoring up
	// afterwards is NOT safe here: the refresh applies addOptions.monitor
	// asynchronously and would race with — and overwrite — any immediate
	// follow-up monitoring calls.
	if len(r.seasonNumbers) > 0 {
		addReq.Seasons = seasonSelection(lookup.Seasons, r.seasonNumbers)
		addReq.AddOptions.SearchForMissingEpisodes = true
		if err := sonarrClient.AddSeries(addReq); err != nil {
			return "", "", fmt.Errorf("add series failed: %w", err)
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

// monitorAndSearchSeasons additively monitors the chosen seasons on an existing
// Sonarr series ("request more seasons"), then triggers a SeasonSearch for each
// chosen season. Seasons that aren't chosen keep their current monitored state;
// a deliberate re-request of the same seasons re-searches them.
func (s *Service) monitorAndSearchSeasons(client *sonarr.Client, series *sonarr.Series, seasons []int) error {
	if _, err := s.monitorSeasons(client, series, seasons); err != nil {
		return err
	}
	for _, n := range seasons {
		// Best-effort: a failed per-season search shouldn't undo the monitor
		// change. Sonarr will still pick up monitored episodes on its next cycle.
		_ = client.TriggerSeasonSearch(series.ID, n)
	}
	return nil
}

// monitorSeasons makes sure the series itself, the chosen seasons, and the
// chosen seasons' episodes are all monitored, preserving the monitored state of
// everything else. It returns the chosen seasons where anything actually had to
// change, so callers can decide what deserves a fresh search.
//
// The per-episode pass matters: Sonarr's series update only syncs episode
// monitoring for seasons whose flag CHANGES, so a season already flagged
// monitored can still hold unmonitored episodes that no search would ever grab.
func (s *Service) monitorSeasons(client *sonarr.Client, series *sonarr.Series, seasons []int) (changed []int, err error) {
	flags := make(map[int]bool, len(series.Seasons)+len(seasons))
	for _, ss := range series.Seasons {
		flags[ss.SeasonNumber] = ss.Monitored
	}
	flagChanged := make(map[int]bool, len(seasons))
	for _, n := range seasons {
		if !flags[n] {
			flagChanged[n] = true
		}
		flags[n] = true
	}
	if err := client.UpdateSeriesMonitoring(series.ID, true, flags); err != nil {
		return nil, fmt.Errorf("set seasons failed: %w", err)
	}
	for _, n := range seasons {
		episodes, err := client.GetEpisodes(series.ID, n)
		if err != nil {
			return nil, fmt.Errorf("load season %d episodes: %w", n, err)
		}
		ids := make([]int, 0, len(episodes))
		for _, e := range episodes {
			if !e.Monitored {
				ids = append(ids, e.ID)
			}
		}
		if err := client.SetEpisodesMonitored(ids, true); err != nil {
			return nil, fmt.Errorf("monitor season %d episodes: %w", n, err)
		}
		if len(ids) > 0 || flagChanged[n] || !series.Monitored {
			changed = append(changed, n)
		}
	}
	return changed, nil
}

// requestExistingSeries fulfills a coarse-scope request for a series Sonarr
// already tracks. Returning the current availability without touching Sonarr
// (the old behavior) made re-requesting a dormant series — unmonitored, or
// monitored with unmonitored episodes — a silent no-op that could even report
// "available", since Sonarr's percentOfEpisodes only counts monitored episodes.
// Instead, apply the scope additively to the seasons that are still missing
// files, and search only the seasons where something actually changed so
// repeated requests don't spam the indexers.
func (s *Service) requestExistingSeries(client *sonarr.Client, existing *sonarr.Series, r *resolvedRequest) (string, string, error) {
	if r.seasonScope == SeasonScopePilot {
		if err := s.monitorPilot(client, existing); err != nil {
			return "", "", err
		}
		return StatusRequested, existing.Title, nil
	}
	var incomplete []int
	for _, n := range scopeSeasonNumbers(existing, r.seasonScope) {
		if !seasonHasAllFiles(existing, n) {
			incomplete = append(incomplete, n)
		}
	}
	if len(incomplete) > 0 {
		changed, err := s.monitorSeasons(client, existing, incomplete)
		if err != nil {
			return "", "", err
		}
		if len(changed) > 0 {
			for _, n := range changed {
				// Best-effort, same as monitorAndSearchSeasons.
				_ = client.TriggerSeasonSearch(existing.ID, n)
			}
			return StatusRequested, existing.Title, nil
		}
	}
	// Nothing needed to change: report honest completeness (never Sonarr's
	// monitored-only percentOfEpisodes; see getTVStatus).
	files, total := existing.EpisodeTotals()
	status, _ := statusFromCompletion(sonarr.Completion{Files: files, Aired: total}, existing.Monitored)
	if status == StatusUnavailable {
		// The series is in Sonarr and this request just (re)confirmed it;
		// nothing on disk yet still reads as an accepted request.
		status = StatusRequested
	}
	return status, existing.Title, nil
}

// monitorPilot makes sure S1E1 of an existing series is monitored and searched.
// The pilot scope is episode-level, so it can't be expressed as season
// monitoring; matching Sonarr's own pilot handling, the season flag is left
// alone (Sonarr deliberately doesn't monitor season 1 for a pilot-only add).
func (s *Service) monitorPilot(client *sonarr.Client, series *sonarr.Series) error {
	first := 0
	for _, ss := range series.Seasons {
		if ss.SeasonNumber > 0 && (first == 0 || ss.SeasonNumber < first) {
			first = ss.SeasonNumber
		}
	}
	if first == 0 {
		return fmt.Errorf("series has no seasons to request")
	}
	episodes, err := client.GetEpisodes(series.ID, first)
	if err != nil {
		return fmt.Errorf("load season %d episodes: %w", first, err)
	}
	for _, e := range episodes {
		if e.EpisodeNumber != 1 {
			continue
		}
		if e.HasFile {
			return nil
		}
		if !series.Monitored {
			if err := client.UpdateSeriesMonitoring(series.ID, true, nil); err != nil {
				return fmt.Errorf("monitor series failed: %w", err)
			}
		}
		if !e.Monitored {
			if err := client.SetEpisodesMonitored([]int{e.ID}, true); err != nil {
				return fmt.Errorf("monitor pilot episode: %w", err)
			}
		}
		_ = client.TriggerEpisodeSearch([]int{e.ID})
		return nil
	}
	return fmt.Errorf("pilot episode not found")
}

// scopeSeasonNumbers expands a coarse season scope to concrete season numbers
// against the series' real (non-Specials) seasons. The pilot scope is handled
// separately because it's episode-level.
func scopeSeasonNumbers(series *sonarr.Series, scope string) []int {
	real := make([]int, 0, len(series.Seasons))
	for _, ss := range series.Seasons {
		if ss.SeasonNumber > 0 {
			real = append(real, ss.SeasonNumber)
		}
	}
	sort.Ints(real)
	if len(real) == 0 {
		return nil
	}
	switch scope {
	case SeasonScopeFirst:
		return real[:1]
	case SeasonScopeLatest:
		return real[len(real)-1:]
	default: // all
		return real
	}
}

// seasonHasAllFiles reports whether a season already has a file for every
// episode Sonarr knows about. It prefers totalEpisodeCount (which includes
// unaired episodes) so an in-progress season still counts as incomplete and a
// request for it keeps it monitored.
func seasonHasAllFiles(series *sonarr.Series, seasonNumber int) bool {
	for _, ss := range series.Seasons {
		if ss.SeasonNumber != seasonNumber {
			continue
		}
		if ss.Statistics == nil {
			return false
		}
		total := ss.Statistics.TotalEpisodeCount
		if total == 0 {
			total = ss.Statistics.EpisodeCount
		}
		return total > 0 && ss.Statistics.EpisodeFileCount >= total
	}
	return false
}

// seasonSelection builds the seasons array for an explicit-season add: every
// season Sonarr's lookup knows about, monitored only when chosen (Specials stay
// unmonitored unless explicitly chosen). Chosen seasons the lookup doesn't list
// are included defensively so a stale metadata season list can't silently drop
// part of the request.
func seasonSelection(known []sonarr.SeasonResource, chosen []int) []sonarr.SeasonResource {
	chosenSet := make(map[int]bool, len(chosen))
	for _, n := range chosen {
		chosenSet[n] = true
	}
	out := make([]sonarr.SeasonResource, 0, len(known)+len(chosen))
	seen := make(map[int]bool, len(known))
	for _, ss := range known {
		seen[ss.SeasonNumber] = true
		out = append(out, sonarr.SeasonResource{SeasonNumber: ss.SeasonNumber, Monitored: chosenSet[ss.SeasonNumber]})
	}
	for _, n := range chosen {
		if !seen[n] {
			out = append(out, sonarr.SeasonResource{SeasonNumber: n, Monitored: true})
		}
	}
	return out
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
// Readarr foreignBookId (books have no tmdb_id). Status is the collapsed
// (latest) request_log state (pending / denied / requested); BookFormats breaks
// it down per format so the dashboard can still offer the other format after one
// is requested. A stored "both" request covers both ebook and audiobook. Deeper
// library availability + download progress are shown in the Books module itself.
func (s *Service) GetUserBookStatus(userID int64, foreignID string) (*StatusResponse, error) {
	rows, err := s.db.Query(
		"SELECT COALESCE(book_format, 'both'), status FROM request_log WHERE user_id = ? AND foreign_id = ? AND media_type = 'book' ORDER BY requested_at DESC, id DESC",
		userID, foreignID,
	)
	if err != nil {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}
	defer rows.Close()

	formats := map[string]string{}
	collapsed := ""
	for rows.Next() {
		var format, status string
		if err := rows.Scan(&format, &status); err != nil {
			return &StatusResponse{Status: StatusUnavailable}, nil
		}
		if collapsed == "" {
			collapsed = status // first row is the latest overall
		}
		// Rows are newest-first, so only record a format's status the first
		// (latest) time it appears. A "both" row fills both concrete formats.
		for _, f := range expandBookFormat(format) {
			if _, ok := formats[f]; !ok {
				formats[f] = status
			}
		}
	}
	if collapsed == "" {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}

	resp := &StatusResponse{BookFormats: formats}
	switch collapsed {
	case StatusPending:
		resp.Status = StatusPending
	case StatusDenied:
		resp.Status = StatusDenied
	default:
		resp.Status = StatusRequested
	}
	return resp, nil
}

// expandBookFormat maps a stored book_format to the concrete formats it covers:
// "both" (and any unrecognized value) covers both ebook and audiobook.
func expandBookFormat(format string) []string {
	switch normalizeBookFormat(format) {
	case BookFormatEbook:
		return []string{BookFormatEbook}
	case BookFormatAudiobook:
		return []string{BookFormatAudiobook}
	default:
		return []string{BookFormatEbook, BookFormatAudiobook}
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
		if s.bridge == nil {
			return &StatusResponse{Status: StatusUnavailable}, nil
		}
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

	// Derive availability from the real episode list: "available" strictly
	// means every aired episode has a file. Sonarr's percentOfEpisodes (and
	// its season episodeCount) only count monitored episodes, so a series with
	// two monitored, downloaded episodes and the rest unmonitored would read
	// 100% / "available" while most of it is missing.
	if episodes, epErr := sonarrClient.GetAllEpisodes(series.ID); epErr == nil {
		completion, bySeason := sonarr.SeriesCompletion(episodes, time.Now())
		status, progress := statusFromCompletion(completion, series.Monitored)
		return &StatusResponse{
			Status:   status,
			Progress: progress,
			Seasons:  seasonStatusesFromCompletion(series, bySeason),
		}, nil
	}

	// Fallback (episode fetch failed): season-statistics totals. Stricter than
	// the aired-aware path — unaired episodes count as missing — but still
	// immune to the monitored-episodes-only skew.
	seasons := seasonStatuses(series)
	files, total := series.EpisodeTotals()
	status, progress := statusFromCompletion(sonarr.Completion{Files: files, Aired: total}, series.Monitored)
	return &StatusResponse{Status: status, Progress: progress, Seasons: seasons}, nil
}

// statusFromCompletion maps on-disk completeness (plus the series' monitored
// flag) onto the request status vocabulary: complete → available, anything on
// disk → partial (the button offers "Request More"), nothing on disk →
// requested when monitored, else unavailable.
func statusFromCompletion(c sonarr.Completion, monitored bool) (string, float64) {
	switch {
	case c.Complete():
		return StatusAvailable, 1.0
	case c.Files > 0:
		progress := 0.0
		if c.Aired > 0 {
			progress = float64(c.Files) / float64(c.Aired)
		}
		return StatusPartial, progress
	case monitored:
		return StatusRequested, 0
	default:
		return StatusUnavailable, 0
	}
}

// seasonStatusesFromCompletion builds the per-season availability breakdown
// from real episode counts (see SeriesCompletion). EpisodeCount is the aired
// (obtainable) episode count, so the app's "x/y eps" label reflects true
// completeness. Season 0 / Specials is excluded to match the rest of the app.
func seasonStatusesFromCompletion(series *sonarr.Series, bySeason map[int]sonarr.Completion) []SeasonStatus {
	monitored := make(map[int]bool, len(series.Seasons))
	numbers := make([]int, 0, len(series.Seasons)+len(bySeason))
	for _, s := range series.Seasons {
		monitored[s.SeasonNumber] = s.Monitored
		numbers = append(numbers, s.SeasonNumber)
	}
	// Include seasons that have episodes but are missing from series.Seasons
	// (defensive; normally the seasons array covers them all).
	for n := range bySeason {
		if _, ok := monitored[n]; !ok {
			numbers = append(numbers, n)
		}
	}
	sort.Ints(numbers)

	out := make([]SeasonStatus, 0, len(numbers))
	for _, n := range numbers {
		if n <= 0 {
			continue // skip Specials
		}
		c := bySeason[n]
		status, progress := statusFromCompletion(c, monitored[n])
		out = append(out, SeasonStatus{
			SeasonNumber:     n,
			EpisodeFileCount: c.Files,
			EpisodeCount:     c.Aired,
			Status:           status,
			Progress:         progress,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// seasonStatuses builds the per-season availability breakdown for a series
// from its seasons[].statistics — the fallback when the episode list couldn't
// be fetched (see seasonStatusesFromCompletion for the primary path). Season
// totals use totalEpisodeCount, NOT episodeCount: Sonarr's episodeCount only
// counts monitored episodes, which is exactly the skew that made half-empty
// seasons read "available". totalEpisodeCount includes unaired episodes, so
// this fallback under-reports availability for airing seasons rather than
// over-reporting it. Season 0 / Specials is excluded to match the rest of the
// app (the TMDB SeasonGrid filters it out too).
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
			total := s.Statistics.TotalEpisodeCount
			if total == 0 {
				total = s.Statistics.EpisodeCount
			}
			ss.EpisodeFileCount = s.Statistics.EpisodeFileCount
			ss.EpisodeCount = total
			ss.Status, ss.Progress = statusFromCompletion(
				sonarr.Completion{Files: s.Statistics.EpisodeFileCount, Aired: total}, s.Monitored)
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
	// approval replays the explicit season selection the requester chose.
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

// chaptarrRequestFormats expands a requested book format into the concrete
// Chaptarr media types to add. Chaptarr stores a title's ebook and audiobook as
// separate book records (same foreignBookId, different mediaType), so "both"
// expands to two adds rather than one record flagged for both.
func chaptarrRequestFormats(format string) []string {
	switch normalizeBookFormat(format) {
	case BookFormatEbook:
		return []string{"ebook"}
	case BookFormatAudiobook:
		return []string{"audiobook"}
	default: // both
		return []string{"ebook", "audiobook"}
	}
}

// setChaptarrMediaType pins one add-book payload to a single Chaptarr media type
// (ebook or audiobook) and its matching monitor flag. This fork tracks format at
// the book level via mediaType — its lookup editions carry no format — so format
// intent is expressed here, not by selecting an edition. A book record holds one
// format; the flag that doesn't match mediaType is ignored by Chaptarr.
func setChaptarrMediaType(req *chaptarr.AddBookRequest, mediaType string) {
	req.MediaType = mediaType
	ebook := mediaType == "ebook"
	audiobook := mediaType == "audiobook"
	req.EbookMonitored = &ebook
	req.AudiobookMonitored = &audiobook
}

// patchEditionForAdd prepares one lookup edition for the add payload: it marks
// the edition monitored/manualAdd and guarantees the NOT NULL links and images
// arrays survive. The edition is otherwise passed through verbatim — Chaptarr's
// Editions table rejects a null links or images, and the lookup result already
// carries both. ok is false when the element is a JSON null (which decodes to a
// nil map) — Chaptarr never sends that, but guarding it avoids a nil-map-write
// panic; the caller skips such elements.
func patchEditionForAdd(raw json.RawMessage) (out json.RawMessage, ok bool, err error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false, err
	}
	if obj == nil {
		return nil, false, nil
	}
	t := json.RawMessage("true")
	obj["monitored"] = t
	obj["manualAdd"] = t
	if v, ok := obj["links"]; !ok || string(v) == "null" {
		obj["links"] = json.RawMessage("[]")
	}
	if v, ok := obj["images"]; !ok || string(v) == "null" {
		obj["images"] = json.RawMessage("[]")
	}
	out, err = json.Marshal(obj)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func fallbackTitleSlug(title string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if b.Len() > 0 && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
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
