// instances.go — instance management (CRUD, per-user pins, webhook stub) and
// the arr-proxy dispatcher /api/instances/{instanceID}/* (srv-instances §1–2,
// app-admin §4, gap-plan §1.9).
//
// Created/deleted instances go straight into the shared registry
// (registerInstance/removeInstance) so config, per-user defaults, pins, and
// the proxy stay coherent. All rendering goes through copies so no shared
// pointer escapes a lock.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// instMgmtMediaRoot is the single configured media root (contract.md §3).
const instMgmtMediaRoot = "/media"

// instMgmtServiceTypes is the exact service_type enum (srv-instances §0).
var instMgmtServiceTypes = map[string]bool{
	serviceRadarr: true, serviceSonarr: true, serviceChaptarr: true,
	serviceSabnzbd: true, serviceQbittorrent: true, serviceNzbget: true,
	serviceTransmission: true, serviceTautulli: true,
	serviceJellyfin: true, serviceEmby: true, servicePlex: true,
}

const instMgmtEnumError = "service_type must be one of 'radarr', 'sonarr', 'chaptarr', 'sabnzbd', 'qbittorrent', 'nzbget', 'transmission', 'tautulli', 'jellyfin', 'emby', 'plex'"

const instMgmtMediaServerEnumError = "service_type must be a media server type ('jellyfin', 'emby', 'plex')"

// ─── Domain-local overlay state ─────────────────────────

var (
	instMgmtMu         sync.Mutex
	instMgmtSortOrders = map[string]int{}  // instance id -> sort_order (absent = 0)
	instMgmtWebhookSet = map[string]bool{} // instance id -> webhook already configured
)

// instMgmtResolve returns a defensive copy of the instance with the given id,
// or nil when unknown.
func instMgmtResolve(id string) *DemoInstance {
	var cp *DemoInstance
	withInstance(id, func(i *DemoInstance) {
		c := *i
		c.MediaPathMappings = append([]map[string]string{}, i.MediaPathMappings...)
		c.MediaServerConfig = i.MediaServerConfig.clone()
		cp = &c
	})
	return cp
}

// instMgmtWith mutates an instance under the state lock; reports whether it
// exists. fn must only set plain fields.
func instMgmtWith(id string, fn func(*DemoInstance)) bool {
	return withInstance(id, fn)
}

// instMgmtAll returns copies of every live instance, unsorted.
func instMgmtAll() []*DemoInstance {
	out := []*DemoInstance{}
	for _, i := range allInstances() {
		if cp := instMgmtResolve(i.ID); cp != nil {
			out = append(out, cp)
		}
	}
	return out
}

func instMgmtSortOrderOf(id string) int {
	instMgmtMu.Lock()
	defer instMgmtMu.Unlock()
	return instMgmtSortOrders[id]
}

// instMgmtJSON renders one instanceResponse. url/username/media_path_mappings
// keys are always present (editor prefill); secrets never appear.
func instMgmtJSON(inst *DemoInstance) map[string]any {
	mappings := make([]map[string]string, 0, len(inst.MediaPathMappings))
	for _, m := range inst.MediaPathMappings {
		mappings = append(mappings, map[string]string{
			"arr_path":       m["arr_path"],
			"cantinarr_path": m["cantinarr_path"],
		})
	}
	out := map[string]any{
		"id":                  inst.ID,
		"service_type":        inst.ServiceType,
		"name":                inst.Name,
		"url":                 inst.URL,
		"username":            inst.Username,
		"is_default":          inst.IsDefault,
		"sort_order":          instMgmtSortOrderOf(inst.ID),
		"media_downloads":     inst.MediaDownloads,
		"media_path_mappings": mappings,
	}
	// media_server_config is present only for media servers, and only in its
	// public shape — the server-managed link identity (client id, the Plex
	// owner) is never served.
	if cfg := inst.MediaServerConfig; cfg != nil && isMediaServerType(inst.ServiceType) {
		msc := map[string]any{
			"public_address": cfg.PublicAddress,
			"library_ids":    append([]string{}, cfg.LibraryIDs...),
		}
		if cfg.MachineIdentifier != "" {
			msc["machine_identifier"] = cfg.MachineIdentifier
		}
		if cfg.AutoApprove {
			msc["auto_approve"] = true
		}
		out["media_server_config"] = msc
	}
	return out
}

// ─── Request body shapes ────────────────────────────────

type instMgmtMapping struct {
	ArrPath       string `json:"arr_path"`
	CantinarrPath string `json:"cantinarr_path"`
}

