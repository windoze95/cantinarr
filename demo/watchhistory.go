// watchhistory.go — the Monitoring module's watch-history fixtures, served
// for BOTH provider types (Tautulli, Tracearr) under BOTH route prefixes
// (/api/watch-history/{id}/* and /api/tautulli/{id}/*), exactly as the real
// server's one handler does: live activity with per-poll drift, watch
// history, and 7/30-day top stats. One provider-neutral fixture set is
// rendered per provider type — Tautulli names no server (it watches exactly
// one Plex), Tracearr names the server every stream and play happened on
// and adds a music track — and every history/stats answer carries the
// provider's own coverage note. Admin-only (monitoring:read).
package main

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ─── Wire shapes (server/internal/watchhistory/handler.go) ──

type whStreamJSON struct {
	User            string `json:"user"`
	Title           string `json:"title"`
	FullTitle       string `json:"full_title"`
	Player          string `json:"player"`
	Product         string `json:"product"`
	State           string `json:"state"` // playing | paused | buffering
	ProgressPercent int    `json:"progress_percent"`
	Quality         string `json:"quality"`
	StreamType      string `json:"stream_type"` // direct play | copy | transcode
	BandwidthKbps   int    `json:"bandwidth_kbps"`
	MediaType       string `json:"media_type"` // movie | episode | track
	Server          string `json:"server"`
	ServerType      string `json:"server_type"`
}

type whHistoryItemJSON struct {
	User            string `json:"user"`
	FullTitle       string `json:"full_title"`
	Date            string `json:"date"` // RFC3339 UTC, "" if unknown
	DurationSeconds int    `json:"duration_seconds"`
	PercentComplete int    `json:"percent_complete"`
	Player          string `json:"player"`
	Platform        string `json:"platform"`
	MediaType       string `json:"media_type"`
	Server          string `json:"server"`
	ServerType      string `json:"server_type"`
}

// whCoverageJSON says what an answer was computed from, so an empty list
// reads as absence rather than blindness. plays/since/until are omitempty;
// truncated and note are always present.
type whCoverageJSON struct {
	Plays     int    `json:"plays,omitempty"`
	Since     string `json:"since,omitempty"`
	Until     string `json:"until,omitempty"`
	Truncated bool   `json:"truncated"`
	Note      string `json:"note"`
}

type whTitlePlaysJSON struct {
	Title string `json:"title"`
	Plays int    `json:"plays"`
}

type whUserPlaysJSON struct {
	User  string `json:"user"`
	Plays int    `json:"plays"`
}

type whStatsJSON struct {
	TopMovies []whTitlePlaysJSON `json:"top_movies"`
	TopShows  []whTitlePlaysJSON `json:"top_shows"`
	TopUsers  []whUserPlaysJSON  `json:"top_users"`
	Coverage  whCoverageJSON     `json:"coverage"`
}

// ─── Provider-neutral fixtures ──────────────────────────

// whStream is one live stream before per-provider rendering. tracearrOnly
// marks fixtures a Tautulli instance (Plex video only) never reports.
type whStream struct {
	User            string
	Title           string
	FullTitle       string
	Player          string
	Product         string // the Plex client Tautulli reports
	TracearrProduct string // the client of the viewer's own server, per Tracearr
	State           string
	ProgressPercent int
	Quality         string
	StreamType      string
	BandwidthKbps   int
	MediaType       string
	tracearrOnly    bool
}

// whHistoryRow is one play before per-provider rendering.
type whHistoryRow struct {
	User            string
	FullTitle       string
	Date            string
	DurationSeconds int
	PercentComplete int
	Player          string
	Platform        string
	MediaType       string
	tracearrOnly    bool
}

// whServer is one media server Tracearr monitors.
type whServer struct {
	Name string
	Type string
}

var whServers = []whServer{
	{"Living Room Plex", servicePlex},
	{"Jellyfin", serviceJellyfin},
	{"Emby", serviceEmby},
}

// whViewerServer is the server each household member watches on, so a
// viewer's streams and plays always name the same server and all three
// server types show a badge on the Tracearr instance.
var whViewerServer = map[string]int{
	"alice":  0, // Living Room Plex
	"ben":    1, // Jellyfin
	"carmen": 2, // Emby
	"dana":   1, // Jellyfin
}

