// ai_admin.go — admin-side AI surfaces: the shared credentials profile,
// the shared Codex device flow, AI tool toggles + timed debug logging, and
// the external-settings-changes configuration history (+revert).
// Shared AI state lives in ai.go (aiMu); configuration-history storage and
// seeds live in data_ai.go (aiChMu).
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ─── Routes ─────────────────────────────────────────────

// registerAIAdmin mounts the admin AI surfaces on the authenticated /api
// router. The credentials / ai-tools / settings-changes groups use
// requireAdmin; the codex handlers do their own role check because the real
// handlers answer 403 {"error":"forbidden"} there (not "permission denied").
func registerAIAdmin(r chi.Router) {
	r.With(requireAdmin).Get("/admin/credentials", aiAdminGetCredentials)
	r.With(requireAdmin).Put("/admin/credentials", aiAdminPutCredentials)
	r.With(requireAdmin).Delete("/admin/credentials/{key}", aiAdminDeleteCredential)

	r.Get("/admin/ai/codex/status", aiAdminCodexStatus)
	r.Post("/admin/ai/codex/device/begin", aiAdminCodexBegin)
	r.Get("/admin/ai/codex/device/{flowID}", aiAdminCodexPoll)
	r.Delete("/admin/ai/codex/device/{flowID}", aiAdminCodexCancel)
	r.Delete("/admin/ai/codex", aiAdminCodexUnlink)

	r.With(requireAdmin).Get("/admin/ai-tools", aiAdminGetTools)
	r.With(requireAdmin).Put("/admin/ai-tools/debug", aiAdminPutToolsDebug)
	r.With(requireAdmin).Put("/admin/ai-tools/{name}", aiAdminPutTool)

	r.With(requireAdmin).Get("/admin/external-settings-changes", aiAdminListChanges)
	r.With(requireAdmin).Get("/admin/external-settings-changes/{id}", aiAdminGetChange)
	r.With(requireAdmin).Post("/admin/external-settings-changes/{id}/revert", aiAdminRevertChange)
}

