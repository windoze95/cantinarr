package watchhistory

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// InstanceSource is the subset of the instance store the handler uses. It is
// declared here rather than importing the instance package because that
// package's registry holds providers from this one; implemented by
// *instance.Store.
type InstanceSource interface {
	LookupServiceType(instanceID string) (serviceType string, found bool, err error)
}

// ProviderSource hands out cached providers per instance; implemented by
// *instance.Registry.
type ProviderSource interface {
	GetWatchHistoryProvider(instanceID string) (Provider, error)
}

// Handler serves the watch-history routes for every provider type.
type Handler struct {
	store     InstanceSource
	providers ProviderSource
}

// NewHandler creates the handler.
func NewHandler(store InstanceSource, providers ProviderSource) *Handler {
	return &Handler{store: store, providers: providers}
}

// The wire shapes below are the ones the Tautulli routes have always served;
// only fields appended after the original ones are new, so an app that knows
// nothing about Tracearr keeps decoding them.

type streamResponse struct {
	User            string `json:"user"`
	Title           string `json:"title"`
	FullTitle       string `json:"full_title"`
	Player          string `json:"player"`
	Product         string `json:"product"`
	State           string `json:"state"` // playing/paused/buffering
	ProgressPercent int    `json:"progress_percent"`
	Quality         string `json:"quality"`
	StreamType      string `json:"stream_type"` // direct play/copy/transcode
	BandwidthKbps   int    `json:"bandwidth_kbps"`
	MediaType       string `json:"media_type"`
	Server          string `json:"server"`
	ServerType      string `json:"server_type"`
}

type activityResponse struct {
	StreamCount        int              `json:"stream_count"`
	TotalBandwidthKbps int              `json:"total_bandwidth_kbps"`
	Streams            []streamResponse `json:"streams"`
}

type historyEntryResponse struct {
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

type coverageResponse struct {
	Plays     int    `json:"plays,omitempty"`
	Since     string `json:"since,omitempty"`
	Until     string `json:"until,omitempty"`
	Truncated bool   `json:"truncated"`
	Note      string `json:"note"`
}

type historyResponse struct {
	Items    []historyEntryResponse `json:"items"`
	Coverage coverageResponse       `json:"coverage"`
}

type titleStat struct {
	Title string `json:"title"`
	Plays int    `json:"plays"`
}

type userStat struct {
	User  string `json:"user"`
	Plays int    `json:"plays"`
}

type statsResponse struct {
	TopMovies []titleStat      `json:"top_movies"`
	TopShows  []titleStat      `json:"top_shows"`
	TopUsers  []userStat       `json:"top_users"`
	Coverage  coverageResponse `json:"coverage"`
}

// GetActivity returns what is playing on an instance right now.
func (h *Handler) GetActivity(w http.ResponseWriter, r *http.Request) {
	provider := h.resolveProvider(w, r)
	if provider == nil {
		return
	}
	activity, err := provider.Activity(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	resp := activityResponse{
		StreamCount:        activity.StreamCount,
		TotalBandwidthKbps: activity.TotalBandwidthKbps,
		Streams:            make([]streamResponse, 0, len(activity.Streams)),
	}
	for _, s := range activity.Streams {
		resp.Streams = append(resp.Streams, streamResponse{
			User:            s.User,
			Title:           s.Title,
			FullTitle:       s.FullTitle,
			Player:          s.Player,
			Product:         s.Product,
			State:           s.State,
			ProgressPercent: s.ProgressPercent,
			Quality:         s.Quality,
			StreamType:      s.StreamType,
			BandwidthKbps:   s.BandwidthKbps,
			MediaType:       s.MediaType,
			Server:          s.Server,
			ServerType:      s.ServerType,
		})
	}
	writeJSON(w, resp)
}

// GetHistory returns recent plays. ?limit=N (default 50).
func (h *Handler) GetHistory(w http.ResponseWriter, r *http.Request) {
	provider := h.resolveProvider(w, r)
	if provider == nil {
		return
	}
	limit := positiveQueryInt(r, "limit", 50)
	history, err := provider.History(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	resp := historyResponse{
		Items:    make([]historyEntryResponse, 0, len(history.Items)),
		Coverage: coverageOf(history.Coverage),
	}
	for _, item := range history.Items {
		date := ""
		if !item.Date.IsZero() {
			date = item.Date.UTC().Format(time.RFC3339)
		}
		resp.Items = append(resp.Items, historyEntryResponse{
			User:            item.User,
			FullTitle:       item.FullTitle,
			Date:            date,
			DurationSeconds: item.DurationSeconds,
			PercentComplete: item.PercentComplete,
			Player:          item.Player,
			Platform:        item.Platform,
			MediaType:       item.MediaType,
			Server:          item.Server,
			ServerType:      item.ServerType,
		})
	}
	writeJSON(w, resp)
}

// GetStats returns the top movies/shows/users. ?days=N (default 30).
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	provider := h.resolveProvider(w, r)
	if provider == nil {
		return
	}
	days := positiveQueryInt(r, "days", 30)
	stats, err := provider.Stats(r.Context(), days)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	resp := statsResponse{
		TopMovies: make([]titleStat, 0, len(stats.TopMovies)),
		TopShows:  make([]titleStat, 0, len(stats.TopShows)),
		TopUsers:  make([]userStat, 0, len(stats.TopUsers)),
		Coverage:  coverageOf(stats.Coverage),
	}
	for _, row := range stats.TopMovies {
		resp.TopMovies = append(resp.TopMovies, titleStat{Title: row.Title, Plays: row.Plays})
	}
	for _, row := range stats.TopShows {
		resp.TopShows = append(resp.TopShows, titleStat{Title: row.Title, Plays: row.Plays})
	}
	for _, row := range stats.TopUsers {
		resp.TopUsers = append(resp.TopUsers, userStat{User: row.User, Plays: row.Plays})
	}
	writeJSON(w, resp)
}

// positiveQueryInt reads a positive integer query parameter, falling back to
// def for anything missing, junk, or non-positive.
func positiveQueryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func coverageOf(c Coverage) coverageResponse {
	out := coverageResponse{Plays: c.Plays, Truncated: c.Truncated, Note: c.Note}
	if !c.Since.IsZero() {
		out.Since = c.Since.UTC().Format(time.RFC3339)
	}
	if !c.Until.IsZero() {
		out.Until = c.Until.UTC().Format(time.RFC3339)
	}
	return out
}

// resolveProvider loads the instance named in the path, verifies it is a
// watch-history provider, and returns its provider. On failure it writes the
// error response and returns nil. The service-type check runs on the
// metadata-only lookup so a wrong-type answer never decrypts credentials.
func (h *Handler) resolveProvider(w http.ResponseWriter, r *http.Request) Provider {
	instanceID := chi.URLParam(r, "instanceID")
	serviceType, found, err := h.store.LookupServiceType(instanceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil
	}
	if !found {
		writeError(w, http.StatusNotFound, "instance not found")
		return nil
	}
	if !IsServiceType(serviceType) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("instance %s is not a watch-history instance (%s)", instanceID, ServiceTypeList()))
		return nil
	}
	provider, err := h.providers.GetWatchHistoryProvider(instanceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil
	}
	return provider
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
