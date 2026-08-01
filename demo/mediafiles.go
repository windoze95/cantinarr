// mediafiles.go — completed-media downloads (srv-instances §5, app-arr §7,
// gap-plan §1.15): lexical coverage verdicts, 10-minute capability tickets,
// and the public self-authorizing download route that serves a bundled
// public-domain sample file with Range support.
//
// registerMediaFiles receives the PUBLIC /api router (main.go): it applies
// requireAuth itself to coverage + tickets and leaves GET|HEAD
// /media-files/download/{ticket} public.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "embed"

	"github.com/go-chi/chi/v5"
)

//go:embed assets/sample_download.txt
var mfSampleData []byte

// mfModTime backs If-Modified-Since / If-Range handling in ServeContent.
var mfModTime = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

const (
	mfTicketTTL     = 10 * time.Minute
	mfPerUserCap    = 32
	mfGlobalCap     = 1024
	mfMaxPaths      = 200
	mfDownloadRoute = "/api/media-files/download/"
)

// ─── Ticket store (guarded by mfMu) ─────────────────────

type mfTicket struct {
	Token      string
	UserID     int
	InstanceID string
	FileID     int
	Filename   string
	ExpiresAt  time.Time
}

var (
	mfMu      sync.Mutex
	mfTickets = map[string]*mfTicket{} // token -> ticket
	mfByKey   = map[string]string{}    // "user|instance|file" -> token
)

func mfKey(userID int, instanceID string, fileID int) string {
	return fmt.Sprintf("%d|%s|%d", userID, instanceID, fileID)
}

func mfPurgeExpiredLocked(now time.Time) {
	for tok, t := range mfTickets {
		if now.After(t.ExpiresAt) {
			delete(mfTickets, tok)
			delete(mfByKey, mfKey(t.UserID, t.InstanceID, t.FileID))
		}
	}
}

func mfNewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b) // 43 chars
}

// ─── Register ───────────────────────────────────────────

// registerMediaFiles mounts /api/media-files/* on the public /api router.
func registerMediaFiles(r chi.Router) {
	r.Group(func(pr chi.Router) {
		pr.Use(requireAuth)
		pr.Post("/media-files/coverage", mfHandleCoverage)
		pr.Post("/media-files/tickets", mfHandleTickets)
	})
	// Self-authorizing capability URL — all methods land here so non-GET/HEAD
	// can answer 405 with an Allow header.
	r.HandleFunc("/media-files/download/{ticket}", mfHandleDownload)
}

// ─── Coverage ───────────────────────────────────────────

// mfHandleCoverage answers a purely lexical per-path verdict. The media-files
// feature is ENABLED in the demo and a failed coverage call disables the
// whole feature client-side for the session — so every well-formed request
// gets a 200 (unknown instances simply cover nothing).
func mfHandleCoverage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstanceID string   `json:"instance_id"`
		Paths      []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if body.InstanceID == "" || len(body.Paths) == 0 || len(body.Paths) > mfMaxPaths {
		writeErr(w, http.StatusBadRequest, "instance_id and 1-200 paths are required")
		return
	}
	var mappings []map[string]string
	if inst := instMgmtResolve(body.InstanceID); inst != nil {
		mappings = inst.MediaPathMappings
	}
	covered := make([]bool, 0, len(body.Paths))
	for _, p := range body.Paths {
		covered = append(covered, mfPathCovered(mappings, p))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"covered": covered})
}

// mfPathCovered reports whether an arr-reported path would translate through
// a mapping into the configured media root (lexical only, never touches the
// filesystem).
func mfPathCovered(mappings []map[string]string, path string) bool {
	for _, m := range mappings {
		arrPath := m["arr_path"]
		cantinarrPath := m["cantinarr_path"]
		if arrPath == "" {
			continue
		}
		if path != arrPath && !strings.HasPrefix(path, strings.TrimRight(arrPath, "/")+"/") {
			continue
		}
		if cantinarrPath == instMgmtMediaRoot || strings.HasPrefix(cantinarrPath, instMgmtMediaRoot+"/") {
			return true
		}
	}
	return false
}

// ─── Tickets ────────────────────────────────────────────

