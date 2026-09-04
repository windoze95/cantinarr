package api

import (
	"encoding/json"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/downloads"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/remediation"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// setupItem is one entry in the admin setup checklist. The list is DERIVED
// live from actual configuration on every request — never stored — so the
// setup wizard is resumable and editable for free and can never go stale.
// New features grow the product by adding an item here; clients render
// unknown keys generically, so old apps still show new items.
type setupItem struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Configured  bool   `json:"configured"`
	// Optional separates "the app doesn't work without this" (Radarr/Sonarr/
	// TMDB) from features an admin may deliberately skip.
	Optional bool `json:"optional"`
	// Skipped marks an optional item an admin acknowledged and dismissed, so
	// clients can stop counting it as unfinished without a persistent nag.
	// Only optional items ever carry it — an essential in the stored skip set
	// is ignored rather than silenced — and a skipped item that later becomes
	// configured simply reads as configured. Stored server-wide (the
	// checklist grades the server, not a device) and reversible in place.
	Skipped bool `json:"skipped,omitempty"`
}

// setupFacts is the configuration state the checklist derives from, gathered by
// the handler and kept separate so the item list itself is a pure function.
type setupFacts struct {
	HasRadarr         bool
	HasSonarr         bool
	HasChaptarr       bool
	HasLidarr         bool
	HasDownloadClient bool
	MediaDownloads    bool
	HasWatchHistory   bool
	HasMediaServer    bool
	TMDB              bool
	Trakt             bool
	AI                bool
	Push              bool
	// RemediationDecided is whether an admin has ever saved remediation
	// settings; like discovery, on and off are both correct answers and the
	// checklist only asks that the decision was made.
	RemediationDecided bool
	// RemediationEnabled + AI together surface the one genuinely broken shape:
	// detection switched on with no shared provider to investigate.
	RemediationEnabled bool
	// DiscoveryChosen is whether an admin has ever saved a discovery
	// preference. Unlike the rest of the checklist there is no "correct"
	// answer to grade against — every source and either language setting is
	// valid — so the item asks whether the decision was made at all.
	DiscoveryChosen bool
	// DiscoverySource is the feed currently backing the headline rows, used
	// only to describe the state back to the admin.
	DiscoverySource string
}

// remediationDescription grades the decision, then calls out the one
// genuinely broken shape: detection switched on with nothing configured to
// investigate — the issues it opens would only ever wait.
func remediationDescription(f setupFacts) string {
	if f.RemediationEnabled && !f.AI {
		return "Remediation is ON but no shared AI provider is configured — detected problems wait instead of being investigated. Add a provider under Providers & Credentials."
	}
	return "Decide whether Cantinarr should detect and investigate stuck downloads on its own. On or off — deciding is the step."
}

// discoveryDescription explains the discovery-rows step in terms of what is
// true right now. Rows that are running on Trakt because the credential
// appeared — not because anyone picked it — are the state worth calling out:
// the server changed what the headline row shows, and this step is the only
// place an admin would find that out. Once a decision exists the plain copy is
// right; the admin chose, and the checklist does not second-guess which.
func discoveryDescription(f setupFacts) string {
	if !f.DiscoveryChosen && f.Trakt && f.DiscoverySource == serversettings.DiscoverySourceTraktTrending {
		return "Trakt is connected, so the headline rows already use it. Confirm the feed here — and whether to hide non-English titles."
	}
	return "Pick which feed backs the headline rows on Movies and TV, and whether to hide non-English titles."
}

// buildSetupItems maps configuration facts to the ordered checklist:
// essentials first, then optional features in rough order of impact.
func buildSetupItems(f setupFacts) []setupItem {
	return []setupItem{
		{
			Key:         "radarr",
			Title:       "Movies (Radarr)",
			Description: "Connect Radarr so movie requests have somewhere to go.",
			Configured:  f.HasRadarr,
		},
		{
			Key:         "sonarr",
			Title:       "TV (Sonarr)",
			Description: "Connect Sonarr so TV requests have somewhere to go.",
			Configured:  f.HasSonarr,
		},
		{
			Key:         "tmdb",
			Title:       "Discovery (TMDB)",
			Description: "Browsing, search, and artwork are powered by TMDB. The built-in key works out of the box; add your own in the Discover settings to use your account instead.",
			Configured:  f.TMDB,
		},
		{
			Key:         "push",
			Title:       "Push notifications",
			Description: "Approval, issue, and new-content alerts on devices. Set CANTINARR_PUSH_GATEWAY_URL on the server.",
			Configured:  f.Push,
			Optional:    true,
		},
		{
			Key:         "media_servers",
			Title:       "Media server access",
			Description: "Connect Plex, Jellyfin, or Emby so users can get access from the app: a Plex invite, or an account they create themselves.",
			Configured:  f.HasMediaServer,
			Optional:    true,
		},
		{
			Key:         "trakt",
			Title:       "Trakt discovery",
			Description: "Trending, popular lists, and the release calendar run on Cantinarr's built-in Trakt app out of the box; add your own client ID in the Discover settings to use yours instead.",
			Configured:  f.Trakt,
			Optional:    true,
		},
		{
			Key:         "discovery_prefs",
			Title:       "Discovery rows",
			Description: discoveryDescription(f),
			Configured:  f.DiscoveryChosen,
			Optional:    true,
		},
		{
			Key:         "download_client",
			Title:       "Download activity",
			Description: "See and manage the live download queue (SABnzbd, qBittorrent, NZBGet, Transmission, or Deluge).",
			Configured:  f.HasDownloadClient,
			Optional:    true,
		},
		{
			Key:         "media_downloads",
			Title:       "Completed media downloads",
			Description: "Mount media read-only on the server, then map paths inside each Radarr, Sonarr, Chaptarr, or Lidarr instance.",
			Configured:  f.MediaDownloads,
			Optional:    true,
		},
		{
			// The key predates Tracearr and stays: admins' dismissals are
			// stored against it.
			Key:         "tautulli",
			Title:       "Monitoring (Tautulli or Tracearr)",
			Description: "See live streams, watch history, and stats in the Monitoring module. Tautulli covers Plex; Tracearr covers Plex, Jellyfin, and Emby.",
			Configured:  f.HasWatchHistory,
			Optional:    true,
		},
		{
			Key:         "books",
			Title:       "Books (Chaptarr)",
			Description: "Let users request ebooks and audiobooks; access is granted per user.",
			Configured:  f.HasChaptarr,
			Optional:    true,
		},
		{
			Key:         "music",
			Title:       "Music (Lidarr)",
			Description: "Let users request albums; access is granted per user.",
			Configured:  f.HasLidarr,
			Optional:    true,
		},
		{
			Key:         "ai",
			Title:       "AI assistant",
			Description: "Conversational discovery, requests, and server management. Configure a shared provider; users may override it with their own credentials.",
			Configured:  f.AI,
			Optional:    true,
		},
		{
			Key:         "remediation",
			Title:       "Automatic problem detection",
			Description: remediationDescription(f),
			Configured:  f.RemediationDecided,
			Optional:    true,
		},
	}
}

