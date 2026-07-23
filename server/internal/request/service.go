package request

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	ErrBookOutcomePending        = errors.New("book request outcome is still being reconciled")
	ErrBookConfigurationInvalid  = errors.New("Chaptarr book configuration is incomplete")
	ErrBookSelectionInvalid      = errors.New("selected book publication is invalid")
	ErrBookMatchNotFound         = errors.New("selected catalog book could not be resolved")
	ErrBookCatalogPending        = errors.New("Chaptarr is still preparing this title")
	ErrBookEditionUnavailable    = errors.New("Chaptarr has no usable edition for the requested format")
	ErrBookMutationUnverified    = errors.New("Chaptarr did not retain the requested book settings")
	ErrBookMutationRejected      = errors.New("Chaptarr rejected the requested book change")
	ErrBookSearchUnconfirmed     = errors.New("Chaptarr did not confirm the book search")
	ErrBookSearchRejected        = errors.New("Chaptarr rejected the book search")
	ErrBookMultiWorkUnsupported  = errors.New("select one book instead of a bundle or multi-book set")
	errBookTitleRequired         = errors.New("title is required to add a new book")
)

var numberedMultiBookTitle = regexp.MustCompile(`(?i)\bbooks?\s+\d+\s*(?:-|–|—|&|\+|,|to|through)\s*\d+\b`)

const (
	defaultBookSetupTimeout    = 15 * time.Second
	defaultBookMutationTimeout = 90 * time.Second
	defaultBookSettleInterval  = 500 * time.Millisecond
	defaultBookSearchAckTTL    = 2 * time.Minute
	defaultBookSeedOutcomeTTL  = 30 * time.Minute
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
	libraryCache            *cache.Cache
	decisionLocks           [64]sync.Mutex
	bookLocks               [64]sync.Mutex
	bookAuthorLocks         [64]sync.Mutex
	bookAuthorMutationLocks [64]sync.Mutex
	projectionLocks         [32]sync.Mutex
	bookSearchAckMu         sync.Mutex
	bookSearchAcks          map[string]time.Time
	bookSearchOutcomeMu     sync.Mutex
	bookUncertainSearches   map[string]time.Time
	bookSeedOutcomeMu       sync.Mutex
	bookUncertainSeeds      map[string]time.Time
	bookUncertainSeedTitles map[string]string
	bookJobWake             chan struct{}
	bookWorkerGeneration    string

	// These durations are fields rather than package variables so focused tests
	// can exercise settling and expiry without racing unrelated parallel tests.
	bookMutationTimeout time.Duration
	bookSettleInterval  time.Duration
	bookSearchAckTTL    time.Duration
}

func NewService(db *sql.DB, registry *instance.Registry, bridge *tmdb.Bridge, notifier Notifier) *Service {
	return &Service{
		db:                      db,
		registry:                registry,
		bridge:                  bridge,
		notifier:                notifier,
		libraryCache:            cache.New(),
		bookSearchAcks:          make(map[string]time.Time),
		bookUncertainSearches:   make(map[string]time.Time),
		bookUncertainSeeds:      make(map[string]time.Time),
		bookUncertainSeedTitles: make(map[string]string),
		bookJobWake:             make(chan struct{}, 1),
		bookMutationTimeout:     defaultBookMutationTimeout,
		bookSettleInterval:      defaultBookSettleInterval,
		bookSearchAckTTL:        defaultBookSearchAckTTL,
	}
}

func (s *Service) bookLock(key string) *sync.Mutex {
	return &s.bookLocks[stripeHash(key)%uint32(len(s.bookLocks))]
}

func (s *Service) newBookMutationContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := s.bookMutationTimeout
	if timeout <= 0 {
		timeout = defaultBookMutationTimeout
	}
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, timeout)
}

func newBookSetupContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, defaultBookSetupTimeout)
}

func (s *Service) bookAuthorLock(key string) *sync.Mutex {
	return &s.bookAuthorLocks[stripeHash(key)%uint32(len(s.bookAuthorLocks))]
}

func (s *Service) lockBookAuthorSeed(instanceID, foreignAuthorID, authorName string) func() {
	indices := make(map[int]bool, 2)
	if foreignAuthorID = strings.TrimSpace(foreignAuthorID); foreignAuthorID != "" {
		index := int(stripeHash(instanceID+"\x00provider\x00"+foreignAuthorID) % uint32(len(s.bookAuthorLocks)))
		indices[index] = true
	}
	if authorName = normalizeBookIdentity(authorName); authorName != "" {
		index := int(stripeHash(instanceID+"\x00name\x00"+authorName) % uint32(len(s.bookAuthorLocks)))
		indices[index] = true
	}
	if len(indices) == 0 {
		indices[int(stripeHash(instanceID+"\x00unknown-author")%uint32(len(s.bookAuthorLocks)))] = true
	}
	ordered := make([]int, 0, len(indices))
	for index := range indices {
		ordered = append(ordered, index)
	}
	sort.Ints(ordered)
	for _, index := range ordered {
		s.bookAuthorLocks[index].Lock()
	}
	return func() {
		for index := len(ordered) - 1; index >= 0; index-- {
			s.bookAuthorLocks[ordered[index]].Unlock()
		}
	}
}

