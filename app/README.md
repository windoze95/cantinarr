# Cantinarr Flutter App

The client for [Cantinarr](https://github.com/windoze95/cantinarr) -- a frictionless media request app for Plex and Jellyfin households.

Built with Flutter; iOS, Android, and web are the shipping targets (web is embedded in the server binary). One dark, warm-gold "cantina" theme, TMDB/Trakt-powered discovery, one-tap requests with approvals, deep *arr control, books, an AI assistant, and push notifications -- all through the Cantinarr backend, which is the only API the app talks to.

```
┌──────────────────────────────────────────────────────┐
│                   Cantinarr App                      │
│                                                      │
│  Dashboard · Requests · Movies · TV · Books ·        │
│  Downloads · Tautulli · Issues · AI · Settings       │
│                        │                             │
│                        ▼                             │
│         ┌─────────────────────────────────┐          │
│         │       Cantinarr Backend         │          │
│         │  REST + SSE + WebSocket + push  │          │
│         │ (discovery, requests, arr proxy,│          │
│         │   AI, issues, auth -- all keys  │          │
│         │        stay server-side)        │          │
│         └─────────────────────────────────┘          │
│                                                      │
│  (images load from the TMDB CDN; no API key needed)  │
└──────────────────────────────────────────────────────┘
```

## Design

A single dark-first theme with warm gold accents, designed for couch browsing. Sheets and setting cards paint on a Material surface so ink effects render correctly everywhere.

| Color | Hex | Usage |
|---|---|---|
| Accent | `#E5A00D` | Warm gold -- buttons, active states, brand |
| Background | `#0F0A04` | Warm near-black canvas |
| Surface | `#1C1510` | Cards, nav, sheets |
| Surface variant | `#2A1F14` | Elevated tiles |
| Text primary | `#F0F0F0` | Headings, body |
| Text secondary | `#9E918A` | Labels, hints |
| Available | `#4CAF50` | Green -- on the server |
| Requested | `#FFA726` | Amber -- pending/requested |
| Downloading | `#42A5F5` | Blue -- in progress |
| Unavailable | `#757575` | Grey |

## Features

### Discovery & search
- **TMDB + Trakt rows** -- trending, popular movies/TV, top rated, upcoming; all proxied through the backend so keys stay server-side (poster/backdrop images load straight from the TMDB CDN).
- **Ever-present search bar** -- debounced multi-search from anywhere in the shell. Results carry **requester-vocabulary availability chips** (Available / Partially Available / Requested -- never arr jargon), matched against the user's default library and kept fresh via WebSocket pings.
- **Search-to-AI hand-off** -- a query that looks like a question (or returns nothing) lights up an AI affordance; the chat opens inline in the shell and shares one conversation with the full-screen assistant.

### Dashboard
- **Movies / TV tabs** -- discovery rows plus live library rows from the user's default instances: Downloading Soon, Recently Downloaded, Airing Next.
- **Releases tab** -- a unified movie + episode release timeline with list and month-calendar views.
- **Books tab** -- appears only for users with a Chaptarr grant: owned-aware book search with per-format request buttons (see Books).

### Requests
- **One-tap requesting** with status-aware labels: Request → Pending (awaiting approval) → Requested → Downloading → **Watch Now**; partially-available shows get **Request More**, which jumps to the season picker.
- **Season-level choice** -- per-season availability, multi-select, "Request N seasons"; shown only to users the admin has allowed to choose (others inherit the default scope).
- **Book formats** -- request the eBook, the Audiobook, or both; formats already owned or requested are disabled with their status shown.
- **Live status** -- request state and download progress update in real time over WebSocket, including changes made directly in the arrs (webhooks).

### Movies & TV management (admin)
- **Drill-down** -- library → movie detail, or series → season → episode, with per-item download progress, quality/size, history, and messages, proxied verbatim to Radarr/Sonarr API v3.
- **Sonarr episode power tools** -- long-press action menus, episode **multi-select with batch search** (quick-select All / Undownloaded), batch **delete files**, an **All Seasons** view, per-season/series monitor toggles, **Edit Series** (profile, type, path, tags, season folders), and external links (IMDb/TheTVDB/TMDB/Trakt).
- **Interactive release search** -- per-episode, per-season, per-movie, and per-book: live indexer results with smart sorting, seeders/leechers, and rejection reasons; tap to grab.
- **Import Doctor** -- any stuck queue item explains itself in plain English with the raw arr messages shown for transparency, then offers ordered one-click fixes (manual/force import with candidates preview, remove, blocklist + re-search, category hand-off, rescan). One shared rule engine drives Sonarr, Radarr, and Chaptarr, mirrored from the server's classifier.

### Books (Chaptarr)
- **Per-format everything** -- a title's ebook and audiobook are separate records; the author page shows two bookmark toggles per book (tap an empty one to add + search the missing format).
- **Owned-aware search** -- library titles are injected into lookup results and floated to the top with Downloaded / In Library chips; distinct records are never merged.
- **Full module** -- library with author drill-down, queue with Import Doctor, history, and wanted (missing / cutoff unmet).

### Downloads & Tautulli (admin)
- **Unified download queue** across SABnzbd, qBittorrent, NZBGet, and Transmission: pause/resume all or per item, remove (optionally with data), speeds, ETAs -- live via WebSocket snapshots.
- **Tautulli** -- current Plex streams with quality/transcode badges, watch history, and top-stats.

### Issues & AI remediation
- **Report a problem** -- on any available title (admin-toggleable), scoped to a movie, series, or season; category chips plus free text.
- **Issue threads** -- a chat-style thread per issue where the reporter, admins, and the AI agent converse; the agent's questions flip the issue to "Needs your reply".
- **Agent fixes** -- proposed mutations render as safety-critical approval cards (typed summaries, quoted parameters, rationale as passive text); admins approve or deny inline or from a grouped queue, and every run has a read-only audit timeline with step ledger and cost.
- **Live badges** -- Approvals / Issues / Agent fixes counts in the drawer, kept current over WebSocket. A **Plex invites** entry appears (with count) only while someone is waiting on a Plex invite -- the persistent surface behind the miss-able push -- and lands on the Users screen, where waiting users carry a "Needs Plex invite" tag. A **Setup checklist** entry (with unconfigured-feature count) appears for admins until everything is configured or they mute it from the checklist.

### AI assistant
- **Multi-provider chat** (Anthropic, OpenAI, or Gemini -- server-configured) with SSE streaming, visible tool activity, and a poster carousel for results.
- **Server-side tools** -- the assistant searches, checks availability, and requests on your behalf; admins can triage queues conversationally.
- **One session everywhere** -- the inline shell chat and `/assistant` share the same conversation (30-minute idle expiry).

### Notifications (iOS)
- **Native APNs push** via a `MethodChannel` -- no Firebase. Tokens register with the backend per device; taps deep-link to the right screen (detail page, approvals, issue thread...).
- **Per-category preferences** -- request decisions, new movies, new episodes, Plex invite sent, and admin-only categories (new requests, issues, agent fixes, Plex access requests), plus a test-push diagnostic.

### Settings
- **Setup Checklist** (admin) -- a live wizard at `/setup`: which features are configured and which aren't, derived by the server from actual configuration (never stored progress, so it's resumable and editable by construction). Each step opens the real settings screen and re-derives on return; unknown items from newer servers still render, which is how future features announce themselves. Surfaced as a Settings tile with an "X of Y configured" subtitle and a muteable drawer reminder with the remaining count.
- **Instances** (admin) -- add/edit all eight service types; test connections; set the global default (single-default invariant with takeover confirmation) or per-user default pins; assign users to a Chaptarr instance (the Books access grant); assigning a user pinned to a sibling instance asks for confirmation before removing them from it; copy the per-instance **webhook URL** for Radarr/Sonarr > Connect.
- **Users** (admin) -- roles, connect links / re-invites / device links, per-user password & passkey enablement (disabling is a real revoke), per-user request settings (tri-state inherit/on/off + default instances), test push. Shows each user's shared Plex email and invite state: with a linked Plex account it's a one-tap **Send Plex invite** (resend supported); otherwise **Invite in Plex…** copies the address and opens Plex's Manage Library Access page.
- **Plex Invites** (admin) -- link a Plex account via the PIN flow, pick the server and libraries invites share, and toggle **auto-invite** (a user sharing their Plex email gets invited with zero admin taps).
- **Request policy** (admin) -- global require-approval, season choice + default scope, quality choice + default profiles.
- **Devices** (admin) -- every connected device with hardware model, last-seen, "This device" badge, and revoke.
- **Credentials** (admin, write-only) -- TMDB, Trakt, and AI provider keys + provider/model selection.
- **AI tools** (admin) -- per-tool toggles for chat + MCP, and a one-hour debug-logging switch.
- **AI remediation** (admin) -- master switch, auto-dispatch, reporting affordance, autonomy tier, provider/model, and run budgets.
- **Notifications, Passkeys, Password** -- self-service (passkey/password screens appear when admin-enabled).
- **Watch on Plex guide** -- requester-focused walkthrough (install the Plex app, sign in, accept the invite, start watching) with a **Request your invite** step: the user shares their Plex email, admins get a push pointing at the Users screen, and once the invite goes out (one-tap or auto) the card flips to "check your inbox" and the user gets a push. Hideable from the guide itself or via a Settings toggle that also removes it from the menu.

