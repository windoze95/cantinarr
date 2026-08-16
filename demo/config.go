package main

// config.go — GET /api/config (the per-user gating payload the app builds its
// whole UI from), plus the admin setup checklist (GET /api/admin/setup-status)
// and the update banner (GET|PUT /api/admin/update-status).

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
)

// cfgMu guards config-domain mutable state (the saved management URL).
var cfgMu sync.Mutex

// cfgManagementURL is the admin-saved management-portal URL ("" = unset).
var cfgManagementURL string

// registerConfig mounts the config-domain routes on the authenticated /api
// subrouter. Admin gating is applied per route.
func registerConfig(r chi.Router) {
	r.Get("/config", cfgHandleConfig)
	r.With(requireAdmin).Get("/admin/setup-status", cfgHandleSetupStatus)
	r.With(requireAdmin).Get("/admin/update-status", cfgHandleUpdateStatusGet)
	r.With(requireAdmin).Put("/admin/update-status", cfgHandleUpdateStatusPut)
}

// ─── GET /api/config ────────────────────────────────────

// cfgHandleConfig answers the per-user server config. Visibility rules:
// admins see every instance (incl. download clients + tautulli); non-admins
// see only their effective radarr/sonarr defaults plus a granted chaptarr —
// and every entry on a non-admin's list carries is_default:true.
func cfgHandleConfig(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	isAdmin := u.Role == roleAdmin

	// Snapshot the per-user fields we need under the state lock.
	pins := map[string]string{}
	aiEnabled := false
	withUser(u.ID, func(uu *DemoUser) {
		for k, v := range uu.DefaultInstances {
			pins[k] = v
		}
		aiEnabled = uu.AISharedEnabled
	})

	vis := visibleInstances(u)

	services := map[string]bool{
		"radarr":          false,
		"sonarr":          false,
		"chaptarr":        false,
		"media_downloads": false,
		// ai is per-user: the demo always has a shared credential configured,
		// so the shared-AI grant alone decides it.
		"ai": aiEnabled,
		// Server-level credentials — always configured in the demo.
		"tmdb":  true,
		"trakt": true,
	}

	instances := make([]map[string]any, 0, len(vis))
	for _, inst := range vis {
		switch inst.ServiceType {
		case serviceRadarr:
			services["radarr"] = true
		case serviceSonarr:
			services["sonarr"] = true
		case serviceChaptarr:
			services["chaptarr"] = true
		}
		if inst.MediaDownloads {
			services["media_downloads"] = true
		}

		isDefault := inst.IsDefault
		if !isAdmin {
			// Every entry on a non-admin's list is that user's effective
			// default.
			isDefault = true
		} else if pin, ok := pins[inst.ServiceType]; ok && pin != "" {
			// An admin's personal pin overrides the global default flag for
			// that service type.
			isDefault = pin == inst.ID
		}

		instances = append(instances, map[string]any{
			"id":              inst.ID,
			"service_type":    inst.ServiceType,
			"name":            inst.Name,
			"is_default":      isDefault,
			"media_downloads": inst.MediaDownloads,
		})
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"server_name":     "Cantinarr Demo",
		"version":         "demo",
		"min_app_version": "0.0.0",
		"services":        services,
		"instances":       instances,
		"issues_enabled":  true,
		"allow_reporting": true,
	})
}

// ─── GET /api/admin/setup-status ────────────────────────

