# Cantinarr Server

The backend brain for [Cantinarr](https://github.com/windoze95/cantinarr) -- a self-hosted media request app for Plex and Jellyfin households.

A single Go binary that bridges your arr stack, serves the web UI, and keeps API keys off user devices. Drop it on your NAS, point it at Radarr/Sonarr (and Chaptarr for books), and generate connect links for family and friends.

```
                     Cantinarr Server (:8585)
  ┌───────────────────────────────────────────────────────────┐
  │  Auth (JWT/passkeys)   Requests + Approvals   AI Chat     │
  │        │                     │                   │        │
  │        │              ┌──────┴──────┐      26 MCP Tools   │
  │        │              │  ID Bridge  │            │        │
  │        │              └──┬───────┬──┘     AI Remediation  │
  │        │                 │       │            Agent       │
  │   Instance registry ── Radarr  Sonarr  Chaptarr           │
  │        │                 ▲       ▲                        │
  │   Verbatim arr proxy ────┘       └──── Webhook receiver   │
  │                                                           │
  │  Downloads (SAB/qBit/NZBGet/Transmission)   Tautulli      │
  │  WebSocket hub          Push gateway client               │
  │  TMDB/Trakt discover proxy      Flutter web (embedded)    │
  └───────────────────────────────────────────────────────────┘
```

## Features

- **One-tap requests with an approval queue** -- Users browse and tap request; the server handles ID bridging, arr lookups, quality profiles, and root folders. Admins can require approval globally or per user, and per user allow season-level choice, quality choice, and per-service default quality profiles.
- **Books via Chaptarr** -- A Readarr-API (v1) books module with per-format (ebook/audiobook) monitoring, requesting, and library awareness. Chaptarr access is granted per user by an admin.
- **Automatic ID bridging** -- Transparently translates TMDB IDs to TVDB IDs for Sonarr. Falls back to Trakt cross-references, then title+year search. Results cached in SQLite for 30 days.
- **Availability computed live** -- Request status is derived from the arrs' real episode/file state (never from a stale snapshot or monitored-only stats), refreshed by queue polling and instant arr webhooks.
- **Connect link auth, passwordless by default** -- Admins generate connect links; sessions are long-lived JWTs that refresh on use. Passwords and passkeys (WebAuthn, incl. native iOS/Android/Windows) are admin-gated per user.
- **AI assistant + remediation agent** -- Anthropic, OpenAI, or Gemini-powered chat with server-side tool execution, plus an autonomous investigation agent that diagnoses reported/detected media problems and proposes fixes an admin approves.
- **MCP server** -- The same 26 tools exposed at `/mcp` (Streamable HTTP) with full OAuth: discovery metadata, dynamic client registration, PKCE, browser/passkey login, rotating refresh tokens.
- **Import Doctor** -- Plain-English diagnosis of stuck downloads with one-click fixes (manual/force import, remove+blocklist+re-search, category hand-off, rescan), shared by the app, the AI assistant, and MCP.
- **Push notifications** -- APNs delivery through a self-hosted push gateway with zero-config auto-enrollment, per-user preference categories, and admin-scoped alerts.
- **Real-time updates** -- WebSocket hub polls arr queues (30s) and download clients (15s) and pushes progress, queue snapshots, and change pings; arr webhooks make external changes (manual imports, deletes) land instantly.
- **Arr proxy** -- Read-only Radarr/Sonarr browsing for users, full passthrough for admins, without exposing API keys.
- **Secrets encrypted at rest** -- Instance API keys/passwords, external credentials, webhook tokens, and the JWT secret are AES-256-GCM encrypted in SQLite.
- **Flutter web embed** -- The web build ships inside the binary via `go:embed`. One container, one port, API + UI.
- **Tiny footprint** -- Pure Go (no CGO), static binary, Alpine-based image.

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

Open `http://your-server:8585` -- the setup wizard creates your admin account. Then configure API credentials and service instances from the admin UI.

### From Source

```bash
# Requires Go 1.25+
cd server
go build -o cantinarr ./cmd/server
./cantinarr
```

## Configuration

Service credentials (TMDB, Anthropic/OpenAI/Gemini, Trakt) and all service instances (Radarr, Sonarr, Chaptarr, SABnzbd, qBittorrent, NZBGet, Transmission, Tautulli) are managed through the admin UI. No environment variables needed for API keys.

Optional env vars for deployment tuning (a `.env` file next to the binary is auto-loaded):

| Variable | Default | Description |
|---|---|---|
| `CANTINARR_PORT` | `8585` | HTTP listen port |
| `CANTINARR_SERVER_NAME` | `Cantinarr` | Display name shown in clients |
| `CANTINARR_JWT_SECRET` | auto-generated | HMAC secret for JWT signing (persisted encrypted when auto-generated) |
| `CANTINARR_ENCRYPTION_KEY` | auto-generated key file | Base64 32-byte key for secrets-at-rest (default: `/config/encryption.key`) |
| `CANTINARR_AI_PROVIDER` | `anthropic` | Fallback AI provider when none is saved in the admin UI (`anthropic`, `openai`, `gemini`) |
| `CANTINARR_AI_MODEL` | provider default | Fallback model when none is saved in the admin UI |
| `CANTINARR_PUSH_GATEWAY_URL` | unset | Push gateway origin; setting it **enables** push notifications |
| `CANTINARR_PUSH_API_KEY` | unset | Optional pinned gateway key -- leave blank and the server auto-enrolls on first start, persisting its issued key encrypted in the DB |
| `CANTINARR_PUSH_ENROLL_TOKEN` | unset | Shared enroll token, only for gateways with gated enrollment |
| `CANTINARR_APPLE_APP_IDS` | unset | Comma-separated `TeamID.BundleID` values served in `/.well-known/apple-app-site-association` for native Apple passkeys |
| `CANTINARR_ANDROID_PACKAGE_NAME` | `com.cantinarr.cantinarr` | Android package name for native passkeys |
| `CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS` | unset | Comma-separated Android signing cert SHA-256 fingerprints for `/.well-known/assetlinks.json` and Android WebAuthn origins |
| `CANTINARR_WEBAUTHN_EXTRA_ORIGINS` | unset | Additional WebAuthn origins to trust (e.g. non-standard HTTPS ports) |

The database lives at `/config/cantinarr.db` (SQLite, WAL mode). Keep the `/config` volume -- it holds the DB and the auto-generated encryption key, and encrypted secrets are unrecoverable without that key.

Native app passkeys require a public HTTPS domain associated with the app: Apple devices verify the AASA file, Android credential providers verify `assetlinks.json`. Browser passkey setup remains available when native association isn't possible.

## API Reference

Auth levels: **public**, **user** (any signed-in account), **admin**. Public auth endpoints are rate-limited to 10 requests/minute/IP. Internally, authorization is permission-based (`users:manage`, `requests:manage`, `remediation:manage`, `instances:manage`, `arr:browse`, ...) -- "admin" below means the admin role holds the required permission and the user role doesn't.

### Auth & account
```
GET    /api/auth/status                    # public: is setup complete, login methods
POST   /api/auth/setup                     # public: first-run admin account creation
POST   /api/auth/login                     # public: { username, password } -> tokens
POST   /api/auth/refresh                   # public: rotate refresh token
POST   /api/auth/connect                   # public: redeem a connect-link token -> session
POST   /api/auth/passkey/login/begin|finish   # public: WebAuthn login
POST   /api/auth/passkey/setup/begin|finish   # public: passkey setup via short-lived link
GET    /api/auth/me                        # user: profile + permissions
POST   /api/auth/password                  # user: set password (admin-enabled users only)
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
DELETE /api/admin/users/{userID}
POST   /api/admin/users/{userID}/test-push       # delivery diagnostics for one user
GET|PUT /api/admin/users/{userID}/default-instances  # pin per-user default arr instances;
                                                     # for Chaptarr this doubles as the access grant
```

### Requests
```
POST   /api/requests                       # user: create (movie/tv by tmdb_id;
                                           #   books by foreign_id + book_format)
GET    /api/requests                       # user: own request history
GET    /api/requests/options               # user: what this user may choose (seasons, quality)
GET    /api/requests/book-status           # user: per-format request/ownership state of a book
GET    /api/requests/book-library          # user: owned/monitored books digest (~2 min cache)
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
POST   /api/issues                         # user: report a problem (gated by allow_reporting)
GET    /api/issues/{id}                    # reporter or admin: issue thread
POST   /api/issues/{id}/reply              # reporter or admin: reply (answers agent questions)
GET    /api/admin/issues?status=           # admin: issue queue (user-reported + auto-detected)
POST   /api/admin/issues/{id}/dismiss      # admin
GET|PUT /api/admin/remediation-settings    # admin: master switch, auto-dispatch, reporting,
                                           #   autonomy tier, provider/model, run budgets
GET    /api/admin/agent-actions?status=    # admin: proposed-fix approval queue
POST   /api/admin/agent-actions/{id}/approve   # admin: executes the stored proposal exactly once
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
POST   /api/ai/chat             # SSE-streamed conversation with tool use
GET    /api/ai/available        # { available: bool }
```
The chat request accepts an optional `conversation_id`; the server replays its provider-neutral stored transcript (including tool results) so follow-up turns keep full grounding across Anthropic, OpenAI, and Gemini. SSE frames: `{conversation_id}`, `{text}`, `{tool_start: {name, label}}`, `{tool_end: {name, ok}}`, `{media_results}`, `{error}`, then `[DONE]`.

### AI tool toggles (admin)
```
GET    /api/admin/ai-tools          # list tools: { name, description, enabled, admin_only }
PUT    /api/admin/ai-tools/{name}   # { enabled } -- applies to chat and /mcp immediately
PUT    /api/admin/ai-tools/debug    # toggle tool debug mode
```

### Instances & arr proxy
```
GET|POST /api/instances                      # admin: list/create
PUT|DELETE /api/instances/{instanceID}       # admin: update/delete
GET|PUT /api/instances/{instanceID}/users    # admin: which users are pinned/assigned here
ANY    /api/instances/{instanceID}/*         # verbatim proxy to the instance's own API
```
The proxy allows read-only Radarr/Sonarr browsing (library, queue, history, wanted, calendar) for regular users; writes, commands, interactive search, config, and all non-arr services require admin.

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

### Webhooks (token-authed, no session)
```
POST   /api/webhooks/arr/{instanceID}?token=...   # Sonarr/Radarr -> Connect -> Webhook
```
Each Radarr/Sonarr instance gets a stable per-instance token (shown in the instance editor with a copy-paste URL). The token rides the query param or the webhook form's basic-auth password field. Handled events -- `Grab`, `Download`, `MovieAdded`/`SeriesAdd`, `MovieDelete`/`SeriesDelete`, `MovieFileDelete`, `EpisodeFileDelete` -- invalidate availability, broadcast WebSocket updates, and (for imports) send new-content pushes; `Test` and everything else is acknowledged with 200 so the arr's Test button just works.

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

### Real-time
```
WS     /api/ws                  # WebSocket (JWT via subprotocol header)
```

WebSocket events:
- `download_progress` -- `{ tmdb_id, media_type, progress, status }`
- `request_status_changed` -- `{ tmdb_id, media_type, status }` (queue polling **and** arr webhooks; status here is `available`, `partially_available`, `requested`, or `unavailable` -- note the longer spelling vs the REST `partial`)
- `downloads_queue` -- full download-client queue snapshot `{ instance_id, paused, speed_bps, items }`, sent on change
- `arr_queue_changed` -- `{ instance_id, service_type }` invalidation ping; clients refetch via REST
- targeted events fanned out per user/admin: `request_pending`, `request_decision`, `issue_created`, `issue_updated`, `agent_action_pending`, `agent_action_decided`, `remediation_autodispatch_disabled`

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

Movies skip bridging entirely -- Radarr natively supports `term=tmdb:{id}`. Books have no TMDB id at all; they're keyed by the Chaptarr/Readarr `foreignBookId` plus a `book_format` (`ebook`, `audiobook`, or both).

### Requests, approvals & live availability

A request is recorded in `request_log`, then either executed immediately or parked as `pending` when approval is required (globally or for that user). Approval replays the stored request -- season scope, quality choice, book format -- exactly once; denial notifies the requester with the reason.

Availability is **always derived live from the arrs**: TV availability comes from the real episode list (aired episodes with files), never from Sonarr's monitored-only percentage -- so a show with one monitored season never reads "available" while most of it is missing. Series with some-but-not-all aired episodes read `partial`, with per-season detail and a one-tap "request more" path that adds seasons without unmonitoring what's already there. Stale request rows are reconciled against reality (a "requested" title the arr has since imported reads `available`; a deleted one falls back to `unavailable`).

Freshness has three layers: WebSocket queue polling (30s), instant arr webhooks for out-of-band changes, and short-TTL caches (e.g. the owned-books digest, ~2 minutes) that those events invalidate.

### Instances & per-user defaults

The instance registry supports eight service types: `radarr`, `sonarr`, `chaptarr`, `sabnzbd`, `qbittorrent`, `nzbget`, `transmission`, `tautulli`. At most one instance per service type is the global default (enforced in the store -- setting a new default clears the old one). Admins can additionally pin a per-user default per service type, which wins over the global flag; for Chaptarr -- which has no global default -- the per-user pin **is** the access grant. `/api/config` returns a per-user filtered view: regular users only see their effective default instances, and `services.chaptarr` is `false` without a grant.

### AI remediation agent

The issue system turns "my episode won't download" into a supervised agent workflow:

1. **Report or detect** -- users tap "Report a problem" on media (admin-toggleable), or the queue-poll witness auto-opens issues for stuck downloads (deduped by an open-issue unique index). Auto-detected issues resolve themselves when the underlying problem clears.
2. **Investigate** -- an AI agent (provider/model configurable, defaulting to the chat provider) runs a budgeted tool loop against read-only arr state. Budgets: max steps, wall-clock, per-run cost, daily run cap, daily cost ceiling.
3. **Ask** -- if the agent needs information only the reporter has, the issue flips to `awaiting_user` and the reporter answers in the issue thread.
4. **Propose** -- mutating fixes (grab release, remediate queue, manual import, trigger search, rescan) become `agent_actions` awaiting admin approval. Approve executes the stored proposal exactly once (compare-and-swap guarded); deny (with optional note) resumes the investigation. The `auto_safe` autonomy tier lets low-risk actions run without approval; `investigate_only` proposes nothing.
5. **Audit** -- every run persists its full step ledger (`agent_runs`/`agent_steps`) with token counts and cost, viewable from the app.

Auto-dispatch has a circuit breaker: repeated agent give-ups disable it and notify admins. Issue statuses: `open`, `investigating`, `awaiting_user`, `awaiting_approval`, `resolved`, `wont_fix`, `failed`, `dismissed`.

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
| `issue_created` | on | admins | a problem is reported/detected |
| `agent_action_pending` | on | admins | the agent proposed a fix needing approval |

Bodies are server-authored templates (untrusted text never hits the lock screen), sends are fire-and-forget with a 30s timeout, a 10-minute in-process dedupe window absorbs the overlap between queue polling and webhooks, and tokens the gateway reports dead are pruned automatically. Payloads carry deep-link data (`type`, `tmdb_id`/`issue_id`) the app routes on tap.

### MCP server endpoint

Cantinarr exposes its tools as a [Model Context Protocol](https://modelcontextprotocol.io/) server at `/mcp` (Streamable HTTP, session tracked via `Mcp-Session-Id`). External clients (Claude Desktop, Claude Code, Codex, ...) discover auth from the well-known metadata, register dynamically, and log in through a browser page -- with a Cantinarr password or a passkey. Connect-link-only users can create their first passkey from the MCP login flow; a password is what authorizes MCP on plain-HTTP deployments where WebAuthn is unavailable.

Access tokens are short-lived and audience-bound to `/mcp`. Refresh tokens are persisted, rotate on use, have a one-year sliding lifetime, and are tied to a Cantinarr device record -- revoking the device revokes the MCP client. Registered clients and token state live in the database, so they survive restarts and upgrades.

The MCP server also publishes prompt templates and a `guide://cantinarr/agent-guide.md` resource so external agents pick up the same operating habits as the built-in assistant (trending behavior, `display_media` carousel use, request-status checks before requests, admin download-triage rules).

**Client example**:
```json
{
  "mcpServers": {
    "cantinarr": { "url": "http://your-server:8585/mcp" }
  }
}
```

### MCP tools

The same 26 tools power the in-app AI assistant, the remediation agent's toolbox, and `/mcp`. Every tool can be disabled from Settings > AI Tools. Tools marked **admin** require the admin role (either flagged directly or gated by a permission the user role doesn't hold):

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
| `request_media` | Actually add to Radarr/Sonarr (honors the approval queue) |
| `list_my_requests` | User's request history |
| `display_media` | Curate the visual results carousel |
| `get_queue` | Combined arr download queue (admin) |
| `get_calendar` | Upcoming releases (admin) |
| `get_library` | What's on the server, filterable (admin) |
| `get_history` | Recent grabs/imports/failures (admin) |
| `trigger_search` | Kick off an automatic download search (admin) |
| `search_releases` | Interactive indexer release search (admin) |
| `grab_release` | Download a specific release (admin) |
| `remove_queue_item` | Remove/blocklist a queue item (admin) |
| `get_disk_space` | Disk space across instances (admin) |
| `get_arr_health` | Arr system health: download client, remote path mapping, indexers, disk, root folders (admin) |
| `diagnose_queue` | Import Doctor: explain stuck items + print the exact next call (admin) |
| `get_manual_import_candidates` | List a stuck download's files, mappings, rejections (admin) |
| `execute_manual_import` | Force a download's files into the library (admin) |
| `remediate_queue_item` | One-click queue fix: remove, blocklist+search, change category (admin) |
| `rescan_media` | Rescan a movie/series on disk and run the import pass (admin) |

### Database

SQLite (pure Go driver) with WAL mode. **The live schema is code**: `internal/db/db.go` -- the `initSQL` create statements plus an in-code list of tolerant `ALTER TABLE` migrations with one-time backfills. There are no SQL migration files.

| Area | Tables |
|---|---|
| Accounts & sessions | `users`, `refresh_tokens`, `connect_tokens`, `devices` (hardware-id deduped), `webauthn_credentials` |
| Requests | `request_log` (approval + season/quality/book-format capture), `user_request_settings` |
| Instances | `service_instances` (encrypted keys/passwords + webhook token), `user_default_instances` |
| Push | `push_tokens` (one per device), `notification_prefs` |
| Remediation | `issues`, `issue_messages`, `agent_runs`, `agent_steps`, `agent_actions` (fingerprint-deduped) |
| MCP OAuth | `oauth_clients`, `oauth_authorization_codes`, `oauth_refresh_tokens` |
| Misc | `settings` (encrypted KV: JWT secret, push key, request policy), `tmdb_tvdb_cache` (30-day TTL) |

## Project Structure

```
server/
├── cmd/server/main.go        # Entry point, dependency wiring
├── internal/
│   ├── ai/                   # Multi-provider chat: SSE handler, Anthropic/OpenAI/Gemini
│   │                         #   streaming loops, provider-neutral conversation store
│   ├── api/router.go         # Chi router: routes, CORS, permissions, /api/config payload
│   ├── arr/doctor.go         # Shared Import Doctor classifier (app + AI + MCP agree)
│   ├── auth/                 # JWT, connect links, users/devices, WebAuthn, OAuth AS, RBAC
│   ├── cache/                # Small TTL cache used by request-side digests
│   ├── chaptarr/             # Chaptarr (Readarr v1) client for the books module
│   ├── config/               # Env config (port, name, passkey/push settings)
│   ├── credentials/          # External credential registry + lazy client caching
│   ├── db/db.go              # SQLite setup, WAL, THE live schema + in-code migrations
│   ├── discover/             # TMDB/Trakt discovery + media detail proxy handlers
│   ├── downloads/            # Unified download-client queue API across all four clients
│   ├── instance/             # Instance registry, defaults invariant, per-user pins, webhook tokens
│   ├── mcp/                  # The 26 tools, toggles, tool server (chat + MCP + agent share it)
│   ├── mcpserver/            # MCP Streamable HTTP endpoint, prompts, agent guide (mcp-go)
│   ├── nzbget/               # NZBGet JSON-RPC client
│   ├── proxy/                # Verbatim arr reverse proxy (read-only for users)
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
│   ├── webhooks/             # Arr webhook receiver (per-instance token auth)
│   └── websocket/            # Hub: queue polling, event fan-out, auto-issue witness
├── Dockerfile                # API-only build
└── go.mod
```

## Tech Stack

- **Go 1.25** with [Chi](https://github.com/go-chi/chi) router
- **SQLite** via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO)
- **JWT** via [golang-jwt](https://github.com/golang-jwt/jwt), **WebAuthn** via [go-webauthn](https://github.com/go-webauthn/webauthn)
- **WebSocket** via [gorilla/websocket](https://github.com/gorilla/websocket)
- **MCP** via [mcp-go](https://github.com/mark3labs/mcp-go) (Streamable HTTP)
- **Anthropic Messages API**, **OpenAI Chat Completions API**, and **Gemini streamGenerateContent** -- all streaming, behind one provider-neutral loop

## License

See the root repository for license information.