### Auth
- **Connect links** -- open one and the account connects instantly (`cantinarr://connect` deep links on iOS); passwordless by default with a long-lived, auto-refreshing session.
- **First-run setup** -- the auth screen walks through server URL → admin account creation → an optional passkey offer.
- **Passkeys & passwords** -- native passkey sign-in on associated deployments (iOS/Android/Windows platform plugins, browser fallback), password login where enabled.
- **Session resilience** -- the session survives transport failures and VPN flaps; only a genuine 401 clears it. There is deliberately no logout button -- admins revoke devices server-side.

## Getting Started

### Prerequisites
- Flutter (stable channel), Dart SDK 3.3+
- A running [Cantinarr server](../server/)

### Run the app
```bash
cd app
flutter pub get
flutter run
```

Native iOS passkeys require iOS 16+, the Associated Domains entitlement for the server domain (`webcredentials:your.domain`), and the server publishing an AASA file with the app's `TeamID.BundleID`. Push requires the APNs entitlement (production) and a push-gateway-enabled server. See the [server README](../server/README.md#configuration) for the deployment env vars.

### Build for web (embedded in server)
```bash
flutter build web --release
# Output in build/web/ -- copied into the Go binary during the Docker build (or `make`)
```

## Architecture

Feature-first structure with data / logic / ui layers per feature. State is Riverpod with hand-written providers and hand-rolled `fromJson` models throughout -- no codegen. The backend is the only API surface:

