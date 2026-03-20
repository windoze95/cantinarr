# Cantinarr

The flashiest, most seamless media request app for Plex and Jellyfin households.

Cantinarr makes it dead simple for your family and friends to discover and request movies and TV shows. They browse a beautiful interface powered by TMDB and Trakt, tap "Request," and the server handles everything behind the scenes -- adding to Radarr or Sonarr with the right IDs, the right quality profile, the right root folder. No configuration, no confusion, no TVDB headaches.

```
┌─────────────────────────────────────────────────────────┐
│  Cantinarr Server (Go, single container, port 8585)     │
│                                                         │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────────┐  │
│  │ Auth/JWT │  │ Request  │  │ AI Chat (Claude)      │  │
│  │          │  │ Service  │  │ + 9 MCP Tools         │  │
│  └──────────┘  └────┬─────┘  └───────────────────────┘  │
│                     │                                    │
│  ┌──────────────────┼──────────────────────┐             │
│  │   Rosetta Stone Bridge                  │             │
│  │   TMDB → Trakt → TVDB (cached 30 days) │             │
│  └──────────────────┼──────────────────────┘             │
│                     │                                    │
│  ┌─────────┐  ┌─────┴───┐  ┌──────────────┐             │
│  │ Radarr  │  │ Sonarr  │  │ Flutter Web  │             │
│  │ Client  │  │ Client  │  │ (embedded)   │             │
│  └────┬────┘  └────┬────┘  └──────────────┘             │
└───────┼─────────────┼────────────────────────────────────┘
        │             │
   ┌────▼────┐  ┌─────▼───┐
   │ Radarr  │  │ Sonarr  │   (your existing services)
   └─────────┘  └─────────┘

┌───────────────────────────────┐
│  Cantinarr App (Flutter)      │
│                               │     ┌──────────┐
│  Discovery ───────────────────┼────>│ TMDB API │  (direct, fast)
│  Trending & Lists ────────────┼────>│Trakt API │  (direct, fast)
│  Requests / Arr / AI ────────┼────>│ Backend  │  (proxied)
│                               │     └──────────┘
└───────────────────────────────┘
```

## Why Cantinarr?

- **Zero-config requesting** -- Your users never see API keys, TVDB IDs, or quality profiles. They browse, they tap, it works.
- **TMDB + Trakt for discovery** -- The best metadata, images, and trending data. Sonarr's TVDB dependency is invisible.
- **The Rosetta Stone** -- Automatic TMDB-to-TVDB ID bridging with Trakt fallback. The #1 source of failed Sonarr adds, solved.
- **AI assistant** -- "What should I watch tonight?" Claude searches your library, checks availability, and can request for you.
- **Household-friendly** -- Invite codes, role-based access. Admins manage arr services, users just browse and request.
- **Single container** -- One Go binary, one port, serves API + web UI. Runs great on a Raspberry Pi or NAS.

## Quick Start

```bash
git clone https://github.com/windoze95/cantinarr.git
cd cantinarr
```

### Docker (recommended)

```bash
cp .env.example .env
# Edit .env with your TMDB key, admin password, and arr URLs

docker compose up -d
```

Open `http://your-server:8585` -- log in as `admin` with your configured password.

### From Source

```bash
# Server (requires Go 1.22+)
cd server
export CANTINARR_TMDB_KEY="your-key"
export CANTINARR_ADMIN_PASSWORD="your-password"
go run ./cmd/server

# App (requires Flutter 3.2+)
cd app
flutter pub get
flutter run
```

## Repository Structure

