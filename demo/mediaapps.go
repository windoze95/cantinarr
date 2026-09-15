// mediaapps.go — the player-preference and library-access surfaces that sit
// beside the media-server access guide: which app a Watch/Listen link opens
// (/api/me/video-apps, /api/me/listening-apps), the resolved audiobook links
// for one Chaptarr book (/api/media-servers/listen), the Audiobookshelf
// per-user library assignment an admin edits
// (/api/admin/instances/{id}/media-access), and the switch that keeps an
// identity link while handing access management back to the admin
// (/api/admin/users/{id}/media-servers/{id}/account/management).
//
// Prefix: mapp… / abs…
package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// Audiobookshelf library ids the seeded instance shares. Referenced from the
// instance seed in state.go, so they live here beside everything else that
// reads them.
const (
	absLibAudiobooks = "abs-lib-audiobooks"
	absLibPodcasts   = "abs-lib-podcasts"
)

// absLibraryNames is what the media server calls each library, for the
// admin's picker and the resolved listen links.
var absLibraryNames = map[string]string{
	absLibAudiobooks: "Audiobooks",
	absLibPodcasts:   "Podcasts",
}

// mappVideoServices are the media servers a video app preference exists for,
// in the order the app iterates them. Every GET answers all three: the app
// rejects a payload missing one.
var mappVideoServices = []string{servicePlex, serviceJellyfin, serviceEmby}

var (
	mappMu sync.Mutex

	// mappVideo is userID -> serviceType -> iOS app id ("" inherits the
	// instance default; "service"/"infuse"/"browser" are the app's choices).
	mappVideo = map[int]map[string]string{}

	// mappListening is userID -> {ios, android} audiobook app ids.
	mappListening = map[int]map[string]string{}

	// absPolicies is the per-user Audiobookshelf library choice, keyed
	// "<userID>|<instanceID>". Absent means the instance default.
	absPolicies = map[string]*absPolicy{}

	// absDefaultLibraries is the instance default selection, per instance id.
	absDefaultLibraries = map[string][]string{}
)

// absPolicy is one person's library choice on one Audiobookshelf instance.
// Mode is "default" (follow the instance), "all", or "selected" (LibraryIDs
// must then be non-empty) — the same vocabulary the server validates.
type absPolicy struct {
	Mode             string
	LibraryIDs       []string
	SyncPending      bool
	ManagesLibraries bool
}

func init() {
	// The seeded requester listens through the Audiobookshelf instance with
	// an explicit selection, so the admin screen opens on a real choice
	// rather than an empty form.
	absDefaultLibraries[instAudiobookshelf] = []string{absLibAudiobooks}
	absPolicies[msvKey(2, instAudiobookshelf)] = &absPolicy{
		Mode:             "selected",
		LibraryIDs:       []string{absLibAudiobooks},
		ManagesLibraries: true,
	}
	// …and holds the account the listen links resolve against.
	acct := &msvAccount{
		UserID: 2, InstanceID: instAudiobookshelf, RemoteUserID: "abs-2",
		Username: "user", CreatedByCantinarr: true, ManageAccess: true,
		CreatedAt: chapSeedTime.Add(-11 * 24 * time.Hour),
	}
	msvAccounts[msvKey(acct.UserID, acct.InstanceID)] = acct
	msvRosters[instAudiobookshelf] = []*msvRemoteUser{
		{ID: "abs-1", Name: "abs-admin", IsAdministrator: true},
		{ID: "abs-2", Name: "user"},
		{ID: "abs-3", Name: "rowan"},
	}
}

func registerMediaApps(r chi.Router) {
	r.Get("/me/video-apps", mappVideoHandler)
	r.Put("/me/video-apps", mappVideoHandler)
	r.Get("/me/listening-apps", mappListeningHandler)
	r.Put("/me/listening-apps", mappListeningHandler)
	r.Get("/media-servers/listen", mappListenHandler)

	admin := r.With(requireAdmin)
	admin.Get("/admin/instances/{instanceID}/media-access", absAccessHandler)
	admin.Put("/admin/instances/{instanceID}/media-access", absAccessHandler)
	admin.Patch("/admin/users/{userID}/media-servers/{instanceID}/account/management", mappManagementHandler)
}

// ─── Video apps (/api/me/video-apps) ────────────────────

// mappVideoView answers every service type, always: the app throws a
// FormatException on a payload missing one, which would blank the screen.
func mappVideoView(userID int) map[string]any {
	out := map[string]any{}
	stored := mappVideo[userID]
	for _, service := range mappVideoServices {
		out[service] = map[string]string{"ios": stored[service]}
	}
	return out
}

// mappVideoApps are the iOS choices the server accepts. "" inherits.
var mappVideoApps = map[string]bool{"": true, "service": true, "infuse": true, "browser": true}

func mappVideoHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	mappMu.Lock()
	defer mappMu.Unlock()
	if r.Method == http.MethodPut {
		var body map[string]struct {
			IOS string `json:"ios"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid video app preferences")
			return
		}
		chosen := map[string]string{}
		for _, service := range mappVideoServices {
			pref, ok := body[service]
			if !ok {
				continue
			}
			if !mappVideoApps[pref.IOS] {
				writeErr(w, http.StatusBadRequest, "unknown video app")
				return
			}
			chosen[service] = pref.IOS
		}
		for service, app := range chosen {
			if mappVideo[u.ID] == nil {
				mappVideo[u.ID] = map[string]string{}
			}
			mappVideo[u.ID][service] = app
		}
	}
	writeJSON(w, http.StatusOK, mappVideoView(u.ID))
}

// ─── Listening apps (/api/me/listening-apps) ────────────

var mappListeningApps = map[string]bool{
	"": true, "browser": true, "audiobookshelf": true, "shelfplayer": true, "theshelf": true,
}

func mappListeningView(userID int) map[string]any {
	stored := mappListening[userID]
	return map[string]any{"ios": stored["ios"], "android": stored["android"]}
}

func mappListeningHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	mappMu.Lock()
	defer mappMu.Unlock()
	if r.Method == http.MethodPut {
		var body struct {
			IOS     string `json:"ios"`
			Android string `json:"android"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid listening app preferences")
			return
		}
		if !mappListeningApps[body.IOS] || !mappListeningApps[body.Android] {
			writeErr(w, http.StatusBadRequest, "unknown listening app")
			return
		}
		mappListening[u.ID] = map[string]string{"ios": body.IOS, "android": body.Android}
	}
	writeJSON(w, http.StatusOK, mappListeningView(u.ID))
}

// ─── Resolved listen links (/api/media-servers/listen) ──

// mappListenHandler resolves one authorized Chaptarr book to the audiobook
// items an Audiobookshelf instance the caller holds actually carries. Grant
// only: a user with no Audiobookshelf account gets an empty list, which says
// "nothing is linked for you", not "this book has no audio".
func mappListenHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	foreignID := strings.TrimSpace(r.URL.Query().Get("foreign_book_id"))
	if instanceID == "" || foreignID == "" {
		writeErr(w, http.StatusBadRequest, "instance_id and foreign_book_id are required")
		return
	}
	// The instance_id names the CHAPTARR instance the book came from, and the
	// caller must hold it — same authorization as any other book surface.
	held := false
	for _, id := range visibleInstanceIDs(u, serviceChaptarr) {
		if id == instanceID {
			held = true
			break
		}
	}
	if !held {
		writeErr(w, http.StatusForbidden, "you do not have access to this library")
		return
	}
	book := chapBooksByFID[foreignID]
	if book == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	out := []map[string]any{}
	mappMu.Lock()
	defer mappMu.Unlock()
	for _, inst := range msvUserServers(u) {
		if inst.ServiceType != serviceAudiobookshelf {
			continue
		}
		link := map[string]any{
			"instance_id":    inst.ID,
			"name":           inst.Name,
			"state":          "no_account",
			"fallback_url":   msvPublicAddress(inst),
			"listening_apps": mappListeningView(u.ID),
			"items":          []map[string]any{},
		}
		if msvAccountFor(u.ID, inst.ID) == nil {
			out = append(out, link)
			continue
		}
		items := []map[string]any{}
		// Only a title whose audiobook format exists AND is on disk can be
		// listened to; anything else is an honest "not here".
		if f := book.Formats[bookFormatAudiobook]; f != nil && f.Downloaded {
			for _, libraryID := range absEffectiveLibraries(u.ID, inst.ID) {
				if libraryID != absLibAudiobooks {
					continue
				}
				items = append(items, map[string]any{
					"id":           "abs-item-" + strconv.Itoa(f.BookID),
					"title":        book.Title,
					"library_name": absLibraryNames[libraryID],
					"url": msvPublicAddress(inst) + "/item/abs-item-" +
						strconv.Itoa(f.BookID),
					"narrators": []string{book.AuthorName},
				})
			}
		}
		link["items"] = items
		if len(items) > 0 {
			link["state"] = "resolved"
		} else {
			link["state"] = "not_found"
		}
		out = append(out, link)
	}
	writeJSON(w, http.StatusOK, out)
}

// ─── Audiobookshelf library access (admin) ──────────────