// ─── State (guarded by whMu) ────────────────────────────

var (
	whMu      sync.Mutex
	whStreams []*whStream
	whHistory []whHistoryRow
	whStats7  whStatsJSON
	whStats30 whStatsJSON
)

func init() {
	whSeed()
}

func whSeed() {
	whMu.Lock()
	defer whMu.Unlock()

	// Live streams: PD film titles plus a fictional demo series;
	// playing/paused + direct play/copy/transcode mix. The music track is a
	// Tracearr-only fixture (Tautulli watches Plex video).
	whStreams = []*whStream{
		{
			User: "alice", Title: "Metropolis", FullTitle: "Metropolis (1927)",
			Player: "Living Room TV", Product: "Plex for Apple TV", TracearrProduct: "Plex for Apple TV", State: "playing",
			ProgressPercent: 42, Quality: "1080p", StreamType: "direct play", BandwidthKbps: 12400,
			MediaType: "movie",
		},
		{
			User: "ben", Title: "The Brass Key",
			FullTitle: "The Clockwork Archive - S01E03 - The Brass Key",
			Player:    "Ben's iPad", Product: "Plex for iOS", TracearrProduct: "Swiftfin", State: "playing",
			ProgressPercent: 67, Quality: "720p", StreamType: "transcode", BandwidthKbps: 4200,
			MediaType: "episode",
		},
		{
			User: "carmen", Title: "Nosferatu", FullTitle: "Nosferatu (1922)",
			Player: "Bedroom Shield", Product: "Plex for Android (TV)", TracearrProduct: "Emby for Android TV", State: "paused",
			ProgressPercent: 18, Quality: "1080p", StreamType: "copy", BandwidthKbps: 8200,
			MediaType: "movie",
		},
		{
			User: "dana", Title: "Gymnopédie No. 1", FullTitle: "Erik Satie - Gymnopédie No. 1",
			Player: "Dana's MacBook", Product: "Jellyfin Web", TracearrProduct: "Jellyfin Web", State: "playing",
			ProgressPercent: 24, Quality: "FLAC", StreamType: "direct play", BandwidthKbps: 900,
			MediaType: "track", tracearrOnly: true,
		},
	}

	now := time.Now().UTC()
	at := func(hoursAgo int) string { return now.Add(-time.Duration(hoursAgo) * time.Hour).Format(time.RFC3339) }
	whHistory = []whHistoryRow{
		{User: "dana", FullTitle: "Erik Satie - Gymnopédie No. 3", Date: at(1), DurationSeconds: 170, PercentComplete: 100, Player: "Dana's MacBook", Platform: "macOS", MediaType: "track", tracearrOnly: true},
		{User: "alice", FullTitle: "The General (1926)", Date: at(3), DurationSeconds: 4620, PercentComplete: 98, Player: "Living Room TV", Platform: "tvOS", MediaType: "movie"},
		{User: "ben", FullTitle: "The Clockwork Archive - S01E02 - Winding the Year", Date: at(7), DurationSeconds: 2700, PercentComplete: 100, Player: "Ben's iPad", Platform: "iOS", MediaType: "episode"},
		{User: "carmen", FullTitle: "The Cabinet of Dr. Caligari (1920)", Date: at(11), DurationSeconds: 4560, PercentComplete: 95, Player: "Bedroom Shield", Platform: "Android", MediaType: "movie"},
		{User: "dana", FullTitle: "A Trip to the Moon (1902)", Date: at(16), DurationSeconds: 780, PercentComplete: 100, Player: "Dana's MacBook", Platform: "macOS", MediaType: "movie"},
		{User: "alice", FullTitle: "The Clockwork Archive - S01E01 - The Hollow Hour", Date: at(21), DurationSeconds: 2640, PercentComplete: 100, Player: "Living Room TV", Platform: "tvOS", MediaType: "episode"},
		{User: "ben", FullTitle: "Sherlock Jr. (1924)", Date: at(27), DurationSeconds: 2700, PercentComplete: 88, Player: "Ben's iPad", Platform: "iOS", MediaType: "movie"},
		{User: "carmen", FullTitle: "Metropolis (1927)", Date: at(33), DurationSeconds: 9180, PercentComplete: 100, Player: "Bedroom Shield", Platform: "Android", MediaType: "movie"},
		{User: "dana", FullTitle: "Harbor Lights Below - S02E04 - Slack Tide", Date: at(40), DurationSeconds: 2580, PercentComplete: 100, Player: "Dana's MacBook", Platform: "macOS", MediaType: "episode"},
		{User: "alice", FullTitle: "Safety Last! (1923)", Date: at(49), DurationSeconds: 4400, PercentComplete: 100, Player: "Living Room TV", Platform: "tvOS", MediaType: "movie"},
		{User: "ben", FullTitle: "The Kid (1921)", Date: at(55), DurationSeconds: 3240, PercentComplete: 74, Player: "Ben's Pixel", Platform: "Android", MediaType: "movie"},
		{User: "carmen", FullTitle: "Harbor Lights Below - S02E03 - The Long Haul", Date: at(63), DurationSeconds: 2520, PercentComplete: 100, Player: "Bedroom Shield", Platform: "Android", MediaType: "episode"},
		{User: "dana", FullTitle: "Battleship Potemkin (1925)", Date: at(70), DurationSeconds: 4500, PercentComplete: 92, Player: "Dana's MacBook", Platform: "macOS", MediaType: "movie"},
		{User: "alice", FullTitle: "The Clockwork Archive - S01E03 - The Brass Key", Date: at(77), DurationSeconds: 2700, PercentComplete: 45, Player: "Living Room TV", Platform: "tvOS", MediaType: "episode"},
		{User: "ben", FullTitle: "The Phantom of the Opera (1925)", Date: at(85), DurationSeconds: 5580, PercentComplete: 100, Player: "Ben's iPad", Platform: "iOS", MediaType: "movie"},
		{User: "carmen", FullTitle: "The Lost World (1925)", Date: at(94), DurationSeconds: 6360, PercentComplete: 100, Player: "Bedroom Shield", Platform: "Android", MediaType: "movie"},
		{User: "dana", FullTitle: "The Gold Rush (1925)", Date: at(103), DurationSeconds: 5700, PercentComplete: 100, Player: "Dana's MacBook", Platform: "macOS", MediaType: "movie"},
		{User: "alice", FullTitle: "Sunrise: A Song of Two Humans (1927)", Date: at(112), DurationSeconds: 5680, PercentComplete: 100, Player: "Living Room TV", Platform: "tvOS", MediaType: "movie"},
		{User: "ben", FullTitle: "Harbor Lights Below - S02E02 - Dead Reckoning", Date: at(121), DurationSeconds: 2520, PercentComplete: 100, Player: "Ben's iPad", Platform: "iOS", MediaType: "episode"},
		{User: "carmen", FullTitle: "Nosferatu (1922)", Date: at(130), DurationSeconds: 5640, PercentComplete: 61, Player: "Bedroom Shield", Platform: "Android", MediaType: "movie"},
		{User: "dana", FullTitle: "The Clockwork Archive - S01E02 - Winding the Year", Date: at(139), DurationSeconds: 2700, PercentComplete: 100, Player: "Dana's MacBook", Platform: "macOS", MediaType: "episode"},
	}

	whStats7 = whStatsJSON{
		TopMovies: []whTitlePlaysJSON{
			{Title: "Metropolis", Plays: 5},
			{Title: "The General", Plays: 4},
			{Title: "Nosferatu", Plays: 3},
			{Title: "A Trip to the Moon", Plays: 2},
		},
		TopShows: []whTitlePlaysJSON{
			{Title: "The Clockwork Archive", Plays: 8},
			{Title: "Harbor Lights Below", Plays: 5},
		},
		TopUsers: []whUserPlaysJSON{
			{User: "alice", Plays: 11},
			{User: "ben", Plays: 8},
			{User: "carmen", Plays: 6},
			{User: "dana", Plays: 4},
		},
	}
	whStats30 = whStatsJSON{
		TopMovies: []whTitlePlaysJSON{
			{Title: "Metropolis", Plays: 12},
			{Title: "The General", Plays: 9},
			{Title: "The Cabinet of Dr. Caligari", Plays: 8},
			{Title: "Nosferatu", Plays: 7},
			{Title: "The Gold Rush", Plays: 6},
			{Title: "Safety Last!", Plays: 5},
		},
		TopShows: []whTitlePlaysJSON{
			{Title: "The Clockwork Archive", Plays: 30},
			{Title: "Harbor Lights Below", Plays: 19},
			{Title: "The Petticoat Detective", Plays: 9},
		},
		TopUsers: []whUserPlaysJSON{
			{User: "alice", Plays: 41},
			{User: "ben", Plays: 30},
			{User: "carmen", Plays: 24},
			{User: "dana", Plays: 15},
		},
	}
}

