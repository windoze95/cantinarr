package tautulli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// InstanceSource is the subset of the instance store used by the handler. It
// is defined here rather than importing the instance package because that
// package's registry imports this one for its client cache; implemented by
// *instance.Store.
type InstanceSource interface {
	LookupServiceType(instanceID string) (serviceType string, found bool, err error)
}

// ClientSource provides cached Tautulli clients; implemented by
// *instance.Registry.
type ClientSource interface {
	GetTautulliClient(instanceID string) (*Client, error)
}

// Handler provides REST endpoints for Tautulli instances.
type Handler struct {
	store    InstanceSource
	registry ClientSource
}

// NewHandler creates a new Tautulli handler.
func NewHandler(store InstanceSource, registry ClientSource) *Handler {
	return &Handler{store: store, registry: registry}
}

// activityStream is the normalized shape of a single active stream.
type activityStream struct {
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
}

type activityResponse struct {
	StreamCount        int              `json:"stream_count"`
	TotalBandwidthKbps int              `json:"total_bandwidth_kbps"`
	Streams            []activityStream `json:"streams"`
}

// GetActivity returns the current streaming activity for an instance.
func (h *Handler) GetActivity(w http.ResponseWriter, r *http.Request) {
	client := h.resolveClient(w, r)
	if client == nil {
		return
	}

	activity, err := client.GetActivity()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	resp := activityResponse{
		StreamCount:        int(activity.StreamCount),
		TotalBandwidthKbps: int(activity.TotalBandwidth),
		Streams:            make([]activityStream, 0, len(activity.Sessions)),
	}
	for _, s := range activity.Sessions {
		resp.Streams = append(resp.Streams, activityStream{
			User:            s.User,
			Title:           s.Title,
			FullTitle:       s.FullTitle,
			Player:          s.Player,
			Product:         s.Product,
			State:           s.State,
			ProgressPercent: int(s.ProgressPercent),
			Quality:         s.QualityProfile,
			StreamType:      s.TranscodeDecision,
			BandwidthKbps:   int(s.Bandwidth),
		})
	}
	writeJSON(w, resp)
}

// historyEntry is the normalized shape of a single watch-history entry.
type historyEntry struct {
	User            string `json:"user"`
	FullTitle       string `json:"full_title"`
	Date            string `json:"date"` // RFC3339 UTC, "" if unknown
	DurationSeconds int    `json:"duration_seconds"`
	PercentComplete int    `json:"percent_complete"`
	Player          string `json:"player"`
	Platform        string `json:"platform"`
}

type historyResponse struct {
	Items []historyEntry `json:"items"`
}

// GetHistory returns recent watch history. ?limit=N (default 50).
func (h *Handler) GetHistory(w http.ResponseWriter, r *http.Request) {
	client := h.resolveClient(w, r)
	if client == nil {
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	rows, err := client.GetHistory(limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	resp := historyResponse{Items: make([]historyEntry, 0, len(rows))}
	for _, row := range rows {
		date := ""
		if row.Date > 0 {
			date = time.Unix(int64(row.Date), 0).UTC().Format(time.RFC3339)
		}
		resp.Items = append(resp.Items, historyEntry{
			User:            row.User,
			FullTitle:       row.FullTitle,
			Date:            date,
			DurationSeconds: int(row.Duration),
			PercentComplete: int(row.PercentComplete),
			Player:          row.Player,
			Platform:        row.Platform,
		})
	}
	writeJSON(w, resp)
}

// titleStat is a play count keyed by title (top movies/shows).
type titleStat struct {
	Title string `json:"title"`
	Plays int    `json:"plays"`
}

// userStat is a play count keyed by user (top users).
type userStat struct {
	User  string `json:"user"`
	Plays int    `json:"plays"`
}

type statsResponse struct {
	TopMovies []titleStat `json:"top_movies"`
	TopShows  []titleStat `json:"top_shows"`
	TopUsers  []userStat  `json:"top_users"`
}

// GetStats returns the top movies/shows/users. ?days=N (default 30).
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	client := h.resolveClient(w, r)
	if client == nil {
		return
	}

	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}

	stats, err := client.GetHomeStats(days)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	resp := statsResponse{
		TopMovies: make([]titleStat, 0),
		TopShows:  make([]titleStat, 0),
		TopUsers:  make([]userStat, 0),
	}
	for _, stat := range stats {
		switch stat.StatID {
		case "top_movies":
			for _, row := range stat.Rows {
				resp.TopMovies = append(resp.TopMovies, titleStat{Title: row.Title, Plays: int(row.TotalPlays)})
			}
		case "top_tv":
			for _, row := range stat.Rows {
				resp.TopShows = append(resp.TopShows, titleStat{Title: row.Title, Plays: int(row.TotalPlays)})
			}
		case "top_users":
			for _, row := range stat.Rows {
				user := row.FriendlyName
				if user == "" {
					user = row.User
				}
				resp.TopUsers = append(resp.TopUsers, userStat{User: user, Plays: int(row.TotalPlays)})
			}
		}
	}
	writeJSON(w, resp)
}

// resolveClient loads the instance from the path, verifies it is a Tautulli
// instance, and returns its client. On failure it writes the error response
// and returns nil.
func (h *Handler) resolveClient(w http.ResponseWriter, r *http.Request) *Client {
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
	if serviceType != "tautulli" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("instance %s is not a tautulli instance", instanceID))
		return nil
	}
	client, err := h.registry.GetTautulliClient(instanceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil
	}
	return client
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
