package main

// Content-first download activity for the current app. The existing
// downloads.go owns client-shaped queue/history responses; this file projects
// those same mutable jobs into title-shaped rows without inventing a second
// queue.

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

var (
	dlActivitySettingsMu sync.Mutex
	dlActivityScope      = "all"
)

func dlActivityUserScope() string {
	dlActivitySettingsMu.Lock()
	defer dlActivitySettingsMu.Unlock()
	return dlActivityScope
}

func registerDownloadActivity(r chi.Router) {
	r.Get("/downloads/activity", dlActivityHandler(false))
	r.Get("/downloads/summary", dlActivityHandler(true))
	r.With(requireAdmin).Get("/admin/downloads/settings", dlActivitySettingsHandler)
	r.With(requireAdmin).Put("/admin/downloads/settings", dlActivitySettingsHandler)
}

type dlActivityJob struct {
	ID            string         `json:"id"`
	Status        string         `json:"status"`
	Name          string         `json:"name,omitempty"`
	SizeBytes     int64          `json:"size_bytes"`
	SizeLeftBytes int64          `json:"size_left_bytes"`
	Progress      float64        `json:"progress"`
	SpeedBPS      int64          `json:"speed_bps"`
	Control       map[string]any `json:"control,omitempty"`
}

type dlActivityChild struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Season  *int     `json:"season,omitempty"`
	Episode int      `json:"episode,omitempty"`
	Disc    int      `json:"disc,omitempty"`
	Track   string   `json:"track,omitempty"`
	JobIDs  []string `json:"job_ids"`
}

type dlActivityGroup struct {
	ID           string            `json:"id"`
	InstanceID   string            `json:"instance_id,omitempty"`
	InstanceName string            `json:"instance_name,omitempty"`
	MediaType    string            `json:"media_type"`
	Title        string            `json:"title"`
	Year         int               `json:"year,omitempty"`
	Creator      string            `json:"creator,omitempty"`
	Format       string            `json:"format,omitempty"`
	Artwork      string            `json:"artwork,omitempty"`
	JobIDs       []string          `json:"job_ids"`
	Children     []dlActivityChild `json:"children"`
	DetailsKnown bool              `json:"details_known"`
	Progress     float64           `json:"progress"`
	requestedBy  map[int]bool
	tmdbID       int
}

func dlActivityHandler(summary bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope := r.URL.Query().Get("scope")
		if scope == "" {
			scope = "all"
		}
		if scope != "all" && scope != "mine" {
			writeErr(w, http.StatusBadRequest, "scope must be all or mine")
			return
		}
		u := userFrom(r)
		userScope := dlActivityUserScope()
		if u.Role == roleAdmin {
			scope = "all"
		} else if userScope == "mine" {
			scope = "mine"
		}
		groups, jobs := dlActivitySnapshot(u, scope)
		count := len(groups)
		payload := map[string]any{
			"scope": scope, "user_scope": userScope, "count": count,
			"complete": true, "stale": false, "fetched_at": time.Now().UTC(),
			"sources": []map[string]any{
				{"instance_id": instSab, "name": "SABnzbd", "available": true},
				{"instance_id": instQbittorrent, "name": "qBittorrent", "available": true},
			},
		}
		if !summary {
			payload["groups"], payload["jobs"] = groups, jobs
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, payload)
	}
}

func dlActivitySettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		var body struct {
			UserScope string `json:"user_scope"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		dec.DisallowUnknownFields()
		if dec.Decode(&body) != nil || (body.UserScope != "all" && body.UserScope != "mine") {
			writeErr(w, http.StatusBadRequest, "user_scope must be all or mine")
			return
		}
		dlActivitySettingsMu.Lock()
		dlActivityScope = body.UserScope
		dlActivitySettingsMu.Unlock()
		wsBroadcast("config_changed", map[string]any{})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"user_scope": dlActivityUserScope()})
}

// dlActivityMeta recognizes the deliberately seeded queue labels. A real
// server joins by provider IDs; the demo keeps the same safety property by
// refusing to guess anything not in this closed fixture table.
func dlActivityMeta(name string) *dlActivityGroup {
	lower := strings.ToLower(name)
	group := func(id, mediaType, title string, year, tmdbID int, instanceID, instanceName string, requesters ...int) *dlActivityGroup {
		by := map[int]bool{}
		for _, id := range requesters {
			by[id] = true
		}
		return &dlActivityGroup{ID: id, MediaType: mediaType, Title: title, Year: year,
			InstanceID: instanceID, InstanceName: instanceName, DetailsKnown: true,
			JobIDs: []string{}, Children: []dlActivityChild{}, requestedBy: by, tmdbID: tmdbID}
	}
	switch {
	case strings.Contains(lower, "metropolis"):
		return group("movie:19", mediaTypeMovie, "Metropolis", 1927, 19, instRadarr, "Radarr", 2)
	case strings.Contains(lower, "the.general"):
		return group("movie:961", mediaTypeMovie, "The General", 1926, 961, instRadarr, "Radarr", 1, 4)
	case strings.Contains(lower, "caligari"):
		return group("movie:234", mediaTypeMovie, "The Cabinet of Dr. Caligari", 1920, 234, instRadarr, "Radarr", 2)
	case strings.Contains(lower, "nosferatu"):
		return group("movie:653", mediaTypeMovie, "Nosferatu", 1922, 653, instRadarr, "Radarr")
	case strings.Contains(lower, "sherlock.holmes.adventures"):
		g := group("tv:90001", mediaTypeTV, "Sherlock Holmes Adventures", 2023, 90001, instSonarr, "Sonarr", 2)
		season := 4
		g.Children = []dlActivityChild{{ID: "900010409", Title: "The Blue Carbuncle", Season: &season, Episode: 9, JobIDs: []string{}}}
		return g
	case strings.Contains(lower, "moby.dick"):
		g := group("book:153747:audiobook", mediaTypeBook, "Moby-Dick", 1851, 0, instChaptarr, "Chaptarr")
		g.Creator, g.Format = "Herman Melville", bookFormatAudiobook
		return g
	case strings.Contains(lower, "original.dixieland"):
		g := group("music:odjb-1917", mediaTypeMusic, "Livery Stable Blues: The 1917 Sessions", 1917, 0, instLidarr, "Lidarr", 2)
		g.Creator = "Original Dixieland Jass Band"
		g.Children = []dlActivityChild{{ID: "odjb-track-1", Title: "Livery Stable Blues", Disc: 1, Track: "1", JobIDs: []string{}}}
		return g
	}
	return nil
}

func dlActivityStatus(raw string, paused bool) string {
	s := strings.ToLower(raw)
	switch {
	case paused || strings.Contains(s, "paused") || strings.Contains(s, "stopped"):
		return "paused"
	case strings.Contains(s, "stall"):
		return "stalled"
	case strings.Contains(s, "fail") || strings.Contains(s, "error") || strings.Contains(s, "missing"):
		return "failed"
	case strings.Contains(s, "queue") || strings.Contains(s, "wait"):
		return "queued"
	default:
		return "downloading"
	}
}

func dlActivitySnapshot(u *DemoUser, scope string) ([]dlActivityGroup, []dlActivityJob) {
	dlMu.Lock()
	defer dlMu.Unlock()
	groups := map[string]*dlActivityGroup{}
	jobs := map[string]dlActivityJob{}
	appendJob := func(clientID, clientName, serviceType, itemID, name, status string,
		size, left, speed int64, paused bool) {
		meta := dlActivityMeta(name)
		if meta == nil {
			return
		}
		if scope == "mine" && !meta.requestedBy[u.ID] {
			return
		}
		if u.Role != roleAdmin {
			if meta.InstanceID != "" && !userCanSeeInstance(u, meta.InstanceID) {
				return
			}
			if (meta.MediaType == mediaTypeMovie || meta.MediaType == mediaTypeTV) && !cpAllowsTmdb(u, meta.MediaType, meta.tmdbID) {
				return
			}
		}
		jobID := clientID + ":" + itemID
		job := dlActivityJob{ID: jobID, Status: dlActivityStatus(status, paused),
			SizeBytes: size, SizeLeftBytes: left, SpeedBPS: speed}
		if size > 0 {
			job.Progress = float64(size-left) / float64(size) * 100
		}
		if u.Role == roleAdmin {
			job.Name = name
			job.Control = map[string]any{"instance_id": clientID, "item_id": itemID,
				"service_type": serviceType, "client_name": clientName}
		}
		jobs[jobID] = job
		g := groups[meta.ID]
		if g == nil {
			g = meta
			groups[g.ID] = g
		}
		g.JobIDs = append(g.JobIDs, jobID)
		for i := range g.Children {
			g.Children[i].JobIDs = append(g.Children[i].JobIDs, jobID)
		}
	}
	for _, item := range dlItems {
		appendJob(instSab, "SABnzbd", serviceSabnzbd, item.ID, item.Name, item.Status,
			item.SizeBytes, item.LeftBytes, 0, dlPaused)
	}
	for _, item := range dlTorrents {
		if item.Progress >= 1 {
			continue
		}
		left := int64(float64(item.Size) * (1 - item.Progress))
		appendJob(instQbittorrent, "qBittorrent", serviceQbittorrent, item.Hash, item.Name,
			item.State, item.Size, left, item.DLSpeed, false)
	}
	outGroups := make([]dlActivityGroup, 0, len(groups))
	for _, g := range groups {
		var size, left int64
		for _, id := range g.JobIDs {
			size += jobs[id].SizeBytes
			left += jobs[id].SizeLeftBytes
		}
		if size > 0 {
			g.Progress = float64(size-left) / float64(size) * 100
		}
		outGroups = append(outGroups, *g)
	}
	sort.Slice(outGroups, func(i, j int) bool { return outGroups[i].Title < outGroups[j].Title })
	outJobs := make([]dlActivityJob, 0, len(jobs))
	for _, job := range jobs {
		outJobs = append(outJobs, job)
	}
	sort.Slice(outJobs, func(i, j int) bool { return outJobs[i].ID < outJobs[j].ID })
	return outGroups, outJobs
}
