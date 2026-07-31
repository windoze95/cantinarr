package api

import (
	"encoding/json"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/plex"
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
}

// setupFacts is the configuration state the checklist derives from, gathered by
// the handler and kept separate so the item list itself is a pure function.
type setupFacts struct {
	HasRadarr         bool
	HasSonarr         bool
	HasChaptarr       bool
	HasDownloadClient bool
	MediaDownloads    bool
	HasTautulli       bool
	TMDB              bool
	Trakt             bool
	AI                bool
	Push              bool
	PlexInvites       bool
	// DiscoveryChosen is whether an admin has ever saved a discovery
	// preference. Unlike the rest of the checklist there is no "correct"
	// answer to grade against — every source and either language setting is
	// valid — so the item asks whether the decision was made at all.
	DiscoveryChosen bool
	// DiscoverySource is the feed currently backing the headline rows, used
	// only to describe the state back to the admin.
	DiscoverySource string
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
			Description: "Browsing, search, and artwork are powered by TMDB.",
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
			Key:         "plex_invites",
			Title:       "Plex invites",
			Description: "Link a Plex account to send server invites with one tap — or automatically.",
			Configured:  f.PlexInvites,
			Optional:    true,
		},
		{
			Key:         "download_client",
			Title:       "Download activity",
			Description: "See and manage the live download queue (SABnzbd, qBittorrent, NZBGet, or Transmission).",
			Configured:  f.HasDownloadClient,
			Optional:    true,
		},
		{
			Key:         "media_downloads",
			Title:       "Completed media downloads",
			Description: "Mount media read-only on the server, then map paths inside each Radarr, Sonarr, or Chaptarr instance.",
			Configured:  f.MediaDownloads,
			Optional:    true,
		},
		{
			Key:         "tautulli",
			Title:       "Plex monitoring (Tautulli)",
			Description: "Watch live Plex streams, history, and stats from the app.",
			Configured:  f.HasTautulli,
			Optional:    true,
		},
		{
			Key:         "trakt",
			Title:       "Trakt discovery",
			Description: "Adds trending, popular lists, and the release calendar to discovery.",
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
			Key:         "books",
			Title:       "Books (Chaptarr)",
			Description: "Let users request ebooks and audiobooks; access is granted per user.",
			Configured:  f.HasChaptarr,
			Optional:    true,
		},
		{
			Key:         "ai",
			Title:       "AI assistant",
			Description: "Conversational discovery, requests, and server management. Configure a shared provider; users may override it with their own credentials.",
			Configured:  f.AI,
			Optional:    true,
		},
	}
}

// setupStatusHandler answers the admin setup checklist: which features are
// configured right now. Everything is re-derived per request.
func setupStatusHandler(cfg *config.Config, store *instance.Store, creds *credentials.Registry, aiHandler *ai.Handler, plexService *plex.Service, serverSettings *serversettings.Service) http.HandlerFunc {
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
				case "tautulli":
					facts.HasTautulli = true
				case "sabnzbd", "qbittorrent", "nzbget", "transmission":
					facts.HasDownloadClient = true
				}
			}
		}
		facts.TMDB = creds.IsConfigured(credentials.KeyTMDBAccessToken)
		facts.Trakt = creds.IsConfigured(credentials.KeyTraktClientID)
		facts.AI = creds.IsAIConfigured()
		if aiHandler != nil {
			facts.AI = aiHandler.ProviderConfigured()
		}
		facts.Push = cfg.PushGatewayURL != ""
		facts.PlexInvites = plexService.Status().Configured
		if serverSettings != nil {
			facts.DiscoveryChosen = serverSettings.DiscoveryChosen()
			facts.DiscoverySource = serverSettings.Get().DiscoverySource
		}

		items := buildSetupItems(facts)
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
