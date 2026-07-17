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
	"github.com/windoze95/cantinarr-server/internal/plex"
	"github.com/windoze95/cantinarr-server/internal/proxy"
	"github.com/windoze95/cantinarr-server/internal/push"
	"github.com/windoze95/cantinarr-server/internal/remediation"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
	"github.com/windoze95/cantinarr-server/internal/tautulli"
	"github.com/windoze95/cantinarr-server/internal/update"
	"github.com/windoze95/cantinarr-server/internal/version"
	"github.com/windoze95/cantinarr-server/internal/web"
	"github.com/windoze95/cantinarr-server/internal/webhooks"
	ws "github.com/windoze95/cantinarr-server/internal/websocket"
)

func NewRouter(
	cfg *config.Config,
	authHandler *auth.Handler,
	authService *auth.Service,
	requestHandler *request.Handler,
	remediationService *remediation.Service,
	remediationHandler *remediation.Handler,
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
	pushHandler *push.Handler,
	webhookHandler *webhooks.Handler,
	plexHandler *plex.Handler,
	plexService *plex.Service,
	updateChecker *update.Checker,
	serverSettings *serversettings.Service,
) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(safeRequestLogger)
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

		// Arr webhook receiver (Sonarr/Radarr → Connect → Webhook). No session:
		// the server-only per-instance credential is supplied through Basic Auth;
		// query-string credentials are rejected so access logs cannot retain them.
		r.Post("/webhooks/arr/{instanceID}", webhookHandler.HandleArr)

		// Rate limiter for public auth endpoints: 10 requests per minute per IP
		authLimiter := auth.NewRateLimiter(10, 1*time.Minute)
		// Keep authenticated ChatGPT device-flow churn from consuming the public
		// password/passkey budget for everyone behind the same household proxy.
		codexLoginLimiter := auth.NewRateLimiter(10, 1*time.Minute)

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
				r.With(authLimiter.Middleware).Post("/plex-email", authHandler.SetPlexEmail)

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
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Put("/users/{userID}/ai-access", authHandler.HandleUpdateUserAIAccess)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Delete("/users/{userID}", authHandler.HandleDeleteUser)
			// Send a test push to a specific user's devices (delivery diagnostics).
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Post("/users/{userID}/test-push", pushHandler.TestPushToUser)

			// Setup checklist: which features are configured, derived live on
			// every request (drives the app's setup wizard + reminders).
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Get("/setup-status", setupStatusHandler(cfg, instanceStore, creds, aiHandler, plexService))

			// Update availability + the admin-configured management-portal URL that
			// backs the "update available" banner. GET returns both; PUT sets the
			// management URL.
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Get("/update-status", updateStatusHandler(updateChecker, serverSettings))
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Put("/update-status", updateServerSettingsHandler(updateChecker, serverSettings))

			// Plex integration: link the admin's Plex account (PIN flow), pick
			// the server/libraries invites share, and send one-tap invites for
			// a user's shared Plex email.
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/plex/status", plexHandler.Status)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Post("/plex/link/begin", plexHandler.BeginLink)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Post("/plex/link/check", plexHandler.CheckLink)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Delete("/plex/link", plexHandler.Unlink)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/plex/servers", plexHandler.Servers)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/plex/servers/{machineID}/libraries", plexHandler.Libraries)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Put("/plex/settings", plexHandler.UpdateSettings)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Post("/users/{userID}/plex-invite", plexHandler.InviteUser)

			// Per-user default *arr instance overrides (admin-managed). Pins which
			// instance is a given user's default source per service type, and —
			// for service types with no global default (chaptarr) — grants the
			// user access to that instance.
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/users/{userID}/default-instances", instanceHandler.GetUserDefaultInstances)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Put("/users/{userID}/default-instances", instanceHandler.UpdateUserDefaultInstances)

			// Credential management
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Get("/credentials", credHandler.Get)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Put("/credentials", credHandler.Update)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Delete("/credentials/{key}", credHandler.Delete)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Get("/ai/codex/status", aiHandler.SharedCodexStatus)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage), codexLoginLimiter.Middleware).Post("/ai/codex/device/begin", aiHandler.BeginSharedCodexDeviceLogin)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Get("/ai/codex/device/{flowID}", aiHandler.CheckSharedCodexDeviceLogin)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Delete("/ai/codex/device/{flowID}", aiHandler.CancelSharedCodexDeviceLogin)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Delete("/ai/codex", aiHandler.UnlinkSharedCodex)

			// AI tool toggles
			aiToolsHandler := mcp.NewToolSettingsHandler(toolServer)
			r.With(auth.RequirePermission(auth.PermissionAIToolsManage)).Get("/ai-tools", aiToolsHandler.List)
			r.With(auth.RequirePermission(auth.PermissionAIToolsManage)).Put("/ai-tools/debug", aiToolsHandler.UpdateDebug)
			r.With(auth.RequirePermission(auth.PermissionAIToolsManage)).Put("/ai-tools/{name}", aiToolsHandler.Update)

			// Media request management: approval queue + request defaults
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Get("/requests", requestHandler.ListPending)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Post("/requests/{id}/approve", requestHandler.Approve)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Post("/requests/{id}/deny", requestHandler.Deny)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Get("/request-settings", requestHandler.GetSettings)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Put("/request-settings", requestHandler.UpdateSettings)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Get("/users/{userID}/request-settings", requestHandler.GetUserSettings)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Put("/users/{userID}/request-settings", requestHandler.UpdateUserSettings)

			// AI remediation: issue queue + dismissal + global settings (Wave 1).
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/issues", remediationHandler.ListAdmin)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/issues/{id}/dismiss", remediationHandler.Dismiss)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/issues/{id}/resolve", remediationHandler.ResolveIssue)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/issues/{id}/activity", remediationHandler.GetIssueActivity)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/remediation-settings", remediationHandler.GetSettings)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Put("/remediation-settings", remediationHandler.UpdateSettings)

			// AI remediation: agent-action approval queue + run audit (Wave 3 —
			// propose→approve→execute). Approval claims a stored proposal for
			// at-most-once dispatch; denial resumes the investigation.
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/agent-actions", remediationHandler.ListActions)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/agent-actions/{id}", remediationHandler.GetAction)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/agent-actions/{id}/approve", remediationHandler.ApproveAction)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/agent-actions/{id}/deny", remediationHandler.DenyAction)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/agent-runs/{id}", remediationHandler.GetRun)
		})

		// Config route (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Get("/config", configHandler(cfg, instanceStore, creds, aiHandler, remediationService))
		})

		// Device push-token + notification preference routes (authenticated).
		// Any signed-in user may register/clear the APNs token for one of their
		// own devices, read/update their own notification preferences, and fire
		// a test push to their own devices.
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Post("/devices/push-token", pushHandler.Register)
			r.Delete("/devices/push-token/{deviceID}", pushHandler.Delete)
			r.Get("/notifications/preferences", pushHandler.GetPreferences)
			r.Put("/notifications/preferences", pushHandler.UpdatePreferences)
			r.Post("/notifications/test", pushHandler.TestPush)
		})

		// Request routes (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Use(auth.RequirePermission(auth.PermissionMediaRequest))
			r.Post("/requests", requestHandler.Create)
			r.Get("/requests", requestHandler.List)
			r.Get("/requests/options", requestHandler.Options)
			r.Get("/requests/book-status", requestHandler.GetBookStatus)
			r.Get("/requests/book-library", requestHandler.GetBookLibrary)
			r.Get("/requests/{tmdb_id}/status", requestHandler.GetStatus)
		})

		// Issue reporting (authenticated). Filing an issue needs the same
		// permission as requesting media; viewing/replying to a single issue is
		// gated in-handler to the issue's reporter or an admin.
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.With(auth.RequirePermission(auth.PermissionMediaRequest)).Post("/issues", remediationHandler.Create)
			r.Get("/issues/{id}", remediationHandler.Get)
			r.Post("/issues/{id}/reply", remediationHandler.Reply)
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

			// Account visibility and revocation remain available to the account
			// owner even if their role later loses AI access.
			r.Get("/ai/codex/status", aiHandler.CodexStatus)
			r.Delete("/ai/codex", aiHandler.UnlinkCodex)
			r.Delete("/ai/codex/device/{flowID}", aiHandler.CancelCodexDeviceLogin)
			r.Get("/ai/settings", aiHandler.AISettings)
			r.Delete("/ai/settings", aiHandler.DeleteAISettings)
			r.Delete("/ai/credentials/{provider}", aiHandler.DeletePersonalAICredential)

			r.Group(func(r chi.Router) {
				r.Use(auth.RequirePermission(auth.PermissionAIChat))
				r.Post("/ai/chat", aiHandler.Chat)
				r.Get("/ai/available", aiHandler.Available)
				r.Put("/ai/settings", aiHandler.UpdateAISettings)
				r.Put("/ai/credentials/{provider}", aiHandler.UpdatePersonalAICredential)
				r.With(codexLoginLimiter.Middleware).Post("/ai/codex/device/begin", aiHandler.BeginCodexDeviceLogin)
				r.Get("/ai/codex/device/{flowID}", aiHandler.CheckCodexDeviceLogin)
			})
		})

		// Instance routes (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)

			// Instance CRUD — admin only
			r.Group(func(r chi.Router) {
				r.Use(auth.RequirePermission(auth.PermissionInstancesManage))
				r.Get("/instances", instanceHandler.List)
				r.Post("/instances", instanceHandler.Create)
				r.Put("/instances/{instanceID}", instanceHandler.Update)
				r.Delete("/instances/{instanceID}", instanceHandler.Delete)
				// Instance-centric view of user_default_instances (the static
				// "users" segment wins over the proxy wildcard below): which
				// users are pinned to which instance of this instance's service
				// type, and (PUT) assign this instance to an exact set of users.
				r.Get("/instances/{instanceID}/users", instanceHandler.GetInstanceUsers)
				r.Put("/instances/{instanceID}/users", instanceHandler.UpdateInstanceUsers)
				// Configure the server-managed Radarr/Sonarr Connect webhook
				// without ever returning its callback credential to the app.
				r.Post("/instances/{instanceID}/webhook", instanceHandler.ConfigureWebhook)
			})

			// Instance proxy — forward to specific instance. Read-only
			// Radarr/Sonarr browsing is allowed for non-admins (arr:browse);
			// every other request (writes, commands, interactive search, config,
			// and non-arr services) requires instances:manage. See
			// auth.RequireArrProxyAccess.
			r.With(auth.RequireArrProxyAccess(instanceStore)).HandleFunc("/instances/{instanceID}/*", proxyHandler.InstanceProxy())
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

