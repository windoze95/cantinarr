# Cantinarr Server

The backend brain for [Cantinarr](https://github.com/windoze95/cantinarr) -- the self-hosted server behind its discovery, request, management, and repair loop.

A single Go binary that bridges your arr stack, serves the web UI, and keeps API keys off user devices. Drop it on your NAS, point it at Radarr/Sonarr (and Chaptarr for books), and generate connect links for family and friends.

```
                     Cantinarr Server (:8585)
  ┌───────────────────────────────────────────────────────────┐
  │  Auth (JWT/passkeys)   Requests + Approvals   AI Chat     │
  │        │                     │                   │        │
  │        │              ┌──────┴──────┐      33 AI Tools    │
  │        │              │  ID Bridge  │            │        │
  │        │              └──┬───────┬──┘     AI Remediation  │
  │        │                 │       │            Agent       │
  │   Instance registry ── Radarr  Sonarr  Chaptarr           │
  │        │                 ▲       ▲                        │
  │   Scrubbed arr proxy ────┘       └──── Webhook receiver   │
  │                                                           │
  │  Downloads (SAB/qBit/NZBGet/Transmission)   Tautulli      │
  │  WebSocket hub          Push gateway client               │
  │  TMDB/Trakt discover proxy      Flutter web (embedded)    │
  └───────────────────────────────────────────────────────────┘
```

## Features

- **One-tap requests with an approval queue** -- Users browse and tap request; the server handles ID bridging, arr lookups, quality profiles, and root folders. Admins can require approval globally or per user, and per user allow season-level choice, quality choice, and per-service default quality profiles.
- **Books via Chaptarr** -- A Readarr-API (v1) books module with per-format (ebook/audiobook) monitoring, requesting, and library awareness. Chaptarr access is granted per user by an admin.
- **Completed-media downloads** -- Opt-in delivery of ebook, audiobook, movie, and episode files from read-only library mounts. Deployment roots form the outer filesystem allowlist; per-instance path mappings translate each arr namespace into that boundary. The server accepts only live arr file IDs and streams through short-lived file-scoped links with HEAD and Range support.
- **Automatic ID bridging** -- Transparently translates TMDB IDs to TVDB IDs for Sonarr. Falls back to Trakt cross-references, then title+year search. Results cached in SQLite for 30 days.
- **Availability computed live** -- Request status is derived from the arrs' real episode/file state (never from a stale snapshot or monitored-only stats), refreshed by queue polling and instant arr webhooks.
- **Connect link auth, passwordless by default** -- Admins generate connect links; redeeming one starts a permanent device session (an opaque refresh token validated against the DB -- never expires, never rotates, independent of the JWT secret) that mints 15-minute access JWTs. Sessions end only by device revocation or user deletion. Passwords and passkeys (WebAuthn, incl. native iOS/Android/Windows) are admin-gated per user.
- **AI assistant + remediation agent** -- Interactive chat resolves a personal Anthropic/OpenAI/Gemini key or OpenAI (OAuth) link first; that personal choice works without an included-access grant and need not match the server provider. An admin-funded provider is available only to users granted included access. A selected personal provider fails closed instead of silently consuming the shared account. The autonomous investigation agent is server-owned, always uses the admin shared API key or shared OpenAI OAuth connection without consulting user grants, may use a separately tested remediation model designation, and proposes fixes an admin approves.
- **MCP server** -- 31 of the 33 in-app AI tools are exposed at `/mcp` (Streamable HTTP) with full inbound Cantinarr OAuth: discovery metadata, dynamic client registration, PKCE, browser/passkey login, rotating refresh tokens. Admin settings tools inspect quality profiles and import or update native/TRaSH custom formats on Radarr, Sonarr, and Chaptarr. Profile preview/apply stays in-app-only because its one-use, same-turn handoff depends on authenticated in-app chat provenance. After an explicit admin request, the assistant can preview and apply autonomously within that turn. AI/MCP profile and custom-format writes are recorded in durable configuration history with live comparison; one-time guarded restore is limited to applied quality-profile updates. This is separate from the outbound OpenAI OAuth account link used by Codex chat.
- **Import Doctor** -- Plain-English diagnosis of stuck downloads with one-click fixes (manual/force import, remove+blocklist+re-search, category hand-off, rescan), shared by the app, the AI assistant, and MCP.
- **Push notifications** -- APNs delivery through a self-hosted push gateway with zero-config auto-enrollment, per-user preference categories, and admin-scoped alerts.
- **Real-time updates** -- WebSocket hub polls arr queues (30s) and download clients (15s) and pushes progress, queue snapshots, and change pings; arr webhooks make external changes (manual imports, deletes) land instantly.
- **Arr proxy** -- Read-only Radarr/Sonarr browsing for users, ordinary-method passthrough for admins (tunnel/reflection methods blocked for everyone), without exposing API keys.
- **Secrets encrypted at rest** -- Instance API keys/passwords, personal and shared AI credentials, OpenAI OAuth authorization, webhook tokens, and the JWT secret are AES-256-GCM encrypted in SQLite.
- **Flutter web embed** -- The web build ships inside the binary via `go:embed`. One container, one port, API + UI.
- **Single Alpine image** -- A static, no-CGO Go server plus the pinned Codex app-server helper, with one exposed port.

## Quick Start

### Docker Compose (recommended)

```yaml
services:
  cantinarr:
    image: ghcr.io/windoze95/cantinarr:latest
    ports:
      - "8585:8585"
    volumes:
      - ./config:/config
    environment:
      # Optional: enables push notifications (see Configuration)
      - CANTINARR_PUSH_GATEWAY_URL=https://push.julian.codes
    restart: unless-stopped
```

```bash
docker compose up -d
```

Open `http://your-server:8585` -- the setup wizard creates your admin account. Then configure API credentials and service instances from the admin UI. The admin may configure an included AI profile and grant it per user; every user may instead bring a personal provider under **Settings > AI Access**.

### From Source

```bash
# Requires Go 1.25+
cd server
go build -o cantinarr ./cmd/server
./cantinarr
```

## Configuration

Service credentials (TMDB, Trakt), the admin's included AI profile, and all service instances (Radarr, Sonarr, Chaptarr, SABnzbd, qBittorrent, NZBGet, Transmission, Tautulli) are managed through the admin UI. The included AI profile and each user's optional personal override can use Anthropic/OpenAI/Gemini API keys or OpenAI OAuth backed by a ChatGPT account. No environment variables are needed for credentials.

Optional env vars for deployment tuning (a `.env` file next to the binary is auto-loaded):

| Variable | Default | Description |
|---|---|---|
| `CANTINARR_PORT` | `8585` | HTTP listen port. Kubernetes service-link values (`tcp://…`) injected by a Service named `cantinarr` are ignored in favor of the default; set a numeric value to override |
| `CANTINARR_SERVER_NAME` | `Cantinarr` | Display name shown in clients |
| `CANTINARR_PUBLIC_URL` | direct request origin | Origin the Radarr/Sonarr containers POST webhooks back to, so it must be resolvable and reachable from the arrs themselves -- in same-network/cluster deployments a cluster-internal origin like `http://cantinarr:8585` is usually the right value. Set it explicitly behind a reverse proxy because forwarded host/protocol headers are deliberately ignored |
| `CANTINARR_OAUTH_ISSUER` | request-derived origin | Canonical external HTTPS origin for inbound MCP OAuth metadata, token audience, and browser-origin checks; setting it also enables stable RFC 9207 authorization-response `iss` and permits that origin to call `/mcp`. Set it behind a reverse proxy and keep it stable (changing it makes existing audience-bound MCP tokens reconnect); it is intentionally separate from the arr-reachable `CANTINARR_PUBLIC_URL` |
| `CANTINARR_MCP_ALLOWED_ORIGINS` | unset | Comma-separated additional browser origins allowed to call `/mcp`. If neither this nor `CANTINARR_OAUTH_ISSUER` is configured, requests that supply `Origin` are rejected; native and server-side clients omit `Origin` and need no entry |
| `CANTINARR_JWT_SECRET` | auto-generated | HMAC secret for signing short-lived access tokens (persisted encrypted when auto-generated). Opaque device-session refresh tokens do not depend on it, so changing it never signs devices out |
| `CANTINARR_ENCRYPTION_KEY` | auto-generated key file | Base64 32-byte key for secrets-at-rest (default: `/config/encryption.key`) |
| `CANTINARR_AI_PROVIDER` | `anthropic` | Fallback provider for the included server AI profile when none is saved in the admin UI (`anthropic`, `openai`, `gemini`, `codex`) |
| `CANTINARR_AI_MODEL` | provider default | Fallback model for the included server AI profile when none is saved in the admin UI |
| `CANTINARR_CODEX_BIN` | auto-discovered | Optional path to `codex-app-server` or the full `codex` CLI; official container images bundle the tested 0.144.3 app-server at `/usr/local/bin/codex-app-server` |
| `CANTINARR_CODEX_RUNTIME_DIR` | `/dev/shm/cantinarr-codex` | Absolute Linux tmpfs/ramfs directory used for server-owned, ephemeral per-session Codex state; if it already exists, it must be owned by the server user with mode `0700` |
| `CANTINARR_MEDIA_ROOTS` | unset | Comma-separated absolute server/container paths forming the outer filesystem allowlist for completed-media downloads. Empty disables downloads. Mount libraries read-only beneath these roots, then map each arr-reported prefix to a path inside them from that instance's settings; `/` and aliases of `/` are refused |
| `CANTINARR_PUSH_GATEWAY_URL` | unset | Push gateway origin; setting it **enables** push notifications |
| `CANTINARR_PUSH_API_KEY` | unset | Optional pinned gateway key -- leave blank and the server auto-enrolls on first start, persisting its issued key encrypted in the DB |
| `CANTINARR_PUSH_ENROLL_TOKEN` | unset | Shared enroll token, only for gateways with gated enrollment |
| `CANTINARR_APPLE_APP_IDS` | unset | Comma-separated `TeamID.BundleID` values served in `/.well-known/apple-app-site-association` for native Apple passkeys |
| `CANTINARR_ANDROID_PACKAGE_NAME` | `codes.julian.cantinarr` | Android package name for native passkeys |
| `CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS` | unset | Comma-separated Android signing cert SHA-256 fingerprints for `/.well-known/assetlinks.json` and Android WebAuthn origins |
| `CANTINARR_WEBAUTHN_EXTRA_ORIGINS` | unset | Additional WebAuthn origins to trust (e.g. non-standard HTTPS ports) |
| `CANTINARR_DISABLE_UPDATE_CHECK` | unset | Set to `1` to disable the periodic GitHub release check behind the admin "update available" banner |

OpenAI OAuth source deployments use Codex app-server and are supported only on Linux; non-Linux hosts report this provider unavailable even when a Codex binary is installed. The runtime directory's parent must exist, and the directory must be on tmpfs or ramfs—not persistent storage. Give each concurrently running Cantinarr process its own runtime directory; startup removes stale `session-*` entries from that dedicated root. The official container uses its private Docker `/dev/shm` tmpfs. Use the tested Codex 0.144.3 release or a protocol-compatible build.

The database lives at `/config/cantinarr.db` (SQLite, WAL mode). Keep the `/config` volume -- it holds the DB and the auto-generated encryption key, and encrypted secrets are unrecoverable without that key.

Native app passkeys require a public HTTPS domain associated with the app: Apple devices verify the AASA file, Android credential providers verify `assetlinks.json`. Browser passkey setup remains available when native association isn't possible.

## API Reference

Auth levels: **public**, **user** (any signed-in account), **admin**. Public auth endpoints are rate-limited to 10 requests/minute/IP. Internally, authorization is permission-based (`users:manage`, `requests:manage`, `remediation:manage`, `instances:manage`, `arr:browse`, ...) -- "admin" below means the admin role holds the required permission and the user role doesn't.

### Auth & account
```
GET    /api/auth/status                    # public: is setup complete, login methods
POST   /api/auth/setup                     # public: first-run admin account creation
POST   /api/auth/login                     # public: { username, password } -> tokens
POST   /api/auth/refresh                   # public: mint access token (refresh token is stable; legacy JWT refresh tokens migrate to the opaque scheme; 401 only on genuine revocation, 503 on transient faults)
POST   /api/auth/connect                   # public: redeem a connect-link token -> session
POST   /api/auth/passkey/login/begin|finish   # public: WebAuthn login
POST   /api/auth/passkey/setup/begin|finish   # public: passkey setup via short-lived link
GET    /api/auth/me                        # user: profile + permissions
POST   /api/auth/password                  # user: set password (admin-enabled users only)
POST   /api/auth/plex-email                # user: share the email for a Plex invite (notifies admins on change)
POST   /api/auth/passkey/register/begin|finish  # user: add passkey (admin-enabled only)
POST   /api/auth/passkey/setup-link        # user: mint a browser passkey-setup URL
GET    /api/auth/passkeys                  # user: list own passkeys
DELETE /api/auth/passkeys/{credentialID}   # user: remove own passkey
```

### Users, devices & connect links (admin)
```
POST   /api/admin/connect-token            # mint a connect link for a user
GET    /api/admin/devices                  # all connected devices
DELETE /api/admin/devices/{deviceID}       # revoke a device (kills its sessions + MCP tokens)
GET    /api/admin/users
PATCH  /api/admin/users/{userID}                 # change role
PATCH  /api/admin/users/{userID}/auth-methods    # enable/disable password & passkey per user
PUT    /api/admin/users/{userID}/ai-access       # grant/revoke use of the admin-funded AI profile
DELETE /api/admin/users/{userID}
POST   /api/admin/users/{userID}/test-push       # delivery diagnostics for one user
GET|PUT /api/admin/users/{userID}/default-instances  # pin per-user default arr instances;
                                                     # for Chaptarr this doubles as the access grant
POST   /api/admin/users/{userID}/plex-invite     # send the Plex invite for the user's shared email
```

### Plex invites (admin)
```
GET    /api/admin/plex/status              # linked account + invite config (never the token)
POST   /api/admin/plex/link/begin          # start the PIN flow -> { pin_id, code, url }
POST   /api/admin/plex/link/check          # poll the PIN; stores the token once approved
DELETE /api/admin/plex/link                # unlink + forget invite settings
GET    /api/admin/plex/servers             # linked account's owned Plex Media Servers
GET    /api/admin/plex/servers/{machineID}/libraries  # sections for the library picker
PUT    /api/admin/plex/settings            # server, shared libraries, auto-invite toggle
```

### Setup status (admin)
```
GET    /api/admin/setup-status             # live-derived checklist of configured/unconfigured features
```
Re-derived from actual configuration on every request (never stored), so the app's setup wizard is resumable and can't go stale. New features surface themselves by adding an item here; clients render unknown keys generically. Completed-media downloads are an optional item and count as configured once deployment roots are present and at least one Radarr, Sonarr, or Chaptarr instance has an effective media path mapping.

### Update status (admin)
```
GET    /api/admin/update-status            # latest-release comparison + management-portal URL
PUT    /api/admin/update-status            # set the management-portal URL ({ management_url })
```
Backs the app's "update available" banner. The server compares its own build version against the latest published GitHub release (`windoze95/cantinarr`), cached ~12h, best-effort and non-blocking; only real semver-tagged builds check (dev/`latest`/PR builds never contact GitHub), and `CANTINARR_DISABLE_UPDATE_CHECK=1` turns it off. `management_url` is an optional admin-set link to a container-management portal the banner points at. Unlike instance URLs (which only the server dials), this link opens **on the admin's own devices**, so it must be reachable from them -- a cluster-internal name that only the server resolves won't work from a phone. The running version is also surfaced to all clients (for the About screen) in `/api/config` as `version`.

### AI configuration history (admin)
```
GET    /api/admin/external-settings-changes          # list AI/MCP profile/custom-format writes
GET    /api/admin/external-settings-changes/{id}     # detail with live profile/custom-format comparison
POST   /api/admin/external-settings-changes/{id}/revert  # guarded quality-profile restore only
```
The app exposes these records at Settings > Configuration history. Timeline responses stay lightweight; selecting one record reads the live profile or custom format and projects a safe before/recorded/current comparison without sending server-owned raw snapshots to a device. The recorded value is labeled as applied, attempted, or intended according to the outcome. Each successfully applied quality-profile update can be restored once while the instance binding, live profile, and relevant dependencies still match the recorded applied state. Success appends a linked restore record instead of rewriting history; that record cannot be restored again, and the source update remains consumed even if its applied values later return. An executing or outcome-unknown restore also blocks replay because Cantinarr cannot safely infer the external result; only a definitively failed attempt may retry after the live guards pass again. Custom-format entries support live comparison but not restore. Generic admin-proxy writes and managed-webhook changes are outside this history.

### Requests
```
POST   /api/requests                       # user: create (movie/tv by tmdb_id;
                                           #   books by foreign_id + book_format; optional instance_id)
GET    /api/requests                       # user: own request history
GET    /api/requests/options               # user: what this user may choose (seasons, quality)
GET    /api/requests/book-status           # user: per-format live state by foreign_id; optional instance_id
GET    /api/requests/book-library          # user: owned/monitored digest; optional instance_id (brief cache)
GET    /api/requests/{tmdb_id}/status      # user: live availability + download progress
GET    /api/admin/requests                 # admin: pending approval queue
POST   /api/admin/requests/{id}/approve    # admin: approve (executes the stored request once)
POST   /api/admin/requests/{id}/deny       # admin: deny with optional reason
GET|PUT /api/admin/request-settings        # admin: global policy (require_approval,
                                           #   allow_season_choice, default scope/quality...)
GET|PUT /api/admin/users/{userID}/request-settings  # admin: per-user overrides
```
Request statuses: `unavailable`, `requested`, `pending` (awaiting approval), `denied`, `downloading`, `partial`, `available`.

### Issues & AI remediation
```
POST   /api/issues                         # user: report a problem; requires instance_id plus movie/tv media scope
                                           #   (instance must be Radarr for movie, Sonarr for tv; gated by allow_reporting)
                                           #   returns {issue_id,status}; initial status is observing or recovering
GET    /api/issues/{id}                    # reporter or admin: issue thread (an admin viewing marks it read)
POST   /api/issues/{id}/reply              # reporter or admin: reply (answers agent questions)
GET    /api/admin/issues?status=           # admin: issue queue (user-reported + auto-detected; each row carries read/unread)
POST   /api/admin/issues/{id}/dismiss      # admin
POST   /api/admin/issues/{id}/resolve      # admin: { disposition: resolved|wont_fix, note: required, <=8192 bytes }
                                           #   transactional reviewed completion; races return 409
GET    /api/admin/issues/{id}/activity     # admin: durable action + run history for one issue
GET|PUT /api/admin/remediation-settings    # admin: master switch, auto-dispatch, reporting,
                                           #   mark-resolved-as-read, mode, optional model_override
                                           #   (provider/credential always follow the shared selection),
                                           #   step/turn/time and daily-run budgets,
                                           #   observation_min_minutes (10), observation_quiet_minutes (5),
                                           #   observation_settle_minutes (2)
GET    /api/admin/agent-actions?status=    # admin: awaiting queue, or status=all for history;
                                           #   actions include immutable instance_id + instance name/service;
                                           #   release GUIDs are one-way fingerprints, never raw indexer capabilities
GET    /api/admin/agent-actions/{id}       # admin: reconcile one durable action after a lost response
POST   /api/admin/agent-actions/{id}/approve   # admin: claims and dispatches the stored proposal once
POST   /api/admin/agent-actions/{id}/deny      # admin: denial resumes the investigation
GET    /api/admin/agent-runs/{id}          # admin: full audit trail of one agent run
```

### Discover & media (user)
```
GET /api/discover/trending | /discover/movies/popular | /discover/tv/popular
GET /api/discover/movies/top-rated | upcoming | now-playing
GET /api/discover/movies | /api/discover/tv          # filterable discover
GET /api/search                                      # multi-search
GET /api/media/movie/{id} | /api/media/tv/{id}       # detail (+ /recommendations, /similar)
GET /api/media/person/{id} | /api/media/person/{id}/credits
GET /api/genres/movie | /api/genres/tv | /api/providers/movie
GET /api/trakt/trending | popular | lists | lists/{user}/{slug}/items
GET /api/trakt/calendar | anticipated | recommendations
```
TMDB and Trakt are proxied server-side -- client devices never hold those keys.

### AI chat (user)
```
POST   /api/ai/chat                         # SSE-streamed conversation with tool use
GET    /api/ai/available                    # effective availability + personal|shared|none source
GET    /api/ai/settings                     # provider catalog, write-only credential flags, effective source
PUT    /api/ai/settings                     # test + atomically save personal provider/model/key override
DELETE /api/ai/settings                     # clear personal override and return to granted included access
PUT    /api/ai/credentials/{provider}       # test + set/replace one personal API key (write-only)
DELETE /api/ai/credentials/{provider}       # erase one personal API key
GET    /api/ai/codex/status                 # personal linked state, safe account metadata, usage windows
POST   /api/ai/codex/device/begin           # begin ChatGPT device authorization
GET    /api/ai/codex/device/{flowID}        # poll this user's pending device flow
DELETE /api/ai/codex/device/{flowID}        # cancel this user's pending device flow
DELETE /api/ai/codex                        # unlink this user's ChatGPT account
```
All personal settings, credentials, Codex accounts, and device flows derive ownership from the authenticated caller; no user ID is accepted from the client. `device/begin` returns a verification URL, one-time code, flow ID, expiry, and polling interval; the app opens the explicit ChatGPT browser sign-in while keeping access and refresh tokens off the device. API keys and completed OAuth authorization are AES-256-GCM encrypted at rest, and responses expose configured booleans rather than secret values. A provider, model, key, or completed OAuth selection is accepted only after the exact candidate completes a small tool-free response turn; a failure leaves the previous key and selection unchanged.

Resolution is deterministic: a personal selection row is the explicit override; otherwise a user with an included-access grant receives the admin's shared provider. If the personal key, OAuth link, runtime, or allowance is unavailable, that request fails as personal instead of silently switching accounts and spending shared quota. The source is resolved once per request and never changes during a provider turn. Chat admission is non-blocking and cost-aware: one active turn per user, 16 turns server-wide, and at most four included-provider turns; excess requests are rejected before any provider call.

### Included AI profile (admin)
```
GET|PUT /api/admin/credentials                  # shared profile + daily-check state; AI writes test before commit
DELETE  /api/admin/credentials/{key}            # erase one shared API key
GET     /api/admin/ai/codex/status              # shared OpenAI OAuth status + admin-only usage metadata
POST    /api/admin/ai/codex/device/begin        # begin shared OpenAI OAuth device authorization
GET     /api/admin/ai/codex/device/{flowID}     # initiating admin polls the pending shared flow
DELETE  /api/admin/ai/codex/device/{flowID}     # initiating admin cancels the pending shared flow
DELETE  /api/admin/ai/codex                     # unlink the shared OpenAI OAuth account
```
The included profile supports the same Anthropic, OpenAI, Gemini, and OpenAI (OAuth) choices as a personal profile. OpenAI OAuth exposes the recommended Codex model plus GPT-5.6 Sol, Terra, and Luna. Grants are independent of roles and are changed per user. The initial admin starts granted, newly invited users start without a grant, and the one-time migration keeps existing users enabled to preserve the former global-provider behavior. Shared account identity, plan, rate-limit windows, and authorization stay admin-only; granted users learn only that their effective source is included. Codex execution separates the singleton credential identity from the requesting Cantinarr identity: refresh state serializes against the shared account, while tool permissions always use the actual caller's current user ID and role. Interactive tool dispatch rechecks the current user, device, role, and (for included AI) shared grant at execution time; a revoked or invalid actor terminates the turn instead of being converted into a model-visible tool error.

Every shared provider/model/API-key save, remediation-model override, and completed OAuth selection performs one real, tool-free, low-reasoning message-response turn before success is reported. The probe uses a bounded response budget, retries only transient failures that occur before any output, and returns a redacted actionable category for invalid credentials/connections, unsupported model access, quota/rate limits, temporary upstream failures, or invalid responses. The settings endpoint commits a candidate profile atomically only after all supplied AI credentials pass. A remediation override is tested with the live shared provider and credential and stored with that provider binding; if the global provider later changes, runs safely fall back to its global model until an override is tested for the new provider. A separate shared-model monitor defaults on and runs at most once every 24 hours; its durable last-check timestamp prevents restart-driven usage. Failure creates or refreshes one `source=system` admin-only issue, and the next successful scheduled or save-time turn resolves it with `resolution_kind=ai_health_restored`. Admins can disable only this background monitor; save-time validation remains mandatory. Neither the monitor nor its issue enters the remediation job queue, and the remediation runner continues to resolve only the admin-global provider and credential without user settings or grants.

The chat request accepts an optional `conversation_id`; the server replays its provider-neutral stored transcript (including tool results and provider-signed continuation state) so follow-up turns keep full grounding across Anthropic, OpenAI, Gemini, and Codex. A transcript is bound to the Cantinarr user, personal/included source, provider account or one-way credential fingerprint, selected model, and the current in-memory OAuth connection generation; changing any of those starts a fresh conversation. Signed provider state is kept atomic and byte-for-byte or the oversized turn is discarded—it is never truncated into an invalid signature pair. Transcripts are byte-bounded and kept only in process memory: they become inaccessible after four hours of inactivity, are evicted by later chat activity, and disappear on restart or a failed provider turn. Prompts, conversation context, and scrubbed tool results are sent to the selected provider (OpenAI for Codex). SSE frames: `{conversation_id}`, `{text}`, `{tool_start: {name, label}}`, `{tool_end: {name, ok}}`, `{media_results}`, `{error}`, then `[DONE]`.

### AI tool toggles (admin)
```
GET    /api/admin/ai-tools          # list tools: { name, description, enabled, admin_only }
PUT    /api/admin/ai-tools/{name}   # { enabled } -- applies to chat and /mcp immediately
PUT    /api/admin/ai-tools/debug    # toggle tool debug mode
```
Tool debug mode records names, timing, status, and payload sizes only; tool inputs, outputs, and error bodies are never written to logs. Every MCP tool result also crosses a shared credential scrubber before it can reach chat, `/mcp`, or the remediation agent, including nested JSON, authorization/cookie headers, URL userinfo, and secret-bearing query parameters.

### Instances & arr proxy
```
GET|POST /api/instances                      # admin: list/create
GET    /api/instances/media-roots             # admin: deployment-approved Cantinarr roots for the mapping editor
POST   /api/instances/test                   # admin: dry-run connectivity check, dialed from the server
PUT|DELETE /api/instances/{instanceID}       # admin: update/delete
GET|PUT /api/instances/{instanceID}/users    # admin: which users are pinned/assigned here
POST   /api/instances/{instanceID}/webhook   # admin: rotate credentials and upsert a managed arr webhook
ANY    /api/instances/{instanceID}/*         # proxy to the instance's own API; JSON secrets are redacted,
                                             # and upstream redirects are remapped onto this route (off-origin ones become 502)
```
The proxy allows read-only Radarr/Sonarr browsing (library, queue, history, wanted, calendar) for regular users; writes, commands, interactive search, config, and all non-arr services require admin. Requesters are bound to their own effective instance -- their pin, or the deterministic global default/fallback -- exactly as `/api/config` reports it; a sibling instance the admin has hidden cannot be reached by guessing its ID, and instance authorization is classified from stored metadata so an undecryptable secret can never widen access. JSON responses are bounded and recursively scrubbed for credential fields and secret-bearing URL query parameters before they reach any client. An encoded, malformed, streaming, or oversized JSON response fails closed rather than bypassing that scrubber.

Radarr, Sonarr, and Chaptarr create/update payloads may include `media_path_mappings`, an ordered array of `{ "arr_path": "...", "cantinarr_path": "..." }` objects. Each row translates an absolute path prefix reported by that one instance into a server-visible directory beneath `CANTINARR_MEDIA_ROOTS`; source prefixes may use POSIX, Windows drive, or UNC syntax independently of the server OS. An empty array disables completed-media downloads for that instance. The admin-only `media-roots` endpoint supplies the approved target roots used by the editor. Mapping paths are admin-only instance configuration and never appear in the requester `/api/config` payload. Saving explicit mappings replaces the legacy identity behavior for that instance.

The proxy is also a transport trust boundary. `CONNECT`, `TRACE`, and `TRACK` are rejected before instance resolution or any upstream contact, and an upstream protocol upgrade (`101`) is refused, so the HTTP proxy can never become an opaque tunnel or reflect the injected `X-Api-Key`. Inbound Cantinarr session cookies and every forwarded credential, client-identity assertion (reverse-proxy `X-Auth-*`/`Remote-*`/mTLS headers), routing/method-override, and request-trailer header terminate here; only the instance's own `X-Api-Key` is added outbound. Upstream responses are marked private and non-cacheable, and nginx/lighttpd internal-redirect controls (`X-Accel-*`, `X-Sendfile`, `X-Reproxy-URL`) plus response trailers are stripped so a fronting web server cannot be steered by an upstream header.

### Completed media downloads (user)

```
POST     /api/media-files/tickets                    # authenticated: { instance_id, file_id }
GET|HEAD /api/media-files/download/{ticket}          # public bearer capability until expires_at
```

Ticket issuance requires `media:download`, re-checks the caller's current account role, and limits requesters to their effective Radarr/Sonarr instance or explicitly granted Chaptarr instance. Administrators may select any configured arr instance. The client supplies only an instance ID and live arr file ID; it never supplies a path. Cantinarr re-fetches that exact file record from Radarr (`moviefile`), Sonarr (`episodefile`), or Chaptarr (`bookfile`) both when issuing the ticket and for every transfer.

Bytes are available only when two boundaries agree: the live arr path must match a mapping owned by that exact instance, and the mapped Cantinarr path must remain beneath a configured `CANTINARR_MEDIA_ROOTS` root. In Docker the target is the container path of a read-only mount; for a native server it is an absolute local directory readable by the server process. This lets instances that both report `/ebooks` safely map to different mounts, and lets a Chaptarr instance map as many independent ebook/audiobook trees as it uses. Mapping does not infer media format from a directory name; the exact Chaptarr record remains authoritative. Files outside either boundary, directory entries, and symlink escapes are refused.

Instances that predate per-instance mappings retain the former identity behavior: each global root is treated as both the arr prefix and Cantinarr prefix until an admin saves explicit mappings. Newly created instances start with downloads disabled. `/api/config.services.media_downloads` remains a compatibility aggregate and is true when at least one instance visible to the current user has an effective download mapping; each returned instance also reports its own path-free download capability so current clients show controls only for the selected instance. Neither response exposes roots or mapping paths.

Tickets are opaque, bounded, reusable for ten minutes so browser HEAD probes and resumed Range requests work, and contain neither JWTs nor server paths. Every GET/HEAD re-checks that the user still exists, uses the user's current role, re-checks effective-instance access for non-admins, re-fetches the live file record, and applies the current mapping. Responses are attachment-only `application/octet-stream` with no-store, no-referrer, nosniff, same-origin resource policy, and sandbox CSP headers; errors never expose arr hosts, filesystem paths, or OS details. Delivery covers the primary files indexed by Radarr, Sonarr, and Chaptarr, not arbitrary neighboring files, subtitles, extras, or directories. Multi-file audiobooks remain individual file choices rather than being packaged into an archive.

### Downloads & monitoring (admin)
```
GET    /api/downloads/{instanceID}/queue     # unified SABnzbd/qBittorrent/NZBGet/Transmission
POST   /api/downloads/{instanceID}/pause|resume            # whole client
POST   /api/downloads/{instanceID}/queue/{itemID}/pause|resume
DELETE /api/downloads/{instanceID}/queue/{itemID}?deleteData=bool
GET    /api/downloads/{instanceID}/history?limit=50
GET    /api/tautulli/{instanceID}/activity   # current Plex streams + bandwidth
GET    /api/tautulli/{instanceID}/history?limit=50
GET    /api/tautulli/{instanceID}/stats?days=30
```

### Push & notification preferences (user)
```
POST   /api/devices/push-token               # register this device's APNs token
DELETE /api/devices/push-token/{deviceID}
GET|PUT /api/notifications/preferences       # per-category opt in/out
POST   /api/notifications/test               # test push to own devices
```

### Webhooks (credential-authenticated, no session)
```
POST   /api/webhooks/arr/{instanceID}             # Sonarr/Radarr -> Connect -> Webhook (Basic Auth)
```
The instance editor's **Configure instant updates** action asks the server to rotate a per-instance credential and create or update a `Cantinarr` Connect webhook in Radarr/Sonarr. The secret moves only between servers: instance API responses and the app never receive it. Managed records use webhook Basic Auth; query-string credentials are rejected and access logs omit all query strings. Set `CANTINARR_PUBLIC_URL` when Cantinarr is behind a reverse proxy; callback generation uses that trusted origin and never trusts client-supplied forwarded headers. The callback must be resolvable and reachable from inside the Radarr/Sonarr containers -- the arr tests the notification when saving it, so an unreachable callback fails the whole configuration step. In Docker/k8s topologies a cluster-internal origin (`http://cantinarr:8585`) is usually correct; a public FQDN works only if the arrs can egress (or hairpin) to it. The configurator can still recognize an old copy/paste record by its callback path and migrate it. Rotation keeps the current and pending credentials valid until the arr accepts the update, and configuration is serialized per instance, so failed or concurrent attempts cannot break a working hook. Handled events -- `Grab`, `Download`, `MovieAdded`/`SeriesAdd`, `MovieDelete`/`SeriesDelete`, `MovieFileDelete`, `EpisodeFileDelete` -- invalidate availability, broadcast WebSocket updates, and (for imports) send new-content pushes; `Test` and everything else is acknowledged with 200 so the arr's Test button just works.

### MCP & OAuth (external tool access)
```
POST|GET|DELETE /mcp                         # MCP Streamable HTTP (JSON-RPC + SSE)
GET  /.well-known/oauth-protected-resource[/mcp]
GET  /.well-known/oauth-authorization-server | /.well-known/openid-configuration
POST /oauth/register                         # dynamic client registration
GET|POST /oauth/authorize                    # browser login (password or passkey) + consent
POST /oauth/token                            # code/refresh grants, PKCE, rotating refresh
GET  /passkeys/setup | /passkeys/create      # passkey pages for MCP/browser setup links
```
These endpoints are Cantinarr's **inbound** OAuth authorization server: an external MCP client signs in to access Cantinarr. They do not link a ChatGPT account. Personal and admin-shared Codex chat use separate **outbound** device flows under `/api/ai/codex/*` and `/api/admin/ai/codex/*`.

When `CANTINARR_OAUTH_ISSUER` is set, authorization-server metadata advertises RFC 9207 issuer identification and successful password/passkey authorization responses include the exact canonical `iss`; without it, Cantinarr preserves request-derived legacy metadata and does not claim stable authorization-response issuer support. The configured issuer must use HTTPS so metadata, redirects, token audience, and auth challenges share one secure canonical external origin. Dynamic client registration accepts and echoes OpenID Connect `application_type` values `native` and `web` (omitted legacy values default to `web`); explicit web registrations require HTTPS redirects. Trusted browser preflights accept the current session header plus MCP protocol-version and method/name routing headers. Supplied origins must match the configured issuer or an entry in `CANTINARR_MCP_ALLOWED_ORIGINS`; when neither is configured all browser origins are rejected before authentication, while native/server clients normally send no `Origin` and are unaffected.

### Real-time
```
WS     /api/ws                  # WebSocket (JWT via subprotocol header)
```

WebSocket events:
- `download_progress` -- `{ tmdb_id, media_type, progress, status }`
- `request_status_changed` -- `{ tmdb_id, media_type, status }` (queue polling **and** arr webhooks; status here is `available`, `partially_available`, `requested`, or `unavailable` -- note the longer spelling vs the REST `partial`)
- `downloads_queue` -- full download-client queue snapshot `{ instance_id, paused, speed_bps, items }`, sent on change
- `arr_queue_changed` -- `{ instance_id, service_type }` invalidation ping; clients refetch via REST
- targeted events fanned out per user/admin: `request_pending`, `request_decision`, `issue_created`, `issue_updated`, `agent_action_pending`, `agent_action_decided`, `remediation_autodispatch_disabled`, `plex_access_request`, `plex_invite_sent`

## Architecture

### ID Bridge (TMDB-to-TVDB)

TMDB has the best metadata and images, but Sonarr only speaks TVDB. The bridge translates transparently:

```
User taps "Request" on Breaking Bad (TMDB 1396)
  |
  v
1. Check SQLite cache for TMDB 1396 -> found TVDB 81189 (cache hit)
  |  or
  v
2. GET api.themoviedb.org/3/tv/1396/external_ids -> { tvdb_id: 81189 }
  |  or (if TMDB has no mapping)
  v
3. GET api.trakt.tv/search/tmdb/1396?type=show -> extract TVDB from Trakt IDs
  |  or (last resort)
  v
4. Sonarr title+year search as fallback
  |
  v
GET sonarr/api/v3/series/lookup?term=tvdb:81189  (exact match)
POST sonarr/api/v3/series  (add with the user's effective defaults)
```

Movies skip bridging entirely -- Radarr natively supports `term=tmdb:{id}`. Books have no TMDB id at all; they're keyed by the Chaptarr/Readarr `foreignBookId` plus a strict `book_format` (`ebook`, `audiobook`, or `both`). Book request bodies may include `instance_id` and `book_selection`: `lookup_term` remembers the catalog query that produced the row, `catalog_foreign_book_id` preserves that exact lookup row when an already-local Chaptarr record uses a different canonical `foreign_id`, and `foreign_author_id`/`author_name` plus per-format external edition, ISBN, ASIN, publisher, language, year, and page-count evidence identify the publication the requester chose. The catalog fields are locators only: the top-level `foreign_id` remains the ownership, status, history, and mutation identity, and a differing catalog ID is accepted only when that canonical work is present in the live library. Local Chaptarr numeric IDs are not accepted, and the server still requires the exact work, author, and publication before a mutation. Status and library endpoints accept `instance_id` as a query parameter. The server authorizes the instance and persists both selections with new pending/history rows, so approval always targets the library and publication the requester was viewing. Omitted instance IDs on new requests resolve through the requester's effective grant (or the admin fallback). Legacy rows are deliberately left unscoped because today's default cannot prove their original library; a legacy pending book row must be resubmitted instead of being approved against a guessed instance.

### Requests, approvals & live availability

A request is recorded in `request_log`, then either executed immediately or parked as `pending` when approval is required (globally or for that user). Approval replays the stored request -- season scope, quality choice, immutable concrete book format, pinned Chaptarr instance, and any stable external book author/publication selection -- exactly once; book format cannot be changed during approval. Book approvals remain pending until their durable Chaptarr job has reached a verified outcome and the decision transaction commits, so a lost browser response or server restart cannot replay an accepted add/search. Denial notifies the requester with the reason and is refused while that exact approval job owns an in-flight mutation. Book `both` operations return `book_formats` with a result for each concrete format. On partial approval, successful formats are recorded as requested/available while failed coverage remains pending for retry; a subscriber whose entire requested slice failed is not sent a false approval. Pending book coverage is overlap-aware (`both` conflicts with either concrete format) and shared across users only when the canonical title, instance, and per-format publication selection agree; different versions remain separate admin decisions. Every subscriber sees their own concrete pending coverage in personal history, while the admin row exposes the safe instance name, requester count, and readable publication facts without local IDs. Approval materializes each subscriber's successful formats in personal history and sends a format-scoped decision; denial history is likewise personal, and unrelated users remain requestable.

For a brand-new Chaptarr title, Cantinarr resolves each concrete format independently: it selects the unique quality profile typed `ebook` or `audiobook`, the corresponding metadata profile typed `2` or `1`, and one accessible root path matching that format. Chaptarr's new-author add contract still requires both formats' configuration on the body even for a one-format request. Legacy untyped entries retain deterministic format-name, sole-profile, or unique-`Default` fallbacks, and a sole generic root remains valid; ambiguous choices fail with an admin-fixable error. Adding a missing sibling format beside an existing canonical book instead reuses that author's complete live configuration while monitoring only the requested format. Bounded in-process striped locks serialize conflicting canonical-book mutations and instance projection refreshes without a single global network-call lock. A per-instance configuration lock also keeps each in-flight write bound to one URL/key while an admin update or deletion waits. The supported deployment remains the repository's single-process SQLite server, not multiple independent writers.

The add itself is a bounded verified sequence, not a blind `POST`: the seed body carries the selected lookup editions unchanged because Chaptarr requires that discovery shape, but Cantinarr never treats those hints as local edition truth. If the app supplied the query that found the row, the server replays it before falling back to the display title; either response must still pass the exact foreign-book, title, author, and publication checks. This avoids losing a valid selection when Chaptarr returns an empty list for the complete title without allowing a broader query to substitute another work. Discovery-only format hints such as `isEbook` are not persisted as an exact publication choice; authoritative format validation uses Chaptarr's format enum. A changed, missing, or ambiguous choice fails closed. Otherwise Cantinarr retains the deterministic legacy choice. It waits for Chaptarr's local author/book catalog to settle, resolves the exact requested-format record, reads authoritative editions from `/edition`, selects the proven edition, applies the full author and book resources, monitors the exact book, and rereads it to prove the settings stuck. No client-provided Chaptarr-local numeric ID is accepted. It then requires Chaptarr to acknowledge the `BookSearch` command before reporting success. This wait covers catalog consistency and command acceptance only; Cantinarr does not hold the request open for indexers to finish, a release to be grabbed, or a download to complete.

Every requester book add/search mutation is persisted as one durable job before any Chaptarr write (a `both` request remains one ordered job); approval jobs are additionally linked to their still-pending request and approving admin. The stable external selection, phase, and exact resolved author/book identity are committed before the non-idempotent book-add and search calls, successful concrete formats are checkpointed before the next format begins, and verified completion writes personal request history and removes the job in the same transaction that finalizes an approval. If the browser response is interrupted, Chaptarr is slow to materialize its catalog, or Cantinarr restarts, the worker resumes by rereading that exact live state and revalidating the selected publication; it does not depend on a status-page click and does not blindly replay an in-flight add. An unconfirmed search is held behind a bounded evidence guard, while a persisted Chaptarr search acknowledgement remains conclusive across restarts only for the same external selection. A definitively unusable match/configuration is retained as `unknown_reason: request_failed` with its safe failure code until the same direct action or approval is retried; retry atomically resets that failed work item before any new remote write, and changes to the selected version discard incompatible format checkpoints. Other active jobs return `unknown_reason: outcome_pending`, so the app disables another request and says Cantinarr is still checking. Transient retries use bounded backoff, abandoned process claims are recoverable, and unrelated due jobs run with bounded concurrency. Seed and instance-repoint recovery discard stale local IDs and restart the exact read-only preflight only after their duplicate-prevention guard expires. Verified requested history and carried format checkpoints are bound to the opaque instance-settings fingerprint, so repointing an instance cannot make an old server's record hide a format or suppress a search on the new server.

Radarr and Sonarr do not wait for indexers or downloads either. A new title returns after the arr accepts the add request carrying its search option; existing-title repair paths persist the needed monitoring and may enqueue an immediate search on a best-effort basis. Chaptarr uses the stricter local-catalog, edition, monitor-readback, and explicit `BookSearch` acknowledgement above because its author, book, and edition records can materialize asynchronously after the initial add acknowledgement.

This write contract is regression-tested against Chaptarr `0.9.720`. Chaptarr remains pre-1.0, and Cantinarr's instance connection check proves API reachability rather than certifying a particular fork build, so the `BOOK-004` live canary is required when validating another Chaptarr release. Long synchronous requests also require any deployment reverse proxy to allow the app's four-minute book-request window; a proxy can otherwise end the browser response while the server is still safely reconciling Chaptarr.

Availability is **always derived live from the arrs**: TV availability comes from the real episode list (aired episodes with files), never from Sonarr's monitored-only percentage -- so a show with one monitored season never reads "available" while most of it is missing. Series with some-but-not-all aired episodes read `partial`, with per-season detail and a one-tap "request more" path that adds seasons without unmonitoring what's already there. Stale request rows are reconciled against reality (a "requested" title the arr has since imported reads `available`; a deleted one falls back to `unavailable`).

Freshness has three layers: WebSocket queue polling (30s), instant arr webhooks for out-of-band changes, and short-TTL caches that mutations/events invalidate. Book status is a per-instance live projection: a file is `available`, a healthy active item in the fully paginated Chaptarr queue is `downloading`, and a matching grabbed row or recent verified command acknowledgement is `requested`; a monitored exact record with its own active `BookSearch` is also `requested` unless Cantinarr is still reconciling an interrupted search response. A monitored exact row may retain `requested` after restart only when the user's concrete verified history carries the current instance-settings fingerprint. A bare monitor flag or legacy/unbound history is repairable partial state, not proof that a request was accepted. Warning, blocked, failed, and error queue rows do not claim active progress. The projection and reduced owned-books digest are cached briefly across search-result calls and invalidated together after mutations. Live state outranks stale decided history. When legacy Chaptarr data cannot be mapped safely to eBook versus audiobook, targeted status returns `status_known: false` and the library digest marks that title the same way; clients must present an unknown/unresolved state rather than treating it as requestable.

### Instances & per-user defaults

The instance registry supports eight service types: `radarr`, `sonarr`, `chaptarr`, `sabnzbd`, `qbittorrent`, `nzbget`, `transmission`, `tautulli`. Stored URLs must be absolute `http`/`https` with no credentials, query, or fragment, and every create/update (plus the dry-run `POST /api/instances/test`) proves reachability with a live connection check **from the server** -- the only host that ever dials these URLs. Clients never receive them (`/api/config` omits the URL field), so cluster-internal names like `http://radarr:7878` are fully supported and the arrs need no exposure beyond the server's network. `https` instances need a certificate the server container trusts: add an internal CA to the image trust store, or use plain `http` on a trusted network -- a self-signed cert otherwise fails the connection check with an x509 error. At most one instance per service type is the global default (enforced in the store -- setting a new default clears the old one). Admins can additionally pin a per-user default per service type, which wins over the global flag; for Chaptarr -- which has no global default -- the per-user pin **is** the access grant. `/api/config` returns a per-user filtered view: regular users only see their effective default instances, and `services.chaptarr` is `false` without a grant.

### AI remediation agent

The issue system turns "my episode won't download" into a supervised agent workflow:

1. **Observe, then report or detect** -- users tap "Report a problem" on media (admin-toggleable); every report names the exact active/detail Radarr or Sonarr instance, and otherwise-identical reports against different instances remain distinct. Every user report and auto detection starts silently as `observing`/`recovering`: read, excluded from the badge, no push, no agent run, and no proposal. Successful complete queue snapshots are cached briefly and drive durable observation; incomplete/capped or failed reads are never interpreted as an empty queue. Replacement download IDs stay in one incident keyed by exact instance + movie/episode scope (including exact S00 specials), and every observed ID is retained for recovery attribution. A problem is promoted once only after both the configured minimum age (10 minutes) and unchanged quiet window (5 minutes); absence must also pass the settle window (2 minutes). Continuous connection/proof uncertainty lasting the minimum window becomes `needs_admin` without starting the agent, so reports neither alert prematurely nor disappear forever. Queue disappearance or file presence alone never proves resolution. `arr_state_cleared` requires the exact live file plus an exact-media import-history record that binds its file ID to one observed download ID. If Cantinarr's first baseline already contains that file, the queue response must have supplied the exact media's file ID (or known absence), any supplied positive ID must match the live/imported file, and the receipt must be no older than the queue attempt's arr-provided `added` time. This handles imports that beat the baseline and already-imported queue rows without trusting cross-service clocks. Cantinarr persists only the compact validated receipt (history/download/file IDs and timestamp), never raw history data.
2. **Investigate** -- a server-owned AI agent follows the currently selected admin shared provider and credential and runs a budgeted tool loop against read-only arr state bound to that issue's instance and media scope. By default it also follows the shared model; an admin may instead save a remediation-only model override after a real response test. It uses only admin-global credentials: the shared Anthropic/OpenAI/Gemini API key or shared OpenAI OAuth connection. Reporter identity, personal AI settings, per-user included-access grants, and legacy remediation provider/model fields never participate in provider resolution. The tested override is bound to its provider, so a later provider change falls back to that provider's shared model instead of sending a stale designation. Budgets cover total tool calls, accumulated active wall-clock time across approval/reporter pauses, and daily run count. API-key providers receive `max_turn_tokens` as a request cap. Codex app-server has no equivalent request field, so Cantinarr records its per-turn usage notifications and interrupts once reported output reaches the configured ceiling. That is a best-effort guard rather than a hard cap: notification timing can let a response exceed the boundary before interruption. Wall-clock, concurrency, daily-run, and tool-step bounds remain independent safeguards.
3. **Ask** -- if the agent needs information only the reporter has, the issue flips to `awaiting_user` and the reporter answers in the issue thread.
4. **Propose** -- in `supervised` mode, mutating fixes (grab release, remediate queue, manual import, trigger search, rescan) become typed `agent_actions` that always require admin confirmation. `investigate_only` mode records no proposal. The server validates the action against the issue's authoritative instance/media/queue/download/episode scope, permits only one active proposal, and stores an admin override separately from the agent's immutable proposal. For a release grab, the server binds title, quality, size, protocol, indexer, and rejection details from the latest exact scoped search; the approval card shows that server-observed metadata. Raw indexer capabilities are replaced by one-way references before persistence or API delivery. Approval refreshes the exact movie, season, or episode search, requires both the reference and metadata to match, and resolves the live capability only in memory for immediate dispatch; episode reports also trigger only an episode search. A manual import filters the just-fetched candidates by the same movie/series/episode identity even when `force` is approved. Book issues currently permit only exact queue/manual-import actions; title-level book mutations fail closed until issues store a durable book id.
5. **Decide** -- every approval card and confirmation names the exact target service, instance name, and immutable instance ID. Approval uses a compare-and-swap claim so retries reconcile the durable state instead of dispatching again; denial (with an optional note) resumes the investigation. A fresh exact-scope recovery check runs both before and immediately after the execution claim: if the arr has begun retrying/replacing, the proposal is superseded, its run is aborted, the issue returns silently to `recovering`, and the executor is never called. A losing concurrent decision returns `409 Conflict`, prompting the app to re-read the winner instead of claiming the attempted decision succeeded. Recovery never hides `needs_admin`, `executing`, or `outcome_unknown`. A process loss after dispatch cannot prove the remote outcome, so startup marks that action `outcome_unknown` and never guesses or silently replays it. Partial or unknown outcomes stop at `needs_admin` and abort the parked run; the model cannot propose another mutation until a human has verified remote state.
6. **Complete or audit** -- when judgment or manual verification is required (especially `needs_admin`/`outcome_unknown`), an admin can explicitly mark the issue `resolved` or `wont_fix` with a required bounded note. The note, admin actor, aggregate close, proposed-action supersession, and parked-run abort commit together under `admin_completed`; a race returns `409` and the app reloads the winner. **Dismiss** remains a separate `admin_dismissed` workflow and does not claim review. Every action and run remains reachable from the issue, and runs persist their ordered step ledger (`agent_runs`/`agent_steps`) with token counts and stop reason. Model-facing issue text, tool results/errors, resume outcomes, transcripts, and audit text are credential-scrubbed before they are sent or stored; the reporter's original thread message remains intact for the reporter/admin UI.

Auto-dispatch has a circuit breaker: repeated agent give-ups disable it and notify admins. A tool-less answer or exhausted investigation becomes `needs_admin` rather than falsely resolving the report. Issue statuses: `observing`, `recovering`, `open`, `investigating`, `awaiting_user`, `awaiting_approval`, `needs_admin`, `resolved`, `wont_fix`, `failed`, `dismissed`. Terminal issues also expose `resolution`, `resolution_kind`, and `closed_at`; current provenance kinds are `agent_concluded`, `arr_state_cleared`, `reporter_timeout`, `admin_completed`, `admin_dismissed`, `ai_health_restored`, and `legacy_unknown`.

Each issue also carries an admin **read/unread** flag: promoted issues start unread, any non-admin (agent/system/reporter) status change re-flags it unread, and an admin opening the thread (or dismissing it) marks it read. Passive `observing`/`recovering` incidents stay read and do not count in the drawer's Issues badge. The `mark_resolved_as_read` setting (default on) keeps a cleanly resolved issue read instead of re-flagging it.

### Import Doctor

One shared classifier (`internal/arr/doctor.go`) explains stuck queue items in plain English -- sample files, un-extracted archives, unconfirmed TheXEM mappings, "not an upgrade", unparseable/invalid files, remote-path-mapping or download-client problems, stalled torrents, permissions -- and maps each to ordered one-click fixes: process monitored downloads, manual/force import (candidates shown first, `quality`/`languages` blobs round-tripped verbatim), remove, blocklist + re-search, change category (hand-off to e.g. Unpackerr), rescan. The same catalog backs the app UI (Sonarr, Radarr, and Chaptarr), the AI assistant, the remediation agent, and the MCP tools; `diagnose_queue` over MCP prints the exact next tool call per item.

### Push notifications

Cantinarr never holds APNs credentials; it talks to a self-hosted push gateway. Setting `CANTINARR_PUSH_GATEWAY_URL` enables push -- with no API key the server **auto-enrolls** on first start and persists its issued key encrypted in the DB (delete the `push_api_key` settings row to force re-enrollment). Enrollment self-heals: a gateway that's down at boot is retried every 60s, and stored device tokens are re-registered once it comes up.

Notification categories (per-user preferences; admin-scoped ones are enforced in SQL, not just defaults):

| Category | Default | Audience | Sent when |
|---|---|---|---|
| `request_decision` | off | requester | their request is approved/denied |
| `request_pending` | on | admins | a new request needs review (badge = queue depth) |
| `new_movie` | on | everyone | a movie finishes importing (collapse-keyed per title) |
| `new_episode` | on | everyone | new episode(s) import for a series |
| `issue_created` | on | admins | a tracked problem becomes actionable after the quiet recovery window, or durable status proof remains unavailable |
| `agent_action_pending` | on | admins | the agent proposed a fix needing approval |
| `plex_access_request` | on | admins | a user shared their Plex email for a server invite (collapse-keyed per user; body says whether auto-invite already handled it) |
| `plex_invite_sent` | on | requester | their Plex invite email went out (one-tap or auto) |

Bodies are server-authored templates (untrusted text never hits the lock screen), sends are fire-and-forget with a 30s timeout, a 10-minute in-process dedupe window absorbs the overlap between queue polling and webhooks, and tokens the gateway reports dead are pruned automatically. Payloads carry deep-link data (`type`, `tmdb_id`/`issue_id`/`user_id`; book request decisions add `foreign_id`, the Chaptarr foreignBookId, plus `title`, `book_format`, and the pinned `instance_id`, since books store `tmdb_id` 0) the app routes on tap.

### Plex invites

Linking a Plex account (Settings > Plex Invites in the app) uses plex.tv's PIN flow: the server mints a PIN, the admin approves it in the browser, and the resulting token is stored AES-encrypted in the settings table (it never appears in any API response). With a server and libraries selected, `POST /api/admin/users/{id}/plex-invite` shares them with the user's email via plex.tv's `shared_servers` API — and with **auto-invite** on, the same happens with zero taps the moment a user shares their email from the Watch on Plex guide. A duplicate share (the account already has access) is treated as soft success. Sending an invite stamps `users.plex_invited_at` (a record of Cantinarr's action, not live Plex state) and pushes `plex_invite_sent` to the user; changing the email clears the stamp since the old invite went to the old address. The stable `X-Plex-Client-Identifier` survives unlink/relink.

