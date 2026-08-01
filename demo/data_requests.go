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
	reqNextID int64 = 6

	// reqTitleStates is keyed "movie:<tmdb>" / "tv:<tmdb>".
	reqTitleStates = map[string]*reqTitleState{}
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

// reqLockedPendingCount counts pending rows. Caller holds reqMu.
func reqLockedPendingCount() int {
	n := 0
	for _, row := range reqLog {
		if row.Status == statusPending {
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
