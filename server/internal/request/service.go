package request

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
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

var (
	ErrChaptarrInstanceForbidden = errors.New("chaptarr instance is not available to you")
	ErrChaptarrInstanceInvalid   = errors.New("invalid chaptarr instance")
	ErrBookFormatUnresolved      = errors.New("book format is unsupported or ambiguous")
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
	libraryCache    *cache.Cache
	decisionLocks   [64]sync.Mutex
	bookLocks       [64]sync.Mutex
	projectionLocks [32]sync.Mutex
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

func (s *Service) bookLock(key string) *sync.Mutex {
	return &s.bookLocks[stripeHash(key)%uint32(len(s.bookLocks))]
}

func (s *Service) decisionLock(requestID int64) *sync.Mutex {
	return &s.decisionLocks[uint64(requestID)%uint64(len(s.decisionLocks))]
}

func (s *Service) projectionLock(instanceID string) *sync.Mutex {
	return &s.projectionLocks[stripeHash(instanceID)%uint32(len(s.projectionLocks))]
}

func stripeHash(value string) uint32 {
	const prime32 = uint32(16777619)
	hash := uint32(2166136261)
	for i := 0; i < len(value); i++ {
		hash ^= uint32(value[i])
		hash *= prime32
	}
	return hash
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
	client, _, _ := s.resolveChaptarr(userID, "")
	return client
}

// resolveChaptarr resolves an explicitly selected instance when present and
// enforces requester access before returning a client. Admins may select any
// configured Chaptarr; omitted IDs preserve the legacy effective-instance
// behavior.
func (s *Service) resolveChaptarr(userID int64, instanceID string) (*chaptarr.Client, string, error) {
	if s.registry == nil {
		return nil, "", nil
	}
	if instanceID != "" {
		if !s.userIsAdmin(userID) {
			allowed, err := s.registry.UserCanAccessInstance(userID, instanceID, "chaptarr")
			if err != nil {
				return nil, "", fmt.Errorf("check chaptarr access: %w", err)
			}
			if !allowed {
				return nil, "", ErrChaptarrInstanceForbidden
			}
		}
		client, err := s.registry.GetChaptarrClient(instanceID)
		if err != nil {
			return nil, "", ErrChaptarrInstanceInvalid
		}
		return client, instanceID, nil
	}
	client, id, err := s.registry.GetUserChaptarrClient(userID)
	if err != nil {
		return nil, "", err
	}
	if client != nil {
		return client, id, nil
	}
	if s.userIsAdmin(userID) {
		return s.registry.GetDefaultChaptarrClient()
	}
	return nil, "", nil
}

type CreateRequest struct {
	TmdbID    int    `json:"tmdb_id"`
	MediaType string `json:"media_type"`
	Title     string `json:"title"`
	TvdbID    int    `json:"tvdb_id"`
	// ForeignID is the Chaptarr/Readarr foreignBookId for book requests, which
	// have no TMDB id. Required when media_type == "book"; ignored otherwise.
	ForeignID  string `json:"foreign_id"`
	BookFormat string `json:"book_format"`
	// InstanceID pins book operations to the Chaptarr instance the requester is
	// viewing. It is optional for backward compatibility; omitted uses the
	// user's effective grant (or the admin default).
	InstanceID       string `json:"instance_id,omitempty"`
	SeasonScope      string `json:"season_scope"`
	QualityProfileID int    `json:"quality_profile_id"`
	// Seasons is an optional explicit list of season numbers (TV only). When
	// present & non-empty exactly these seasons are monitored (additively for a
	// series already in the library), overriding the coarse SeasonScope.
	// Empty/absent keeps the existing season_scope behavior.
	Seasons []int `json:"seasons,omitempty"`
}

type CreateResponse struct {
	Success     bool              `json:"success"`
	Status      string            `json:"status"`
	Title       string            `json:"title"`
	BookFormats map[string]string `json:"book_formats,omitempty"`
}

type StatusResponse struct {
	Status      string  `json:"status"`
	Progress    float64 `json:"progress"`
	StatusKnown *bool   `json:"status_known,omitempty"`
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
	ForeignID   string    `json:"foreign_id,omitempty"`
	BookFormat  string    `json:"book_format,omitempty"`
	InstanceID  string    `json:"instance_id,omitempty"`
	MediaType   string    `json:"media_type"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	StatusKnown bool      `json:"status_known"`
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
	InstanceID       string    `json:"instance_id,omitempty"`
	InstanceName     string    `json:"instance_name,omitempty"`
	RequesterCount   int       `json:"requester_count"`
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

// DecisionOverride lets an admin tweak supported TV/movie options when
// approving. BookFormat remains in the wire shape for compatibility but is
// immutable: a non-empty different value is rejected.
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
	actorID          int64 // optional execution authority; history remains userID-owned
	tmdbID           int
	tvdbID           int
	foreignID        string // Chaptarr foreignBookId (book requests)
	bookFormat       string
	instanceID       string
	bookFormats      map[string]string
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
	if req.MediaType == "book" {
		req.ForeignID = strings.TrimSpace(req.ForeignID)
		req.Title = strings.TrimSpace(req.Title)
		if req.ForeignID == "" {
			return nil, fmt.Errorf("foreign_id is required for book requests")
		}
		if req.BookFormat != "" && !validBookFormat(req.BookFormat) {
			return nil, fmt.Errorf("book_format must be ebook, audiobook, or both")
		}
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
		instanceID: strings.TrimSpace(req.InstanceID),
		mediaType:  req.MediaType,
		title:      req.Title,
	}
	var resolvedBookClient *chaptarr.Client
	if resolved.mediaType == "book" {
		client, instanceID, err := s.resolveChaptarr(userID, resolved.instanceID)
		if err != nil {
			return nil, err
		}
		if client == nil {
			return nil, fmt.Errorf("chaptarr is not configured for you")
		}
		resolvedBookClient = client
		resolved.instanceID = instanceID
		// Keep the live preflight, external mutation, and request-log write in one
		// same-process per-title critical section.
		bookLock := s.bookLock(resolved.instanceID + "\x00" + resolved.foreignID)
		bookLock.Lock()
		defer bookLock.Unlock()
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
	// Books resolve deterministic Chaptarr profile/root settings at add time, so
	// they carry no requester-selectable quality profile here.
	if req.QualityProfileID != 0 && eff.AllowQualityChoice && req.MediaType != "book" {
		resolved.qualityProfileID = req.QualityProfileID
	}

	if eff.RequiresApproval {
		if resolved.mediaType == "book" {
			live, err := s.freshLiveBookFormats(resolvedBookClient, resolved.instanceID, resolved.foreignID)
			if err != nil {
				return nil, err
			}
			if resolved.title == "" && len(live) == 0 {
				return nil, fmt.Errorf("title is required to add a new book")
			}
			missing := make([]string, 0, 2)
			for _, format := range expandBookFormat(resolved.bookFormat) {
				status, covered := live[format]
				if !covered || status == StatusUnavailable || status == StatusDenied {
					missing = append(missing, format)
				}
			}
			if len(missing) == 0 {
				return &CreateResponse{Success: true, Status: collapseBookStatuses(live, ""), Title: resolved.title, BookFormats: live}, nil
			}
			if len(missing) == 1 {
				resolved.bookFormat = missing[0]
			} else {
				resolved.bookFormat = BookFormatBoth
			}
			pendingResp, err := s.createPendingUnlocked(resolved)
			if err != nil {
				return nil, err
			}
			if pendingResp.BookFormats == nil {
				pendingResp.BookFormats = map[string]string{}
			}
			for format, status := range live {
				if status != StatusUnavailable {
					pendingResp.BookFormats[format] = status
				}
			}
			pendingResp.Status = collapseBookStatuses(pendingResp.BookFormats, StatusPending)
			return pendingResp, nil
		}
		return s.createPending(resolved)
	}

	status, title, err := s.addToArr(resolved)
	if err != nil {
		return nil, err
	}
	resolved.title = title
	s.logRequest(resolved, title, status)
	return &CreateResponse{Success: true, Status: status, Title: title, BookFormats: resolved.bookFormats}, nil
}

// createPending records a request awaiting admin approval without touching the
// arr services. The stored options are replayed verbatim on approval.
func (s *Service) createPending(r *resolvedRequest) (*CreateResponse, error) {
	if r.mediaType == "book" {
		lock := s.bookLock(r.instanceID + "\x00" + r.foreignID)
		lock.Lock()
		defer lock.Unlock()
	}
	return s.createPendingUnlocked(r)
}

func (s *Service) createPendingUnlocked(r *resolvedRequest) (*CreateResponse, error) {
	// Insert only when no overlapping pending work already exists, so a
	// double-submit cannot create duplicate queue entries. Books have no TMDB id,
	// so they key on canonical foreignBookId + pinned Chaptarr instance and share
	// one work item across subscribed requesters.
	var res sql.Result
	var err error
	insertedBookFormat := ""
	if r.mediaType == "book" {
		tx, beginErr := s.db.Begin()
		if beginErr != nil {
			return nil, fmt.Errorf("begin pending book request: %w", beginErr)
		}
		defer tx.Rollback()

		coveredBy := map[string]int64{}
		rows, queryErr := tx.Query(
			`SELECT id, COALESCE(book_format, 'both') FROM request_log
			 WHERE foreign_id = ? AND COALESCE(instance_id, '') = COALESCE(?, '')
			   AND media_type = 'book' AND status = ?`,
			r.foreignID, sqlNullStr(r.instanceID), StatusPending,
		)
		if queryErr != nil {
			return nil, fmt.Errorf("check pending book formats: %w", queryErr)
		}
		covered := map[string]bool{}
		for rows.Next() {
			var requestID int64
			var storedFormat string
			if scanErr := rows.Scan(&requestID, &storedFormat); scanErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan pending book format: %w", scanErr)
			}
			for _, format := range expandBookFormat(storedFormat) {
				covered[format] = true
				coveredBy[format] = requestID
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read pending book formats: %w", rowsErr)
		}
		_ = rows.Close()

		missing := make([]string, 0, 2)
		for _, format := range expandBookFormat(r.bookFormat) {
			if !covered[format] {
				missing = append(missing, format)
			}
		}
		pendingFormat := ""
		switch len(missing) {
		case 2:
			pendingFormat = BookFormatBoth
		case 1:
			pendingFormat = missing[0]
		}
		if pendingFormat == "" {
			res = zeroRowsResult{}
		} else {
			insertedBookFormat = pendingFormat
			res, err = tx.Exec(
				`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status)
				 VALUES (?, 0, ?, ?, ?, 'book', ?, ?)`,
				r.userID, r.foreignID, pendingFormat, sqlNullStr(r.instanceID), r.title, StatusPending,
			)
			if err == nil {
				requestID, _ := res.LastInsertId()
				for _, format := range expandBookFormat(pendingFormat) {
					coveredBy[format] = requestID
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("save pending request: %w", err)
		}

		requestedByRow := map[int64][]string{}
		for _, format := range expandBookFormat(r.bookFormat) {
			if requestID := coveredBy[format]; requestID != 0 {
				requestedByRow[requestID] = append(requestedByRow[requestID], format)
			}
		}
		for requestID, formats := range requestedByRow {
			waiterFormat := formats[0]
			if len(formats) > 1 {
				waiterFormat = BookFormatBoth
			}
			if _, err := tx.Exec(
				`INSERT INTO book_request_waiters (request_id, user_id, book_format) VALUES (?, ?, ?)
				 ON CONFLICT(request_id, user_id) DO UPDATE SET book_format = CASE
				   WHEN book_request_waiters.book_format = 'both' OR excluded.book_format = 'both' THEN 'both'
				   WHEN book_request_waiters.book_format <> excluded.book_format THEN 'both'
				   ELSE book_request_waiters.book_format END`,
				requestID, r.userID, waiterFormat,
			); err != nil {
				return nil, fmt.Errorf("subscribe to pending book request: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit pending book request: %w", err)
		}
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
		if r.mediaType == "book" {
			data["foreign_id"] = r.foreignID
			data["book_format"] = insertedBookFormat
			data["instance_id"] = r.instanceID
		}
		s.notifier.NotifyAdmins("request_pending", data)
	}
	formats := map[string]string{}
	if r.mediaType == "book" {
		for _, format := range expandBookFormat(r.bookFormat) {
			formats[format] = StatusPending
		}
	}
	return &CreateResponse{Success: true, Status: StatusPending, Title: r.title, BookFormats: formats}, nil
}

// zeroRowsResult lets duplicate pending actions share the normal notification
// path without issuing a dummy write.
type zeroRowsResult struct{}

func (zeroRowsResult) LastInsertId() (int64, error) { return 0, nil }
func (zeroRowsResult) RowsAffected() (int64, error) { return 0, nil }

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

// addToChaptarr adds a book to the pinned Chaptarr instance. Books have no TMDB
// id, so the request carries a canonical foreignBookId. Existing canonical
// groups reuse their author's live configuration for a missing sibling format;
// brand-new titles require an exact lookup match and unambiguous profiles/root.
func (s *Service) addToChaptarr(r *resolvedRequest) (string, string, error) {
	r.foreignID = strings.TrimSpace(r.foreignID)
	r.title = strings.TrimSpace(r.title)
	if r.foreignID == "" {
		return "", "", fmt.Errorf("foreign_id is required for book requests")
	}
	actorID := r.actorID
	if actorID == 0 {
		actorID = r.userID
	}
	client, instanceID, err := s.resolveChaptarr(actorID, r.instanceID)
	r.instanceID = instanceID
	if err != nil {
		return "", "", err
	}
	if client == nil {
		return "", "", fmt.Errorf("chaptarr is not configured for you")
	}

	// Preflight the live library before lookup/add. The request boundary is the
	// idempotency boundary: a file is already available, a monitored record is
	// already requested, and an unmonitored record is monitored/searched in
	// place rather than duplicated.
	books, libraryErr := client.GetAllBooks()
	if libraryErr != nil {
		return "", "", fmt.Errorf("check existing book state: %w", libraryErr)
	}
	title, existing, unresolved := recordsForForeignID(books, r.foreignID)
	title = strings.TrimSpace(title)
	if unresolved {
		return "", "", ErrBookFormatUnresolved
	}
	if title == "" {
		title = r.title
	}
	r.bookFormats = make(map[string]string)
	missing := make([]string, 0, 2)
	var lastErr error
	for _, mediaType := range chaptarrRequestFormats(r.bookFormat) {
		records := existing[mediaType]
		if len(records) == 0 {
			missing = append(missing, mediaType)
			continue
		}
		available := false
		monitored := false
		ids := make([]int, 0, len(records))
		for _, rec := range records {
			ids = append(ids, rec.ID)
			available = available || rec.Statistics.BookFileCount > 0
			monitored = monitored || rec.Monitored
		}
		switch {
		case available:
			r.bookFormats[mediaType] = StatusAvailable
		case monitored:
			r.bookFormats[mediaType] = StatusRequested
		default:
			if err := client.SetBookMonitored(ids, true); err != nil {
				lastErr = fmt.Errorf("monitor %s: %w", mediaType, err)
				r.bookFormats[mediaType] = StatusUnavailable
				continue
			}
			// Monitoring is the durable request contract; the immediate search is a
			// best-effort accelerator. A failed command must not make the now-
			// monitored record requestable again.
			_ = client.TriggerBookSearch(ids)
			r.bookFormats[mediaType] = StatusRequested
		}
	}
	if len(missing) == 0 {
		return s.finishBookMutation(r, title, lastErr)
	}
	if len(existing) > 0 {
		// A requester can arrive with the canonical library foreignBookId while
		// metadata lookup uses a different provider ID. Add missing sibling
		// formats under the already-tracked author instead of requiring those IDs
		// to match or creating a second title group.
		var seed chaptarr.Book
		authorIDs := map[int]bool{}
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			for _, record := range existing[format] {
				authorID := record.AuthorID
				if authorID == 0 && record.Author != nil {
					authorID = record.Author.ID
				}
				if authorID != 0 {
					authorIDs[authorID] = true
				}
				if seed.ID == 0 {
					seed = record
				}
			}
		}
		if len(authorIDs) > 1 {
			return "", "", fmt.Errorf("existing canonical book records disagree on author")
		}
		authorID := 0
		for collectedID := range authorIDs {
			authorID = collectedID
		}
		if authorID == 0 {
			return "", "", fmt.Errorf("existing book author is unresolved")
		}
		author, err := client.GetAuthor(authorID)
		if err != nil {
			return "", "", fmt.Errorf("load existing book author: %w", err)
		}
		if author.QualityProfileID == 0 || author.MetadataProfileID == 0 || strings.TrimSpace(author.Path) == "" {
			return "", "", fmt.Errorf("existing book author configuration is incomplete")
		}
		match := &chaptarr.LookupResult{
			ForeignBookID:   r.foreignID,
			Title:           title,
			TitleSlug:       seed.TitleSlug,
			AuthorName:      author.AuthorName,
			ForeignAuthorID: author.ForeignAuthorID,
		}
		if match.TitleSlug == "" {
			match.TitleSlug = fallbackTitleSlug(title)
		}
		for _, mediaType := range missing {
			if err := s.addChaptarrBookRecord(client, match, author.QualityProfileID, author.MetadataProfileID, author.Path, title, match.TitleSlug, mediaType); err != nil {
				lastErr = err
				r.bookFormats[mediaType] = StatusUnavailable
				continue
			}
			r.bookFormats[mediaType] = StatusRequested
		}
		return s.finishBookMutation(r, title, lastErr)
	}
	if r.title == "" {
		return "", "", fmt.Errorf("title is required to add a new book")
	}

	results, err := client.LookupBook(r.title)
	if err != nil {
		if len(missing) > 0 {
			return "", "", fmt.Errorf("book lookup failed: %w", err)
		}
		results = nil
	}
	var match *chaptarr.LookupResult
	for i := range results {
		if results[i].ForeignBookID == r.foreignID {
			match = &results[i]
			break
		}
	}
	if match == nil && len(missing) > 0 {
		// The foreignBookId belongs to a book already in the library (owned
		// records carry a library foreignBookId the metadata lookup can't match);
		// complete the missing format from the existing records instead of adding.
		lastErr = fmt.Errorf("book not found for foreign id %s", r.foreignID)
		for _, mediaType := range missing {
			r.bookFormats[mediaType] = StatusUnavailable
		}
	}
	if match == nil {
		return s.finishBookMutation(r, title, lastErr)
	}

	qps, err := client.GetQualityProfiles()
	if err != nil || len(qps) == 0 {
		return "", "", fmt.Errorf("no quality profiles available")
	}
	qualityProfileID, ok := selectBookQualityProfile(qps)
	if !ok {
		return "", "", fmt.Errorf("multiple Chaptarr quality profiles are configured; name exactly one Default or reduce them to one")
	}
	mps, err := client.GetMetadataProfiles()
	if err != nil || len(mps) == 0 {
		return "", "", fmt.Errorf("no metadata profiles available")
	}
	metadataProfileID, ok := selectBookMetadataProfile(mps)
	if !ok {
		return "", "", fmt.Errorf("multiple Chaptarr metadata profiles are configured; name exactly one Default or reduce them to one")
	}
	folders, err := client.GetRootFolders()
	if err != nil || len(folders) == 0 {
		return "", "", fmt.Errorf("no root folders available")
	}

	title = strings.TrimSpace(match.Title)
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
	for _, mediaType := range missing {
		root, ok := selectBookRoot(folders, mediaType)
		if !ok {
			lastErr = fmt.Errorf("no accessible root folder available for %s", mediaType)
			r.bookFormats[mediaType] = StatusUnavailable
			continue
		}
		if err := s.addChaptarrBookRecord(client, match, qualityProfileID, metadataProfileID, root.Path, title, titleSlug, mediaType); err != nil {
			lastErr = err
			r.bookFormats[mediaType] = StatusUnavailable
			continue
		}
		r.bookFormats[mediaType] = StatusRequested
	}
	return s.finishBookMutation(r, title, lastErr)
}

func selectBookQualityProfile(profiles []chaptarr.QualityProfile) (int, bool) {
	if len(profiles) == 1 {
		return profiles[0].ID, profiles[0].ID != 0
	}
	selected := 0
	for _, profile := range profiles {
		if strings.EqualFold(strings.TrimSpace(profile.Name), "default") {
			if selected != 0 || profile.ID == 0 {
				return 0, false
			}
			selected = profile.ID
		}
	}
	return selected, selected != 0
}

func selectBookMetadataProfile(profiles []chaptarr.MetadataProfile) (int, bool) {
	if len(profiles) == 1 {
		return profiles[0].ID, profiles[0].ID != 0
	}
	selected := 0
	for _, profile := range profiles {
		if strings.EqualFold(strings.TrimSpace(profile.Name), "default") {
			if selected != 0 || profile.ID == 0 {
				return 0, false
			}
			selected = profile.ID
		}
	}
	return selected, selected != 0
}

// finishBookMutation reduces per-format outcomes without allowing a partial
// "both" request to masquerade as complete success.
func (s *Service) finishBookMutation(r *resolvedRequest, title string, lastErr error) (string, string, error) {
	succeeded := 0
	for _, status := range r.bookFormats {
		if status != StatusUnavailable {
			succeeded++
		}
	}
	if succeeded == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("no requested format could be completed")
		}
		return "", "", lastErr
	}
	if s.libraryCache != nil && r.instanceID != "" {
		s.libraryCache.Delete("book-library:" + r.instanceID)
		s.libraryCache.Delete("book-live:" + r.instanceID)
	}
	if succeeded != len(r.bookFormats) {
		return StatusPartial, title, nil
	}
	if allBookFormatsAre(r.bookFormats, StatusAvailable) {
		return StatusAvailable, title, nil
	}
	if anyBookFormatIs(r.bookFormats, StatusAvailable) {
		return StatusPartial, title, nil
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
	// monitored and need nothing further. Monitoring is required; only the
	// immediate search command is best-effort after monitoring succeeds.
	if book != nil && book.ID != 0 && !book.Monitored {
		if err := client.SetBookMonitored([]int{book.ID}, true); err != nil {
			return fmt.Errorf("monitor added %s: %w", mediaType, err)
		}
		_ = client.TriggerBookSearch([]int{book.ID})
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
// Readarr foreignBookId (books have no tmdb_id). Status starts from the
// collapsed (latest) request_log state (pending / denied / requested), then
// live ownership is overlaid: a requested format whose file has since landed
// in Chaptarr reads available (request_log is never updated, so without the
// overlay a fulfilled request would read "requested" forever). BookFormats
// breaks it down per format so the dashboard can still offer the other format
// after one is requested. A stored "both" request covers both ebook and
// audiobook.
func (s *Service) GetUserBookStatus(userID int64, foreignID string) (*StatusResponse, error) {
	return s.GetUserBookStatusForInstance(userID, foreignID, "")
}

// GetUserBookStatusForInstance combines per-user approval history with live,
// per-format Chaptarr truth for the selected authorized instance. Live file,
// queue, and monitored state outrank pending/denied/history labels.
func (s *Service) GetUserBookStatusForInstance(userID int64, foreignID, requestedInstanceID string) (*StatusResponse, error) {
	foreignID = strings.TrimSpace(foreignID)
	if foreignID == "" {
		return nil, fmt.Errorf("foreign_id is required")
	}
	client, instanceID, err := s.resolveChaptarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
	query := "SELECT COALESCE(book_format, 'both'), status FROM request_log WHERE (user_id = ? OR status = 'pending') AND foreign_id = ? AND media_type = 'book'"
	args := []interface{}{userID, foreignID}
	if instanceID != "" {
		if requestedInstanceID != "" {
			// An explicit selection must never absorb unscoped legacy history from
			// another instance.
			query += " AND instance_id = ?"
		} else {
			// Omitted IDs are the compatibility path for pre-pinning clients/rows.
			query += " AND (instance_id = ? OR instance_id IS NULL)"
		}
		args = append(args, instanceID)
	} else if requestedInstanceID != "" {
		query += " AND instance_id = ?"
		args = append(args, requestedInstanceID)
	}
	query += " ORDER BY requested_at DESC, id DESC"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query book request status: %w", err)
	}
	defer rows.Close()

	formats := map[string]string{}
	collapsed := ""
	for rows.Next() {
		var format, status string
		if err := rows.Scan(&format, &status); err != nil {
			return nil, fmt.Errorf("scan book request status: %w", err)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read book request status: %w", err)
	}
	if client != nil {
		live, lerr := s.liveBookFormats(client, instanceID, foreignID)
		if lerr != nil {
			if errors.Is(lerr, ErrBookFormatUnresolved) {
				known := false
				return &StatusResponse{Status: StatusUnavailable, StatusKnown: &known}, nil
			}
			return nil, lerr
		}
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			liveStatus, exists := live[format]
			loggedStatus, logged := formats[format]
			if exists && liveStatus != StatusUnavailable {
				formats[format] = liveStatus
				continue
			}
			if !logged {
				continue
			}
			// Approval workflow outcomes remain meaningful while there is no newer
			// live work for that format.
			if loggedStatus != StatusPending && loggedStatus != StatusDenied {
				formats[format] = StatusUnavailable
			}
		}
	}

	if len(formats) == 0 {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}
	resp := &StatusResponse{BookFormats: formats}
	resp.Status = collapseBookStatuses(formats, collapsed)
	return resp, nil
}

const bookLiveProjectionTTL = 15 * time.Second

type bookLiveProjection struct {
	Formats    map[string]map[string]string `json:"formats"`
	Unresolved map[string]bool              `json:"unresolved,omitempty"`
}

// liveBookFormats returns one title's slice of a short-lived instance-wide
// projection. Search grids call book-status once per row, so fetching the full
// library/queue per title would be an accidental N+1 load on Chaptarr.
func (s *Service) liveBookFormats(client *chaptarr.Client, instanceID, foreignID string) (map[string]string, error) {
	cacheKey := "book-live:" + instanceID
	if projection, ok := s.cachedBookProjection(cacheKey); ok {
		return projection.formatsFor(foreignID)
	}
	projectionLock := s.projectionLock(instanceID)
	projectionLock.Lock()
	defer projectionLock.Unlock()
	if projection, ok := s.cachedBookProjection(cacheKey); ok {
		return projection.formatsFor(foreignID)
	}
	projection, err := buildBookLiveProjection(client)
	if err != nil {
		return nil, err
	}
	s.cacheBookProjection(cacheKey, projection)
	return projection.formatsFor(foreignID)
}

func (s *Service) freshLiveBookFormats(client *chaptarr.Client, instanceID, foreignID string) (map[string]string, error) {
	projectionLock := s.projectionLock(instanceID)
	projectionLock.Lock()
	defer projectionLock.Unlock()
	projection, err := buildBookLiveProjection(client)
	if err != nil {
		return nil, err
	}
	s.cacheBookProjection("book-live:"+instanceID, projection)
	return projection.formatsFor(foreignID)
}

func buildBookLiveProjection(client *chaptarr.Client) (*bookLiveProjection, error) {
	books, err := client.GetAllBooks()
	if err != nil {
		return nil, fmt.Errorf("check live book state: %w", err)
	}
	queued := make(map[int]bool)
	if queue, err := client.GetQueueDetailed(1, 100); err == nil {
		for _, item := range queue {
			if item.BookID != 0 && bookQueueItemDownloading(item) {
				queued[item.BookID] = true
			}
		}
	}
	projection := &bookLiveProjection{Formats: make(map[string]map[string]string), Unresolved: make(map[string]bool)}
	foreignIDs := make(map[string]bool)
	for _, book := range books {
		if book.ForeignBookID != "" {
			foreignIDs[book.ForeignBookID] = true
		}
	}
	for id := range foreignIDs {
		_, records, unresolved := recordsForForeignID(books, id)
		if unresolved {
			projection.Unresolved[id] = true
			continue
		}
		live := make(map[string]string)
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			recs := records[format]
			if len(recs) == 0 {
				continue
			}
			status := StatusUnavailable
			for _, rec := range recs {
				switch {
				case rec.Statistics.BookFileCount > 0:
					status = StatusAvailable
				case status != StatusAvailable && queued[rec.ID]:
					status = StatusDownloading
				case status != StatusAvailable && status != StatusDownloading && rec.Monitored:
					status = StatusRequested
				}
			}
			live[format] = status
		}
		projection.Formats[id] = live
	}
	return projection, nil
}

func (s *Service) cachedBookProjection(cacheKey string) (*bookLiveProjection, bool) {
	if s.libraryCache == nil || cacheKey == "book-live:" {
		return nil, false
	}
	data, ok := s.libraryCache.Get(cacheKey)
	if !ok {
		return nil, false
	}
	var projection bookLiveProjection
	if json.Unmarshal(data, &projection) != nil || projection.Formats == nil {
		return nil, false
	}
	return &projection, true
}

func (s *Service) cacheBookProjection(cacheKey string, projection *bookLiveProjection) {
	if s.libraryCache == nil || cacheKey == "book-live:" {
		return
	}
	if data, err := json.Marshal(projection); err == nil {
		s.libraryCache.Set(cacheKey, data, bookLiveProjectionTTL)
	}
}

func (p *bookLiveProjection) formatsFor(foreignID string) (map[string]string, error) {
	if p.Unresolved[foreignID] {
		return nil, ErrBookFormatUnresolved
	}
	return p.Formats[foreignID], nil
}

func bookQueueItemDownloading(item chaptarr.QueueItem) bool {
	trackedStatus := strings.ToLower(strings.TrimSpace(item.TrackedDownloadStatus))
	trackedState := strings.ToLower(strings.TrimSpace(item.TrackedDownloadState))
	status := strings.ToLower(strings.TrimSpace(item.Status))
	problemState := trackedStatus + " " + trackedState + " " + status
	for _, token := range []string{"paused", "unavailable", "problem", "warning", "error", "failed", "blocked", "stalled"} {
		if strings.Contains(problemState, token) {
			return false
		}
	}
	if trackedStatus != "" && trackedStatus != "ok" || strings.TrimSpace(item.ErrorMessage) != "" {
		return false
	}
	for _, message := range item.StatusMessages {
		if strings.TrimSpace(message.Title) != "" || len(message.Messages) > 0 {
			return false
		}
	}
	switch status {
	case "queued", "downloading", "importing":
		return true
	case "completed":
		return trackedState == "importpending" || trackedState == "importing"
	case "":
		return trackedState == "queued" || trackedState == "downloading" || trackedState == "importpending" || trackedState == "importing"
	default:
		return false
	}
}

func collapseBookStatuses(formats map[string]string, _ string) string {
	if allBookFormatsAre(formats, StatusAvailable) {
		return StatusAvailable
	}
	if anyBookFormatIs(formats, StatusAvailable) {
		return StatusPartial
	}
	if anyBookFormatIs(formats, StatusDownloading) {
		return StatusDownloading
	}
	if anyBookFormatIs(formats, StatusRequested) {
		return StatusRequested
	}
	if anyBookFormatIs(formats, StatusPending) {
		return StatusPending
	}
	if anyBookFormatIs(formats, StatusDenied) {
		return StatusDenied
	}
	return StatusUnavailable
}

// allBookFormatsAre reports whether every requested format carries [status];
// false for an empty map (no requested formats means nothing to fulfill).
func allBookFormatsAre(formats map[string]string, status string) bool {
	if len(formats) == 0 {
		return false
	}
	for _, st := range formats {
		if st != status {
			return false
		}
	}
	return true
}

// anyBookFormatIs reports whether at least one requested format carries
// [status].
func anyBookFormatIs(formats map[string]string, status string) bool {
	for _, st := range formats {
		if st == status {
			return true
		}
	}
	return false
}

// expandBookFormat maps a supported stored book_format to the concrete formats
// it covers. Empty legacy values normalize to both; unknown non-empty values
// remain unsupported and expand to nothing.
func expandBookFormat(format string) []string {
	switch normalizeBookFormat(format) {
	case BookFormatEbook:
		return []string{BookFormatEbook}
	case BookFormatAudiobook:
		return []string{BookFormatAudiobook}
	case BookFormatBoth:
		return []string{BookFormatEbook, BookFormatAudiobook}
	default:
		return nil
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

// historyRow carries a request_log row through the live-status overlay: the
// user-facing RequestLog plus the lookup keys the overlay needs but the
// response doesn't expose.
type historyRow struct {
	log        RequestLog
	tvdbID     int
	foreignID  string
	bookFormat string
	instanceID string
}

// GetRequests returns the user's request history with each row's status
// recomputed from the live arr libraries, so the list tracks reality instead
// of the point-in-time snapshot request_log stores (nothing ever updates those
// rows — a "requested" title that Sonarr has long since imported, or that an
// admin deleted directly in the arr, would otherwise read wrong forever).
func (s *Service) GetRequests(userID int64) ([]RequestLog, error) {
	rows, err := s.db.Query(
		`SELECT tmdb_id, tvdb_id, foreign_id, book_format, instance_id, media_type, title, status, deny_reason, requested_at
		 FROM (
		   SELECT r.tmdb_id,
		          COALESCE(r.tvdb_id, 0) AS tvdb_id,
		          COALESCE(r.foreign_id, '') AS foreign_id,
		          COALESCE(r.book_format, '') AS book_format,
		          COALESCE(r.instance_id, '') AS instance_id,
		          r.media_type, r.title, r.status,
		          COALESCE(r.deny_reason, '') AS deny_reason,
		          r.requested_at
		   FROM request_log r
		   WHERE r.user_id = ?
		   UNION ALL
		   SELECT r.tmdb_id,
		          COALESCE(r.tvdb_id, 0),
		          COALESCE(r.foreign_id, ''),
		          bw.book_format,
		          COALESCE(r.instance_id, ''),
		          r.media_type, r.title, r.status,
		          COALESCE(r.deny_reason, ''),
		          r.requested_at
		   FROM request_log r
		   JOIN book_request_waiters bw ON bw.request_id = r.id
		   WHERE bw.user_id = ?
		     AND r.user_id <> ?
		     AND r.media_type = 'book'
		     AND r.status = ?
		 )
		 ORDER BY requested_at DESC`,
		userID, userID, userID, StatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("query requests: %w", err)
	}
	defer rows.Close()

	var history []historyRow
	for rows.Next() {
		var r historyRow
		if err := rows.Scan(&r.log.TmdbID, &r.tvdbID, &r.foreignID, &r.bookFormat, &r.instanceID, &r.log.MediaType, &r.log.Title, &r.log.Status, &r.log.DenyReason, &r.log.RequestedAt); err != nil {
			return nil, fmt.Errorf("scan request: %w", err)
		}
		r.log.StatusKnown = true
		r.log.ForeignID = r.foreignID
		r.log.BookFormat = normalizeBookFormat(r.bookFormat)
		r.log.InstanceID = r.instanceID
		history = append(history, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	s.overlayLiveStatuses(userID, history)

	requests := make([]RequestLog, len(history))
	for i := range history {
		requests[i] = history[i].log
	}
	return requests, nil
}

// overlayLiveStatuses recomputes each history row's status from the live arr
// libraries (via the short-lived per-instance digests, fetched at most once
// per media kind). Precedence mirrors GetUserStatus: pending always shows
// as-is (the title isn't in the arr yet); denied is kept unless the title has
// since landed anyway. Movie/TV rows match by reliable ids, so a title gone
// from the library reads unavailable. Pinned book rows use the same
// per-instance projection as detail views, including available, downloading,
// requested, and unavailable. Legacy unscoped rows and unreachable arr sources
// keep their stored state; explicitly unresolved format truth is marked unknown.
func (s *Service) overlayLiveStatuses(userID int64, history []historyRow) {
	var (
		movies               map[int]movieAvailability
		moviesOK, moviesDone bool
		series               map[int]seriesAvailability
		seriesOK, seriesDone bool
		bookClients          = map[string]*chaptarr.Client{}
		bookInstanceDone     = map[string]bool{}
		bookInstanceOK       = map[string]bool{}
	)

	for i := range history {
		row := &history[i]
		if row.log.Status == StatusPending {
			continue
		}

		live := ""
		switch row.log.MediaType {
		case "movie":
			if !moviesDone {
				movies, moviesOK = s.movieAvailabilityDigest(userID)
				moviesDone = true
			}
			if !moviesOK {
				continue
			}
			a, found := movies[row.log.TmdbID]
			live = movieAvailabilityStatus(a, found)

		case "tv":
			if !seriesDone {
				series, seriesOK = s.seriesAvailabilityDigest(userID)
				seriesDone = true
			}
			if !seriesOK {
				continue
			}
			tvdbID := row.tvdbID
			if tvdbID == 0 {
				// Older rows predate the tvdb_id column; the id mapping cache
				// usually still knows the title from request time.
				_ = s.db.QueryRow("SELECT tvdb_id FROM tmdb_tvdb_cache WHERE tmdb_id = ?", row.log.TmdbID).Scan(&tvdbID)
				if tvdbID == 0 {
					continue
				}
			}
			a, found := series[tvdbID]
			live = seriesAvailabilityStatus(a, found)

		case "book":
			// Legacy unscoped rows cannot be safely attributed after a user's
			// default changes, so their point-in-time state remains untouched.
			if row.foreignID == "" || row.instanceID == "" {
				continue
			}
			instanceID := row.instanceID
			if !bookInstanceDone[instanceID] {
				client, resolvedID, err := s.resolveChaptarr(userID, instanceID)
				if err == nil && client != nil && resolvedID == instanceID {
					bookClients[instanceID] = client
					bookInstanceOK[instanceID] = true
				}
				bookInstanceDone[instanceID] = true
			}
			if !bookInstanceOK[instanceID] {
				continue
			}
			formats, err := s.liveBookFormats(bookClients[instanceID], instanceID, row.foreignID)
			if err != nil {
				if errors.Is(err, ErrBookFormatUnresolved) {
					row.log.Status = StatusUnavailable
					row.log.StatusKnown = false
				}
				// An unresolved format is explicit unknown truth; transient failures
				// retain the stored state until live truth is available again.
				continue
			}
			selected := map[string]string{}
			for _, format := range expandBookFormat(row.bookFormat) {
				selected[format] = StatusUnavailable
				if status, ok := formats[format]; ok {
					selected[format] = status
				}
			}
			live = collapseBookStatuses(selected, StatusUnavailable)

		default:
			continue
		}

		if row.log.Status == StatusDenied && live == StatusUnavailable {
			continue
		}
		row.log.Status = live
	}
}

// bookOwnershipIndex returns the user's book library digest indexed by
// foreignBookId. ok is false when the digest couldn't be fetched (no access
// resolves to an empty digest, which is a valid — always-missing — index).
func (s *Service) bookOwnershipIndex(userID int64) (map[string]LibraryTitle, bool) {
	return s.bookOwnershipIndexForInstance(userID, "")
}

func (s *Service) bookOwnershipIndexForInstance(userID int64, instanceID string) (map[string]LibraryTitle, bool) {
	digest, err := s.GetBookLibraryDigestForInstance(userID, instanceID)
	if err != nil || digest == nil {
		return nil, false
	}
	index := make(map[string]LibraryTitle, len(digest.Titles))
	for _, t := range digest.Titles {
		if t.ForeignBookID != "" {
			index[t.ForeignBookID] = t
		}
	}
	return index, true
}

// bookAvailabilityStatus reports how much of a stored book request the library
// now fulfills: available when every requested format has a file, partial when
// some do, "" when none do (callers treat "" as no evidence, not absence).
func bookAvailabilityStatus(t LibraryTitle, bookFormat string) string {
	formats := expandBookFormat(bookFormat)
	if len(formats) == 0 {
		return ""
	}
	downloaded := 0
	for _, f := range formats {
		if bookFormatDownloaded(t, f) {
			downloaded++
		}
	}
	switch {
	case downloaded == len(formats):
		return StatusAvailable
	case downloaded > 0:
		return StatusPartial
	default:
		return ""
	}
}

// bookFormatDownloaded reports whether a concrete format ("ebook"/"audiobook")
// of a library title has a file on disk.
func bookFormatDownloaded(t LibraryTitle, format string) bool {
	switch format {
	case BookFormatEbook:
		return t.Ebook.Downloaded
	case BookFormatAudiobook:
		return t.Audiobook.Downloaded
	}
	return false
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

type bookRequestSubscriber struct {
	UserID     int64
	BookFormat string
}

func (s *Service) bookRequestAudience(requestID, ownerID int64, ownerFormat string) ([]bookRequestSubscriber, error) {
	audience := map[int64]string{ownerID: ownerFormat}
	rows, err := s.db.Query("SELECT user_id, COALESCE(book_format, 'both') FROM book_request_waiters WHERE request_id = ?", requestID)
	if err != nil {
		return nil, fmt.Errorf("query book request subscribers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var userID int64
		var bookFormat string
		if err := rows.Scan(&userID, &bookFormat); err != nil {
			return nil, fmt.Errorf("scan book request subscriber: %w", err)
		}
		bookFormat = normalizeBookFormat(bookFormat)
		if !validBookFormat(bookFormat) {
			return nil, fmt.Errorf("book request subscriber has unsupported book_format %q", bookFormat)
		}
		if existing, ok := audience[userID]; ok {
			audience[userID] = mergeBookFormats(existing, bookFormat)
		} else {
			audience[userID] = bookFormat
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read book request subscribers: %w", err)
	}
	subscribers := make([]bookRequestSubscriber, 0, len(audience))
	for userID, bookFormat := range audience {
		subscribers = append(subscribers, bookRequestSubscriber{UserID: userID, BookFormat: bookFormat})
	}
	sort.Slice(subscribers, func(i, j int) bool { return subscribers[i].UserID < subscribers[j].UserID })
	return subscribers, nil
}

func mergeBookFormats(a, b string) string {
	if a == b {
		return a
	}
	return BookFormatBoth
}

func bookFormatIncludes(bookFormat, concrete string) bool {
	for _, format := range expandBookFormat(bookFormat) {
		if format == concrete {
			return true
		}
	}
	return false
}

func concreteBookFormat(formats map[string]string) string {
	if len(formats) > 1 {
		return BookFormatBoth
	}
	for format := range formats {
		return format
	}
	return ""
}

func (s *Service) ListPending() ([]PendingRequest, error) {
	rows, err := s.db.Query(
		`SELECT r.id, r.user_id, COALESCE(u.username, ''), r.tmdb_id, COALESCE(r.tvdb_id, 0), r.media_type, r.title, COALESCE(r.book_format, ''), COALESCE(r.instance_id, ''),
		        CASE WHEN r.media_type = 'book' THEN COALESCE(si.name, '') ELSE '' END,
		        CASE WHEN r.media_type = 'book' THEN 1 + (SELECT COUNT(*) FROM book_request_waiters bw WHERE bw.request_id = r.id AND bw.user_id <> r.user_id) ELSE 1 END,
		        COALESCE(r.season_scope, ''), COALESCE(r.quality_profile_id, 0), r.requested_at
		 FROM request_log r
		 LEFT JOIN users u ON u.id = r.user_id
		 LEFT JOIN service_instances si ON si.id = r.instance_id
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
		if err := rows.Scan(&p.ID, &p.UserID, &p.Username, &p.TmdbID, &p.TvdbID, &p.MediaType, &p.Title, &p.BookFormat, &p.InstanceID, &p.InstanceName, &p.RequesterCount, &p.SeasonScope, &p.QualityProfileID, &p.RequestedAt); err != nil {
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
		"SELECT user_id, tmdb_id, COALESCE(tvdb_id, 0), COALESCE(foreign_id, ''), COALESCE(book_format, ''), COALESCE(instance_id, ''), media_type, title, status, COALESCE(season_scope, ''), COALESCE(quality_profile_id, 0) FROM request_log WHERE id = ?",
		requestID,
	).Scan(&r.userID, &r.tmdbID, &r.tvdbID, &r.foreignID, &r.bookFormat, &r.instanceID, &r.mediaType, &r.title, &status, &r.seasonScope, &r.qualityProfileID)
	if err == sql.ErrNoRows {
		return nil, "", fmt.Errorf("request not found")
	}
	if err != nil {
		return nil, "", fmt.Errorf("load request: %w", err)
	}
	r.bookFormat = normalizeBookFormat(r.bookFormat)
	if r.mediaType == "book" && !validBookFormat(r.bookFormat) {
		return nil, "", fmt.Errorf("request has unsupported book_format %q", r.bookFormat)
	}
	// An explicit season list was stored as JSON in season_scope; decode it so
	// approval replays the explicit season selection the requester chose.
	r.seasonNumbers = decodeSeasonNumbers(r.seasonScope)
	return &r, status, nil
}

