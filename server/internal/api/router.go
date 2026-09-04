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
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/discover"
	"github.com/windoze95/cantinarr-server/internal/downloads"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/mcpserver"
	"github.com/windoze95/cantinarr-server/internal/mediaaccess"
	"github.com/windoze95/cantinarr-server/internal/mediafiles"
	"github.com/windoze95/cantinarr-server/internal/proxy"
	"github.com/windoze95/cantinarr-server/internal/push"
	"github.com/windoze95/cantinarr-server/internal/remediation"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
	"github.com/windoze95/cantinarr-server/internal/update"
	"github.com/windoze95/cantinarr-server/internal/version"
	"github.com/windoze95/cantinarr-server/internal/watchhistory"
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
	mediaFilesHandler *mediafiles.Handler,
	watchHistoryHandler *watchhistory.Handler,
	creds *credentials.Registry,
	credHandler *credentials.Handler,
	toolServer *mcp.ToolServer,
	pushHandler *push.Handler,
	webhookHandler *webhooks.Handler,
	mediaAccessHandler *mediaaccess.Handler,
	updateChecker *update.Checker,
	serverSettings *serversettings.Service,
	contentPolicyHandler *contentpolicy.Handler,
) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(safeRequestLogger)
	r.Use(middleware.Recoverer)

	oauthHandler := auth.NewOAuthHandler(authService, cfg.OAuthIssuer)
	r.Get("/.well-known/oauth-protected-resource", oauthHandler.ProtectedResourceMetadata)
	r.Get("/.well-known/oauth-protected-resource/mcp", oauthHandler.ProtectedResourceMetadata)
	r.Get("/.well-known/oauth-authorization-server", oauthHandler.AuthorizationServerMetadata)
	r.Get("/.well-known/openid-configuration", oauthHandler.AuthorizationServerMetadata)
	r.Get("/.well-known/apple-app-site-association", appleAppSiteAssociationHandler(cfg))
	r.Get("/.well-known/assetlinks.json", androidAssetLinksHandler(cfg))
	// Rate limiter for the unauthenticated OAuth endpoints, matching the
	// /api/auth posture. POST /oauth/authorize accepts a password, so leaving
	// it unlimited would hand brute-forcers a second, uncapped login form; the
	// register/token/passkey endpoints share the budget so unauthenticated
	// callers cannot spam client registrations or grind at grants either. The
	// metadata endpoints above and the GET authorize form stay unlimited —
	// they attempt no credential and MCP client discovery depends on them.
	oauthLimiter := auth.NewRateLimiter(10, 1*time.Minute)
	r.With(oauthLimiter.Middleware).Post("/oauth/register", oauthHandler.RegisterClient)
	r.Get("/oauth/authorize", oauthHandler.Authorize)
	r.With(oauthLimiter.Middleware).Post("/oauth/authorize", oauthHandler.Authorize)
	r.With(oauthLimiter.Middleware).Post("/oauth/passkey/login/begin", oauthHandler.BeginOAuthPasskeyLogin)
	r.With(oauthLimiter.Middleware).Post("/oauth/passkey/login/finish", oauthHandler.FinishOAuthPasskeyLogin)
	r.With(oauthLimiter.Middleware).Post("/oauth/token", oauthHandler.Token)
	r.Get("/passkeys/setup", oauthHandler.PasskeySetup)
	r.Get("/passkeys/create", oauthHandler.PasskeyCreate)

	r.Route("/api", func(r chi.Router) {
		// CORS: same-origin only. No CORS middleware is mounted on purpose —
		// the web build is served from this same origin, native apps and MCP
		// clients don't use CORS, and emitting no Access-Control-* headers
		// leaves the browser's default same-origin policy in force. (The /mcp
		// mount below has a separate configured-origin policy for browser MCP
		// clients.) Previously an empty go-chi/cors allowlist sat here, which
		// that library treats as allow-all — the opposite of this intent.
		r.Use(middleware.SetHeader("Content-Type", "application/json"))

		// WebSocket (auth handled via subprotocol header)
		r.Get("/ws", wsHub.ServeWS)

		// Health check
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		})

		// Media bytes use a short-lived opaque capability in the path so browser
		// downloads and Range resumes need not expose the session JWT. Ticket
		// issuance below is authenticated; an unknown or expired capability is a
		// generic 404. HEAD is explicit because browsers commonly probe first.
		r.Get("/media-files/download/{ticket}", mediaFilesHandler.Download)
		r.Head("/media-files/download/{ticket}", mediaFilesHandler.Download)

		// Arr webhook receiver (Sonarr/Radarr → Connect → Webhook). No session:
		// the server-only per-instance credential is supplied through Basic Auth;
		// query-string credentials are rejected so access logs cannot retain them.
		r.Post("/webhooks/arr/{instanceID}", webhookHandler.HandleArr)

		// Rate limiter for public auth endpoints: 10 requests per minute per IP
		authLimiter := auth.NewRateLimiter(10, 1*time.Minute)
		// Keep authenticated ChatGPT/xAI device-flow churn from consuming the
		// public password/passkey budget for everyone behind the same household
		// proxy. Both OAuth providers share this begin-login budget.
		oauthLoginLimiter := auth.NewRateLimiter(10, 1*time.Minute)

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

				// Sign out: revoke the calling device's own session.
				r.Post("/logout", authHandler.HandleLogout)
			})
		})

		// Admin routes
		r.Route("/admin", func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Post("/connect-token", authHandler.HandleCreateConnectToken)
			// The origin invite/passkey links are built from. Lives beside
			// connect-token because that is the surface it exists for.
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/external-address", externalAddressHandler(serverSettings))
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Put("/external-address", updateExternalAddressHandler(serverSettings))
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/devices", authHandler.HandleListDevices)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Delete("/devices/{deviceID}", authHandler.HandleRevokeDevice)

			// User management
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/users", authHandler.HandleListUsers)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Patch("/users/{userID}", authHandler.HandleUpdateUserRole)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Patch("/users/{userID}/auth-methods", authHandler.HandleUpdateUserAuthMethods)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Put("/users/{userID}/ai-access", authHandler.HandleUpdateUserAIAccess)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Delete("/users/{userID}", authHandler.HandleDeleteUser)
			// Kids accounts: the per-user content policy and the rating
			// schemes the editor offers. The policy is enforced server-side
			// on every title surface (internal/contentpolicy).
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/users/{userID}/content-policy", contentPolicyHandler.GetUserPolicy)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Put("/users/{userID}/content-policy", contentPolicyHandler.PutUserPolicy)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Delete("/users/{userID}/content-policy", contentPolicyHandler.DeleteUserPolicy)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/certifications", contentPolicyHandler.Certifications)
			// Send a test push to a specific user's devices (delivery diagnostics).
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Post("/users/{userID}/test-push", pushHandler.TestPushToUser)

			// Setup checklist: which features are configured, derived live on
			// every request (drives the app's setup wizard + reminders).
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Get("/setup-status", setupStatusHandler(cfg, instanceStore, creds, aiHandler, serverSettings, remediationService))
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Put("/setup-status/skips", setupSkipHandler(serverSettings))

			// Update availability + the admin-configured management-portal URL the
			// app's version warnings link to. GET returns both; PUT sets the
			// management URL.
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Get("/update-status", updateStatusHandler(updateChecker, serverSettings))
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Put("/update-status", updateServerSettingsHandler(updateChecker, serverSettings))

			// Which feed backs the headline discovery rows, and whether those
			// rows drop non-English originals.
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Get("/discovery-settings", discoverySettingsHandler(serverSettings, creds))
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Put("/discovery-settings", updateDiscoverySettingsHandler(serverSettings, creds))

			// Media-server accounts (Jellyfin, Emby, Plex): the linked-account
			// rows the Users screen tags, the server's own account list for the
			// link picker, link/unlink, and the import that turns picked
			// accounts into granted, linked Cantinarr users. Access itself is
			// the instance grant.
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/media-servers/accounts", mediaAccessHandler.ListAccounts)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/media-servers/{instanceID}/users", mediaAccessHandler.RemoteUsers)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Post("/media-servers/{instanceID}/import", mediaAccessHandler.Import)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Put("/users/{userID}/media-servers/{instanceID}/account", mediaAccessHandler.LinkAccount)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Delete("/users/{userID}/media-servers/{instanceID}/account", mediaAccessHandler.UnlinkAccount)

			// Per-user default *arr instance overrides (admin-managed). Pins which
			// instance is a given user's default source per service type, and —
			// for service types with no global default (chaptarr) — grants the
			// user access to that instance.
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/users/{userID}/default-instances", instanceHandler.GetUserDefaultInstances)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Put("/users/{userID}/default-instances", instanceHandler.UpdateUserDefaultInstances)

			// Per-user instance access grants (admin-managed). Additive to the
			// default: a granted instance appears alongside the user's default
			// so they can choose a library per request.
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Get("/users/{userID}/instance-grants", instanceHandler.GetUserInstanceGrants)
			r.With(auth.RequirePermission(auth.PermissionUsersManage)).Put("/users/{userID}/instance-grants", instanceHandler.UpdateUserInstanceGrants)

			// Credential management
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Get("/credentials", credHandler.Get)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Put("/credentials", credHandler.Update)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Delete("/credentials/{key}", credHandler.Delete)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Get("/ai/codex/status", aiHandler.SharedCodexStatus)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage), oauthLoginLimiter.Middleware).Post("/ai/codex/device/begin", aiHandler.BeginSharedCodexDeviceLogin)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Get("/ai/codex/device/{flowID}", aiHandler.CheckSharedCodexDeviceLogin)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Delete("/ai/codex/device/{flowID}", aiHandler.CancelSharedCodexDeviceLogin)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Delete("/ai/codex", aiHandler.UnlinkSharedCodex)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Get("/ai/grok/status", aiHandler.SharedGrokStatus)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage), oauthLoginLimiter.Middleware).Post("/ai/grok/device/begin", aiHandler.BeginSharedGrokDeviceLogin)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Get("/ai/grok/device/{flowID}", aiHandler.CheckSharedGrokDeviceLogin)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Delete("/ai/grok/device/{flowID}", aiHandler.CancelSharedGrokDeviceLogin)
			r.With(auth.RequirePermission(auth.PermissionCredentialsManage)).Delete("/ai/grok", aiHandler.UnlinkSharedGrok)

			// AI tool toggles
			aiToolsHandler := mcp.NewToolSettingsHandler(toolServer)
			r.With(auth.RequirePermission(auth.PermissionAIToolsManage)).Get("/ai-tools", aiToolsHandler.List)
			r.With(auth.RequirePermission(auth.PermissionAIToolsManage)).Put("/ai-tools/debug", aiToolsHandler.UpdateDebug)
			r.With(auth.RequirePermission(auth.PermissionAIToolsManage)).Put("/ai-tools/{name}", aiToolsHandler.Update)

			// Append-only history for AI-driven external settings changes. Detail
			// performs a live comparison; revert refuses drift and appends a linked
			// inverse record instead of editing history.
			settingsChangesHandler := mcp.NewSettingsChangeHandler(toolServer)
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Get("/external-settings-changes", settingsChangesHandler.List)
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Get("/external-settings-changes/{id}", settingsChangesHandler.Get)
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Post("/external-settings-changes/{id}/revert", settingsChangesHandler.Revert)

			// Profile changes parked by external MCP agents: an admin reviews
			// the stored diff here and approves (which re-validates live state
			// and executes the same verified write path as the in-app apply)
			// or rejects without touching the arr.
			profileProposalsHandler := mcp.NewProfileProposalHandler(toolServer)
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Get("/profile-change-proposals", profileProposalsHandler.List)
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Get("/profile-change-proposals/{id}", profileProposalsHandler.Get)
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Post("/profile-change-proposals/{id}/approve", profileProposalsHandler.Approve)
			r.With(auth.RequirePermission(auth.PermissionInstancesManage)).Post("/profile-change-proposals/{id}/reject", profileProposalsHandler.Reject)

			// Media request management: approval queue + request defaults
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Get("/requests", requestHandler.ListPending)
			// Static segment, so it wins over /requests/{id}/… in chi's router.
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Get("/requests/waiting", requestHandler.ListWaiting)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Post("/requests/{id}/approve", requestHandler.Approve)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Post("/requests/{id}/deny", requestHandler.Deny)
			// "Try again" on a demoted author-import book request: resume the
			// watch instead of deciding it.
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Post("/requests/{id}/wait", requestHandler.Wait)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Get("/request-settings", requestHandler.GetSettings)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Put("/request-settings", requestHandler.UpdateSettings)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Get("/users/{userID}/request-settings", requestHandler.GetUserSettings)
			r.With(auth.RequirePermission(auth.PermissionRequestsManage)).Put("/users/{userID}/request-settings", requestHandler.UpdateUserSettings)

			// AI remediation: issue queue + dismissal + global settings (Wave 1).
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/issues", remediationHandler.ListAdmin)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/issues/{id}/dismiss", remediationHandler.Dismiss)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/issues/{id}/resolve", remediationHandler.ResolveIssue)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/issues/{id}/activity", remediationHandler.GetIssueActivity)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/agent-digest", remediationHandler.Digest)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/agent-approval-rules/candidates", remediationHandler.ListRuleCandidates)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/agent-approval-rules", remediationHandler.ArmRule)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/remediation-settings", remediationHandler.GetSettings)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Put("/remediation-settings", remediationHandler.UpdateSettings)

			// AI remediation: agent-action approval queue + run audit (Wave 3 —
			// propose→approve→execute). Approval claims a stored proposal for
			// at-most-once dispatch; denial resumes the investigation.
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/agent-actions", remediationHandler.ListActions)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/agent-actions/{id}", remediationHandler.GetAction)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/agent-actions/{id}/approve", remediationHandler.ApproveAction)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/agent-actions/approve-batch", remediationHandler.BatchApproveActions)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/agent-actions/{id}/deny", remediationHandler.DenyAction)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/agent-runs/{id}", remediationHandler.GetRun)

			// Standing auto-approval rules: admin-authored (problem, fix,
			// facet) pairs the sweep may approve without paging. Armed only
			// from an explicit "remember" on a reviewed approval; paused
			// automatically on the first failed outcome.
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Get("/agent-approval-rules", remediationHandler.ListApprovalRules)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/agent-approval-rules/{id}/pause", remediationHandler.PauseApprovalRule)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Post("/agent-approval-rules/{id}/resume", remediationHandler.ResumeApprovalRule)
			r.With(auth.RequirePermission(auth.PermissionRemediationManage)).Delete("/agent-approval-rules/{id}", remediationHandler.DeleteApprovalRule)
		})

		// Config route (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Get("/config", configHandler(cfg, instanceStore, creds, aiHandler, remediationService))
		})

		// Media-server accounts (authenticated, self-scoped): a granted user
		// sees their media servers, asks where a title can be watched on them,
		// creates their own account there, or links one that is already
		// theirs (a password check against the server, or a plex.tv
		// sign-in). The credential writes are rate-limited like
		// the other self-service ones; the sign-in poll is not, since the
		// app polls it every few seconds and a pin can only be polled by the
		// user who began it. Eligibility is checked inside, with one answer
		// for every "not for you" case.
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Get("/media-servers", mediaAccessHandler.List)
			r.Get("/media-servers/watch", mediaAccessHandler.Watch)
			r.With(authLimiter.Middleware).Post("/media-servers/{instanceID}/account", mediaAccessHandler.CreateAccount)
			r.With(authLimiter.Middleware).Post("/media-servers/{instanceID}/account/link", mediaAccessHandler.LinkOwnAccount)
			r.With(authLimiter.Middleware).Post("/media-servers/plex/sign-in/begin", mediaAccessHandler.PlexSignInBegin)
			r.Post("/media-servers/plex/sign-in/check", mediaAccessHandler.PlexSignInCheck)
		})

		// Completed-media ticket issuance (authenticated requester/admin). The
		// handler additionally enforces the requester's exact effective instance
		// and accepts only a live arr file ID, never a client-supplied path.
		r.With(
			authService.AuthMiddleware,
			auth.RequirePermission(auth.PermissionMediaDownload),
		).Post("/media-files/tickets", mediaFilesHandler.IssueTicket)

		// Batch lexical check of arr-reported paths against the instance's
		// mappings so clients can hide download affordances that could never
		// succeed. Never touches the filesystem.
		r.With(
			authService.AuthMiddleware,
			auth.RequirePermission(auth.PermissionMediaDownload),
		).Post("/media-files/coverage", mediaFilesHandler.Coverage)

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
			r.Get("/requests/book-recent", requestHandler.GetBookRecent)
			r.Get("/requests/book-authors", requestHandler.GetBookAuthors)
			r.Get("/requests/book-author", requestHandler.GetBookAuthor)
			r.Get("/requests/book-series", requestHandler.GetBookSeries)
			r.Get("/requests/book-series-detail", requestHandler.GetBookSeriesDetail)
			r.Get("/requests/music-status", requestHandler.GetMusicStatus)
			r.Get("/requests/music-library", requestHandler.GetMusicLibrary)
			r.Get("/requests/music-recent", requestHandler.GetMusicRecent)
			r.Get("/requests/music-artists", requestHandler.GetMusicArtists)
			r.Get("/requests/music-artist", requestHandler.GetMusicArtist)
			r.Get("/requests/{tmdb_id}/status", requestHandler.GetStatus)
		})

		// Issue reporting (authenticated). Filing an issue needs the same
		// permission as requesting media; viewing/replying to a single issue is
		// gated in-handler to the issue's reporter or an admin.
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.With(auth.RequirePermission(auth.PermissionMediaRequest)).Post("/issues", remediationHandler.Create)
			r.Get("/issues", remediationHandler.ListMine)
			r.Get("/issues/{id}", remediationHandler.Get)
			r.Post("/issues/{id}/reply", remediationHandler.Reply)
			r.Post("/issues/{id}/confirm-fixed", remediationHandler.ConfirmFixed)
		})

		// Discover / media routes (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Use(auth.RequirePermission(auth.PermissionMediaDiscover))

			// Discover
			r.Get("/discover/trending", discoverHandler.Trending)
			r.Get("/discover/movies/popular", discoverHandler.PopularMovies)
			r.Get("/discover/tv/popular", discoverHandler.PopularTV)
			// The headline rows: whichever feed the admin configured, in one shape.
			r.Get("/discover/movies/featured", discoverHandler.FeaturedMovies)
			r.Get("/discover/tv/featured", discoverHandler.FeaturedTV)
			r.Get("/discover/movies/top-rated", discoverHandler.TopRatedMovies)
			r.Get("/discover/movies/upcoming", discoverHandler.UpcomingMovies)
			r.Get("/discover/movies/now-playing", discoverHandler.NowPlayingMovies)
			r.Get("/discover/tv/on-the-air", discoverHandler.OnTheAirTV)
			r.Get("/discover/tv/top-rated", discoverHandler.TopRatedTV)
			r.Get("/discover/tv/upcoming", discoverHandler.UpcomingTV)
			r.Get("/discover/movies", discoverHandler.DiscoverMovies)
			r.Get("/discover/tv", discoverHandler.DiscoverTV)

			// Search
			r.Get("/search", discoverHandler.Search)
			r.Get("/search/keyword", discoverHandler.SearchKeywords)
			r.Get("/search/company", discoverHandler.SearchCompanies)

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
			r.Get("/providers/tv", discoverHandler.TVWatchProviders)
			r.Get("/providers/regions", discoverHandler.WatchProviderRegions)
			r.Get("/languages", discoverHandler.Languages)

			// Trakt
			r.Get("/trakt/trending", discoverHandler.TraktTrending)
			r.Get("/trakt/popular", discoverHandler.TraktPopular)
			r.Get("/trakt/lists", discoverHandler.TraktPopularLists)
			r.Get("/trakt/lists/{user}/{slug}/items", discoverHandler.TraktListItems)
			r.Get("/trakt/calendar", discoverHandler.TraktCalendar)
			r.Get("/trakt/anticipated", discoverHandler.TraktAnticipated)
			r.Get("/trakt/recommendations", discoverHandler.TraktRecommendations)
			// Trakt artwork relay: Trakt's image CDNs send no CORS headers, so
			// the web client fetches Trakt posters through this same-origin
			// path instead of the CDN. Host validation lives in the handler.
			r.Get("/trakt/images/{host}/*", discoverHandler.TraktImage)
		})

		// AI routes (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)

			// Account visibility and revocation remain available to the account
			// owner even if their role later loses AI access.
			r.Get("/ai/codex/status", aiHandler.CodexStatus)
			r.Delete("/ai/codex", aiHandler.UnlinkCodex)
			r.Delete("/ai/codex/device/{flowID}", aiHandler.CancelCodexDeviceLogin)
			r.Get("/ai/grok/status", aiHandler.GrokStatus)
			r.Delete("/ai/grok", aiHandler.UnlinkGrok)
			r.Delete("/ai/grok/device/{flowID}", aiHandler.CancelGrokDeviceLogin)
			r.Get("/ai/settings", aiHandler.AISettings)
			r.Delete("/ai/settings", aiHandler.DeleteAISettings)
			r.Delete("/ai/credentials/{provider}", aiHandler.DeletePersonalAICredential)

			r.Group(func(r chi.Router) {
				r.Use(auth.RequirePermission(auth.PermissionAIChat))
				r.Post("/ai/chat", aiHandler.Chat)
				r.Get("/ai/available", aiHandler.Available)
				r.Put("/ai/settings", aiHandler.UpdateAISettings)
				r.Put("/ai/credentials/{provider}", aiHandler.UpdatePersonalAICredential)
				r.With(oauthLoginLimiter.Middleware).Post("/ai/codex/device/begin", aiHandler.BeginCodexDeviceLogin)
				r.Get("/ai/codex/device/{flowID}", aiHandler.CheckCodexDeviceLogin)
				r.With(oauthLoginLimiter.Middleware).Post("/ai/grok/device/begin", aiHandler.BeginGrokDeviceLogin)
				r.Get("/ai/grok/device/{flowID}", aiHandler.CheckGrokDeviceLogin)
			})
		})

		// Instance routes (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)

			// Instance CRUD — admin only
			r.Group(func(r chi.Router) {
				r.Use(auth.RequirePermission(auth.PermissionInstancesManage))
				r.Get("/instances", instanceHandler.List)
				r.Get("/instances/media-roots", instanceHandler.MediaRoots)
				r.Post("/instances", instanceHandler.Create)
				// Dry-run connectivity check from the server — the host that
				// actually dials instance URLs — so cluster-internal names the
				// admin's device cannot resolve still test truthfully.
				r.Post("/instances/test", instanceHandler.TestConnection)
				// Libraries a media server reports, for the shared-libraries
				// picker; same candidate body and credential fallback as /test.
				r.Post("/instances/media-server/libraries", instanceHandler.MediaServerLibraries)
				// Plex: the PIN link that yields an instance's token (held
				// server-side, referenced by pin id on save) and the linked
				// account's owned servers for the editor's server picker.
				r.Post("/instances/plex/link/begin", instanceHandler.PlexLinkBegin)
				r.Post("/instances/plex/link/check", instanceHandler.PlexLinkCheck)
				r.Post("/instances/plex/servers", instanceHandler.PlexServers)
				r.Put("/instances/{instanceID}", instanceHandler.Update)
				r.Delete("/instances/{instanceID}", instanceHandler.Delete)
				// Instance-centric view of user_default_instances (the static
				// "users" segment wins over the proxy wildcard below): which
				// users are pinned to which instance of this instance's service
				// type, and (PUT) assign this instance to an exact set of users.
				r.Get("/instances/{instanceID}/users", instanceHandler.GetInstanceUsers)
				r.Put("/instances/{instanceID}/users", instanceHandler.UpdateInstanceUsers)
				// Instance-centric view of user_instance_grants: which users
				// hold an access grant on which instance of this service type,
				// and (PUT) grant this instance to an exact set of users
				// without moving anyone's default.
				r.Get("/instances/{instanceID}/grant-users", instanceHandler.GetInstanceGrantUsers)
				r.Put("/instances/{instanceID}/grant-users", instanceHandler.UpdateInstanceGrantUsers)
				// Configure the server-managed Radarr/Sonarr Connect webhook
				// without ever returning its callback credential to the app.
				r.Post("/instances/{instanceID}/webhook", instanceHandler.ConfigureWebhook)
				// Live webhook state, derived from the arr itself on every
				// call — a stored flag would drift the moment an admin edited
				// the arr's Connect list.
				r.Get("/instances/{instanceID}/webhook", instanceHandler.WebhookStatus)
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

		// Watch-history (Tautulli, Tracearr) routes (admin only). The
		// /api/tautulli prefix predates Tracearr and stays mounted on the same
		// handler so older apps keep working; new clients use /api/watch-history.
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Use(auth.RequirePermission(auth.PermissionMonitoringRead))

			for _, prefix := range []string{"/watch-history", "/tautulli"} {
				r.Get(prefix+"/{instanceID}/activity", watchHistoryHandler.GetActivity)
				r.Get(prefix+"/{instanceID}/history", watchHistoryHandler.GetHistory)
				r.Get(prefix+"/{instanceID}/stats", watchHistoryHandler.GetStats)
			}
		})

	})

	// MCP endpoint (authenticated, separate CORS for external MCP clients)
	mcpHandler := mcpserver.NewMCPHandler(toolServer)
	r.Route("/mcp", func(r chi.Router) {
		r.Use(requireValidMCPOrigin(cfg))
		r.Use(cors.Handler(cors.Options{
			AllowedMethods: []string{"GET", "POST", "DELETE", "OPTIONS"},
			AllowedHeaders: []string{
				"Authorization",
				"Content-Type",
				"Mcp-Method",
				"Mcp-Name",
				"MCP-Protocol-Version",
				"Mcp-Session-Id",
			},
			AllowOriginFunc: func(_ *http.Request, origin string) bool {
				return mcpOriginAllowed(cfg, origin)
			},
			ExposedHeaders:   []string{"Mcp-Session-Id"},
			AllowCredentials: false,
		}))
		r.Use(oauthHandler.MCPAuthMiddleware)
		r.Use(auth.RequirePermission(auth.PermissionMCPAccess))
		r.Use(mcpRequestObserver)
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
	VisibleInstanceIDs(userID int64, serviceType string) ([]string, error)
	EffectiveDefaultInstanceID(userID int64, serviceType string) (string, error)
}

func configHandler(cfg *config.Config, store configInstanceStore, creds *credentials.Registry, aiHandler *ai.Handler, remediationService *remediation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// Build instances list
		type instanceInfo struct {
			ID             string `json:"id"`
			ServiceType    string `json:"service_type"`
			Name           string `json:"name"`
			IsDefault      bool   `json:"is_default"`
			MediaDownloads bool   `json:"media_downloads"`
		}

		// The config payload is per-user: admins see every instance, while
		// regular users see their granted Radarr/Sonarr/Chaptarr set — every
		// access-granted instance plus their effective default (the global
		// default when nothing was granted; for chaptarr only explicit rows,
		// never a fallback). is_default is rewritten per user to mark THEIR
		// effective default, which is how older clients that expect a single
		// instance keep picking the right one.
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
		visible := map[string]map[string]bool{}
		visibleDefault := map[string]string{}
		if !isAdmin {
			// Media servers are listed too so a granted user's app can offer
			// the account guide; their grant-only rules make the visible set
			// exactly the grants (see EffectiveDefaultInstanceID).
			for _, serviceType := range append([]string{"radarr", "sonarr", "chaptarr", "lidarr"}, instance.MediaServerTypes()...) {
				visibleIDs, err := store.VisibleInstanceIDs(userID, serviceType)
				if err != nil {
					http.Error(w, `{"error":"temporarily unavailable, retry shortly"}`, http.StatusServiceUnavailable)
					return
				}
				defaultID, err := store.EffectiveDefaultInstanceID(userID, serviceType)
				if err != nil {
					http.Error(w, `{"error":"temporarily unavailable, retry shortly"}`, http.StatusServiceUnavailable)
					return
				}
				ids := map[string]bool{}
				for _, id := range visibleIDs {
					ids[id] = true
				}
				visible[serviceType] = ids
				visibleDefault[serviceType] = defaultID
			}
		}

		instances := []instanceInfo{}
		// plexRequestable tells a user who holds no Plex grant that there is a
		// Plex server to ask for: the guide offers "share your Plex email" and
		// admins hear about it. Nothing else about the instance is revealed.
		plexRequestable := false
		allInstances, err := store.ListAll()
		if err == nil {
			for _, inst := range allInstances {
				if inst.ServiceType == "plex" {
					plexRequestable = true
				}
				if !isAdmin && !visible[inst.ServiceType][inst.ID] {
					continue
				}
				// A requester's is_default always marks their effective
				// default, including the deterministic first-instance fallback
				// when no row carries the global flag. Admins retain the
				// configured global flag unless their own per-user override
				// selects a sibling.
				isDefault := inst.IsDefault
				if !isAdmin {
					isDefault = visibleDefault[inst.ServiceType] == inst.ID
				} else if pinned, ok := overrides[inst.ServiceType]; ok {
					isDefault = pinned == inst.ID
				}
				instances = append(instances, instanceInfo{
					ID:             inst.ID,
					ServiceType:    inst.ServiceType,
					Name:           inst.Name,
					IsDefault:      isDefault,
					MediaDownloads: inst.MediaDownloadsConfigured(cfg.MediaDownloadRoots),
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
			"radarr":          false,
			"sonarr":          false,
			"chaptarr":        false,
			"lidarr":          false,
			"media_downloads": false,
			"ai":              aiAvailable,
			"tmdb":            creds.TMDBAvailable(),
			"trakt":           creds.TraktAvailable(),
		}
		for _, inst := range instances {
			if inst.MediaDownloads {
				services["media_downloads"] = true
			}
			switch inst.ServiceType {
			case "radarr":
				services["radarr"] = true
			case "sonarr":
				services["sonarr"] = true
			case "chaptarr":
				services["chaptarr"] = true
			case "lidarr":
				services["lidarr"] = true
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
			"min_app_version": version.MinAppVersion,
			"services":        services,
			"instances":       instances,
			"issues_enabled":  remSettings.Enabled,
			"allow_reporting": remSettings.AllowReporting,
			// True when a Plex server exists at all, so a user without the
			// grant can still ask for access from the guide.
			"plex_access_requestable": plexRequestable,
		})
	}
}
