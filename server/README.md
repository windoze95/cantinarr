# Cantinarr Server

The backend brain for [Cantinarr](https://github.com/windoze95/cantinarr) -- a self-hosted media request app for Plex and Jellyfin households.

A single Go binary that bridges your arr stack, serves the web UI, and keeps API keys off user devices. Drop it on your NAS, point it at Radarr/Sonarr, and generate connect links for family and friends.

```
                        Cantinarr Server (:8585)
    ┌──────────────────────────────────────────────────┐
    │                                                  │
    │   Auth (JWT)    Requests    AI Chat (Claude)     │
    │       │             │             │              │
    │       │      ┌──────┴──────┐      │              │
    │       │      │    ID       │      │              │
    │       │      │   Bridge    │   MCP Tools         │
    │       │      │             │      │              │
    │       │      └──┬─────┬───┘      │              │
    │       │         │     │          │              │
    │       │    Radarr   Sonarr    Anthropic         │
    │       │         │     │       API               │
    │   Flutter Web (embedded)     WebSocket Hub      │
    └──────────────────────────────────────────────────┘
```

## Features

- **One-tap requests** -- Users browse TMDB/Trakt on the app, tap request, and the server handles everything: ID bridging, arr lookups, quality profiles, root folders.
- **Automatic ID bridging** -- Transparently translates TMDB IDs to TVDB IDs for Sonarr. Falls back to Trakt cross-references, then title+year search. Results cached in SQLite for 30 days.
- **Connect link auth** -- Admin generates one-time connect links for household members. JWT-based sessions with 15-minute access / 30-day refresh tokens.
- **AI assistant** -- Claude-powered chat with server-side tool execution (search, recommendations, request status, make requests). Streams responses via SSE.
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

Service credentials (TMDB, Anthropic, Trakt) and Radarr/Sonarr instances are managed through the admin UI at **Settings > API Credentials** and **Settings > Add Instance**. No environment variables needed for API keys.

Optional env vars for deployment tuning:

| Variable | Default | Description |
|---|---|---|
| `CANTINARR_PORT` | `8585` | HTTP listen port |
| `CANTINARR_SERVER_NAME` | `Cantinarr` | Display name shown in clients |
| `CANTINARR_JWT_SECRET` | auto-generated | HMAC secret for JWT signing |

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
POST   /api/ai/chat             # SSE-streamed Claude conversation with tool use
GET    /api/ai/available        # { available: true/false }
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

The same 9 tools power both the in-app AI assistant and the external MCP endpoint:

| Tool | Description |
|---|---|
| `search_movies` | Search TMDB for movies |
| `search_tv_shows` | Search TMDB for TV shows |
| `get_trending` | Trending movies/shows by day or week |
| `get_movie_details` | Full movie metadata |
| `get_tv_details` | Full TV show metadata |
| `get_recommendations` | Similar content suggestions |
| `check_request_status` | Is this on my server? |
| `request_media` | Actually add to Radarr/Sonarr |
| `list_my_requests` | User's request history |

### MCP Server Endpoint

Cantinarr exposes these tools as a proper [Model Context Protocol](https://modelcontextprotocol.io/) server at `/mcp` using Streamable HTTP transport. External MCP clients (Claude Desktop, Claude Code, etc.) can connect directly.

**Authentication:** Uses Cantinarr's existing JWT Bearer tokens. Pass an access token via the `Authorization` header.

**Claude Desktop example** (`claude_desktop_config.json`):
```json
{
  "mcpServers": {
    "cantinarr": {
      "url": "http://your-server:8585/mcp",
      "headers": {
        "Authorization": "Bearer <access-token>"
      }
    }
  }
}
```

**Limitations:**
- Access tokens expire after 15 minutes. MCP clients cannot automatically refresh them because the endpoint uses Cantinarr's JWT auth rather than the MCP spec's OAuth 2.1 flow. Long MCP sessions will require manually providing a fresh access token.
- A future update may implement the MCP OAuth 2.1 authorization spec, which would allow clients to handle login and token refresh automatically via browser-based auth.

### Database

SQLite with WAL mode for concurrent reads. Four tables:

- `users` -- accounts with bcrypt password hashes
- `request_log` -- audit trail of all requests
- `tmdb_tvdb_cache` -- ID bridge cache (30-day TTL)

## Project Structure

```
server/
├── cmd/server/main.go              # Entry point, dependency wiring
├── internal/
│   ├── ai/
│   │   ├── handler.go              # SSE streaming chat endpoint
│   │   └── service.go              # Anthropic API client + tool loop
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
│   │   ├── context.go             # JWT auth -> MCP context bridge
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
- **Anthropic Messages API** (raw HTTP, no SDK)

## License

See the root repository for license information.
