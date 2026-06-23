# Cantinarr Server

The backend brain for [Cantinarr](https://github.com/windoze95/cantinarr) -- a self-hosted media request app for Plex and Jellyfin households.

A single Go binary that bridges your arr stack, serves the web UI, and keeps API keys off user devices. Drop it on your NAS, point it at Radarr/Sonarr, and generate connect links for family and friends.

```
                        Cantinarr Server (:8585)
    ┌──────────────────────────────────────────────────┐
    │                                                  │
    │   Auth (JWT)    Requests    AI Chat              │
    │       │             │             │              │
    │       │      ┌──────┴──────┐      │              │
    │       │      │    ID       │      │              │
    │       │      │   Bridge    │   MCP Tools         │
    │       │      │             │      │              │
    │       │      └──┬─────┬───┘      │              │
    │       │         │     │          │              │
    │       │    Radarr   Sonarr    AI Providers      │
    │       │         │     │       API               │
    │   Flutter Web (embedded)     WebSocket Hub      │
    └──────────────────────────────────────────────────┘
```

## Features

- **One-tap requests** -- Users browse TMDB/Trakt on the app, tap request, and the server handles everything: ID bridging, arr lookups, quality profiles, root folders.
- **Automatic ID bridging** -- Transparently translates TMDB IDs to TVDB IDs for Sonarr. Falls back to Trakt cross-references, then title+year search. Results cached in SQLite for 30 days.
- **Connect link auth** -- Admin generates one-time connect links for household members. JWT-based sessions with 15-minute access / 30-day refresh tokens.
- **AI assistant** -- Anthropic, OpenAI, or Gemini-powered chat with server-side tool execution (search, recommendations, request status, make requests). Streams responses via SSE.
- **Real-time updates** -- WebSocket hub polls arr queues every 30 seconds and pushes download progress to connected clients.
- **Arr proxy** -- Admins get full passthrough to Radarr/Sonarr APIs for management without exposing keys.
- **Flutter web embed** -- The Flutter web build is embedded in the binary via `go:embed`. One container, one port, serves both API and UI.
- **Tiny footprint** -- Pure Go (no CGO), static binary, Alpine-based Docker image under 20MB.

## Quick Start

### Docker Compose (recommended)

```yaml
services:
  cantinarr:
    image: cantinarr/cantinarr:latest
    ports:
      - "8585:8585"
    volumes:
      - ./config:/config
    restart: unless-stopped
```

```bash
docker-compose up -d
```

Open `http://your-server:8585` -- the setup wizard creates your admin account. Then configure API credentials and service instances from the admin UI.

### From Source

```bash
# Requires Go 1.22+
cd server
go build -o cantinarr ./cmd/server
./cantinarr
```

## Configuration

Service credentials (TMDB, Anthropic/OpenAI/Gemini, Trakt) and Radarr/Sonarr instances are managed through the admin UI at **Settings > API Credentials** and **Settings > Add Instance**. No environment variables needed for API keys.

Optional env vars for deployment tuning:

| Variable | Default | Description |
|---|---|---|
| `CANTINARR_PORT` | `8585` | HTTP listen port |
| `CANTINARR_SERVER_NAME` | `Cantinarr` | Display name shown in clients |
| `CANTINARR_JWT_SECRET` | auto-generated | HMAC secret for JWT signing |
| `CANTINARR_APPLE_APP_IDS` | unset | Comma-separated `TeamID.BundleID` values for native Apple passkeys |
| `CANTINARR_ANDROID_PACKAGE_NAME` | `com.cantinarr.cantinarr` | Android package name for native passkeys |
| `CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS` | unset | Comma-separated Android signing certificate SHA-256 fingerprints for Digital Asset Links and native origin validation |
| `CANTINARR_WEBAUTHN_EXTRA_ORIGINS` | unset | Comma-separated additional WebAuthn origins to trust |

Native app passkeys require a public HTTPS server domain associated with the app. Cantinarr serves `/.well-known/apple-app-site-association` when `CANTINARR_APPLE_APP_IDS` is set, and `/.well-known/assetlinks.json` when Android signing fingerprints are set. Windows native passkeys use the `https://host` origin; deployments served from a non-standard HTTPS port should add that origin to `CANTINARR_WEBAUTHN_EXTRA_ORIGINS`. Browser passkey setup remains available for deployments that cannot use native app association.

## API Reference

### Authentication
```
POST   /api/auth/login          # { username, password } -> tokens + user
POST   /api/auth/refresh        # { refresh_token } -> new tokens
GET    /api/auth/me             # current user profile
```

### Configuration
```
GET    /api/config              # server name, available services, instances
GET    /api/health              # { status: "ok" }
```

### Credentials (admin only)
```
GET    /api/admin/credentials          # which credentials are set (booleans, never values)
PUT    /api/admin/credentials          # set/update credentials
DELETE /api/admin/credentials/:key     # remove a credential
```

### Requests
```
POST   /api/requests            # { tmdb_id, media_type } -> bridged add to arr
GET    /api/requests/:id/status # availability + download progress
GET    /api/requests            # request history for current user
```

### AI Chat
```
POST   /api/ai/chat             # SSE-streamed AI conversation with tool use
GET    /api/ai/available        # { available: true/false }
```
The chat request accepts an optional `conversation_id`; when supplied, the server
replays its provider-neutral stored transcript (including tool results) so follow-up
turns keep full grounding across Anthropic, OpenAI, and Gemini. SSE frames:
`{conversation_id}`, `{text}`, `{tool_start: {name, label}}`, `{tool_end: {name, ok}}`,
`{media_results}`, `{error}`, then `[DONE]`.

### AI Tool Toggles (admin only)
```
GET    /api/admin/ai-tools       # list tools: { name, description, enabled, admin_only }
PUT    /api/admin/ai-tools/:name # { enabled: bool } -- applies to chat and /mcp immediately
```

### Downloads (admin only)
```
GET    /api/downloads/:id/queue                 # unified SABnzbd/qBittorrent/NZBGet/Transmission queue
POST   /api/downloads/:id/pause|resume          # whole queue
POST   /api/downloads/:id/queue/:item/pause|resume
DELETE /api/downloads/:id/queue/:item?deleteData=bool
GET    /api/downloads/:id/history?limit=50
```

### Tautulli (admin only)
```
GET    /api/tautulli/:id/activity   # current Plex streams + bandwidth
GET    /api/tautulli/:id/history?limit=50
GET    /api/tautulli/:id/stats?days=30
```

### MCP (external tool access)
```
POST   /mcp                     # MCP JSON-RPC (initialize, tools/list, tools/call)
GET    /mcp                     # SSE stream for async notifications
DELETE /mcp                     # Close MCP session
```
Requires `Authorization: Bearer <token>` header. Session tracked via `Mcp-Session-Id` header.

### Instance Proxy (admin only)
```
GET|POST|DELETE  /api/instances/:id/*  # passthrough to specific Radarr/Sonarr instance
```

### Real-time
```
WS     /api/ws                  # WebSocket (JWT via subprotocol header)
```

WebSocket events:
- `download_progress` -- `{ tmdb_id, media_type, progress, status }`
- `request_status_changed` -- `{ tmdb_id, media_type, status }`
- `downloads_queue` -- full download-client queue snapshot `{ instance_id, paused, speed_bps, items }`, sent on change
- `arr_queue_changed` -- `{ instance_id, service_type }` invalidation ping; refetch the queue via REST

Secrets at rest: instance API keys/passwords, external credentials, and the JWT
secret are AES-256-GCM encrypted. Key: `CANTINARR_ENCRYPTION_KEY` env var
(base64, 32 bytes) or the auto-generated `/config/encryption.key` (0600). Keep
that file with your database backups -- without it encrypted secrets are
unrecoverable.

## Architecture

### ID Bridge (TMDB-to-TVDB)

The key innovation. TMDB has the best metadata and images, but Sonarr only speaks TVDB. The bridge translates transparently:

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
POST sonarr/api/v3/series  (add with defaults)
```

Movies skip bridging entirely -- Radarr natively supports `term=tmdb:{id}`.

### MCP Tools

The same 26 tools power both the in-app AI assistant and the external MCP endpoint. Tools marked admin require an admin account; every tool can be disabled from Settings > AI Tools:

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
| `request_media` | Actually add to Radarr/Sonarr |
| `list_my_requests` | User's request history |
| `display_media` | Curate the visual results carousel |
| `get_queue` | Combined Radarr/Sonarr download queue |
| `get_calendar` | Upcoming releases |
| `get_library` | What's on the server (filterable) |
| `get_history` | Recent grabs/imports/failures |
| `trigger_search` | Kick off an automatic download search |
| `search_releases` | Interactive indexer release search (admin) |
| `grab_release` | Download a specific release (admin) |
| `remove_queue_item` | Remove/blocklist a queue item (admin) |
| `get_disk_space` | Disk space across instances (admin) |
| `get_arr_health` | Radarr/Sonarr system health: download client, remote path mapping, indexers, disk, root folder (admin) |
| `diagnose_queue` | Import Doctor: explain stuck/failed/blocked queue items, print the exact next tool call, and the fix to apply (admin) |
| `get_manual_import_candidates` | List a stuck download's files, mappings, and rejection reasons (admin) |
| `execute_manual_import` | Force a download's files into the library via manual import (admin) |
| `remediate_queue_item` | One-click queue fix: remove, blocklist+search, or change category (admin) |
| `rescan_media` | Rescan a movie/series on disk and run the import pass (admin) |

### MCP Server Endpoint

Cantinarr exposes these tools as a proper [Model Context Protocol](https://modelcontextprotocol.io/) server at `/mcp` using Streamable HTTP transport. External MCP clients (Claude Desktop, Claude Code, Codex, etc.) can connect directly.

**Authentication:** Uses the MCP HTTP authorization flow: OAuth protected-resource metadata, authorization-server metadata, dynamic client registration, PKCE authorization code flow, and rotating refresh tokens. Clients discover auth from `/mcp`, open a browser login against the Cantinarr server, then keep themselves refreshed without manual token copying.

The MCP server also publishes prompt templates and a `guide://cantinarr/agent-guide.md` resource so external agents can pick up the same operating habits as the built-in assistant: mixed movie/TV trending behavior, carousel use via `display_media`, request-status checks before requests, and admin download triage rules.

Users can authorize MCP with a Cantinarr password or a passkey. Connect-link-only users can create their first passkey from the MCP login page: Cantinarr opens the app when available, and the app can mint a short-lived browser setup URL when the platform cannot create passkeys natively.

**Client example**:
```json
{
  "mcpServers": {
    "cantinarr": {
      "url": "http://your-server:8585/mcp"
    }
  }
}
```

MCP access tokens are short-lived and audience-bound to `/mcp`. MCP refresh tokens are persisted in SQLite, rotate on use, have a one-year sliding lifetime, and are tied to a Cantinarr device record so revoking the device also revokes the MCP client. Tokens survive server restarts and upgrades because registered OAuth clients and refresh token state live in the database.

### Database

SQLite with WAL mode for concurrent reads. Core tables:

- `users` -- accounts with bcrypt password hashes
- `request_log` -- audit trail of all requests
- `tmdb_tvdb_cache` -- ID bridge cache (30-day TTL)
- `oauth_clients`, `oauth_authorization_codes`, `oauth_refresh_tokens` -- MCP OAuth client and token state

## Project Structure

```
server/
├── cmd/server/main.go              # Entry point, dependency wiring
├── internal/
│   ├── ai/
│   │   ├── handler.go              # SSE streaming chat endpoint
│   │   ├── service.go              # Anthropic streaming API client + tool loop
│   │   └── http_providers.go       # OpenAI/Gemini streaming API clients + tool loops
│   ├── api/router.go               # Chi router, CORS, middleware, routes
│   ├── auth/
│   │   ├── handler.go              # Login, refresh, connect token
│   │   ├── middleware.go           # JWT validation + admin gate
│   │   ├── models.go              # User, tokens, request/response types
│   │   └── service.go             # Auth business logic, JWT signing
│   ├── config/config.go           # Server configuration (port, name)
│   ├── credentials/
│   │   ├── handler.go             # Admin credential REST endpoints
│   │   └── registry.go            # Lazy client caching + invalidation
│   ├── db/db.go                   # SQLite setup, WAL mode, migrations
│   ├── mcp/
│   │   ├── server.go              # Tool execution engine
│   │   └── tools.go               # 9 tool definitions + implementations
│   ├── mcpserver/
│   │   ├── server.go              # MCP Streamable HTTP endpoint (mcp-go)
│   │   ├── context.go             # Auth context -> MCP context bridge
│   │   ├── guidance.go            # MCP prompts + agent guide resource
│   │   └── tools.go               # Registers existing tools with mcp-go
│   ├── proxy/handler.go           # Reverse proxy for arr admin access
│   ├── radarr/client.go           # Radarr API v3 client
│   ├── request/
│   │   ├── handler.go             # Request HTTP handlers
│   │   └── service.go             # Orchestration: TMDB -> bridge -> arr
│   ├── sonarr/client.go           # Sonarr API v3 client
│   ├── tmdb/
│   │   ├── bridge.go              # TMDB/Trakt -> TVDB resolution + cache
│   │   └── client.go              # TMDB API v3 client
│   ├── trakt/client.go            # Trakt API v2 (fallback ID resolver)
│   ├── web/
│   │   ├── embed.go               # go:embed for Flutter web assets
│   │   └── handler.go             # SPA-aware file server
│   └── websocket/hub.go           # Real-time events, arr queue polling
├── migrations/001_init.sql        # Schema reference
├── Dockerfile                     # API-only build
├── docker-compose.yml             # API-only compose
└── go.mod
```

## Tech Stack

- **Go** with [Chi](https://github.com/go-chi/chi) router
- **SQLite** via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO)
- **JWT** via [golang-jwt](https://github.com/golang-jwt/jwt)
- **WebSocket** via [gorilla/websocket](https://github.com/gorilla/websocket)
- **Anthropic Messages API** (SDK streaming), **OpenAI Chat Completions API** (streaming), and **Gemini streamGenerateContent API**

## License

See the root repository for license information.
