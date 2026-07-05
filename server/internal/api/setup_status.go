package api

import (
	"encoding/json"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/plex"
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

// setupFacts are the booleans the checklist derives from, gathered by the
// handler and kept separate so the item list itself is a pure function.
type setupFacts struct {
	HasRadarr         bool
	HasSonarr         bool
	HasChaptarr       bool
	HasDownloadClient bool
	HasTautulli       bool
	TMDB              bool
	Trakt             bool
	AI                bool
	Push              bool
	PlexInvites       bool
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
			Key:         "books",
			Title:       "Books (Chaptarr)",
			Description: "Let users request ebooks and audiobooks; access is granted per user.",
			Configured:  f.HasChaptarr,
			Optional:    true,
		},
		{
			Key:         "ai",
			Title:       "AI assistant",
			Description: "Conversational discovery, requests, and server management. Bring an Anthropic, OpenAI, or Gemini key.",
			Configured:  f.AI,
			Optional:    true,
		},
	}
}

// setupStatusHandler answers the admin setup checklist: which features are
// configured right now. Everything is re-derived per request.
func setupStatusHandler(cfg *config.Config, store *instance.Store, creds *credentials.Registry, plexService *plex.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var facts setupFacts
		if instances, err := store.ListAll(); err == nil {
			for _, inst := range instances {
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
		facts.Push = cfg.PushGatewayURL != ""
		facts.PlexInvites = plexService.Status().Configured

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
