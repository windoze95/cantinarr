# Cantinarr

A seamless media request and *arr management server + app.

Cantinarr makes it dead simple for your family and friends to discover and request movies and TV shows. They browse a beautiful interface powered by TMDB and Trakt, tap "Request," and the server handles everything behind the scenes -- adding to Radarr or Sonarr with the right IDs, the right quality profile, the right root folder. No configuration, no confusion, no TVDB headaches.

```
┌─────────────────────────────────────────────────────────┐
│  Cantinarr Server (Go, single container, port 8585)     │
│                                                         │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────────┐  │
│  │ Auth/JWT │  │ Request  │  │ AI Chat               │  │
│  │          │  │ Service  │  │ + 19 MCP Tools        │  │
│  └──────────┘  └────┬─────┘  └───────────────────────┘  │
│                     │                                    │
│  ┌──────────────────┼──────────────────────┐             │
│  │   ID Bridge                             │             │
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
- **Automatic ID bridging** -- TMDB-to-TVDB translation with Trakt fallback. The #1 source of failed Sonarr adds, solved.
- **AI assistant** -- "What should I watch tonight?" Bring Anthropic, OpenAI, or Gemini; the assistant searches your library, checks availability, and can request for you. Admins can also manage the server conversationally: check the queue, kick off searches, grab a specific release from the indexers, or clean up failed downloads.
- **MCP server** -- The same 19 AI tools are exposed as a [Model Context Protocol](https://modelcontextprotocol.io/) endpoint at `/mcp`, with MCP OAuth discovery, browser login, dynamic client registration, and persistent rotating refresh tokens. Every tool can be toggled on/off per server from Settings > AI Tools.
- **Download clients** -- SABnzbd, qBittorrent, NZBGet, and Transmission modules with live queue management (pause, resume, remove, speeds, real-time push updates) alongside full Radarr/Sonarr control: queue actions, history, wanted/missing, calendar, and interactive release search.
- **Tautulli** -- watch what's playing on Plex right now: active streams with quality/transcode badges, watch history, and top movies/shows/users stats.
- **Secrets encrypted at rest** -- arr API keys, download-client passwords, and external credentials are AES-256-GCM encrypted in the database.
- **Household-friendly** -- Connect links, role-based access. Admins manage arr services, users just browse and request.
- **Single container** -- One Go binary, one port, serves API + web UI. Runs great on a Raspberry Pi or NAS.

## Quick Start

```bash
git clone https://github.com/windoze95/cantinarr.git
cd cantinarr
docker compose up -d
```

Open `http://your-server:8585` -- the setup wizard walks you through creating an admin account. Then configure your services (TMDB, Radarr, Sonarr, etc.) from **Settings > API Credentials** in the admin UI.

### From Source