```
cantinarr/
├── server/                 # Go backend
│   ├── cmd/server/         # Entry point
│   ├── internal/
│   │   ├── ai/             # Claude AI chat with SSE streaming
│   │   ├── api/            # Chi router, middleware, routes
│   │   ├── auth/           # JWT, bcrypt, invite codes
│   │   ├── config/         # Environment variable loading
│   │   ├── db/             # SQLite with WAL mode
│   │   ├── mcp/            # 9 AI tools (search, request, status)
│   │   ├── proxy/          # Arr admin reverse proxy
│   │   ├── radarr/         # Radarr API v3 client
│   │   ├── request/        # Request orchestration + bridging
│   │   ├── sonarr/         # Sonarr API v3 client
│   │   ├── tmdb/           # TMDB client + Rosetta Stone bridge
│   │   ├── trakt/          # Trakt fallback ID resolver
│   │   ├── web/            # Flutter web embed (go:embed)
│   │   └── websocket/      # Real-time download progress
│   ├── Dockerfile          # API-only build
│   └── README.md           # Server documentation
│
├── app/                    # Flutter client (iOS, Android, Web)
│   ├── lib/
│   │   ├── core/           # Models, networking, theme, widgets
│   │   ├── features/       # Auth, discover, request, AI, arr, settings
│   │   └── navigation/     # GoRouter with auth guard
│   └── README.md           # App documentation
│
├── Dockerfile              # Multi-stage build (Flutter web + Go)
├── docker-compose.yml      # Full-stack deployment
└── README.md               # This file
```

## Configuration

All via environment variables with the `CANTINARR_` prefix:

| Variable | Required | Description |
|---|---|---|
| `CANTINARR_TMDB_KEY` | Yes | TMDB API v3 key ([get one here](https://www.themoviedb.org/settings/api)) |
| `CANTINARR_ADMIN_PASSWORD` | First run | Password for the `admin` account |
| `CANTINARR_RADARR_URL` | No | Radarr URL (e.g. `http://radarr:7878`) |
| `CANTINARR_RADARR_KEY` | No | Radarr API key |
| `CANTINARR_SONARR_URL` | No | Sonarr URL (e.g. `http://sonarr:8989`) |
| `CANTINARR_SONARR_KEY` | No | Sonarr API key |
| `CANTINARR_ANTHROPIC_KEY` | No | Enables AI assistant |
| `CANTINARR_TRAKT_CLIENT_ID` | No | Enhances discovery + fallback ID bridging |

See [`server/README.md`](server/README.md) for the full configuration reference.

## How It Works

### For Users
1. Admin gives you a 6-character invite code and a server URL
2. Open the app, enter the URL and code, pick a username
3. Browse movies and TV shows powered by TMDB and Trakt
4. Tap "Request" on anything you want -- it just works
5. Watch download progress in real-time via WebSocket updates
6. Ask the AI assistant for recommendations or to make requests

### For Admins
1. Deploy the container, set your TMDB key and arr connections
2. Log in as `admin`, generate invite codes for your household
3. Users' requests flow through the Rosetta Stone bridge to the correct arr service
4. Manage Radarr/Sonarr from the app (admin tabs) without exposing API keys
5. Optionally add an Anthropic key for AI chat features

### The Rosetta Stone Bridge (TMDB-to-TVDB)

The core technical challenge: TMDB has better metadata and APIs, but Sonarr only accepts TVDB IDs. Cantinarr solves this transparently:

```
Request: "Add The Last of Us" (TMDB ID 100088)

1. Cache check     -> miss
2. TMDB external_ids API -> tvdb_id: 392256 (hit!)
3. Cache result (30 days)
4. Sonarr lookup by tvdb:392256 -> exact match
5. Add to Sonarr with default quality profile + root folder
```

If TMDB doesn't have a TVDB mapping (rare), the bridge falls back to Trakt's cross-reference database, then to a title+year search as a last resort.

Movies don't need bridging -- Radarr natively supports TMDB IDs.

## Tech Stack

| Component | Technology |
|---|---|
| Server | Go, Chi router, SQLite (pure Go) |
| Client | Flutter (Dart), Riverpod, GoRouter |
| Auth | JWT (HS256), bcrypt, invite codes |
| AI | Anthropic Claude API, SSE streaming |
| Real-time | gorilla/websocket |
| Discovery | TMDB API v3, Trakt API v2 |
| Packaging | Multi-stage Docker, go:embed |

## API Reference

Full API documentation is in [`server/README.md`](server/README.md#api-reference).

## Contributing

Contributions are welcome! Please open an issue to discuss your idea before submitting a PR.

## License

MIT