// absEffectiveLibraries is the library set one user actually sees: their
// explicit selection, "all" (every shared library), or the instance default.
// Caller holds mappMu.
func absEffectiveLibraries(userID int, instanceID string) []string {
	shared := []string{}
	if inst := instanceByID(instanceID); inst != nil && inst.MediaServerConfig != nil {
		shared = append(shared, inst.MediaServerConfig.LibraryIDs...)
	}
	p := absPolicies[msvKey(userID, instanceID)]
	if p == nil || p.Mode == "default" {
		if ids := absDefaultLibraries[instanceID]; len(ids) > 0 {
			return ids
		}
		return shared
	}
	if p.Mode == "all" {
		return shared
	}
	return p.LibraryIDs
}

func absPolicyView(p *absPolicy) map[string]any {
	if p == nil {
		return map[string]any{
			"mode": "default", "library_ids": []string{},
			"sync_pending": false, "manages_libraries": false,
		}
	}
	ids := p.LibraryIDs
	if ids == nil {
		ids = []string{}
	}
	return map[string]any{
		"mode": p.Mode, "library_ids": ids,
		"sync_pending": p.SyncPending, "manages_libraries": p.ManagesLibraries,
	}
}

// absAccessView is the whole screen: who is granted, the instance default,
// and every stored per-user choice.
func absAccessView(instanceID string) map[string]any {
	userIDs := []int{}
	for _, u := range allUsers() {
		for _, id := range grantedInstanceIDs(u, serviceAudiobookshelf) {
			if id == instanceID {
				userIDs = append(userIDs, u.ID)
				break
			}
		}
	}
	sort.Ints(userIDs)
	defaults := absDefaultLibraries[instanceID]
	if defaults == nil {
		defaults = []string{}
	}
	policies := map[string]any{}
	for key, p := range absPolicies {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 || parts[1] != instanceID {
			continue
		}
		policies[parts[0]] = absPolicyView(p)
	}
	return map[string]any{
		"user_ids":            userIDs,
		"default_library_ids": defaults,
		"policies":            policies,
	}
}

func absAccessHandler(w http.ResponseWriter, r *http.Request) {
	inst := instanceByID(chi.URLParam(r, "instanceID"))
	if inst == nil || inst.ServiceType != serviceAudiobookshelf {
		writeErr(w, http.StatusNotFound, "instance not found")
		return
	}
	mappMu.Lock()
	defer mappMu.Unlock()
	if r.Method == http.MethodPut {
		var body struct {
			DefaultLibraryIDs []string `json:"default_library_ids"`
			UserIDs           []int    `json:"user_ids"`
			Policies          map[string]struct {
				Mode       string   `json:"mode"`
				LibraryIDs []string `json:"library_ids"`
			} `json:"policies"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid library selection")
			return
		}
		for _, p := range body.Policies {
			if !absValidPolicy(p.Mode, p.LibraryIDs) {
				writeErr(w, http.StatusBadRequest, "invalid library selection")
				return
			}
		}
		if body.DefaultLibraryIDs == nil {
			body.DefaultLibraryIDs = []string{}
		}
		absDefaultLibraries[inst.ID] = body.DefaultLibraryIDs

		// The granted set is authoritative: a user dropped from it loses the
		// grant, exactly like the instance-grants surface.
		if err := setInstanceGrantUsers(inst.ID, body.UserIDs); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		for raw, p := range body.Policies {
			id, err := strconv.Atoi(raw)
			if err != nil {
				continue
			}
			ids := p.LibraryIDs
			if ids == nil {
				ids = []string{}
			}
			absPolicies[msvKey(id, inst.ID)] = &absPolicy{
				Mode: p.Mode, LibraryIDs: ids, ManagesLibraries: true,
			}
		}
	}
	writeJSON(w, http.StatusOK, absAccessView(inst.ID))
}

func absValidPolicy(mode string, ids []string) bool {
	switch mode {
	case "default", "all":
		return len(ids) == 0
	case "selected":
		return len(ids) > 0
	}
	return false
}

// ─── Link management (admin) ────────────────────────────

// mappManagementHandler flips who manages access on an existing link without
// touching the identity: "linked only" keeps the row, stops the writes.
func mappManagementHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil || userID <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	instanceID := chi.URLParam(r, "instanceID")
	var body struct {
		ManageAccess *bool `json:"manage_access"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.ManageAccess == nil {
		writeErr(w, http.StatusBadRequest, "manage_access is required")
		return
	}
	msvMu.Lock()
	acct := msvAccounts[msvKey(userID, instanceID)]
	if acct == nil {
		msvMu.Unlock()
		msvWriteCodedErr(w, http.StatusNotFound, "no linked account", "not_found")
		return
	}
	// An administrator account is recorded and never changed — the same
	// refusal the real server gives.
	if acct.Administrator {
		msvMu.Unlock()
		msvWriteCodedErr(w, http.StatusConflict,
			"this is a protected administrator account", "administrator")
		return
	}
	acct.ManageAccess = *body.ManageAccess
	row := msvAdminAccountRow(acct)
	msvMu.Unlock()
	writeJSON(w, http.StatusOK, row)
}