type instMgmtBody struct {
	ID                string             `json:"id"`
	ServiceType       string             `json:"service_type"`
	Name              string             `json:"name"`
	URL               string             `json:"url"`
	APIKey            string             `json:"api_key"`
	Username          string             `json:"username"`
	Password          string             `json:"password"`
	IsDefault         bool               `json:"is_default"`
	SortOrder         *int               `json:"sort_order"`
	MediaPathMappings *[]instMgmtMapping `json:"media_path_mappings"` // nil = key omitted
	// MediaServerConfig is nil when the key is omitted (keep stored),
	// non-nil when present (replace).
	MediaServerConfig *instMgmtMediaServerBody `json:"media_server_config"`
	// PlexLinkPin references a PIN link this admin already approved. The
	// token it yields is held server-side and never travels to the app.
	PlexLinkPin int64 `json:"plex_link_pin"`
}

// instMgmtMediaServerBody is the media-server config as an admin sends it.
// The server-managed fields (client id, Plex owner) are deliberately absent:
// they are recorded by the PIN link, never taken from a request.
type instMgmtMediaServerBody struct {
	PublicAddress     string   `json:"public_address"`
	LibraryIDs        []string `json:"library_ids"`
	MachineIdentifier string   `json:"machine_identifier"`
	AutoApprove       bool     `json:"auto_approve"`
}

func instMgmtIsArrType(st string) bool {
	return st == serviceRadarr || st == serviceSonarr || st == serviceChaptarr
}

// instMgmtValidateMediaServerConfig turns the request shape into the stored
// one, or returns the message to answer 400 with. A Plex instance must name
// the server it shares — without a machine identifier there is nothing to
// invite anyone to.
func instMgmtValidateMediaServerConfig(serviceType string, body *instMgmtMediaServerBody) (*DemoMediaServerConfig, string) {
	if body == nil {
		return nil, ""
	}
	if !isMediaServerType(serviceType) {
		return nil, "media_server_config is supported only for media servers ('jellyfin', 'emby', 'plex')"
	}
	address := strings.TrimRight(strings.TrimSpace(body.PublicAddress), "/")
	if address != "" {
		pu, err := url.Parse(address)
		if err != nil || !pu.IsAbs() || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
			return nil, "invalid media_server_config: public_address must be an absolute http or https URL"
		}
		if pu.User != nil || pu.RawQuery != "" || pu.Fragment != "" {
			return nil, "invalid media_server_config: public_address must not contain credentials, a query string, or a fragment"
		}
	}
	ids := []string{}
	for _, id := range body.LibraryIDs {
		if len(id) > 128 {
			return nil, "invalid media_server_config: library id is too long"
		}
		if strings.ContainsAny(id, "/\\ ") {
			return nil, "invalid media_server_config: library id contains invalid characters"
		}
		ids = append(ids, id)
	}
	machine := strings.TrimSpace(body.MachineIdentifier)
	if len(machine) > 128 {
		return nil, "invalid media_server_config: machine identifier is too long"
	}
	if strings.ContainsAny(machine, "/\\ ") {
		return nil, "invalid media_server_config: machine identifier contains invalid characters"
	}
	if serviceType == servicePlex && machine == "" {
		return nil, "pick the Plex server to share (media_server_config.machine_identifier)"
	}
	if serviceType == servicePlex && address == "" {
		address = plexPublicAddress
	}
	return &DemoMediaServerConfig{
		PublicAddress:     address,
		LibraryIDs:        ids,
		MachineIdentifier: machine,
		AutoApprove:       body.AutoApprove,
	}, ""
}

// instMgmtPlexLinkOK reports whether a Plex save carries a usable link: either
// an approved pin from this session, or a stored token on the instance being
// edited. Message is "" when it does.
func instMgmtPlexLinkOK(body *instMgmtBody, existing *DemoInstance) string {
	if body.PlexLinkPin != 0 {
		if !instMgmtPlexPinApproved(body.PlexLinkPin) {
			return "the Plex link is not approved yet"
		}
		return ""
	}
	if existing != nil && existing.MediaServerConfig != nil && existing.MediaServerConfig.MachineIdentifier != "" {
		return "" // editing an instance whose token is already stored
	}
	return "link a Plex account first"
}