| Concern | Data source | Why |
|---|---|---|
| Discovery, search, media detail | Backend `/api/discover`, `/api/media`, `/api/trakt` | TMDB/Trakt keys stay server-side |
| Poster/backdrop images | TMDB CDN (direct) + one shared tuned image cache | CDN images need no key |
| Requests & approvals | Backend `/api/requests`, `/api/admin/requests` | ID bridging + policy live server-side |
| Arr management | Backend `/api/instances/{id}/api/v3` (verbatim proxy) | API keys never reach devices; reads allowed for users, writes admin-only |
| Books | Backend proxy to Chaptarr (Readarr API v1) | Per-user grant enforced server-side |
| AI chat | Backend `/api/ai/chat` (SSE) | Provider keys + tool execution server-side |
| Realtime | Backend `/api/ws` | Queue snapshots, status pings, badges |
| Push | Native APNs token → backend → push gateway | No Firebase |

Realtime consumption is provider-based: a raw event stream fans out into typed, auto-disposing providers (`downloads_queue`, `arr_queue_changed`, `request_status_changed`, `request_decision`, `issue_*`, `agent_action_*`, `remediation_autodispatch_disabled`); screens pair WS pings with silent refetch and a polling fallback, so a dead socket degrades gracefully.

## Project Structure

