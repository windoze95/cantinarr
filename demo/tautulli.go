// tautulli.go — Tautulli monitoring fixtures (srv-instances §4, app-admin
// §11, gap-plan §1.14): live activity with per-poll drift, watch history, and
// 7/30-day top stats. Admin-only (monitoring:read).
package main

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ─── Shapes (srv-instances §4) ──────────────────────────

type tauStreamJSON struct {
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
}

type tauHistoryItemJSON struct {
	User            string `json:"user"`
	FullTitle       string `json:"full_title"`
	Date            string `json:"date"` // RFC3339 UTC, "" if unknown
	DurationSeconds int    `json:"duration_seconds"`
	PercentComplete int    `json:"percent_complete"`
	Player          string `json:"player"`
	Platform        string `json:"platform"`
}

type tauTitlePlaysJSON struct {
	Title string `json:"title"`
	Plays int    `json:"plays"`
}

type tauUserPlaysJSON struct {
	User  string `json:"user"`
	Plays int    `json:"plays"`
}

type tauStatsJSON struct {
	TopMovies []tauTitlePlaysJSON `json:"top_movies"`
	TopShows  []tauTitlePlaysJSON `json:"top_shows"`
	TopUsers  []tauUserPlaysJSON  `json:"top_users"`
}

// ─── State (guarded by tauMu) ───────────────────────────

var (
	tauMu      sync.Mutex
	tauStreams []*tauStreamJSON
	tauHistory []tauHistoryItemJSON
	tauStats7  tauStatsJSON
	tauStats30 tauStatsJSON
)

func init() {
	tauSeed()
}