### MCP server endpoint

Cantinarr exposes its tools as a [Model Context Protocol](https://modelcontextprotocol.io/) server at `/mcp` (Streamable HTTP, session tracked via `Mcp-Session-Id`). External clients (Claude Desktop, Claude Code, Codex, ...) discover auth from the well-known metadata, register dynamically, and log in through a browser page -- with a Cantinarr password or a passkey. This browser login grants an external client access **to Cantinarr**; it is unrelated to the AI Access device-code flows that grant Cantinarr outbound OpenAI OAuth access to a personal or admin-shared ChatGPT account. Connect-link-only users can create their first passkey from the MCP login flow; a password is what authorizes MCP on plain-HTTP deployments where WebAuthn is unavailable. Initialization reports the running Cantinarr build version, advertises only the implemented tool/resource/prompt behavior, and tells clients where to load the operating guide.

Access tokens are short-lived and audience-bound to `/mcp`. Refresh tokens are persisted, rotate on use, have a one-year sliding lifetime, and are tied to a Cantinarr device record -- revoking the device revokes the MCP client. Registered clients and token state live in the database, so they survive restarts and upgrades.

The MCP server also publishes prompt templates and a `guide://cantinarr/agent-guide.md` resource so external agents pick up the same operating habits as the built-in assistant (trending behavior, `display_media` carousel use, request-status checks before requests, admin download-triage rules). Tool declarations include human-readable titles and explicit read-only, destructive, idempotency, and open-world hints. Media-capable tools reference the `ui://cantinarr/media-results.html` MCP App; its resource declaration and returned content both carry the image-domain CSP metadata enforced by compliant hosts.

Authenticated MCP request observability records only bounded protocol metadata: JSON-RPC method and safe target name, protocol/lifecycle era, whether client capabilities were supplied, sanitized client name/version, HTTP status, classified outcome, and duration. Authentication and permission checks run before any observation body read. Logs never contain bearer or session tokens, tool arguments or results, capability values, resource URIs, or JSON-RPC error text. This makes discovery failures and legacy fallback sequences diagnosable without turning protocol logs into a content audit trail.

**Client example**:
```json
{
  "mcpServers": {
    "cantinarr": { "url": "http://your-server:8585/mcp" }
  }
}
```

### MCP tools

The registry contains 33 in-app AI tools; 31 are also exposed through `/mcp`. `preview_profile_change` and `apply_profile_change` are deliberately hidden from external MCP because their one-use handoff depends on authenticated in-app chat-turn provenance. The remediation agent receives a constrained read-only subset plus issue-scoped human gates. Every shared tool can be disabled from Settings > AI Tools. Interactive execution reauthorizes the current device and role immediately before each tool and rechecks the included-AI grant when shared billing is in use. Tools marked **admin** require the admin role (either flagged directly or gated by a permission the user role doesn't hold):

| Tool | Description |
|---|---|
| `search_movies` | Search TMDB for movies |
| `search_movie_collections` | Search TMDB for movie franchises/collections |
| `search_tv_shows` | Search TMDB for TV shows |
| `get_trending` | Trending movies/shows by day or week |
| `get_movie_details` | Full movie metadata |
| `get_tv_details` | Full TV show metadata |
| `get_recommendations` | Similar content suggestions |
| `check_request_status` | Is this on my server? |
| `get_request_options` | Show the current user's selectable request options and quality profiles |
| `request_media` | Add to Radarr/Sonarr, optionally choosing an allowed quality profile (honors the approval queue) |
| `list_my_requests` | User's request history |
| `display_media` | Curate the visual results carousel |
| `get_queue` | Combined arr download queue (admin) |
| `get_calendar` | Upcoming releases (admin) |
| `get_library` | What's on the server, filterable (admin) |
| `get_history` | Recent grabs/imports/failures (admin) |
| `trigger_search` | Kick off an automatic download search (admin) |
| `search_releases` | Exact movie, season, episode, or book indexer search; returns one-way release references, never raw GUID capabilities (admin) |
| `grab_release` | Freshly re-search the supplied exact media scope and download the unique release matching its one-way reference + indexer id (admin) |
| `remove_queue_item` | Remove/blocklist a queue item (admin) |
| `get_disk_space` | Disk space across instances (admin) |
| `get_arr_health` | Arr system health: download client, remote path mapping, indexers, disk, root folders (admin) |
| `list_arr_instances` | Configured arr instances with the instance IDs the settings tools accept (admin) |
| `get_quality_profiles` | Quality profile summaries, one profile's full stored JSON by id, and optionally the live Radarr/Sonarr language catalog with IDs that may vary by service/version (admin) |
| `get_custom_formats` | Custom format summaries, or one format's full stored JSON by id (admin) |
| `upsert_custom_format` | Create/update a native or TRaSH custom format by exact name; creates enter profiles at score 0, updates preserve profile scores without recomputing stored file matches, and every AI/MCP write is recorded as readable, non-restorable history (admin) |
| `preview_profile_change` | Build a read-only diff for one profile and mint a one-use reference valid only in the same authenticated chat turn after an explicit admin request (in-app chat only, admin) |
| `apply_profile_change` | Autonomously consume that same-turn reference, refuse detected stale state, verify the complete result, and record durable before/after history (in-app chat only, admin) |
| `diagnose_queue` | Import Doctor: explain stuck items + print the exact next call (admin) |
| `get_manual_import_candidates` | List a stuck download's files, mappings, rejections (admin) |
| `execute_manual_import` | Force a download's files into the library (admin) |
| `remediate_queue_item` | One-click queue fix: remove, blocklist+search, change category (admin) |
| `rescan_media` | Rescan a movie/series on disk and run the import pass (admin) |

Custom-format tools probe the configured instance's live collection endpoint rather than trusting a stored version. Sonarr requires v4 for custom formats; a collection-read 404 is reported as either an older/incompatible build or a stored instance URL missing its service URL base, because those cases are indistinguishable at that API boundary. Write-side 404s stay concrete so a concurrently deleted record is not misdiagnosed as an old service. Every successful AI/MCP custom-format create or update receives a verified readback and durable history entry with live comparison; failed or ambiguous attempts retain outcome-aware history for reconciliation. Custom-format entries cannot be restored.

Quality-profile mutation is intentionally narrow: one existing profile, one full-object `PUT`, and only upgrade policy, an already-allowed quality/group cutoff, score thresholds, existing custom-format scores, plus Radarr's profile language. It does not create/delete/rename profiles, toggle or reorder qualities, create custom formats, or batch profiles. An explicit admin request lets the assistant preview and apply within the same authenticated chat turn; the admin never has to copy or type the one-use reference. Preview binds the actor, device, issuing chat turn, exact service instance and current URL/API-key fingerprint, profile, complete custom-format collection, relevant language catalog, and desired full object. The resolved instance remains pinned even if the service default changes. Apply consumes the random reference before remote I/O, reauthorizes, rebuilds from fresh JSON, checks the bound state immediately before writing, verifies the complete stored profile afterward, and records server-held before/applied snapshots. Any detected stale state, expired/superseded/restarted/used reference, or ambiguous write outcome requires a new preview.

The final guards are optimistic, not an atomic compare-and-swap: Cantinarr serializes its own settings writes, but a direct arr UI/API edit—or a local authorization, tool-toggle, URL, or API-key change—can still race the last check and `PUT`. Settings > Configuration history safely projects the recorded differences and current live comparison. A guarded restore is offered once, only while the live profile, its relevant dependencies, and the instance binding still match the applied update; success creates a linked, non-restorable history entry and permanently consumes that update's restore action. Direct arr edits or connection changes make restore unavailable rather than overwriting newer work. Typed arr HTTP 400 validation details are projected through a bounded, redacted exception used only for credential-free custom-format/profile endpoints; all other error bodies remain discarded.

Language IDs may vary by service and version, so they are read live from each Radarr/Sonarr instance (`get_quality_profiles` with `include_languages`) instead of hardcoded or reused. Sonarr v4 language behavior comes from scoring an existing `LanguageSpecification` custom format; it has no persistent profile-language write. Radarr's profile-level language is a hard release filter and must be `Any` when a language custom format has a nonzero score. Chaptarr supports the scalar/cutoff/custom-format-score profile changes but no release-language specification. These settings influence future release selection for media assigned to the profile: they do not inspect or remux downloaded streams, change file-level default audio/subtitle flags, guarantee playback language, or retroactively replace files.

### Database

SQLite (pure Go driver) with WAL mode. **The live schema is code**: `internal/db/db.go` -- the `initSQL` create statements plus an in-code list of tolerant `ALTER TABLE` migrations with one-time backfills. There are no SQL migration files.

| Area | Tables |
|---|---|
| Accounts & sessions | `users`, `refresh_tokens`, `connect_tokens`, `devices` (hardware-id deduped), `webauthn_credentials` |
| Requests | `request_log` (approval + season/quality/book-format/instance/external-publication capture, plus opaque verified book-binding proof), `book_request_waiters` (selection-compatible shared pending subscribers + their concrete format coverage), `book_request_jobs` (restart-safe direct and approval-linked Chaptarr convergence with external selection), `user_request_settings` |
| Instances | `service_instances` (encrypted keys/passwords + current/pending server-only webhook credentials + per-instance media path mappings/legacy mode), `user_default_instances` |
| Push | `push_tokens` (one per device), `notification_prefs` |
| AI access | `user_ai_settings` (explicit personal selection), `user_ai_credentials` (per-provider encrypted personal API keys), `user_codex_accounts` (personal encrypted OpenAI OAuth authorization), `shared_codex_account` (singleton encrypted included authorization); `users.ai_shared_enabled` stores the included-access grant, while `settings` stores the daily health-check switch/timestamp |
| AI configuration history | `external_setting_changes` (append-only AI/MCP quality-profile/custom-format outcomes, server-held before/applied snapshots, and linked quality-profile restores) |
| Remediation | `issues` (exact arr-scoped reports plus admin-only system alerts, with closure provenance), `issue_observations` (durable retry/settle clocks, baseline + compact import receipt), `issue_observation_downloads` (incident download IDs + arr attempt/file boundaries), `issue_observation_attempts` (transition audit), `remediation_queue_snapshots` (latest successful minimal typed snapshot), `remediation_observation_failures` (bounded outage timer), `remediation_observation_watermarks` (monotonic per-instance success/failure ordering), `issue_messages`, `agent_runs`, `agent_steps`, `agent_actions` (one active proposal per issue; immutable proposal + approved params) |
| MCP OAuth | `oauth_clients`, `oauth_authorization_codes`, `oauth_refresh_tokens` |
| Misc | `settings` (encrypted KV: JWT secret, push key, request policy, Plex token + invite config), `tmdb_tvdb_cache` (30-day TTL) |

## Project Structure

```
server/
├── cmd/server/main.go        # Entry point, dependency wiring
├── internal/
│   ├── ai/                   # Multi-provider chat: SSE handler, API-key providers
│   │                         #   provider-neutral streaming + conversation store
│   ├── api/router.go         # Chi router: routes, CORS, permissions, /api/config payload
│   ├── arr/                  # Import Doctor plus safe settings HTTP/validation boundaries
│   ├── auth/                 # JWT, connect links, users/devices, WebAuthn, OAuth AS, RBAC
│   ├── cache/                # Small TTL cache used by request-side digests
│   ├── chaptarr/             # Chaptarr (Readarr v1) client for the books module
│   ├── codexapp/             # Scoped personal/shared Codex auth, chat, usage + lifecycle
│   ├── config/               # Env config (port, name, passkey/push/Codex settings)
│   ├── credentials/          # External credential registry + lazy client caching
│   ├── db/db.go              # SQLite setup, WAL, THE live schema + in-code migrations
│   ├── discover/             # TMDB/Trakt discovery + media detail proxy handlers
│   ├── downloads/            # Unified download-client queue API across all four clients
│   ├── instance/             # Instance registry, defaults invariant, per-user pins, safe webhook rotation
│   ├── mcp/                  # 33 registered tools, toggles, tool server (31 also exposed through external MCP)
│   ├── mcpserver/            # MCP Streamable HTTP endpoint, prompts, agent guide (mcp-go)
│   ├── mediafiles/           # Ticketed, instance-mapped + root-confined media streaming
│   ├── mediapath/            # Cross-platform arr-path validation and local translation
│   ├── nzbget/               # NZBGet JSON-RPC client
│   ├── plex/                 # plex.tv PIN link + shared_servers invites (one-tap & auto)
│   ├── proxy/                # Credential-scrubbing arr reverse proxy (read-only for users)
│   ├── push/                 # Push gateway client, auto-enroll, prefs, notifier
│   ├── qbittorrent/          # qBittorrent WebUI v2 client
│   ├── radarr/               # Radarr API v3 client
│   ├── remediation/          # Issues, agent runner, approvals, auto-dispatch, budgets
│   ├── request/              # Request orchestration, approvals, live availability
│   ├── sabnzbd/              # SABnzbd JSON API client
│   ├── secrets/              # AES-256-GCM secrets-at-rest
│   ├── sonarr/               # Sonarr API v3 client
│   ├── tautulli/             # Tautulli activity/history/stats client
│   ├── tmdb/                 # TMDB client + ID bridge
│   ├── trakt/                # Trakt client (discovery + fallback ID resolver)
│   ├── transmission/         # Transmission RPC client
│   ├── web/                  # Flutter web embed (go:embed) + SPA file server
│   ├── webhooks/             # Arr webhook receiver (server-managed per-instance Basic auth)
│   └── websocket/            # Hub: queue polling, event fan-out, complete observation feed
├── Dockerfile                # API-only build
└── go.mod
```

## Tech Stack

- **Go 1.25** with [Chi](https://github.com/go-chi/chi) router
- **SQLite** via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO)
- **JWT** via [golang-jwt](https://github.com/golang-jwt/jwt), **WebAuthn** via [go-webauthn](https://github.com/go-webauthn/webauthn)
- **WebSocket** via [gorilla/websocket](https://github.com/gorilla/websocket)
- **MCP** via [mcp-go](https://github.com/mark3labs/mcp-go) (Streamable HTTP)
- **Anthropic Messages API**, **OpenAI Chat Completions API**, and **Gemini streamGenerateContent**, plus scoped personal/shared OpenAI OAuth through Codex app-server in `internal/codexapp`
- **Codex app-server** bundled by both Dockerfiles with a pinned `CODEX_VERSION` and checksum-verified amd64/arm64 binaries; its upstream Apache-2.0 `LICENSE` and `NOTICE` ship under `/usr/share/licenses/codex-app-server/`. Source runs can override discovery with `CANTINARR_CODEX_BIN`.

## License

See the root repository for license information.
