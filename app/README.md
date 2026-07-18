# Cantinarr Flutter App

The client for [Cantinarr](https://github.com/windoze95/cantinarr) -- an intelligent self-hosted media manager for Plex and Jellyfin households.

Built with Flutter; iOS, Android, and web are the shipping targets (web is embedded in the server binary). One dark, warm cinematic theme, TMDB/Trakt-powered discovery, one-tap requests with approvals, deep *arr control, books, download-client management, stuck-download diagnosis and supervised fixes, an AI assistant, and push notifications -- all app API traffic goes through the Cantinarr backend, while a ChatGPT account link explicitly hands authorization off to the external browser.

```
┌──────────────────────────────────────────────────────┐
│                   Cantinarr App                      │
│                                                      │
│  Discover · Requests · Movies · TV · Books ·         │
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

A single dark-first, cinematic theme designed for couch browsing and high-signal admin work. Its near-black sign face, espresso/umber layers, glowing amber-gold, and ember highlights come directly from the Cantinarr logo. A static, pointer-transparent ambient gradient sits behind translucent page scaffolds, while semantic Material 3 surfaces provide restrained depth for navigation, forms, grouped settings, and action docks. The futuristic feel comes from precision, depth, and interaction—not a cold blue cyber palette.

The shared design foundation also owns typography, spacing, shape, and motion tokens. Reusable ambient-canvas, panel, section-header, media-card, and featured-hero primitives keep discovery and management screens visually related without wrapping every row in another card or adding perpetual background animation.

| Color | Hex | Usage |
|---|---|---|
| Background | `#0C0805` | Warm near-black ambient canvas |
| Surface | `#15100C` | Navigation, sheets, sign-face chrome |
| Surface variant | `#201710` | Cards, fields, grouped content |
| Raised surface | `#2A1E14` | Elevated umber panels and hero controls |
| Amber accent | `#F2AC2D` | Primary actions, active navigation, logo glow |
| Ember signal | `#F47A2E` | Secondary highlights, AI, and live signals |
| Text primary | `#F6F0E8` | Warm-cream headings and body copy |
| Text secondary | `#B7A99D` | Supporting copy and labels |
| Text muted | `#A09286` | Disabled, unavailable, and low-emphasis metadata |
| Available / success | `#72CC91` | Ready on the media server |
| Requested / pending | `#F4C66A` | Awaiting approval or acquisition |
| Downloading / info | `#D98A58` | In progress and informational state |

## Features

### Discovery & search
- **TMDB + Trakt rows** -- trending, popular movies/TV, top rated, upcoming; all proxied through the backend so keys stay server-side (poster/backdrop images load straight from the TMDB CDN).
- **Module-global search bar** -- debounced multi-search from every primary library/discovery surface. Secondary work screens hide it to avoid stacking global search above local filters. Results carry **requester-vocabulary availability chips** (Available / Partially Available / Requested -- never arr jargon), matched against the user's default library and kept fresh via WebSocket pings.
- **Search-to-AI hand-off** -- a query that looks like a question (or returns nothing) lights up an AI affordance; sending it opens the dedicated assistant with the prompt already in flight.

### Discover
- **Movies / TV tabs** -- discovery rows plus live library rows from the user's default instances: Downloading Soon, Recently Downloaded, Airing Next.
- **Releases tab** -- a unified movie + episode release timeline with list and month-calendar views.
- **Books tab** -- appears only for users with a Chaptarr grant: owned-aware book search with per-format request buttons (see Books).

### Requests
- **One-tap requesting** with status-aware labels: Request → Pending (awaiting approval) → Requested → Downloading → **Available**; partially-available shows get **Request More**, which jumps to the season picker. The ready state stays provider-neutral rather than assuming a particular playback app.
- **Season-level choice** -- per-season availability, multi-select, "Request N seasons"; shown only to users the admin has allowed to choose (others inherit the default scope).
- **Book formats** -- request the eBook, the Audiobook, or both; formats already owned or requested are disabled with their status shown.
- **Live status** -- request state and download progress update in real time over WebSocket, including changes made directly in the arrs (webhooks).

### Movies & TV management (admin)
- **Drill-down** -- library → movie detail, or series → season → episode, with per-item download progress, quality/size, history, and messages, proxied from Radarr/Sonarr API v3 with credential fields scrubbed server-side.
- **Open in Radarr/Sonarr** -- on a discovery detail page, admins get a jump into the matching arr item, shown only once the title actually exists there (it appears right after a request adds it). Movies link to Radarr, TV to Sonarr; books are out of scope.
- **Explicit safe removal** -- destructive library actions live in each row's labeled overflow menu rather than behind a swipe gesture; confirmation is mandatory and deleting files from disk is always opt-in.
- **Sonarr episode power tools** -- long-press action menus, episode **multi-select with batch search** (quick-select All / Undownloaded), batch **delete files**, an **All Seasons** view, per-season/series monitor toggles, **Edit Series** (profile, type, path, tags, season folders), and external links (IMDb/TheTVDB/TMDB/Trakt).
- **Interactive release search** -- per-episode, per-season, per-movie, and per-book: live indexer results with smart sorting, seeders/leechers, and rejection reasons; tap to grab.
- **Import Doctor** -- any stuck queue item explains itself in plain English with the raw arr messages shown for transparency, then offers ordered one-click fixes (manual/force import with candidates preview, remove, blocklist + re-search, category hand-off, rescan). One shared rule engine drives Sonarr, Radarr, and Chaptarr, mirrored from the server's classifier.

### Books (Chaptarr)
- **Per-format everything** -- a title's ebook and audiobook are separate records; the author page shows two bookmark toggles per book (tap an empty one to add + search the missing format).
- **Owned-aware search** -- library titles are injected into lookup results and floated to the top with Downloaded / In Library chips; distinct records are never merged.
- **Full module** -- library with author drill-down, queue with Import Doctor, history, and wanted (missing / cutoff unmet).

### Downloads & Tautulli (admin)
- **Unified download queue** across SABnzbd, qBittorrent, NZBGet, and Transmission: pause/resume all or per item, remove (optionally with data; NZBGet removes the queue item only, files stay on disk), speeds, ETAs -- live via WebSocket snapshots.
- **Tautulli** -- current Plex streams with quality/transcode badges, watch history, and top-stats.

### Issues & AI remediation
- **Report a problem** -- on any available title (admin-toggleable), bound to the exact active/detail Radarr or Sonarr instance and scoped to a movie, series, season, or exact episode (including S00 specials); category chips plus free text.
- **Issue threads** -- a chat-style thread per issue where the reporter, admins, and the AI agent converse; agent questions flip the issue to "Needs your reply", while inconclusive investigations surface as requester-safe **Needs a closer look** instead of pretending the problem was resolved. New **Watching the download** and **Download recovery in progress** states are passive: reporter-facing copy says the problem is being tracked quietly, without exposing arr/agent/admin workflow vocabulary, and the thread hides typing, replies, and completion controls until recovery is finished or truly needs attention. Admins can finish any actionable thread with an explicit **Mark resolved** or **Close without fix** judgment and a required note after manual verification; concurrent changes are reloaded instead of overwritten, and **Dismiss** remains separate.
- **Attention vs tracking** -- the admin issue list separates **Needs attention**, **Tracking**, and **Closed**. Tracking rows are muted and never show an unread dot, while actionable new issues and non-admin status changes retain the read/unread affordance. The drawer issue count excludes passive arr recovery; admin-toggleable "mark resolved issues as read" keeps a cleanly resolved issue from re-flagging.
- **Agent fixes** -- proposed mutations render as safety-critical approval cards that prominently name the target service, instance name, and immutable instance ID alongside typed summaries, quoted parameters, and passive rationale; every execution requires confirmation showing that same target. The dedicated screen keeps separate **Awaiting review** and **History** tabs; issue threads retain terminal actions, run summaries, closure provenance, and links to the full step ledger. Stale proposals and concurrent decisions are reconciled against the server, so the app never claims a denial when another admin's approval won.
- **Live badges** -- Approvals / actionable Issues / Agent fixes counts in the drawer, kept current over WebSocket; quietly observed or actively retrying arr issues are tracked without adding alert pressure. A **Plex invites** entry appears (with count) only while someone is waiting on a Plex invite -- the persistent surface behind the miss-able push -- and lands on the Users screen, where waiting users carry a "Needs Plex invite" tag. A **Setup checklist** entry (with unconfigured-feature count) appears for admins until everything is configured or they mute it from the checklist.
- **Focused attention menu** -- admins can independently keep Approvals, Issues, and Agent fixes pinned or show each only while requests await approval, an issue needs attention or is being tracked, or a fix awaits review. These device-local switches appear on the queue screens and in Settings, so a hidden entry can always be restored; passive tracking keeps the Issues entry available without inflating its actionable badge.

### AI assistant
- **Multi-provider chat** with SSE streaming, visible tool activity, and a poster carousel for results. Every user can bring a personal Anthropic, OpenAI, or Gemini API key, or link OpenAI (OAuth) with a ChatGPT browser device code. Admins can configure the same choices as an included server profile and grant it per user. Personal overrides fail closed instead of silently spending shared quota.
- **Server-side tools** -- the assistant searches, checks availability, and requests on your behalf; admins can triage queues conversationally.
- **Persistent session** -- the focused `/assistant` workspace keeps one conversation alive across navigation (30-minute idle expiry).

### Notifications (iOS)
- **Native APNs push** via a `MethodChannel` -- no Firebase. Tokens register with the backend per device; taps deep-link to the right screen (detail page, approvals, issue thread...).
- **Per-category preferences** -- request decisions, new movies, new episodes, Plex invite sent, and admin-only categories (new requests, issues, agent fixes, Plex access requests), plus a test-push diagnostic.

### Settings
- **Setup Checklist** (admin) -- a live wizard at `/setup`: which features are configured and which aren't, derived by the server from actual configuration (never stored progress, so it's resumable and editable by construction). Each step opens the real settings screen and re-derives on return; unknown items from newer servers still render, which is how future features announce themselves. Surfaced as a Settings tile with an "X of Y configured" subtitle and a muteable drawer reminder with the remaining count.
- **Needs-attention navigation** (admin) -- device-local parity switches for Approvals, Issues, and Agent fixes control whether each queue stays pinned or appears only while it has active work. The same controls remain available in Settings after a conditional menu entry disappears.
- **Instances** (admin) -- add/edit all eight service types; test connections; set the global default (single-default invariant with takeover confirmation) or per-user default pins; assign users to a Chaptarr instance (the Books access grant); assigning a user pinned to a sibling instance asks for confirmation before removing them from it; use **Configure instant updates** to have the server rotate credentials and install the Radarr/Sonarr Connect webhook without exposing its secret to the device.
- **Users** (admin) -- roles, connect links / re-invites / device links, per-user password & passkey enablement (disabling is a real revoke), included-AI grants, per-user request settings (tri-state inherit/on/off + default instances), test push. Enabling an OAuth-backed grant requires an explicit sharing/quota warning. The screen also shows each user's shared Plex email and invite state: with a linked Plex account it's a one-tap **Send Plex invite** (resend supported); otherwise **Invite in Plex…** copies the address and opens Plex's Manage Library Access page.
- **Plex Invites** (admin) -- link a Plex account via the PIN flow, pick the server and libraries invites share, and toggle **auto-invite** (a user sharing their Plex email gets invited with zero admin taps).
- **Request policy** (admin) -- global require-approval, season choice + default scope, quality choice + default profiles.
- **Devices** (admin) -- every connected device with hardware model, last-seen, "This device" badge, and revoke.
- **Credentials** (admin, write-only) -- TMDB and Trakt, plus the included server AI profile: Anthropic/OpenAI/Gemini API keys or a shared OpenAI (OAuth) connection and provider/model selection. AI saves show a testing state and succeed only after one small tool-free, low-reasoning response turn. Validation distinguishes invalid credentials, unsupported model access, exhausted quota, and temporary provider outages without exposing upstream secrets. A default-on daily shared-model test can be disabled to eliminate background usage; failures open one admin issue.
- **AI Access** (self-service) -- choose included access when the admin grants it, or configure a personal Anthropic/OpenAI/Gemini key or OpenAI (OAuth) link at any time, with or without a grant. A personal provider need not match the server provider. Personal and included sources are labeled separately, keys are write-only, and a broken personal override is never replaced by surprise shared usage. Key and model are tested and saved together so a failure keeps the prior profile intact and shows the same safe actionable error category.
- **OpenAI OAuth** -- personal and admin-shared device-code flows open ChatGPT sign-in in the browser, poll until approval, perform a small response test, show the owning account's current Codex usage windows, and support disconnecting it. The model picker includes OpenAI recommended and GPT-5.6 Sol, Terra, and Luna. Passwords and OAuth tokens never pass through the app; authorization is encrypted on the server. Only admins can see shared-account identity and usage metadata.
- **AI tools** (admin) -- per-tool toggles for chat + MCP, and a one-hour debug-logging switch.
- **AI remediation** (admin) -- master switch, auto-dispatch, reporting affordance, mark-resolved-issues-as-read, `supervised`/`investigate_only` mode, an optional remediation-only model override, step/turn/time and daily-run budgets, reporter-reply timeout, and minimum-watch / arr-quiet / recovery-settle timers that delay investigation and alerts while Radarr or Sonarr can still recover on its own. This server-owned agent always follows the currently selected admin shared provider and credential, including the shared OpenAI OAuth connection; it never uses personal credentials or per-user included-access grants. The override must pass a small response test with that shared provider, and a later provider change safely falls back to the shared model until a new override is tested.
- **Update Portal** (admin) -- optional link to your own container-management portal (e.g. an Unraid or Portainer page). When the server sees a newer published release, an admin-only banner appears app-wide and links here (or to the update guide when unset); it's dismissible per release. The About sheet also shows the running server version.
- **Notifications, Passkeys, Password** -- self-service (passkey/password screens appear when admin-enabled).
- **Watch on Plex guide** -- requester-focused walkthrough (install the Plex app, sign in, accept the invite, start watching) with a **Request your invite** step: the user shares their Plex email, admins get a push pointing at the Users screen, and once the invite goes out (one-tap or auto) the card flips to "check your inbox" and the user gets a push. Hideable from the guide itself or via a Settings toggle that also removes it from the menu.