func (s *Service) bookAuthorMutationLock(key string) *sync.Mutex {
	return &s.bookAuthorMutationLocks[stripeHash(key)%uint32(len(s.bookAuthorMutationLocks))]
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
	// BookSelection carries only stable external author/publication evidence
	// from the requester-facing match chooser. Chaptarr-local numeric IDs are
	// intentionally excluded because they can change while the catalog settles.
	BookSelection *BookSelection `json:"book_selection,omitempty"`
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

type BookPublicationSelection struct {
	ForeignEditionID string `json:"foreign_edition_id,omitempty"`
	ISBN13           string `json:"isbn13,omitempty"`
	ASIN             string `json:"asin,omitempty"`
	EditionTitle     string `json:"edition_title,omitempty"`
	Publisher        string `json:"publisher,omitempty"`
	Language         string `json:"language,omitempty"`
	Year             int    `json:"year,omitempty"`
	PageCount        int    `json:"page_count,omitempty"`
}

type BookSelection struct {
	ForeignAuthorID string                    `json:"foreign_author_id,omitempty"`
	AuthorName      string                    `json:"author_name,omitempty"`
	Ebook           *BookPublicationSelection `json:"ebook,omitempty"`
	Audiobook       *BookPublicationSelection `json:"audiobook,omitempty"`
}

func (s *BookSelection) publication(format string) *BookPublicationSelection {
	if s == nil {
		return nil
	}
	if format == BookFormatAudiobook {
		return s.Audiobook
	}
	return s.Ebook
}

type CreateResponse struct {
	Success     bool              `json:"success"`
	Status      string            `json:"status"`
	Title       string            `json:"title"`
	BookFormats map[string]string `json:"book_formats,omitempty"`
}

type StatusResponse struct {
	Status        string  `json:"status"`
	Progress      float64 `json:"progress"`
	StatusKnown   *bool   `json:"status_known,omitempty"`
	UnknownReason string  `json:"unknown_reason,omitempty"`
	FailureCode   string  `json:"failure_code,omitempty"`
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
	ID               int64          `json:"id"`
	UserID           int64          `json:"user_id"`
	Username         string         `json:"username"`
	TmdbID           int            `json:"tmdb_id"`
	TvdbID           int            `json:"tvdb_id"`
	MediaType        string         `json:"media_type"`
	Title            string         `json:"title"`
	BookFormat       string         `json:"book_format"`
	BookSelection    *BookSelection `json:"book_selection,omitempty"`
	InstanceID       string         `json:"instance_id,omitempty"`
	InstanceName     string         `json:"instance_name,omitempty"`
	RequesterCount   int            `json:"requester_count"`
	SeasonScope      string         `json:"season_scope"`
	QualityProfileID int            `json:"quality_profile_id"`
	RequestedAt      time.Time      `json:"requested_at"`
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
	bookSelection    *BookSelection
	instanceID       string
	bookFormats      map[string]string
	mediaType        string
	title            string
	seasonScope      string
	qualityProfileID int
	bookJobID        int64
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
		if chaptarrTitleIsMultiWork(req.Title) {
			return nil, ErrBookMultiWorkUnsupported
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
	if resolved.mediaType == "book" {
		selection, selectionErr := normalizeBookSelection(req.BookSelection, resolved.bookFormat)
		if selectionErr != nil {
			return nil, selectionErr
		}
		resolved.bookSelection = selection
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
		resolved.instanceID = instanceID
		// Pin the instance URL/credentials for the full request. Re-resolve after
		// taking the shared configuration lock so an admin update that won the
		// race cannot leave this request writing through a stale client and then
		// recording the result against the repointed instance ID.
		unlockConfig := s.registry.LockInstanceConfigRead(resolved.instanceID)
		defer unlockConfig()
		client, lockedInstanceID, err := s.resolveChaptarr(userID, resolved.instanceID)
		if err != nil {
			return nil, err
		}
		if client == nil || lockedInstanceID != resolved.instanceID {
			return nil, ErrChaptarrInstanceInvalid
		}
		resolvedBookClient = client
		// Keep the live preflight, external mutation, and request-log write in one
		// same-process per-title critical section.
		bookLock := s.bookLock(resolved.instanceID + "\x00" + resolved.foreignID)
		bookLock.Lock()
		defer bookLock.Unlock()
		if eff.RequiresApproval {
			ctx, cancel := newBookSetupContext(context.Background())
			books, readErr := client.GetAllBooksContext(ctx)
			if readErr != nil {
				cancel()
				return nil, fmt.Errorf("check existing book identity: %w", readErr)
			}
			canonicalTitle, _, identityErr := s.selectChaptarrLibraryWorkForRequest(
				ctx, client, books, resolved.foreignID, resolved.title, resolved.bookSelection,
			)
			cancel()
			if identityErr != nil {
				return nil, identityErr
			}
			if resolved.title == "" {
				resolved.title = canonicalTitle
			}
		}
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

	if resolved.mediaType == "book" {
		job, client, alreadyActive, err := s.prepareDirectBookJob(resolved)
		if err != nil {
			return nil, err
		}
		if alreadyActive {
			return nil, ErrBookOutcomePending
		}
		resolved.bookJobID = job.ID
		applyBookJobCheckpoints(resolved, job)
		status, title, err := s.addToChaptarrWithClient(resolved, client, resolved.instanceID)
		if err != nil {
			if retained := s.deferDirectBookJob(job.ID, err); !retained {
				return nil, err
			}
			return nil, ErrBookOutcomePending
		}
		resolved.title = title
		if err := s.completeDirectBookJob(job.ID, resolved, title); err != nil {
			s.deferDirectBookJob(job.ID, err)
			return nil, ErrBookOutcomePending
		}
		return &CreateResponse{Success: true, Status: status, Title: title, BookFormats: resolved.bookFormats}, nil
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
			`SELECT id, COALESCE(book_format, 'both'), COALESCE(book_selection_json, '') FROM request_log
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
			var storedFormat, storedSelectionJSON string
			if scanErr := rows.Scan(&requestID, &storedFormat, &storedSelectionJSON); scanErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan pending book format: %w", scanErr)
			}
			storedFormat = normalizeBookFormat(storedFormat)
			storedSelection, selectionErr := requireDecodedBookSelection(storedSelectionJSON, storedFormat)
			if selectionErr != nil {
				_ = rows.Close()
				return nil, selectionErr
			}
			for _, format := range expandBookFormat(storedFormat) {
				if !bookSelectionsEquivalent(storedSelection, r.bookSelection, format) {
					continue
				}
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
			selectionJSON, encodeErr := encodeBookSelection(r.bookSelection, pendingFormat)
			if encodeErr != nil {
				return nil, encodeErr
			}
			res, err = tx.Exec(
				`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, book_selection_json, media_type, title, status)
				 VALUES (?, 0, ?, ?, ?, ?, 'book', ?, ?)`,
				r.userID, r.foreignID, pendingFormat, sqlNullStr(r.instanceID), sqlNullStr(selectionJSON), r.title, StatusPending,
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
// groups reuse their author's live per-format configuration for a missing
// sibling format; brand-new titles require an exact lookup match and
// unambiguous per-format profiles/roots.
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
	return s.addToChaptarrWithClient(r, client, instanceID)
}

func (s *Service) addToChaptarrWithClient(r *resolvedRequest, client *chaptarr.Client, instanceID string) (string, string, error) {
	return s.addToChaptarrWithClientContext(context.Background(), r, client, instanceID)
}

func (s *Service) addToChaptarrWithClientContext(parent context.Context, r *resolvedRequest, client *chaptarr.Client, instanceID string) (string, string, error) {
	r.instanceID = instanceID
	ctx, cancel := newBookSetupContext(parent)
	defer cancel()

	// Preflight the live library before lookup/add. The request boundary is the
	// idempotency boundary: a file is already available, a monitored record is
	// repaired in place rather than duplicated. A bare monitor flag is not an
	// accepted request: the edition choice, author gate, monitor read-back, and
	// BookSearch acknowledgement must all converge first.
	books, libraryErr := client.GetAllBooksContext(ctx)
	if libraryErr != nil {
		return "", "", fmt.Errorf("check existing book state: %w", libraryErr)
	}
	title, canonicalBooks, err := s.selectChaptarrLibraryWorkForRequest(
		ctx, client, books, r.foreignID, r.title, r.bookSelection,
	)
	if err != nil {
		return "", "", err
	}
	_, existing, unresolved := recordsForForeignID(canonicalBooks, r.foreignID)
	physicalRecords, unsafeUnresolved := chaptarrPhysicalAndUnsafeRecords(canonicalBooks, r.foreignID)
	if unresolved && unsafeUnresolved {
		return "", "", ErrBookFormatUnresolved
	}
	// Preserve the title the requester explicitly selected. Library ordering is
	// not stable, and a physical sibling or duplicate pocket can carry a title
	// variant that must not redirect the exact work selected in the app.
	if chaptarrTitleIsMultiWork(title) {
		return "", "", ErrBookMultiWorkUnsupported
	}
	if r.bookFormats == nil {
		r.bookFormats = make(map[string]string)
	}
	missing := make([]string, 0, 2)
	var lastErr error
	abortFurtherFormats := false
	for _, mediaType := range chaptarrRequestFormats(r.bookFormat) {
		if completed, ok := r.bookFormats[mediaType]; ok && completed != StatusUnavailable {
			continue
		}
		if abortFurtherFormats {
			r.bookFormats[mediaType] = StatusUnavailable
			continue
		}
		records := existing[mediaType]
		if len(records) == 0 {
			missing = append(missing, mediaType)
			continue
		}
		if r.bookSelection.publication(mediaType) == nil {
			available := false
			for _, rec := range records {
				available = available || chaptarrBookAvailable(rec)
			}
			if available {
				if err := s.completeBookJobFormat(r, mediaType, StatusAvailable); err != nil {
					return "", "", err
				}
				continue
			}
		}
		authorID, err := chaptarrRecordAuthorID(records)
		if err != nil {
			lastErr = preferBookMutationError(lastErr, err)
			r.bookFormats[mediaType] = StatusUnavailable
			abortFurtherFormats = bookMutationOutcomeUnknown(err)
			continue
		}
		target := chaptarrBookTargetForResolvedRequest(r, authorID, title, mediaType)
		formatCtx, formatCancel := s.newBookMutationContext(parent)
		status, err := s.ensureChaptarrBookRequest(formatCtx, client, r.instanceID, target)
		formatCancel()
		if err != nil {
			lastErr = preferBookMutationError(lastErr, err)
			r.bookFormats[mediaType] = StatusUnavailable
			abortFurtherFormats = bookMutationOutcomeUnknown(err)
			continue
		}
		if err := s.completeBookJobFormat(r, mediaType, status); err != nil {
			return "", "", err
		}
	}
	if abortFurtherFormats {
		markRemainingBookFormatsUnavailable(r.bookFormats, missing)
		return s.finishBookMutation(r, title, lastErr)
	}
	if len(missing) == 0 {
		return s.finishBookMutation(r, title, lastErr)
	}
	// Existing-format convergence can consume its full independent budget. Use
	// a fresh setup window for the missing-format author/lookup/config reads so a
	// slow first format cannot strand the second before its own mutation budget.
	cancel()
	ctx, cancel = newBookSetupContext(parent)
	defer cancel()
	if len(existing) > 0 || len(physicalRecords) > 0 {
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
		for _, record := range physicalRecords {
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
		if len(authorIDs) > 1 {
			return s.finishBookSetupFailure(
				r,
				title,
				missing,
				fmt.Errorf("existing canonical book records disagree on author"),
			)
		}
		authorID := 0
		for collectedID := range authorIDs {
			authorID = collectedID
		}
		if authorID == 0 {
			return s.finishBookSetupFailure(
				r,
				title,
				missing,
				fmt.Errorf("existing book author is unresolved"),
			)
		}
		author, err := client.GetAuthorContext(ctx, authorID)
		if err != nil {
			return s.finishBookSetupFailure(
				r,
				title,
				missing,
				fmt.Errorf("load existing book author: %w", err),
			)
		}
		if !bookSelectionMatchesAuthorIdentity(r.bookSelection, author.ForeignAuthorID, author.AuthorName) {
			return s.finishBookSetupFailure(
				r,
				title,
				missing,
				fmt.Errorf("%w: selected author changed", ErrBookMatchNotFound),
			)
		}
		config, ok := bookConfigFromAuthor(author)
		if !ok {
			return s.finishBookSetupFailure(
				r,
				title,
				missing,
				fmt.Errorf("%w: existing book configuration is incomplete for one or more formats", ErrBookConfigurationInvalid),
			)
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
		lookupResults, lookupErr := client.LookupBookContext(ctx, title)
		if lookupErr != nil {
			return s.finishBookSetupFailure(
				r,
				title,
				missing,
				fmt.Errorf("load missing-format seed editions: %w", lookupErr),
			)
		}
		match.Editions, lookupErr = selectChaptarrSeedEditions(lookupResults, r.foreignID, title, author, r.bookSelection)
		if lookupErr != nil {
			return s.finishBookSetupFailure(r, title, missing, lookupErr)
		}
		for index, mediaType := range missing {
			formatCtx, formatCancel := s.newBookMutationContext(parent)
			status, materialized, err := s.ensureMaterializedChaptarrBook(formatCtx, client, r.instanceID, match, config.authorID, title, mediaType, r.bookJobID, r.bookSelection)
			if err != nil {
				formatCancel()
				lastErr = preferBookMutationError(lastErr, err)
				r.bookFormats[mediaType] = StatusUnavailable
				if bookMutationOutcomeUnknown(err) {
					markRemainingBookFormatsUnavailable(r.bookFormats, missing[index+1:])
					break
				}
				continue
			}
			if materialized {
				formatCancel()
				if err := s.completeBookJobFormat(r, mediaType, status); err != nil {
					return "", "", err
				}
				config.markMonitorFuture(mediaType)
				continue
			}
			status, err = s.addChaptarrBookRecord(formatCtx, client, r.instanceID, match, &config, title, match.TitleSlug, mediaType, r.bookJobID, r.bookSelection)
			formatCancel()
			if err != nil {
				lastErr = preferBookMutationError(lastErr, err)
				r.bookFormats[mediaType] = StatusUnavailable
				if bookMutationOutcomeUnknown(err) {
					markRemainingBookFormatsUnavailable(r.bookFormats, missing[index+1:])
					break
				}
				continue
			}
			if err := s.completeBookJobFormat(r, mediaType, status); err != nil {
				return "", "", err
			}
		}
		return s.finishBookMutation(r, title, lastErr)
	}
	if r.title == "" {
		return "", "", errBookTitleRequired
	}

	results, err := client.LookupBookContext(ctx, r.title)
	if err != nil {
		if len(missing) > 0 {
			return "", "", fmt.Errorf("book lookup failed: %w", err)
		}
		results = nil
	}
	match, err := selectChaptarrLookupResultWithSelection(results, r.foreignID, r.title, r.bookSelection)
	if err != nil {
		return "", "", err
	}
	if match == nil && len(missing) > 0 {
		// The foreignBookId belongs to a book already in the library (owned
		// records carry a library foreignBookId the metadata lookup can't match);
		// complete the missing format from the existing records instead of adding.
		lastErr = preferBookMutationError(lastErr, fmt.Errorf("%w: book not found for foreign id %s", ErrBookMatchNotFound, r.foreignID))
		for _, mediaType := range missing {
			r.bookFormats[mediaType] = StatusUnavailable
		}
	}
	if match == nil {
		return s.finishBookMutation(r, title, lastErr)
	}

	authorName, foreignAuthorID := chaptarrLookupAuthorIdentity(match)
	if authorName == "" && foreignAuthorID == "" {
		return "", "", fmt.Errorf("%w: selected author identity is missing", ErrBookMutationUnverified)
	}
	// This first resolution chooses configuration without mutating anything.
	// addChaptarrBookRecord repeats it under deterministic provider/name locks
	// immediately before POST, which closes alias races without serializing
	// unrelated authors during the longer catalog/search convergence.
	localAuthor, err := findExistingChaptarrAuthor(ctx, client, foreignAuthorID, authorName)
	if err != nil {
		return "", "", err
	}
	var config bookAddConfig
	if localAuthor != nil {
		localAuthorID := localAuthor.ID
		localAuthor, err = client.GetAuthorContext(ctx, localAuthor.ID)
		if err != nil {
			return "", "", fmt.Errorf("load existing selected author: %w", err)
		}
		providerMatched := foreignAuthorID != "" && localAuthor.ForeignAuthorID == foreignAuthorID
		nameMatched := authorName != "" && normalizeBookIdentity(localAuthor.AuthorName) == normalizeBookIdentity(authorName)
		if localAuthor.ID != localAuthorID || (!providerMatched && !nameMatched) {
			return "", "", fmt.Errorf("%w: selected author identity changed", ErrBookMutationUnverified)
		}
		// The unique name fallback deliberately tolerates provider-id drift. Once
		// it resolves, pin every later write/read-back to the authoritative local
		// identity instead of carrying the stale lookup provider id forward.
		authorName = strings.TrimSpace(localAuthor.AuthorName)
		foreignAuthorID = strings.TrimSpace(localAuthor.ForeignAuthorID)
		match.AuthorName = authorName
		match.ForeignAuthorID = foreignAuthorID
		if match.Author != nil {
			match.Author.ID = localAuthor.ID
			match.Author.AuthorName = authorName
			match.Author.ForeignAuthorID = foreignAuthorID
		}
		var ok bool
		config, ok = bookConfigFromAuthor(localAuthor)
		if !ok {
			return "", "", fmt.Errorf("%w: existing author configuration is incomplete for one or more formats", ErrBookConfigurationInvalid)
		}
	} else {
		qps, err := client.GetQualityProfilesContext(ctx)
		if err != nil {
			return "", "", fmt.Errorf("load book quality profiles: %w", err)
		}
		if len(qps) == 0 {
			return "", "", fmt.Errorf("%w: no quality profiles available", ErrBookConfigurationInvalid)
		}
		mps, err := client.GetMetadataProfilesContext(ctx)
		if err != nil {
			return "", "", fmt.Errorf("load book metadata profiles: %w", err)
		}
		if len(mps) == 0 {
			return "", "", fmt.Errorf("%w: no metadata profiles available", ErrBookConfigurationInvalid)
		}
		folders, err := client.GetRootFoldersContext(ctx)
		if err != nil {
			return "", "", fmt.Errorf("load book root folders: %w", err)
		}
		if len(folders) == 0 {
			return "", "", fmt.Errorf("%w: no root folders available", ErrBookConfigurationInvalid)
		}
		config, err = selectBookConfig(qps, mps, folders)
		if err != nil {
			return "", "", err
		}
	}
	title = strings.TrimSpace(match.Title)
	if title == "" {
		title = r.title
	}
	if chaptarrTitleIsMultiWork(title) {
		return "", "", ErrBookMultiWorkUnsupported
	}
	titleSlug := match.TitleSlug
	if titleSlug == "" {
		titleSlug = fallbackTitleSlug(title)
	}

	// Chaptarr stores a title's ebook and audiobook as separate book records
	// (same foreignBookId, different mediaType), so a "both" request adds the
	// book once per format. Adding at least one record counts as requested; the
	// last error is surfaced only if every requested format failed.
	for index, mediaType := range missing {
		formatCtx, formatCancel := s.newBookMutationContext(parent)
		status, materialized, err := s.ensureMaterializedChaptarrBook(formatCtx, client, r.instanceID, match, config.authorID, title, mediaType, r.bookJobID, r.bookSelection)
		if err != nil {
			formatCancel()
			lastErr = preferBookMutationError(lastErr, err)
			r.bookFormats[mediaType] = StatusUnavailable
			if bookMutationOutcomeUnknown(err) {
				markRemainingBookFormatsUnavailable(r.bookFormats, missing[index+1:])
				break
			}
			continue
		}
		if materialized {
			formatCancel()
			if err := s.completeBookJobFormat(r, mediaType, status); err != nil {
				return "", "", err
			}
			config.markMonitorFuture(mediaType)
			continue
		}
		status, err = s.addChaptarrBookRecord(formatCtx, client, r.instanceID, match, &config, title, titleSlug, mediaType, r.bookJobID, r.bookSelection)
		formatCancel()
		if err != nil {
			lastErr = preferBookMutationError(lastErr, err)
			r.bookFormats[mediaType] = StatusUnavailable
			if bookMutationOutcomeUnknown(err) {
				markRemainingBookFormatsUnavailable(r.bookFormats, missing[index+1:])
				break
			}
			continue
		}
		if err := s.completeBookJobFormat(r, mediaType, status); err != nil {
			return "", "", err
		}
	}
	return s.finishBookMutation(r, title, lastErr)
}

// bookAddConfig is the complete Chaptarr author configuration required by
// current releases. Chaptarr keeps separate quality/metadata profiles and root
// paths for ebooks and audiobooks; the legacy singular fields in the add body
// are still populated from the concrete format for older releases.
type bookAddConfig struct {
	authorID                   int
	ebookQualityProfileID      int
	audiobookQualityProfileID  int
	ebookMetadataProfileID     int
	audiobookMetadataProfileID int
	ebookRootFolderPath        string
	audiobookRootFolderPath    string
	ebookMonitorFuture         bool
	audiobookMonitorFuture     bool
}

func (c bookAddConfig) forFormat(format string) (qualityProfileID, metadataProfileID int, rootFolderPath string) {
	if format == BookFormatAudiobook {
		return c.audiobookQualityProfileID, c.audiobookMetadataProfileID, c.audiobookRootFolderPath
	}
	return c.ebookQualityProfileID, c.ebookMetadataProfileID, c.ebookRootFolderPath
}

func (c bookAddConfig) complete() bool {
	return c.ebookQualityProfileID > 0 &&
		c.audiobookQualityProfileID > 0 &&
		c.ebookMetadataProfileID > 0 &&
		c.audiobookMetadataProfileID > 0 &&
		strings.TrimSpace(c.ebookRootFolderPath) != "" &&
		strings.TrimSpace(c.audiobookRootFolderPath) != ""
}

func (c *bookAddConfig) markMonitorFuture(mediaType string) {
	if mediaType == BookFormatAudiobook {
		c.audiobookMonitorFuture = true
	} else if mediaType == BookFormatEbook {
		c.ebookMonitorFuture = true
	}
}

func bookConfigFromAuthor(author *chaptarr.Author) (bookAddConfig, bool) {
	if author == nil {
		return bookAddConfig{}, false
	}
	config := bookAddConfig{authorID: author.ID}
	hasPerFormatConfig := author.EbookQualityProfileID > 0 ||
		author.AudiobookQualityProfileID > 0 ||
		author.EbookMetadataProfileID > 0 ||
		author.AudiobookMetadataProfileID > 0 ||
		strings.TrimSpace(author.EbookRootFolderPath) != "" ||
		strings.TrimSpace(author.AudiobookRootFolderPath) != ""
	if hasPerFormatConfig {
		config.ebookQualityProfileID = author.EbookQualityProfileID
		config.audiobookQualityProfileID = author.AudiobookQualityProfileID
		config.ebookMetadataProfileID = author.EbookMetadataProfileID
		config.audiobookMetadataProfileID = author.AudiobookMetadataProfileID
		config.ebookRootFolderPath = strings.TrimSpace(author.EbookRootFolderPath)
		config.audiobookRootFolderPath = strings.TrimSpace(author.AudiobookRootFolderPath)
	} else {
		config.ebookQualityProfileID = author.QualityProfileID
		config.audiobookQualityProfileID = author.QualityProfileID
		config.ebookMetadataProfileID = author.MetadataProfileID
		config.audiobookMetadataProfileID = author.MetadataProfileID
		config.ebookRootFolderPath = strings.TrimSpace(author.Path)
		config.audiobookRootFolderPath = strings.TrimSpace(author.Path)
	}
	config.ebookMonitorFuture = author.EbookMonitorFuture
	config.audiobookMonitorFuture = author.AudiobookMonitorFuture
	return config, config.complete()
}

func selectBookConfig(qualityProfiles []chaptarr.QualityProfile, metadataProfiles []chaptarr.MetadataProfile, folders []chaptarr.RootFolder) (bookAddConfig, error) {
	config := bookAddConfig{}
	for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
		qualityProfileID, ok := selectBookQualityProfile(qualityProfiles, format)
		if !ok {
			return bookAddConfig{}, fmt.Errorf("%w: Chaptarr %s quality profile selection is ambiguous", ErrBookConfigurationInvalid, format)
		}
		metadataProfileID, ok := selectBookMetadataProfile(metadataProfiles, format)
		if !ok {
			return bookAddConfig{}, fmt.Errorf("%w: Chaptarr %s metadata profile selection is ambiguous", ErrBookConfigurationInvalid, format)
		}
		root, ok := selectBookRoot(folders, format)
		if !ok {
			return bookAddConfig{}, fmt.Errorf("%w: no accessible root folder available for %s", ErrBookConfigurationInvalid, format)
		}
		if format == BookFormatEbook {
			config.ebookQualityProfileID = qualityProfileID
			config.ebookMetadataProfileID = metadataProfileID
			config.ebookRootFolderPath = root.Path
		} else {
			config.audiobookQualityProfileID = qualityProfileID
			config.audiobookMetadataProfileID = metadataProfileID
			config.audiobookRootFolderPath = root.Path
		}
	}
	return config, nil
}

func selectBookQualityProfile(profiles []chaptarr.QualityProfile, format string) (int, bool) {
	expectedType := format
	typed := make([]bookProfileCandidate, 0, len(profiles))
	untyped := make([]bookProfileCandidate, 0, len(profiles))
	for _, profile := range profiles {
		profileType := strings.ToLower(strings.TrimSpace(profile.ProfileType))
		if profileType == BookFormatEbook || profileType == BookFormatAudiobook {
			if profileType == expectedType {
				typed = append(typed, bookProfileCandidate{ID: profile.ID, Name: profile.Name})
			}
		} else if profileType == "" {
			untyped = append(untyped, bookProfileCandidate{ID: profile.ID, Name: profile.Name})
		}
	}
	if len(typed) > 0 {
		return selectSingleBookProfileCandidate(typed)
	}
	return selectLegacyBookProfile(untyped, format)
}

func selectBookMetadataProfile(profiles []chaptarr.MetadataProfile, format string) (int, bool) {
	expectedType := "2"
	if format == BookFormatAudiobook {
		expectedType = "1"
	}
	typed := make([]bookProfileCandidate, 0, len(profiles))
	untyped := make([]bookProfileCandidate, 0, len(profiles))
	for _, profile := range profiles {
		profileType := strings.ToLower(strings.TrimSpace(profile.ProfileType))
		recognized := profileType == "0" || profileType == "1" || profileType == "2" ||
			profileType == BookFormatEbook || profileType == BookFormatAudiobook
		matches := profileType == expectedType ||
			(format == BookFormatEbook && profileType == BookFormatEbook) ||
			(format == BookFormatAudiobook && profileType == BookFormatAudiobook)
		if matches {
			typed = append(typed, bookProfileCandidate{ID: profile.ID, Name: profile.Name})
		} else if !recognized && profileType == "" {
			untyped = append(untyped, bookProfileCandidate{ID: profile.ID, Name: profile.Name})
		}
	}
	if len(typed) > 0 {
		return selectSingleBookProfileCandidate(typed)
	}
	return selectLegacyBookProfile(untyped, format)
}

type bookProfileCandidate struct {
	ID   int
	Name string
}

func selectSingleBookProfileCandidate(profiles []bookProfileCandidate) (int, bool) {
	return selectBookProfileCandidate(profiles)
}

func selectLegacyBookProfile(profiles []bookProfileCandidate, format string) (int, bool) {
	if len(profiles) == 1 {
		return profiles[0].ID, profiles[0].ID > 0
	}
	formatMatches := make([]bookProfileCandidate, 0, len(profiles))
	for _, profile := range profiles {
		if bookProfileNameMatchesFormat(profile.Name, format) {
			formatMatches = append(formatMatches, profile)
		}
	}
	if len(formatMatches) > 0 {
		return selectBookProfileCandidate(formatMatches)
	}
	return selectBookProfileCandidate(profiles)
}

func selectBookProfileCandidate(profiles []bookProfileCandidate) (int, bool) {
	valid := make([]bookProfileCandidate, 0, len(profiles))
	for _, profile := range profiles {
		if profile.ID > 0 {
			valid = append(valid, profile)
		}
	}
	if len(valid) == 1 {
		return valid[0].ID, true
	}
	selected := 0
	for _, profile := range valid {
		if bookProfileNameIsDefault(profile.Name) {
			if selected != 0 {
				return 0, false
			}
			selected = profile.ID
		}
	}
	return selected, selected != 0
}

func bookProfileNameMatchesFormat(name, format string) bool {
	normalized := strings.NewReplacer("-", " ", "_", " ").Replace(strings.ToLower(strings.TrimSpace(name)))
	if format == BookFormatAudiobook {
		return strings.Contains(normalized, "audiobook") || strings.Contains(normalized, "audio book")
	}
	return strings.Contains(normalized, "ebook") || strings.Contains(normalized, "e book")
}

func bookProfileNameIsDefault(name string) bool {
	normalized := strings.NewReplacer("-", " ", "_", " ").Replace(strings.ToLower(strings.TrimSpace(name)))
	for _, part := range strings.Fields(normalized) {
		if part == "default" {
			return true
		}
	}
	return false
}

// finishBookMutation reduces per-format outcomes without allowing a partial
// "both" request to masquerade as complete success.
func (s *Service) finishBookMutation(r *resolvedRequest, title string, lastErr error) (string, string, error) {
	requestedFormats := expandBookFormat(r.bookFormat)
	succeeded := 0
	for _, format := range requestedFormats {
		status := r.bookFormats[format]
		if status != StatusUnavailable {
			if strings.TrimSpace(status) == "" {
				continue
			}
			succeeded++
		}
	}
	// Never reduce an outcome-unknown sibling to a clean partial response. That
	// would label it unavailable and immediately invite a duplicate retry even
	// though its seed/search may have committed upstream.
	if bookMutationOutcomeUnknown(lastErr) {
		return "", "", lastErr
	}
	if succeeded == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("no requested format could be completed")
		}
		return "", "", lastErr
	}
	s.invalidateBookCaches(r.instanceID)
	if succeeded != len(requestedFormats) {
		return StatusPartial, title, nil
	}
	requestedStatuses := make(map[string]string, len(requestedFormats))
	for _, format := range requestedFormats {
		requestedStatuses[format] = r.bookFormats[format]
	}
	if allBookFormatsAre(requestedStatuses, StatusAvailable) {
		return StatusAvailable, title, nil
	}
	if anyBookFormatIs(requestedStatuses, StatusAvailable) {
		return StatusPartial, title, nil
	}
	return StatusRequested, title, nil
}

// finishBookSetupFailure preserves a concrete format that completed before a
// later sibling failed during read-only setup. No write for the missing
// formats has started yet, so committing the completed coverage as a partial
// result is both accurate and safer than deleting its durable checkpoint or
// retrying the whole "both" action indefinitely.
func (s *Service) finishBookSetupFailure(r *resolvedRequest, title string, missing []string, setupErr error) (string, string, error) {
	hasCompleted := false
	for _, format := range expandBookFormat(r.bookFormat) {
		status := r.bookFormats[format]
		if status != StatusUnavailable {
			if strings.TrimSpace(status) == "" {
				continue
			}
			hasCompleted = true
			break
		}
	}
	if !hasCompleted {
		return "", "", setupErr
	}
	markRemainingBookFormatsUnavailable(r.bookFormats, missing)
	return s.finishBookMutation(r, title, nil)
}

func markRemainingBookFormatsUnavailable(statuses map[string]string, formats []string) {
	for _, format := range formats {
		statuses[format] = StatusUnavailable
	}
}

func bookMutationOutcomeUnknown(err error) bool {
	return errors.Is(err, ErrBookSearchUnconfirmed) || errors.Is(err, ErrBookMutationUnverified) ||
		errors.Is(err, ErrBookCatalogPending) || errors.Is(err, ErrBookOutcomePending)
}

func preferBookMutationError(current, candidate error) error {
	if current == nil {
		return candidate
	}
	if candidate == nil {
		return current
	}
	rank := func(err error) int {
		switch {
		case errors.Is(err, ErrBookSearchUnconfirmed):
			return 4
		case errors.Is(err, ErrBookMutationUnverified):
			return 3
		case errors.Is(err, ErrBookCatalogPending):
			return 2
		default:
			return 1
		}
	}
	if rank(candidate) > rank(current) {
		return candidate
	}
	return current
}

// chaptarrBookTarget is the immutable identity selected before a mutation.
// Local provider IDs can drift between metadata sources, so an existing author
// is pinned by local ID; a new one is re-resolved by exact provider identity or
// one unique normalized name. Work and format are rechecked before every write
// and after the final read-back.
type chaptarrBookTarget struct {
	jobID             int64
	authorID          int
	foreignAuthorID   string
	authorName        string
	foreignBookID     string
	title             string
	mediaType         string
	publication       *BookPublicationSelection
	explicitSelection bool
	selection         *BookSelection
}

type chaptarrBookCandidate struct {
	book                chaptarr.Book
	editions            []chaptarr.Edition
	usable              []chaptarr.Edition
	identityTier        int
	editionIdentityTier int
	editionMultiWork    bool
}

// selectChaptarrLibraryWork narrows a provider id to the exact work selected
// by the requester before any availability shortcut or sibling add is allowed.
// Provider ids are external metadata and can collide or be reassigned; a
// conflicting title or local author therefore fails closed instead of letting
// an unrelated row report Available or donate its author configuration.
func selectChaptarrLibraryWork(books []chaptarr.Book, foreignBookID, selectedTitle string) (string, []chaptarr.Book, error) {
	return selectChaptarrLibraryWorkWithSelection(books, foreignBookID, selectedTitle, nil)
}

func selectChaptarrLibraryWorkWithSelection(books []chaptarr.Book, foreignBookID, selectedTitle string, selection *BookSelection) (string, []chaptarr.Book, error) {
	selectedTitle = strings.TrimSpace(selectedTitle)
	sameID := make([]chaptarr.Book, 0)
	for _, book := range books {
		if foreignBookID != "" && book.ForeignBookID == foreignBookID {
			sameID = append(sameID, book)
		}
	}
	if len(sameID) == 0 {
		return selectedTitle, nil, nil
	}
	if selectedTitle == "" {
		for _, book := range sameID {
			if candidate := strings.TrimSpace(book.Title); candidate != "" {
				selectedTitle = candidate
				break
			}
		}
	}
	if selectedTitle == "" {
		return "", nil, fmt.Errorf("%w: existing book title is unresolved", ErrBookMutationUnverified)
	}

	bestTitleTier := 0
	selectedBooks := make([]chaptarr.Book, 0, len(sameID))
	for _, book := range sameID {
		tier := bookTitleIdentityTier(book.Title, selectedTitle)
		if tier == 0 {
			return "", nil, fmt.Errorf("%w: provider id identifies conflicting works", ErrBookMutationUnverified)
		}
		if tier > bestTitleTier {
			bestTitleTier = tier
			selectedBooks = selectedBooks[:0]
		}
		if tier == bestTitleTier {
			selectedBooks = append(selectedBooks, book)
		}
	}
	if selection != nil && (selection.ForeignAuthorID != "" || selection.AuthorName != "") {
		providerMatches := make([]chaptarr.Book, 0, len(selectedBooks))
		nameMatches := make([]chaptarr.Book, 0, len(selectedBooks))
		for _, book := range selectedBooks {
			if book.Author == nil {
				continue
			}
			if selection.ForeignAuthorID != "" && strings.TrimSpace(book.Author.ForeignAuthorID) == selection.ForeignAuthorID {
				providerMatches = append(providerMatches, book)
			}
			if selection.AuthorName != "" && normalizeBookIdentity(book.Author.AuthorName) == normalizeBookIdentity(selection.AuthorName) {
				nameMatches = append(nameMatches, book)
			}
		}
		switch {
		case len(providerMatches) > 0:
			selectedBooks = providerMatches
		case len(nameMatches) > 0:
			selectedBooks = nameMatches
		default:
			return "", nil, fmt.Errorf("%w: selected author is no longer present", ErrBookMatchNotFound)
		}
	}

	authorIDs := make(map[int]bool)
	authorNames := make(map[string]bool)
	foreignAuthorIDs := make(map[string]bool)
	for _, book := range selectedBooks {
		if chaptarrTitleIsMultiWork(book.Title) {
			return "", nil, ErrBookMultiWorkUnsupported
		}
		authorID := book.AuthorID
		if book.Author != nil {
			if authorID == 0 {
				authorID = book.Author.ID
			}
			if name := normalizeBookIdentity(book.Author.AuthorName); name != "" {
				authorNames[name] = true
			}
			if providerID := strings.TrimSpace(book.Author.ForeignAuthorID); providerID != "" {
				foreignAuthorIDs[providerID] = true
			}
		}
		if authorID > 0 {
			authorIDs[authorID] = true
		}
	}
	if len(authorIDs) > 1 || (len(authorIDs) == 0 && len(authorNames) > 1 && len(foreignAuthorIDs) != 1) {
		return "", nil, fmt.Errorf("%w: provider id identifies conflicting authors", ErrBookMutationUnverified)
	}
	return selectedTitle, selectedBooks, nil
}

func selectChaptarrLookupResult(results []chaptarr.LookupResult, foreignBookID, selectedTitle string) (*chaptarr.LookupResult, error) {
	return selectChaptarrLookupResultWithSelection(results, foreignBookID, selectedTitle, nil)
}

func selectChaptarrLookupResultWithSelection(results []chaptarr.LookupResult, foreignBookID, selectedTitle string, selection *BookSelection) (*chaptarr.LookupResult, error) {
	type rankedLookup struct {
		result     chaptarr.LookupResult
		authorName string
		authorID   string
	}
	candidates := make([]rankedLookup, 0, len(results))
	bestTier := 0
	for _, result := range results {
		if result.ForeignBookID != foreignBookID {
			continue
		}
		tier := bookTitleIdentityTier(result.Title, selectedTitle)
		if tier == 0 {
			continue
		}
		if tier < bestTier {
			continue
		}
		if tier > bestTier {
			bestTier = tier
			candidates = candidates[:0]
		}
		authorName, authorID := chaptarrLookupAuthorIdentity(&result)
		candidates = append(candidates, rankedLookup{
			result: result, authorName: normalizeBookIdentity(authorName), authorID: strings.TrimSpace(authorID),
		})
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	for _, candidate := range candidates {
		if chaptarrTitleIsMultiWork(candidate.result.Title) {
			return nil, ErrBookMultiWorkUnsupported
		}
	}
	if selection != nil && (selection.ForeignAuthorID != "" || selection.AuthorName != "") {
		providerMatches := make([]rankedLookup, 0, len(candidates))
		nameMatches := make([]rankedLookup, 0, len(candidates))
		for _, candidate := range candidates {
			if selection.ForeignAuthorID != "" && candidate.authorID == selection.ForeignAuthorID {
				providerMatches = append(providerMatches, candidate)
			}
			if selection.AuthorName != "" && candidate.authorName == normalizeBookIdentity(selection.AuthorName) {
				nameMatches = append(nameMatches, candidate)
			}
		}
		switch {
		case len(providerMatches) > 0:
			candidates = providerMatches
		case len(nameMatches) > 0:
			candidates = nameMatches
		default:
			return nil, nil
		}
	}
	if selection != nil && (selection.Ebook != nil || selection.Audiobook != nil) {
		publicationMatches := make([]rankedLookup, 0, len(candidates))
		for _, candidate := range candidates {
			ebookMatches, matchErr := lookupResultMatchesPublication(candidate.result, BookFormatEbook, selection.Ebook)
			if matchErr != nil {
				return nil, matchErr
			}
			audiobookMatches, matchErr := lookupResultMatchesPublication(candidate.result, BookFormatAudiobook, selection.Audiobook)
			if matchErr != nil {
				return nil, matchErr
			}
			if ebookMatches && audiobookMatches {
				publicationMatches = append(publicationMatches, candidate)
			}
		}
		candidates = publicationMatches
		if len(candidates) == 0 {
			return nil, nil
		}
	}
	for i := 1; i < len(candidates); i++ {
		left, right := candidates[0], candidates[i]
		sameProvider := left.authorID != "" && left.authorID == right.authorID
		sameName := left.authorName != "" && left.authorName == right.authorName
		if !sameProvider && !sameName {
			return nil, fmt.Errorf("%w: lookup returned ambiguous authors", ErrBookMutationUnverified)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if (left.authorName != "") != (right.authorName != "") {
			return left.authorName != ""
		}
		if (left.authorID != "") != (right.authorID != "") {
			return left.authorID != ""
		}
		if left.authorName != right.authorName {
			return left.authorName < right.authorName
		}
		if left.authorID != right.authorID {
			return left.authorID < right.authorID
		}
		return normalizeBookIdentity(left.result.Title) < normalizeBookIdentity(right.result.Title)
	})
	selected := candidates[0].result
	return &selected, nil
}

func selectChaptarrSeedEditions(results []chaptarr.LookupResult, foreignBookID, selectedTitle string, author *chaptarr.Author, selections ...*BookSelection) ([]json.RawMessage, error) {
	var selection *BookSelection
	if len(selections) > 0 {
		selection = selections[0]
	}
	if exact, err := selectChaptarrLookupResultWithSelection(results, foreignBookID, selectedTitle, selection); err != nil {
		return nil, err
	} else if exact != nil && len(exact.Editions) > 0 {
		if !chaptarrLookupMatchesAuthor(*exact, author) {
			return nil, fmt.Errorf("%w: missing-format seed author identity changed", ErrBookMutationUnverified)
		}
		return append([]json.RawMessage(nil), exact.Editions...), nil
	}

	bestTitleTier := 0
	candidates := make([]chaptarr.LookupResult, 0, 1)
	for _, result := range results {
		if len(result.Editions) == 0 || chaptarrTitleIsMultiWork(result.Title) {
			continue
		}
		titleTier := bookTitleIdentityTier(result.Title, selectedTitle)
		if titleTier == 0 || titleTier < bestTitleTier {
			continue
		}
		if !chaptarrLookupMatchesAuthor(result, author) {
			continue
		}
		if selection != nil {
			ebookMatches, matchErr := lookupResultMatchesPublication(result, BookFormatEbook, selection.Ebook)
			if matchErr != nil {
				return nil, matchErr
			}
			audiobookMatches, matchErr := lookupResultMatchesPublication(result, BookFormatAudiobook, selection.Audiobook)
			if matchErr != nil {
				return nil, matchErr
			}
			if !ebookMatches || !audiobookMatches {
				continue
			}
		}
		if titleTier > bestTitleTier {
			bestTitleTier = titleTier
			candidates = candidates[:0]
		}
		candidates = append(candidates, result)
	}
	if len(candidates) != 1 {
		return nil, fmt.Errorf("%w: missing-format seed editions are unresolved or ambiguous", ErrBookMutationUnverified)
	}
	return append([]json.RawMessage(nil), candidates[0].Editions...), nil
}

func chaptarrLookupMatchesAuthor(result chaptarr.LookupResult, author *chaptarr.Author) bool {
	if author == nil {
		return false
	}
	authorName, authorProviderID := chaptarrLookupAuthorIdentity(&result)
	providerMatched := strings.TrimSpace(author.ForeignAuthorID) != "" &&
		strings.TrimSpace(authorProviderID) == strings.TrimSpace(author.ForeignAuthorID)
	nameMatched := normalizeBookIdentity(author.AuthorName) != "" &&
		normalizeBookIdentity(authorName) == normalizeBookIdentity(author.AuthorName)
	return providerMatched || nameMatched
}

func chaptarrLookupAuthorIdentity(match *chaptarr.LookupResult) (authorName, foreignAuthorID string) {
	if match == nil {
		return "", ""
	}
	authorName = strings.TrimSpace(match.AuthorName)
	foreignAuthorID = strings.TrimSpace(match.ForeignAuthorID)
	if match.Author != nil {
		if authorName == "" {
			authorName = strings.TrimSpace(match.Author.AuthorName)
		}
		if foreignAuthorID == "" {
			foreignAuthorID = strings.TrimSpace(match.Author.ForeignAuthorID)
		}
	}
	return authorName, foreignAuthorID
}

func findExistingChaptarrAuthor(ctx context.Context, client *chaptarr.Client, foreignAuthorID, authorName string) (*chaptarr.Author, error) {
	authors, err := client.GetAllAuthorsContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve selected book author: %w", err)
	}
	matches := make([]chaptarr.Author, 0, 1)
	if foreignAuthorID != "" {
		for _, author := range authors {
			if author.ForeignAuthorID == foreignAuthorID {
				matches = append(matches, author)
			}
		}
	}
	if len(matches) == 0 && authorName != "" {
		for _, author := range authors {
			if normalizeBookIdentity(author.AuthorName) == normalizeBookIdentity(authorName) {
				matches = append(matches, author)
			}
		}
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("%w: selected author identity is ambiguous", ErrBookMutationUnverified)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	if matches[0].ID <= 0 {
		return nil, fmt.Errorf("%w: selected author has no local identity", ErrBookMutationUnverified)
	}
	return &matches[0], nil
}

// ensureMaterializedChaptarrBook closes a subtle both-format race. Seeding a
// new author can populate more than the one requested mediaType while the
// catalog settles. Before a later format POST, re-read that author's exact work
// and converge any row Chaptarr already created instead of duplicating it.
func (s *Service) ensureMaterializedChaptarrBook(ctx context.Context, client *chaptarr.Client, instanceID string, match *chaptarr.LookupResult, authorID int, title, mediaType string, jobID int64, selections ...*BookSelection) (status string, materialized bool, err error) {
	if authorID <= 0 {
		return "", false, nil
	}
	target := chaptarrBookTargetFromLookup(match, authorID, title, mediaType, selections...)
	target.jobID = jobID
	materialized, err = s.waitForMaterializedChaptarrBook(ctx, client, target)
	if err != nil || !materialized {
		return "", materialized, err
	}
	status, err = s.ensureChaptarrBookRequest(ctx, client, instanceID, target)
	return status, true, err
}

func chaptarrBookTargetFromLookup(match *chaptarr.LookupResult, authorID int, title, mediaType string, selections ...*BookSelection) chaptarrBookTarget {
	target := chaptarrBookTarget{
		authorID:        authorID,
		foreignBookID:   match.ForeignBookID,
		title:           title,
		mediaType:       mediaType,
		foreignAuthorID: match.ForeignAuthorID,
		authorName:      match.AuthorName,
	}
	if match.Author != nil {
		if target.foreignAuthorID == "" {
			target.foreignAuthorID = match.Author.ForeignAuthorID
		}
		if target.authorName == "" {
			target.authorName = match.Author.AuthorName
		}
	}
	if len(selections) > 0 && selections[0] != nil {
		target.publication = selections[0].publication(mediaType)
		target.explicitSelection = true
		target.selection = bookSelectionForFormat(selections[0], mediaType)
	}
	return target
}

// waitForMaterializedChaptarrBook proves a row is either present or absent
// after the relevant author refresh has quiesced. It performs no mutation, so
// callers can use it inside the short author-seed critical section and release
// that lock before edition/search convergence.
func (s *Service) waitForMaterializedChaptarrBook(ctx context.Context, client *chaptarr.Client, target chaptarrBookTarget) (bool, error) {
	interval := s.bookSettleInterval
	if interval <= 0 {
		interval = defaultBookSettleInterval
	}
	stableAbsent := 0
	for {
		books, err := client.GetBooksContext(ctx, target.authorID)
		if err != nil {
			if ctx.Err() != nil {
				return false, fmt.Errorf("%w: %s catalog did not settle", ErrBookCatalogPending, target.mediaType)
			}
			return false, fmt.Errorf("recheck materialized %s book: %w", target.mediaType, err)
		}
		bestIdentityTier := 0
		materializedBooks := make([]chaptarr.Book, 0, 1)
		for _, book := range books {
			if book.ForeignBookID == target.foreignBookID && !bookTitlesMatch(book.Title, target.title) {
				return false, fmt.Errorf("%w: provider id identifies conflicting works", ErrBookMutationUnverified)
			}
			tier := chaptarrBookIdentityTier(book, target)
			if tier == 0 || tier < bestIdentityTier {
				continue
			}
			if tier > bestIdentityTier {
				bestIdentityTier = tier
				materializedBooks = materializedBooks[:0]
			}
			materializedBooks = append(materializedBooks, book)
		}
		if len(materializedBooks) > 0 {
			for _, book := range materializedBooks {
				if chaptarrTitleIsMultiWork(book.Title) {
					return false, ErrBookMultiWorkUnsupported
				}
			}
			return true, nil
		}
		commands, err := client.GetCommandsContext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return false, fmt.Errorf("%w: %s catalog did not settle", ErrBookCatalogPending, target.mediaType)
			}
			return false, fmt.Errorf("recheck Chaptarr catalog commands: %w", err)
		}
		if chaptarrCatalogCommandActive(commands, target.authorID) {
			stableAbsent = 0
		} else {
			stableAbsent++
			if stableAbsent >= 2 {
				return false, nil
			}
		}
		if err := waitForBookPoll(ctx, interval); err != nil {
			return false, fmt.Errorf("%w: %s catalog did not settle", ErrBookCatalogPending, target.mediaType)
		}
	}
}

// addChaptarrBookRecord seeds one unmonitored format record, then treats the
// response only as an acknowledgement. Chaptarr populates authors, books, and
// editions asynchronously; ensureChaptarrBookRequest resolves the authoritative
// local row after that work settles and verifies every silent-write-prone step.
func (s *Service) addChaptarrBookRecord(ctx context.Context, client *chaptarr.Client, instanceID string, match *chaptarr.LookupResult, config *bookAddConfig, title, titleSlug, mediaType string, jobID int64, selections ...*BookSelection) (string, error) {
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
	unlockAuthorSeed := func() {}
	authorSeedLocked := false
	if config.authorID == 0 {
		unlockAuthorSeed = s.lockBookAuthorSeed(instanceID, foreignAuthorID, authorName)
		authorSeedLocked = true
	}
	releaseAuthorSeed := func() {
		if authorSeedLocked {
			unlockAuthorSeed()
			authorSeedLocked = false
		}
	}
	defer releaseAuthorSeed()

	// Repeat author discovery inside the alias-aware seed lock. A concurrent
	// request may have created this author after the caller selected profiles.
	var localAuthor *chaptarr.Author
	var err error
	if config.authorID > 0 {
		localAuthor, err = client.GetAuthorContext(ctx, config.authorID)
		if err != nil {
			return "", fmt.Errorf("recheck selected book author: %w", err)
		}
	} else {
		localAuthor, err = findExistingChaptarrAuthor(ctx, client, foreignAuthorID, authorName)
		if err != nil {
			return "", err
		}
		if localAuthor != nil {
			localAuthor, err = client.GetAuthorContext(ctx, localAuthor.ID)
			if err != nil {
				return "", fmt.Errorf("load concurrently added book author: %w", err)
			}
		}
	}
	if localAuthor != nil {
		if err := pinChaptarrLookupToLocalAuthor(match, localAuthor, foreignAuthorID, authorName); err != nil {
			return "", err
		}
		freshConfig, ok := bookConfigFromAuthor(localAuthor)
		if !ok {
			return "", fmt.Errorf("%w: existing author configuration is incomplete for one or more formats", ErrBookConfigurationInvalid)
		}
		*config = freshConfig
		authorName = match.AuthorName
		foreignAuthorID = match.ForeignAuthorID
		// Once a local author is pinned, different works no longer share a seed
		// race. Release before any per-work catalog settling.
		releaseAuthorSeed()
	}
	if config.authorID == 0 {
		owned, ownerErr := s.hasDurableAuthorSeedOwner(instanceID, foreignAuthorID, authorName, jobID)
		if ownerErr != nil {
			return "", fmt.Errorf("check active author seed: %w", ownerErr)
		}
		if owned {
			return "", ErrBookOutcomePending
		}
	}

	target := chaptarrBookTargetFromLookup(match, config.authorID, title, mediaType, selections...)
	target.jobID = jobID
	if config.authorID > 0 {
		materialized, err := s.waitForMaterializedChaptarrBook(ctx, client, target)
		if err != nil {
			return "", err
		}
		if materialized {
			releaseAuthorSeed()
			status, err := s.ensureChaptarrBookRequest(ctx, client, instanceID, target)
			if err == nil {
				config.markMonitorFuture(mediaType)
			}
			return status, err
		}
	}

	seedKey := s.bookSeedOutcomeKey(instanceID, match.ForeignBookID, mediaType)
	if s.hasUncertainBookSeed(seedKey) {
		if config.authorID == 0 {
			resolvedID, resolveErr := s.resolveChaptarrAuthorID(ctx, client, nil, chaptarrBookTarget{
				foreignAuthorID: foreignAuthorID,
				authorName:      authorName,
			})
			if resolveErr != nil {
				return "", fmt.Errorf("%w: prior %s seed outcome remains unknown: %w", ErrBookMutationUnverified, mediaType, resolveErr)
			}
			config.authorID = resolvedID
		}
		resolvedAuthor, resolveErr := s.waitForChaptarrAuthor(ctx, client, config.authorID)
		if resolveErr != nil {
			return "", fmt.Errorf("%w: verify prior %s seed author: %w", ErrBookMutationUnverified, mediaType, resolveErr)
		}
		if err := pinChaptarrLookupToLocalAuthor(match, resolvedAuthor, foreignAuthorID, authorName); err != nil {
			return "", err
		}
		target.authorID = config.authorID
		target.foreignAuthorID = match.ForeignAuthorID
		target.authorName = match.AuthorName
		releaseAuthorSeed()
		status, reconcileErr := s.ensureChaptarrBookRequest(ctx, client, instanceID, target)
		if reconcileErr != nil {
			return "", fmt.Errorf("%w: prior %s seed outcome remains unknown: %w", ErrBookMutationUnverified, mediaType, reconcileErr)
		}
		s.clearUncertainBookSeed(seedKey)
		config.markMonitorFuture(mediaType)
		return status, nil
	}

	qualityProfileID, metadataProfileID, rootFolderPath := config.forFormat(mediaType)
	monitorFalse := false
	addReq := chaptarr.AddBookRequest{
		ForeignBookID:              match.ForeignBookID,
		AuthorID:                   config.authorID,
		Title:                      title,
		TitleSlug:                  titleSlug,
		Monitored:                  false,
		AnyEditionOk:               false,
		MediaType:                  mediaType,
		EbookMonitored:             &monitorFalse,
		AudiobookMonitored:         &monitorFalse,
		RootFolderPath:             rootFolderPath,
		EbookQualityProfileID:      config.ebookQualityProfileID,
		AudiobookQualityProfileID:  config.audiobookQualityProfileID,
		EbookMetadataProfileID:     config.ebookMetadataProfileID,
		AudiobookMetadataProfileID: config.audiobookMetadataProfileID,
		EbookRootFolderPath:        config.ebookRootFolderPath,
		AudiobookRootFolderPath:    config.audiobookRootFolderPath,
		Editions:                   append([]json.RawMessage(nil), match.Editions...),
	}
	if len(selections) > 0 && selections[0] != nil {
		if publication := selections[0].publication(mediaType); publication != nil {
			addReq.ForeignEditionID = publication.ForeignEditionID
		}
	}
	addReq.Author.ID = config.authorID
	addReq.Author.AuthorName = authorName
	addReq.Author.ForeignAuthorID = foreignAuthorID
	addReq.Author.QualityProfileID = qualityProfileID
	addReq.Author.MetadataProfileID = metadataProfileID
	addReq.Author.RootFolderPath = rootFolderPath
	addReq.Author.EbookQualityProfileID = config.ebookQualityProfileID
	addReq.Author.AudiobookQualityProfileID = config.audiobookQualityProfileID
	addReq.Author.EbookMetadataProfileID = config.ebookMetadataProfileID
	addReq.Author.AudiobookMetadataProfileID = config.audiobookMetadataProfileID
	addReq.Author.EbookRootFolderPath = config.ebookRootFolderPath
	addReq.Author.AudiobookRootFolderPath = config.audiobookRootFolderPath
	addReq.Author.EbookMonitorFuture = config.ebookMonitorFuture || mediaType == BookFormatEbook
	addReq.Author.AudiobookMonitorFuture = config.audiobookMonitorFuture || mediaType == BookFormatAudiobook
	addReq.Author.Monitored = true
	addReq.Author.MonitorNewItems = "none"
	addReq.Author.AddOptions.Monitor = "none"
	addReq.AddOptions.SearchForNewBook = false

	s.invalidateBookCaches(instanceID)
	defer s.invalidateBookCaches(instanceID)
	if err := s.setBookJobPhase(jobID, "seed_inflight", mediaType, config.authorID, foreignAuthorID, authorName, 0, false); err != nil {
		return "", fmt.Errorf("persist %s seed intent: %w", mediaType, err)
	}
	s.recordUncertainBookSeed(seedKey, title)
	book, err := client.AddBookContext(ctx, addReq)
	if err != nil {
		if chaptarrMutationErrorIsDefinitive(err) {
			s.clearUncertainBookSeed(seedKey)
			return "", fmt.Errorf("%w: seed %s book: %w", ErrBookMutationRejected, mediaType, err)
		}
	}
	if config.authorID == 0 {
		resolvedID, err := s.resolveChaptarrAuthorID(ctx, client, book, chaptarrBookTarget{
			foreignAuthorID: foreignAuthorID,
			authorName:      authorName,
		})
		if err != nil {
			return "", fmt.Errorf("%w: %s seed outcome remains unknown: %w", ErrBookMutationUnverified, mediaType, err)
		}
		config.authorID = resolvedID
	}
	resolvedAuthor, authorErr := s.waitForChaptarrAuthor(ctx, client, config.authorID)
	if authorErr != nil {
		return "", fmt.Errorf("%w: verify seeded %s author: %w", ErrBookMutationUnverified, mediaType, authorErr)
	}
	if err := pinChaptarrLookupToLocalAuthor(match, resolvedAuthor, foreignAuthorID, authorName); err != nil {
		return "", err
	}
	authorName = match.AuthorName
	foreignAuthorID = match.ForeignAuthorID
	target = chaptarrBookTargetFromLookup(match, config.authorID, title, mediaType, selections...)
	target.jobID = jobID
	releaseAuthorSeed()

	status, err := s.ensureChaptarrBookRequest(ctx, client, instanceID, target)
	if err == nil {
		s.clearUncertainBookSeed(seedKey)
		config.markMonitorFuture(mediaType)
	}
	return status, err
}

func pinChaptarrLookupToLocalAuthor(match *chaptarr.LookupResult, localAuthor *chaptarr.Author, expectedProviderID, expectedName string) error {
	if match == nil || localAuthor == nil || localAuthor.ID <= 0 {
		return fmt.Errorf("%w: selected author has no local identity", ErrBookMutationUnverified)
	}
	providerMatched := expectedProviderID != "" && localAuthor.ForeignAuthorID == expectedProviderID
	nameMatched := expectedName != "" && normalizeBookIdentity(localAuthor.AuthorName) == normalizeBookIdentity(expectedName)
	if !providerMatched && !nameMatched {
		return fmt.Errorf("%w: selected author identity changed", ErrBookMutationUnverified)
	}
	match.AuthorName = strings.TrimSpace(localAuthor.AuthorName)
	match.ForeignAuthorID = strings.TrimSpace(localAuthor.ForeignAuthorID)
	if match.Author != nil {
		match.Author.ID = localAuthor.ID
		match.Author.AuthorName = match.AuthorName
		match.Author.ForeignAuthorID = match.ForeignAuthorID
	}
	return nil
}

func (s *Service) bookSeedOutcomeKey(instanceID, foreignBookID, mediaType string) string {
	return instanceID + "\x00" + foreignBookID + "\x00" + mediaType
}

func (s *Service) hasUncertainBookSeed(key string) bool {
	now := time.Now()
	s.bookSeedOutcomeMu.Lock()
	defer s.bookSeedOutcomeMu.Unlock()
	seededAt, ok := s.bookUncertainSeeds[key]
	if !ok {
		return false
	}
	if now.Sub(seededAt) > defaultBookSeedOutcomeTTL {
		delete(s.bookUncertainSeeds, key)
		delete(s.bookUncertainSeedTitles, key)
		return false
	}
	return true
}

func (s *Service) hasUncertainBookSeedFor(instanceID, foreignBookID string) bool {
	prefix := instanceID + "\x00" + foreignBookID + "\x00"
	now := time.Now()
	s.bookSeedOutcomeMu.Lock()
	defer s.bookSeedOutcomeMu.Unlock()
	uncertain := false
	for key, seededAt := range s.bookUncertainSeeds {
		if now.Sub(seededAt) > defaultBookSeedOutcomeTTL {
			delete(s.bookUncertainSeeds, key)
			delete(s.bookUncertainSeedTitles, key)
			continue
		}
		if strings.HasPrefix(key, prefix) {
			uncertain = true
		}
	}
	return uncertain
}

func (s *Service) recordUncertainBookSeed(key, title string) {
	now := time.Now()
	s.bookSeedOutcomeMu.Lock()
	if s.bookUncertainSeeds == nil {
		s.bookUncertainSeeds = make(map[string]time.Time)
	}
	if s.bookUncertainSeedTitles == nil {
		s.bookUncertainSeedTitles = make(map[string]string)
	}
	for candidate, seededAt := range s.bookUncertainSeeds {
		if now.Sub(seededAt) > defaultBookSeedOutcomeTTL {
			delete(s.bookUncertainSeeds, candidate)
			delete(s.bookUncertainSeedTitles, candidate)
		}
	}
	s.bookUncertainSeeds[key] = now
	s.bookUncertainSeedTitles[key] = strings.TrimSpace(title)
	s.bookSeedOutcomeMu.Unlock()
	instanceID, _, _ := strings.Cut(key, "\x00")
	s.invalidateBookCaches(instanceID)
}

func (s *Service) clearUncertainBookSeed(key string) {
	s.bookSeedOutcomeMu.Lock()
	_, existed := s.bookUncertainSeeds[key]
	delete(s.bookUncertainSeeds, key)
	delete(s.bookUncertainSeedTitles, key)
	s.bookSeedOutcomeMu.Unlock()
	if existed {
		instanceID, _, _ := strings.Cut(key, "\x00")
		s.invalidateBookCaches(instanceID)
	}
}

func chaptarrMutationErrorIsDefinitive(err error) bool {
	var statusErr *chaptarr.HTTPStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	switch statusErr.StatusCode {
	case 400, 401, 403, 404, 405, 406, 411, 412, 413, 414, 415, 422:
		return true
	default:
		// Conflicts, throttling/timeouts, and every 5xx can follow a committed
		// POST, so they remain guarded until live state reconciles.
		return false
	}
}

func chaptarrVerifiedMutationError(step string, err error) error {
	if chaptarrMutationErrorIsDefinitive(err) {
		return fmt.Errorf("%w: %s: %w", ErrBookMutationRejected, step, err)
	}
	return fmt.Errorf("%w: %s: %w", ErrBookMutationUnverified, step, err)
}

func (s *Service) resolveChaptarrAuthorID(ctx context.Context, client *chaptarr.Client, added *chaptarr.Book, target chaptarrBookTarget) (int, error) {
	if added != nil {
		if added.AuthorID > 0 {
			return added.AuthorID, nil
		}
		if added.Author != nil && added.Author.ID > 0 {
			return added.Author.ID, nil
		}
	}

	interval := s.bookSettleInterval
	if interval <= 0 {
		interval = defaultBookSettleInterval
	}
	for {
		authors, err := client.GetAllAuthorsContext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return 0, fmt.Errorf("%w: author did not become available", ErrBookCatalogPending)
			}
			return 0, fmt.Errorf("resolve added book author: %w", err)
		}
		matches := make([]chaptarr.Author, 0, 1)
		for _, author := range authors {
			if target.foreignAuthorID != "" && author.ForeignAuthorID == target.foreignAuthorID {
				matches = append(matches, author)
			}
		}
		if len(matches) == 0 && target.authorName != "" {
			for _, author := range authors {
				if normalizeBookIdentity(author.AuthorName) == normalizeBookIdentity(target.authorName) {
					matches = append(matches, author)
				}
			}
		}
		if len(matches) == 1 && matches[0].ID > 0 {
			return matches[0].ID, nil
		}
		if len(matches) > 1 {
			return 0, fmt.Errorf("%w: added author identity is ambiguous", ErrBookMutationUnverified)
		}
		if err := waitForBookPoll(ctx, interval); err != nil {
			return 0, fmt.Errorf("%w: author did not become available", ErrBookCatalogPending)
		}
	}
}

func (s *Service) waitForChaptarrAuthor(ctx context.Context, client *chaptarr.Client, authorID int) (*chaptarr.Author, error) {
	if authorID <= 0 {
		return nil, fmt.Errorf("%w: author is unresolved", ErrBookMutationUnverified)
	}
	interval := s.bookSettleInterval
	if interval <= 0 {
		interval = defaultBookSettleInterval
	}
	for {
		author, err := client.GetAuthorContext(ctx, authorID)
		if err == nil && author != nil && author.ID == authorID {
			return author, nil
		}
		if err != nil && bookUpstreamAuthFailure(err) {
			return nil, fmt.Errorf("read added book author: %w", err)
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: author did not become readable", ErrBookCatalogPending)
		}
		if err := waitForBookPoll(ctx, interval); err != nil {
			return nil, fmt.Errorf("%w: author did not become readable", ErrBookCatalogPending)
		}
	}
}

// ensureChaptarrBookRequest is deliberately convergent: retries inspect every
// matching pocket, preserve a valid existing edition choice, repair partial
// state, and avoid a duplicate search while an exact command or recent local
// acknowledgement proves that this row was already submitted.
func (s *Service) ensureChaptarrBookRequest(ctx context.Context, client *chaptarr.Client, instanceID string, target chaptarrBookTarget) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		status, err := s.ensureChaptarrBookRequestOnce(ctx, client, instanceID, target)
		if err == nil {
			s.clearUncertainBookSeed(s.bookSeedOutcomeKey(instanceID, target.foreignBookID, target.mediaType))
			return status, nil
		}
		lastErr = err
		if !errors.Is(err, ErrBookMutationUnverified) || ctx.Err() != nil {
			return "", err
		}
		interval := s.bookSettleInterval
		if interval <= 0 {
			interval = defaultBookSettleInterval
		}
		if err := waitForBookPoll(ctx, interval); err != nil {
			break
		}
	}
	return "", lastErr
}

func (s *Service) ensureChaptarrBookRequestOnce(ctx context.Context, client *chaptarr.Client, instanceID string, target chaptarrBookTarget) (string, error) {
	// Clear both before and after the convergence attempt. A status request can
	// repopulate the short-lived projection while this bounded workflow is still
	// in flight; the deferred clear prevents that stale snapshot from masking a
	// successful, failed, or outcome-unknown mutation.
	s.invalidateBookCaches(instanceID)
	defer s.invalidateBookCaches(instanceID)
	candidates, commands, err := s.waitForChaptarrBookCandidates(ctx, client, instanceID, target)
	if err != nil {
		return "", err
	}
	// Request evidence is aggregated across every duplicate pocket before one
	// row is ranked for repair. Chaptarr can place the useful edition in one
	// pocket while a sibling pocket already owns the file, grab, or exact search;
	// never launch a duplicate merely because the evidence is not on the row we
	// would otherwise prefer to mutate.
	for i := range candidates {
		candidate := &candidates[i]
		if chaptarrBookAvailable(candidate.book) {
			s.clearUncertainBookSearch(instanceID, candidate.book.ID)
			return StatusAvailable, nil
		}
		if candidate.book.Grabbed || s.hasRecentBookSearchAck(instanceID, candidate.book.ID) {
			s.clearUncertainBookSearch(instanceID, candidate.book.ID)
			return StatusRequested, nil
		}
	}
	selected := selectChaptarrBookCandidate(candidates, target.mediaType)
	if selected == nil {
		if chaptarrAnyCandidateBookSearchActive(commands, candidates) {
			for _, candidate := range candidates {
				if chaptarrExactBookSearchActive(commands, candidate.book.ID) {
					s.recordUncertainBookSearch(instanceID, candidate.book.ID)
				}
			}
			return "", fmt.Errorf("%w: exact book search is active while the requested edition settles", ErrBookOutcomePending)
		}
		return "", fmt.Errorf("%w: %s", ErrBookEditionUnavailable, target.mediaType)
	}
	if chaptarrBookAvailable(selected.book) {
		return StatusAvailable, nil
	}
	selected, err = readChaptarrBookCandidate(ctx, client, selected.book.ID, target, selected.identityTier, selected.editionIdentityTier)
	if err != nil {
		return "", err
	}
	if chaptarrBookAvailable(selected.book) {
		return StatusAvailable, nil
	}
	if chosen, chooseErr := chooseChaptarrEditionForTarget(selected.usable, target); chooseErr != nil {
		return "", chooseErr
	} else if chosen == nil {
		return "", fmt.Errorf("%w: %s", ErrBookEditionUnavailable, target.mediaType)
	}
	authorMutationLock := s.bookAuthorMutationLock(instanceID + "\x00" + fmt.Sprintf("%d", target.authorID))
	authorMutationLock.Lock()
	authorMutationLocked := true
	releaseAuthorMutation := func() {
		if authorMutationLocked {
			authorMutationLock.Unlock()
			authorMutationLocked = false
		}
	}
	defer releaseAuthorMutation()

	author, err := client.GetAuthorContext(ctx, target.authorID)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%w: author did not become available", ErrBookCatalogPending)
		}
		return "", fmt.Errorf("read selected book author: %w", err)
	}
	if !chaptarrAuthorMatches(*author, target) {
		return "", fmt.Errorf("%w: author identity changed", ErrBookMutationUnverified)
	}
	// Existing library targets may initially carry only a local author ID. Bind
	// the durable phase to the authoritative provider/name identity before the
	// first author or book write so restart and instance-drift recovery never
	// has to trust an endpoint-local integer by itself.
	if target.foreignAuthorID == "" {
		target.foreignAuthorID = strings.TrimSpace(author.ForeignAuthorID)
	}
	if target.authorName == "" {
		target.authorName = strings.TrimSpace(author.AuthorName)
	}
	if err := s.setBookJobPhase(target.jobID, "converging", target.mediaType, target.authorID, target.foreignAuthorID, target.authorName, selected.book.ID, false); err != nil {
		return "", fmt.Errorf("persist %s convergence: %w", target.mediaType, err)
	}
	if !chaptarrAuthorGate(*author, target.mediaType) || !author.Monitored {
		author.Monitored = true
		if target.mediaType == BookFormatAudiobook {
			author.AudiobookMonitorFuture = true
		} else {
			author.EbookMonitorFuture = true
		}
		s.invalidateBookCaches(instanceID)
		if _, err := client.UpdateAuthorContext(ctx, *author); err != nil {
			return "", chaptarrVerifiedMutationError("enable "+target.mediaType+" author monitoring", err)
		}
		author, err = client.GetAuthorContext(ctx, target.authorID)
		if err != nil {
			return "", fmt.Errorf("%w: verify selected book author: %w", ErrBookMutationUnverified, err)
		}
		if !chaptarrAuthorMatches(*author, target) || !author.Monitored || !chaptarrAuthorGate(*author, target.mediaType) {
			return "", fmt.Errorf("%w: %s author monitoring", ErrBookMutationUnverified, target.mediaType)
		}
	}
	releaseAuthorMutation()

	// Author writes can themselves overlap provider activity. Re-read the exact
	// full resource and authoritative editions once more immediately before the
	// book PUT so the selected work cannot switch after the gate check.
	fresh, err := readChaptarrBookCandidate(ctx, client, selected.book.ID, target, selected.identityTier, selected.editionIdentityTier)
	if err != nil {
		return "", err
	}
	selected = fresh
	if chaptarrBookAvailable(selected.book) {
		return StatusAvailable, nil
	}
	chosen, chooseErr := chooseChaptarrEditionForTarget(selected.usable, target)
	if chooseErr != nil {
		return "", chooseErr
	}
	if chosen == nil {
		return "", fmt.Errorf("%w: %s", ErrBookEditionUnavailable, target.mediaType)
	}
	alreadyVerified := chaptarrBookSelectionVerified(selected.book, selected.editions, chosen.ID, target, selected.identityTier, selected.editionIdentityTier)
	activeSearch := chaptarrAnyCandidateBookSearchActive(commands, candidates)
	if alreadyVerified && (selected.book.Grabbed || activeSearch || s.hasRecentBookSearchAck(instanceID, selected.book.ID)) {
		return StatusRequested, nil
	}
	if alreadyVerified {
		durable, historyErr := s.hasDurableVerifiedBookRequest(instanceID, target.foreignBookID, target.mediaType, bookSelectionFromTarget(target))
		if historyErr != nil {
			return "", historyErr
		}
		if durable {
			return StatusRequested, nil
		}
	}

	bookForUpdate := selected.book
	bookForUpdate.AnyEditionOk = false
	bookForUpdate.EbookMonitored = target.mediaType == BookFormatEbook
	bookForUpdate.AudiobookMonitored = target.mediaType == BookFormatAudiobook
	bookForUpdate.Editions = append([]chaptarr.Edition(nil), selected.editions...)
	for i := range bookForUpdate.Editions {
		bookForUpdate.Editions[i].Monitored = bookForUpdate.Editions[i].ID == chosen.ID
		bookForUpdate.Editions[i].ManualAdd = bookForUpdate.Editions[i].ID == chosen.ID
	}
	s.invalidateBookCaches(instanceID)
	if _, err := client.UpdateBookContext(ctx, bookForUpdate); err != nil {
		return "", chaptarrVerifiedMutationError("select "+target.mediaType+" edition", err)
	}
	if err := client.SetBookMonitoredContext(ctx, []int{selected.book.ID}, true); err != nil {
		return "", chaptarrVerifiedMutationError("monitor selected "+target.mediaType+" book", err)
	}

	verifiedBook, err := client.GetBookContext(ctx, selected.book.ID)
	if err != nil {
		return "", fmt.Errorf("%w: verify selected %s book: %w", ErrBookMutationUnverified, target.mediaType, err)
	}
	verifiedEditions, err := client.GetEditionsContext(ctx, selected.book.ID)
	if err != nil {
		return "", fmt.Errorf("%w: verify selected %s edition: %w", ErrBookMutationUnverified, target.mediaType, err)
	}
	verifiedAuthor, err := client.GetAuthorContext(ctx, target.authorID)
	if err != nil {
		return "", fmt.Errorf("%w: verify selected book author gate: %w", ErrBookMutationUnverified, err)
	}
	if !chaptarrAuthorMatches(*verifiedAuthor, target) || !verifiedAuthor.Monitored || !chaptarrAuthorGate(*verifiedAuthor, target.mediaType) ||
		!chaptarrBookSelectionVerified(*verifiedBook, verifiedEditions, chosen.ID, target, selected.identityTier, selected.editionIdentityTier) {
		return "", fmt.Errorf("%w: %s book or edition read-back", ErrBookMutationUnverified, target.mediaType)
	}
	if chaptarrBookAvailable(*verifiedBook) {
		s.clearUncertainBookSearch(instanceID, selected.book.ID)
		return StatusAvailable, nil
	}
	if verifiedBook.Grabbed {
		s.clearUncertainBookSearch(instanceID, selected.book.ID)
		return StatusRequested, nil
	}
	if s.hasRecentBookSearchAck(instanceID, selected.book.ID) {
		return StatusRequested, nil
	}
	durable, err := s.hasDurableVerifiedBookRequest(instanceID, target.foreignBookID, target.mediaType, bookSelectionFromTarget(target))
	if err != nil {
		return "", err
	}
	if durable {
		return StatusRequested, nil
	}
	latestCommands, err := client.GetCommandsContext(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: verify existing book search: %w", ErrBookSearchUnconfirmed, err)
	}
	if chaptarrAnyCandidateBookSearchActive(latestCommands, candidates) {
		s.clearUncertainBookSearch(instanceID, selected.book.ID)
		return StatusRequested, nil
	}
	if s.hasUncertainBookSearch(instanceID, selected.book.ID) {
		return "", fmt.Errorf("%w: prior search outcome remains unknown", ErrBookSearchUnconfirmed)
	}
	if err := s.setBookJobPhase(target.jobID, "search_inflight", target.mediaType, target.authorID, target.foreignAuthorID, target.authorName, selected.book.ID, false); err != nil {
		return "", fmt.Errorf("persist %s search intent: %w", target.mediaType, err)
	}
	s.recordUncertainBookSearch(instanceID, selected.book.ID)
	command, err := client.QueueBookSearchContext(ctx, []int{selected.book.ID})
	if err != nil {
		if chaptarrMutationErrorIsDefinitive(err) {
			s.clearUncertainBookSearch(instanceID, selected.book.ID)
			return "", fmt.Errorf("%w: %w", ErrBookSearchRejected, err)
		}
		return "", fmt.Errorf("%w: %w", ErrBookSearchUnconfirmed, err)
	}
	if command == nil || !command.Acknowledges() || !strings.EqualFold(command.EffectiveName(), "BookSearch") {
		return "", fmt.Errorf("%w: invalid command acknowledgement", ErrBookSearchUnconfirmed)
	}
	s.clearUncertainBookSearch(instanceID, selected.book.ID)
	s.recordBookSearchAck(instanceID, selected.book.ID)
	if err := s.setBookJobPhase(target.jobID, "search_inflight", target.mediaType, target.authorID, target.foreignAuthorID, target.authorName, selected.book.ID, true); err != nil {
		return "", fmt.Errorf("persist %s search acknowledgement: %w", target.mediaType, err)
	}
	return StatusRequested, nil
}

func (s *Service) hasDurableVerifiedBookRequest(instanceID, foreignBookID, format string, selections ...*BookSelection) (bool, error) {
	if s.db == nil || s.registry == nil || instanceID == "" || foreignBookID == "" || !validBookFormat(format) || format == BookFormatBoth {
		return false, nil
	}
	_, fingerprint, err := s.registry.GetFreshChaptarrClient(instanceID)
	if err != nil {
		return false, fmt.Errorf("bind durable verified book request: %w", err)
	}
	var selection *BookSelection
	if len(selections) > 0 {
		selection = selections[0]
	}
	rows, err := s.db.Query(
		`SELECT COALESCE(book_format, 'both'), COALESCE(book_selection_json, '') FROM request_log
		 WHERE media_type = 'book' AND foreign_id = ?
		   AND COALESCE(instance_id, '') = ?
		   AND (COALESCE(book_format, 'both') = ? OR COALESCE(book_format, 'both') = 'both')
		   AND status = ? AND book_settings_fingerprint = ?
		 ORDER BY id DESC`,
		foreignBookID, instanceID, format, StatusRequested, fingerprint[:],
	)
	if err != nil {
		return false, fmt.Errorf("read durable verified book request: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var storedFormat, selectionJSON string
		if err := rows.Scan(&storedFormat, &selectionJSON); err != nil {
			return false, fmt.Errorf("scan durable verified book request: %w", err)
		}
		if selection == nil {
			return true, nil
		}
		storedSelection, decodeErr := requireDecodedBookSelection(selectionJSON, normalizeBookFormat(storedFormat))
		if decodeErr != nil {
			return false, decodeErr
		}
		if bookSelectionsEquivalent(storedSelection, selection, format) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func readChaptarrBookCandidate(ctx context.Context, client *chaptarr.Client, bookID int, target chaptarrBookTarget, requiredBookTier, requiredEditionTier int) (*chaptarrBookCandidate, error) {
	book, err := client.GetBookContext(ctx, bookID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %s catalog did not settle", ErrBookCatalogPending, target.mediaType)
		}
		return nil, fmt.Errorf("re-resolve selected %s book: %w", target.mediaType, err)
	}
	bookTier := chaptarrBookIdentityTier(*book, target)
	if bookTier != requiredBookTier || chaptarrTitleIsMultiWork(book.Title) || !chaptarrBookResolved(*book) {
		return nil, fmt.Errorf("%w: selected book identity changed", ErrBookMutationUnverified)
	}
	editions, err := client.GetEditionsContext(ctx, bookID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %s catalog did not settle", ErrBookCatalogPending, target.mediaType)
		}
		return nil, fmt.Errorf("re-resolve selected %s editions: %w", target.mediaType, err)
	}
	usable, editionTier, editionMultiWork, selectionErr := selectChaptarrEditionsForTarget(editions, *book, target)
	if selectionErr != nil {
		return nil, selectionErr
	}
	if editionMultiWork {
		return nil, ErrBookMultiWorkUnsupported
	}
	if editionTier != requiredEditionTier || len(usable) == 0 {
		return nil, fmt.Errorf("%w: selected %s edition changed", ErrBookMutationUnverified, target.mediaType)
	}
	candidate := &chaptarrBookCandidate{
		book: *book, editions: editions, usable: usable, identityTier: bookTier, editionIdentityTier: editionTier,
	}
	return candidate, nil
}

func (s *Service) waitForChaptarrBookCandidates(ctx context.Context, client *chaptarr.Client, instanceID string, target chaptarrBookTarget) ([]chaptarrBookCandidate, []chaptarr.Command, error) {
	if target.authorID <= 0 {
		return nil, nil, fmt.Errorf("%w: author is unresolved", ErrBookMutationUnverified)
	}
	interval := s.bookSettleInterval
	if interval <= 0 {
		interval = defaultBookSettleInterval
	}
	lastFingerprint := ""
	stableSamples := 0
	for {
		books, err := client.GetBooksContext(ctx, target.authorID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, fmt.Errorf("%w: %s catalog did not settle", ErrBookCatalogPending, target.mediaType)
			}
			return nil, nil, fmt.Errorf("read Chaptarr catalog: %w", err)
		}
		// Keep every compatible pocket in the stable catalog snapshot. Identity
		// tier is a mutation-ranking signal, not an evidence filter: Chaptarr can
		// put the exact title on one row while a compatible subtitle row carries
		// the requested-format edition, file, grab, or active search.
		candidates := make([]chaptarrBookCandidate, 0, len(books))
		for _, book := range books {
			if book.ForeignBookID == target.foreignBookID && !bookTitlesMatch(book.Title, target.title) {
				return nil, nil, fmt.Errorf("%w: provider id identifies conflicting works", ErrBookMutationUnverified)
			}
			identityTier := chaptarrBookIdentityTier(book, target)
			if identityTier == 0 {
				continue
			}
			editions, err := client.GetEditionsContext(ctx, book.ID)
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, fmt.Errorf("%w: %s catalog did not settle", ErrBookCatalogPending, target.mediaType)
				}
				return nil, nil, fmt.Errorf("read Chaptarr editions: %w", err)
			}
			usable, editionTier, editionMultiWork, selectionErr := selectChaptarrEditionsForTarget(editions, book, target)
			if selectionErr != nil {
				return nil, nil, selectionErr
			}
			candidate := chaptarrBookCandidate{
				book: book, editions: editions, usable: usable, identityTier: identityTier,
				editionIdentityTier: editionTier, editionMultiWork: editionMultiWork,
			}
			candidates = append(candidates, candidate)
		}
		for _, candidate := range candidates {
			if chaptarrTitleIsMultiWork(candidate.book.Title) {
				return nil, nil, ErrBookMultiWorkUnsupported
			}
		}
		hasSafeEdition := false
		hasMultiWorkEdition := false
		for _, candidate := range candidates {
			if len(candidate.usable) == 0 {
				continue
			}
			if candidate.editionMultiWork {
				hasMultiWorkEdition = true
			} else {
				hasSafeEdition = true
			}
		}
		// Any requestable single-work edition beats a bundle, even when its
		// optional title is blank and therefore has a lower identity tier. Ranking
		// must never let a better-named bundle poison a safe duplicate pocket.
		if !hasSafeEdition && hasMultiWorkEdition {
			return nil, nil, ErrBookMultiWorkUnsupported
		}
		if hasSafeEdition && hasMultiWorkEdition {
			safe := candidates[:0]
			for _, candidate := range candidates {
				if !candidate.editionMultiWork {
					safe = append(safe, candidate)
				}
			}
			candidates = safe
		}
		commands, err := client.GetCommandsContext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, fmt.Errorf("%w: %s catalog did not settle", ErrBookCatalogPending, target.mediaType)
			}
			return nil, nil, fmt.Errorf("read Chaptarr catalog commands: %w", err)
		}

		fingerprint := chaptarrCandidatesFingerprint(candidates)
		if fingerprint == "" || chaptarrCatalogCommandActive(commands, target.authorID) {
			stableSamples = 0
			lastFingerprint = ""
		} else if fingerprint == lastFingerprint {
			stableSamples++
		} else {
			lastFingerprint = fingerprint
			stableSamples = 1
		}
		if stableSamples >= 2 {
			requestCandidates := candidates
			if target.publication != nil {
				requestCandidates = make([]chaptarrBookCandidate, 0, len(candidates))
				for _, candidate := range candidates {
					if len(candidate.usable) == 1 {
						requestCandidates = append(requestCandidates, candidate)
					}
				}
			}
			if chaptarrProviderlessCompatibleCandidatesAmbiguous(requestCandidates) {
				return nil, nil, fmt.Errorf("%w: provider-less compatible book identity is ambiguous", ErrBookMutationUnverified)
			}
			if selectChaptarrBookCandidate(requestCandidates, target.mediaType) != nil {
				return requestCandidates, commands, nil
			}
			for _, candidate := range requestCandidates {
				if chaptarrBookAvailable(candidate.book) || candidate.book.Grabbed ||
					chaptarrExactBookSearchActive(commands, candidate.book.ID) ||
					s.hasRecentBookSearchAck(instanceID, candidate.book.ID) {
					return requestCandidates, commands, nil
				}
			}
			// A complete, stable row with authoritative editions of only other
			// formats is a real format miss. In particular, Physical is never an
			// audiobook fallback. Empty editions and placeholders may still be
			// catalog work in progress and continue polling to the deadline.
			allPocketsComplete := len(candidates) > 0
			for _, candidate := range candidates {
				if !chaptarrBookResolved(candidate.book) || len(candidate.editions) == 0 {
					allPocketsComplete = false
					break
				}
			}
			if allPocketsComplete {
				if target.publication != nil {
					return nil, nil, fmt.Errorf("%w: selected %s publication is no longer present", ErrBookMatchNotFound, target.mediaType)
				}
				return nil, nil, fmt.Errorf("%w: %s", ErrBookEditionUnavailable, target.mediaType)
			}
		}

		if err := waitForBookPoll(ctx, interval); err != nil {
			return nil, nil, fmt.Errorf("%w: %s catalog did not settle", ErrBookCatalogPending, target.mediaType)
		}
	}
}

func chaptarrProviderlessCompatibleCandidatesAmbiguous(candidates []chaptarrBookCandidate) bool {
	compatibleTitles := make(map[string]bool)
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.book.ForeignBookID) != "" || candidate.identityTier != 11 {
			continue
		}
		compatibleTitles[normalizeBookIdentity(candidate.book.Title)] = true
	}
	return len(compatibleTitles) > 1
}

func waitForBookPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func chaptarrRecordAuthorID(records []chaptarr.Book) (int, error) {
	authorID := 0
	for _, record := range records {
		candidate := record.AuthorID
		if candidate == 0 && record.Author != nil {
			candidate = record.Author.ID
		}
		if candidate <= 0 {
			return 0, fmt.Errorf("%w: existing book author is unresolved", ErrBookMutationUnverified)
		}
		if authorID != 0 && authorID != candidate {
			return 0, fmt.Errorf("existing canonical book records disagree on author")
		}
		authorID = candidate
	}
	if authorID == 0 {
		return 0, fmt.Errorf("%w: existing book author is unresolved", ErrBookMutationUnverified)
	}
	return authorID, nil
}

func chaptarrPhysicalAndUnsafeRecords(books []chaptarr.Book, foreignBookID string) (physical []chaptarr.Book, unsafe bool) {
	for _, book := range books {
		if book.ForeignBookID != foreignBookID || recordFormat(book) != chaptarr.FormatUnknown {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(book.MediaType)) {
		case "physical", "paperback", "hardcover", "print":
			physical = append(physical, book)
		default:
			unsafe = true
		}
	}
	return physical, unsafe
}

func chaptarrBookMatchesTarget(book chaptarr.Book, target chaptarrBookTarget) bool {
	return chaptarrBookIdentityTier(book, target) > 0
}

// chaptarrBookIdentityTier is deliberately lexicographic: an explicit exact
// provider id outranks a temporarily missing id, then an exact normalized title
// outranks a compatible subtitle/base-title variant. Candidate polling retains
// every compatible pocket for file/search evidence and catalog stability; this
// tier only decides which single safe row wins mutation ranking.
func chaptarrBookIdentityTier(book chaptarr.Book, target chaptarrBookTarget) int {
	if book.ID <= 0 || strings.ToLower(strings.TrimSpace(book.MediaType)) != target.mediaType {
		return 0
	}
	authorID := book.AuthorID
	if authorID == 0 && book.Author != nil {
		authorID = book.Author.ID
	}
	if target.authorID > 0 && authorID != target.authorID {
		return 0
	}
	providerTier := 1
	if target.foreignBookID != "" {
		switch {
		case book.ForeignBookID == target.foreignBookID:
			providerTier = 2
		case strings.TrimSpace(book.ForeignBookID) == "":
			providerTier = 1
		default:
			return 0
		}
	}
	titleTier := 0
	if normalizeBookIdentity(book.Title) == normalizeBookIdentity(target.title) {
		titleTier = 2
	} else if bookTitlesMatch(book.Title, target.title) {
		titleTier = 1
	}
	if titleTier == 0 {
		return 0
	}
	return providerTier*10 + titleTier
}

func chaptarrBookResolved(book chaptarr.Book) bool {
	foreignEditionID := strings.ToLower(strings.TrimSpace(book.ForeignEditionID))
	return book.ReleaseDate != nil && len(book.Images) > 0 && foreignEditionID != "" && !strings.HasPrefix(foreignEditionID, "default-")
}

func chaptarrBookAvailable(book chaptarr.Book) bool {
	if book.HasFiles || book.Statistics.BookFileCount > 0 {
		return true
	}
	if strings.EqualFold(book.MediaType, BookFormatAudiobook) {
		return book.AudiobookStatistics.BookFileCount > 0
	}
	return book.EbookStatistics.BookFileCount > 0
}

func chaptarrEditionFormat(edition chaptarr.Edition) string {
	if format := strictChaptarrFormat(edition.Format); format != chaptarr.FormatUnknown {
		return format
	}
	return ""
}

func selectChaptarrBookCandidate(candidates []chaptarrBookCandidate, mediaType string) *chaptarrBookCandidate {
	best := -1
	for i := range candidates {
		candidate := &candidates[i]
		if !chaptarrBookResolved(candidate.book) || len(candidate.usable) == 0 {
			continue
		}
		if best == -1 || chaptarrCandidateBetter(*candidate, candidates[best], mediaType) {
			best = i
		}
	}
	if best == -1 {
		return nil
	}
	return &candidates[best]
}

func chaptarrCandidateBetter(left, right chaptarrBookCandidate, mediaType string) bool {
	if left.identityTier != right.identityTier {
		return left.identityTier > right.identityTier
	}
	if left.editionIdentityTier != right.editionIdentityTier {
		return left.editionIdentityTier > right.editionIdentityTier
	}
	// A full-book PUT can persist the exact edition before /book/monitor fails.
	// Prefer that partial selection on retry even while the book-level monitor
	// flag is still false, then repair the same pocket instead of selecting a
	// second duplicate row.
	leftSelected := countMonitoredChaptarrEditions(left.editions, mediaType) == 1
	rightSelected := countMonitoredChaptarrEditions(right.editions, mediaType) == 1
	if leftSelected != rightSelected {
		return leftSelected
	}
	if len(left.usable) != len(right.usable) {
		return len(left.usable) > len(right.usable)
	}
	if chaptarrBookAvailable(left.book) != chaptarrBookAvailable(right.book) {
		return chaptarrBookAvailable(left.book)
	}
	if left.book.Grabbed != right.book.Grabbed {
		return left.book.Grabbed
	}
	if left.book.Ratings.Popularity != right.book.Ratings.Popularity {
		return left.book.Ratings.Popularity > right.book.Ratings.Popularity
	}
	if left.book.Ratings.Votes != right.book.Ratings.Votes {
		return left.book.Ratings.Votes > right.book.Ratings.Votes
	}
	return left.book.ID < right.book.ID
}

func selectChaptarrEditions(editions []chaptarr.Edition, targetTitle, mediaType string) ([]chaptarr.Edition, int, bool) {
	bestSafeTier := 0
	bestUnsafeTier := 0
	safe := make([]chaptarr.Edition, 0, len(editions))
	unsafe := make([]chaptarr.Edition, 0, len(editions))
	for _, edition := range editions {
		if edition.ID <= 0 || chaptarrEditionFormat(edition) != mediaType {
			continue
		}
		tier := 0
		if strings.TrimSpace(edition.Title) == "" {
			tier = 1 // Chaptarr permits title-less local editions.
		} else if titleTier := bookTitleIdentityTier(edition.Title, targetTitle); titleTier > 0 {
			tier = titleTier + 1 // titled compatible/exact editions outrank unknown.
		}
		if tier == 0 {
			continue
		}
		if chaptarrTitleIsMultiWork(edition.Title) {
			if tier < bestUnsafeTier {
				continue
			}
			if tier > bestUnsafeTier {
				bestUnsafeTier = tier
				unsafe = unsafe[:0]
			}
			unsafe = append(unsafe, edition)
			continue
		}
		if tier < bestSafeTier {
			continue
		}
		if tier > bestSafeTier {
			bestSafeTier = tier
			safe = safe[:0]
		}
		safe = append(safe, edition)
	}
	if len(safe) > 0 {
		return safe, bestSafeTier, false
	}
	return unsafe, bestUnsafeTier, len(unsafe) > 0
}

func chooseChaptarrEdition(editions []chaptarr.Edition, targetTitle, mediaType string) (*chaptarr.Edition, error) {
	matching, _, multiWork := selectChaptarrEditions(editions, targetTitle, mediaType)
	if multiWork {
		return nil, ErrBookMultiWorkUnsupported
	}
	best := -1
	bestScore := -1
	for i := range matching {
		edition := matching[i]
		score := 0
		if edition.Monitored {
			score += 100
		}
		if strings.TrimSpace(edition.ForeignEditionID) != "" {
			score += 8
		}
		if strings.TrimSpace(edition.Language) != "" {
			score += 4
		}
		if strings.TrimSpace(edition.ISBN13) != "" || strings.TrimSpace(edition.ASIN) != "" {
			score += 2
		}
		if strings.TrimSpace(edition.Title) != "" {
			score++
		}
		// Equal scores retain the authoritative response order. Local IDs are
		// allocation artifacts and must not become an invented preference.
		if best == -1 || score > bestScore {
			best = i
			bestScore = score
		}
	}
	if best == -1 {
		return nil, nil
	}
	return &matching[best], nil
}

func chooseChaptarrEditionForTarget(editions []chaptarr.Edition, target chaptarrBookTarget) (*chaptarr.Edition, error) {
	if target.publication != nil {
		if len(editions) > 1 {
			return nil, fmt.Errorf("%w: selected publication is ambiguous", ErrBookMatchNotFound)
		}
		if len(editions) == 0 {
			return nil, nil
		}
		chosen := editions[0]
		return &chosen, nil
	}
	return chooseChaptarrEdition(editions, target.title, target.mediaType)
}

func countMonitoredChaptarrEditions(editions []chaptarr.Edition, mediaType string) int {
	count := 0
	for _, edition := range editions {
		if edition.Monitored && chaptarrEditionFormat(edition) == mediaType {
			count++
		}
	}
	return count
}

func chaptarrBookSelectionVerified(book chaptarr.Book, editions []chaptarr.Edition, editionID int, target chaptarrBookTarget, requiredBookTier, requiredEditionTier int) bool {
	if chaptarrBookIdentityTier(book, target) != requiredBookTier || chaptarrTitleIsMultiWork(book.Title) ||
		!chaptarrBookResolved(book) || !book.Monitored || book.AnyEditionOk {
		return false
	}
	usable, editionTier, multiWork, selectionErr := selectChaptarrEditionsForTarget(editions, book, target)
	if selectionErr != nil || multiWork || editionTier != requiredEditionTier {
		return false
	}
	chosenIdentity := false
	for _, edition := range usable {
		if edition.ID == editionID {
			chosenIdentity = true
			break
		}
	}
	if !chosenIdentity {
		return false
	}
	if target.mediaType == BookFormatAudiobook {
		if !book.AudiobookMonitored {
			return false
		}
	} else if !book.EbookMonitored {
		return false
	}
	monitored := 0
	chosen := false
	for _, edition := range editions {
		if !edition.Monitored {
			continue
		}
		monitored++
		if edition.ID == editionID && chaptarrEditionFormat(edition) == target.mediaType {
			chosen = true
		}
	}
	return monitored == 1 && chosen
}

func chaptarrAuthorMatches(author chaptarr.Author, target chaptarrBookTarget) bool {
	if author.ID != target.authorID {
		return false
	}
	if target.foreignAuthorID != "" && author.ForeignAuthorID != "" {
		if author.ForeignAuthorID == target.foreignAuthorID {
			return true
		}
	}
	if target.authorName != "" {
		return normalizeBookIdentity(author.AuthorName) == normalizeBookIdentity(target.authorName)
	}
	return true
}

func chaptarrAuthorGate(author chaptarr.Author, mediaType string) bool {
	if mediaType == BookFormatAudiobook {
		return author.AudiobookMonitorFuture
	}
	return author.EbookMonitorFuture
}

func chaptarrCandidatesFingerprint(candidates []chaptarrBookCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	sorted := append([]chaptarrBookCandidate(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].book.ID < sorted[j].book.ID })
	var out strings.Builder
	for _, candidate := range sorted {
		fmt.Fprintf(&out, "b:%d:%d:%s:%s:%s:%s:%d:%d:%t:%t:%t:%t:%t:%t:%t:%t:%d:%d:%f:%d|",
			candidate.book.ID,
			candidate.book.AuthorID,
			candidate.book.ForeignBookID,
			candidate.book.ForeignEditionID,
			candidate.book.MediaType,
			normalizeBookIdentity(candidate.book.Title),
			candidate.identityTier,
			candidate.editionIdentityTier,
			candidate.editionMultiWork,
			candidate.book.ReleaseDate != nil,
			len(candidate.book.Images) > 0,
			candidate.book.Monitored,
			candidate.book.EbookMonitored,
			candidate.book.AudiobookMonitored,
			candidate.book.HasFiles,
			candidate.book.Grabbed,
			candidate.book.Statistics.BookFileCount,
			candidate.book.EbookStatistics.BookFileCount+candidate.book.AudiobookStatistics.BookFileCount,
			candidate.book.Ratings.Popularity,
			candidate.book.Ratings.Votes,
		)
		editions := append([]chaptarr.Edition(nil), candidate.editions...)
		sort.Slice(editions, func(i, j int) bool { return editions[i].ID < editions[j].ID })
		for _, edition := range editions {
			fmt.Fprintf(&out, "e:%d:%s:%s:%s:%s:%s:%s:%t:%t|",
				edition.ID,
				edition.ForeignEditionID,
				edition.Format,
				normalizeBookIdentity(edition.Title),
				strings.TrimSpace(edition.Language),
				strings.TrimSpace(edition.ISBN13),
				strings.TrimSpace(edition.ASIN),
				edition.Monitored,
				edition.ManualAdd,
			)
		}
	}
	return out.String()
}

func chaptarrCommandActive(command chaptarr.Command) bool {
	switch strings.ToLower(strings.TrimSpace(command.Status)) {
	case "queued", "started", "running":
		return true
	default:
		return false
	}
}

func chaptarrCatalogCommandActive(commands []chaptarr.Command, authorID int) bool {
	for _, command := range commands {
		if !chaptarrCommandActive(command) {
			continue
		}
		name := strings.ToLower(command.EffectiveName())
		catalogMutation := strings.Contains(name, "refresh") && (strings.Contains(name, "author") || strings.Contains(name, "book"))
		catalogMutation = catalogMutation || strings.Contains(name, "rescan") && (strings.Contains(name, "author") || strings.Contains(name, "book"))
		if !catalogMutation {
			continue
		}
		if command.Body.AuthorID > 0 {
			if command.Body.AuthorID == authorID {
				return true
			}
			continue
		}
		if len(command.Body.AuthorIDs) > 0 {
			for _, id := range command.Body.AuthorIDs {
				if id == authorID {
					return true
				}
			}
			continue
		}
		// A global refresh has no author identity; fail closed for every author.
		return true
	}
	return false
}

func chaptarrExactBookSearchActive(commands []chaptarr.Command, bookID int) bool {
	for _, command := range commands {
		if !chaptarrCommandActive(command) || !strings.EqualFold(command.EffectiveName(), "BookSearch") {
			continue
		}
		for _, candidateID := range command.Body.BookIDs {
			if candidateID == bookID {
				return true
			}
		}
	}
	return false
}

func chaptarrAnyCandidateBookSearchActive(commands []chaptarr.Command, candidates []chaptarrBookCandidate) bool {
	for _, candidate := range candidates {
		if chaptarrExactBookSearchActive(commands, candidate.book.ID) {
			return true
		}
	}
	return false
}

func (s *Service) bookSearchAckKey(instanceID string, bookID int) string {
	return instanceID + "\x00" + fmt.Sprintf("%d", bookID)
}

func (s *Service) hasRecentBookSearchAck(instanceID string, bookID int) bool {
	ttl := s.bookSearchAckTTL
	if ttl <= 0 {
		ttl = defaultBookSearchAckTTL
	}
	key := s.bookSearchAckKey(instanceID, bookID)
	now := time.Now()
	s.bookSearchAckMu.Lock()
	defer s.bookSearchAckMu.Unlock()
	acknowledgedAt, ok := s.bookSearchAcks[key]
	if !ok {
		return false
	}
	if now.Sub(acknowledgedAt) > ttl {
		delete(s.bookSearchAcks, key)
		return false
	}
	return true
}

func (s *Service) recordBookSearchAck(instanceID string, bookID int) {
	ttl := s.bookSearchAckTTL
	if ttl <= 0 {
		ttl = defaultBookSearchAckTTL
	}
	now := time.Now()
	s.bookSearchAckMu.Lock()
	for key, acknowledgedAt := range s.bookSearchAcks {
		if now.Sub(acknowledgedAt) > ttl {
			delete(s.bookSearchAcks, key)
		}
	}
	s.bookSearchAcks[s.bookSearchAckKey(instanceID, bookID)] = now
	s.bookSearchAckMu.Unlock()
}

func (s *Service) hasUncertainBookSearch(instanceID string, bookID int) bool {
	key := s.bookSearchAckKey(instanceID, bookID)
	now := time.Now()
	s.bookSearchOutcomeMu.Lock()
	defer s.bookSearchOutcomeMu.Unlock()
	startedAt, ok := s.bookUncertainSearches[key]
	if !ok {
		return false
	}
	if now.Sub(startedAt) > defaultBookSearchAckTTL {
		delete(s.bookUncertainSearches, key)
		return false
	}
	return true
}

func (s *Service) recordUncertainBookSearch(instanceID string, bookID int) {
	key := s.bookSearchAckKey(instanceID, bookID)
	now := time.Now()
	s.bookSearchOutcomeMu.Lock()
	if s.bookUncertainSearches == nil {
		s.bookUncertainSearches = make(map[string]time.Time)
	}
	for candidate, startedAt := range s.bookUncertainSearches {
		if now.Sub(startedAt) > defaultBookSearchAckTTL {
			delete(s.bookUncertainSearches, candidate)
		}
	}
	s.bookUncertainSearches[key] = now
	s.bookSearchOutcomeMu.Unlock()
	s.invalidateBookCaches(instanceID)
}

func (s *Service) clearUncertainBookSearch(instanceID string, bookID int) {
	s.bookSearchOutcomeMu.Lock()
	key := s.bookSearchAckKey(instanceID, bookID)
	_, existed := s.bookUncertainSearches[key]
	delete(s.bookUncertainSearches, key)
	s.bookSearchOutcomeMu.Unlock()
	if existed {
		s.invalidateBookCaches(instanceID)
	}
}

func (s *Service) invalidateBookCaches(instanceID string) {
	if s.libraryCache == nil || instanceID == "" {
		return
	}
	// Serialize Delete against both cache builders. This prevents an in-flight
	// pre-mutation GET from publishing stale data after invalidation.
	projectionLock := s.projectionLock(instanceID)
	projectionLock.Lock()
	defer projectionLock.Unlock()
	s.libraryCache.Delete("book-library:" + instanceID)
	s.libraryCache.Delete("book-live:" + instanceID)
}

func normalizeBookIdentity(value string) string {
	var out strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && out.Len() > 0 {
				out.WriteByte(' ')
			}
			out.WriteRune(r)
			space = false
		} else if out.Len() > 0 {
			space = true
		}
	}
	return out.String()
}

func bookTitlesMatch(left, right string) bool {
	return bookTitleIdentityTier(left, right) > 0
}

func bookTitleIdentityTier(left, right string) int {
	leftNormalized := normalizeBookIdentity(left)
	rightNormalized := normalizeBookIdentity(right)
	if leftNormalized == "" || rightNormalized == "" {
		return 0
	}
	if leftNormalized == rightNormalized {
		return 2
	}
	if normalizeBookIdentity(bookTitleBase(left)) == rightNormalized ||
		normalizeBookIdentity(bookTitleBase(right)) == leftNormalized {
		return 1
	}
	return 0
}

func bookTitleBase(title string) string {
	cut := len(title)
	for _, separator := range []string{" (", ":", " - ", " — "} {
		if index := strings.Index(title, separator); index >= 0 && index < cut {
			cut = index
		}
	}
	return strings.TrimSpace(title[:cut])
}

func chaptarrTitleIsMultiWork(title string) bool {
	normalized := normalizeBookIdentity(title)
	if normalized == "" {
		return false
	}
	padded := " " + normalized + " "
	if strings.Contains(padded, " omnibus ") ||
		strings.Contains(padded, " box set ") || strings.Contains(padded, " boxed set ") ||
		strings.HasSuffix(normalized, " bundle") || strings.HasSuffix(normalized, " trilogy") {
		return true
	}
	if strings.Contains(normalized, "complete ") && strings.Contains(normalized, " series") {
		return true
	}
	return numberedMultiBookTitle.MatchString(title)
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
	var currentFingerprint []byte
	if client != nil && s.registry != nil {
		freshClient, fingerprint, freshErr := s.registry.GetFreshChaptarrClient(instanceID)
		if freshErr != nil || freshClient == nil {
			return nil, ErrChaptarrInstanceInvalid
		}
		client = freshClient
		currentFingerprint = append([]byte(nil), fingerprint[:]...)
	}
	failedFormat, failureCode, failedCheckpoints, failureUpdatedAt, err := s.bookRequestFailure(instanceID, foreignID, currentFingerprint, userID)
	if err != nil {
		return nil, fmt.Errorf("read failed book request: %w", err)
	}
	if failedFormat == "" {
		failureCode = ""
	}
	query := `SELECT COALESCE(book_format, 'both'), status, book_settings_fingerprint,
	                 CAST(strftime('%s', COALESCE(decided_at, requested_at)) AS INTEGER)
	          FROM request_log
	          WHERE (user_id = ? OR status = 'pending') AND foreign_id = ? AND media_type = 'book'`
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
	historyTimes := map[string]int64{}
	verifiedRequested := map[string]bool{}
	collapsed := ""
	for rows.Next() {
		var format, status string
		var historyFingerprint []byte
		var historyAt int64
		if err := rows.Scan(&format, &status, &historyFingerprint, &historyAt); err != nil {
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
				historyTimes[f] = historyAt
				verifiedRequested[f] = status == StatusRequested &&
					len(currentFingerprint) > 0 && bytes.Equal(historyFingerprint, currentFingerprint)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read book request status: %w", err)
	}
	for format, status := range failedCheckpoints {
		if strings.TrimSpace(status) != "" {
			formats[format] = status
		}
	}
	if bookFailureSupersededByNewerDenial(formats, historyTimes, failedFormat, failureUpdatedAt) {
		failedFormat, failureCode = "", ""
		failedCheckpoints = map[string]string{}
	}
	if client != nil {
		live, monitored, lerr := s.liveBookState(client, instanceID, foreignID)
		if lerr != nil {
			// A terminal worker failure is durable requester-facing truth. If
			// Chaptarr cannot currently be read, retain that exact failed format
			// and code instead of replacing it with a transient status-read error.
			// A later successful projection can still prove live coverage below
			// and heal the failure presentation.
			if failureCode != "" {
				return failedBookStatus(formats, failedCheckpoints, failedFormat, failureCode), nil
			}
			if errors.Is(lerr, ErrBookOutcomePending) {
				known := false
				return &StatusResponse{
					Status:        StatusUnavailable,
					StatusKnown:   &known,
					UnknownReason: "outcome_pending",
				}, nil
			}
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
			if checkpoint, ok := failedCheckpoints[format]; ok && strings.TrimSpace(checkpoint) != "" {
				// A completed first half of a durable "both" job has already
				// crossed its search acknowledgement boundary. An absent live row
				// must not make that exact format requestable again.
				formats[format] = checkpoint
				continue
			}
			if logged && loggedStatus == StatusRequested && verifiedRequested[format] && monitored[format] {
				// Requested history is durable proof of an acknowledged search.
				// Preserve it only while the exact live format remains monitored;
				// history alone must not mask a removed/unmonitored library row.
				formats[format] = StatusRequested
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
	if failureCode != "" && !bookFailureHasCoverage(formats, failedFormat) {
		return failedBookStatus(formats, failedCheckpoints, failedFormat, failureCode), nil
	}

	if len(formats) == 0 {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}
	resp := &StatusResponse{BookFormats: formats}
	resp.Status = collapseBookStatuses(formats, collapsed)
	return resp, nil
}

func bookUncheckpointedFormat(failedFormat string, checkpoints map[string]string) string {
	remaining := make(map[string]string, 2)
	for _, format := range expandBookFormat(failedFormat) {
		if strings.TrimSpace(checkpoints[format]) == "" {
			remaining[format] = StatusUnavailable
		}
	}
	return concreteBookFormat(remaining)
}

func bookFailureSupersededByNewerDenial(formats map[string]string, historyTimes map[string]int64, failedFormat string, failureUpdatedAt int64) bool {
	failed := expandBookFormat(failedFormat)
	if len(failed) == 0 || failureUpdatedAt == 0 {
		return false
	}
	for _, format := range failed {
		if formats[format] != StatusDenied || historyTimes[format] < failureUpdatedAt {
			return false
		}
	}
	return true
}

func failedBookStatus(formats, checkpoints map[string]string, failedFormat, failureCode string) *StatusResponse {
	for format, status := range checkpoints {
		if strings.TrimSpace(status) != "" {
			formats[format] = status
		}
	}
	for _, format := range expandBookFormat(failedFormat) {
		if strings.TrimSpace(checkpoints[format]) != "" {
			continue
		}
		// Without readable live truth, request history cannot prove that a
		// terminally failed format has since been covered.
		formats[format] = StatusUnavailable
	}
	known := false
	return &StatusResponse{
		Status:        StatusUnavailable,
		StatusKnown:   &known,
		UnknownReason: "request_failed",
		FailureCode:   failureCode,
		BookFormats:   formats,
	}
}

func bookFailureHasCoverage(formats map[string]string, failedFormat string) bool {
	failed := expandBookFormat(failedFormat)
	if len(failed) == 0 {
		return false
	}
	for _, format := range failed {
		switch formats[format] {
		case StatusAvailable, StatusDownloading, StatusRequested, StatusPending, StatusPartial:
		default:
			return false
		}
	}
	return true
}

const bookLiveProjectionTTL = 15 * time.Second

type bookLiveProjection struct {
	Formats        map[string]map[string]string `json:"formats"`
	Monitored      map[string]map[string]bool   `json:"monitored,omitempty"`
	Unresolved     map[string]bool              `json:"unresolved,omitempty"`
	OutcomePending map[string]bool              `json:"outcome_pending,omitempty"`
}

// liveBookFormats returns one title's slice of a short-lived instance-wide
// projection. Search grids call book-status once per row, so fetching the full
// library/queue per title would be an accidental N+1 load on Chaptarr.
func (s *Service) liveBookFormats(client *chaptarr.Client, instanceID, foreignID string) (map[string]string, error) {
	formats, _, err := s.liveBookState(client, instanceID, foreignID)
	return formats, err
}

func (s *Service) liveBookState(client *chaptarr.Client, instanceID, foreignID string) (map[string]string, map[string]bool, error) {
	active, err := s.hasActiveBookRequestJob(instanceID, foreignID)
	if err != nil {
		return nil, nil, fmt.Errorf("check active book request: %w", err)
	}
	if active {
		return nil, nil, ErrBookOutcomePending
	}
	cacheKey := "book-live:" + instanceID
	if projection, ok := s.cachedBookProjection(cacheKey); ok {
		return projection.stateFor(foreignID)
	}
	projectionLock := s.projectionLock(instanceID)
	projectionLock.Lock()
	defer projectionLock.Unlock()
	if projection, ok := s.cachedBookProjection(cacheKey); ok {
		return projection.stateFor(foreignID)
	}
	projection, err := s.buildBookLiveProjection(client, instanceID)
	if err != nil {
		return nil, nil, err
	}
	s.cacheBookProjection(cacheKey, projection)
	return projection.stateFor(foreignID)
}

func (s *Service) freshLiveBookFormats(client *chaptarr.Client, instanceID, foreignID string) (map[string]string, error) {
	active, err := s.hasActiveBookRequestJob(instanceID, foreignID)
	if err != nil {
		return nil, fmt.Errorf("check active book request: %w", err)
	}
	if active {
		return nil, ErrBookOutcomePending
	}
	projectionLock := s.projectionLock(instanceID)
	projectionLock.Lock()
	defer projectionLock.Unlock()
	projection, err := s.buildBookLiveProjection(client, instanceID)
	if err != nil {
		return nil, err
	}
	s.cacheBookProjection("book-live:"+instanceID, projection)
	return projection.formatsFor(foreignID)
}

func (s *Service) buildBookLiveProjection(client *chaptarr.Client, instanceID string) (*bookLiveProjection, error) {
	books, err := client.GetAllBooks()
	if err != nil {
		return nil, fmt.Errorf("check live book state: %w", err)
	}
	queued := make(map[int]bool)
	queue, err := client.GetQueueDetailed(1, 100)
	if err != nil {
		return nil, fmt.Errorf("check live book queue: %w", err)
	}
	for _, item := range queue {
		if item.BookID != 0 && bookQueueItemDownloading(item) {
			queued[item.BookID] = true
		}
	}
	commands, err := client.GetCommands()
	if err != nil {
		return nil, fmt.Errorf("check live book searches: %w", err)
	}
	projection := &bookLiveProjection{
		Formats:        make(map[string]map[string]string),
		Monitored:      make(map[string]map[string]bool),
		Unresolved:     make(map[string]bool),
		OutcomePending: make(map[string]bool),
	}
	foreignIDs := make(map[string]bool)
	for _, book := range books {
		if book.ForeignBookID != "" {
			foreignIDs[book.ForeignBookID] = true
		}
	}
	for id := range foreignIDs {
		_, records, unresolved := recordsForForeignID(books, id)
		_, unsafeUnresolved := chaptarrPhysicalAndUnsafeRecords(books, id)
		if unresolved && unsafeUnresolved {
			projection.Unresolved[id] = true
			continue
		}
		live := make(map[string]string)
		monitored := make(map[string]bool)
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			recs := records[format]
			if len(recs) == 0 {
				continue
			}
			status := StatusUnavailable
			for _, rec := range recs {
				monitored[format] = monitored[format] || chaptarrBookMonitoredForFormat(rec, format)
				switch {
				case chaptarrBookAvailable(rec):
					status = StatusAvailable
				case status != StatusAvailable && queued[rec.ID]:
					status = StatusDownloading
				case status != StatusAvailable && status != StatusDownloading &&
					(rec.Grabbed || s.hasRecentBookSearchAck(instanceID, rec.ID)):
					status = StatusRequested
				case status != StatusAvailable && status != StatusDownloading &&
					rec.Monitored && chaptarrExactBookSearchActive(commands, rec.ID):
					status = StatusRequested
				}
			}
			live[format] = status
		}
		if projection.Unresolved[id] {
			continue
		}
		projection.Formats[id] = live
		projection.Monitored[id] = monitored
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
	formats, _, err := p.stateFor(foreignID)
	return formats, err
}

func (p *bookLiveProjection) stateFor(foreignID string) (map[string]string, map[string]bool, error) {
	if p.OutcomePending[foreignID] {
		return nil, nil, ErrBookOutcomePending
	}
	if p.Unresolved[foreignID] {
		return nil, nil, ErrBookFormatUnresolved
	}
	return p.Formats[foreignID], p.Monitored[foreignID], nil
}

func chaptarrBookMonitoredForFormat(book chaptarr.Book, format string) bool {
	if !book.Monitored {
		return false
	}
	if format == BookFormatAudiobook {
		return book.AudiobookMonitored
	}
	return book.EbookMonitored
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
				if errors.Is(err, ErrBookFormatUnresolved) || errors.Is(err, ErrBookOutcomePending) {
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
		`SELECT r.id, r.user_id, COALESCE(u.username, ''), r.tmdb_id, COALESCE(r.tvdb_id, 0), r.media_type, r.title, COALESCE(r.book_format, ''), COALESCE(r.book_selection_json, ''), COALESCE(r.instance_id, ''),
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
		var selectionJSON string
		if err := rows.Scan(&p.ID, &p.UserID, &p.Username, &p.TmdbID, &p.TvdbID, &p.MediaType, &p.Title, &p.BookFormat, &selectionJSON, &p.InstanceID, &p.InstanceName, &p.RequesterCount, &p.SeasonScope, &p.QualityProfileID, &p.RequestedAt); err != nil {
			return nil, fmt.Errorf("scan pending request: %w", err)
		}
		p.BookFormat = normalizeBookFormat(p.BookFormat)
		if p.MediaType == "book" {
			p.BookSelection, err = requireDecodedBookSelection(selectionJSON, p.BookFormat)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// loadRequest reads a request_log row into a resolvedRequest plus its status.
func (s *Service) loadRequest(requestID int64) (*resolvedRequest, string, error) {
	var r resolvedRequest
	var status, selectionJSON string
	err := s.db.QueryRow(
		"SELECT user_id, tmdb_id, COALESCE(tvdb_id, 0), COALESCE(foreign_id, ''), COALESCE(book_format, ''), COALESCE(book_selection_json, ''), COALESCE(instance_id, ''), media_type, title, status, COALESCE(season_scope, ''), COALESCE(quality_profile_id, 0) FROM request_log WHERE id = ?",
		requestID,
	).Scan(&r.userID, &r.tmdbID, &r.tvdbID, &r.foreignID, &r.bookFormat, &selectionJSON, &r.instanceID, &r.mediaType, &r.title, &status, &r.seasonScope, &r.qualityProfileID)
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
	if r.mediaType == "book" {
		r.bookSelection, err = requireDecodedBookSelection(selectionJSON, r.bookFormat)
		if err != nil {
			return nil, "", err
		}
	}
	// An explicit season list was stored as JSON in season_scope; decode it so
	// approval replays the explicit season selection the requester chose.
	r.seasonNumbers = decodeSeasonNumbers(r.seasonScope)
	return &r, status, nil
}

func (s *Service) approveBookRequestDurably(adminID, requestID int64, r *resolvedRequest) (*CreateResponse, error) {
	if strings.TrimSpace(r.instanceID) == "" {
		return nil, fmt.Errorf("pending book request has no pinned Chaptarr instance")
	}
	// Validate the current shared audience before touching Chaptarr. Completion
	// reads it again transactionally so subscribers who join while work is being
	// reconciled are still included.
	if _, err := s.bookRequestAudience(requestID, r.userID, r.bookFormat); err != nil {
		return nil, err
	}
	if s.registry == nil {
		return nil, ErrChaptarrInstanceInvalid
	}
	unlockConfig := s.registry.LockInstanceConfigRead(r.instanceID)
	defer unlockConfig()
	bookLock := s.bookLock(r.instanceID + "\x00" + r.foreignID)
	bookLock.Lock()
	defer bookLock.Unlock()
	job, client, alreadyActive, err := s.prepareApprovalBookJob(adminID, requestID, r)
	if err != nil {
		return nil, err
	}
	if alreadyActive {
		return nil, ErrBookOutcomePending
	}
	r.actorID = adminID
	r.bookJobID = job.ID
	applyBookJobCheckpoints(r, job)
	status, title, err := s.addToChaptarrWithClient(r, client, r.instanceID)
	if err != nil {
		if s.deferDirectBookJob(job.ID, err) {
			return nil, ErrBookOutcomePending
		}
		return nil, err
	}
	response, err := s.completeApprovedBookJob(job, r, title, status)
	if err != nil {
		s.deferDirectBookJob(job.ID, err)
		return nil, ErrBookOutcomePending
	}
	return response, nil
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
	if r.mediaType == "book" {
		return s.approveBookRequestDurably(adminID, requestID, r)
	}
	audience := []bookRequestSubscriber{{UserID: r.userID}}
	var bookDecisionFingerprint []byte

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
		"UPDATE request_log SET status = ?, title = ?, tvdb_id = ?, book_format = ?, instance_id = ?, book_settings_fingerprint = ?, season_scope = ?, quality_profile_id = ?, approved_by = ?, decided_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?",
		primaryStatus, title, sqlNullInt(r.tvdbID), sqlNullStr(primaryFormat), sqlNullStr(r.instanceID), bookDecisionFingerprint, sqlNullStr(r.seasonScope), sqlNullInt(r.qualityProfileID), adminID, requestID, StatusPending,
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
				`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, book_settings_fingerprint, media_type, title, status, approved_by, decided_at)
				 VALUES (?, 0, ?, ?, ?, ?, 'book', ?, ?, ?, CURRENT_TIMESTAMP)`,
				r.userID, r.foreignID, format, sqlNullStr(r.instanceID), bookDecisionFingerprint, title, formatStatus, adminID,
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
					`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, book_settings_fingerprint, media_type, title, status, approved_by, decided_at)
					 VALUES (?, 0, ?, ?, ?, ?, 'book', ?, ?, ?, CURRENT_TIMESTAMP)`,
					subscriber.UserID, r.foreignID, format, sqlNullStr(r.instanceID), bookDecisionFingerprint, title, formatStatus, adminID,
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
	unlockBook := func() {}
	if r.mediaType == "book" {
		if r.instanceID != "" {
			bookLock := s.bookLock(r.instanceID + "\x00" + r.foreignID)
			bookLock.Lock()
			unlockBook = bookLock.Unlock
		}
	}
	defer unlockBook()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin request denial: %w", err)
	}
	defer tx.Rollback()
	if r.mediaType == "book" {
		audience, err = bookRequestAudienceTx(tx, requestID, r.userID, r.bookFormat)
		if err != nil {
			return err
		}
		var active int
		err := tx.QueryRow(
			`SELECT 1 FROM book_request_jobs
			 WHERE request_id = ? AND state IN ('running','retry_wait','outcome_unknown')
			 LIMIT 1`,
			requestID,
		).Scan(&active)
		if err == nil {
			return ErrBookOutcomePending
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check active book approval: %w", err)
		}
	}
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
			selectionJSON, encodeErr := encodeBookSelection(r.bookSelection, subscriber.BookFormat)
			if encodeErr != nil {
				return encodeErr
			}
			if _, err := tx.Exec(
				`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, book_selection_json, media_type, title, status, deny_reason, approved_by, decided_at)
				 VALUES (?, 0, ?, ?, ?, ?, 'book', ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
				subscriber.UserID, r.foreignID, subscriber.BookFormat, sqlNullStr(r.instanceID), sqlNullStr(selectionJSON), r.title, StatusDenied, sqlNullStr(reason), adminID,
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