// aiAdminOnly is the codex handlers' inline role check (403 "forbidden",
// matching the real codex handlers' internal check).
func aiAdminOnly(w http.ResponseWriter, r *http.Request) bool {
	u := userFrom(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if u.Role != roleAdmin {
		writeErr(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

// ─── /api/admin/credentials ─────────────────────────────

// aiSecretCredentialKeys are the five deletable secret keys; presence
// booleans (never values) are the only thing GET returns.
var aiSecretCredentialKeys = []string{
	"tmdb_access_token", "anthropic_key", "openai_key", "gemini_key", "trakt_client_id",
}

func aiIsSecretCredentialKey(key string) bool {
	for _, k := range aiSecretCredentialKeys {
		if k == key {
			return true
		}
	}
	return false
}

func aiAdminGetCredentials(w http.ResponseWriter, r *http.Request) {
	aiMu.Lock()
	presence := map[string]bool{}
	for _, k := range aiSecretCredentialKeys {
		presence[k] = aiSharedCreds[k]
	}
	var lastChecked any // null when never checked
	if !aiHealthLastChecked.IsZero() {
		lastChecked = aiHealthLastChecked.UTC().Format(time.RFC3339)
	}
	sharedConfig := map[string]any{"provider": aiSharedProvider, "model": aiSharedModel}
	out := map[string]any{
		// Legacy + namespaced duplication — the five presence booleans appear
		// both at top level and under "credentials"; keep both.
		"credentials": presence,
		// The demo presents admin-stored TMDB/Trakt credentials, so neither
		// service runs on the server's built-in public fallbacks.
		"tmdb_using_builtin":  false,
		"trakt_using_builtin": false,
		"ai": map[string]any{
			"config":    sharedConfig,
			"providers": aiProviderCatalog(),
			"health_check": map[string]any{
				"enabled":         aiHealthEnabled,
				"interval_hours":  24,
				"last_checked_at": lastChecked,
			},
			"shared": map[string]any{
				"config":     map[string]any{"provider": aiSharedProvider, "model": aiSharedModel},
				"configured": aiLockedSharedUsable(),
			},
		},
	}
	for k, v := range presence {
		out[k] = v
	}
	aiMu.Unlock()
	aiNoStore(w)
	writeJSON(w, http.StatusOK, out)
}

func aiAdminPutCredentials(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	allowed := map[string]bool{
		"tmdb_access_token": true, "anthropic_key": true, "openai_key": true,
		"gemini_key": true, "trakt_client_id": true,
		"ai_provider": true, "ai_model": true, "ai_health_check_enabled": true,
	}
	for key := range body {
		if !allowed[key] {
			writeErr(w, http.StatusBadRequest, "unknown credential key: "+key)
			return
		}
	}
	if v := body["ai_provider"]; v != "" && !aiProviderKnown(v) {
		writeErr(w, http.StatusBadRequest, "unknown AI provider")
		return
	}
	if len(body["ai_model"]) > 256 {
		writeErr(w, http.StatusBadRequest, "AI model is too long")
		return
	}
	for _, k := range []string{"anthropic_key", "openai_key", "gemini_key"} {
		if len(body[k]) > 32<<10 {
			writeErr(w, http.StatusBadRequest, "AI credential is too long")
			return
		}
	}
	healthSupplied := false
	healthEnabled := false
	if v, ok := body["ai_health_check_enabled"]; ok && v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "ai_health_check_enabled must be true or false")
			return
		}
		healthSupplied = true
		healthEnabled = parsed
	}

	// Simulated validation turn: every supplied AI key plus the selected
	// candidate is "probed" before anything commits (atomic; the demo always
	// passes).
	aiRelated := body["anthropic_key"] != "" || body["openai_key"] != "" ||
		body["gemini_key"] != "" || body["ai_provider"] != "" ||
		body["ai_model"] != "" || healthSupplied
	if aiRelated {
		time.Sleep(aiValidationDelay)
	}

	aiMu.Lock()
	// Only non-empty values are written (partial update; deletion goes
	// through DELETE).
	for _, k := range aiSecretCredentialKeys {
		if body[k] != "" {
			aiSharedCreds[k] = true
		}
	}
	if p := body["ai_provider"]; p != "" {
		if p != aiSharedProvider && body["ai_model"] == "" {
			aiSharedModel = aiDefaultModel(p)
		}
		aiSharedProvider = p
	}
	if m := body["ai_model"]; m != "" {
		aiSharedModel = m
	}
	if healthSupplied {
		aiHealthEnabled = healthEnabled
	}
	if aiRelated {
		aiHealthLastChecked = time.Now()
	}
	aiMu.Unlock()

	aiNoStore(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func aiAdminDeleteCredential(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if !aiIsSecretCredentialKey(key) {
		writeErr(w, http.StatusBadRequest, "unknown credential key")
		return
	}
	aiMu.Lock()
	aiSharedCreds[key] = false
	aiMu.Unlock()
	aiNoStore(w)
	w.WriteHeader(http.StatusNoContent)
}

// ─── Shared Codex device flow ───────────────────────────

func aiAdminCodexStatus(w http.ResponseWriter, r *http.Request) {
	if !aiAdminOnly(w, r) {
		return
	}
	aiMu.Lock()
	out := map[string]any{
		"available": true,
		"selected":  aiSharedProvider == "codex",
		"connected": aiSharedCodexLinked,
	}
	if aiSharedCodexLinked {
		out["account_email"] = aiSharedCodexEmail
		out["plan_type"] = aiSharedCodexPlan
		out["stale"] = false
		if !aiSharedCodexLinkedAt.IsZero() {
			out["updated_at"] = aiSharedCodexLinkedAt.UTC().Format(time.RFC3339)
		}
		out["rate_limits"] = aiCodexRateLimitsJSON()
	}
	aiMu.Unlock()
	aiNoStore(w)
	writeJSON(w, http.StatusOK, out)
}

func aiAdminCodexBegin(w http.ResponseWriter, r *http.Request) {
	if !aiAdminOnly(w, r) {
		return
	}
	u := userFrom(r)
	aiMu.Lock()
	if aiSharedCodexLinked {
		aiMu.Unlock()
		aiNoStore(w)
		writeErr(w, http.StatusConflict, "Disconnect the shared OpenAI OAuth account before linking another one")
		return
	}
	flowID := randomHex(16)
	aiCodexFlows[flowID] = &aiCodexFlow{UserID: u.ID, Shared: true, Created: time.Now()}
	aiMu.Unlock()
	aiNoStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"flow_id":          flowID,
		"verification_uri": aiCodexVerificationURI,
		"user_code":        aiNewUserCode(),
		"expires_in":       900,
		"interval":         2,
	})
}

func aiAdminCodexPoll(w http.ResponseWriter, r *http.Request) {
	if !aiAdminOnly(w, r) {
		return
	}
	u := userFrom(r)
	flowID := chi.URLParam(r, "flowID")
	aiMu.Lock()
	flow := aiCodexFlows[flowID]
	// Only the initiating admin may poll a shared flow.
	if flow == nil || !flow.Shared || flow.UserID != u.ID {
		aiMu.Unlock()
		aiNoStore(w)
		writeErr(w, http.StatusNotFound, "ChatGPT sign-in flow not found")
		return
	}
	if time.Since(flow.Created) > aiCodexFlowTTL {
		delete(aiCodexFlows, flowID)
		aiMu.Unlock()
		aiNoStore(w)
		writeJSON(w, http.StatusOK, map[string]any{"status": "expired"})
		return
	}
	flow.Polls++
	if flow.Polls < 2 {
		aiMu.Unlock()
		aiNoStore(w)
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
		return
	}
	// Connected: link the shared account and purge ALL conversations.
	delete(aiCodexFlows, flowID)
	aiSharedCodexLinked = true
	aiSharedCodexEmail = "admin@example.com"
	aiSharedCodexPlan = "pro"
	aiSharedCodexLinkedAt = time.Now()
	aiConvs = map[int]map[string]bool{}
	email, plan := aiSharedCodexEmail, aiSharedCodexPlan
	aiMu.Unlock()
	aiNoStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "connected",
		"account": map[string]any{"email": email, "plan_type": plan},
	})
}