type configInstanceStore interface {
	ListAll() ([]instance.Instance, error)
	ListUserDefaults(userID int64) (map[string]string, error)
}

func configHandler(cfg *config.Config, store configInstanceStore, creds *credentials.Registry, aiHandler *ai.Handler, remediationService *remediation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// Build instances list
		type instanceInfo struct {
			ID          string `json:"id"`
			ServiceType string `json:"service_type"`
			Name        string `json:"name"`
			IsDefault   bool   `json:"is_default"`
		}

		// The config payload is per-user: admins see every instance, while
		// regular users only see the effective default Radarr/Sonarr instances
		// selected for them and any Chaptarr instance explicitly granted by an
		// admin.
		claims := auth.GetClaims(r.Context())
		var userID int64
		isAdmin := false
		if claims != nil {
			userID = claims.UserID
			isAdmin = auth.HasPermission(claims.Role, auth.PermissionInstancesManage)
		}
		overrides := map[string]string{}
		if userID != 0 {
			var err error
			overrides, err = store.ListUserDefaults(userID)
			if err != nil {
				http.Error(w, `{"error":"temporarily unavailable, retry shortly"}`, http.StatusServiceUnavailable)
				return
			}
		}

		instances := []instanceInfo{}
		allInstances, err := store.ListAll()
		if err == nil {
			visibleDefaults := map[string]string{}
			if !isAdmin {
				visibleDefaults = effectiveUserInstanceIDs(allInstances, overrides)
			}
			for _, inst := range allInstances {
				if !isAdmin && visibleDefaults[inst.ServiceType] != inst.ID {
					continue
				}
				// A requester's filtered entry is always its effective default,
				// including the deterministic first-instance fallback when no row
				// carries the global is_default flag. Admins retain the configured
				// global flag unless their own per-user override selects a sibling.
				isDefault := inst.IsDefault
				if !isAdmin {
					isDefault = visibleDefaults[inst.ServiceType] == inst.ID
				} else if pinned, ok := overrides[inst.ServiceType]; ok {
					isDefault = pinned == inst.ID
				}
				instances = append(instances, instanceInfo{
					ID:          inst.ID,
					ServiceType: inst.ServiceType,
					Name:        inst.Name,
					IsDefault:   isDefault,
				})
			}
		}

		// Derive service availability from the per-user filtered instance list,
		// so a user without a chaptarr grant sees services.chaptarr == false.
		aiAvailable := creds.IsAIConfigured()
		if aiHandler != nil && userID != 0 {
			aiAvailable = aiHandler.AvailableForUser(userID)
		}
		services := map[string]bool{
			"radarr":   false,
			"sonarr":   false,
			"chaptarr": false,
			"ai":       aiAvailable,
			"tmdb":     creds.IsConfigured(credentials.KeyTMDBAccessToken),
			"trakt":    creds.IsConfigured(credentials.KeyTraktClientID),
		}
		for _, inst := range instances {
			switch inst.ServiceType {
			case "radarr":
				services["radarr"] = true
			case "sonarr":
				services["sonarr"] = true
			case "chaptarr":
				services["chaptarr"] = true
			}
		}

		// Remediation toggles so non-admin clients know whether to surface the
		// "Report a problem" affordance: issues_enabled is the master switch,
		// allow_reporting the user-facing affordance toggle.
		remSettings := remediationService.Settings()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"server_name":     cfg.ServerName,
			"version":         version.Version,
			"services":        services,
			"instances":       instances,
			"issues_enabled":  remSettings.Enabled,
			"allow_reporting": remSettings.AllowReporting,
		})
	}
}

func effectiveUserInstanceIDs(instances []instance.Instance, overrides map[string]string) map[string]string {
	first := map[string]string{}
	globalDefault := map[string]string{}
	for _, inst := range instances {
		switch inst.ServiceType {
		case "radarr", "sonarr":
			if _, ok := first[inst.ServiceType]; !ok {
				first[inst.ServiceType] = inst.ID
			}
			if inst.IsDefault {
				if _, ok := globalDefault[inst.ServiceType]; !ok {
					globalDefault[inst.ServiceType] = inst.ID
				}
			}
		}
	}

	visible := map[string]string{}
	for _, serviceType := range []string{"radarr", "sonarr"} {
		if override, ok := overrides[serviceType]; ok {
			visible[serviceType] = override
			continue
		}
		if id, ok := globalDefault[serviceType]; ok {
			visible[serviceType] = id
			continue
		}
		if id, ok := first[serviceType]; ok {
			visible[serviceType] = id
		}
	}
	if chaptarrID, ok := overrides["chaptarr"]; ok {
		visible["chaptarr"] = chaptarrID
	}
	return visible
}