```
app/lib/
├── main.dart / app.dart          # Entry, MaterialApp, deep-link listener
├── core/
│   ├── config/                   # Timeouts, debounce, TMDB image URL helpers
│   ├── models/                   # BackendConnection, UserProfile, AppModule
│   ├── network/                  # Dio client + JWT refresh interceptor, WS client,
│   │                             #   shared image cache (1000 objects / 30-day stale)
│   ├── providers/                # Realtime event fan-out, instances, modules
│   ├── storage/                  # Secure tokens + stable device identity, prefs
│   ├── theme/                    # The cantina design system
│   └── widgets/                  # MediaCard, StatusPill, InstanceDropdown, sheets...
├── features/
│   ├── auth/                     # Auth screen (setup/login/connect), passkeys, session
│   ├── ai_assistant/             # SSE chat, tool activity, media carousel
│   ├── chaptarr/                 # Books module: library/queue/history/wanted + doctor
│   ├── dashboard/                # Movies/TV/Releases/Books home tabs
│   ├── discover/                 # Discovery rows + multi-search (backend-proxied)
│   ├── downloads/                # Unified download-client queue + history
│   ├── issues/                   # Report-a-problem, threads, agent approvals + audit
│   ├── media_detail/             # Detail screens, season table, request surface
│   ├── notifications/            # APNs registration, prefs, deep-link routing
│   ├── person/                   # Cast/crew detail sheet
│   ├── radarr/                   # Movie management: library/queue/history/wanted/calendar
│   ├── request/                  # Request buttons, options sheet, status sheet
│   ├── settings/                 # Everything under Settings (see Features)
│   ├── setup_wizard/             # Live setup checklist wizard + Plex guide
│   ├── shell/                    # App shell: drawer, search bar, inline AI
│   ├── sonarr/                   # TV management + episode tools + import doctor engine
│   └── tautulli/                 # Plex activity/history/stats
└── navigation/app_router.dart    # GoRouter: shell + module tab shells + guards
```

## Navigation

An outer shell (persistent search bar + navigation chrome) hosts per-module page shells. The chrome adapts at 900px: below it, a hamburger drawer lists modules and each module shows its pages as a bottom nav; at 900px+ the drawer becomes a persistent sidebar whose active module expands into its pages, and the bottom nav disappears. Line-length-sensitive surfaces (search results, chat, detail pages, settings forms) cap and center their content on desktop; modal bottom sheets cap at 640px.

| Module | Tabs | Access |
|---|---|---|
| `/dashboard` | Movies · TV · Releases · Books¹ | everyone |
| `/radarr` | Library · Queue · History · Wanted · Calendar | admin |
| `/sonarr` | Library · Queue · History · Wanted · Calendar | admin |
| `/chaptarr` | Library · Queue · History · Wanted | admin |
| `/downloads` | Queue · History | admin |
| `/tautulli` | Activity · History · Stats | admin |

¹ Books appears only with a Chaptarr grant.

Full-screen routes: `/login`, `/assistant`, `/detail/:type/:id`, `/approvals`, `/issues`, `/issues/:id`, `/agent-actions`, `/agent-runs/:id`, `/settings/...`, `/plex-guide`, `/setup`.

The router guard redirects unauthenticated users to `/login` and bounces non-admins from management modules to `/dashboard/movies`. Modules with multiple instances get an instance selector in the drawer/app bar.

## Key Dependencies

| Package | Purpose |
|---|---|
| `flutter_riverpod` | State management (hand-written providers) |
| `go_router` | Shell + stateful tab-shell routing with guards |
| `dio` | HTTP client with auth/refresh interceptor |
| `web_socket_channel` | Realtime backend events |
| `cached_network_image` + `flutter_cache_manager` | Tuned shared image cache |
| `flutter_secure_storage` / `shared_preferences` | Tokens + device id / lightweight prefs |
| `passkeys_ios` / `passkeys_android` / `passkeys_windows` | Native WebAuthn |
| `app_links` | `cantinarr://` connect-link deep linking |
| `url_launcher` | External links (trailers, IMDb/TVDB/TMDB/Trakt) |
| `device_info_plus` / `package_info_plus` | Device naming, version display |
| `shimmer`, `intl`, `uuid` | Loading placeholders, formatting, ids |

## Platforms & CI

- **iOS**, **Android**, and **web** are the shipping targets. Web is built in CI and embedded in the server image; iOS auto-deploys to TestFlight on `main` (manual signing via repo secrets); Android auto-deploys a signed AAB to the Play Store beta track on `main` (upload-keystore signing via repo secrets — see [docs/store-release.md](../docs/store-release.md)). macOS/Windows/Linux directories are unbuilt scaffolding.
- CI runs `flutter analyze --no-fatal-infos`, `flutter test`, and `flutter build web --release` on every PR.
- Store listing copy, graphics, and screenshots live in `android/fastlane` + `ios/fastlane` and sync to both consoles on merge (`storelisting.yml`). Screenshots are generated from the demo-data harness `test/preview/screenshot_main.dart` via `tool/screenshots/` — see [docs/store-release.md](../docs/store-release.md).

## License

See the root repository for license information.
