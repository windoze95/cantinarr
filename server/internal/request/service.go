package request

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/windoze95/cantinarr-server/internal/cache"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/lidarr"
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
	ErrArrInstanceForbidden      = errors.New("that library is not available to you")
	ErrArrInstanceInvalid        = errors.New("unknown library for this media type")
	ErrBookFormatUnresolved      = errors.New("book format is unsupported or ambiguous")
	// ErrBookMetadataUnresolved means no Chaptarr metadata record could be found
	// for the requested foreignBookId, so there is nothing to add a book from.
	// It is the one add failure that says nothing about the requester, the
	// instance config, or the book's availability, so it parks the request in the
	// approval queue instead of discarding it.
	ErrBookMetadataUnresolved = errors.New("book not found for foreign id")
)

// bookParkedMessage explains, in requester vocabulary, why a book request that
// could not be added is sitting in the approval queue instead.
const bookParkedMessage = "This book couldn't be matched in the library, so it was saved as a request for an admin instead of being added automatically."

// bookAuthorImportingMessage explains a park caused by the library's metadata
// service still importing the book's author (chaptarr.ErrAuthorPendingImport):
// the add works once that import lands, and the park-maintenance sweep
// completes it without anyone's intervention, so the requester is promised
// automatic completion rather than an approval story.
const bookAuthorImportingMessage = "This book's author is still being added to the library. Your request will finish automatically once that completes."

// bookParkReasonAuthorImport marks a pending request_log row the server itself
// owns: it parks only on chaptarr.ErrAuthorPendingImport from an auto-approved
// create, is hidden from the approval queue and badge, and is watched by the
// park-maintenance sweep. Rows pending because policy requires approval keep a
// NULL park_reason and are untouchable by the sweep — that NULL is the
// approval-bypass guard.
//
// It doubles as the wire value of BookFormatWait.Reason: the reason a requester
// is shown and the reason the sweep keys on are the same fact, and splitting
// them would let the two drift.
const bookParkReasonAuthorImport = "author_import"

// serverOwnedParkReasons is the single answer to "is this row the server's
// problem or a person's?", and every surface must use it.
//
// It exists because the two halves once disagreed: visibility treated ANY
// non-NULL park_reason as server-owned, while the sweep only ever retried the
// exact value 'author_import'. A row carrying any other value was therefore
// hidden from the approval queue and the badge, advertised in "Waiting for
// library" as being retried automatically, and never retried or demoted by
// anything — stranded under a label claiming it was handled. That is the exact
// failure the waiting list was built to end, so the list must not be able to
// cause it.
//
// A value this build does not recognise is NOT server-owned: unknown falls back
// to the approval queue, where a person can see and decide it. Same direction
// the rest of the schema fails in — toward human review, never toward silence.
var serverOwnedParkReasons = []string{bookParkReasonAuthorImport}

// serverOwnedParkSQL renders serverOwnedParkReasons as an IN-list fragment plus
// its bind arguments, so adding a reason to the slice updates every query at
// once instead of leaving one behind.
func serverOwnedParkSQL() (string, []interface{}) {
	placeholders := make([]string, len(serverOwnedParkReasons))
	args := make([]interface{}, len(serverOwnedParkReasons))
	for i, reason := range serverOwnedParkReasons {
		placeholders[i] = "?"
		args[i] = reason
	}
	return strings.Join(placeholders, ", "), args
}

// bookImportAddFailureSQL renders bookImportAddFailures as an IN-list fragment
// plus its bind arguments, the same shape as serverOwnedParkSQL and for the
// same reason: adding a value updates every query at once.
func bookImportAddFailureSQL() (string, []interface{}) {
	placeholders := make([]string, len(bookImportAddFailures))
	args := make([]interface{}, len(bookImportAddFailures))
	for i, reason := range bookImportAddFailures {
		placeholders[i] = "?"
		args[i] = reason
	}
	return strings.Join(placeholders, ", "), args
}

// BookWaitReasonAuthorImport exposes that wire value to the other packages that
// have to render a wait (the MCP tool surface). Same string, one definition.
const BookWaitReasonAuthorImport = bookParkReasonAuthorImport

// An approval-queue row can be there for two unrelated reasons, and until these
// existed both rendered identically: a request awaiting someone's decision, and
// a request whose automatic add already ran and failed. The second is not a
// policy question — approving it replays the same add — so a row that says
// nothing invites an admin to press Approve, read "Something went wrong", and
// have no idea the real next step is in Chaptarr.
//
// These are descriptive only. They never move a row out of the approval queue
// or out of the badge: a human really does have to act on both kinds. See the
// add_failure_reason column comment for why this is not a park_reason value.
const (
	// bookAddFailureMetadataUnresolved: the library could not match the book, so
	// the add never happened. Approving replays it, which works only once
	// Chaptarr can find the record — usually because an admin added it.
	bookAddFailureMetadataUnresolved = "metadata_unresolved"
	// bookAddFailureImportAbandoned: the watch on a server-owned author-import
	// park ended on an add failure — the legacy probe (builds without the
	// pending-import API) hit something beyond the still-importing refusal, or
	// the add failed even after the author landed. The wait itself never
	// expires: Chaptarr's own retry loop is unbounded by design, so Cantinarr
	// deliberately has no give-up clock of its own.
	bookAddFailureImportAbandoned = "import_abandoned"
	// bookAddFailureImportFailed: Chaptarr's pending import reached its
	// declared-terminal Failed state (a typed metadata-server outcome stopped
	// its automatic retries). Waiting longer cannot succeed on its own;
	// keep-waiting re-parks AND asks Chaptarr to reopen the import.
	bookAddFailureImportFailed = "import_failed"
	// bookAddFailureImportCancelled: the pending import vanished from Chaptarr
	// without the author landing — someone cancelled it in the arr's own UI.
	// Keep-waiting replays the add, which re-queues it.
	bookAddFailureImportCancelled = "import_cancelled"
)

// bookImportAddFailures are the add_failure_reason values that mean "the wait
// on Chaptarr's author import ended without the author": the demotion lanes
// whose approval-queue card offers keep-waiting alongside close.
var bookImportAddFailures = []string{
	bookAddFailureImportAbandoned, bookAddFailureImportFailed, bookAddFailureImportCancelled,
}

func isBookImportAddFailure(reason string) bool {
	for _, candidate := range bookImportAddFailures {
		if reason == candidate {
			return true
		}
	}
	return false
}

const (
	// bookParkRetryInterval paces the maintenance sweep's status polls.
	// Chaptarr processes its own queued author imports on a similar cadence,
	// so polling faster reads the same answer twice.
	bookParkRetryInterval = 5 * time.Minute
	// bookParkStallAfter is when a still-parked request stops being a routine
	// wait and becomes a system-health fact worth one auto-resolving issue.
	//
	// There is deliberately NO give-up horizon beyond it: Chaptarr's own
	// retry loop is unbounded (transient "author not ready" never ages into a
	// synthetic terminal), and Cantinarr mirrors the arr's verdicts instead of
	// inventing a clock — a park ends when the import lands, when Chaptarr
	// declares it failed or it is cancelled, or when a human closes the
	// request, never because a timer fired.
	bookParkStallAfter = 24 * time.Hour
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
	// posterCache holds approval-queue artwork paths keyed by media type + TMDB
	// id, so re-opening the queue doesn't re-ask TMDB for every row.
	posterCache *cache.Cache
	// posterLookupOverride substitutes the queue's metadata client in tests.
	// Production leaves it nil and resolves TMDB through the bridge.
	posterLookupOverride posterLookup
	// bookImportStallSink surfaces long-stalled author-import parks as an
	// auto-resolving system issue; nil (tests, minimal wiring) disables it.
	bookImportStallSink BookImportStallSink
	decisionLocks       [64]sync.Mutex
	bookLocks           [64]sync.Mutex
	projectionLocks     [32]sync.Mutex
	// startedAt bounds which retry attempts this process can vouch for. A park
	// created before it was last retried by a predecessor process whose passes
	// were never written down anywhere.
	startedAt time.Time
	// lastParkSweep is when SweepParkedBookRequests last finished a pass, and
	// with it every parked row's last attempt: the sweep retries all of them on
	// every pass, so one process-level timestamp is the complete answer. A
	// per-row column would carry no information this does not, and would rewrite
	// every parked row every five minutes against a pool that holds exactly one
	// connection.
	parkSweepMu   sync.RWMutex
	lastParkSweep time.Time
	// parkSweepRunMu serializes sweep passes: the five-minute ticker and the
	// webhook-driven resume may fire together, and two concurrent passes would
	// race their probes over the same parked rows.
	parkSweepRunMu sync.Mutex
	// contentPolicy is the kids-account service: a child cannot request,
	// see the status of, or list a title outside their limits. nil until
	// wired (SetContentPolicy); a server without it gates nothing.
	contentPolicy *contentpolicy.Service
}

// SetContentPolicy wires the kids-account service.
func (s *Service) SetContentPolicy(svc *contentpolicy.Service) { s.contentPolicy = svc }

var (
	// ErrTitleNotAvailable is a kids account asking for a title outside its
	// limits, in the requester's own words: the same "not available" the
	// detail route answers, never a hint at what was hidden.
	ErrTitleNotAvailable = errors.New("that title is not available for this account")
	// ErrContentPolicyUnavailable is a limit that could not be checked; the
	// request is refused rather than let through.
	ErrContentPolicyUnavailable = errors.New("content limits are temporarily unavailable, retry shortly")
)

// contentPolicyFor reads a requester's policy; nil means unrestricted.
func (s *Service) contentPolicyFor(userID int64, isAdmin bool) (*contentpolicy.Policy, error) {
	if s.contentPolicy == nil || isAdmin {
		return nil, nil
	}
	policy, err := s.contentPolicy.Store.Get(userID)
	if err != nil {
		return nil, ErrContentPolicyUnavailable
	}
	return policy, nil
}

// checkContentPolicy refuses a movie or show a kids account may not see.
func (s *Service) checkContentPolicy(userID int64, isAdmin bool, mediaType string, tmdbID int) error {
	if mediaType != contentpolicy.MediaMovie && mediaType != contentpolicy.MediaTV {
		return nil
	}
	policy, err := s.contentPolicyFor(userID, isAdmin)
	if err != nil || policy == nil {
		return err
	}
	allowed, err := s.contentPolicy.Allows(context.Background(), policy, contentpolicy.Candidate{MediaType: mediaType, TMDBID: tmdbID})
	if err != nil {
		return ErrContentPolicyUnavailable
	}
	if !allowed {
		return ErrTitleNotAvailable
	}
	return nil
}

