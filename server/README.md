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
    │       │      │  Rosetta    │      │              │
    │       │      │  Stone      │   MCP Tools         │
    │       │      │  Bridge     │      │              │
    │       │      └──┬─────┬───┘      │              │
    │       │         │     │          │              │
    │       │    Radarr   Sonarr    Anthropic         │
    │       │         │     │       API               │
    │   Flutter Web (embedded)     WebSocket Hub      │
    └──────────────────────────────────────────────────┘
```

## Features

- **One-tap requests** -- Users browse TMDB/Trakt on the app, tap request, and the server handles everything: ID bridging, arr lookups, quality profiles, root folders.
- **TMDB-to-TVDB Rosetta Stone** -- Transparently translates TMDB IDs to TVDB IDs for Sonarr. Falls back to Trakt cross-references, then title+year search. Results cached in SQLite for 30 days.
- **Connect link auth** -- Admin generates one-time connect links for household members. JWT-based sessions with 7-day access / 30-day refresh tokens.
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
    environment:
      CANTINARR_TMDB_KEY: "your-tmdb-api-key"        # required
      CANTINARR_ADMIN_PASSWORD: "your-admin-password" # required on first run
      CANTINARR_RADARR_URL: "http://radarr:7878"      # optional
      CANTINARR_RADARR_KEY: "radarr-api-key"
      CANTINARR_SONARR_URL: "http://sonarr:8989"      # optional
      CANTINARR_SONARR_KEY: "sonarr-api-key"
      CANTINARR_ANTHROPIC_KEY: "sk-ant-..."           # optional, enables AI chat
      CANTINARR_TRAKT_CLIENT_ID: "trakt-client-id"    # optional, enhances discovery
    restart: unless-stopped
```

```bash
docker-compose up -d
```

Open `http://your-server:8585` and log in with `admin` / your admin password.

### From Source

```bash
# Requires Go 1.22+
cd server
go build -o cantinarr ./cmd/server

# Set required env vars
export CANTINARR_TMDB_KEY="..."
export CANTINARR_ADMIN_PASSWORD="..."

./cantinarr
```

## Configuration

All configuration is via environment variables with the `CANTINARR_` prefix.

| Variable | Required | Default | Description |
|---|---|---|---|
| `CANTINARR_TMDB_KEY` | Yes | -- | TMDB API v3 key |
| `CANTINARR_ADMIN_PASSWORD` | First run | -- | Creates the `admin` account |
| `CANTINARR_RADARR_URL` | No | -- | Radarr base URL (e.g. `http://radarr:7878`) |
| `CANTINARR_RADARR_KEY` | No | -- | Radarr API key |
| `CANTINARR_SONARR_URL` | No | -- | Sonarr base URL (e.g. `http://sonarr:8989`) |
| `CANTINARR_SONARR_KEY` | No | -- | Sonarr API key |
| `CANTINARR_ANTHROPIC_KEY` | No | -- | Anthropic API key (enables AI chat) |
| `CANTINARR_TRAKT_CLIENT_ID` | No | -- | Trakt client ID (enhances discovery + fallback bridging) |
| `CANTINARR_TRAKT_CLIENT_SECRET` | No | -- | Trakt client secret |
| `CANTINARR_JWT_SECRET` | No | auto-generated | HMAC secret for JWT signing |
| `CANTINARR_DB_PATH` | No | `/config/cantinarr.db` | SQLite database path |
| `CANTINARR_PORT` | No | `8585` | HTTP listen port |
| `CANTINARR_SERVER_NAME` | No | `Cantinarr` | Display name shown in clients |

## API Reference

### Authentication
```
POST   /api/auth/login          # { username, password } -> tokens + user
POST   /api/auth/refresh        # { refresh_token } -> new tokens
GET    /api/auth/me             # current user profile
```

### Configuration
```
GET    /api/config              # TMDB key, Trakt ID, server name, available services
GET    /api/health              # { status: "ok" }
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

### Arr Proxy (admin only)
```
GET|POST|DELETE  /api/radarr/*  # passthrough to Radarr
GET|POST|DELETE  /api/sonarr/*  # passthrough to Sonarr
```

### Real-time
```
WS     /api/ws                  # WebSocket (JWT via subprotocol header)
```

WebSocket events:
- `download_progress` -- `{ tmdb_id, media_type, progress, status }`
- `request_status_changed` -- `{ tmdb_id, media_type, status }`

## Architecture

### The Rosetta Stone Bridge

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

The AI assistant has 9 server-side tools:

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
│   ├── config/config.go           # Environment variable loading
│   ├── db/db.go                   # SQLite setup, WAL mode, migrations
│   ├── mcp/
│   │   ├── server.go              # Tool execution engine
│   │   └── tools.go               # 9 tool definitions + implementations
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
