// types.go — shared enums, constants, JSON helpers, and the shared catalog
// types every domain file populates or consumes. This file is part of the
// frozen Stage A contract (see contract.md); do not edit it from a domain
// branch — add domain-local supplementary maps in your own file instead.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// ─── Roles ──────────────────────────────────────────────

const (
	roleAdmin = "admin"
	roleUser  = "user"
)

// ─── Media types (movie/tv/book describe media) ─────────

const (
	mediaTypeMovie = "movie"
	mediaTypeTV    = "tv"
	mediaTypeBook  = "book"
)

// ─── Service types (radarr/sonarr/… describe services) ──

const (
	serviceRadarr       = "radarr"
	serviceSonarr       = "sonarr"
	serviceChaptarr     = "chaptarr"
	serviceSabnzbd      = "sabnzbd"
	serviceNzbget       = "nzbget"
	serviceQbittorrent  = "qbittorrent"
	serviceTransmission = "transmission"
	serviceTautulli     = "tautulli"

	// Media servers. Cantinarr manages user ACCESS on these, never library
	// routing: they follow the Chaptarr rule (never a global default, granted
	// per user, invisible to arr routing). Jellyfin and Emby hold accounts
	// Cantinarr creates; Plex holds shares Cantinarr sends.
	serviceJellyfin = "jellyfin"
	serviceEmby     = "emby"
	servicePlex     = "plex"
)

// mediaServerTypes are the service types that are media servers, in the
// server's stable order. Mirrors instance.MediaServerTypes().
func mediaServerTypes() []string {
	return []string{serviceJellyfin, serviceEmby, servicePlex}
}

// isMediaServerType reports whether serviceType is a media server.
func isMediaServerType(serviceType string) bool {
	switch serviceType {
	case serviceJellyfin, serviceEmby, servicePlex:
		return true
	}
	return false
}

// mediaServerKindFor maps a media-server type to the access kind the app
// renders: Jellyfin/Emby hand out accounts, Plex hands out invites.
func mediaServerKindFor(serviceType string) string {
	if serviceType == servicePlex {
		return mediaServerKindInvite
	}
	return mediaServerKindAccount
}

const (
	mediaServerKindAccount = "account"
	mediaServerKindInvite  = "invite"

	// plexPublicAddress is where anyone signs in to any Plex server, so it is
	// the sign-in address a Plex instance shows unless an admin typed another.
	plexPublicAddress = "https://app.plex.tv"
)

// ─── Request statuses ───────────────────────────────────
//
// Every REST surface uses these spellings, including "partial".
// wsStatusPartiallyAvailable is the WS-ONLY spelling used on the
// request_status_changed event; never emit it on any REST response and
// never emit "partial" over WS.

const (
	statusUnavailable = "unavailable"
	statusRequested   = "requested"
	statusPending     = "pending"
	statusDenied      = "denied"
	statusDownloading = "downloading"
	statusPartial     = "partial"
	statusAvailable   = "available"

	wsStatusPartiallyAvailable = "partially_available"
)

// ─── Book formats ───────────────────────────────────────

const (
	bookFormatEbook     = "ebook"
	bookFormatAudiobook = "audiobook"
	bookFormatBoth      = "both"
)

// ─── WebSocket event names ──────────────────────────────

const (
	evtDownloadProgress                = "download_progress"
	evtRequestStatusChanged            = "request_status_changed"
	evtDownloadsQueue                  = "downloads_queue"
	evtArrQueueChanged                 = "arr_queue_changed"
	evtRequestPending                  = "request_pending"
	evtRequestDecision                 = "request_decision"
	evtIssueCreated                    = "issue_created"
	evtIssueUpdated                    = "issue_updated"
	evtAgentActionPending              = "agent_action_pending"
	evtAgentActionDecided              = "agent_action_decided"
	evtAgentAutoapprovalPaused         = "agent_autoapproval_paused"
	evtRemediationAutodispatchDisabled = "remediation_autodispatch_disabled"
	evtPlexAccessRequest               = "plex_access_request"
	evtPlexInviteSent                  = "plex_invite_sent"
)