// instMgmtValidateURL enforces the create/update URL rules and returns the
// trimmed URL or an error message ("" = ok).
func instMgmtValidateURL(raw string) (string, string) {
	pu, err := url.Parse(raw)
	if err != nil || !pu.IsAbs() || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
		return "", "url must be an absolute http or https URL"
	}
	if pu.User != nil || pu.RawQuery != "" || pu.Fragment != "" {
		return "", "url must not contain credentials, a query string, or a fragment"
	}
	return strings.TrimRight(raw, "/"), ""
}

// instMgmtMappingsDerive converts request mappings to the stored shape and
// derives the media_downloads flag (any cantinarr_path under the media root).
func instMgmtMappingsDerive(in []instMgmtMapping) ([]map[string]string, bool) {
	stored := make([]map[string]string, 0, len(in))
	downloads := false
	for _, m := range in {
		stored = append(stored, map[string]string{
			"arr_path":       m.ArrPath,
			"cantinarr_path": m.CantinarrPath,
		})
		if m.CantinarrPath == instMgmtMediaRoot || strings.HasPrefix(m.CantinarrPath, instMgmtMediaRoot+"/") {
			downloads = true
		}
	}
	return stored, downloads
}

// instMgmtClearDefaults clears is_default on every instance of the type
// except exceptID. Snapshot first, then per-id mutations (no accessor
// nesting).
func instMgmtClearDefaults(serviceType, exceptID string) {
	for _, i := range instMgmtAll() {
		if i.ServiceType == serviceType && i.ID != exceptID && i.IsDefault {
			instMgmtWith(i.ID, func(inst *DemoInstance) { inst.IsDefault = false })
		}
	}
}

// ─── Register ───────────────────────────────────────────

// registerInstances mounts instance CRUD (admin) and the arr-proxy dispatcher
// on the authenticated /api router.
func registerInstances(r chi.Router) {
	admin := r.With(requireAdmin)

	admin.Get("/instances", instMgmtHandleList)
	admin.Post("/instances", instMgmtHandleCreate)
	admin.Get("/instances/media-roots", instMgmtHandleMediaRoots)
	admin.Post("/instances/test", instMgmtHandleTest)
	// The libraries a media server reports, for the shared-libraries picker.
	// Same candidate body and credential fallback as /test, so an edit form
	// can list libraries without retyping the key.
	admin.Post("/instances/media-server/libraries", instMgmtHandleMediaServerLibraries)
	// Plex: the PIN link that yields an instance's token (held server-side,
	// referenced by pin id on save) and the linked account's owned servers.
	admin.Post("/instances/plex/link/begin", instMgmtHandlePlexLinkBegin)
	admin.Post("/instances/plex/link/check", instMgmtHandlePlexLinkCheck)
	admin.Post("/instances/plex/servers", instMgmtHandlePlexServers)
	admin.Put("/instances/{instanceID}", instMgmtHandleUpdate)
	admin.Delete("/instances/{instanceID}", instMgmtHandleDelete)
	admin.Get("/instances/{instanceID}/users", instMgmtHandleGetUsers)
	admin.Put("/instances/{instanceID}/users", instMgmtHandlePutUsers)
	// The grant view of the same screen: which users hold an access grant on
	// which instance of this service type, and (PUT) grant this instance to
	// an exact set of users WITHOUT moving anyone's default.
	admin.Get("/instances/{instanceID}/grant-users", instMgmtHandleGetGrantUsers)
	admin.Put("/instances/{instanceID}/grant-users", instMgmtHandlePutGrantUsers)
	admin.Post("/instances/{instanceID}/webhook", instMgmtHandleWebhook)
	admin.Get("/instances/{instanceID}/webhook", instMgmtHandleWebhookStatus)

	// The proxy dispatcher — ANY method, any deeper path. Authorization
	// (admin full access vs non-admin GET-only allowlist bound to the
	// effective instance) is enforced HERE so the fakes can assume it.
	r.HandleFunc("/instances/{instanceID}/*", instMgmtHandleProxy)
}

// ─── CRUD handlers ──────────────────────────────────────

func instMgmtHandleList(w http.ResponseWriter, _ *http.Request) {
	list := instMgmtAll()
	sort.Slice(list, func(a, b int) bool {
		ia, ib := list[a], list[b]
		if ia.ServiceType != ib.ServiceType {
			return ia.ServiceType < ib.ServiceType
		}
		sa, sb := instMgmtSortOrderOf(ia.ID), instMgmtSortOrderOf(ib.ID)
		if sa != sb {
			return sa < sb
		}
		if ia.Name != ib.Name {
			return ia.Name < ib.Name
		}
		return ia.ID < ib.ID
	})
	out := make([]map[string]any, 0, len(list))
	for _, i := range list {
		out = append(out, instMgmtJSON(i))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func instMgmtHandleMediaRoots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, []string{instMgmtMediaRoot})
}