func tauSeed() {
	tauMu.Lock()
	defer tauMu.Unlock()

	// Live streams: PD film titles plus a fictional demo series;
	// playing/paused + direct play/copy/transcode mix.
	tauStreams = []*tauStreamJSON{
		{
			User: "alice", Title: "Metropolis", FullTitle: "Metropolis (1927)",
			Player: "Living Room TV", Product: "Plex for Apple TV", State: "playing",
			ProgressPercent: 42, Quality: "1080p", StreamType: "direct play", BandwidthKbps: 12400,
		},
		{
			User: "ben", Title: "The Brass Key",
			FullTitle: "The Clockwork Archive - S01E03 - The Brass Key",
			Player:    "Ben's iPad", Product: "Plex for iOS", State: "playing",
			ProgressPercent: 67, Quality: "720p", StreamType: "transcode", BandwidthKbps: 4200,
		},
		{
			User: "carmen", Title: "Nosferatu", FullTitle: "Nosferatu (1922)",
			Player: "Bedroom Shield", Product: "Plex for Android (TV)", State: "paused",
			ProgressPercent: 18, Quality: "1080p", StreamType: "copy", BandwidthKbps: 8200,
		},
	}

	now := time.Now().UTC()
	at := func(hoursAgo int) string { return now.Add(-time.Duration(hoursAgo) * time.Hour).Format(time.RFC3339) }
	tauHistory = []tauHistoryItemJSON{
		{User: "alice", FullTitle: "The General (1926)", Date: at(3), DurationSeconds: 4620, PercentComplete: 98, Player: "Living Room TV", Platform: "tvOS"},
		{User: "ben", FullTitle: "The Clockwork Archive - S01E02 - Winding the Year", Date: at(7), DurationSeconds: 2700, PercentComplete: 100, Player: "Ben's iPad", Platform: "iOS"},
		{User: "carmen", FullTitle: "The Cabinet of Dr. Caligari (1920)", Date: at(11), DurationSeconds: 4560, PercentComplete: 95, Player: "Bedroom Shield", Platform: "Android"},
		{User: "dana", FullTitle: "A Trip to the Moon (1902)", Date: at(16), DurationSeconds: 780, PercentComplete: 100, Player: "Dana's MacBook", Platform: "macOS"},
		{User: "alice", FullTitle: "The Clockwork Archive - S01E01 - The Hollow Hour", Date: at(21), DurationSeconds: 2640, PercentComplete: 100, Player: "Living Room TV", Platform: "tvOS"},
		{User: "ben", FullTitle: "Sherlock Jr. (1924)", Date: at(27), DurationSeconds: 2700, PercentComplete: 88, Player: "Ben's iPad", Platform: "iOS"},
		{User: "carmen", FullTitle: "Metropolis (1927)", Date: at(33), DurationSeconds: 9180, PercentComplete: 100, Player: "Bedroom Shield", Platform: "Android"},
		{User: "dana", FullTitle: "Harbor Lights Below - S02E04 - Slack Tide", Date: at(40), DurationSeconds: 2580, PercentComplete: 100, Player: "Dana's MacBook", Platform: "macOS"},
		{User: "alice", FullTitle: "Safety Last! (1923)", Date: at(49), DurationSeconds: 4400, PercentComplete: 100, Player: "Living Room TV", Platform: "tvOS"},
		{User: "ben", FullTitle: "The Kid (1921)", Date: at(55), DurationSeconds: 3240, PercentComplete: 74, Player: "Ben's Pixel", Platform: "Android"},
		{User: "carmen", FullTitle: "Harbor Lights Below - S02E03 - The Long Haul", Date: at(63), DurationSeconds: 2520, PercentComplete: 100, Player: "Bedroom Shield", Platform: "Android"},
		{User: "dana", FullTitle: "Battleship Potemkin (1925)", Date: at(70), DurationSeconds: 4500, PercentComplete: 92, Player: "Dana's MacBook", Platform: "macOS"},
		{User: "alice", FullTitle: "The Clockwork Archive - S01E03 - The Brass Key", Date: at(77), DurationSeconds: 2700, PercentComplete: 45, Player: "Living Room TV", Platform: "tvOS"},
		{User: "ben", FullTitle: "The Phantom of the Opera (1925)", Date: at(85), DurationSeconds: 5580, PercentComplete: 100, Player: "Ben's iPad", Platform: "iOS"},
		{User: "carmen", FullTitle: "The Lost World (1925)", Date: at(94), DurationSeconds: 6360, PercentComplete: 100, Player: "Bedroom Shield", Platform: "Android"},
		{User: "dana", FullTitle: "The Gold Rush (1925)", Date: at(103), DurationSeconds: 5700, PercentComplete: 100, Player: "Dana's MacBook", Platform: "macOS"},
		{User: "alice", FullTitle: "Sunrise: A Song of Two Humans (1927)", Date: at(112), DurationSeconds: 5680, PercentComplete: 100, Player: "Living Room TV", Platform: "tvOS"},
		{User: "ben", FullTitle: "Harbor Lights Below - S02E02 - Dead Reckoning", Date: at(121), DurationSeconds: 2520, PercentComplete: 100, Player: "Ben's iPad", Platform: "iOS"},
		{User: "carmen", FullTitle: "Nosferatu (1922)", Date: at(130), DurationSeconds: 5640, PercentComplete: 61, Player: "Bedroom Shield", Platform: "Android"},
		{User: "dana", FullTitle: "The Clockwork Archive - S01E02 - Winding the Year", Date: at(139), DurationSeconds: 2700, PercentComplete: 100, Player: "Dana's MacBook", Platform: "macOS"},
	}

	tauStats7 = tauStatsJSON{
		TopMovies: []tauTitlePlaysJSON{
			{Title: "Metropolis", Plays: 5},
			{Title: "The General", Plays: 4},
			{Title: "Nosferatu", Plays: 3},
			{Title: "A Trip to the Moon", Plays: 2},
		},
		TopShows: []tauTitlePlaysJSON{
			{Title: "The Clockwork Archive", Plays: 8},
			{Title: "Harbor Lights Below", Plays: 5},
		},
		TopUsers: []tauUserPlaysJSON{
			{User: "alice", Plays: 11},
			{User: "ben", Plays: 8},
			{User: "carmen", Plays: 6},
			{User: "dana", Plays: 4},
		},
	}
	tauStats30 = tauStatsJSON{
		TopMovies: []tauTitlePlaysJSON{
			{Title: "Metropolis", Plays: 12},
			{Title: "The General", Plays: 9},
			{Title: "The Cabinet of Dr. Caligari", Plays: 8},
			{Title: "Nosferatu", Plays: 7},
			{Title: "The Gold Rush", Plays: 6},
			{Title: "Safety Last!", Plays: 5},
		},
		TopShows: []tauTitlePlaysJSON{
			{Title: "The Clockwork Archive", Plays: 30},
			{Title: "Harbor Lights Below", Plays: 19},
			{Title: "The Petticoat Detective", Plays: 9},
		},
		TopUsers: []tauUserPlaysJSON{
			{User: "alice", Plays: 41},
			{User: "ben", Plays: 30},
			{User: "carmen", Plays: 24},
			{User: "dana", Plays: 15},
		},
	}
}