### Auth
- **Connect links** -- open one and the account connects instantly (`cantinarr://connect` deep links on iOS); passwordless by default with a long-lived, auto-refreshing session.
- **First-run setup** -- the auth screen walks through server URL → admin account creation → an optional passkey offer.
- **Passkeys & passwords** -- native passkey sign-in on associated deployments (iOS/Android/Windows platform plugins, browser fallback), password login where enabled.
- **Session resilience** -- the session survives transport failures and VPN flaps; only a genuine 401 clears it. There is deliberately no logout button -- admins revoke devices server-side.
- **Separate OAuth directions** -- ChatGPT device authorization is an explicit outbound sign-in that lets Cantinarr use a personal or admin-shared Codex allowance. Cantinarr's MCP OAuth is a different inbound login that lets an external client access Cantinarr.

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
| Arr management | Backend `/api/instances/{id}/api/v3` (credential-scrubbed proxy) | API keys never reach devices; reads allowed for users, writes admin-only |
| Books | Backend proxy to Chaptarr (Readarr API v1) | Per-user grant enforced server-side |
| AI chat | Backend `/api/ai/chat` (SSE) | Tool execution stays server-side; chat uses the resolved personal provider or a granted included provider |
| AI settings | Backend `/api/ai/settings` + write-only personal credential routes | The app sees provider/model, source, validation errors, and configured booleans, never secret values |
| OpenAI OAuth | Personal `/api/ai/codex/*` or admin-shared `/api/admin/ai/codex/*` + explicit ChatGPT browser sign-in | Device code and scope-appropriate safe status reach the app; OAuth tokens remain encrypted on the server |
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
│   ├── theme/                    # Semantic color, type, spacing, shape, motion tokens
│   └── widgets/                  # Ambient canvas, panels, heroes, media primitives, sheets...
├── features/
│   ├── auth/                     # Auth screen (setup/login/connect), passkeys, session
│   ├── ai_assistant/             # SSE chat, source-aware AI settings, media carousel, OpenAI OAuth
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
│   ├── shell/                    # App shell: navigation + search-to-AI hand-off
│   ├── sonarr/                   # TV management + episode tools + import doctor engine
│   └── tautulli/                 # Plex activity/history/stats
└── navigation/app_router.dart    # GoRouter: shell + module tab shells + guards
```

## Navigation

One authenticated shell hosts both module pages and secondary work screens over the shared ambient canvas. The chrome adapts at 900px: below it, a hamburger drawer lists modules and each module shows its pages in a floating bottom dock; at 900px+ the drawer becomes a layered persistent sidebar whose active module expands into its pages, and the bottom dock disappears. Detail, settings, approval, issue, and assistant routes therefore keep the desktop command sidebar instead of dropping users into a disconnected navigation mode. The global discovery bar appears on primary module surfaces and yields to each secondary screen's own focused controls. Line-length-sensitive surfaces (search results, chat, detail pages, settings forms) cap and center their content on desktop; modal bottom sheets cap at 640px.

| Module | Tabs | Access |
|---|---|---|
| `/dashboard` | Movies · TV · Releases · Books¹ | everyone |
| `/radarr` | Library · Queue · History · Wanted · Calendar | admin |
| `/sonarr` | Library · Queue · History · Wanted · Calendar | admin |
| `/chaptarr` | Library · Queue · History · Wanted | admin |
| `/downloads` | Queue · History | admin |
| `/tautulli` | Activity · History · Stats | admin |

¹ Books appears only with a Chaptarr grant.

`/login` is the only route outside the authenticated shell. Secondary routes inside it include `/assistant`, `/detail/:type/:id`, `/approvals`, `/issues`, `/issues/:id`, `/agent-actions`, `/agent-runs/:id`, `/settings/ai`, `/settings/chatgpt`, `/settings/...`, `/plex-guide`, and `/setup`.

The router guard redirects unauthenticated users to `/login`, remembers safe internal deep-link targets through sign-in, centrally bounces non-admins from admin routes, and gates Books on the user's Chaptarr grant. Modules with multiple instances get an instance selector in the drawer/app bar.

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
| `url_launcher` | External links (ChatGPT device authorization, trailers, IMDb/TVDB/TMDB/Trakt) |
| `device_info_plus` / `package_info_plus` | Device naming, version display |
| `shimmer`, `intl`, `uuid` | Loading placeholders, formatting, ids |

## Platforms & CI

- **iOS**, **Android**, and **web** are the shipping targets. Web is built in CI and embedded in the server image; iOS auto-deploys to TestFlight on `main` (manual signing via repo secrets); Android auto-deploys a signed AAB to the Play Store beta track on `main` (upload-keystore signing via repo secrets — see [docs/store-release.md](../docs/store-release.md)). macOS/Windows/Linux directories are unbuilt scaffolding.
- CI runs `flutter analyze --no-fatal-infos`, `flutter test`, and `flutter build web --release` on every PR.
- Store listing copy, graphics, and screenshots live in `android/fastlane` + `ios/fastlane` and sync to both consoles on merge (`storelisting.yml`). Screenshots are generated from the demo-data harness `test/preview/screenshot_main.dart` via `tool/screenshots/` — see [docs/store-release.md](../docs/store-release.md).

## License

See the root repository for license information.
