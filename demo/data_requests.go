// data_requests.go — the request domain's (D2) in-memory store and seed
// data: request-log rows, global + per-user request policy, per-title
// availability states, and the quality-profile fixtures the request-settings
// surfaces render.
//
// Locking: reqMu guards every req*/book* domain-local map and slice across
// requests.go, requests_admin.go, books.go, and this file. Never call a
// state.go accessor or a cross-domain hook (findMovie/findShow/bookByForeignID/
// arrOn*/chaptarrOn*) while holding reqMu.
package main

import (
	"strconv"
	"sync"
	"time"
)

// ─── Store types ────────────────────────────────────────

// reqLogRow is one request_log row. TmdbID is 0 for books; ForeignID and
// InstanceID are "" for movies/tv. BookFormat "" normalizes to "both" on
// every rendered surface. Waiters maps subscriber user ids to their own
// format slice (pending book rows only).
type reqLogRow struct {
	ID               int64
	UserID           int
	TmdbID           int
	TvdbID           int
	ForeignID        string
	BookFormat       string
	InstanceID       string
	MediaType        string
	Title            string
	Status           string
	DenyReason       string
	SeasonScope      string // "" | "all" | "first" | "latest" | "pilot" | "[1,3]"
	QualityProfileID int
	SearchTerm       string
	RequestedAt      time.Time
	Waiters          map[int]string // user id -> that waiter's format slice
	// ParkReason marks a pending row the SERVER owns and retries itself
	// (only reqParkReasonAuthorImport): hidden from the approval queue and
	// badge, served on GET /api/admin/requests/waiting, and rendered to
	// requesters as "requested" plus a book_format_wait(s) explanation.
	ParkReason string
	// AddFailureReason marks a pending approval-queue row whose automatic add
	// already ran and failed (reqAddFailure*). The import_* values are ended
	// waits: their queue card offers "Try again" (POST …/{id}/wait) alongside
	// deny.
	AddFailureReason string
}

// reqTitleState is the live availability of one movie/TV title — the demo's
// stand-in for the arr library digest. It backs requestStatusForTmdb, the
// per-title status endpoint, and the history overlay.
type reqTitleState struct {
	Status           string      // requested | downloading | partial | available
	Progress         float64     // 0..1
	SeasonFiles      map[int]int // tv: season_number -> episode files on disk
	MonitoredSeasons map[int]bool
}

// reqGlobalSettingsT is the global request policy (flat "settings" object of
// GET/PUT /api/admin/request-settings).
type reqGlobalSettingsT struct {
	RequireApproval      bool   `json:"require_approval"`
	AllowSeasonChoice    bool   `json:"allow_season_choice"`
	DefaultSeasonScope   string `json:"default_season_scope"`
	AllowQualityChoice   bool   `json:"allow_quality_choice"`
	DefaultQualityRadarr int    `json:"default_quality_radarr"`
	DefaultQualitySonarr int    `json:"default_quality_sonarr"`
}

// reqUserOverride stores the per-user overrides OTHER than require_approval
// (which lives on DemoUser.RequireApproval per the shared contract).
// nil pointer = inherit the global default.
type reqUserOverride struct {
	AllowSeasonChoice    *bool
	SeasonScope          *string
	AllowQualityChoice   *bool
	QualityProfileRadarr *int
	QualityProfileSonarr *int
}

// reqQualityProfile is one {"id","name"} quality-profile entry.
type reqQualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ─── Store state (guarded by reqMu) ─────────────────────

var (
	reqMu sync.Mutex

	reqLog    []*reqLogRow
	reqNextID int64 = 8

	// reqTitleStates is keyed "movie:<tmdb>" / "tv:<tmdb>".
	reqTitleStates = map[string]*reqTitleState{}

	// reqTitleInstances records WHICH library a title was routed to, same
	// key. Availability is tracked per title in the demo, so this is what
	// lets the sibling-library chips say the honest thing: the library that
	// took it reports the live status, the others report that they hold
	// nothing.
	reqTitleInstances = map[string]string{}
	// reqActiveSims dedupes concurrent download simulations per title key.
	reqActiveSims = map[string]bool{}

	reqGlobal = reqGlobalSettingsT{
		RequireApproval:      false,
		AllowSeasonChoice:    true,
		DefaultSeasonScope:   "all",
		AllowQualityChoice:   false,
		DefaultQualityRadarr: 0,
		DefaultQualitySonarr: 0,
	}
	reqUserOverrides = map[int]*reqUserOverride{}
)

// Quality-profile fixtures served on /api/requests/options and the admin
// request-settings view (kept in line with the fake arrs' profile id 1).
var (
	reqRadarrProfiles = []reqQualityProfile{{ID: 1, Name: "HD-1080p"}, {ID: 2, Name: "Ultra-HD 2160p"}}
	reqSonarrProfiles = []reqQualityProfile{{ID: 1, Name: "HD-1080p"}, {ID: 2, Name: "Ultra-HD 2160p"}}
)