```bash
# Server (requires Go 1.22+)
cd server
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
│   │   ├── ai/             # Multi-provider AI chat with SSE streaming
│   │   ├── api/            # Chi router, middleware, routes
│   │   ├── auth/           # JWT, bcrypt, connect tokens
│   │   ├── config/         # Server configuration (port, name)
│   │   ├── credentials/   # API credential management + client registry
│   │   ├── db/             # SQLite with WAL mode
│   │   ├── mcp/            # 19 AI tools + per-tool toggles
│   │   ├── downloads/      # Unified SABnzbd/qBittorrent queue API
│   │   ├── mcpserver/      # MCP Streamable HTTP endpoint
│   │   ├── proxy/          # Arr admin reverse proxy
│   │   ├── nzbget/         # NZBGet JSON-RPC client
│   │   ├── qbittorrent/    # qBittorrent WebUI v2 client
│   │   ├── radarr/         # Radarr API v3 client
│   │   ├── request/        # Request orchestration + bridging
│   │   ├── sabnzbd/        # SABnzbd JSON API client
│   │   ├── secrets/        # AES-256-GCM secrets-at-rest
│   │   ├── sonarr/         # Sonarr API v3 client
│   │   ├── tautulli/       # Tautulli activity/history/stats
│   │   ├── tmdb/           # TMDB client + ID bridge
│   │   ├── transmission/   # Transmission RPC client
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

All service credentials are managed through the admin UI -- no environment variables needed for API keys.

| Setting | Where | Description |
|---|---|---|
| TMDB access token | Admin UI | Required for media discovery and search ([get one here](https://www.themoviedb.org/settings/api)) |
| Radarr/Sonarr instances | Admin UI | Add via Settings > Add Instance |
| SABnzbd/qBittorrent/NZBGet/Transmission instances | Admin UI | Download client modules (queue, history, speeds) |
| Tautulli instance | Admin UI | Plex activity, watch history, stats |
| Anthropic/OpenAI/Gemini API key | Admin UI | Enables AI assistant for the selected provider |
| Trakt client ID | Admin UI | Enhances discovery + fallback ID bridging |

Optional server env vars for deployment tuning:

| Variable | Default | Description |
|---|---|---|
| `CANTINARR_PORT` | `8585` | HTTP listen port |
| `CANTINARR_SERVER_NAME` | `Cantinarr` | Display name shown in clients |
| `CANTINARR_JWT_SECRET` | auto-generated | HMAC secret for JWT signing |
| `CANTINARR_AI_PROVIDER` | `anthropic` | AI provider used by the assistant (`anthropic`, `openai`, or `gemini`) |
| `CANTINARR_AI_MODEL` | provider default | Model used by the AI assistant when no admin UI model is saved |
| `CANTINARR_ENCRYPTION_KEY` | auto-generated key file | Base64 32-byte key for secrets-at-rest (default: `/config/encryption.key`) |
| `CANTINARR_APPLE_APP_IDS` | unset | Comma-separated `TeamID.BundleID` values for native Apple passkeys; served from `/.well-known/apple-app-site-association` |
| `CANTINARR_ANDROID_PACKAGE_NAME` | `com.cantinarr.cantinarr` | Android package name for native passkeys |
| `CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS` | unset | Comma-separated Android signing certificate SHA-256 fingerprints for `/.well-known/assetlinks.json` and Android WebAuthn origin validation |
| `CANTINARR_WEBAUTHN_EXTRA_ORIGINS` | unset | Comma-separated additional WebAuthn origins to trust, for native clients or advanced deployments |

Native app passkeys require a public HTTPS server domain that is associated with the app. Apple devices verify `/.well-known/apple-app-site-association`; Android credential providers verify `/.well-known/assetlinks.json` and return an `android:apk-key-hash` origin that Cantinarr validates from the configured signing certificate fingerprint. Browser setup remains available when a self-hosted deployment cannot satisfy native app association.

## How It Works

### For Users
1. Admin sends you a connect link
2. Open the link on your device -- it creates your account and connects automatically
3. Browse movies and TV shows powered by TMDB and Trakt
4. Tap "Request" on anything you want -- it just works
5. Watch download progress in real-time via WebSocket updates
6. Ask the AI assistant for recommendations or to make requests

### For Admins
1. Deploy the container and complete the setup wizard
2. Add your API credentials and Radarr/Sonarr instances from Settings
3. Generate connect links for your household
4. Users' requests flow through the ID bridge to the correct arr service
5. Manage everything from the app -- no config files or env vars to edit

### ID Bridge (TMDB-to-TVDB)

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
| Auth | JWT (HS256), bcrypt, connect tokens |
| AI | Anthropic, OpenAI, or Gemini APIs with SSE app streaming |
| MCP | [mcp-go](https://github.com/mark3labs/mcp-go), Streamable HTTP |
| Real-time | gorilla/websocket |
| Discovery | TMDB API v3, Trakt API v2 |
| Packaging | Multi-stage Docker, go:embed |

## API Reference

Full API documentation is in [`server/README.md`](server/README.md#api-reference).

## Contributing

Contributions are welcome! Please open an issue to discuss your idea before submitting a PR.

## License

AGPL-3.0 — See [LICENSE](LICENSE) for details.

Copyright (c) 2026 Julian Dice