// ─── Per-provider rendering ─────────────────────────────

// whServerFor is the server a viewer's fixture happened on, per provider:
// Tautulli watches exactly one Plex and names none (server "", server_type
// "plex", tautulli/provider.go); Tracearr names the viewer's own server.
func whServerFor(serviceType, user string) (name, kind string) {
	if serviceType == serviceTracearr {
		s := whServers[whViewerServer[user]%len(whServers)]
		return s.Name, s.Type
	}
	return "", servicePlex
}

// whRenderStream renders one live stream for the provider type; ok=false
// when this provider never reports the fixture.
func whRenderStream(serviceType string, s *whStream) (whStreamJSON, bool) {
	if s.tracearrOnly && serviceType != serviceTracearr {
		return whStreamJSON{}, false
	}
	server, kind := whServerFor(serviceType, s.User)
	product := s.Product
	if serviceType == serviceTracearr && s.TracearrProduct != "" {
		product = s.TracearrProduct
	}
	return whStreamJSON{
		User:            s.User,
		Title:           s.Title,
		FullTitle:       s.FullTitle,
		Player:          s.Player,
		Product:         product,
		State:           s.State,
		ProgressPercent: s.ProgressPercent,
		Quality:         s.Quality,
		StreamType:      s.StreamType,
		BandwidthKbps:   s.BandwidthKbps,
		MediaType:       s.MediaType,
		Server:          server,
		ServerType:      kind,
	}, true
}