// ─── Handlers ───────────────────────────────────────────

// tauResolve validates the {instanceID} route param as a tautulli instance;
// writes the error response and returns nil when invalid.
func tauResolve(w http.ResponseWriter, r *http.Request) *DemoInstance {
	id := chi.URLParam(r, "instanceID")
	inst := instMgmtResolve(id)
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return nil
	}
	if inst.ServiceType != serviceTautulli {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("instance %s is not a tautulli instance", inst.ID))
		return nil
	}
	return inst
}

// registerTautulli mounts /api/tautulli/{instanceID}/* (admin only).
func registerTautulli(r chi.Router) {
	r.Route("/tautulli/{instanceID}", func(sr chi.Router) {
		sr.Use(requireAdmin)
		sr.Get("/activity", tauHandleActivity)
		sr.Get("/history", tauHandleHistory)
		sr.Get("/stats", tauHandleStats)
	})
}

func tauHandleActivity(w http.ResponseWriter, r *http.Request) {
	if tauResolve(w, r) == nil {
		return
	}
	tauMu.Lock()
	// Drift a little on every poll (the app polls every 10 s) so the screen
	// feels live: playing streams creep forward, bandwidth jitters.
	streams := make([]tauStreamJSON, 0, len(tauStreams))
	total := 0
	for _, s := range tauStreams {
		if s.State == "playing" {
			s.ProgressPercent += 1 + rand.IntN(2)
			if s.ProgressPercent > 99 {
				s.ProgressPercent = 3 + rand.IntN(10) // loop for an endless demo
			}
			s.BandwidthKbps += rand.IntN(401) - 200
			if s.BandwidthKbps < 1000 {
				s.BandwidthKbps = 1000
			}
		}
		streams = append(streams, *s)
		total += s.BandwidthKbps
	}
	tauMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"stream_count":         len(streams),
		"total_bandwidth_kbps": total,
		"streams":              streams,
	})
}

func tauHandleHistory(w http.ResponseWriter, r *http.Request) {
	if tauResolve(w, r) == nil {
		return
	}
	limit := queryInt(r, "limit", 50)
	if limit <= 0 {
		limit = 50
	}
	tauMu.Lock()
	if limit > len(tauHistory) {
		limit = len(tauHistory)
	}
	items := append([]tauHistoryItemJSON{}, tauHistory[:limit]...)
	tauMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func tauHandleStats(w http.ResponseWriter, r *http.Request) {
	if tauResolve(w, r) == nil {
		return
	}
	days := queryInt(r, "days", 30)
	tauMu.Lock()
	stats := tauStats30
	if days <= 7 {
		stats = tauStats7
	}
	tauMu.Unlock()
	writeJSON(w, http.StatusOK, stats)
}