// cfgSetupItems derives the 13-item setup checklist from live state on every
// request (never stored). Titles/descriptions are verbatim from the real
// server; configured flags are truthful for the demo.
func cfgSetupItems() []map[string]any {
	haveType := map[string]bool{}
	anyDownloadClient := false
	anyMediaDownloads := false
	for _, inst := range allInstances() {
		haveType[inst.ServiceType] = true
		if inst.MediaDownloads {
			anyMediaDownloads = true
		}
		switch inst.ServiceType {
		case serviceSabnzbd, serviceNzbget, serviceQbittorrent, serviceTransmission:
			anyDownloadClient = true
		}
	}

	// discovery_prefs description is dynamic: the demo seeds discovery
	// settings as SAVED, so the static description applies; if prefs were
	// never saved the derived source would be trakt_trending (Trakt is always
	// configured in the demo), which selects the Trakt-aware description.
	discoveryDesc := "Pick which feed backs the headline rows on Movies and TV, and whether to hide non-English titles."
	if !discoveryPrefsSaved() {
		discoveryDesc = "Trakt is connected, so the headline rows already use it. Confirm the feed here — and whether to hide non-English titles."
	}

	item := func(key, title, description string, configured, optional bool) map[string]any {
		return map[string]any{
			"key":         key,
			"title":       title,
			"description": description,
			"configured":  configured,
			"optional":    optional,
		}
	}

	return []map[string]any{
		item("radarr", "Movies (Radarr)",
			"Connect Radarr so movie requests have somewhere to go.",
			haveType[serviceRadarr], false),
		item("sonarr", "TV (Sonarr)",
			"Connect Sonarr so TV requests have somewhere to go.",
			haveType[serviceSonarr], false),
		item("tmdb", "Discovery (TMDB)",
			"Browsing, search, and artwork are powered by TMDB. The built-in key works out of the box; add your own in the Discover settings to use your account instead.",
			true, false),
		item("push", "Push notifications",
			"Approval, issue, and new-content alerts on devices. Set CANTINARR_PUSH_GATEWAY_URL on the server.",
			false, true),
		item("plex_invites", "Plex invites",
			"Link a Plex account to send server invites with one tap — or automatically.",
			plexConfigured(), true),
		item("trakt", "Trakt discovery",
			"Trending, popular lists, and the release calendar run on Cantinarr's built-in Trakt app out of the box; add your own client ID in the Discover settings to use yours instead.",
			true, true),
		item("discovery_prefs", "Discovery rows",
			discoveryDesc,
			discoveryPrefsSaved(), true),
		item("download_client", "Download activity",
			"See and manage the live download queue (SABnzbd, qBittorrent, NZBGet, or Transmission).",
			anyDownloadClient, true),
		item("media_downloads", "Completed media downloads",
			"Mount media read-only on the server, then map paths inside each Radarr, Sonarr, or Chaptarr instance.",
			anyMediaDownloads, true),
		item("tautulli", "Plex monitoring (Tautulli)",
			"Watch live Plex streams, history, and stats from the app.",
			haveType[serviceTautulli], true),
		item("books", "Books (Chaptarr)",
			"Let users request ebooks and audiobooks; access is granted per user.",
			haveType[serviceChaptarr], true),
		item("ai", "AI assistant",
			"Conversational discovery, requests, and server management. Configure a shared provider; users may override it with their own credentials.",
			true, true),
		// The demo seeds remediation as deliberately decided-and-on, and a
		// shared AI provider is always configured, so the graded
		// broken-shape description can never apply here.
		item("remediation", "Automatic problem detection",
			"Decide whether Cantinarr should detect and investigate stuck downloads on its own. On or off — deciding is the step.",
			true, true),
	}
}

func cfgHandleSetupStatus(w http.ResponseWriter, r *http.Request) {
	items := cfgSetupItems()
	configured := 0
	for _, it := range items {
		if c, ok := it["configured"].(bool); ok && c {
			configured++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"configured": configured,
		"total":      len(items),
	})
}

// ─── GET|PUT /api/admin/update-status ───────────────────

// cfgUpdateStatusJSON builds the full update-status response. The demo runs a
// non-semver build ("demo"), so the update checker is disabled: latest/url
// empty, available false — all four update keys always present.
func cfgUpdateStatusJSON() map[string]any {
	cfgMu.Lock()
	managementURL := cfgManagementURL
	cfgMu.Unlock()
	return map[string]any{
		"update": map[string]any{
			"current":   "demo",
			"latest":    "",
			"available": false,
			"url":       "",
		},
		"management_url": managementURL,
	}
}

func cfgHandleUpdateStatusGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, cfgUpdateStatusJSON())
}

func cfgHandleUpdateStatusPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ManagementURL string `json:"management_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	trimmed := strings.TrimSpace(body.ManagementURL)
	if trimmed != "" {
		parsed, err := url.Parse(trimmed)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			writeErr(w, http.StatusBadRequest, "management_url must be an http(s) URL")
			return
		}
	}
	cfgMu.Lock()
	cfgManagementURL = trimmed
	cfgMu.Unlock()
	writeJSON(w, http.StatusOK, cfgUpdateStatusJSON())
}