// ─── Small locked helpers ───────────────────────────────

// reqTitleKey builds the reqTitleStates key for a movie/TV title.
func reqTitleKey(mediaType string, tmdbID int) string {
	if mediaType == mediaTypeTV {
		return "tv:" + strconv.Itoa(tmdbID)
	}
	return "movie:" + strconv.Itoa(tmdbID)
}

// reqLockedPendingCount counts the pending rows awaiting a HUMAN decision —
// server-owned author-import parks are excluded exactly like the approval
// queue and the badge. Caller holds reqMu.
func reqLockedPendingCount() int {
	n := 0
	for _, row := range reqLog {
		if row.Status == statusPending && row.ParkReason != reqParkReasonAuthorImport {
			n++
		}
	}
	return n
}

// reqValidCoarseScope reports whether v is a valid coarse season scope.
func reqValidCoarseScope(v string) bool {
	switch v {
	case "all", "first", "latest", "pilot":
		return true
	}
	return false
}

// reqNormalizeBookFormat maps "" (and anything unknown) to "both" — the
// normalization every rendered row applies before serialization.
func reqNormalizeBookFormat(v string) string {
	switch v {
	case bookFormatEbook, bookFormatAudiobook, bookFormatBoth:
		return v
	}
	return bookFormatBoth
}

// ─── Author-import parks ("waiting for library") ────────
//
// Wire vocabulary mirrored from the real server: park_reason marks a pending
// row the server owns and retries; the add_failure_reason values record how a
// wait ended (or that a metadata lookup failed), which decides the admin verbs.

const (
	reqParkReasonAuthorImport = "author_import"

	reqAddFailureMetadataUnresolved = "metadata_unresolved"
	reqAddFailureImportAbandoned    = "import_abandoned"
	reqAddFailureImportFailed       = "import_failed"
	reqAddFailureImportCancelled    = "import_cancelled"
)

// Requester/admin copy, verbatim from the real server.
const (
	reqBookAuthorImportingMessage = "This book's author is still being added to the library. Your request will finish automatically once that completes."
	reqBookWaitExtendedMessage    = "Waiting resumed: the library is importing this author again, and the request completes automatically when it lands."
	reqErrAuthorPendingImport     = "the book's author is still being imported by the library's metadata service"
)

// reqIsImportAddFailure reports whether reason is an ended author-import wait
// (the demotion lanes whose queue card offers "Try again" alongside deny).
func reqIsImportAddFailure(reason string) bool {
	switch reason {
	case reqAddFailureImportAbandoned, reqAddFailureImportFailed, reqAddFailureImportCancelled:
		return true
	}
	return false
}

// reqParkRetryInterval is the real park-maintenance sweep cadence; the demo
// keeps a virtual clock on it so last_attempt_at ages the way the real one
// does.
const reqParkRetryInterval = 5 * time.Minute

// reqParkSweepEpoch is this process's start — the demo's stand-in for the real
// server's startedAt/lastParkSweep pair.
var reqParkSweepEpoch = time.Now()

// reqParkLastAttempt mirrors Service.bookFormatWaitFor's timestamp: the most
// recent virtual sweep tick, or the request's own accepted-at when that add is
// newer (the failed add itself was an attempt this process can vouch for). Nil
// before the first tick for rows older than the process — "unknown", never
// "never tried".
func reqParkLastAttempt(requestedAt time.Time) *time.Time {
	var last time.Time
	if ticks := time.Since(reqParkSweepEpoch) / reqParkRetryInterval; ticks >= 1 {
		last = reqParkSweepEpoch.Add(ticks * reqParkRetryInterval)
	}
	if requested := requestedAt.UTC(); !requested.Before(reqParkSweepEpoch.UTC()) && requested.After(last) {
		last = requested
	}
	if last.IsZero() {
		return nil
	}
	utc := last.UTC()
	return &utc
}

// reqBookWaitJSON renders one BookFormatWait object ({reason, waiting_since,
// last_attempt_at?}) for a server-owned author-import park.
func reqBookWaitJSON(requestedAt time.Time) map[string]any {
	wait := map[string]any{
		"reason":        reqParkReasonAuthorImport,
		"waiting_since": requestedAt.UTC(),
	}
	if last := reqParkLastAttempt(requestedAt); last != nil {
		wait["last_attempt_at"] = *last
	}
	return wait
}

// ─── Seed data ──────────────────────────────────────────
//
// References users/instances/catalog entries by frozen ids only — no state
// accessors or cross-domain hooks may run from init() (contract seeding rule).