func aiAdminCodexCancel(w http.ResponseWriter, r *http.Request) {
	if !aiAdminOnly(w, r) {
		return
	}
	flowID := chi.URLParam(r, "flowID")
	aiMu.Lock()
	if flow := aiCodexFlows[flowID]; flow != nil && flow.Shared {
		delete(aiCodexFlows, flowID)
	}
	aiMu.Unlock()
	aiNoStore(w)
	w.WriteHeader(http.StatusNoContent)
}

func aiAdminCodexUnlink(w http.ResponseWriter, r *http.Request) {
	if !aiAdminOnly(w, r) {
		return
	}
	aiMu.Lock()
	aiSharedCodexLinked = false
	aiSharedCodexEmail = ""
	aiSharedCodexPlan = ""
	aiSharedCodexLinkedAt = time.Time{}
	aiConvs = map[int]map[string]bool{} // shared unlink purges all conversations
	aiMu.Unlock()
	aiNoStore(w)
	w.WriteHeader(http.StatusNoContent)
}

// ─── AI tool registry (ported from server/internal/mcp) ─

// aiToolDef mirrors one registry tool: name + description verbatim from the
// real tool definitions, the RBAC permission string, and the effective
// admin_only flag (tool flagged AdminOnly OR the user role lacks its
// permission — the user role holds media:discover, media:request,
// media:download, ai:chat, mcp:access, arr:browse).
type aiToolDef struct {
	Name        string
	Permission  string
	AdminOnly   bool
	Description string
}