// whRenderHistory renders one play for the provider type. Tautulli's
// get_history carries no media type, so its rows say "" (tautulli/provider.go
// maps none); Tracearr's rows carry it.
func whRenderHistory(serviceType string, row whHistoryRow) (whHistoryItemJSON, bool) {
	if row.tracearrOnly && serviceType != serviceTracearr {
		return whHistoryItemJSON{}, false
	}
	server, kind := whServerFor(serviceType, row.User)
	mediaType := row.MediaType
	if serviceType != serviceTracearr {
		mediaType = ""
	}
	return whHistoryItemJSON{
		User:            row.User,
		FullTitle:       row.FullTitle,
		Date:            row.Date,
		DurationSeconds: row.DurationSeconds,
		PercentComplete: row.PercentComplete,
		Player:          row.Player,
		Platform:        row.Platform,
		MediaType:       mediaType,
		Server:          server,
		ServerType:      kind,
	}, true
}

// whHistoryCoverage is the providers' verbatim wording for a history read
// (tautulli/provider.go, tracearr/provider.go). The demo never truncates.
func whHistoryCoverage(serviceType string, plays int) whCoverageJSON {
	if serviceType == serviceTracearr {
		return whCoverageJSON{
			Plays: plays,
			Note:  fmt.Sprintf("The %d most recent plays across every server Tracearr monitors; anything older is outside this window.", plays),
		}
	}
	return whCoverageJSON{
		Plays: plays,
		Note:  fmt.Sprintf("The %d most recent plays Tautulli recorded; anything older is outside this window.", plays),
	}
}

// whStatsCoverage is the providers' verbatim wording for a stats window.
// Tautulli ranks server-side and reports no play total; Tracearr derives its
// ranking from history, so it reports the plays it counted.
func whStatsCoverage(serviceType string, days int, plays int, now time.Time) whCoverageJSON {
	since := now.AddDate(0, 0, -days)
	out := whCoverageJSON{
		Since: since.Format(time.RFC3339),
		Until: now.Format(time.RFC3339),
	}
	if serviceType == serviceTracearr {
		out.Plays = plays
		out.Note = fmt.Sprintf("Based on %d plays Tracearr recorded since %s across every server it monitors.", plays, since.Format("2 Jan 2006"))
		return out
	}
	out.Note = fmt.Sprintf("Ranked by Tautulli over the last %d days.", days)
	return out
}

