// music.go — the Cantinarr-native music routes (/api/requests/music-*) and
// the music request lifecycle (create, approve, simulation, history overlay).
//
// SKELETON: minimal empty answers so the app's Music tab renders while the
// music domain lands. The music domain replaces this file wholesale; the
// function signatures are what requests.go / requests_admin.go compile
// against.
package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerMusic mounts the five requester-facing music routes.
func registerMusic(r chi.Router) {
	r.Get("/requests/music-status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": statusUnavailable, "progress": 0})
	})
	r.Get("/requests/music-library", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"titles": []any{}})
	})
	r.Get("/requests/music-recent", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
	})
	r.Get("/requests/music-artists", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"artists": []any{}, "total": 0})
	})
	r.Get("/requests/music-artist", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "artist is not in this music library")
	})
}

// reqCreateMusic handles POST /api/requests with media_type "music".
func reqCreateMusic(w http.ResponseWriter, u *DemoUser, body *reqCreateBody) {
	writeErr(w, http.StatusServiceUnavailable, "music requests are not available yet")
}

// reqAdminApproveMusic executes a pending music request.
func reqAdminApproveMusic(w http.ResponseWriter, target *reqLogRow, snapshot *reqLogRow) {
	writeErr(w, http.StatusBadRequest, "album not found for foreign id "+snapshot.ForeignID)
}

// musicRowLiveStatus is the history overlay for one music request row: the
// library's live truth for the album, else the row's denied/unavailable.
func musicRowLiveStatus(row *reqLogRow) string {
	return row.Status
}