func instMgmtHandleCreate(w http.ResponseWriter, r *http.Request) {
	var body instMgmtBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !instMgmtServiceTypes[body.ServiceType] {
		writeErr(w, http.StatusBadRequest, instMgmtEnumError)
		return
	}
	if body.Name == "" || body.URL == "" {
		writeErr(w, http.StatusBadRequest, "name and url are required")
		return
	}
	trimmedURL, msg := instMgmtValidateURL(body.URL)
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	switch body.ServiceType {
	case serviceQbittorrent, serviceNzbget:
		if body.Username == "" || body.Password == "" {
			writeErr(w, http.StatusBadRequest,
				fmt.Sprintf("username and password are required for %s", body.ServiceType))
			return
		}
	case serviceTransmission:
		// credentials optional
	case servicePlex:
		// Plex carries no api key: the PIN link is the credential.
		if msg := instMgmtPlexLinkOK(&body, nil); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
	default:
		if body.APIKey == "" {
			writeErr(w, http.StatusBadRequest, "name, url, and api_key are required")
			return
		}
	}
	mediaServerConfig, msMsg := instMgmtValidateMediaServerConfig(body.ServiceType, body.MediaServerConfig)
	if msMsg != "" {
		writeErr(w, http.StatusBadRequest, msMsg)
		return
	}
	if isMediaServerType(body.ServiceType) && mediaServerConfig == nil {
		// A media server with no config still needs the zero one, so the
		// stored row is never a half-configured document.
		mediaServerConfig, msMsg = instMgmtValidateMediaServerConfig(
			body.ServiceType, &instMgmtMediaServerBody{})
		if msMsg != "" {
			writeErr(w, http.StatusBadRequest, msMsg)
			return
		}
	}
	mappings := []map[string]string{}
	mediaDownloads := false
	if body.MediaPathMappings != nil && len(*body.MediaPathMappings) > 0 {
		if !instMgmtIsArrType(body.ServiceType) {
			writeErr(w, http.StatusBadRequest,
				"media path mappings are supported only for Radarr, Sonarr, and Chaptarr")
			return
		}
		mappings, mediaDownloads = instMgmtMappingsDerive(*body.MediaPathMappings)
	}

	isDefault := body.IsDefault
	if body.ServiceType == serviceChaptarr || isMediaServerType(body.ServiceType) {
		// Chaptarr and the media servers are never a global default: access
		// to them is the per-user grant, not a fallback everyone inherits.
		isDefault = false
	}
	inst := &DemoInstance{
		ID:                body.ServiceType + "-" + uuid.NewString()[:8],
		ServiceType:       body.ServiceType,
		Name:              body.Name,
		URL:               trimmedURL,
		Username:          body.Username,
		IsDefault:         isDefault,
		MediaDownloads:    mediaDownloads,
		MediaPathMappings: mappings,
		MediaServerConfig: mediaServerConfig,
	}
	if isDefault {
		instMgmtClearDefaults(inst.ServiceType, inst.ID)
	}
	registerInstance(inst)
	if body.SortOrder != nil {
		instMgmtMu.Lock()
		instMgmtSortOrders[inst.ID] = *body.SortOrder
		instMgmtMu.Unlock()
	}

	writeJSON(w, http.StatusCreated, instMgmtJSON(instMgmtResolve(inst.ID)))
}

func instMgmtHandleUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "instanceID")
	existing := instMgmtResolve(id)
	if existing == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return
	}
	var body instMgmtBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// service_type is immutable — the stored type wins.
	serviceType := existing.ServiceType
	if body.Name == "" || body.URL == "" {
		writeErr(w, http.StatusBadRequest, "name and url are required")
		return
	}
	trimmedURL, msg := instMgmtValidateURL(body.URL)
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	var newMappings []map[string]string
	newDownloads := false
	if body.MediaPathMappings != nil {
		if len(*body.MediaPathMappings) > 0 && !instMgmtIsArrType(serviceType) {
			writeErr(w, http.StatusBadRequest,
				"media path mappings are supported only for Radarr, Sonarr, and Chaptarr")
			return
		}
		newMappings, newDownloads = instMgmtMappingsDerive(*body.MediaPathMappings)
	}
	if serviceType == servicePlex {
		if msg := instMgmtPlexLinkOK(&body, existing); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
	}
	mediaServerConfig, msMsg := instMgmtValidateMediaServerConfig(serviceType, body.MediaServerConfig)
	if msMsg != "" {
		writeErr(w, http.StatusBadRequest, msMsg)
		return
	}
	isDefault := body.IsDefault
	if serviceType == serviceChaptarr || isMediaServerType(serviceType) {
		isDefault = false
	}
	if isDefault {
		instMgmtClearDefaults(serviceType, id)
	}
	instMgmtWith(id, func(inst *DemoInstance) {
		inst.Name = body.Name
		inst.URL = trimmedURL
		if body.Username != "" { // blank credentials keep the stored values
			inst.Username = body.Username
		}
		inst.IsDefault = isDefault
		if body.MediaPathMappings != nil { // omitted key = keep current mappings
			inst.MediaPathMappings = newMappings
			inst.MediaDownloads = newDownloads
		}
		if mediaServerConfig != nil { // omitted key = keep stored config
			inst.MediaServerConfig = mediaServerConfig
		}
	})
	if body.SortOrder != nil {
		instMgmtMu.Lock()
		instMgmtSortOrders[id] = *body.SortOrder
		instMgmtMu.Unlock()
	}
	writeJSON(w, http.StatusOK, instMgmtJSON(instMgmtResolve(id)))
}

func instMgmtHandleTest(w http.ResponseWriter, r *http.Request) {
	var body instMgmtBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ID != "" {
		if instMgmtResolve(body.ID) == nil {
			writeErr(w, http.StatusNotFound, "instance not found")
			return
		}
	} else if !instMgmtServiceTypes[body.ServiceType] {
		writeErr(w, http.StatusBadRequest, instMgmtEnumError)
		return
	}
	// The demo never dials anything: every test succeeds.
	w.WriteHeader(http.StatusNoContent)
}

func instMgmtHandleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "instanceID")
	inst := instMgmtResolve(id)
	if inst == nil {
		// Quirk parity: the real store error is not special-cased to 404.
		writeErr(w, http.StatusInternalServerError, fmt.Sprintf("instance not found: %s", id))
		return
	}
	// The seeded chaptarr instance always has a pending book request in the
	// demo (user 2's approvals-loop fixture), so its delete demonstrates the
	// pending-book-requests guard.
	if inst.ID == instChaptarr {
		writeErr(w, http.StatusConflict,
			"instance has pending book requests: cannot delete instance while 1 book request(s) await approval")
		return
	}
	removeInstance(id)
	// Deleting the stored default leaves the type defaultless; promote the
	// first survivor so admin views keep exactly one default per type.
	if inst.IsDefault && inst.ServiceType != serviceChaptarr {
		for _, s := range instMgmtAll() {
			if s.ServiceType == inst.ServiceType {
				instMgmtWith(s.ID, func(i *DemoInstance) { i.IsDefault = true })
				break
			}
		}
	}
	instMgmtMu.Lock()
	delete(instMgmtSortOrders, id)
	delete(instMgmtWebhookSet, id)
	instMgmtMu.Unlock()
	// Access rows pointing at the deleted instance go with it: the grants,
	// and any media-server accounts recorded against it.
	dropInstanceGrants(id)
	msvDropInstance(id)
	// Per-user pins pointing at the deleted instance are removed (chaptarr
	// grants revoked).
	for _, u := range allUsers() {
		withUser(u.ID, func(uu *DemoUser) {
			for st, pinned := range uu.DefaultInstances {
				if pinned == id {
					delete(uu.DefaultInstances, st)
				}
			}
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Plex link + media-server libraries ─────────────────

var (
	// instMgmtPlexApproved records pin ids this admin already approved, so a
	// save can reference one. A pin is consumed on save, like the real
	// server's short-lived link record.
	instMgmtPlexApproved = map[int64]bool{}
)

// instMgmtPlexPinApproved reports whether a pin was approved and is still
// usable for a save.
func instMgmtPlexPinApproved(pinID int64) bool {
	instMgmtMu.Lock()
	defer instMgmtMu.Unlock()
	return instMgmtPlexApproved[pinID]
}

// instMgmtHandlePlexLinkBegin — POST /api/instances/plex/link/begin. Mints the
// PIN the admin approves at plex.tv. The token it yields never travels to the
// app; the save references the pin id instead.
func instMgmtHandlePlexLinkBegin(w http.ResponseWriter, _ *http.Request) {
	pinID, code, link := plexBeginPin()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"pin_id": pinID, "code": code, "url": link})
}

// instMgmtHandlePlexLinkCheck — POST /api/instances/plex/link/check. linked
// false means the admin has not approved it yet; an unknown or consumed pin is
// the same thing plex.tv's own expiry looks like.
func instMgmtHandlePlexLinkCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PinID int64 `json:"pin_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PinID == 0 {
		writeErr(w, http.StatusBadRequest, "pin_id required")
		return
	}
	if instMgmtPlexPinApproved(req.PinID) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"linked": true, "account": plexDemoAccount})
		return
	}
	approved, found := plexPollPin(req.PinID)
	if !found {
		writeErr(w, http.StatusNotFound, "the Plex link has expired; link the account again")
		return
	}
	if !approved {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"linked": false})
		return
	}
	instMgmtMu.Lock()
	instMgmtPlexApproved[req.PinID] = true
	instMgmtMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"linked": true, "account": plexDemoAccount})
}

// instMgmtHandlePlexServers — POST /api/instances/plex/servers. The owned
// servers of the linked account, for the editor's server picker: by approved
// pin when creating, by stored instance id when editing.
func instMgmtHandlePlexServers(w http.ResponseWriter, r *http.Request) {
	var body instMgmtBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ID != "" {
		existing := instMgmtResolve(body.ID)
		if existing == nil {
			writeErr(w, http.StatusNotFound, "instance not found")
			return
		}
		if existing.ServiceType != servicePlex {
			writeErr(w, http.StatusBadRequest, "service_type must be plex")
			return
		}
	} else if body.ServiceType != servicePlex {
		writeErr(w, http.StatusBadRequest, "service_type must be plex")
		return
	} else if !instMgmtPlexPinApproved(body.PlexLinkPin) {
		writeErr(w, http.StatusBadGateway,
			"could not list the account's servers; relink the Plex account")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"servers": plexServerChoices()})
}

// instMgmtHandleMediaServerLibraries — POST /api/instances/media-server/libraries.
// Asks the server what libraries it has right now, so the picker offers real
// ones rather than ids typed from memory.
func instMgmtHandleMediaServerLibraries(w http.ResponseWriter, r *http.Request) {
	var body instMgmtBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	serviceType := body.ServiceType
	if body.ID != "" {
		existing := instMgmtResolve(body.ID)
		if existing == nil {
			writeErr(w, http.StatusNotFound, "instance not found")
			return
		}
		serviceType = existing.ServiceType
	}
	if !isMediaServerType(serviceType) {
		writeErr(w, http.StatusBadRequest, instMgmtMediaServerEnumError)
		return
	}
	if serviceType == servicePlex {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"server_name": plexDemoServer,
			"version":     "1.41.3.9314",
			"libraries":   plexDemoLibraries,
		})
		return
	}
	name, version := "Jellyfin", "10.10.7"
	prefix := "jf"
	if serviceType == serviceEmby {
		name, version, prefix = "Emby", "4.9.5.0", "emby"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"server_name": name + " Demo",
		"version":     version,
		"libraries": []map[string]any{
			{"id": prefix + "-lib-movies", "name": "Movies", "collection_type": "movies"},
			{"id": prefix + "-lib-shows", "name": "Shows", "collection_type": "tvshows"},
			{"id": prefix + "-lib-books", "name": "Books", "collection_type": "books"},
		},
	})
}

// ─── Instance-centric grant endpoints ───────────────────

// instMgmtHandleGetGrantUsers — GET /api/instances/{id}/grant-users. Every
// grant row for this instance's SERVICE TYPE (siblings included), so the
// editor can show who is on which library. Both keys are hard-required by the
// app — a null in either blanks the assignment section.
func instMgmtHandleGetGrantUsers(w http.ResponseWriter, r *http.Request) {
	inst := instMgmtResolve(chi.URLParam(r, "instanceID"))
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return
	}
	writeJSON(w, http.StatusOK, instMgmtGrantRowsJSON(inst.ServiceType))
}

func instMgmtGrantRowsJSON(serviceType string) []map[string]any {
	out := []map[string]any{}
	for _, row := range instanceGrantRows(serviceType) {
		out = append(out, map[string]any{
			"user_id":     row.UserID,
			"instance_id": row.InstanceID,
		})
	}
	return out
}

// instMgmtHandlePutGrantUsers — PUT /api/instances/{id}/grant-users. An exact
// set for THIS instance: listed users gain the grant, unlisted users lose it.
// Siblings are untouched, which is the whole difference from the pin endpoint
// next door — granting a library never moves anyone off another one.
func instMgmtHandlePutGrantUsers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "instanceID")
	inst := instMgmtResolve(id)
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return
	}
	if !uaGrantableServiceTypes[inst.ServiceType] {
		writeErr(w, http.StatusBadRequest,
			fmt.Sprintf("service_type %s does not support grants", inst.ServiceType))
		return
	}
	var req struct {
		UserIDs []int `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := setInstanceGrantUsers(id, req.UserIDs); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, instMgmtGrantRowsJSON(inst.ServiceType))
}