// whPositiveQuery reads a positive integer query parameter, falling back to
// def for anything missing, junk, or non-positive (handler.go:212-219).
func whPositiveQuery(r *http.Request, key string, def int) int {
	n := queryInt(r, key, def)
	if n <= 0 {
		return def
	}
	return n
}

// ─── Handlers ───────────────────────────────────────────

// whResolve validates the {instanceID} route param as a watch-history
// instance; writes the error response and returns nil when invalid. Either
// prefix serves either provider type, exactly like the real handler.
func whResolve(w http.ResponseWriter, r *http.Request) *DemoInstance {
	id := chi.URLParam(r, "instanceID")
	inst := instMgmtResolve(id)
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return nil
	}
	if !isWatchHistoryType(inst.ServiceType) {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("instance %s is not a watch-history instance (%s)",
			inst.ID, strings.Join(watchHistoryTypes(), ", ")))
		return nil
	}
	return inst
}

// registerWatchHistory mounts /api/watch-history/{instanceID}/* and the
// original /api/tautulli/{instanceID}/* alias (admin only). The app picks the
// prefix by service type; the server answers both for either type.
func registerWatchHistory(r chi.Router) {
	for _, prefix := range []string{"/watch-history", "/tautulli"} {
		r.Route(prefix+"/{instanceID}", func(sr chi.Router) {
			sr.Use(requireAdmin)
			sr.Get("/activity", whHandleActivity)
			sr.Get("/history", whHandleHistory)
			sr.Get("/stats", whHandleStats)
		})
	}
}

func whHandleActivity(w http.ResponseWriter, r *http.Request) {
	inst := whResolve(w, r)
	if inst == nil {
		return
	}
	whMu.Lock()
	// Drift a little on every poll (the app polls every 10 s) so the screen
	// feels live: playing streams creep forward, bandwidth jitters.
	streams := make([]whStreamJSON, 0, len(whStreams))
	total := 0
	for _, s := range whStreams {
		if s.State == "playing" {
			s.ProgressPercent += 1 + rand.IntN(2)
			if s.ProgressPercent > 99 {
				s.ProgressPercent = 3 + rand.IntN(10) // loop for an endless demo
			}
			s.BandwidthKbps += rand.IntN(401) - 200
			if s.BandwidthKbps < 500 {
				s.BandwidthKbps = 500
			}
		}
		rendered, ok := whRenderStream(inst.ServiceType, s)
		if !ok {
			continue
		}
		streams = append(streams, rendered)
		total += rendered.BandwidthKbps
	}
	whMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"stream_count":         len(streams),
		"total_bandwidth_kbps": total,
		"streams":              streams,
	})
}

func whHandleHistory(w http.ResponseWriter, r *http.Request) {
	inst := whResolve(w, r)
	if inst == nil {
		return
	}
	limit := whPositiveQuery(r, "limit", 50)
	whMu.Lock()
	items := make([]whHistoryItemJSON, 0, len(whHistory))
	for _, row := range whHistory {
		if len(items) >= limit {
			break
		}
		rendered, ok := whRenderHistory(inst.ServiceType, row)
		if !ok {
			continue
		}
		items = append(items, rendered)
	}
	whMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"coverage": whHistoryCoverage(inst.ServiceType, len(items)),
	})
}

func whHandleStats(w http.ResponseWriter, r *http.Request) {
	inst := whResolve(w, r)
	if inst == nil {
		return
	}
	days := whPositiveQuery(r, "days", 30)
	whMu.Lock()
	stats := whStats30
	if days <= 7 {
		stats = whStats7
	}
	whMu.Unlock()
	// Copy the buckets so the response never aliases the fixture slices.
	out := whStatsJSON{
		TopMovies: append([]whTitlePlaysJSON{}, stats.TopMovies...),
		TopShows:  append([]whTitlePlaysJSON{}, stats.TopShows...),
		TopUsers:  append([]whUserPlaysJSON{}, stats.TopUsers...),
	}
	plays := 0
	for _, u := range out.TopUsers {
		plays += u.Plays
	}
	out.Coverage = whStatsCoverage(inst.ServiceType, days, plays, time.Now().UTC())
	writeJSON(w, http.StatusOK, out)
}
