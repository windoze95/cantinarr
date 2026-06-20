package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/discover"
	"github.com/windoze95/cantinarr-server/internal/downloads"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/mcpserver"
	"github.com/windoze95/cantinarr-server/internal/proxy"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/tautulli"
	"github.com/windoze95/cantinarr-server/internal/web"
	ws "github.com/windoze95/cantinarr-server/internal/websocket"
)

func NewRouter(
	cfg *config.Config,
	authHandler *auth.Handler,
	authService *auth.Service,
	requestHandler *request.Handler,
	proxyHandler *proxy.Handler,
	wsHub *ws.Hub,
	aiHandler *ai.Handler,
	discoverHandler *discover.Handler,
	instanceHandler *instance.Handler,
	instanceStore *instance.Store,
	downloadsHandler *downloads.Handler,
	tautulliHandler *tautulli.Handler,
	creds *credentials.Registry,
	credHandler *credentials.Handler,
	toolServer *mcp.ToolServer,
) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	oauthHandler := auth.NewOAuthHandler(authService)
	r.Get("/.well-known/oauth-protected-resource", oauthHandler.ProtectedResourceMetadata)
	r.Get("/.well-known/oauth-protected-resource/mcp", oauthHandler.ProtectedResourceMetadata)
	r.Get("/.well-known/oauth-authorization-server", oauthHandler.AuthorizationServerMetadata)
	r.Get("/.well-known/openid-configuration", oauthHandler.AuthorizationServerMetadata)
	r.Get("/.well-known/apple-app-site-association", appleAppSiteAssociationHandler(cfg))
	r.Get("/.well-known/assetlinks.json", androidAssetLinksHandler(cfg))
	r.Post("/oauth/register", oauthHandler.RegisterClient)
	r.Get("/oauth/authorize", oauthHandler.Authorize)
	r.Post("/oauth/authorize", oauthHandler.Authorize)
	r.Post("/oauth/passkey/login/begin", oauthHandler.BeginOAuthPasskeyLogin)
	r.Post("/oauth/passkey/login/finish", oauthHandler.FinishOAuthPasskeyLogin)
	r.Post("/oauth/token", oauthHandler.Token)
	r.Get("/passkeys/setup", oauthHandler.PasskeySetup)
	r.Get("/passkeys/create", oauthHandler.PasskeyCreate)

	r.Route("/api", func(r chi.Router) {
		// CORS: same-origin only (frontend is served from the same origin).
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   []string{},
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
			ExposedHeaders:   []string{"Link"},
			AllowCredentials: false,
			MaxAge:           300,
		}))
		r.Use(middleware.SetHeader("Content-Type", "application/json"))

		// WebSocket (auth handled via subprotocol header)
		r.Get("/ws", wsHub.ServeWS)

		// Health check
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		})

		// Rate limiter for public auth endpoints: 10 requests per minute per IP
		authLimiter := auth.NewRateLimiter(10, 1*time.Minute)

		// Auth routes (public)
		r.Route("/auth", func(r chi.Router) {
			r.Get("/status", authHandler.AuthStatus)
			r.With(authLimiter.Middleware).Post("/setup", authHandler.HandleSetup)
			r.With(authLimiter.Middleware).Post("/login", authHandler.Login)
			r.Post("/refresh", authHandler.Refresh)
			r.With(authLimiter.Middleware).Post("/connect", authHandler.HandleRedeemConnectToken)

			// Passkey login (public, rate-limited)
			r.With(authLimiter.Middleware).Post("/passkey/login/begin", authHandler.BeginPasskeyLogin)
			r.With(authLimiter.Middleware).Post("/passkey/login/finish", authHandler.FinishPasskeyLogin)
			r.With(authLimiter.Middleware).Post("/passkey/setup/begin", authHandler.BeginPasskeySetup)
			r.With(authLimiter.Middleware).Post("/passkey/setup/finish", authHandler.FinishPasskeySetup)

			// Protected auth routes
			r.Group(func(r chi.Router) {
				r.Use(authService.AuthMiddleware)
				r.Get("/me", authHandler.Me)
				r.With(authLimiter.Middleware).Post("/password", authHandler.SetPassword)

				// Passkey registration (authenticated)
				r.Post("/passkey/register/begin", authHandler.BeginPasskeyRegistration)
				r.Post("/passkey/register/finish", authHandler.FinishPasskeyRegistration)
				r.Post("/passkey/setup-link", authHandler.CreatePasskeySetupLink)
				r.Get("/passkeys", authHandler.ListPasskeys)
				r.Delete("/passkeys/{credentialID}", authHandler.DeletePasskey)
			})
		})

		// Admin routes
		r.Route("/admin", func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Post("/connect-token", authHandler.HandleCreateConnectToken)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/devices", authHandler.HandleListDevices)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Delete("/devices/{deviceID}", authHandler.HandleRevokeDevice)

			// User management
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/users", authHandler.HandleListUsers)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Patch("/users/{userID}", authHandler.HandleUpdateUserRole)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Patch("/users/{userID}/auth-methods", authHandler.HandleUpdateUserAuthMethods)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Delete("/users/{userID}", authHandler.HandleDeleteUser)

			// Credential management
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Get("/credentials", credHandler.Get)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Put("/credentials", credHandler.Update)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Delete("/credentials/{key}", credHandler.Delete)

			// AI tool toggles
			aiToolsHandler := mcp.NewToolSettingsHandler(toolServer)
			r.With(auth.RequirePermission(auth.PermissionAIToolsManage)).Get("/ai-tools", aiToolsHandler.List)
			r.With(auth.RequirePermission(auth.PermissionAIToolsManage)).Put("/ai-tools/debug", aiToolsHandler.UpdateDebug)
			r.With(auth.RequirePermission(auth.PermissionAIToolsManage)).Put("/ai-tools/{name}", aiToolsHandler.Update)
		})

		// Config route (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Get("/config", configHandler(cfg, instanceStore, creds))
		})

		// Request routes (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Use(auth.RequirePermission(auth.PermissionMediaRequest))
			r.Post("/requests", requestHandler.Create)
			r.Get("/requests", requestHandler.List)
			r.Get("/requests/{tmdb_id}/status", requestHandler.GetStatus)
		})

		// Discover / media routes (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Use(auth.RequirePermission(auth.PermissionMediaDiscover))

			// Discover
			r.Get("/discover/trending", discoverHandler.Trending)
			r.Get("/discover/movies/popular", discoverHandler.PopularMovies)
			r.Get("/discover/tv/popular", discoverHandler.PopularTV)
			r.Get("/discover/movies/top-rated", discoverHandler.TopRatedMovies)
			r.Get("/discover/movies/upcoming", discoverHandler.UpcomingMovies)
			r.Get("/discover/movies/now-playing", discoverHandler.NowPlayingMovies)
			r.Get("/discover/movies", discoverHandler.DiscoverMovies)
			r.Get("/discover/tv", discoverHandler.DiscoverTV)

			// Search
			r.Get("/search", discoverHandler.Search)

			// Media details
			r.Get("/media/movie/{id}", discoverHandler.MovieDetail)
			r.Get("/media/tv/{id}", discoverHandler.TVDetail)
			r.Get("/media/movie/{id}/recommendations", discoverHandler.MovieRecommendations)
			r.Get("/media/tv/{id}/recommendations", discoverHandler.TVRecommendations)
			r.Get("/media/movie/{id}/similar", discoverHandler.SimilarMovies)
			r.Get("/media/tv/{id}/similar", discoverHandler.SimilarTV)
			r.Get("/media/person/{id}", discoverHandler.PersonDetail)
			r.Get("/media/person/{id}/credits", discoverHandler.PersonCredits)

			// Genres & providers
			r.Get("/genres/movie", discoverHandler.MovieGenres)
			r.Get("/genres/tv", discoverHandler.TVGenres)
			r.Get("/providers/movie", discoverHandler.MovieWatchProviders)

			// Trakt
			r.Get("/trakt/trending", discoverHandler.TraktTrending)
			r.Get("/trakt/popular", discoverHandler.TraktPopular)
			r.Get("/trakt/lists", discoverHandler.TraktPopularLists)
			r.Get("/trakt/lists/{user}/{slug}/items", discoverHandler.TraktListItems)
			r.Get("/trakt/calendar", discoverHandler.TraktCalendar)
			r.Get("/trakt/anticipated", discoverHandler.TraktAnticipated)
			r.Get("/trakt/recommendations", discoverHandler.TraktRecommendations)
		})

		// AI routes (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Use(auth.RequirePermission(auth.PermissionAIChat))
			r.Post("/ai/chat", aiHandler.Chat)
			r.Get("/ai/available", aiHandler.Available)
		})

		// Instance CRUD routes (admin only)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Use(auth.RequirePermission(auth.PermissionInstancesManage))

			r.Get("/instances", instanceHandler.List)
			r.Post("/instances", instanceHandler.Create)
			r.Put("/instances/{instanceID}", instanceHandler.Update)
			r.Delete("/instances/{instanceID}", instanceHandler.Delete)

			// Instance proxy — forward to specific instance
			r.HandleFunc("/instances/{instanceID}/*", proxyHandler.InstanceProxy())
		})

		// Download client routes (admin only)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)

			r.With(auth.RequirePermission(auth.PermissionDownloadsRead)).Get("/downloads/{instanceID}/queue", downloadsHandler.GetQueue)
			r.With(auth.RequirePermission(auth.PermissionDownloadsManage)).Post("/downloads/{instanceID}/queue/{itemID}/pause", downloadsHandler.PauseItem)
			r.With(auth.RequirePermission(auth.PermissionDownloadsManage)).Post("/downloads/{instanceID}/queue/{itemID}/resume", downloadsHandler.ResumeItem)
			r.With(auth.RequirePermission(auth.PermissionDownloadsManage)).Delete("/downloads/{instanceID}/queue/{itemID}", downloadsHandler.DeleteItem)
			r.With(auth.RequirePermission(auth.PermissionDownloadsManage)).Post("/downloads/{instanceID}/pause", downloadsHandler.PauseAll)
			r.With(auth.RequirePermission(auth.PermissionDownloadsManage)).Post("/downloads/{instanceID}/resume", downloadsHandler.ResumeAll)
			r.With(auth.RequirePermission(auth.PermissionDownloadsRead)).Get("/downloads/{instanceID}/history", downloadsHandler.GetHistory)
		})

		// Tautulli (Plex monitoring) routes (admin only)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Use(auth.RequirePermission(auth.PermissionMonitoringRead))

			r.Get("/tautulli/{instanceID}/activity", tautulliHandler.GetActivity)
			r.Get("/tautulli/{instanceID}/history", tautulliHandler.GetHistory)
			r.Get("/tautulli/{instanceID}/stats", tautulliHandler.GetStats)
		})

	})

	// MCP endpoint (authenticated, separate CORS for external MCP clients)
	mcpHandler := mcpserver.NewMCPHandler(toolServer)
	r.Route("/mcp", func(r chi.Router) {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   []string{"*"},
			AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Authorization", "Content-Type", "Mcp-Session-Id"},
			ExposedHeaders:   []string{"Mcp-Session-Id"},
			AllowCredentials: false,
		}))
		r.Use(oauthHandler.MCPAuthMiddleware)
		r.Use(auth.RequirePermission(auth.PermissionMCPAccess))
		r.Handle("/", mcpHandler)
		r.Handle("/*", mcpHandler)
	})

	// Serve Flutter web UI at root (catch-all for non-API routes)
	r.NotFound(web.Handler().ServeHTTP)

	return r
}

func appleAppSiteAssociationHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(cfg.AppleAppIDs) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"webcredentials": map[string]any{
				"apps": cfg.AppleAppIDs,
			},
		})
	}
}

func androidAssetLinksHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(cfg.AndroidCertFingerprints) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"relation": []string{
					"delegate_permission/common.get_login_creds",
				},
				"target": map[string]any{
					"namespace":                "android_app",
					"package_name":             cfg.AndroidPackageName,
					"sha256_cert_fingerprints": cfg.AndroidCertFingerprints,
				},
			},
		})
	}
}

func configHandler(cfg *config.Config, store *instance.Store, creds *credentials.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Build instances list
		type instanceInfo struct {
			ID          string `json:"id"`
			ServiceType string `json:"service_type"`
			Name        string `json:"name"`
			IsDefault   bool   `json:"is_default"`
		}

		var instances []instanceInfo
		allInstances, err := store.ListAll()
		if err == nil {
			for _, inst := range allInstances {
				instances = append(instances, instanceInfo{
					ID:          inst.ID,
					ServiceType: inst.ServiceType,
					Name:        inst.Name,
					IsDefault:   inst.IsDefault,
				})
			}
		}
		if instances == nil {
			instances = []instanceInfo{}
		}

		// Derive radarr/sonarr availability from instances
		hasRadarr := false
		hasSonarr := false
		for _, inst := range instances {
			if inst.ServiceType == "radarr" {
				hasRadarr = true
			}
			if inst.ServiceType == "sonarr" {
				hasSonarr = true
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"server_name": cfg.ServerName,
			"services": map[string]bool{
				"radarr": hasRadarr,
				"sonarr": hasSonarr,
				"ai":     creds.IsAIConfigured(),
				"tmdb":   creds.IsConfigured(credentials.KeyTMDBAccessToken),
				"trakt":  creds.IsConfigured(credentials.KeyTraktClientID),
			},
			"instances": instances,
		})
	}
}
