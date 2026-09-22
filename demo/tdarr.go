package main

// Read-only Tdarr fixtures. They use the same normalized response contract as
// the production adapter and never expose the fake instance URL or API key.

import (
	"net/http"
	"path"
	"time"

	"github.com/go-chi/chi/v5"
)

func registerTdarr(r chi.Router) {
	admin := r.With(requireAdmin)
	admin.Get("/tdarr/{instanceID}/activity", tdarrServe)
	admin.Get("/tdarr/{instanceID}/libraries", tdarrServe)
	admin.Get("/tdarr/{instanceID}/stats", tdarrServe)
}

func tdarrServe(w http.ResponseWriter, r *http.Request) {
	inst := instanceByID(chi.URLParam(r, "instanceID"))
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return
	}
	if inst.ServiceType != serviceTdarr {
		writeErr(w, http.StatusBadRequest, "instance is not a Tdarr instance")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	observed := time.Now().UTC()
	switch path.Base(r.URL.Path) {
	case "activity":
		writeJSON(w, http.StatusOK, map[string]any{
			"observed_at": observed,
			"nodes": []map[string]any{
				{"id": "garage-gpu", "name": "Garage GPU", "paused": false, "workers": []map[string]any{
					{"id": "gpu-0", "file": "/media/movies/Metropolis (1927)/Metropolis.mkv", "kind": "Transcode", "compute": "GPU", "status": "Transcoding", "progress_percent": 67.4, "fps": 214.8, "eta": "0:18:42", "flow": true},
					{"id": "cpu-0", "file": "/media/tv/Sherlock Holmes Adventures/Season 04/S04E09.mkv", "kind": "Health check", "compute": "CPU", "status": "Checking", "progress_percent": 31.2, "fps": nil, "eta": "0:03:11", "flow": false},
				}},
				{"id": "nas-node", "name": "NAS Node", "paused": false, "workers": []map[string]any{}},
			},
		})
	case "libraries":
		writeJSON(w, http.StatusOK, map[string]any{
			"observed_at": observed,
			"items": []map[string]string{
				{"id": "movies", "name": "Movies"},
				{"id": "tv", "name": "TV Shows"},
			},
		})
	case "stats":
		libraryID := r.URL.Query().Get("library_id")
		if libraryID != "" && libraryID != "movies" && libraryID != "tv" {
			writeErr(w, http.StatusNotFound, "Tdarr library no longer exists")
			return
		}
		total := int64(2847)
		transcodes := []map[string]any{
			{"label": "Success / not required", "value": 2384},
			{"label": "Transcode not required", "value": 141},
			{"label": "Queued", "value": 319},
			{"label": "Error / cancelled", "value": 3},
		}
		health := []map[string]any{
			{"label": "Success", "value": 2710},
			{"label": "Queued", "value": 132},
			{"label": "Error / cancelled", "value": 5},
		}
		if libraryID == "movies" {
			total = 612
			transcodes = []map[string]any{{"label": "Success / not required", "value": 501}, {"label": "Queued", "value": 110}, {"label": "Error / cancelled", "value": 1}}
			health = []map[string]any{{"label": "Success", "value": 594}, {"label": "Queued", "value": 17}, {"label": "Error / cancelled", "value": 1}}
		} else if libraryID == "tv" {
			total = 2235
			transcodes = []map[string]any{{"label": "Success / not required", "value": 2024}, {"label": "Queued", "value": 209}, {"label": "Error / cancelled", "value": 2}}
			health = []map[string]any{{"label": "Success", "value": 2116}, {"label": "Queued", "value": 115}, {"label": "Error / cancelled", "value": 4}}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"observed_at": observed, "library_id": libraryID, "total_files": total,
			"note":       "Totals include files still awaiting Tdarr's final acceptance.",
			"transcodes": transcodes, "health_checks": health,
		})
	default:
		writeErr(w, http.StatusNotFound, "Tdarr view not found")
	}
}