// aiToolDefs is the real 34-tool registry in registration order.
var aiToolDefs = []aiToolDef{
	{Name: "search_movies", Permission: "media:discover", AdminOnly: false,
		Description: `Search TMDB for movies by title or keyword`},
	{Name: "search_movie_collections", Permission: "media:discover", AdminOnly: false,
		Description: `Search TMDB movie collections/franchises by title or keyword. Use this before answering movie franchise, series, saga, collection, count, or title-list questions such as "how many Minions movies are there?" so recent and upcoming installments are not missed.`},
	{Name: "search_tv_shows", Permission: "media:discover", AdminOnly: false,
		Description: `Search TMDB for TV shows by title or keyword`},
	{Name: "get_trending", Permission: "media:discover", AdminOnly: false,
		Description: `Get trending movies and/or TV shows. Use media_type "all" for general trending, unspecified category requests, or when the user asks for both movies and shows/TV; it returns a balanced mixed list.`},
	{Name: "get_movie_details", Permission: "media:discover", AdminOnly: false,
		Description: `Get detailed information about a specific movie`},
	{Name: "get_tv_details", Permission: "media:discover", AdminOnly: false,
		Description: `Get detailed information about a specific TV show`},
	{Name: "get_recommendations", Permission: "media:discover", AdminOnly: false,
		Description: `Get recommendations based on a movie or TV show`},
	{Name: "search_books", Permission: "media:discover", AdminOnly: false,
		Description: `Search for books by title or author on the user's book server. Each result carries the foreign_book_id that check_request_status, request_media, and display_media need for books (books have no TMDB id).`},
	{Name: "check_request_status", Permission: "media:request", AdminOnly: false,
		Description: `Check if a movie, TV show, or book is available, requested, or downloading on the media server. Movies/TV are keyed by tmdb_id; books by the foreign_book_id from search_books (per-format ebook/audiobook state is included).`},
	{Name: "get_request_options", Permission: "media:request", AdminOnly: false,
		Description: `Show whether the current user may choose request options and list the quality profiles available for a movie, TV, or book request`},
	{Name: "request_media", Permission: "media:request", AdminOnly: false,
		Description: `Request a movie, TV show, or book, optionally selecting a quality_profile_id returned by get_request_options when the current user may choose quality. Movies/TV are keyed by tmdb_id; books by the foreign_book_id from search_books plus an optional book_format.`},
	{Name: "list_my_requests", Permission: "media:request", AdminOnly: false,
		Description: `List the current user's media request history`},
	{Name: "display_media", Permission: "media:discover", AdminOnly: false,
		Description: `Display specific movies, TV shows, or books in the UI carousel. Call this whenever your answer names concrete titles to showcase, including recommendations, search/trending picks, franchise/title-list answers, or count answers that enumerate titles. Keep the item order identical to the order you mention in text. Prefer TMDB IDs (movies/TV) or foreign_book_ids (books) copied from prior tool results; if you only have exact title/year values for a movie/show, omit tmdb_id and the server will resolve and verify them.`},
	{Name: "get_queue", Permission: "arr:read", AdminOnly: true,
		Description: `Get the current download queue from Radarr/Sonarr/Chaptarr with progress, time left, protocol, and any errors per item. Admin only`},
	{Name: "get_calendar", Permission: "arr:read", AdminOnly: true,
		Description: `Get upcoming movie releases and TV episode air dates, grouped by date. Books have no calendar in Chaptarr, so media_type=book is not supported. Admin only`},
	{Name: "get_library", Permission: "arr:read", AdminOnly: true,
		Description: `Browse the Radarr/Sonarr/Chaptarr library. Filter for missing (monitored but not downloaded) or unmonitored items, optionally narrowed by a title query. For books, pass author_id to list one author's books with their book ids (the ids search_releases/trigger_search need), or book_id for one exact book. Admin only`},
	{Name: "get_history", Permission: "arr:read", AdminOnly: true,
		Description: `Get recent download activity (grabs, imports, failures) from Radarr/Sonarr/Chaptarr. Admin only`},
	{Name: "trigger_search", Permission: "arr:search", AdminOnly: true,
		Description: `Trigger an automatic indexer search for a movie, series, or book that is already in the library. For movies/TV pass tmdb_id (and, for TV, season_number to search a single season). For books pass book_id to search one book or author_id to search all of an author's monitored books (books have no tmdb_id). Admin only`},
	{Name: "search_releases", Permission: "arr:search", AdminOnly: true,
		Description: `Interactively search indexers for downloadable releases of a library item and list them with a one-way release reference and indexer_id. Raw release GUID capabilities are never exposed. For movies/TV pass tmdb_id (TV also requires season_number and may include episode_number). For books pass book_id (books have no tmdb_id). Admin only`},
	{Name: "grab_release", Permission: "downloads:manage", AdminOnly: true,
		Description: `Freshly re-search the exact movie, TV season/episode, or book scope and send the release matching a one-way reference from search_releases to the download client. Admin only`},
	{Name: "remove_queue_item", Permission: "downloads:manage", AdminOnly: true,
		Description: `Remove an item from the download queue (also removes the download from the client). Optionally blocklist the release so it is not grabbed again. Admin only`},
	{Name: "get_disk_space", Permission: "system:read", AdminOnly: true,
		Description: `Get free and total disk space for the Radarr, Sonarr, and Chaptarr volumes. Admin only`},
	{Name: "get_arr_health", Permission: "arr:read", AdminOnly: true,
		Description: `Check Radarr/Sonarr/Chaptarr system health for config-level problems (download client unreachable, remote path mapping, indexers down, disk, no root folder). Use this when diagnose_queue shows path/permission/client errors to confirm the root cause that per-item queue diagnosis can only guess at. Admin only`},
	{Name: "diagnose_queue", Permission: "arr:read", AdminOnly: true,
		Description: `Import Doctor: scan the Radarr/Sonarr/Chaptarr download queue for items that are stuck, failed, or blocked from importing, and explain each problem in plain language with the queue_id and suggested fix actions (process, manual_import, force_import, remove, blocklist_search, change_category, rescan). For each problem it also prints the exact next MCP tool call to run. Use this before the fix tools. Admin only`},
	{Name: "get_manual_import_candidates", Permission: "downloads:manage", AdminOnly: true,
		Description: `List the files Radarr/Sonarr/Chaptarr found for a stuck download (from its queue_id), including each file's mapped movie/series/episodes/book and any rejection reasons that blocked an automatic import. Use this to understand why an item won't import before calling execute_manual_import. Admin only`},
	{Name: "execute_manual_import", Permission: "downloads:manage", AdminOnly: true,
		Description: `Force the files of a stuck download (from its queue_id) into the library via a manual import. By default skips candidates with permanent rejections; set force=true to import them anyway. Choose this when an item is blocked but the file is actually correct. Admin only`},
	{Name: "remediate_queue_item", Permission: "downloads:manage", AdminOnly: true,
		Description: `Apply a one-click fix to a stuck queue item: remove (delete it and the download), blocklist_search (remove, blocklist the release, and start a fresh search for a different one), or change_category (hand the download to the client's post-import category for tools like Unpackerr). Admin only`},
	{Name: "rescan_media", Permission: "arr:search", AdminOnly: true,
		Description: `Rescan the files on disk for a library movie, series, or author, then run the import pass. Use this after fixing a disk-space, path, or permissions problem so the service picks up files that are already there. For movies/TV pass tmdb_id; for books pass author_id (books have no tmdb_id). Admin only`},
	{Name: "list_arr_instances", Permission: "instances:manage", AdminOnly: true,
		Description: `List the configured Radarr/Sonarr/Chaptarr instances with the instance_id values other settings tools accept, and which instance is each service's default. Admin only`},
	{Name: "get_quality_profiles", Permission: "instances:manage", AdminOnly: true,
		Description: `Read the quality profiles of a Radarr/Sonarr/Chaptarr instance. Without profile_id: a summary of every profile (allowed qualities, cutoff, upgrade policy, custom-format scores, language); include_languages adds the complete bounded live Radarr/Sonarr language catalog, while language_name looks up one exact live name/ID. With profile_id: that one profile's full JSON exactly as the service stores it. Admin only`},
	{Name: "get_custom_formats", Permission: "instances:manage", AdminOnly: true,
		Description: `Read the custom formats of a Radarr/Sonarr/Chaptarr instance. Without format_id: a summary of every custom format and its specifications. With format_id: that one format's full JSON exactly as the service stores it. The scores that make formats matter live in each quality profile (see get_quality_profiles). Admin only`},
	{Name: "upsert_custom_format", Permission: "instances:manage", AdminOnly: true,
		Description: `Create or update one Radarr/Sonarr/Chaptarr custom format by exact name from native or TRaSH-style JSON. Caller-supplied ids are ignored. A create enters every existing quality profile at score 0; an update preserves the profile's numeric score but does not recompute stored file matches. A successful write is read back and recorded in Configuration history for live comparison. This tool does not set profile scores. Admin only`},
	{Name: "preview_profile_change", Permission: "instances:manage", AdminOnly: true,
		Description: `Prepare and show a narrow full-object quality-profile update for one Radarr/Sonarr/Chaptarr instance. Returns a one-use reference that apply_profile_change may consume only in this same authenticated chat turn; it never writes. Use only after an explicit admin request. In-app chat only, admin only`},
	{Name: "apply_profile_change", Permission: "instances:manage", AdminOnly: true,
		Description: `Apply one same-turn previewed quality-profile update after an explicit admin request. Reauthorizes, refuses stale settings, verifies the complete result, and records a durable before/after history entry for safe review and revert. In-app chat only, admin only`},
}