// hideBlockedRequests drops the movie and show rows a kids account may not
// see from their own history (a request made before the limits were set,
// say). A lookup that fails fails the read: a list that looks complete but
// is not would be worse than an error.
func (s *Service) hideBlockedRequests(userID int64, isAdmin bool, requests []RequestLog) ([]RequestLog, error) {
	policy, err := s.contentPolicyFor(userID, isAdmin)
	if err != nil || policy == nil {
		return requests, err
	}
	cands := make([]contentpolicy.Candidate, 0, len(requests))
	indexes := make([]int, 0, len(requests))
	for i, r := range requests {
		if r.MediaType != contentpolicy.MediaMovie && r.MediaType != contentpolicy.MediaTV {
			continue
		}
		cands = append(cands, contentpolicy.Candidate{MediaType: r.MediaType, TMDBID: r.TmdbID})
		indexes = append(indexes, i)
	}
	if len(cands) == 0 {
		return requests, nil
	}
	keep, err := s.contentPolicy.Filter(context.Background(), policy, cands)
	if err != nil {
		return nil, ErrContentPolicyUnavailable
	}
	drop := map[int]struct{}{}
	for i, ok := range keep {
		if !ok {
			drop[indexes[i]] = struct{}{}
		}
	}
	if len(drop) == 0 {
		return requests, nil
	}
	kept := make([]RequestLog, 0, len(requests)-len(drop))
	for i, r := range requests {
		if _, dropped := drop[i]; !dropped {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

func NewService(db *sql.DB, registry *instance.Registry, bridge *tmdb.Bridge, notifier Notifier) *Service {
	return &Service{
		db:           db,
		registry:     registry,
		bridge:       bridge,
		notifier:     notifier,
		libraryCache: cache.New(),
		posterCache:  cache.New(),
		// Truncated to match request_log.requested_at, which SQLite stores at
		// one-second resolution: a row created in this process must never look
		// older than the process by a fraction of a second.
		startedAt: time.Now().UTC().Truncate(time.Second),
	}
}

// markParkSweep records that the maintenance sweep just attempted every parked
// row.
func (s *Service) markParkSweep(at time.Time) {
	s.parkSweepMu.Lock()
	s.lastParkSweep = at.UTC()
	s.parkSweepMu.Unlock()
}

// bookFormatWaitFor describes a server-owned park in the terms both a requester
// and an admin need: why it is waiting, since when, and when the retry loop
// last actually ran for it.
//
// The failed add that created the park is itself an attempt, but only this
// process can vouch for it. A park older than this process was last retried by
// a predecessor, minutes before the restart, and reporting its request time
// would age-stamp that attempt as days old — the "nothing is happening" reading
// this whole change exists to remove. An absent LastAttemptAt says the attempt
// is unknown, which is true, and the next pass is at most one interval away.
func (s *Service) bookFormatWaitFor(reason string, requestedAt time.Time) BookFormatWait {
	wait := BookFormatWait{Reason: reason, WaitingSince: requestedAt.UTC()}
	s.parkSweepMu.RLock()
	last := s.lastParkSweep
	s.parkSweepMu.RUnlock()
	if requested := requestedAt.UTC(); !requested.Before(s.startedAt) && requested.After(last) {
		last = requested
	}
	if !last.IsZero() {
		wait.LastAttemptAt = &last
	}
	return wait
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

// resolveRadarr resolves an explicitly selected instance when present and
// enforces requester access before returning a client. Admins may select any
// configured Radarr; omitted IDs resolve the user's effective default. The
// second return is the resolved instance ID ("" when nothing is configured).
func (s *Service) resolveRadarr(userID int64, instanceID string) (*radarr.Client, string, error) {
	if s.registry == nil {
		return nil, "", nil
	}
	if instanceID != "" {
		if !s.userIsAdmin(userID) {
			allowed, err := s.registry.UserCanAccessInstance(userID, instanceID, "radarr")
			if err != nil {
				return nil, "", fmt.Errorf("check radarr access: %w", err)
			}
			if !allowed {
				return nil, "", ErrArrInstanceForbidden
			}
		}
		client, err := s.registry.GetRadarrClient(instanceID)
		if err != nil {
			return nil, "", ErrArrInstanceInvalid
		}
		return client, instanceID, nil
	}
	client, id, err := s.registry.GetUserDefaultRadarrClient(userID)
	if err != nil {
		return nil, "", err
	}
	return client, id, nil
}

// resolveSonarr is resolveRadarr's Sonarr twin.
func (s *Service) resolveSonarr(userID int64, instanceID string) (*sonarr.Client, string, error) {
	if s.registry == nil {
		return nil, "", nil
	}
	if instanceID != "" {
		if !s.userIsAdmin(userID) {
			allowed, err := s.registry.UserCanAccessInstance(userID, instanceID, "sonarr")
			if err != nil {
				return nil, "", fmt.Errorf("check sonarr access: %w", err)
			}
			if !allowed {
				return nil, "", ErrArrInstanceForbidden
			}
		}
		client, err := s.registry.GetSonarrClient(instanceID)
		if err != nil {
			return nil, "", ErrArrInstanceInvalid
		}
		return client, instanceID, nil
	}
	client, id, err := s.registry.GetUserDefaultSonarrClient(userID)
	if err != nil {
		return nil, "", err
	}
	return client, id, nil
}

type CreateRequest struct {
	TmdbID    int    `json:"tmdb_id"`
	MediaType string `json:"media_type"`
	Title     string `json:"title"`
	TvdbID    int    `json:"tvdb_id"`
	// ForeignID is the arr-native metadata id for requests with no TMDB id:
	// the Chaptarr/Readarr foreignBookId for books, the MusicBrainz
	// release-group id for music. Required when media_type is "book" or
	// "music"; ignored otherwise.
	ForeignID  string `json:"foreign_id"`
	BookFormat string `json:"book_format"`
	// SearchTerm is the text the requester actually searched to find this book.
	// Chaptarr's book lookup is a fuzzy text search, so adding a title that isn't
	// tracked yet means finding its metadata record again — and this term is the
	// one already proven to return it. Optional: deep links and notification taps
	// have no search behind them and fall back to title-derived terms.
	SearchTerm string `json:"search_term,omitempty"`
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

// BookFormatWait is the durable reason one book format is not in the library
// yet even though Cantinarr accepted the request: the server owns it and is
// retrying it unattended. Without this the only honest word left for that state
// was "requested", which promises a monitored library record that does not
// exist — a live retry loop and an app that silently did nothing render
// identically, and the requester cannot tell which one they are looking at.
//
// It rides alongside book_formats rather than becoming a new value inside it:
// clients that predate this field keep reading the status they always read,
// where an unrecognized status word would have flipped them into an unknown
// state and made every older app worse.
type BookFormatWait struct {
	// Reason is the machine-readable wait, today only bookParkReasonAuthorImport.
	// Clients must treat an unfamiliar reason as a wait they cannot name (still
	// covered, still not requestable) rather than as no wait at all.
	Reason string `json:"reason"`
	// WaitingSince is when the request was accepted — the first add attempt is
	// what parked it, so this is both "asked at" and "waiting since".
	WaitingSince time.Time `json:"waiting_since"`
	// LastAttemptAt is when the retry loop last actually ran for this row.
	// Absent means no attempt this process can vouch for (see
	// Service.bookFormatWaitFor) — "unknown", never "never tried".
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
}

type CreateResponse struct {
	Success     bool              `json:"success"`
	Status      string            `json:"status"`
	Title       string            `json:"title"`
	BookFormats map[string]string `json:"book_formats,omitempty"`
	// BookFormatWaits explains, per format, a book_formats entry that reads
	// "requested" only because the server is finishing it unattended.
	BookFormatWaits map[string]BookFormatWait `json:"book_format_waits,omitempty"`
	// CanonicalForeignID is the Chaptarr record's own foreignBookId when it
	// differs from the id the request was made with, so clients can re-address
	// the book by the id the library will report from now on.
	CanonicalForeignID string `json:"canonical_foreign_id,omitempty"`
	// Message explains an outcome the status alone would misrepresent — today,
	// a book request that had to be parked for an admin because its metadata
	// record could not be re-found. Clients show it verbatim; empty means the
	// status speaks for itself.
	Message string `json:"message,omitempty"`
	// InstanceID names the library instance the request was routed to (or is
	// pending for), so clients can reflect the selection without refetching.
	InstanceID string `json:"instance_id,omitempty"`
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
	// BookFormatWaits explains, per format, a book_formats entry that reads
	// "requested" only because the server is finishing it unattended. It is
	// populated strictly after the live overlay, so a format the library has
	// actually taken carries no wait.
	BookFormatWaits map[string]BookFormatWait `json:"book_format_waits,omitempty"`
	// CanonicalForeignID is set when a logged book request resolved its live
	// state through the stored Chaptarr record id and that record now reports a
	// different foreignBookId than the one queried: the id the library files
	// this book under today. Clients should re-address the book by it.
	CanonicalForeignID string `json:"canonical_foreign_id,omitempty"`
	// Releases carries the movie's theatrical and digital release dates, so a
	// title that reads "Requested" can say it is simply not out yet rather than
	// looking like a stalled download. Only populated for movies already in the
	// user's Radarr library — an unadded title has no arr record to read dates
	// from — and omitted entirely when Radarr knows neither date.
	Releases *MovieReleases `json:"releases,omitempty"`
	// InstanceStatuses maps each of the user's granted instance ids for this
	// media type to that library's digest-grade status, present only when the
	// user holds more than one. The headline Status stays the selected (or
	// default) library's full live read; these are the sibling chips. Digest
	// grade means no Downloading state and up to the digest TTL of lag — the
	// documented tradeoff the history overlay already accepts.
	InstanceStatuses map[string]InstanceStatus `json:"instance_statuses,omitempty"`
}

// InstanceStatus is one library's digest-grade status inside
// StatusResponse.InstanceStatuses.
type InstanceStatus struct {
	Status string `json:"status"`
}

// MovieReleases carries a movie's release milestones as plain YYYY-MM-DD
// calendar dates.
//
// They are deliberately not timestamps: a release date has no time-of-day, and
// serialising one as an instant invites a client to localise it and land a day
// early or late. Whether a date is still ahead is likewise the client's call —
// "today" belongs to the viewer's time zone, not the server's — so both dates
// are reported verbatim whenever Radarr knows them, past or future.
type MovieReleases struct {
	InCinemas string `json:"in_cinemas,omitempty"`
	Digital   string `json:"digital,omitempty"`
}

// movieReleases projects Radarr's release dates onto the wire shape, returning
// nil when neither date is known so the field drops out of the response.
func movieReleases(m *radarr.Movie) *MovieReleases {
	if m == nil {
		return nil
	}
	out := &MovieReleases{}
	if m.InCinemas != nil {
		out.InCinemas = m.InCinemas.Format("2006-01-02")
	}
	if m.DigitalRelease != nil {
		out.Digital = m.DigitalRelease.Format("2006-01-02")
	}
	if out.InCinemas == "" && out.Digital == "" {
		return nil
	}
	return out
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
	// BookFormatWait explains a history row reading "requested" only because the
	// server owns it and is retrying it. History rows are already one format
	// each, so this is a single wait rather than the per-format map the detail
	// status endpoint returns.
	BookFormatWait *BookFormatWait `json:"book_format_wait,omitempty"`
}

// PendingRequest is one row of the admin approval queue.
type PendingRequest struct {
	ID       int64  `json:"id"`
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	TmdbID   int    `json:"tmdb_id"`
	TvdbID   int    `json:"tvdb_id"`
	// ForeignID is the Chaptarr identity a book row is addressed by; movie and
	// TV rows are addressed by TmdbID and leave it empty.
	ForeignID string `json:"foreign_id,omitempty"`
	MediaType string `json:"media_type"`
	Title     string `json:"title"`
	// PosterPath is a TMDB artwork path (never a full URL), best-effort: empty
	// means this load resolved none, not that the title has none. See poster.go.
	PosterPath       string    `json:"poster_path,omitempty"`
	BookFormat       string    `json:"book_format"`
	InstanceID       string    `json:"instance_id,omitempty"`
	InstanceName     string    `json:"instance_name,omitempty"`
	RequesterCount   int       `json:"requester_count"`
	SeasonScope      string    `json:"season_scope"`
	QualityProfileID int       `json:"quality_profile_id"`
	RequestedAt      time.Time `json:"requested_at"`
	// WaitReason and LastAttemptAt are populated only for rows served by
	// ListWaiting — the requests the server owns and retries itself. They are
	// absent from the approval queue, whose rows are by definition waiting on a
	// person rather than on a library. RequestedAt doubles as "waiting since".
	WaitReason    string     `json:"wait_reason,omitempty"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	// AddFailureReason marks an approval-queue row whose automatic add already
	// ran and failed, so the queue can distinguish "decide this" from "this
	// already broke and needs you to fix something first". Empty on an ordinary
	// policy hold.
	AddFailureReason string `json:"add_failure_reason,omitempty"`
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
	userID     int64
	actorID    int64 // optional execution authority; history remains userID-owned
	tmdbID     int
	tvdbID     int
	foreignID  string // Chaptarr foreignBookId (book requests)
	bookFormat string
	searchTerm string // the requester's own search text (book requests)
	parkReason string // why a pending row is server-owned (see bookParkReasonAuthorImport)
	addFailure string // the add that already ran and failed (see bookAddFailureMetadataUnresolved)
	instanceID string
	// instanceIsUserDefault marks a movie/TV request whose resolved instance is
	// the requester's effective default, which is the only target allowed to
	// absorb legacy pending rows written before instances were stamped.
	instanceIsUserDefault bool
	bookFormats           map[string]string
	// bookRecordIDs maps each fulfilled concrete format to the Chaptarr record
	// id that satisfies it (created, or monitored in place). The numeric id is
	// the arr-native identity that survives foreignBookId re-keying, so it is
	// persisted with the history row and lets status reads keep resolving live
	// truth after metadata drift.
	bookRecordIDs map[string]int
	// canonicalForeignID is the created/live record's own foreignBookId when
	// Chaptarr filed it under a different id than the request used.
	canonicalForeignID string
	mediaType          string
	title              string
	seasonScope        string
	qualityProfileID   int
	// seasonNumbers, when non-empty, is an explicit set of seasons to monitor
	// (overrides seasonScope). It round-trips through the approval flow by
	// being JSON-encoded into the season_scope column.
	seasonNumbers []int
}

// noteBookRecord captures the live Chaptarr record backing one fulfilled
// format (created, or monitored in place), plus that record's own
// foreignBookId when Chaptarr files it under a different id than requested.
func (r *resolvedRequest) noteBookRecord(format string, recordID int, foreignID string) {
	if recordID <= 0 {
		return
	}
	if r.bookRecordIDs == nil {
		r.bookRecordIDs = make(map[string]int)
	}
	r.bookRecordIDs[format] = recordID
	if foreignID != "" && foreignID != r.foreignID {
		r.canonicalForeignID = foreignID
	}
}

// bookRecordIDForRow is the nullable book_record_id value for this row's
// concrete format. Music rows reuse the column as their generic arr-native
// record id (keyed by the empty format). Movie/TV rows and legacy "both" rows
// store NULL.
func (r *resolvedRequest) bookRecordIDForRow() interface{} {
	if r.mediaType != "book" && r.mediaType != "music" {
		return nil
	}
	return sqlNullInt(r.bookRecordIDs[r.bookFormat])
}

// responseCanonicalForeignID reports the fulfilled record's own foreignBookId
// only when it differs from the requested id — the one case a client must
// react to by re-addressing the book.
func (r *resolvedRequest) responseCanonicalForeignID() string {
	if r.canonicalForeignID != "" && r.canonicalForeignID != r.foreignID {
		return r.canonicalForeignID
	}
	return ""
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
	if req.MediaType != "movie" && req.MediaType != "tv" && req.MediaType != "book" && req.MediaType != "music" {
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
	if req.MediaType == "music" {
		req.ForeignID = strings.TrimSpace(req.ForeignID)
		req.Title = strings.TrimSpace(req.Title)
		if req.ForeignID == "" {
			return nil, fmt.Errorf("foreign_id is required for music requests")
		}
		if req.BookFormat != "" {
			return nil, fmt.Errorf("music requests carry no book_format")
		}
	}

	isAdmin := s.userIsAdmin(userID)
	// A kids account's limits come before anything is resolved or written:
	// a title outside them is not available, in those words.
	if err := s.checkContentPolicy(userID, isAdmin, req.MediaType, req.TmdbID); err != nil {
		return nil, err
	}
	eff, err := s.effectiveSettings(userID, isAdmin)
	if err != nil {
		return nil, err
	}

	resolved := &resolvedRequest{
		userID:     userID,
		tmdbID:     req.TmdbID,
		tvdbID:     req.TvdbID,
		foreignID:  req.ForeignID,
		searchTerm: strings.TrimSpace(req.SearchTerm),
		instanceID: strings.TrimSpace(req.InstanceID),
		mediaType:  req.MediaType,
		title:      req.Title,
	}
	if resolved.mediaType == "book" {
		resolved.bookFormat = normalizeBookFormat(req.BookFormat)
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
	var resolvedMusicClient *lidarr.Client
	if resolved.mediaType == "music" {
		client, instanceID, err := s.resolveLidarr(userID, resolved.instanceID)
		if err != nil {
			return nil, err
		}
		if client == nil {
			return nil, fmt.Errorf("lidarr is not configured for you")
		}
		resolvedMusicClient = client
		resolved.instanceID = instanceID
		// Same per-title critical section as books: preflight, external
		// mutation, and request-log write stay one unit.
		musicLock := s.bookLock(resolved.instanceID + "\x00" + resolved.foreignID)
		musicLock.Lock()
		defer musicLock.Unlock()
	}
	if resolved.mediaType == "movie" || resolved.mediaType == "tv" {
		// Resolve and authorize the target library up front so a pending row
		// stores exactly where approval will send it, and a bad selection
		// fails the create instead of persisting an unhonored provenance
		// stamp (or silently dropping the history row on its foreign key).
		explicit := resolved.instanceID != ""
		serviceType := "radarr"
		var instanceID string
		if resolved.mediaType == "movie" {
			_, instanceID, err = s.resolveRadarr(userID, resolved.instanceID)
		} else {
			serviceType = "sonarr"
			_, instanceID, err = s.resolveSonarr(userID, resolved.instanceID)
		}
		if err != nil {
			return nil, err
		}
		resolved.instanceID = instanceID
		resolved.instanceIsUserDefault = !explicit
		if explicit && s.registry != nil {
			if defaultID, derr := s.registry.EffectiveDefaultInstanceID(userID, serviceType); derr == nil {
				resolved.instanceIsUserDefault = instanceID == defaultID
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
		if resolved.mediaType == "music" {
			// An album the library already covers must answer with that truth
			// instead of queueing a no-op decision.
			live, known, err := s.freshLiveMusicStatus(resolvedMusicClient, resolved.instanceID, resolved.foreignID)
			if err != nil {
				return nil, err
			}
			if known && live != StatusUnavailable {
				return &CreateResponse{Success: true, Status: live, Title: resolved.title, InstanceID: resolved.instanceID}, nil
			}
			if resolved.title == "" {
				return nil, fmt.Errorf("title is required to add a new album")
			}
			resp, err := s.createPendingUnlocked(resolved)
			if err != nil {
				return nil, err
			}
			resp.InstanceID = resolved.instanceID
			return resp, nil
		}
		resp, err := s.createPending(resolved)
		if err != nil {
			return nil, err
		}
		resp.InstanceID = resolved.instanceID
		return resp, nil
	}

	status, title, err := s.addToArr(resolved)
	if err != nil {
		// A book whose add cannot complete yet must not end with the request on
		// the floor: the requester wanted this title and nothing about it is
		// invalid. Two such failures park into the approval queue — a metadata
		// record that can't be re-found (an admin who adds the author in Chaptarr
		// can then approve the parked row and have it work), and an author the
		// library's metadata service is still importing (the import completes on
		// its own; approving afterwards replays the add). Every other failure (no
		// instance, no root folder, ambiguous profiles) is a configuration answer
		// the requester needs to see, not queue.
		if resolved.mediaType == "music" && errors.Is(err, ErrMusicMetadataUnresolved) {
			// Music gets the metadata-unresolved rescue too: the add already
			// ran and failed, so the row queues for an admin with that fact
			// recorded instead of landing on the floor. There is no
			// author-import analogue — a Lidarr add is synchronous and fails
			// loudly, never leaving a pending import to wait on.
			resolved.addFailure = bookAddFailureMetadataUnresolved
			parked, parkErr := s.createPendingUnlocked(resolved)
			if parkErr != nil {
				return nil, err
			}
			parked.Message = musicParkedMessage
			parked.InstanceID = resolved.instanceID
			return parked, nil
		}
		if resolved.mediaType == "book" {
			parkMessage := ""
			switch {
			case errors.Is(err, ErrBookMetadataUnresolved):
				parkMessage = bookParkedMessage
				// This row goes to a human (park_reason stays NULL), but it is
				// not a policy question: the add already ran and failed. Record
				// that so the queue can say so instead of showing it as an
				// ordinary decision.
				resolved.addFailure = bookAddFailureMetadataUnresolved
			case errors.Is(err, chaptarr.ErrAuthorPendingImport):
				parkMessage = bookAuthorImportingMessage
				// Only this create path can park for author_import, and it is by
				// definition auto-approved (approval-required creates never reach
				// the add). The marker makes the row server-owned: hidden from
				// the approval surfaces and retried by the maintenance sweep.
				resolved.parkReason = bookParkReasonAuthorImport
			}
			if parkMessage != "" {
				// The lock is already held for books, so park through the unlocked path.
				parked, parkErr := s.createPendingUnlocked(resolved)
				if parkErr != nil {
					return nil, err
				}
				parked.Message = parkMessage
				if resolved.parkReason == bookParkReasonAuthorImport {
					// Neither stored word is the truth here. Pending narrates an
					// approval that is not happening; requested promises a
					// monitored library record that does not exist yet. The status
					// stays requested for clients that know no other word, and the
					// wait alongside it carries what requested leaves out — which
					// is the only reason a requester can tell an active retry loop
					// from an app that quietly dropped their request.
					wait := s.bookFormatWaitFor(resolved.parkReason, time.Now())
					waits := map[string]BookFormatWait{}
					for format, status := range parked.BookFormats {
						if status == StatusPending {
							parked.BookFormats[format] = StatusRequested
							waits[format] = wait
						}
					}
					parked.BookFormatWaits = waits
					parked.Status = collapseBookStatuses(parked.BookFormats, StatusRequested)
				}
				return parked, nil
			}
		}
		return nil, err
	}
	resolved.title = title
	s.logRequest(resolved, title, status)
	return &CreateResponse{
		Success:            true,
		Status:             status,
		Title:              title,
		InstanceID:         resolved.instanceID,
		BookFormats:        resolved.bookFormats,
		CanonicalForeignID: resolved.responseCanonicalForeignID(),
	}, nil
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
				`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, search_term, park_reason, add_failure_reason)
				 VALUES (?, 0, ?, ?, ?, 'book', ?, ?, ?, ?, ?)`,
				r.userID, r.foreignID, pendingFormat, sqlNullStr(r.instanceID), r.title, StatusPending, sqlNullStr(r.searchTerm), sqlNullStr(r.parkReason), sqlNullStr(r.addFailure),
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
	} else if r.mediaType == "music" {
		// Music pending rows are per-user like movie/TV (no format axis means
		// no shared work item), but keyed on the foreignAlbumId + pinned
		// instance the way books key: two users asking for one album are two
		// rows, and each approval replays an idempotent add.
		res, err = s.db.Exec(
			`INSERT INTO request_log (user_id, tmdb_id, foreign_id, instance_id, media_type, title, status, search_term, add_failure_reason)
			 SELECT ?, 0, ?, ?, 'music', ?, ?, ?, ?
			 WHERE NOT EXISTS (
			     SELECT 1 FROM request_log WHERE user_id = ? AND foreign_id = ? AND media_type = 'music' AND status = ? AND COALESCE(instance_id, '') = COALESCE(?, '')
			 )`,
			r.userID, r.foreignID, sqlNullStr(r.instanceID), r.title, StatusPending, sqlNullStr(r.searchTerm), sqlNullStr(r.addFailure),
			r.userID, r.foreignID, StatusPending, sqlNullStr(r.instanceID),
		)
	} else {
		// The duplicate guard is per target library: the same title pending on
		// a sibling instance is a distinct request, never absorbed. Legacy
		// rows (NULL instance, written before instances were stamped) are
		// absorbed only by the user's effective default — the library their
		// approval would in fact have targeted.
		dupGuard := "instance_id = ?"
		if r.instanceIsUserDefault || r.instanceID == "" {
			dupGuard = "(instance_id = ? OR instance_id IS NULL)"
		}
		res, err = s.db.Exec(
			`INSERT INTO request_log (user_id, tmdb_id, tvdb_id, instance_id, media_type, title, status, season_scope, quality_profile_id)
			 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?
			 WHERE NOT EXISTS (
			     SELECT 1 FROM request_log WHERE user_id = ? AND tmdb_id = ? AND media_type = ? AND status = ? AND `+dupGuard+`
			 )`,
			r.userID, r.tmdbID, sqlNullInt(r.tvdbID), sqlNullStr(r.instanceID), r.mediaType, r.title, StatusPending, sqlNullStr(r.seasonScope), sqlNullInt(r.qualityProfileID),
			r.userID, r.tmdbID, r.mediaType, StatusPending, sqlNullStr(r.instanceID),
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
	// A server-owned park is not an admin work item — nothing pages until the
	// sweep gives up and demotes it to a real approval row.
	if n, _ := res.RowsAffected(); n > 0 && s.notifier != nil && r.parkReason == "" {
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
		if r.mediaType == "music" {
			data["foreign_id"] = r.foreignID
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
		status, title, err = s.addMovie(r)
	case "tv":
		status, title, err = s.addSeries(r)
	case "book":
		return s.addToChaptarr(r)
	case "music":
		return s.addToLidarr(r)
	default:
		return "", "", fmt.Errorf("unsupported media type: %s", r.mediaType)
	}
	// The add just changed that library, and this process may have served its
	// availability digest moments earlier (the per-library status chips read
	// it), so a requester's own add must not hide behind the digest TTL on
	// their next history or chip read. Webhooks cover out-of-band changes;
	// this covers the change Cantinarr itself made.
	if err == nil {
		s.InvalidateAvailabilityDigests(r.instanceID)
	}
	return status, title, err
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
	// One memoized id fetch serves both the alias probe below and the add-time
	// lookup's first term — the identical query must not run twice per request.
	var idFetchResults []chaptarr.LookupResult
	var idFetchErr error
	idFetched := false
	idFetch := func() ([]chaptarr.LookupResult, error) {
		if !idFetched {
			idFetched = true
			idFetchResults, idFetchErr = client.LookupBook(r.foreignID)
		}
		return idFetchResults, idFetchErr
	}

	// The library may already track this book under a different id: the
	// metadata provider keeps alias listings whose id-fetch resolves to the
	// canonical sibling. When the provider itself declares the requested id an
	// alias of a record the library already has, the request completes that
	// record — a requester tapping the duplicate listing means "I want this
	// book", not "track it twice".
	attachID := r.foreignID
	if len(existing) == 0 {
		// Nothing tracked under this id means every remaining outcome — the
		// alias attach, or the add — starts from a metadata lookup, and a
		// request with no title is malformed before any of that: fail it
		// without spending a network call.
		if r.title == "" {
			return "", "", fmt.Errorf("title is required to add a new book")
		}
		if canonicalID, ok := lookupCanonicalAlias(idFetch, r.foreignID); ok {
			aliasTitle, aliasRecords, aliasUnresolved := recordsForForeignID(books, canonicalID)
			if aliasUnresolved {
				return "", "", ErrBookFormatUnresolved
			}
			if len(aliasRecords) > 0 {
				attachID = canonicalID
				existing = aliasRecords
				title = strings.TrimSpace(aliasTitle)
			}
		}
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
		best := records[0]
		for _, rec := range records {
			ids = append(ids, rec.ID)
			available = available || rec.Statistics.BookFileCount > 0
			monitored = monitored || rec.Monitored
			// The record that proves the state is the one worth remembering:
			// a file outranks a bare monitored record outranks the rest.
			if rec.Statistics.BookFileCount > 0 && best.Statistics.BookFileCount == 0 {
				best = rec
			} else if rec.Monitored && best.Statistics.BookFileCount == 0 && !best.Monitored {
				best = rec
			}
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
		r.noteBookRecord(mediaType, best.ID, best.ForeignBookID)
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
		config, ok := bookConfigFromAuthor(author)
		if !ok {
			return "", "", fmt.Errorf("existing book configuration is incomplete for one or more formats")
		}
		config.includeRequestedFormats(r.bookFormat)
		// Missing sibling formats are added under the id the library groups this
		// title by — the attach id — so an alias-fulfilled request never splits
		// one book across two title groups.
		match := &chaptarr.LookupResult{
			ForeignBookID:   attachID,
			Title:           title,
			TitleSlug:       seed.TitleSlug,
			AuthorName:      author.AuthorName,
			ForeignAuthorID: author.ForeignAuthorID,
		}
		if match.TitleSlug == "" {
			match.TitleSlug = fallbackTitleSlug(title)
		}
		for _, mediaType := range missing {
			book, err := s.addChaptarrBookRecord(client, match, config, title, match.TitleSlug, mediaType)
			if err != nil {
				lastErr = err
				r.bookFormats[mediaType] = StatusUnavailable
				continue
			}
			r.bookFormats[mediaType] = StatusRequested
			if book != nil {
				r.noteBookRecord(mediaType, book.ID, book.ForeignBookID)
			}
		}
		return s.finishBookMutation(r, title, lastErr)
	}
	match, lookupErr := lookupBookForAdd(func(term string) ([]chaptarr.LookupResult, error) {
		if term == r.foreignID {
			return idFetch()
		}
		return client.LookupBook(term)
	}, r.foreignID, r.title, r.searchTerm)
	if match == nil {
		if lookupErr != nil {
			return "", "", fmt.Errorf("book lookup failed: %w", lookupErr)
		}
		// No search term produced this foreignBookId, so there is no metadata
		// record to build an add payload from and no record to monitor. Nothing
		// was mutated here; the caller parks the request rather than dropping it.
		lastErr = fmt.Errorf("%w %s", ErrBookMetadataUnresolved, r.foreignID)
		for _, mediaType := range missing {
			r.bookFormats[mediaType] = StatusUnavailable
		}
		return s.finishBookMutation(r, title, lastErr)
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
	config, err := selectBookConfig(qps, mps, folders)
	if err != nil {
		return "", "", err
	}
	config.includeRequestedFormats(r.bookFormat)

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
		book, err := s.addChaptarrBookRecord(client, match, config, title, titleSlug, mediaType)
		if err != nil {
			lastErr = err
			r.bookFormats[mediaType] = StatusUnavailable
			continue
		}
		r.bookFormats[mediaType] = StatusRequested
		if book != nil {
			r.noteBookRecord(mediaType, book.ID, book.ForeignBookID)
		}
	}
	return s.finishBookMutation(r, title, lastErr)
}

// lookupCanonicalAlias resolves the provider's alias→canonical link for a
// foreignBookId, given the id-term fetch. Chaptarr answers an id term with an
// exact fetch of that record, and fetching an alias id returns its canonical
// sibling (verified live; see bookLookupTerms). A response of exactly one
// record filed under a DIFFERENT id is therefore the provider itself declaring
// the two ids one work. Anything else — a miss, an error, a fuzzy multi-hit —
// declares nothing, and the caller must treat the ids as distinct.
func lookupCanonicalAlias(idFetch func() ([]chaptarr.LookupResult, error), foreignID string) (string, bool) {
	results, err := idFetch()
	if err != nil || len(results) != 1 {
		return "", false
	}
	canonical := strings.TrimSpace(results[0].ForeignBookID)
	if canonical == "" || canonical == strings.TrimSpace(foreignID) {
		return "", false
	}
	return canonical, true
}

// lookupBookForAdd re-finds the metadata record a book request points at, so a
// brand-new title can be added. Unlike Radarr — which resolves a movie by
// `term=tmdb:{id}` and therefore cannot miss — Chaptarr's book lookup is a fuzzy
// text search, so the id a requester is holding has to be recovered by searching
// again and picking the row back out. Only an exact foreignBookId match counts,
// which is what makes trying several terms safe: a term that surfaces different
// books contributes nothing, so widening the search can find the right record
// but can never select a different one.
//
// A nil result with a nil error means every term answered and none of them knew
// this id; a nil result with an error means no term could be asked at all.
// [lookup] runs one search term — injected so the caller can reuse an id-term
// fetch it already made.
func lookupBookForAdd(lookup func(term string) ([]chaptarr.LookupResult, error), foreignID, title, searchTerm string) (*chaptarr.LookupResult, error) {
	foreignID = strings.TrimSpace(foreignID)
	var firstErr error
	for _, term := range bookLookupTerms(foreignID, title, searchTerm) {
		results, err := lookup(term)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for i := range results {
			if strings.TrimSpace(results[i].ForeignBookID) == foreignID {
				return &results[i], nil
			}
		}
	}
	return nil, firstErr
}

// bookLookupTerms is the ordered search-term list lookupBookForAdd tries.
//
// The foreignBookId itself comes first: Chaptarr's lookup answers an id term
// with an exact fetch of that record (verified live against Chaptarr 0.9.720:
// `term=gr:297977925` returns exactly that book, an unknown id returns empty),
// which is the same deterministic resolution Radarr gives movies via
// `term=tmdb:{id}`. One caveat keeps the fallbacks alive: the provider resolves
// an alias id to its canonical sibling (two works for one title, id-fetching
// the alias returns the canonical record), and the exact-id gate rightly
// refuses that substitute — the fuzzy terms below then re-find the alias row
// the requester actually chose.
//
// The requester's own search text is that first fallback, because it is the
// one query already proven to return the exact row — they were looking at it
// when they tapped Request. Chaptarr's own UI never faces any of this: its web
// client posts the whole search row straight back and re-searches nothing.
// Cantinarr's client keeps only the id and title, so the server must re-find
// the record here.
//
// The title forms are last, for requests with no search behind them (a
// notification tap, a deep link) on forks whose lookup doesn't answer id
// terms: the exact title, then its headline, because a long title carrying a
// subtitle and a parenthetical series suffix routinely defeats a fuzzy text
// search outright (verified live: the full title above returns zero results
// while its headline finds the book).
func bookLookupTerms(foreignID, title, searchTerm string) []string {
	terms := make([]string, 0, 4)
	add := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" {
			return
		}
		for _, existing := range terms {
			if strings.EqualFold(existing, term) {
				return
			}
		}
		terms = append(terms, term)
	}
	add(foreignID)
	add(searchTerm)
	add(title)
	add(mainBookTitle(title))
	return terms
}

// mainBookTitle reduces a full book title to its headline: trailing
// parenthetical series/part suffixes dropped, then everything from the subtitle
// separator onward. "Some Title: A Subtitle (Part 1) (A Series)" becomes
// "Some Title". It is only ever used as an extra search term, never as identity.
func mainBookTitle(title string) string {
	trimmed := strings.TrimSpace(title)
	for strings.HasSuffix(trimmed, ")") {
		open := strings.LastIndex(trimmed, "(")
		if open <= 0 {
			break
		}
		trimmed = strings.TrimSpace(trimmed[:open])
	}
	if idx := strings.Index(trimmed, ":"); idx > 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	return trimmed
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

func (c *bookAddConfig) includeRequestedFormats(bookFormat string) {
	for _, format := range expandBookFormat(bookFormat) {
		if format == BookFormatEbook {
			c.ebookMonitorFuture = true
		} else if format == BookFormatAudiobook {
			c.audiobookMonitorFuture = true
		}
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
			return bookAddConfig{}, fmt.Errorf("Chaptarr %s quality profile selection is ambiguous", format)
		}
		metadataProfileID, ok := selectBookMetadataProfile(metadataProfiles, format)
		if !ok {
			return bookAddConfig{}, fmt.Errorf("Chaptarr %s metadata profile selection is ambiguous", format)
		}
		root, ok := selectBookRoot(folders, format)
		if !ok {
			return bookAddConfig{}, fmt.Errorf("no accessible root folder available for %s", format)
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
	selected := 0
	for _, profile := range profiles {
		if profile.ID <= 0 {
			continue
		}
		if selected != 0 {
			return 0, false
		}
		selected = profile.ID
	}
	return selected, selected != 0
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
	s.InvalidateBookDigests(r.instanceID)
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

// addChaptarrBookRecord adds one format record (ebook or audiobook) of a book
// and ensures it ends up monitored and searched, returning the created record
// so the caller can persist the identity Chaptarr actually filed it under.
// Chaptarr tracks format at the book level via mediaType, so each requested
// format is its own record.
func (s *Service) addChaptarrBookRecord(client *chaptarr.Client, match *chaptarr.LookupResult, config bookAddConfig, title, titleSlug, mediaType string) (*chaptarr.Book, error) {
	qualityProfileID, metadataProfileID, rootFolderPath := config.forFormat(mediaType)
	addReq := chaptarr.AddBookRequest{
		ForeignBookID:              match.ForeignBookID,
		AuthorID:                   config.authorID,
		Title:                      title,
		TitleSlug:                  titleSlug,
		Monitored:                  true,
		RootFolderPath:             rootFolderPath,
		EbookQualityProfileID:      config.ebookQualityProfileID,
		AudiobookQualityProfileID:  config.audiobookQualityProfileID,
		EbookMetadataProfileID:     config.ebookMetadataProfileID,
		AudiobookMetadataProfileID: config.audiobookMetadataProfileID,
		EbookRootFolderPath:        config.ebookRootFolderPath,
		AudiobookRootFolderPath:    config.audiobookRootFolderPath,
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
				return nil, fmt.Errorf("prepare edition: %w", err)
			}
			if !ok {
				continue // skip a non-object edition element rather than emit junk
			}
			addReq.Editions = append(addReq.Editions, patched)
		}
	}

	book, err := client.AddBook(addReq)
	if err != nil {
		return nil, err
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
			return nil, fmt.Errorf("monitor added %s: %w", mediaType, err)
		}
		_ = client.TriggerBookSearch([]int{book.ID})
	}
	return book, nil
}

func (s *Service) addMovie(r *resolvedRequest) (string, string, error) {
	// A stored/selected library resolves under the acting authority (the
	// approving admin on replay), so a later requester-grant change cannot
	// reroute or strand it. A row with no stamped instance (legacy pending)
	// resolves the requester's own effective default — exactly what its
	// approval would have done when the row was written.
	resolveAs := r.userID
	if r.instanceID != "" && r.actorID != 0 {
		resolveAs = r.actorID
	}
	radarrClient, instanceID, err := s.resolveRadarr(resolveAs, r.instanceID)
	if err != nil {
		return "", "", err
	}
	if radarrClient == nil {
		return "", "", fmt.Errorf("radarr is not configured")
	}
	r.instanceID = instanceID

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
	// Same library-resolution rule as addMovie: stored instance under the
	// acting authority, unstamped legacy rows under the requester's default.
	resolveAs := r.userID
	if r.instanceID != "" && r.actorID != 0 {
		resolveAs = r.actorID
	}
	sonarrClient, instanceID, err := s.resolveSonarr(resolveAs, r.instanceID)
	if err != nil {
		return "", "", err
	}
	if sonarrClient == nil {
		return "", "", fmt.Errorf("sonarr is not configured")
	}
	r.instanceID = instanceID

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
	if tvdbID != 0 {
		lookup, err = sonarrClient.LookupByTVDB(tvdbID)
	}
	if lookup == nil || err != nil {
		if r.title == "" {
			return "", "", fmt.Errorf("series lookup failed: could not resolve a TVDB ID for tmdb %d and no title was provided", r.tmdbID)
		}
		lookup, err = s.lookupSeriesByTitleIdentity(sonarrClient, r.tmdbID, r.title)
		if err != nil {
			return "", "", err
		}
		if lookup.TvdbID != tvdbID {
			// The text search resolved an id the bridge couldn't. That id may
			// name a series the library already tracks (TMDB just has no TVDB
			// mapping for it), so honor the existing-series flows instead of
			// an add Sonarr would reject as a duplicate.
			tvdbID = lookup.TvdbID
			if existing, exErr := sonarrClient.GetSeriesByTVDB(tvdbID); exErr == nil && existing != nil {
				r.tvdbID = tvdbID
				if len(r.seasonNumbers) > 0 {
					if err := s.monitorAndSearchSeasons(sonarrClient, existing, r.seasonNumbers); err != nil {
						return "", "", err
					}
					return StatusRequested, existing.Title, nil
				}
				return s.requestExistingSeries(sonarrClient, existing, r)
			}
		}
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

// lookupSeriesByTitleIdentity resolves a series through Sonarr's text search
// when no TVDB id could be bridged, verifying identity instead of trusting
// relevance order: same-titled series are distinct records — the 2018
// "Tremors" reboot pilot and the 2003 "Tremors" series share a title — and
// blindly taking the first result would fulfil the request with the wrong
// one. The premiere year of the TMDB record the requester actually chose is
// the discriminator; ±1 absorbs TMDB and TVDB dating the same premiere
// differently without reaching the years-apart gap that means a different
// show. When TMDB can't supply a year at all (no client configured, fetch
// failed), the first result is accepted as before — a TMDB-less deployment
// keeps a working request path rather than a dead one.
func (s *Service) lookupSeriesByTitleIdentity(client *sonarr.Client, tmdbID int, title string) (*sonarr.LookupResult, error) {
	candidates, err := client.LookupByTitle(title)
	if err != nil {
		return nil, fmt.Errorf("series lookup failed: %w", err)
	}
	year := s.tmdbTVYear(tmdbID)
	if year == 0 {
		return &candidates[0], nil
	}
	for i := range candidates {
		c := &candidates[i]
		if c.Year == 0 {
			continue
		}
		if diff := c.Year - year; diff >= -1 && diff <= 1 {
			return c, nil
		}
	}
	closest := &candidates[0]
	return nil, fmt.Errorf(
		"series lookup could not verify a match for %q: the requested series premiered in %d, but the closest result is %q (%d) — a same-titled but different series is never substituted",
		title, year, closest.Title, closest.Year,
	)
}

// tmdbTVYear returns the premiere year TMDB records for a series, or 0 when
// no TMDB client is configured or the lookup fails. Callers treat 0 as "no
// year truth available", never as a year.
func (s *Service) tmdbTVYear(tmdbID int) int {
	if s.bridge == nil {
		return 0
	}
	client := s.bridge.TMDB()
	if client == nil {
		return 0
	}
	details, err := client.GetTVDetails(tmdbID)
	if err != nil || details == nil || len(details.FirstAir) < 4 {
		return 0
	}
	year, err := strconv.Atoi(details.FirstAir[:4])
	if err != nil {
		return 0
	}
	return year
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
	return s.statusFor(0, tmdbID, mediaType, "")
}

// statusFor reports a title's availability against userID's source instance:
// an explicit authorized selection, else their per-user effective default.
func (s *Service) statusFor(userID int64, tmdbID int, mediaType, instanceID string) (*StatusResponse, error) {
	switch mediaType {
	case "movie":
		return s.getMovieStatus(userID, tmdbID, instanceID)
	case "tv":
		return s.getTVStatus(userID, tmdbID, instanceID)
	default:
		return &StatusResponse{Status: StatusUnavailable}, nil
	}
}

// GetUserStatus surfaces a user's own pending/denied request first, then falls
// back to the live arr availability that GetStatus reports. An explicit
// instanceID scopes both the request-row overlay and the live read to that
// library; when the user holds more than one granted instance for the media
// type, the response also carries a digest-grade status per granted library.
func (s *Service) GetUserStatus(userID int64, tmdbID int, mediaType, instanceID string) (*StatusResponse, error) {
	// Authorize an explicit selection up front so a forbidden library errors
	// instead of quietly answering with default-library state.
	if instanceID != "" && !s.userIsAdmin(userID) && s.registry != nil {
		serviceType := "radarr"
		if mediaType == "tv" {
			serviceType = "sonarr"
		}
		allowed, err := s.registry.UserCanAccessInstance(userID, instanceID, serviceType)
		if err != nil {
			return nil, fmt.Errorf("check %s access: %w", serviceType, err)
		}
		if !allowed {
			return nil, ErrArrInstanceForbidden
		}
	}

	// A title a kids account may not see has no state to report: it reads
	// unavailable, the same as a title the library does not hold.
	if err := s.checkContentPolicy(userID, s.userIsAdmin(userID), mediaType, tmdbID); err != nil {
		if errors.Is(err, ErrTitleNotAvailable) {
			known := true
			return &StatusResponse{Status: StatusUnavailable, StatusKnown: &known}, nil
		}
		return nil, err
	}

	resp, err := s.userStatusForInstance(userID, tmdbID, mediaType, instanceID)
	if err != nil {
		return nil, err
	}
	resp.InstanceStatuses = s.instanceStatuses(userID, tmdbID, mediaType)
	return resp, nil
}

// userStatusForInstance is the single-library core of GetUserStatus: the
// pending/denied row overlay scoped to the selected library, then the live
// read. An explicit selection never absorbs another library's rows; the
// implicit default also absorbs legacy NULL rows, which factually meant "the
// user's default" when they were written.
func (s *Service) userStatusForInstance(userID int64, tmdbID int, mediaType, instanceID string) (*StatusResponse, error) {
	query := "SELECT status FROM request_log WHERE user_id = ? AND tmdb_id = ? AND media_type = ?"
	args := []interface{}{userID, tmdbID, mediaType}
	if instanceID != "" {
		query += " AND instance_id = ?"
		args = append(args, instanceID)
	} else if defaultID := s.effectiveArrInstanceID(userID, mediaType); defaultID != "" {
		query += " AND (instance_id = ? OR instance_id IS NULL)"
		args = append(args, defaultID)
	}
	query += " ORDER BY requested_at DESC, id DESC LIMIT 1"

	var status string
	err := s.db.QueryRow(query, args...).Scan(&status)
	if err == nil {
		// A pending request isn't in the arr yet, so always surface it.
		if status == StatusPending {
			return &StatusResponse{Status: StatusPending}, nil
		}
		// A denied request shows "denied" only while the title isn't otherwise
		// available; if it later lands in the arr, prefer the live state.
		if status == StatusDenied {
			if live, lerr := s.statusFor(userID, tmdbID, mediaType, instanceID); lerr == nil && live != nil && live.Status != StatusUnavailable {
				return live, nil
			}
			return &StatusResponse{Status: StatusDenied}, nil
		}
	}
	return s.statusFor(userID, tmdbID, mediaType, instanceID)
}

// effectiveArrInstanceID resolves the user's effective default Radarr/Sonarr
// instance id for a media type, or "" when none is configured.
func (s *Service) effectiveArrInstanceID(userID int64, mediaType string) string {
	if s.registry == nil {
		return ""
	}
	serviceType := "radarr"
	if mediaType == "tv" {
		serviceType = "sonarr"
	}
	id, err := s.registry.EffectiveDefaultInstanceID(userID, serviceType)
	if err != nil {
		return ""
	}
	return id
}

// instanceStatuses builds the per-granted-library digest map for a title, or
// nil when the user holds at most one granted instance for the media type.
// Each library's own pending/denied rows take the same precedence they do on
// the headline status, so a request pending on the 4K library never reads
// "not requested" in its chip.
func (s *Service) instanceStatuses(userID int64, tmdbID int, mediaType string) map[string]InstanceStatus {
	if s.registry == nil || userID == 0 {
		return nil
	}
	serviceType := "radarr"
	if mediaType == "tv" {
		serviceType = "sonarr"
	}
	granted, err := s.registry.VisibleInstanceIDs(userID, serviceType)
	if err != nil || len(granted) < 2 {
		return nil
	}

	// Latest own request row per library (NULL rows attribute to the default).
	rowStatus := map[string]string{}
	defaultID := s.effectiveArrInstanceID(userID, mediaType)
	rows, err := s.db.Query(
		"SELECT COALESCE(instance_id, ''), status FROM request_log WHERE user_id = ? AND tmdb_id = ? AND media_type = ? ORDER BY requested_at ASC, id ASC",
		userID, tmdbID, mediaType,
	)
	if err == nil {
		for rows.Next() {
			var rowInstance, status string
			if rows.Scan(&rowInstance, &status) != nil {
				continue
			}
			if rowInstance == "" {
				rowInstance = defaultID
			}
			rowStatus[rowInstance] = status
		}
		_ = rows.Close()
	}

	tvdbID := 0
	if mediaType == "tv" {
		tvdbID = s.resolveTVDBIDCached(tmdbID)
	}

	out := make(map[string]InstanceStatus, len(granted))
	for _, id := range granted {
		status := StatusUnavailable
		known := false
		if mediaType == "tv" {
			if tvdbID != 0 {
				if digest, ok := s.seriesAvailabilityDigestForInstance(id); ok {
					a, found := digest[tvdbID]
					status = seriesAvailabilityStatus(a, found)
					known = true
				}
			}
		} else {
			if digest, ok := s.movieAvailabilityDigestForInstance(id); ok {
				a, found := digest[tmdbID]
				status = movieAvailabilityStatus(a, found)
				known = true
			}
		}
		switch rowStatus[id] {
		case StatusPending:
			status = StatusPending
		case StatusDenied:
			if !known || status == StatusUnavailable {
				status = StatusDenied
			}
		}
		out[id] = InstanceStatus{Status: status}
	}
	return out
}

// resolveTVDBIDCached resolves a TMDB id to a TVDB id via the local mapping
// cache, then the bridge (which repopulates the cache). Returns 0 when no
// mapping is known.
func (s *Service) resolveTVDBIDCached(tmdbID int) int {
	var tvdbID int
	if err := s.db.QueryRow("SELECT tvdb_id FROM tmdb_tvdb_cache WHERE tmdb_id = ?", tmdbID).Scan(&tvdbID); err == nil && tvdbID != 0 {
		return tvdbID
	}
	if s.bridge == nil {
		return 0
	}
	res, err := s.bridge.ResolveTVDBID(tmdbID)
	if err != nil || res == nil {
		return 0
	}
	return res.TVDBID
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
	query := "SELECT COALESCE(book_format, 'both'), status, COALESCE(book_record_id, 0), COALESCE(park_reason, ''), requested_at FROM request_log WHERE (user_id = ? OR status = 'pending') AND foreign_id = ? AND media_type = 'book'"
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
	recordIDs := map[string]int{}
	// Formats whose governing (latest) row is a server-owned author-import
	// park. Its stored status stays pending so the approval machinery keeps
	// working, but requesters read it as requested: the system finishes the
	// request unattended, so no approval story is narrated to anyone.
	parked := map[string]bool{}
	// Each parked format's own requested_at: the wait a requester reads must be
	// dated from the row that is actually waiting, not from "now".
	parkedSince := map[string]time.Time{}
	collapsed := ""
	collapsedParked := false
	for rows.Next() {
		var format, status, parkReason string
		var recordID int
		var requestedAt time.Time
		if err := rows.Scan(&format, &status, &recordID, &parkReason, &requestedAt); err != nil {
			return nil, fmt.Errorf("scan book request status: %w", err)
		}
		isImportPark := status == StatusPending && parkReason == bookParkReasonAuthorImport
		if collapsed == "" {
			collapsed = status // first row is the latest overall
			collapsedParked = isImportPark
		}
		// Rows are newest-first, so only record a format's status the first
		// (latest) time it appears. A "both" row fills both concrete formats.
		for _, f := range expandBookFormat(format) {
			if _, ok := formats[f]; !ok {
				formats[f] = status
				parked[f] = isImportPark
				if isImportPark {
					parkedSince[f] = requestedAt
				}
			}
		}
		// Likewise keep the newest persisted Chaptarr record id per concrete
		// format ("both" rows predate record ids and never carry one).
		if recordID > 0 && format != BookFormatBoth {
			if _, ok := recordIDs[format]; !ok {
				recordIDs[format] = recordID
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read book request status: %w", err)
	}
	canonicalForeignID := ""
	if client != nil {
		projection, lerr := s.liveBookProjectionCached(client, instanceID)
		var live map[string]string
		if lerr == nil {
			live, lerr = projection.formatsFor(foreignID)
		}
		if lerr != nil {
			if errors.Is(lerr, ErrBookFormatUnresolved) {
				known := false
				return &StatusResponse{Status: StatusUnavailable, StatusKnown: &known}, nil
			}
			return nil, lerr
		}
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			liveStatus, exists := live[format]
			if !exists {
				// The projection is keyed by each record's CURRENT foreignBookId,
				// which a Chaptarr metadata refresh can re-key away from the id
				// this request was logged under. The persisted record id is the
				// identity that survives: resolve live truth through it before
				// concluding the request no longer exists.
				if rec, ok := projection.recordByID(recordIDs[format]); ok {
					liveStatus, exists = rec.Status, true
					if canonicalForeignID == "" && rec.ForeignID != "" && rec.ForeignID != foreignID {
						canonicalForeignID = rec.ForeignID
					}
				}
			}
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

	// The park mapping runs after the live overlay on purpose: live truth (a
	// completed add, a file) already outranked the stored pending above, so
	// only a still-parked pending flips to the requester-facing "requested".
	// The wait context is built in the same pass and therefore inherits that
	// ordering: the moment the library really has the record, the explanation
	// for its absence disappears with it.
	waits := map[string]BookFormatWait{}
	for format, status := range formats {
		if status == StatusPending && parked[format] {
			formats[format] = StatusRequested
			waits[format] = s.bookFormatWaitFor(bookParkReasonAuthorImport, parkedSince[format])
		}
	}
	if collapsed == StatusPending && collapsedParked {
		collapsed = StatusRequested
	}

	if len(formats) == 0 {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}
	resp := &StatusResponse{BookFormats: formats, BookFormatWaits: waits, CanonicalForeignID: canonicalForeignID}
	resp.Status = collapseBookStatuses(formats, collapsed)
	return resp, nil
}

const bookLiveProjectionTTL = 15 * time.Second

type bookLiveProjection struct {
	Formats    map[string]map[string]string `json:"formats"`
	Unresolved map[string]bool              `json:"unresolved,omitempty"`
	// Records indexes every live record by its numeric Chaptarr id with that
	// record's own status and current foreignBookId. Request history persists
	// the record id it fulfilled, so status reads can follow a record whose
	// foreignBookId a metadata refresh re-keyed away from the requested id.
	Records map[int]bookLiveRecord `json:"records,omitempty"`
}

// bookLiveRecord is one live record's requester-facing status plus the
// foreignBookId Chaptarr currently files it under.
type bookLiveRecord struct {
	Status    string `json:"status"`
	ForeignID string `json:"foreignId,omitempty"`
}

func (p *bookLiveProjection) recordByID(id int) (bookLiveRecord, bool) {
	if id <= 0 {
		return bookLiveRecord{}, false
	}
	rec, ok := p.Records[id]
	return rec, ok
}

// liveBookFormats returns one title's slice of a short-lived instance-wide
// projection. Search grids call book-status once per row, so fetching the full
// library/queue per title would be an accidental N+1 load on Chaptarr.
func (s *Service) liveBookFormats(client *chaptarr.Client, instanceID, foreignID string) (map[string]string, error) {
	projection, err := s.liveBookProjectionCached(client, instanceID)
	if err != nil {
		return nil, err
	}
	return projection.formatsFor(foreignID)
}

func (s *Service) liveBookProjectionCached(client *chaptarr.Client, instanceID string) (*bookLiveProjection, error) {
	cacheKey := "book-live:" + instanceID
	if projection, ok := s.cachedBookProjection(cacheKey); ok {
		return projection, nil
	}
	projectionLock := s.projectionLock(instanceID)
	projectionLock.Lock()
	defer projectionLock.Unlock()
	if projection, ok := s.cachedBookProjection(cacheKey); ok {
		return projection, nil
	}
	projection, err := buildBookLiveProjection(client)
	if err != nil {
		return nil, err
	}
	s.cacheBookProjection(cacheKey, projection)
	return projection, nil
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
	if queue, err := client.GetQueueDetailed(); err == nil {
		for _, item := range queue {
			if item.BookID != 0 && bookQueueItemDownloading(item) {
				queued[item.BookID] = true
			}
		}
	}
	projection := &bookLiveProjection{
		Formats:    make(map[string]map[string]string),
		Unresolved: make(map[string]bool),
		Records:    make(map[int]bookLiveRecord, len(books)),
	}
	for _, book := range books {
		if book.ID <= 0 {
			continue
		}
		status := StatusUnavailable
		switch {
		case book.Statistics.BookFileCount > 0:
			status = StatusAvailable
		case queued[book.ID]:
			status = StatusDownloading
		case book.Monitored:
			status = StatusRequested
		}
		projection.Records[book.ID] = bookLiveRecord{Status: status, ForeignID: book.ForeignBookID}
	}
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

func (s *Service) getMovieStatus(userID int64, tmdbID int, instanceID string) (*StatusResponse, error) {
	radarrClient, _, err := s.resolveRadarr(userID, instanceID)
	if err != nil {
		return nil, err
	}
	if radarrClient == nil {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}

	movie, err := radarrClient.GetMovieByTMDB(tmdbID)
	if err != nil || movie == nil {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}

	// Release dates ride along on every branch below: the movie is in the
	// library, so they are known, and which of them still matters is the
	// client's decision.
	releases := movieReleases(movie)

	if movie.HasFile {
		return &StatusResponse{Status: StatusAvailable, Progress: 1.0, Releases: releases}, nil
	}

	queue, err := radarrClient.GetQueue()
	if err == nil {
		for _, item := range queue {
			if item.MovieID == movie.ID {
				progress := 0.0
				if item.Size > 0 {
					progress = (item.Size - item.Sizeleft) / item.Size
				}
				return &StatusResponse{Status: StatusDownloading, Progress: progress, Releases: releases}, nil
			}
		}
	}

	if movie.Monitored {
		return &StatusResponse{Status: StatusRequested, Progress: 0, Releases: releases}, nil
	}

	return &StatusResponse{Status: StatusUnavailable, Releases: releases}, nil
}

func (s *Service) getTVStatus(userID int64, tmdbID int, instanceID string) (*StatusResponse, error) {
	sonarrClient, _, err := s.resolveSonarr(userID, instanceID)
	if err != nil {
		return nil, err
	}
	if sonarrClient == nil {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}

	tvdbID := s.resolveTVDBIDCached(tmdbID)
	if tvdbID == 0 {
		return &StatusResponse{Status: StatusUnavailable}, nil
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
	parkReason string
}

// GetRequests returns the user's request history with each row's status
// recomputed from the live arr libraries, so the list tracks reality instead
// of the point-in-time snapshot request_log stores (nothing ever updates those
// rows — a "requested" title that Sonarr has long since imported, or that an
// admin deleted directly in the arr, would otherwise read wrong forever).
func (s *Service) GetRequests(userID int64) ([]RequestLog, error) {
	rows, err := s.db.Query(
		`SELECT tmdb_id, tvdb_id, foreign_id, book_format, instance_id, media_type, title, status, deny_reason, park_reason, requested_at
		 FROM (
		   SELECT r.tmdb_id,
		          COALESCE(r.tvdb_id, 0) AS tvdb_id,
		          COALESCE(r.foreign_id, '') AS foreign_id,
		          COALESCE(r.book_format, '') AS book_format,
		          COALESCE(r.instance_id, '') AS instance_id,
		          r.media_type, r.title, r.status,
		          COALESCE(r.deny_reason, '') AS deny_reason,
		          COALESCE(r.park_reason, '') AS park_reason,
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
		          COALESCE(r.park_reason, ''),
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
		if err := rows.Scan(&r.log.TmdbID, &r.tvdbID, &r.foreignID, &r.bookFormat, &r.instanceID, &r.log.MediaType, &r.log.Title, &r.log.Status, &r.log.DenyReason, &r.parkReason, &r.log.RequestedAt); err != nil {
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
		// After the overlay (whose "pending shows as-is" protection carried the
		// stored status through), a server-owned author-import park reads as
		// requested — same mapping as the detail status endpoint, and it carries
		// the same wait, so a requester scrolling their history is told why
		// rather than being shown a request that looks finished.
		if history[i].log.Status == StatusPending && history[i].parkReason == bookParkReasonAuthorImport {
			history[i].log.Status = StatusRequested
			wait := s.bookFormatWaitFor(bookParkReasonAuthorImport, history[i].log.RequestedAt)
			history[i].log.BookFormatWait = &wait
		}
		requests[i] = history[i].log
	}
	return s.hideBlockedRequests(userID, s.userIsAdmin(userID), requests)
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
	// Movie/TV digests are memoized per row-stored instance (mirroring the
	// book branch below); the "" key is the user's effective default, which is
	// also what legacy rows written before instance stamping factually meant.
	var (
		movieDigests      = map[string]map[int]movieAvailability{}
		movieDigestDone   = map[string]bool{}
		movieDigestOK     = map[string]bool{}
		seriesDigests     = map[string]map[int]seriesAvailability{}
		seriesDigestDone  = map[string]bool{}
		seriesDigestOK    = map[string]bool{}
		bookClients       = map[string]*chaptarr.Client{}
		bookInstanceDone  = map[string]bool{}
		bookInstanceOK    = map[string]bool{}
		musicClients      = map[string]*lidarr.Client{}
		musicInstanceDone = map[string]bool{}
		musicInstanceOK   = map[string]bool{}
	)

	for i := range history {
		row := &history[i]
		if row.log.Status == StatusPending {
			continue
		}

		live := ""
		switch row.log.MediaType {
		case "movie":
			instanceID := row.instanceID
			if !movieDigestDone[instanceID] {
				if instanceID == "" {
					movieDigests[instanceID], movieDigestOK[instanceID] = s.movieAvailabilityDigest(userID)
				} else {
					movieDigests[instanceID], movieDigestOK[instanceID] = s.movieAvailabilityDigestForInstance(instanceID)
				}
				movieDigestDone[instanceID] = true
			}
			if !movieDigestOK[instanceID] {
				continue
			}
			a, found := movieDigests[instanceID][row.log.TmdbID]
			live = movieAvailabilityStatus(a, found)

		case "tv":
			instanceID := row.instanceID
			if !seriesDigestDone[instanceID] {
				if instanceID == "" {
					seriesDigests[instanceID], seriesDigestOK[instanceID] = s.seriesAvailabilityDigest(userID)
				} else {
					seriesDigests[instanceID], seriesDigestOK[instanceID] = s.seriesAvailabilityDigestForInstance(instanceID)
				}
				seriesDigestDone[instanceID] = true
			}
			if !seriesDigestOK[instanceID] {
				continue
			}
			series := seriesDigests[instanceID]
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

		case "music":
			// Legacy unscoped rows cannot be safely attributed after a user's
			// default changes, so their point-in-time state remains untouched.
			if row.foreignID == "" || row.instanceID == "" {
				continue
			}
			instanceID := row.instanceID
			if !musicInstanceDone[instanceID] {
				client, resolvedID, err := s.resolveLidarr(userID, instanceID)
				if err == nil && client != nil && resolvedID == instanceID {
					musicClients[instanceID] = client
					musicInstanceOK[instanceID] = true
				}
				musicInstanceDone[instanceID] = true
			}
			if !musicInstanceOK[instanceID] {
				continue
			}
			projection, err := s.liveMusicProjectionCached(musicClients[instanceID], instanceID)
			if err != nil {
				// Transient failures retain the stored state until live truth
				// is available again.
				continue
			}
			status, exists := projection.Statuses[row.foreignID]
			if !exists {
				// Like the book overlay, history rows are not re-keyed through
				// record ids here; the detail status endpoint does that. A
				// record the projection no longer keys by this id reads
				// unavailable.
				status = StatusUnavailable
			}
			live = status

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
	// Server-owned parks are excluded like scanPending: the badge counts
	// decisions a human can make, and a park the sweep is retrying offers none.
	// A park_reason this build does not recognise is counted — nothing is
	// retrying it, so a person is the only one who can.
	placeholders, args := serverOwnedParkSQL()
	var n int
	if err := s.db.QueryRow(
		"SELECT COUNT(*) FROM request_log WHERE status = ? AND (park_reason IS NULL OR park_reason NOT IN ("+placeholders+"))",
		append([]interface{}{StatusPending}, args...)...,
	).Scan(&n); err != nil {
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

// ListPending returns the admin approval queue: the pending rows that are
// waiting on a person's decision, and only those.
func (s *Service) ListPending() ([]PendingRequest, error) {
	out, err := s.scanPending(false)
	if err != nil {
		return nil, err
	}
	// Artwork resolution reaches the network, so it runs only once the rows and
	// their connection are released.
	s.attachPosterPaths(out)
	return out, nil
}

// ListWaiting returns the mirror image of the approval queue: the pending rows
// the server owns and retries itself. They are deliberately kept out of
// ListPending — that list is an actionable queue, and a client old or new would
// offer Approve on a row whose approval the server refuses. They are equally
// deliberately not hidden: before this list existed, a request Cantinarr was
// actively retrying was indistinguishable from one it had silently dropped,
// with the reason visible only in the database for the first 24 hours.
//
// Nothing here is actionable, so nothing here is counted: PendingCount, the
// badge, and the request_pending push all continue to skip these rows until the
// sweep gives up and demotes one into a real approval.
func (s *Service) ListWaiting() ([]PendingRequest, error) {
	out, err := s.scanPending(true)
	if err != nil {
		return nil, err
	}
	s.attachPosterPaths(out)
	return out, nil
}

// scanPending reads one side of the pending set. parked selects the rows the
// sweep is actually retrying (see serverOwnedParkReasons) instead of the
// human-decision rows. The two filters are exact complements over the same
// list, so every pending row lands in exactly one of them — a row can never be
// hidden from both, which is how one would be stranded.
func (s *Service) scanPending(parked bool) ([]PendingRequest, error) {
	placeholders, parkArgs := serverOwnedParkSQL()
	parkFilter := "(r.park_reason IS NULL OR r.park_reason NOT IN (" + placeholders + "))"
	if parked {
		parkFilter = "r.park_reason IN (" + placeholders + ")"
	}
	rows, err := s.db.Query(
		`SELECT r.id, r.user_id, COALESCE(u.username, ''), r.tmdb_id, COALESCE(r.tvdb_id, 0), COALESCE(r.foreign_id, ''), r.media_type, r.title, COALESCE(r.book_format, ''), COALESCE(r.instance_id, ''),
		        COALESCE(si.name, ''),
		        CASE WHEN r.media_type = 'book' THEN 1 + (SELECT COUNT(*) FROM book_request_waiters bw WHERE bw.request_id = r.id AND bw.user_id <> r.user_id) ELSE 1 END,
		        COALESCE(r.season_scope, ''), COALESCE(r.quality_profile_id, 0), r.requested_at, COALESCE(r.park_reason, ''), COALESCE(r.add_failure_reason, '')
		 FROM request_log r
		 LEFT JOIN users u ON u.id = r.user_id
		 LEFT JOIN service_instances si ON si.id = r.instance_id
		 WHERE r.status = ? AND `+parkFilter+` ORDER BY r.requested_at ASC`,
		append([]interface{}{StatusPending}, parkArgs...)...,
	)
	if err != nil {
		return nil, fmt.Errorf("query pending requests: %w", err)
	}
	defer rows.Close()

	var out []PendingRequest
	for rows.Next() {
		var p PendingRequest
		var parkReason string
		if err := rows.Scan(&p.ID, &p.UserID, &p.Username, &p.TmdbID, &p.TvdbID, &p.ForeignID, &p.MediaType, &p.Title, &p.BookFormat, &p.InstanceID, &p.InstanceName, &p.RequesterCount, &p.SeasonScope, &p.QualityProfileID, &p.RequestedAt, &parkReason, &p.AddFailureReason); err != nil {
			return nil, fmt.Errorf("scan pending request: %w", err)
		}
		if p.MediaType == "book" {
			p.BookFormat = normalizeBookFormat(p.BookFormat)
		}
		if parkReason != "" {
			wait := s.bookFormatWaitFor(parkReason, p.RequestedAt)
			p.WaitReason = wait.Reason
			p.LastAttemptAt = wait.LastAttemptAt
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// loadRequest reads a request_log row into a resolvedRequest plus its status.
func (s *Service) loadRequest(requestID int64) (*resolvedRequest, string, error) {
	var r resolvedRequest
	var status string
	err := s.db.QueryRow(
		"SELECT user_id, tmdb_id, COALESCE(tvdb_id, 0), COALESCE(foreign_id, ''), COALESCE(book_format, ''), COALESCE(instance_id, ''), media_type, title, status, COALESCE(season_scope, ''), COALESCE(quality_profile_id, 0), COALESCE(search_term, '') FROM request_log WHERE id = ?",
		requestID,
	).Scan(&r.userID, &r.tmdbID, &r.tvdbID, &r.foreignID, &r.bookFormat, &r.instanceID, &r.mediaType, &r.title, &status, &r.seasonScope, &r.qualityProfileID, &r.searchTerm)
	if err == sql.ErrNoRows {
		return nil, "", fmt.Errorf("request not found")
	}
	if err != nil {
		return nil, "", fmt.Errorf("load request: %w", err)
	}
	if r.mediaType == "book" {
		r.bookFormat = normalizeBookFormat(r.bookFormat)
		if !validBookFormat(r.bookFormat) {
			return nil, "", fmt.Errorf("request has unsupported book_format %q", r.bookFormat)
		}
	}
	// An explicit season list was stored as JSON in season_scope; decode it so
	// approval replays the explicit season selection the requester chose.
	r.seasonNumbers = decodeSeasonNumbers(r.seasonScope)
	return &r, status, nil
}

// isAuthorImportParked reports whether the row is still a server-owned
// author-import park — i.e. the maintenance sweep is still retrying it.
func (s *Service) isAuthorImportParked(requestID int64) bool {
	var reason sql.NullString
	if err := s.db.QueryRow(
		"SELECT park_reason FROM request_log WHERE id = ?", requestID,
	).Scan(&reason); err != nil {
		return false
	}
	return reason.Valid && reason.String == bookParkReasonAuthorImport
}

// ApproveRequest fulfills a pending request (optionally with admin overrides)
// and marks the row approved. The arr add reuses the normal add path.
func (s *Service) ApproveRequest(adminID, requestID int64, override *DecisionOverride) (*CreateResponse, error) {
	resp, err := s.fulfillPendingRequest(adminID, requestID, override, false)
	if err != nil && errors.Is(err, chaptarr.ErrAuthorPendingImport) {
		// The row is untouched either way, but who watches next depends on
		// whether the sweep still owns it. While parked, approving early is a
		// non-event and the plan is honest. A demoted row is no longer
		// watched, so promising automatic completion there would narrate a
		// watch that is not running — name the real verbs instead.
		if s.isAuthorImportParked(requestID) {
			return nil, fmt.Errorf("%w — the request stays queued and completes automatically once the import lands", chaptarr.ErrAuthorPendingImport)
		}
		return nil, fmt.Errorf("%w — the wait on this request ended; choose Try again to resume watching it, or close the request", chaptarr.ErrAuthorPendingImport)
	}
	if err != nil && errors.Is(err, ErrBookMetadataUnresolved) {
		// Approving replayed an add that had already failed the same way, and
		// retrying changes nothing until the library can find the record. The
		// bare error read as a transient glitch and invited another Approve;
		// name the one action that actually moves this.
		return nil, fmt.Errorf("%w — add this book in the library first, then approve", ErrBookMetadataUnresolved)
	}
	return resp, err
}

// bookWaitExtendedMessage is the admin-facing confirmation that a demoted
// author-import row went back to being watched.
const bookWaitExtendedMessage = "Waiting resumed: the library is importing this author again, and the request completes automatically when it lands."

// ExtendBookWait is the admin's "try again" on a demoted author-import row —
// the opposite verb to closing it. It replays the add once (a human asking to
// keep trying is the one legitimate reason to touch Chaptarr's queue): the
// replay either completes the request on the spot because the author landed
// since demotion, re-queues an import that was cancelled, or merges into the
// live one — and on the still-importing refusal it re-parks the row so the
// sweep watches it again. A wait that ended in Chaptarr's declared-terminal
// Failed state also asks the arr to reopen the import (best-effort): Chaptarr
// never resumes a declared terminal on its own.
func (s *Service) ExtendBookWait(adminID, requestID int64) (*CreateResponse, error) {
	var mediaType, status string
	var parkReason, addFailure sql.NullString
	err := s.db.QueryRow(
		"SELECT media_type, status, park_reason, COALESCE(add_failure_reason, '') FROM request_log WHERE id = ?",
		requestID,
	).Scan(&mediaType, &status, &parkReason, &addFailure)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("request not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load request for wait extension: %w", err)
	}
	if mediaType != "book" || status != StatusPending {
		return nil, fmt.Errorf("only a pending book request can keep waiting")
	}
	if parkReason.Valid {
		return nil, fmt.Errorf("this request is already being watched")
	}
	if !isBookImportAddFailure(addFailure.String) {
		return nil, fmt.Errorf("this request is not waiting on an author import")
	}
	importWasFailed := addFailure.String == bookAddFailureImportFailed

	resp, err := s.fulfillPendingRequest(adminID, requestID, nil, false)
	if err == nil {
		// The author landed since the demotion; nothing left to wait for.
		return resp, nil
	}
	if !errors.Is(err, chaptarr.ErrAuthorPendingImport) {
		// A different failure is a real answer the admin must see; the row
		// stays in the queue with it.
		return nil, err
	}
	res, err := s.db.Exec(
		`UPDATE request_log SET park_reason = ?, add_failure_reason = NULL
		 WHERE id = ? AND status = ? AND park_reason IS NULL`,
		bookParkReasonAuthorImport, requestID, StatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("re-park book request: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("request was decided concurrently")
	}
	if importWasFailed {
		s.reopenFailedAuthorImport(requestID)
	}
	return &CreateResponse{Success: true, Status: StatusRequested, Message: bookWaitExtendedMessage}, nil
}

// reopenFailedAuthorImport best-effort asks Chaptarr to retry a pending import
// it had declared terminally failed. The re-park stands either way: the sweep
// re-demotes within a pass if the import really is dead, so a failed reopen
// costs one visible round-trip through the queue rather than a silent strand.
func (s *Service) reopenFailedAuthorImport(requestID int64) {
	r, _, err := s.loadRequest(requestID)
	if err != nil {
		log.Printf("request: reopen author import for request %d: %v", requestID, err)
		return
	}
	client, _, err := s.resolveChaptarr(r.userID, r.instanceID)
	if err != nil || client == nil {
		log.Printf("request: reopen author import for request %d: resolve chaptarr: %v", requestID, err)
		return
	}
	foreignAuthorID, ok := s.lookupParkedAuthorID(client, r.foreignID)
	if !ok || foreignAuthorID == "" {
		return
	}
	importStatus, err := client.GetAuthorImportStatus(foreignAuthorID)
	if err != nil || !importStatus.Pending {
		return
	}
	if err := client.RetryPendingAuthorImport(importStatus.PendingID); err != nil {
		log.Printf("request: reopen author import for request %d: %v", requestID, err)
	}
}

// cancelAuthorImportForDeniedRequest best-effort removes Chaptarr's queued
// author import behind a denied book request. The queued row carries the whole
// add intent — the monitored book and the search flag — so leaving it armed
// would deliver the content whenever the import finally lands, contradicting
// the denial. One guard: another request still waiting on the same author
// keeps the import alive, because cancelling it would strand that wait.
// Failures only log — the denial stands either way, and a book that arrives
// despite a failed cancel is visible (library record, new_book alert), never
// silent.
func (s *Service) cancelAuthorImportForDeniedRequest(requestID int64, r *resolvedRequest) {
	client, _, err := s.resolveChaptarr(r.userID, r.instanceID)
	if err != nil || client == nil {
		log.Printf("request: cancel author import for denied request %d: resolve chaptarr: %v", requestID, err)
		return
	}
	foreignAuthorID, ok := s.lookupParkedAuthorID(client, r.foreignID)
	if !ok || foreignAuthorID == "" {
		return
	}
	importStatus, err := client.GetAuthorImportStatus(foreignAuthorID)
	if err != nil || !importStatus.Pending {
		return
	}
	if s.otherRequestWaitsOnAuthor(client, requestID, r.instanceID, foreignAuthorID) {
		log.Printf("request: denied request %d leaves the author import queued; another request still waits on author %s", requestID, foreignAuthorID)
		return
	}
	if err := client.CancelPendingAuthorImport(importStatus.PendingID); err != nil {
		log.Printf("request: cancel author import for denied request %d: %v", requestID, err)
		return
	}
	log.Printf("request: denied request %d cancelled its queued author import (%s)", requestID, foreignAuthorID)
}

// otherRequestWaitsOnAuthor reports whether another open wait on the same
// instance resolves to the same author: a parked row, or a demoted one still
// holding an import add-failure. Each candidate's author comes from a live
// id-fetch, because request_log deliberately stores no author id. Every
// blindness answers true — a read failure must never cancel someone else's
// wait.
func (s *Service) otherRequestWaitsOnAuthor(client *chaptarr.Client, requestID int64, instanceID, foreignAuthorID string) bool {
	parkSQL, parkArgs := serverOwnedParkSQL()
	failSQL, failArgs := bookImportAddFailureSQL()
	args := append([]interface{}{StatusPending, instanceID, requestID}, append(parkArgs, failArgs...)...)
	rows, err := s.db.Query(
		`SELECT DISTINCT COALESCE(foreign_id, '') FROM request_log
		 WHERE media_type = 'book' AND status = ? AND COALESCE(instance_id, '') = ? AND id != ?
		   AND (park_reason IN (`+parkSQL+`) OR add_failure_reason IN (`+failSQL+`))`,
		args...,
	)
	if err != nil {
		log.Printf("request: list sibling author waits: %v", err)
		return true
	}
	var foreignIDs []string
	for rows.Next() {
		var fid string
		if err := rows.Scan(&fid); err != nil {
			_ = rows.Close()
			log.Printf("request: scan sibling author wait: %v", err)
			return true
		}
		if fid != "" {
			foreignIDs = append(foreignIDs, fid)
		}
	}
	closeErr := rows.Err()
	_ = rows.Close()
	if closeErr != nil {
		log.Printf("request: read sibling author waits: %v", closeErr)
		return true
	}
	for _, fid := range foreignIDs {
		siblingAuthor, ok := s.lookupParkedAuthorID(client, fid)
		if !ok {
			return true
		}
		if siblingAuthor == foreignAuthorID {
			return true
		}
	}
	return false
}

// fulfillPendingRequest executes a pending request_log row and materializes the
// outcome for every subscriber. It is the shared core of an admin approval and
// of the park-maintenance sweep's system completion. A system completion
// (actorID 0, system true) executes under the requester's own authority — the
// park only ever came from their auto-approved create — records no approver,
// and skips the owner's decision notification: the owner was promised
// automatic completion and their status already reads requested, so there is
// no decision to announce to them. Non-owner waiters are still notified; their
// visible state changes.
func (s *Service) fulfillPendingRequest(actorID, requestID int64, override *DecisionOverride, system bool) (*CreateResponse, error) {
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
	}
	if r.mediaType == "music" {
		if strings.TrimSpace(r.instanceID) == "" {
			return nil, fmt.Errorf("pending music request has no pinned Lidarr instance")
		}
		musicLock := s.bookLock(r.instanceID + "\x00" + r.foreignID)
		musicLock.Lock()
		defer musicLock.Unlock()
	}
	// The request's instance was authorized and stamped at submission. Execute
	// the decision under the approving admin so a later requester-grant change
	// cannot reroute or strand it; history remains owned by r.userID. A
	// system completion executes under the requester themselves instead.
	if system {
		r.actorID = r.userID
	} else {
		r.actorID = actorID
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
	// A system completion has no approver; NULL preserves "nobody decided this"
	// for history, and the fulfilled row sheds its park marker either way.
	approvedBy := sql.NullInt64{Int64: actorID, Valid: actorID != 0}
	res, err := tx.Exec(
		// The add just succeeded, so whatever failed before is history: the row
		// sheds both its park marker and its failure note.
		"UPDATE request_log SET status = ?, title = ?, tvdb_id = ?, book_format = ?, book_record_id = ?, instance_id = ?, season_scope = ?, quality_profile_id = ?, approved_by = ?, park_reason = NULL, add_failure_reason = NULL, decided_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?",
		primaryStatus, title, sqlNullInt(r.tvdbID), sqlNullStr(primaryFormat), sqlNullInt(r.bookRecordIDs[primaryFormat]), sqlNullStr(r.instanceID), sqlNullStr(r.seasonScope), sqlNullInt(r.qualityProfileID), approvedBy, requestID, StatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("update request: %w", err)
	}
	// Lost a race with a concurrent decision: skip the duplicate notification.
	if n, _ := res.RowsAffected(); n == 0 {
		return &CreateResponse{Success: true, Status: newStatus, Title: title, BookFormats: r.bookFormats, CanonicalForeignID: r.responseCanonicalForeignID()}, nil
	}
	if r.mediaType == "book" && len(r.bookFormats) > 1 {
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			formatStatus, ok := r.bookFormats[format]
			if !ok || format == primaryFormat || formatStatus == StatusUnavailable {
				continue
			}
			_, err = tx.Exec(
				`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, book_record_id, instance_id, media_type, title, status, approved_by, decided_at)
				 VALUES (?, 0, ?, ?, ?, ?, 'book', ?, ?, ?, CURRENT_TIMESTAMP)`,
				r.userID, r.foreignID, format, sqlNullInt(r.bookRecordIDs[format]), sqlNullStr(r.instanceID), title, formatStatus, approvedBy,
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
					`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, book_record_id, instance_id, media_type, title, status, approved_by, decided_at)
					 VALUES (?, 0, ?, ?, ?, ?, 'book', ?, ?, ?, CURRENT_TIMESTAMP)`,
					subscriber.UserID, r.foreignID, format, sqlNullInt(r.bookRecordIDs[format]), sqlNullStr(r.instanceID), title, formatStatus, approvedBy,
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
				`INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, search_term)
				 VALUES (?, 0, ?, ?, ?, 'book', ?, ?, ?)`,
				r.userID, r.foreignID, format, r.instanceID, title, StatusPending, sqlNullStr(r.searchTerm),
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
		// Music (like books) has no TMDB id; the foreign id is the identity a
		// client can deep-link on. Movie/TV rows carry no foreign_id, so the
		// field is omitted and their payloads are unchanged. Format fields are
		// a book concept and never ride a music payload.
		if r.foreignID != "" {
			data["foreign_id"] = r.foreignID
			data["instance_id"] = r.instanceID
			if r.mediaType == "book" {
				data["book_format"] = primaryFormat
				data["book_formats"] = r.bookFormats
			}
		}
		for _, subscriber := range audience {
			s.notifier.NotifyUser(subscriber.UserID, "request_decision", data)
		}
	}
	if s.notifier != nil && r.mediaType == "book" {
		for _, subscriber := range audience {
			// A system completion stays invisible to the owner on purpose, but
			// NOT for the reason it used to be: "their status already read
			// requested, so nothing changed" stopped being true the moment the
			// park grew a durable BookFormatWait, because the owner now watches
			// their request go from waiting to requested. The reason that
			// survives is narrower and still holds — nobody decided anything, so
			// a "decision" push would invent an approval that never happened, and
			// the outcome the owner actually asked about still reaches them when
			// the file lands. Their in-app state converges on its own: the format
			// panel re-reads while a wait is showing, and the wait disappears
			// with the park.
			if system && subscriber.UserID == r.userID {
				continue
			}
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
	return &CreateResponse{Success: true, Status: newStatus, Title: title, BookFormats: r.bookFormats, CanonicalForeignID: r.responseCanonicalForeignID()}, nil
}

// BookImportStallSink receives transitions of the "book requests are stuck
// behind the library's author-metadata import" condition for one Chaptarr
// instance, so the wiring layer can surface it as an auto-resolving system
// issue without this package importing the issue store.
type BookImportStallSink interface {
	RecordBookImportStall(instanceID, instanceName string, waitingTitles []string, healthy bool) error
}

// SetBookImportStallSink wires the stall reporter (nil disables reporting).
func (s *Service) SetBookImportStallSink(sink BookImportStallSink) {
	s.bookImportStallSink = sink
}

// ResumeBookParks runs one park sweep off the maintenance cadence, in the
// background. It is the webhook seam for Chaptarr's AuthorAdded callback —
// the event that fires at the exact moment a queued author import lands — so
// a waiting request completes in seconds instead of at the next tick. Sweep
// passes are serialized, so a callback burst cannot race the ticker.
func (s *Service) ResumeBookParks() {
	go s.SweepParkedBookRequests()
}

// StartBookParkMaintenance runs SweepParkedBookRequests on a fixed cadence
// until ctx ends.
func (s *Service) StartBookParkMaintenance(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(bookParkRetryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.SweepParkedBookRequests()
			}
		}
	}()
}

// SweepParkedBookRequests is one maintenance pass over every server-owned
// author-import park. Chaptarr owns the retry loop for a queued author import
// (unbounded, on its own schedule), so each pass reads the arr's live verdict
// per row and acts only on real exits: import landed → complete the request;
// import declared failed or cancelled → demote to the human approval queue;
// still importing → leave it alone. There is no time-based give-up (see
// bookParkStallAfter). Afterwards, per-instance stall health is reported so a
// long-running Chaptarr metadata import becomes one auto-resolving system
// issue.
func (s *Service) SweepParkedBookRequests() {
	s.parkSweepRunMu.Lock()
	defer s.parkSweepRunMu.Unlock()
	rows, err := s.db.Query(
		`SELECT id, COALESCE(user_id, 0), COALESCE(foreign_id, ''), COALESCE(instance_id, ''), title
		 FROM request_log
		 WHERE media_type = 'book' AND status = ? AND park_reason = ?
		 ORDER BY id ASC`,
		StatusPending, bookParkReasonAuthorImport,
	)
	if err != nil {
		log.Printf("request: query parked book requests: %v", err)
		return
	}
	var parked []parkedBookRow
	for rows.Next() {
		var row parkedBookRow
		if err := rows.Scan(&row.id, &row.userID, &row.foreignID, &row.instanceID, &row.title); err != nil {
			_ = rows.Close()
			log.Printf("request: scan parked book request: %v", err)
			return
		}
		parked = append(parked, row)
	}
	closeErr := rows.Err()
	_ = rows.Close()
	if closeErr != nil {
		log.Printf("request: read parked book requests: %v", closeErr)
		return
	}

	for _, row := range parked {
		s.probeParkedBookRequest(row)
	}

	// Every parked row was just probed, so one timestamp dates all of them.
	// Stamped after the loop, not before: an early return above means no probe
	// was made, and claiming one would be the same kind of lie this whole change
	// is removing.
	s.markParkSweep(time.Now())

	s.reportBookImportStalls()
}

// parkedBookRow is one server-owned author-import park as the sweep sees it.
type parkedBookRow struct {
	id         int64
	userID     int64
	foreignID  string
	instanceID string
	title      string
}

// probeParkedBookRequest advances one author-import park by reading Chaptarr's
// own pending-import state instead of replaying the add. Chaptarr owns the
// retry loop: the refused add already stored the whole intent (the monitored
// book, profiles, the search flag) on the fork's pending-import table, which
// Chaptarr retries on its own unbounded schedule and completes itself — and a
// duplicate add would merge into that row and force-bump its schedule. The
// sweep therefore only watches for the exits: author landed (complete the
// request through the normal already-tracked lane), import declared terminally
// failed or cancelled (hand a human the row now, not at the horizon), still
// importing (leave it alone). Builds without the read API fall back to the old
// add-probe, whose refusal is the only signal they offer. A read failure
// leaves the park untouched: blindness is not an answer, and the give-up
// horizon still bounds the wait.
func (s *Service) probeParkedBookRequest(row parkedBookRow) {
	client, _, err := s.resolveChaptarr(row.userID, row.instanceID)
	if err != nil || client == nil {
		log.Printf("request: probe parked book request %d (%q): resolve chaptarr: %v", row.id, row.title, err)
		return
	}
	foreignAuthorID, ok := s.lookupParkedAuthorID(client, row.foreignID)
	if !ok {
		return // lookup failed; blindness leaves the park untouched
	}
	if foreignAuthorID == "" {
		// The id-fetch no longer names this record directly (alias resolution
		// or a provider change). The add-probe's term ladder and error taxonomy
		// already know how to land that shape, so use the legacy lane.
		s.legacyProbeParkedBookRequest(row)
		return
	}
	status, err := client.GetAuthorImportStatus(foreignAuthorID)
	if errors.Is(err, chaptarr.ErrPendingImportAPIUnavailable) {
		s.legacyProbeParkedBookRequest(row)
		return
	}
	if errors.Is(err, chaptarr.ErrAuthorProviderAmbiguous) {
		// 409: the provider id matches more than one local author. That is a
		// structural state only a human can untangle — every future sweep
		// would read the same answer, so holding the park would hold it
		// forever.
		log.Printf("request: parked book request %d (%q): chaptarr reports the author id is ambiguous (multiple local authors); handing to the approval queue", row.id, row.title)
		s.demoteParkedBookRequest(row.id, bookAddFailureImportFailed)
		return
	}
	if err != nil {
		log.Printf("request: probe parked book request %d (%q): %v", row.id, row.title, err)
		return
	}
	switch {
	case status.Exists:
		// The author landed — and with it Chaptarr's own completion of the
		// stored add intent. The fulfill re-runs the normal flow and lands in
		// its already-tracked lane rather than adding twice.
		if _, err := s.fulfillPendingRequest(0, row.id, nil, true); err != nil {
			if errors.Is(err, chaptarr.ErrAuthorPendingImport) {
				return // lost a race with the import; the next pass completes it
			}
			log.Printf("request: parked book request %d (%q) failed after its author import landed (%v); handing to the approval queue", row.id, row.title, err)
			s.demoteParkedBookRequest(row.id, bookAddFailureImportAbandoned)
			return
		}
		log.Printf("request: parked book request %d (%q) completed after the author import landed", row.id, row.title)
	case status.Pending && status.Status == chaptarr.AuthorImportStatusFailed:
		// Chaptarr has no distinct cancelled status — a cancel in its UI marks
		// the row Failed with LastError "Cancelled by user" — so the label the
		// admin sees is decided by that field, read best-effort from the row.
		if s.pendingImportWasCancelled(client, status.PendingID) {
			log.Printf("request: parked book request %d (%q): the author import was cancelled in chaptarr; handing to the approval queue", row.id, row.title)
			s.demoteParkedBookRequest(row.id, bookAddFailureImportCancelled)
			return
		}
		log.Printf("request: parked book request %d (%q): chaptarr declared the author import terminally failed; handing to the approval queue", row.id, row.title)
		s.demoteParkedBookRequest(row.id, bookAddFailureImportFailed)
	case status.Pending && chaptarr.AuthorImportStatusConcluded(status.Status):
		// PartialSuccess or Succeeded with the author still absent from the
		// library: Chaptarr's scheduler re-picks only Pending/Retrying rows,
		// so this row will never change again — waiting on it waits forever.
		log.Printf("request: parked book request %d (%q): chaptarr concluded the author import (%s) but the author never landed; handing to the approval queue", row.id, row.title, status.Status)
		s.demoteParkedBookRequest(row.id, bookAddFailureImportFailed)
	case status.Pending:
		// Chaptarr is still importing on its own schedule; nothing to do.
	default:
		// Both answers false: the library does not know the author and no
		// pending row exists. Chaptarr deletes completed rows after 30 days
		// (CleanupOldCompleted) — with a 5-minute sweep the concluded state is
		// seen long before that, so reaching here means the row was removed
		// out-of-band. The closest honest word the app knows is "cancelled".
		log.Printf("request: parked book request %d (%q): the author import vanished from chaptarr without landing; handing to the approval queue", row.id, row.title)
		s.demoteParkedBookRequest(row.id, bookAddFailureImportCancelled)
	}
}

// lookupParkedAuthorID resolves the author foreignAuthorId behind a parked
// book via the exact id-term fetch. Returns ok=false on a read failure (leave
// the park alone), and an empty id with ok=true when the fetch answered but
// did not name this record (the alias shape the legacy lane handles).
func (s *Service) lookupParkedAuthorID(client *chaptarr.Client, foreignID string) (string, bool) {
	results, err := client.LookupBook(foreignID)
	if err != nil {
		log.Printf("request: lookup parked book %q author: %v", foreignID, err)
		return "", false
	}
	foreignID = strings.TrimSpace(foreignID)
	for i := range results {
		if strings.TrimSpace(results[i].ForeignBookID) == foreignID {
			return strings.TrimSpace(results[i].ForeignAuthorID), true
		}
	}
	return "", true
}

// pendingImportWasCancelled reads the arr's own record of why a Failed row
// stopped. Chaptarr has no distinct cancelled status — a cancel in its UI
// marks the row Failed with LastError "Cancelled by user" — so this is the
// only signal separating "the import could not finish" from "someone chose to
// stop it". Best-effort: an unreadable or missing row keeps the failed label.
func (s *Service) pendingImportWasCancelled(client *chaptarr.Client, pendingID int) bool {
	if pendingID <= 0 {
		return false
	}
	detail, err := client.GetPendingAuthorImport(pendingID)
	if err != nil || detail == nil {
		return false
	}
	return strings.Contains(strings.ToLower(detail.LastError), "cancelled by user")
}

// legacyProbeParkedBookRequest is the pre-pending-import-API probe: replay the
// add and read the refusal. Kept for older Chaptarr builds and for rows whose
// id-fetch no longer answers directly.
func (s *Service) legacyProbeParkedBookRequest(row parkedBookRow) {
	_, err := s.fulfillPendingRequest(0, row.id, nil, true)
	switch {
	case err == nil:
		log.Printf("request: parked book request %d (%q) completed after the author import landed", row.id, row.title)
	case errors.Is(err, chaptarr.ErrAuthorPendingImport):
		// Still waiting on the library's import; the next pass retries.
	default:
		log.Printf("request: parked book request %d (%q) failed beyond the pending import (%v); handing to the approval queue", row.id, row.title, err)
		s.demoteParkedBookRequest(row.id, bookAddFailureImportAbandoned)
	}
}

// demoteParkedBookRequest turns a server-owned park back into an ordinary
// approval-queue row and fires the request_pending notification that was
// deliberately withheld when the park was created: this is the moment a human
// decision first exists. reason (one of bookImportAddFailures) records which
// exit ended the wait, so the queue card can offer the honest verbs — try
// again, or close the request.
func (s *Service) demoteParkedBookRequest(requestID int64, reason string) {
	var userID int64
	var foreignID, bookFormat, instanceID, title string
	err := s.db.QueryRow(
		`SELECT user_id, COALESCE(foreign_id, ''), COALESCE(book_format, 'both'), COALESCE(instance_id, ''), title
		 FROM request_log WHERE id = ? AND status = ?`,
		requestID, StatusPending,
	).Scan(&userID, &foreignID, &bookFormat, &instanceID, &title)
	if err != nil {
		log.Printf("request: load parked book request %d for demotion: %v", requestID, err)
		return
	}
	res, err := s.db.Exec(
		// The row keeps the fact that brought it here. A request whose wait
		// ended without the author is not a fresh decision, and the page fired
		// below is the admin's first sight of it.
		"UPDATE request_log SET park_reason = NULL, add_failure_reason = ? WHERE id = ? AND status = ? AND park_reason IS NOT NULL",
		reason, requestID, StatusPending,
	)
	if err != nil {
		log.Printf("request: demote parked book request %d: %v", requestID, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // decided or demoted concurrently; nothing to announce
	}
	if s.notifier == nil {
		return
	}
	data := map[string]interface{}{
		"tmdb_id":     0,
		"media_type":  "book",
		"title":       title,
		"foreign_id":  foreignID,
		"book_format": bookFormat,
		"instance_id": instanceID,
	}
	if count, err := s.PendingCount(); err == nil {
		data["pending_count"] = count
	}
	s.notifier.NotifyAdmins("request_pending", data)
}

// reportBookImportStalls reports, per Chaptarr instance, whether any parked
// request has waited past the stall horizon. Every configured instance gets a
// transition each pass — stalled instances carry their waiting titles, healthy
// ones resolve any open issue — so the sink converges regardless of how the
// stall cleared (import landed, demotion, denial).
func (s *Service) reportBookImportStalls() {
	sink := s.bookImportStallSink
	if sink == nil {
		return
	}
	stalled := map[string][]string{}
	rows, err := s.db.Query(
		`SELECT COALESCE(instance_id, ''), title
		 FROM request_log
		 WHERE media_type = 'book' AND status = ? AND park_reason = ?
		   AND requested_at <= datetime('now', ?)
		 ORDER BY instance_id, id ASC`,
		StatusPending, bookParkReasonAuthorImport,
		fmt.Sprintf("-%d seconds", int64(bookParkStallAfter/time.Second)),
	)
	if err != nil {
		log.Printf("request: query stalled book parks: %v", err)
		return
	}
	for rows.Next() {
		var instanceID, title string
		if err := rows.Scan(&instanceID, &title); err != nil {
			_ = rows.Close()
			log.Printf("request: scan stalled book park: %v", err)
			return
		}
		stalled[instanceID] = append(stalled[instanceID], title)
	}
	closeErr := rows.Err()
	_ = rows.Close()
	if closeErr != nil {
		log.Printf("request: read stalled book parks: %v", closeErr)
		return
	}

	// The instance list is fully drained and the cursor closed BEFORE any sink
	// call. The pool is capped at a single connection (SQLite is single-writer),
	// so an open *sql.Rows holds the only connection there is: reporting from
	// inside the loop would block the sink's own transaction forever, and that
	// deadlocked goroutine would take every later query in the process with it.
	type chaptarrInstance struct{ id, name string }
	var instances []chaptarrInstance
	instanceRows, err := s.db.Query("SELECT id, name FROM service_instances WHERE service_type = 'chaptarr'")
	if err != nil {
		log.Printf("request: query chaptarr instances for stall report: %v", err)
		return
	}
	for instanceRows.Next() {
		var inst chaptarrInstance
		if err := instanceRows.Scan(&inst.id, &inst.name); err != nil {
			_ = instanceRows.Close()
			log.Printf("request: scan chaptarr instance for stall report: %v", err)
			return
		}
		instances = append(instances, inst)
	}
	instanceErr := instanceRows.Err()
	_ = instanceRows.Close()
	if instanceErr != nil {
		log.Printf("request: read chaptarr instances for stall report: %v", instanceErr)
		return
	}

	for _, inst := range instances {
		titles, isStalled := stalled[inst.id]
		if err := sink.RecordBookImportStall(inst.id, inst.name, titles, !isStalled); err != nil {
			log.Printf("request: record book import stall for instance %s: %v", inst.id, err)
		}
	}
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
	wasAuthorImportWait := false
	if r.mediaType == "book" {
		audience, err = s.bookRequestAudience(requestID, r.userID, r.bookFormat)
		if err != nil {
			return err
		}
		// Read the wait marker before the decision overwrites the row's story:
		// a denied author-import wait must also cancel the arr's queued import
		// (below), or the stored add intent delivers the book anyway later.
		var parkReason, addFailure sql.NullString
		if err := s.db.QueryRow(
			"SELECT park_reason, COALESCE(add_failure_reason, '') FROM request_log WHERE id = ?", requestID,
		).Scan(&parkReason, &addFailure); err == nil {
			wasAuthorImportWait = (parkReason.Valid && parkReason.String == bookParkReasonAuthorImport) ||
				isBookImportAddFailure(addFailure.String)
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
	if wasAuthorImportWait {
		s.cancelAuthorImportForDeniedRequest(requestID, r)
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
		// Same as the approval event: book and music rows carry their foreign
		// id for deep-linking; movie/TV payloads are unchanged.
		if r.foreignID != "" {
			data["foreign_id"] = r.foreignID
			data["instance_id"] = r.instanceID
			if r.mediaType == "book" {
				data["book_format"] = r.bookFormat
			}
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
// (when allowed) the available quality profiles for the relevant service. An
// explicit instanceID scopes the profiles to that library (profiles live
// inside an instance, so a selection on one library must never offer a
// sibling's profile ids); empty resolves the user's effective default.
func (s *Service) GetRequestOptions(userID int64, isAdmin bool, mediaType, instanceID string) (*RequestOptions, error) {
	eff, err := s.effectiveSettings(userID, isAdmin)
	if err != nil {
		return nil, err
	}
	opts := &RequestOptions{
		CanChooseSeason:    eff.AllowSeasonChoice && mediaType == "tv",
		CanChooseQuality:   eff.AllowQualityChoice && mediaType != "book" && mediaType != "music",
		DefaultSeasonScope: eff.SeasonScope,
		QualityProfiles:    []QualityProfile{},
	}
	if eff.AllowQualityChoice && mediaType != "book" && mediaType != "music" {
		profiles, err := s.qualityProfilesForInstance(userID, mediaType, instanceID)
		if err != nil {
			return nil, err
		}
		opts.QualityProfiles = profiles
	}
	return opts, nil
}

// qualityProfiles fetches the selectable quality profiles for a media type from
// userID's source instance (userID 0 = global default).
func (s *Service) qualityProfiles(userID int64, mediaType string) []QualityProfile {
	out, _ := s.qualityProfilesForInstance(userID, mediaType, "")
	return out
}

// qualityProfilesForInstance is qualityProfiles scoped to a selected library.
// Authorization failures surface so a forbidden selection is refused rather
// than answered with the default library's profiles; an unreachable arr still
// degrades to an empty list.
func (s *Service) qualityProfilesForInstance(userID int64, mediaType, instanceID string) ([]QualityProfile, error) {
	out := []QualityProfile{}
	if mediaType == "tv" {
		c, _, err := s.resolveSonarr(userID, instanceID)
		if err != nil {
			return nil, err
		}
		if c != nil {
			if ps, err := c.GetQualityProfiles(); err == nil {
				for _, p := range ps {
					out = append(out, QualityProfile{ID: p.ID, Name: p.Name})
				}
			}
		}
		return out, nil
	}
	c, _, err := s.resolveRadarr(userID, instanceID)
	if err != nil {
		return nil, err
	}
	if c != nil {
		if ps, err := c.GetQualityProfiles(); err == nil {
			for _, p := range ps {
				out = append(out, QualityProfile{ID: p.ID, Name: p.Name})
			}
		}
	}
	return out, nil
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
		"INSERT INTO request_log (user_id, tmdb_id, tvdb_id, foreign_id, book_format, book_record_id, instance_id, media_type, title, status, season_scope, quality_profile_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.userID, r.tmdbID, sqlNullInt(r.tvdbID), sqlNullStr(r.foreignID), sqlNullStr(r.bookFormat), r.bookRecordIDForRow(), sqlNullStr(r.instanceID), r.mediaType, title, status, sqlNullStr(r.seasonScope), sqlNullInt(r.qualityProfileID),
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

// BookSearchResult is one requester-facing book search hit for the AI surface:
// the metadata needed to pick a title plus the foreignBookId every book flow
// (request_media, check_request_status, the detail route) keys on. RemoteCover
// is best-effort artwork and only ever an absolute external URL — arr-origin
// or relative cover paths are dropped rather than surfaced to a client (this
// fork's lookups often carry no external cover at all, so it is usually empty).
type BookSearchResult struct {
	Title         string `json:"title"`
	AuthorName    string `json:"author_name,omitempty"`
	Year          int    `json:"year,omitempty"`
	ForeignBookID string `json:"foreign_book_id"`
	Overview      string `json:"overview,omitempty"`
	RemoteCover   string `json:"remote_cover,omitempty"`
}

// ErrNoChaptarrAccess reports that the user has no usable Chaptarr instance
// (none configured, or no per-user books grant).
var ErrNoChaptarrAccess = errors.New("books are not available for this account")

// bookSearchCacheTTL keeps just-searched results addressable by foreign id for
// the immediate follow-up (display_media verification) without re-querying
// Chaptarr once per displayed item.
const bookSearchCacheTTL = 60 * time.Second

// externalCoverURL returns the cover only when it is an absolute http(s) URL
// with a real host. Instance URLs must never reach clients, and this fork
// commonly returns arr-relative /MediaCoverProxy paths (broken server-side) in
// cover fields, so anything else is dropped.
func externalCoverURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return raw
}

// SearchBooksForUser looks a query up on the user's effective Chaptarr
// instance (their per-user grant, or the admin default for admins) — the same
// resolution every book request uses, so the AI assistant sees exactly the
// catalog the Books tab would.
func (s *Service) SearchBooksForUser(userID int64, query string) ([]BookSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	client, instanceID, err := s.resolveChaptarr(userID, "")
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, ErrNoChaptarrAccess
	}
	results, err := client.LookupBook(query)
	if err != nil {
		return nil, fmt.Errorf("book lookup: %w", err)
	}
	out := make([]BookSearchResult, 0, len(results))
	for _, r := range results {
		if strings.TrimSpace(r.ForeignBookID) == "" {
			continue // not addressable by any request flow
		}
		cover := externalCoverURL(r.RemoteCover)
		for _, img := range r.Images {
			if remote := externalCoverURL(img.RemoteURL); remote != "" {
				cover = remote // images[].remoteUrl is the repo-preferred source
				break
			}
		}
		author := r.AuthorName
		if author == "" && r.Author != nil {
			author = r.Author.AuthorName
		}
		result := BookSearchResult{
			Title:         r.Title,
			AuthorName:    author,
			Year:          r.Year,
			ForeignBookID: r.ForeignBookID,
			Overview:      r.Overview,
			RemoteCover:   cover,
		}
		out = append(out, result)
		if s.libraryCache != nil {
			if data, err := json.Marshal(result); err == nil {
				s.libraryCache.Set(bookSearchCacheKey(instanceID, r.ForeignBookID), data, bookSearchCacheTTL)
			}
		}
	}
	return out, nil
}

func bookSearchCacheKey(instanceID, foreignID string) string {
	return "booksearch|" + instanceID + "|" + foreignID
}

// CachedBookByForeignID returns a book the user's own recent search surfaced,
// keyed by foreign id on their effective instance. It performs no network I/O:
// a miss means the id was not in a recent search and the caller must re-verify
// with a live lookup (or reject).
func (s *Service) CachedBookByForeignID(userID int64, foreignID string) (*BookSearchResult, bool) {
	if s.libraryCache == nil {
		return nil, false
	}
	_, instanceID, err := s.resolveChaptarr(userID, "")
	if err != nil || instanceID == "" {
		return nil, false
	}
	data, ok := s.libraryCache.Get(bookSearchCacheKey(instanceID, strings.TrimSpace(foreignID)))
	if !ok {
		return nil, false
	}
	var result BookSearchResult
	if json.Unmarshal(data, &result) != nil {
		return nil, false
	}
	return &result, true
}
