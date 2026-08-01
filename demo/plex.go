// plex.go — admin Plex integration: link status, the simulated plex.tv PIN
// flow, server/library pickers, and invite settings. The demo seeds a LINKED
// account with a selected server so one-tap invites work out of the box.
// Spec: srv-admin-users.md §13–19, app-admin.md §8.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
)

// Seeded Plex identity (fictional account + server, gap-plan dataset item 9).
const (
	plexDemoAccount   = "demoplex"
	plexDemoMachineID = "d3m0p1exmach1ne0000000000000001"
	plexDemoServer    = "Demo Plex"
)

// plexLibrary is one canned library section of the demo's single server.
type plexLibrary struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// plexDemoLibraries is what GET .../libraries always serves for the linked
// account's server.
var plexDemoLibraries = []plexLibrary{
	{ID: 1, Title: "Movies", Type: "movie"},
	{ID: 2, Title: "TV Shows", Type: "show"},
}

// plexStateT mirrors the persisted plex settings row: the linked account and
// the invite configuration (server + shared libraries + auto-invite).
type plexStateT struct {
	Linked            bool
	Account           string
	MachineID         string
	ServerName        string
	LibrarySectionIDs []int
	AutoInvite        bool
}

var (
	plexMu    sync.Mutex
	plexState plexStateT

	// PIN-flow simulation: pin id -> completed /link/check polls. The check
	// answers linked:false for the first two polls (3 s cadence in the app)
	// and links on the third.
	plexPinPolls        = map[int64]int{}
	plexNextPinID int64 = 481000001
)

func init() {
	// Seed LINKED + configured, auto_invite off, so the users screen offers
	// one-tap invites immediately.
	plexState = plexStateT{
		Linked:            true,
		Account:           plexDemoAccount,
		MachineID:         plexDemoMachineID,
		ServerName:        plexDemoServer,
		LibrarySectionIDs: []int{1, 2},
		AutoInvite:        false,
	}
}

// plexConfigured reports whether invites can actually be sent (linked account
// AND a server selected). Contract cross-domain hook — the users-admin invite
// endpoint and the setup-status plex_invites item both call it.
func plexConfigured() bool {
	plexMu.Lock()
	defer plexMu.Unlock()
	return plexState.Linked && plexState.MachineID != ""
}

// registerPlex mounts the admin Plex integration surface (all users:manage in
// the real server; admin-gated here).
func registerPlex(r chi.Router) {
	admin := r.With(requireAdmin)
	admin.Get("/admin/plex/status", plexStatusHandler)
	admin.Post("/admin/plex/link/begin", plexLinkBeginHandler)
	admin.Post("/admin/plex/link/check", plexLinkCheckHandler)
	admin.Delete("/admin/plex/link", plexUnlinkHandler)
	admin.Get("/admin/plex/servers", plexServersHandler)
	admin.Get("/admin/plex/servers/{machineID}/libraries", plexLibrariesHandler)
	admin.Put("/admin/plex/settings", plexSettingsHandler)
}

// plexStatusJSON renders the plex.Status shape: account, machine_identifier,
// and server_name are omitempty (absent when empty); library_section_ids and
// auto_invite always present; configured is derived.
func plexStatusJSON() map[string]any {
	plexMu.Lock()
	defer plexMu.Unlock()
	ids := plexState.LibrarySectionIDs
	if ids == nil {
		ids = []int{}
	}
	out := map[string]any{
		"linked":              plexState.Linked,
		"library_section_ids": ids,
		"auto_invite":         plexState.AutoInvite,
		"configured":          plexState.Linked && plexState.MachineID != "",
	}
	if plexState.Account != "" {
		out["account"] = plexState.Account
	}
	if plexState.MachineID != "" {
		out["machine_identifier"] = plexState.MachineID
	}
	if plexState.ServerName != "" {
		out["server_name"] = plexState.ServerName
	}
	return out
}

// plexStatusHandler — GET /api/admin/plex/status. Never errors.
func plexStatusHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, plexStatusJSON())
}