// ApproveRequest fulfills a pending request (optionally with admin overrides)
// and marks the row approved. The arr add reuses the normal add path.
func (s *Service) ApproveRequest(adminID, requestID int64, override *DecisionOverride) (*CreateResponse, error) {
	decisionLock := s.decisionLock(requestID)
	decisionLock.Lock()
	defer decisionLock.Unlock()

	r, status, err := s.loadRequest(requestID)
	if err != nil {
		return nil, err
	}
	if status != StatusPending {
		return nil, fmt.Errorf("request is not pending")
	}
	audience := []bookRequestSubscriber{{UserID: r.userID}}
	if r.mediaType == "book" {
		if strings.TrimSpace(r.instanceID) == "" {
			return nil, fmt.Errorf("pending book request has no pinned Chaptarr instance")
		}
		audience, err = s.bookRequestAudience(requestID, r.userID, r.bookFormat)
		if err != nil {
			return nil, err
		}
		bookLock := s.bookLock(r.instanceID + "\x00" + r.foreignID)
		bookLock.Lock()
		defer bookLock.Unlock()
		// The request's instance was authorized and pinned at submission. Execute
		// approval under the approving admin so a later requester-grant change
		// cannot reroute or strand it; history remains owned by r.userID.
		r.actorID = adminID
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
		if r.mediaType == "book" && override.BookFormat != "" && override.BookFormat != r.bookFormat {
			return nil, fmt.Errorf("book format cannot be changed during approval")
		}
	}

	newStatus, title, err := s.addToArr(r)
	if err != nil {
		// Leave the row pending so the admin can retry after fixing config.
		return nil, err
	}

	primaryFormat, primaryStatus := r.bookFormat, newStatus
	if r.mediaType == "book" && len(r.bookFormats) > 0 {
		primaryFormat, primaryStatus = "", ""
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			if st, ok := r.bookFormats[format]; ok && st != StatusUnavailable {
				primaryFormat, primaryStatus = format, st
				break
			}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin request approval: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		"UPDATE request_log SET status = ?, title = ?, tvdb_id = ?, book_format = ?, instance_id = ?, season_scope = ?, quality_profile_id = ?, approved_by = ?, decided_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?",
		primaryStatus, title, sqlNullInt(r.tvdbID), sqlNullStr(primaryFormat), sqlNullStr(r.instanceID), sqlNullStr(r.seasonScope), sqlNullInt(r.qualityProfileID), adminID, requestID, StatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("update request: %w", err)
	}
	// Lost a race with a concurrent decision: skip the duplicate notification.
	if n, _ := res.RowsAffected(); n == 0 {
		return &CreateResponse{Success: true, Status: newStatus, Title: title, BookFormats: r.bookFormats}, nil
	}
	if r.mediaType == "book" && len(r.bookFormats) > 1 {
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			formatStatus, ok := r.bookFormats[format]
			if !ok || format == primaryFormat || formatStatus == StatusUnavailable {
				continue
			}
			_, err = tx.Exec(
				`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, approved_by, decided_at)
				 VALUES (?, 0, ?, ?, ?, 'book', ?, ?, ?, CURRENT_TIMESTAMP)`,
				r.userID, r.foreignID, format, sqlNullStr(r.instanceID), title, formatStatus, adminID,
			)
			if err != nil {
				return nil, fmt.Errorf("store approved book format: %w", err)
			}
		}
	}
	if r.mediaType == "book" {
		// A shared pending row is one work item, not the other requesters'
		// personal history. Materialize each successful concrete format for every
		// non-owner subscriber as part of the same decision transaction. Failed
		// coverage is represented by the replacement pending row below.
		for _, subscriber := range audience {
			if subscriber.UserID == r.userID {
				continue
			}
			for _, format := range expandBookFormat(subscriber.BookFormat) {
				formatStatus, ok := r.bookFormats[format]
				if !ok || formatStatus == StatusUnavailable {
					continue
				}
				if _, insertErr := tx.Exec(
					`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, approved_by, decided_at)
					 VALUES (?, 0, ?, ?, ?, 'book', ?, ?, ?, CURRENT_TIMESTAMP)`,
					subscriber.UserID, r.foreignID, format, sqlNullStr(r.instanceID), title, formatStatus, adminID,
				); insertErr != nil {
					return nil, fmt.Errorf("store subscriber book format: %w", insertErr)
				}
			}
		}
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			if r.bookFormats[format] != StatusUnavailable {
				continue
			}
			failedRes, insertErr := tx.Exec(
				`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status)
				 VALUES (?, 0, ?, ?, ?, 'book', ?, ?)`,
				r.userID, r.foreignID, format, r.instanceID, title, StatusPending,
			)
			if insertErr != nil {
				return nil, fmt.Errorf("retain failed book format: %w", insertErr)
			}
			failedRequestID, insertErr := failedRes.LastInsertId()
			if insertErr != nil {
				return nil, fmt.Errorf("read failed book request id: %w", insertErr)
			}
			for _, subscriber := range audience {
				if !bookFormatIncludes(subscriber.BookFormat, format) {
					continue
				}
				if _, insertErr := tx.Exec(
					"INSERT INTO book_request_waiters (request_id, user_id, book_format) VALUES (?, ?, ?)",
					failedRequestID, subscriber.UserID, format,
				); insertErr != nil {
					return nil, fmt.Errorf("retain failed book subscriber: %w", insertErr)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit request approval: %w", err)
	}

	if s.notifier != nil && r.mediaType != "book" {
		data := map[string]interface{}{
			"decision":   "approved",
			"tmdb_id":    r.tmdbID,
			"media_type": r.mediaType,
			"title":      title,
			"status":     newStatus,
		}
		// Books have no TMDB id (tmdb_id is 0); the Chaptarr foreignBookId is
		// the identity a client can deep-link on. Movie/TV rows carry no
		// foreign_id, so the field is omitted and their payloads are unchanged.
		if r.foreignID != "" {
			data["foreign_id"] = r.foreignID
			data["book_format"] = primaryFormat
			data["book_formats"] = r.bookFormats
			data["instance_id"] = r.instanceID
		}
		for _, subscriber := range audience {
			s.notifier.NotifyUser(subscriber.UserID, "request_decision", data)
		}
	}
	if s.notifier != nil && r.mediaType == "book" {
		for _, subscriber := range audience {
			succeeded := map[string]string{}
			for _, format := range expandBookFormat(subscriber.BookFormat) {
				if formatStatus, ok := r.bookFormats[format]; ok && formatStatus != StatusUnavailable {
					succeeded[format] = formatStatus
				}
			}
			// A subscriber whose entire requested slice failed stays subscribed to
			// the replacement pending row and must not be told it was approved.
			if len(succeeded) == 0 {
				continue
			}
			data := map[string]interface{}{
				"decision":     "approved",
				"tmdb_id":      r.tmdbID,
				"media_type":   r.mediaType,
				"title":        title,
				"status":       collapseBookStatuses(succeeded, StatusRequested),
				"foreign_id":   r.foreignID,
				"book_format":  concreteBookFormat(succeeded),
				"book_formats": succeeded,
				"instance_id":  r.instanceID,
			}
			s.notifier.NotifyUser(subscriber.UserID, "request_decision", data)
		}
	}
	return &CreateResponse{Success: true, Status: newStatus, Title: title, BookFormats: r.bookFormats}, nil
}

// DenyRequest marks a pending request denied and notifies the requester.
func (s *Service) DenyRequest(adminID, requestID int64, reason string) error {
	decisionLock := s.decisionLock(requestID)
	decisionLock.Lock()
	defer decisionLock.Unlock()

	r, status, err := s.loadRequest(requestID)
	if err != nil {
		return err
	}
	if status != StatusPending {
		return fmt.Errorf("request is not pending")
	}
	audience := []bookRequestSubscriber{{UserID: r.userID}}
	if r.mediaType == "book" {
		audience, err = s.bookRequestAudience(requestID, r.userID, r.bookFormat)
		if err != nil {
			return err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin request denial: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		"UPDATE request_log SET status = ?, deny_reason = ?, approved_by = ?, decided_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?",
		StatusDenied, sqlNullStr(reason), adminID, requestID, StatusPending,
	)
	if err != nil {
		return fmt.Errorf("update request: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // already decided by a concurrent action
	}
	if r.mediaType == "book" {
		for _, subscriber := range audience {
			if subscriber.UserID == r.userID {
				continue
			}
			if _, err := tx.Exec(
				`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, deny_reason, approved_by, decided_at)
				 VALUES (?, 0, ?, ?, ?, 'book', ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
				subscriber.UserID, r.foreignID, subscriber.BookFormat, sqlNullStr(r.instanceID), r.title, StatusDenied, sqlNullStr(reason), adminID,
			); err != nil {
				return fmt.Errorf("store waiter denial: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit request denial: %w", err)
	}
	if s.notifier != nil && r.mediaType != "book" {
		data := map[string]interface{}{
			"decision":   "denied",
			"tmdb_id":    r.tmdbID,
			"media_type": r.mediaType,
			"title":      r.title,
			"reason":     reason,
			"status":     StatusDenied,
		}
		// Same as the approval event: book rows carry their foreignBookId for
		// deep-linking; movie/TV payloads are unchanged.
		if r.foreignID != "" {
			data["foreign_id"] = r.foreignID
			data["book_format"] = r.bookFormat
			data["instance_id"] = r.instanceID
		}
		for _, subscriber := range audience {
			s.notifier.NotifyUser(subscriber.UserID, "request_decision", data)
		}
	}
	if s.notifier != nil && r.mediaType == "book" {
		for _, subscriber := range audience {
			data := map[string]interface{}{
				"decision":    "denied",
				"tmdb_id":     r.tmdbID,
				"media_type":  r.mediaType,
				"title":       r.title,
				"reason":      reason,
				"status":      StatusDenied,
				"foreign_id":  r.foreignID,
				"book_format": subscriber.BookFormat,
				"instance_id": r.instanceID,
			}
			s.notifier.NotifyUser(subscriber.UserID, "request_decision", data)
		}
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
		CanChooseQuality:   eff.AllowQualityChoice && mediaType != "book",
		DefaultSeasonScope: eff.SeasonScope,
		QualityProfiles:    []QualityProfile{},
	}
	if eff.AllowQualityChoice && mediaType != "book" {
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
		"INSERT INTO request_log (user_id, tmdb_id, tvdb_id, foreign_id, book_format, instance_id, media_type, title, status, season_scope, quality_profile_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.userID, r.tmdbID, sqlNullInt(r.tvdbID), sqlNullStr(r.foreignID), sqlNullStr(r.bookFormat), sqlNullStr(r.instanceID), r.mediaType, title, status, sqlNullStr(r.seasonScope), sqlNullInt(r.qualityProfileID),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// logRequest records a fulfilled request without failing the caller (the arr
// add already succeeded; a history-row failure should not surface as an error).
func (s *Service) logRequest(r *resolvedRequest, title, status string) {
	if r.mediaType == "book" && len(r.bookFormats) > 0 {
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			formatStatus, ok := r.bookFormats[format]
			if !ok || formatStatus == StatusUnavailable {
				continue
			}
			concrete := *r
			concrete.bookFormat = format
			concrete.bookFormats = nil
			_, _ = s.insertRequest(&concrete, title, formatStatus)
		}
		return
	}
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
	case "":
		return BookFormatBoth
	default:
		return format
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
	case BookFormatBoth:
		return []string{"ebook", "audiobook"}
	default:
		return nil
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