// ─── Per-user pin endpoints ─────────────────────────────

type instMgmtPin struct {
	UserID     int    `json:"user_id"`
	InstanceID string `json:"instance_id"`
}

// instMgmtPinsForType lists every per-user pin for a service type (users may
// be pinned to sibling instances), user_id ascending. Never nil.
func instMgmtPinsForType(serviceType string) []instMgmtPin {
	pins := []instMgmtPin{}
	for _, u := range allUsers() {
		var pinned string
		withUser(u.ID, func(uu *DemoUser) { pinned = uu.DefaultInstances[serviceType] })
		if pinned != "" {
			pins = append(pins, instMgmtPin{UserID: u.ID, InstanceID: pinned})
		}
	}
	sort.Slice(pins, func(a, b int) bool { return pins[a].UserID < pins[b].UserID })
	return pins
}

func instMgmtHandleGetUsers(w http.ResponseWriter, r *http.Request) {
	inst := instMgmtResolve(chi.URLParam(r, "instanceID"))
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return
	}
	writeJSON(w, http.StatusOK, instMgmtPinsForType(inst.ServiceType))
}

func instMgmtHandlePutUsers(w http.ResponseWriter, r *http.Request) {
	inst := instMgmtResolve(chi.URLParam(r, "instanceID"))
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return
	}
	var body struct {
		UserIDs []int `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	for _, uid := range body.UserIDs {
		if userByID(uid) == nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("unknown user id: %d", uid))
			return
		}
	}
	listed := map[int]bool{}
	for _, uid := range body.UserIDs {
		listed[uid] = true
	}
	// Exact-set semantics: listed users are pinned here (moved off siblings);
	// users previously pinned to THIS instance but absent revert to the
	// global default (chaptarr: access revoked).
	serviceType, instID := inst.ServiceType, inst.ID
	for _, u := range allUsers() {
		withUser(u.ID, func(uu *DemoUser) {
			if listed[uu.ID] {
				uu.DefaultInstances[serviceType] = instID
			} else if uu.DefaultInstances[serviceType] == instID {
				delete(uu.DefaultInstances, serviceType)
			}
		})
	}
	writeJSON(w, http.StatusOK, instMgmtPinsForType(serviceType))
}

// ─── Webhook stub ───────────────────────────────────────

func instMgmtHandleWebhook(w http.ResponseWriter, r *http.Request) {
	inst := instMgmtResolve(chi.URLParam(r, "instanceID"))
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return
	}
	if !instMgmtIsArrType(inst.ServiceType) {
		writeErr(w, http.StatusBadRequest, "webhooks are supported only for radarr, sonarr, and chaptarr")
		return
	}
	instMgmtMu.Lock()
	action := "created"
	if instMgmtWebhookSet[inst.ID] {
		action = "updated"
	}
	instMgmtWebhookSet[inst.ID] = true
	instMgmtMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "configured", "action": action})
}

// instMgmtHandleWebhookStatus answers the live instant-updates state. The real
// server derives this from the arr's Connect list on every call; the demo's
// arr instances were "created" by this server, which auto-installs the
// webhook, so they always read ok.
func instMgmtHandleWebhookStatus(w http.ResponseWriter, r *http.Request) {
	inst := instMgmtResolve(chi.URLParam(r, "instanceID"))
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return
	}
	if !instMgmtIsArrType(inst.ServiceType) {
		writeJSON(w, http.StatusOK, map[string]any{
			"supported": false, "configured": false, "state": "unsupported",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"supported": true, "configured": true, "state": "ok",
	})
}

// ─── Arr-proxy dispatcher ───────────────────────────────

// instMgmtHandleProxy authorizes and dispatches ANY /api/instances/{id}/*
// request to the per-service fake (handleRadarrProxy / handleSonarrProxy /
// handleChaptarrProxy — contract §7). The non-admin GET-only allowlist and
// effective-instance binding are enforced here so the fakes can assume
// authorization is done.
func instMgmtHandleProxy(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodConnect, http.MethodTrace, "TRACK":
		writeErr(w, http.StatusMethodNotAllowed, "proxy method is not supported")
		return
	}
	id := chi.URLParam(r, "instanceID")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "instance ID required")
		return
	}
	inst := instMgmtResolve(id)
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return
	}
	u := userFrom(r)
	isAdmin := u != nil && u.Role == roleAdmin
	rest := chi.URLParam(r, "*")

	if !isAdmin {
		// Regular users: GET only, arr services only, effective instance
		// only, exact read allowlist. Everything else is permission denied.
		if !instMgmtIsArrType(inst.ServiceType) || r.Method != http.MethodGet {
			writeErr(w, http.StatusForbidden, "permission denied")
			return
		}
		// Grants are additive, so a requester can legitimately read more than
		// one library of a type. Bind to what they can SEE, not to the single
		// effective default — otherwise the sibling a Library chip points at
		// answers 403 the moment they select it.
		if !userCanSeeInstance(u, inst.ID) {
			writeErr(w, http.StatusForbidden, "permission denied")
			return
		}
		if !instMgmtProxyAllowed(inst.ServiceType, rest) {
			writeErr(w, http.StatusForbidden, "permission denied")
			return
		}
	}

	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Pragma", "no-cache")

	switch inst.ServiceType {
	case serviceRadarr:
		handleRadarrProxy(w, r, inst, isAdmin, rest)
	case serviceSonarr:
		handleSonarrProxy(w, r, inst, isAdmin, rest)
	case serviceChaptarr:
		handleChaptarrProxy(w, r, inst, isAdmin, rest)
	default:
		// Download clients, tautulli, and the media servers have no fake
		// upstream behind the raw proxy: the app talks to /api/downloads,
		// /api/tautulli, and /api/media-servers instead.
		writeErr(w, http.StatusBadGateway, "could not reach server: connection refused")
	}
}

// instMgmtProxyAllowed implements the non-admin read allowlist
// (srv-instances §2 + contract §7). rest is the path after
// /api/instances/{id}/ (e.g. "api/v3/movie").
func instMgmtProxyAllowed(serviceType, rest string) bool {
	if strings.Contains(rest, "%") || strings.Contains(rest, "\\") {
		return false
	}
	segs := strings.Split(rest, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return false
		}
	}
	version := "v3"
	if serviceType == serviceChaptarr {
		version = "v1"
	}
	if len(segs) < 3 || segs[0] != "api" || segs[1] != version {
		return false
	}
	tail := segs[2:]
	isID := func(s string) bool {
		n, err := strconv.Atoi(s)
		return err == nil && n > 0
	}
	switch serviceType {
	case serviceRadarr:
		switch len(tail) {
		case 1:
			switch tail[0] {
			case "movie", "calendar", "queue", "history":
				return true
			}
		case 2:
			if tail[0] == "movie" && isID(tail[1]) {
				return true
			}
			if tail[0] == "wanted" && (tail[1] == "missing" || tail[1] == "cutoff") {
				return true
			}
		}
	case serviceSonarr:
		switch len(tail) {
		case 1:
			switch tail[0] {
			case "series", "episode", "calendar", "queue", "history":
				return true
			}
		case 2:
			if (tail[0] == "series" || tail[0] == "episode") && isID(tail[1]) {
				return true
			}
			if tail[0] == "wanted" && (tail[1] == "missing" || tail[1] == "cutoff") {
				return true
			}
		}
	case serviceChaptarr:
		if tail[0] == "MediaCover" {
			// MediaCover/Books/{id}/<file...> — book covers.
			if len(tail) >= 4 && tail[1] == "Books" && isID(tail[2]) {
				return true
			}
			// MediaCover/{id}/<file...> — author covers. Chaptarr has no
			// Authors/ subtree, so an author's images hang off the bare id.
			return len(tail) >= 3 && isID(tail[1])
		}
		switch len(tail) {
		case 1:
			switch tail[0] {
			case "author", "book", "bookfile", "calendar", "queue", "history":
				return true
			}
		case 2:
			if (tail[0] == "author" || tail[0] == "book" || tail[0] == "bookfile") && isID(tail[1]) {
				return true
			}
			if (tail[0] == "book" || tail[0] == "author") && tail[1] == "lookup" {
				return true
			}
			if tail[0] == "wanted" && (tail[1] == "missing" || tail[1] == "cutoff") {
				return true
			}
		}
	}
	return false
}