// plexLinkBeginHandler — POST /api/admin/plex/link/begin. Starts the
// simulated plex.tv PIN flow: mints a pin the app polls via link/check.
func plexLinkBeginHandler(w http.ResponseWriter, _ *http.Request) {
	plexMu.Lock()
	pinID := plexNextPinID
	plexNextPinID++
	plexPinPolls[pinID] = 0
	plexMu.Unlock()
	code := randomHex(8) // 16 hex chars, matches plex.tv code length
	writeJSON(w, http.StatusOK, map[string]any{
		"pin_id": pinID,
		"code":   code,
		"url": fmt.Sprintf(
			"https://app.plex.tv/auth#?clientID=cantinarr-demo&code=%s&context%%5Bdevice%%5D%%5Bproduct%%5D=Cantinarr",
			code),
	})
}

// plexLinkCheckHandler — POST /api/admin/plex/link/check. The app polls every
// 3 s; the demo answers linked:false for the first two polls of a pin, then
// links the seeded account (persisting it, so /plex/status flips linked:true).
func plexLinkCheckHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PinID int64 `json:"pin_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PinID == 0 {
		writeErr(w, http.StatusBadRequest, "pin_id required")
		return
	}
	plexMu.Lock()
	plexPinPolls[req.PinID]++
	linked := plexPinPolls[req.PinID] >= 3
	if linked {
		delete(plexPinPolls, req.PinID)
		// Approving the PIN persists token + account. Server selection is a
		// separate step (PUT /settings) unless one is already configured.
		plexState.Linked = true
		plexState.Account = plexDemoAccount
	}
	plexMu.Unlock()
	if linked {
		writeJSON(w, http.StatusOK, map[string]any{"linked": true, "account": plexDemoAccount})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"linked": false, "account": ""})
}

// plexUnlinkHandler — DELETE /api/admin/plex/link. Forgets the account and
// every invite setting; /plex/status returns the not-linked shape afterwards
// and the setup-status plex_invites item flips configured:false.
func plexUnlinkHandler(w http.ResponseWriter, _ *http.Request) {
	plexMu.Lock()
	plexState = plexStateT{LibrarySectionIDs: []int{}}
	plexMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// plexServersHandler — GET /api/admin/plex/servers. The linked account owns
// exactly one server. Always wrapped in "servers".
func plexServersHandler(w http.ResponseWriter, _ *http.Request) {
	plexMu.Lock()
	linked := plexState.Linked
	plexMu.Unlock()
	if !linked {
		writeErr(w, http.StatusConflict, "link a Plex account first")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"servers": []map[string]any{
			{"name": plexDemoServer, "machine_identifier": plexDemoMachineID},
		},
	})
}

// plexLibrariesHandler — GET /api/admin/plex/servers/{machineID}/libraries.
// Serves the canned sections for the demo server (any machine id — the demo
// only has one). Always wrapped in "libraries".
func plexLibrariesHandler(w http.ResponseWriter, _ *http.Request) {
	plexMu.Lock()
	linked := plexState.Linked
	plexMu.Unlock()
	if !linked {
		writeErr(w, http.StatusConflict, "link a Plex account first")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": plexDemoLibraries})
}

// plexSettingsHandler — PUT /api/admin/plex/settings. Selects the server and
// libraries invites share, plus the auto-invite toggle. Empty/omitted
// library_section_ids stores [] ("all libraries"). Returns the full Status.
func plexSettingsHandler(w http.ResponseWriter, r *http.Request) {
	plexMu.Lock()
	linked := plexState.Linked
	plexMu.Unlock()
	if !linked {
		writeErr(w, http.StatusConflict, "link a Plex account first")
		return
	}
	var req struct {
		MachineIdentifier string `json:"machine_identifier"`
		ServerName        string `json:"server_name"`
		LibrarySectionIDs []int  `json:"library_section_ids"`
		AutoInvite        bool   `json:"auto_invite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MachineIdentifier == "" {
		writeErr(w, http.StatusBadRequest, "machine_identifier required")
		return
	}
	ids := req.LibrarySectionIDs
	if ids == nil {
		ids = []int{}
	}
	plexMu.Lock()
	plexState.MachineID = req.MachineIdentifier
	plexState.ServerName = req.ServerName
	plexState.LibrarySectionIDs = ids
	plexState.AutoInvite = req.AutoInvite
	plexMu.Unlock()
	writeJSON(w, http.StatusOK, plexStatusJSON())
}