// setupStatusHandler answers the admin setup checklist: which features are
// configured right now. Everything is re-derived per request.
func setupStatusHandler(cfg *config.Config, store *instance.Store, creds *credentials.Registry, aiHandler *ai.Handler, serverSettings *serversettings.Service, remediationSvc *remediation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var facts setupFacts
		if instances, err := store.ListAll(); err == nil {
			for _, inst := range instances {
				if inst.MediaDownloadsConfigured(cfg.MediaDownloadRoots) {
					facts.MediaDownloads = true
				}
				switch inst.ServiceType {
				case "radarr":
					facts.HasRadarr = true
				case "sonarr":
					facts.HasSonarr = true
				case "chaptarr":
					facts.HasChaptarr = true
				case "lidarr":
					facts.HasLidarr = true
				case "tautulli", "tracearr":
					facts.HasWatchHistory = true
				default:
					if downloads.IsDownloadClientType(inst.ServiceType) {
						facts.HasDownloadClient = true
					} else if instance.IsMediaServerType(inst.ServiceType) {
						facts.HasMediaServer = true
					}
				}
			}
		}
		facts.TMDB = creds.TMDBAvailable()
		facts.Trakt = creds.TraktAvailable()
		facts.AI = creds.IsAIConfigured()
		if remediationSvc != nil {
			facts.RemediationDecided = remediationSvc.SettingsDecided()
			facts.RemediationEnabled = remediationSvc.Settings().Enabled
		}
		if aiHandler != nil {
			facts.AI = aiHandler.ProviderConfigured()
		}
		facts.Push = cfg.PushGatewayURL != ""
		if serverSettings != nil {
			facts.DiscoveryChosen = serverSettings.DiscoveryChosen()
			facts.DiscoverySource = serverSettings.Get().DiscoverySource
		}

		items := buildSetupItems(facts)
		if serverSettings != nil {
			skipped := make(map[string]bool)
			for _, key := range serverSettings.Get().SetupSkippedItems {
				skipped[key] = true
			}
			for i := range items {
				// Optional-only: a skip stored against an essential (a
				// downgraded build, a hand-edited row) fails toward showing
				// the item rather than silencing something the server cannot
				// work without.
				items[i].Skipped = items[i].Optional && skipped[items[i].Key]
			}
		}
		configured := 0
		for _, item := range items {
			if item.Configured {
				configured++
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"items":      items,
			"configured": configured,
			"total":      len(items),
		})
	}
}

// setupSkipHandler records or clears one checklist skip. Only keys the
// current build's checklist actually contains may be written, and only
// optional ones: an essential can never be acknowledged away, because the
// alarm it carries is about capability, not tidiness.
func setupSkipHandler(serverSettings *serversettings.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if serverSettings == nil {
			http.Error(w, `{"error":"setup skips are unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Key     string `json:"key"`
			Skipped bool   `json:"skipped"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		// Keys and optionality are static facts about the item list, so an
		// empty facts build enumerates them without touching configuration.
		valid := false
		for _, item := range buildSetupItems(setupFacts{}) {
			if item.Key != body.Key {
				continue
			}
			if !item.Optional {
				http.Error(w, `{"error":"only optional setup items can be skipped"}`, http.StatusBadRequest)
				return
			}
			valid = true
			break
		}
		if !valid {
			http.Error(w, `{"error":"unknown setup item"}`, http.StatusBadRequest)
			return
		}
		if _, err := serverSettings.SetSetupItemSkipped(body.Key, body.Skipped); err != nil {
			http.Error(w, `{"error":"could not save the skip"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"key": body.Key, "skipped": body.Skipped})
	}
}