func init() {
	reqMu.Lock()
	defer reqMu.Unlock()

	// Completed requests over PD catalog titles (all available).
	reqLog = append(reqLog,
		&reqLogRow{
			ID: 1, UserID: 1, TmdbID: 961, MediaType: mediaTypeMovie,
			Title: "The General", Status: statusAvailable,
			RequestedAt: time.Date(2026, 6, 10, 14, 5, 0, 0, time.UTC),
			Waiters:     map[int]string{},
		},
		&reqLogRow{
			ID: 2, UserID: 2, TmdbID: 19, MediaType: mediaTypeMovie,
			Title: "Metropolis", Status: statusAvailable,
			RequestedAt: time.Date(2026, 6, 18, 20, 41, 0, 0, time.UTC),
			Waiters:     map[int]string{},
		},
		&reqLogRow{
			ID: 3, UserID: 2, TmdbID: 90001, TvdbID: 390001, MediaType: mediaTypeTV,
			Title: "Sherlock Holmes Adventures", Status: statusAvailable,
			SeasonScope: "all",
			RequestedAt: time.Date(2026, 7, 2, 9, 12, 0, 0, time.UTC),
			Waiters:     map[int]string{},
		},
		// One denied request with a reason the requester sees verbatim.
		&reqLogRow{
			ID: 4, UserID: 2, TmdbID: 10513, MediaType: mediaTypeMovie,
			Title: "Plan 9 from Outer Space", Status: statusDenied,
			DenyReason:  "Movie night already has enough B-movies this month — ask again in August.",
			RequestedAt: time.Date(2026, 7, 15, 18, 30, 0, 0, time.UTC),
			Waiters:     map[int]string{},
		},
		// One PENDING request from user 2 so the admin sees the approvals
		// loop (badge + queue row) immediately after login.
		&reqLogRow{
			ID: 5, UserID: 2, TmdbID: 234, MediaType: mediaTypeMovie,
			Title: "The Cabinet of Dr. Caligari", Status: statusPending,
			RequestedAt: time.Date(2026, 7, 29, 21, 17, 0, 0, time.UTC),
			Waiters:     map[int]string{},
		},
		// One SERVER-OWNED author-import park ("waiting for library"): the
		// author's metadata import is still pending in the fake Chaptarr
		// (chapAuthorImportState), so the row is hidden from the approval
		// queue/badge, listed on GET /api/admin/requests/waiting, and reads
		// "requested" + book_format_wait to the requester.
		&reqLogRow{
			ID: 6, UserID: 2, ForeignID: "60401", BookFormat: bookFormatEbook,
			InstanceID: instChaptarr, MediaType: mediaTypeBook,
			Title: "The Lighthouse at Wintermere", Status: statusPending,
			SearchTerm: "lighthouse at wintermere foxcroft",
			ParkReason: reqParkReasonAuthorImport,
			// Young on purpose: the real server flags a >24h park as a
			// system-health stall, which the demo does not stage.
			RequestedAt: time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second),
			Waiters:     map[int]string{},
		},
		// One ENDED wait: Chaptarr declared this author's import terminally
		// failed, so the park was demoted back to the approval queue with the
		// add_failure_reason that makes the card offer "Try again"
		// (POST /api/admin/requests/{id}/wait) alongside deny.
		&reqLogRow{
			ID: 7, UserID: 2, ForeignID: "60544", BookFormat: bookFormatBoth,
			InstanceID: instChaptarr, MediaType: mediaTypeBook,
			Title: "The Clockwork Ferryman", Status: statusPending,
			SearchTerm:       "clockwork ferryman thistlewood",
			AddFailureReason: reqAddFailureImportFailed,
			RequestedAt:      time.Now().UTC().Add(-49 * time.Hour).Truncate(time.Second),
			Waiters:          map[int]string{},
		},
	)

	// Live availability behind the completed requests (feeds
	// requestStatusForTmdb, the status endpoint, and the history overlay).
	reqTitleStates[reqTitleKey(mediaTypeMovie, 961)] = &reqTitleState{
		Status: statusAvailable, Progress: 1,
		SeasonFiles: map[int]int{}, MonitoredSeasons: map[int]bool{},
	}
	reqTitleStates[reqTitleKey(mediaTypeMovie, 19)] = &reqTitleState{
		Status: statusAvailable, Progress: 1,
		SeasonFiles: map[int]int{}, MonitoredSeasons: map[int]bool{},
	}
	reqTitleStates[reqTitleKey(mediaTypeTV, 90001)] = &reqTitleState{
		Status: statusAvailable, Progress: 1,
		SeasonFiles: map[int]int{}, MonitoredSeasons: map[int]bool{},
	}

	// The Dracula audiobook (foreign id 17245) is seeded mid-download in the
	// fake Chaptarr queue — mirror that in the per-format downloading map so
	// book-status reports "downloading" for it.
	bookDownloadingSet["17245|"+bookFormatAudiobook] = true
}