var (
	aiToolsMu       sync.Mutex
	aiDisabledTools = map[string]bool{} // enabled defaults to true for every tool
	aiDebugUntil    time.Time
)

// aiToolStatusJSON renders one flat ToolStatus object.
func aiToolStatusJSON(def aiToolDef, enabled bool) map[string]any {
	return map[string]any{
		"name":        def.Name,
		"description": def.Description,
		"enabled":     enabled,
		"admin_only":  def.AdminOnly,
		"permission":  def.Permission,
	}
}

// aiDebugStatusJSON renders the AIDebugStatus object (enabled_until omitted
// when disabled; remaining_seconds always present as an integer).
func aiDebugStatusJSON() map[string]any {
	aiToolsMu.Lock()
	defer aiToolsMu.Unlock()
	return aiLockedDebugStatusJSON()
}

func aiLockedDebugStatusJSON() map[string]any {
	now := time.Now()
	if aiDebugUntil.After(now) {
		return map[string]any{
			"enabled":           true,
			"enabled_until":     aiDebugUntil.UTC().Format(time.RFC3339),
			"remaining_seconds": int64(time.Until(aiDebugUntil).Seconds()),
		}
	}
	return map[string]any{
		"enabled":           false,
		"remaining_seconds": int64(0),
	}
}