func mfHandleTickets(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstanceID string `json:"instance_id"`
		FileID     int    `json:"file_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if body.InstanceID == "" || len(body.InstanceID) > 128 || body.FileID <= 0 {
		writeErr(w, http.StatusBadRequest, "instance_id and a positive file_id are required")
		return
	}
	u := userFrom(r)
	inst := instMgmtResolve(body.InstanceID)
	if inst == nil || !instMgmtIsArrType(inst.ServiceType) {
		writeErr(w, http.StatusNotFound, "media file unavailable")
		return
	}
	if u.Role != roleAdmin {
		eff := effectiveInstanceFor(u, inst.ServiceType)
		if eff == nil || eff.ID != inst.ID {
			writeErr(w, http.StatusForbidden, "permission denied")
			return
		}
	}
	if len(inst.MediaPathMappings) == 0 {
		writeErr(w, http.StatusNotFound, "media file unavailable")
		return
	}

	now := time.Now()
	mfMu.Lock()
	mfPurgeExpiredLocked(now)
	key := mfKey(u.ID, inst.ID, body.FileID)
	// Same-token reuse: a live ticket for the same (user, instance, file)
	// is returned verbatim with its original expiry.
	if tok, ok := mfByKey[key]; ok {
		if t := mfTickets[tok]; t != nil {
			mfMu.Unlock()
			mfWriteTicket(w, t)
			return
		}
	}
	if len(mfTickets) >= mfGlobalCap {
		mfMu.Unlock()
		writeErr(w, http.StatusServiceUnavailable, "download service is busy, retry shortly")
		return
	}
	perUser := 0
	for _, t := range mfTickets {
		if t.UserID == u.ID {
			perUser++
		}
	}
	if perUser >= mfPerUserCap {
		mfMu.Unlock()
		writeErr(w, http.StatusTooManyRequests, "too many active download tickets")
		return
	}
	t := &mfTicket{
		Token:      mfNewToken(),
		UserID:     u.ID,
		InstanceID: inst.ID,
		FileID:     body.FileID,
		Filename:   fmt.Sprintf("cantinarr-demo-%s-file-%d.txt", inst.ServiceType, body.FileID),
		ExpiresAt:  now.Add(mfTicketTTL),
	}
	mfTickets[t.Token] = t
	mfByKey[key] = t.Token
	mfMu.Unlock()
	mfWriteTicket(w, t)
}

func mfWriteTicket(w http.ResponseWriter, t *mfTicket) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"url":        mfDownloadRoute + t.Token,
		"filename":   t.Filename,
		"size_bytes": len(mfSampleData),
		"expires_at": t.ExpiresAt.UTC(),
	})
}

// ─── Download (public capability) ───────────────────────

// mfSecurityHeaders applies the headers required on every download response,
// success and error alike.
func mfSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "private, no-store")
	h.Set("Pragma", "no-cache")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}

// mfDownloadError writes an error status; HEAD errors carry no body.
func mfDownloadError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if r.Method == http.MethodHead {
		w.Header().Del("Content-Type")
		w.WriteHeader(status)
		return
	}
	writeErr(w, status, msg)
}

func mfHandleDownload(w http.ResponseWriter, r *http.Request) {
	mfSecurityHeaders(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	token := chi.URLParam(r, "ticket")
	now := time.Now()
	mfMu.Lock()
	mfPurgeExpiredLocked(now)
	t := mfTickets[token] // tickets are reusable until expiry — never consumed
	mfMu.Unlock()
	if t == nil {
		mfDownloadError(w, r, http.StatusNotFound, "download unavailable")
		return
	}
	// Re-validate on every request: user still exists, instance still exists
	// with the same service type, non-admins still bound to their effective
	// instance, mappings still effective.
	u := userByID(t.UserID)
	inst := instMgmtResolve(t.InstanceID)
	if u == nil || inst == nil || !instMgmtIsArrType(inst.ServiceType) || len(inst.MediaPathMappings) == 0 {
		mfDownloadError(w, r, http.StatusNotFound, "download unavailable")
		return
	}
	if u.Role != roleAdmin {
		eff := effectiveInstanceFor(u, inst.ServiceType)
		if eff == nil || eff.ID != inst.ID {
			mfDownloadError(w, r, http.StatusNotFound, "download unavailable")
			return
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream") // never sniffed
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", t.Filename))
	// ServeContent supplies Range / If-Range / If-Modified-Since handling
	// (200 / 206 / 304) and Accept-Ranges: bytes; HEAD gets headers only.
	http.ServeContent(w, r, "", mfModTime, bytes.NewReader(mfSampleData))
}
