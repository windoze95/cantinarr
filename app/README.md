# Cantinarr Flutter App

The mobile and web client for [Cantinarr](https://github.com/windoze95/cantinarr) -- a frictionless media request app for Plex and Jellyfin households.

Built with Flutter for iOS, Android, and web. Features a dark-first design with TMDB-powered discovery, one-tap requests, Trakt-powered trending, and an AI assistant -- all backed by the Cantinarr server.

```
┌──────────────────────────────────────────────────────┐
│                  Cantinarr App                       │
│                                                      │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐           │
│  │ Discover │  │  Movies  │  │ TV Shows │  Assistant │
│  │  (TMDB)  │  │ (Radarr) │  │ (Sonarr) │  (Claude) │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘     │     │
│       │              │             │           │     │
│       ▼              ▼             ▼           ▼     │
│   TMDB API      ┌─────────────────────────────┐     │
│   Trakt API     │   Cantinarr Backend         │     │
│   (direct)      │   (auth, requests, arr, AI) │     │
│                 └─────────────────────────────┘     │
└──────────────────────────────────────────────────────┘
```

## Design

Dark-first UI with warm gold accents, designed for couch browsing.

| Color | Hex | Usage |
|---|---|---|
| Accent | `#E5A00D` | Gold -- buttons, active states, brand |
| Background | `#0F0F1A` | Near-black canvas |
| Surface | `#1A1A2E` | Cards, nav, sheets |
| Text Primary | `#F0F0F0` | Headings, body |
| Text Secondary | `#9E9EB8` | Labels, hints |
| Available | `#4CAF50` | Green -- on server |
| Requested | `#FFA726` | Orange -- pending |
| Downloading | `#42A5F5` | Blue -- in progress |

## Features

### Discovery
- **TMDB-powered browsing** -- trending, popular, top rated, upcoming, now playing
- **Trakt integration** -- trending by actual watch activity, community-curated lists, calendar for upcoming episodes
- **Multi-search** -- movies, TV shows, and people in one search bar
- **Rich detail screens** -- cast, trailers, seasons, episode lists, recommendations
- **Filter & discover** -- by genre, year, watch providers, rating

### Requests
- **One-tap requesting** -- tap a button, the server handles everything
- **No TVDB headaches** -- the backend's Rosetta Stone bridge translates TMDB IDs to TVDB IDs transparently
- **Real-time status** -- WebSocket-powered download progress, live status updates
- **Status indicators** -- available (green), requested (orange), downloading (blue), unavailable (grey)

### AI Assistant
- **Claude-powered chat** -- ask for recommendations, check availability, make requests via conversation
- **SSE streaming** -- responses appear incrementally, not all-at-once
- **Server-side tools** -- the AI can search TMDB, check your server, and make requests on your behalf

### Auth
- **Server URL + login** -- point at your Cantinarr server, enter credentials
- **Connect links** -- new users connect via a one-time link from their admin
- **Automatic session restore** -- JWT stored in secure storage, auto-refreshes on 401

## Getting Started

### Prerequisites
- Flutter SDK 3.2+
- A running [Cantinarr server](../server/) instance

### Run the app
```bash
cd app
flutter pub get
flutter run
```

### Build for web (embedded in server)
```bash
flutter build web --release
# Output in build/web/ -- copied into the Go binary during Docker build
```

## Architecture

The app follows a **feature-first** structure with clear separation between data, logic, and UI layers. State management uses [Riverpod](https://riverpod.dev/) throughout.

### Data Flow

```
┌─────────────┐     ┌──────────────┐     ┌─────────────┐
│   Screens   │────>│  Providers   │────>│  Services   │
│   (UI)      │<────│  (Logic)     │<────│  (Data)     │
└─────────────┘     └──────────────┘     └─────────────┘
                                               │
                                    ┌──────────┴──────────┐
                                    │                     │
                              Direct APIs          Backend API
                              (TMDB, Trakt)        (Auth, Requests,
                                                    Arr, AI, WS)
```

### What talks to what

| Feature | Data Source | Why |
|---|---|---|
| Browse & search | TMDB API (direct) | Speed -- one hop, CDN images, client IP rate limits |
| Trending & lists | Trakt API (direct) | Real watch data, community curation |
| Requests | Backend `/api/requests` | Server handles TMDB-to-TVDB bridging |
| Movie management | Backend `/api/radarr/*` | API keys stay on server |
| TV management | Backend `/api/sonarr/*` | API keys stay on server |
| AI chat | Backend `/api/ai/chat` | Anthropic key + tool execution on server |
| Real-time events | Backend `/api/ws` | WebSocket for download progress |
| Auth | Backend `/api/auth/*` | JWT tokens |
| Config | Backend `/api/config` | TMDB key, Trakt ID, available services |

## Project Structure

```
app/
├── lib/
│   ├── main.dart                              # Entry point
│   ├── app.dart                               # MaterialApp with Riverpod
│   │
│   ├── core/
│   │   ├── config/app_config.dart             # Constants, TMDB image URLs
│   │   ├── models/
│   │   │   ├── backend_connection.dart        # Server URL, JWT, services
│   │   │   └── user_profile.dart              # User ID, name, role
│   │   ├── network/
│   │   │   ├── backend_client.dart            # Authenticated Dio instance
│   │   │   ├── backend_auth_interceptor.dart  # Auto JWT refresh on 401
│   │   │   └── websocket_client.dart          # Real-time events, auto-reconnect
│   │   ├── storage/
│   │   │   ├── secure_storage.dart            # Encrypted JWT + server URL
│   │   │   └── preferences.dart               # SharedPreferences provider
│   │   ├── theme/app_theme.dart               # Dark-first design system
│   │   └── widgets/                           # Shared UI components
│   │       ├── media_card.dart                # Poster card with shimmer
│   │       ├── media_header.dart              # Backdrop + gradient header
│   │       ├── horizontal_item_row.dart       # Scrollable media row
│   │       ├── shimmer_loading.dart           # Loading placeholders
│   │       ├── search_bar.dart                # Debounced search input
│   │       └── error_banner.dart              # Dismissible error display
│   │
│   ├── features/
│   │   ├── auth/                              # Login & registration
│   │   │   ├── data/auth_service.dart         # Login, register, refresh, config
│   │   │   ├── logic/auth_provider.dart       # Session state (AsyncNotifier)
│   │   │   └── ui/
│   │   │       ├── login_screen.dart          # Server URL + credentials
│   │   │
│   │   ├── discover/                          # Browse & search
│   │   │   ├── data/
│   │   │   │   ├── tmdb_api_service.dart      # TMDB API v3 client
│   │   │   │   ├── tmdb_models.dart           # MediaItem, MovieDetail, TVDetail
│   │   │   │   ├── trakt_api_service.dart     # Trakt API v2 client
│   │   │   │   └── trakt_models.dart          # TraktItem, TraktList
│   │   │   ├── logic/
│   │   │   │   ├── discover_provider.dart     # Category state management
│   │   │   │   └── paged_loader.dart          # Infinite scroll pagination
│   │   │   └── ui/
│   │   │       ├── discover_screen.dart       # Main browse screen
│   │   │       ├── category_row.dart          # Horizontal category row
│   │   │       ├── filter_sheet.dart          # Genre/year/provider filters
│   │   │       └── search_results_view.dart   # Search results grid
│   │   │
│   │   ├── media_detail/                      # Movie & TV detail screens
│   │   │   ├── logic/media_detail_provider.dart
│   │   │   └── ui/
│   │   │       ├── media_detail_screen.dart   # Full detail with backdrop
│   │   │       ├── season_grid.dart           # Season/episode grid
│   │   │       └── trailer_player.dart        # YouTube trailer embed
│   │   │
│   │   ├── request/                           # Media requesting
│   │   │   ├── data/request_service.dart      # Backend request API calls
│   │   │   ├── logic/request_provider.dart    # Per-item request state
│   │   │   └── ui/
│   │   │       ├── request_button.dart        # One-tap request button
│   │   │       └── request_status_sheet.dart  # Status detail sheet
│   │   │
│   │   ├── radarr/                            # Movie management (admin)
│   │   │   ├── data/
│   │   │   │   ├── radarr_api_service.dart    # Proxied via backend
│   │   │   │   └── radarr_models.dart
│   │   │   ├── logic/radarr_movies_provider.dart
│   │   │   └── ui/
│   │   │       ├── radarr_home_screen.dart
│   │   │       └── radarr_movie_list.dart
│   │   │
│   │   ├── sonarr/                            # TV management (admin)
│   │   │   ├── data/
│   │   │   │   ├── sonarr_api_service.dart    # Proxied via backend
│   │   │   │   └── sonarr_models.dart
│   │   │   ├── logic/sonarr_series_provider.dart
│   │   │   └── ui/
│   │   │       ├── sonarr_home_screen.dart
│   │   │       └── sonarr_series_list.dart
│   │   │
│   │   ├── ai_assistant/                      # AI chat
│   │   │   ├── data/
│   │   │   │   ├── ai_chat_service.dart       # SSE streaming from backend
│   │   │   │   └── ai_models.dart             # ChatMessage, ChatRole
│   │   │   ├── logic/ai_chat_provider.dart    # Chat state + streaming
│   │   │   └── ui/
│   │   │       ├── ai_chat_screen.dart
│   │   │       └── chat_bubble.dart
│   │   │
│   │   ├── settings/ui/settings_screen.dart   # Server info, account, logout
│   │   ├── setup_wizard/ui/                   # First-run setup
│   │   └── shell/ui/app_shell.dart            # Bottom nav + drawer
│   │
│   └── navigation/app_router.dart             # GoRouter with auth guard
│
├── pubspec.yaml
└── test/
```

## Key Dependencies

| Package | Purpose |
|---|---|
| `flutter_riverpod` | State management |
| `go_router` | Declarative routing with auth redirects |
| `dio` | HTTP client with interceptors |
| `cached_network_image` | Image caching for TMDB posters |
| `flutter_secure_storage` | Encrypted JWT storage |
| `web_socket_channel` | Real-time backend events |
| `shimmer` | Loading placeholder animations |
| `flutter_animate` | UI transitions and effects |
| `equatable` | Value equality for models |

## Navigation

Four-tab bottom navigation with GoRouter:

| Tab | Path | Screen | Data Source |
|---|---|---|---|
| Discover | `/discover` | Browse & search | TMDB + Trakt |
| Movies | `/radarr` | Radarr library | Backend proxy |
| TV Shows | `/sonarr` | Sonarr library | Backend proxy |
| Assistant | `/assistant` | AI chat | Backend SSE |

Full-screen routes: `/detail/:type/:id`, `/settings`, `/login`

Auth guard redirects unauthenticated users to `/login`. Authenticated users on `/login` redirect to `/discover`.

## License

See the root repository for license information.