// ─── JSON helpers ───────────────────────────────────────

// writeJSON writes v as the response body with the given status code and
// Content-Type: application/json.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr writes the standard error body {"error":"<msg>"} with the given
// status code. Every error in the demo — handlers AND middleware — uses this
// (deliberate divergence from the real server's text/plain http.Error sites).
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// queryInt reads an integer query parameter, returning fallback when the
// parameter is absent or malformed.
func queryInt(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// Pointer helpers for building seed data.
func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func boolPtr(b bool) *bool    { return &b }

// yearOf extracts the year from a "YYYY-MM-DD" date string; 0 when unknown.
func yearOf(date string) int {
	if len(date) >= 4 {
		y, _ := strconv.Atoi(date[:4])
		return y
	}
	return 0
}

// ─── Shared catalog types ───────────────────────────────
//
// These structs are Go-side data holders, deliberately WITHOUT json tags:
// the same DemoMovie is rendered as TMDB snake_case by the discover domain
// and as Radarr camelCase by the arr domain. NEVER json.Marshal one of these
// directly — always build the per-endpoint map/struct your spec documents.
//
// The discover domain (D3) owns and seeds demoMovies / demoShows / persons;
// everyone else reads them through the findMovie/findShow hooks (contract.md).

// DemoGenre is a TMDB genre (id + name both required wherever rendered).
type DemoGenre struct {
	ID   int
	Name string
}

// DemoMovie is one catalog film (all public-domain titles, real TMDB ids).
type DemoMovie struct {
	TmdbID           int
	ImdbID           string // e.g. "tt0017136"
	Title            string
	OriginalTitle    string
	Overview         string
	Tagline          string
	PosterPath       string // TMDB image path, e.g. "/kr9wXRN23zLuWJIelahas1mtnYj.jpg"; "" when none
	BackdropPath     string
	ReleaseDate      string // "YYYY-MM-DD"
	Genres           []DemoGenre
	VoteAverage      float64 // fractional — render as float64 or omit, never as an int
	VoteCount        int
	Popularity       float64
	Runtime          int // minutes
	OriginalLanguage string
}

// TraktID derives the fictional Trakt id: tmdb * 10 (old-demo convention).
func (m *DemoMovie) TraktID() int { return m.TmdbID * 10 }

// GenreIDs returns just the genre ids (TMDB list-item shape).
func (m *DemoMovie) GenreIDs() []int {
	ids := make([]int, 0, len(m.Genres))
	for _, g := range m.Genres {
		ids = append(ids, g.ID)
	}
	return ids
}

// Year is the release year; 0 when unknown.
func (m *DemoMovie) Year() int { return yearOf(m.ReleaseDate) }

// DemoShow is one catalog TV series (fictional shows, TMDB ids 90001+,
// TVDB ids 390001+).
type DemoShow struct {
	TmdbID           int
	TvdbID           int
	ImdbID           string
	Name             string
	Overview         string
	Tagline          string
	PosterPath       string
	BackdropPath     string
	FirstAirDate     string // "YYYY-MM-DD"
	Genres           []DemoGenre
	VoteAverage      float64
	VoteCount        int
	Popularity       float64
	Status           string // "Returning Series" | "Ended"
	Type             string // "Scripted" | "Documentary"
	OriginalLanguage string
	Seasons          []DemoSeason // no season 0; ordered by SeasonNumber ASC
}

// TraktID derives the fictional Trakt id: tmdb * 10.
func (s *DemoShow) TraktID() int { return s.TmdbID * 10 }

// Year is the first-air year; 0 when unknown.
func (s *DemoShow) Year() int { return yearOf(s.FirstAirDate) }

// SeasonCount is the number of seasons (derived; keep no stored counter).
func (s *DemoShow) SeasonCount() int { return len(s.Seasons) }

// EpisodeCount is the total episode count across seasons (derived).
func (s *DemoShow) EpisodeCount() int {
	n := 0
	for _, se := range s.Seasons {
		n += se.EpisodeCount
	}
	return n
}

// traktEpisodeID derives a fictional Trakt episode id:
// tmdb*100 + episode_number (old-demo convention).
func traktEpisodeID(showTmdbID, episodeNumber int) int {
	return showTmdbID*100 + episodeNumber
}

// DemoSeason is one season of a DemoShow. ID must be unique across all
// seasons of all shows and stable across restarts (the app hard-requires
// both id and season_number on every TMDB season entry).
type DemoSeason struct {
	ID           int
	SeasonNumber int // 1-based; never 0 (no specials season)
	Name         string
	EpisodeCount int // == len(Episodes)
	AirDate      string
	PosterPath   string
	Episodes     []DemoEpisode // ordered by EpisodeNumber ASC
}

// DemoEpisode is one episode inside a DemoSeason. ID must be unique across
// the whole dataset (the Sonarr fake needs distinct integer episode ids).
type DemoEpisode struct {
	ID            int
	EpisodeNumber int
	Name          string
	AirDate       string
	Overview      string
	Runtime       int // minutes
}

// DemoBook is one catalog book (public-domain titles). The chaptarr domain
// (D8) owns and seeds these; requests/books read them through the
// bookByForeignID/allBooks hooks (contract.md). Chaptarr-shaped extensions
// (editions, quality blobs, paths) live in D8's own supplementary maps keyed
// by ForeignID.
type DemoBook struct {
	ForeignID       string // Chaptarr foreignBookId — the book's identity
	Title           string
	AuthorName      string
	AuthorForeignID string
	Overview        string
	Year            int
	// Formats present on this record, keyed bookFormatEbook /
	// bookFormatAudiobook. A missing key means the format does not exist
	// for this title (book-status must then report it unavailable).
	Formats map[string]*DemoBookFormat
}

// DemoBookFormat is the per-format library state of a DemoBook.
type DemoBookFormat struct {
	Monitored  bool
	Downloaded bool
	BookID     int // Chaptarr book record id (> 0; ebook and audiobook are separate records)
	FileID     int // bookfile id; 0 = no file on disk (Downloaded must then be false)
}

// CanonicalBookID is the Chaptarr record id used for cover art: the ebook
// record when present, else the audiobook record, else 0.
func (b *DemoBook) CanonicalBookID() int {
	if f := b.Formats[bookFormatEbook]; f != nil {
		return f.BookID
	}
	if f := b.Formats[bookFormatAudiobook]; f != nil {
		return f.BookID
	}
	return 0
}

// CoverPath is the arr-relative cover path ("" when no record) — the app
// loads it through the chaptarr instance proxy MediaCover route.
func (b *DemoBook) CoverPath() string {
	id := b.CanonicalBookID()
	if id == 0 {
		return ""
	}
	return fmt.Sprintf("/MediaCover/Books/%d/cover.jpg", id)
}

// DemoPerson is one person entry (real film-history figures with factual
// bios; fake sequential ids 1..N, NOT real TMDB person ids). Credits point
// into the catalog by TMDB id; renderers resolve title/poster/overview via
// findMovie/findShow so the data never drifts.
type DemoPerson struct {
	ID           int
	Name         string
	Biography    string
	Birthday     string // "YYYY-MM-DD"
	Deathday     string // "" when alive
	PlaceOfBirth string
	ProfilePath  string
	KnownForDept string
	Popularity   float64
	Gender       int
	ImdbID       string
	AlsoKnownAs  []string // strings only; may be empty, never nil in rendered JSON
	CastCredits  []DemoCredit
	CrewCredits  []DemoCredit
}

// DemoCredit is one filmography row of a DemoPerson.
type DemoCredit struct {
	TmdbID     int
	MediaType  string // mediaTypeMovie | mediaTypeTV — exactly these two (routing)
	Character  string // cast rows
	Job        string // crew rows, e.g. "Director"
	Department string // crew rows, e.g. "Directing"
}
