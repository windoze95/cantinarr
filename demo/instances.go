// instances.go — instance management (CRUD, per-user pins, webhook stub) and
// the arr-proxy dispatcher /api/instances/{instanceID}/* (srv-instances §1–2,
// app-admin §4, gap-plan §1.9).
//
// Created/deleted instances live in a domain-local overlay (instMgmtCreated /
// instMgmtDeletedIDs) because the frozen core store has no add/remove
// accessors; the seeded instances stay authoritative for every sibling
// domain. All rendering goes through copies so no shared pointer escapes a
// lock.
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
}

const instMgmtEnumError = "service_type must be one of 'radarr', 'sonarr', 'chaptarr', 'sabnzbd', 'qbittorrent', 'nzbget', 'transmission', 'tautulli'"

// ─── Domain-local overlay state ─────────────────────────

var (
	instMgmtMu         sync.Mutex
	instMgmtCreated    []*DemoInstance     // instances created through POST /api/instances
	instMgmtDeletedIDs = map[string]bool{} // seeded instances the admin deleted
	instMgmtSortOrders = map[string]int{}  // instance id -> sort_order (absent = 0)
	instMgmtWebhookSet = map[string]bool{} // instance id -> webhook already configured
)

// instMgmtResolve returns a defensive copy of the instance with the given id
// (seeded or overlay-created), or nil when unknown/deleted.
func instMgmtResolve(id string) *DemoInstance {
	instMgmtMu.Lock()
	deleted := instMgmtDeletedIDs[id]
	instMgmtMu.Unlock()
	if deleted {
		return nil
	}
	var cp *DemoInstance
	if withInstance(id, func(i *DemoInstance) {
		c := *i
		c.MediaPathMappings = append([]map[string]string{}, i.MediaPathMappings...)
		cp = &c
	}) {
		return cp
	}
	instMgmtMu.Lock()
	defer instMgmtMu.Unlock()
	for _, i := range instMgmtCreated {
		if i.ID == id {
			c := *i
			c.MediaPathMappings = append([]map[string]string{}, i.MediaPathMappings...)
			return &c
		}
	}
	return nil
}

// instMgmtWith mutates an instance (seeded or overlay) under the appropriate
// lock; reports whether it exists. fn must only set plain fields.
func instMgmtWith(id string, fn func(*DemoInstance)) bool {
	instMgmtMu.Lock()
	deleted := instMgmtDeletedIDs[id]
	instMgmtMu.Unlock()
	if deleted {
		return false
	}
	if withInstance(id, fn) {
		return true
	}
	instMgmtMu.Lock()
	defer instMgmtMu.Unlock()
	for _, i := range instMgmtCreated {
		if i.ID == id {
			fn(i)
			return true
		}
	}
	return false
}

// instMgmtAll returns copies of every live instance (seeded minus deleted,
// plus overlay-created), unsorted.
func instMgmtAll() []*DemoInstance {
	out := []*DemoInstance{}
	for _, i := range allInstances() {
		if cp := instMgmtResolve(i.ID); cp != nil {
			out = append(out, cp)
		}
	}
	instMgmtMu.Lock()
	created := append([]*DemoInstance{}, instMgmtCreated...)
	instMgmtMu.Unlock()
	for _, i := range created {
		c := *i
		c.MediaPathMappings = append([]map[string]string{}, i.MediaPathMappings...)
		out = append(out, &c)
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
	return map[string]any{
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
}

func instMgmtIsArrType(st string) bool {
	return st == serviceRadarr || st == serviceSonarr || st == serviceChaptarr
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
	admin.Put("/instances/{instanceID}", instMgmtHandleUpdate)
	admin.Delete("/instances/{instanceID}", instMgmtHandleDelete)
	admin.Get("/instances/{instanceID}/users", instMgmtHandleGetUsers)
	admin.Put("/instances/{instanceID}/users", instMgmtHandlePutUsers)
	admin.Post("/instances/{instanceID}/webhook", instMgmtHandleWebhook)

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
	default:
		if body.APIKey == "" {
			writeErr(w, http.StatusBadRequest, "name, url, and api_key are required")
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
	if body.ServiceType == serviceChaptarr {
		isDefault = false // chaptarr is never default
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
	}
	if isDefault {
		instMgmtClearDefaults(inst.ServiceType, inst.ID)
	}
	instMgmtMu.Lock()
	instMgmtCreated = append(instMgmtCreated, inst)
	if body.SortOrder != nil {
		instMgmtSortOrders[inst.ID] = *body.SortOrder
	}
	instMgmtMu.Unlock()

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
	isDefault := body.IsDefault
	if serviceType == serviceChaptarr {
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
	instMgmtMu.Lock()
	removedFromOverlay := false
	for idx, c := range instMgmtCreated {
		if c.ID == id {
			instMgmtCreated = append(instMgmtCreated[:idx], instMgmtCreated[idx+1:]...)
			removedFromOverlay = true
			break
		}
	}
	if !removedFromOverlay {
		instMgmtDeletedIDs[id] = true
	}
	delete(instMgmtSortOrders, id)
	delete(instMgmtWebhookSet, id)
	instMgmtMu.Unlock()
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
		eff := effectiveInstanceFor(u, inst.ServiceType)
		if eff == nil || eff.ID != inst.ID {
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
		// Download clients / tautulli have no fake upstream behind the raw
		// proxy (the app talks to /api/downloads and /api/tautulli instead).
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
			if tail[0] == "queue" && tail[1] == "details" {
				return true
			}
		}
	case serviceChaptarr:
		if tail[0] == "MediaCover" {
			// MediaCover/Books/{id}/<file...> — owned-book covers only.
			return len(tail) >= 4 && tail[1] == "Books" && isID(tail[2])
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
			if tail[0] == "book" && tail[1] == "lookup" {
				return true
			}
			if tail[0] == "wanted" && (tail[1] == "missing" || tail[1] == "cutoff") {
				return true
			}
		}
	}
	return false
}