func aiAdminGetTools(w http.ResponseWriter, r *http.Request) {
	aiToolsMu.Lock()
	tools := make([]map[string]any, 0, len(aiToolDefs))
	for _, def := range aiToolDefs {
		tools = append(tools, aiToolStatusJSON(def, !aiDisabledTools[def.Name]))
	}
	debug := aiLockedDebugStatusJSON()
	aiToolsMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools, "debug": debug})
}

func aiAdminPutTool(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Enabled == nil {
		writeErr(w, http.StatusBadRequest, `body must be {"enabled": bool}`)
		return
	}
	for _, def := range aiToolDefs {
		if def.Name != name {
			continue
		}
		aiToolsMu.Lock()
		if *body.Enabled {
			delete(aiDisabledTools, name)
		} else {
			aiDisabledTools[name] = true
		}
		enabled := !aiDisabledTools[name]
		aiToolsMu.Unlock()
		// Response is the single updated ToolStatus, flat (not wrapped).
		writeJSON(w, http.StatusOK, aiToolStatusJSON(def, enabled))
		return
	}
	writeErr(w, http.StatusNotFound, "unknown tool")
}

func aiAdminPutToolsDebug(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
		Hours   int   `json:"hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Enabled == nil {
		writeErr(w, http.StatusBadRequest, `body must be {"enabled": bool, "hours": int}`)
		return
	}
	aiToolsMu.Lock()
	if *body.Enabled {
		hours := body.Hours
		if hours < 1 {
			hours = 1
		}
		if hours > 24 {
			hours = 24
		}
		base := time.Now()
		if aiDebugUntil.After(base) {
			base = aiDebugUntil // enabling extends from the current expiry
		}
		aiDebugUntil = base.Add(time.Duration(hours) * time.Hour)
	} else {
		aiDebugUntil = time.Time{}
	}
	status := aiLockedDebugStatusJSON()
	aiToolsMu.Unlock()
	// Response is the debug status object directly (NOT wrapped).
	writeJSON(w, http.StatusOK, status)
}

// ─── External settings changes (configuration history) ──

// aiChangeJSON renders one ExternalSettingChange. List rows always carry
// "changes": [] and "can_revert": false; only detail (and revert) responses
// populate current / current_state / current_status / can_revert.
func aiChangeJSON(rec *aiChangeRec, detail bool) map[string]any {
	out := map[string]any{
		"id":            rec.ID,
		"actor_user_id": rec.ActorUserID,
		"actor_name":    rec.ActorName,
		"source":        rec.Source,
		"service_type":  rec.ServiceType,
		"instance_id":   rec.InstanceID,
		"instance_name": rec.InstanceName,
		"resource_type": rec.ResourceType,
		"resource_id":   rec.ResourceID,
		"resource_name": rec.ResourceName,
		"operation":     rec.Operation,
		"status":        rec.Status,
		"summary":       rec.Summary,
		"changes":       []map[string]any{},
		"created_at":    rec.CreatedAt.UTC().Format(time.RFC3339),
		"can_revert":    false,
	}
	if rec.ParentID != 0 {
		out["parent_id"] = rec.ParentID
	}
	if rec.ErrorText != "" {
		out["error_text"] = rec.ErrorText
	}
	if rec.CompletedAt != nil {
		out["completed_at"] = rec.CompletedAt.UTC().Format(time.RFC3339)
	}
	if !detail {
		return out
	}

	// Detail: simulate the live comparison against the arr instance. Applied
	// un-reverted records still match their applied snapshot; reverted or
	// failed records show the live (before) values instead.
	currentIsAfter := rec.Status == "applied" && rec.RevertedBy == 0
	fieldState := "matches_applied"
	if !currentIsAfter {
		fieldState = "matches_before"
	}
	changes := make([]map[string]any, 0, len(rec.Changes))
	for _, ch := range rec.Changes {
		current := ch.After
		if !currentIsAfter {
			current = ch.Before
		}
		changes = append(changes, map[string]any{
			"key":           ch.Key,
			"label":         ch.Label,
			"before":        ch.Before,
			"after":         ch.After,
			"current":       current,
			"current_state": fieldState,
		})
	}
	out["changes"] = changes
	if currentIsAfter {
		out["current_status"] = "matches_applied"
	} else {
		out["current_status"] = "different"
		if rec.RevertedBy != 0 {
			out["current_error"] = "Previous settings were restored after this entry, so the live values no longer match it."
		}
	}
	out["can_revert"] = rec.Status == "applied" &&
		rec.ResourceType == "quality_profile" &&
		rec.Operation == "update" &&
		rec.ParentID == 0 &&
		rec.RevertedBy == 0
	return out
}

func aiAdminListChanges(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 100)
	if limit < 1 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	beforeID := queryInt(r, "before_id", 0)

	aiChMu.Lock()
	rows := []map[string]any{}
	for i := len(aiChanges) - 1; i >= 0; i-- { // id DESC
		rec := aiChanges[i]
		if beforeID > 0 && rec.ID >= beforeID {
			continue
		}
		rows = append(rows, aiChangeJSON(rec, false))
		if len(rows) >= limit {
			break
		}
	}
	aiChMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"changes": rows})
}

func aiAdminGetChange(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid change id")
		return
	}
	aiChMu.Lock()
	rec := aiLockedChangeByID(id)
	if rec == nil {
		aiChMu.Unlock()
		writeErr(w, http.StatusNotFound, "change not found")
		return
	}
	// The detail endpoint returns the BARE change object.
	out := aiChangeJSON(rec, true)
	aiChMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func aiAdminRevertChange(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid change id")
		return
	}
	aiChMu.Lock()
	rec := aiLockedChangeByID(id)
	if rec == nil {
		aiChMu.Unlock()
		writeErr(w, http.StatusNotFound, "change not found")
		return
	}
	if rec.Operation == "revert" || rec.ParentID != 0 ||
		!(rec.Status == "applied" && rec.ResourceType == "quality_profile" && rec.Operation == "update") {
		aiChMu.Unlock()
		writeErr(w, http.StatusConflict, "This history entry cannot be restored.")
		return
	}
	if rec.RevertedBy != 0 {
		aiChMu.Unlock()
		writeErr(w, http.StatusConflict, "Previous settings were already restored or a restore requires review.")
		return
	}
	// Append a new linked inverse record (history is never edited).
	now := time.Now()
	swapped := make([]aiFieldDiff, 0, len(rec.Changes))
	for _, ch := range rec.Changes {
		swapped = append(swapped, aiFieldDiff{Key: ch.Key, Label: ch.Label, Before: ch.After, After: ch.Before})
	}
	revert := &aiChangeRec{
		ID:           aiChangeNextID,
		ParentID:     rec.ID,
		ActorUserID:  u.ID,
		ActorName:    u.Username,
		Source:       "admin_revert",
		ServiceType:  rec.ServiceType,
		InstanceID:   rec.InstanceID,
		InstanceName: rec.InstanceName,
		ResourceType: rec.ResourceType,
		ResourceID:   rec.ResourceID,
		ResourceName: rec.ResourceName,
		Operation:    "revert",
		Status:       "applied",
		Summary:      fmt.Sprintf("Quality profile restore: %q", rec.ResourceName),
		Changes:      swapped,
		CreatedAt:    now,
		CompletedAt:  &now,
	}
	aiChangeNextID++
	aiChanges = append(aiChanges, revert)
	rec.RevertedBy = revert.ID
	out := aiChangeJSON(revert, true) // matches_applied, can_revert false
	aiChMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// aiLockedChangeByID finds a history record; callers hold aiChMu.
func aiLockedChangeByID(id int) *aiChangeRec {
	for _, rec := range aiChanges {
		if rec.ID == id {
			return rec
		}
	}
	return nil
}
