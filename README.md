# Cantinarr

A seamless media request and *arr management server + app.

**[cantinarr.com](https://cantinarr.com)** · **[Live demo](https://demo.cantinarr.com)**

Cantinarr makes it dead simple for your family and friends to discover and request movies, TV shows, and books. They browse a beautiful interface powered by TMDB and Trakt, tap "Request," and the server handles everything behind the scenes -- adding to Radarr, Sonarr, or Chaptarr with the right IDs, the right quality profile, the right root folder. No configuration, no confusion, no TVDB headaches.

```
┌──────────────────────────────────────────────────────────────┐
│  Cantinarr Server (Go, single container, port 8585)          │
│                                                              │
│  ┌──────────┐ ┌───────────┐ ┌─────────┐ ┌────────────────┐   │
│  │ Auth/JWT │ │ Requests  │ │ Issues +│ │ AI Chat        │   │
│  │ Passkeys │ │+ Approvals│ │ AI Agent│ │ + 26 MCP Tools │   │
│  └──────────┘ └─────┬─────┘ └─────────┘ └────────────────┘   │
│                     │                                        │
│  ┌──────────────────┴───────────────────┐  ┌──────────────┐  │
│  │  ID Bridge: TMDB → Trakt → TVDB      │  │ TMDB/Trakt   │  │
│  │  (cached 30 days)                    │  │ discovery    │  │
│  └───┬──────────┬──────────┬────────────┘  └──────────────┘  │
│      │          │          │                                 │
│  ┌───┴───┐ ┌────┴───┐ ┌────┴─────┐ ┌───────────────────────┐ │
│  │Radarr │ │ Sonarr │ │ Chaptarr │ │ Flutter Web (embedded)│ │
│  └───┬───┘ └────┬───┘ └────┬─────┘ └───────────────────────┘ │
└──────┼──────────┼──────────┼─────────────────────────────────┘
       │          │          │        ▲ webhooks push external
  ┌────▼───┐ ┌────▼───┐ ┌────▼─────┐    changes back instantly
  │ Radarr │ │ Sonarr │ │ Chaptarr │  (+ SABnzbd, qBittorrent,
  └────────┘ └────────┘ └──────────┘   NZBGet, Transmission,
                                       Tautulli, push gateway)

┌───────────────────────────────┐
│  Cantinarr App (Flutter)      │      ┌─────────────────────┐
│  Discovery, Requests, Books,  │─────>│  Cantinarr Backend  │
│  Arr control, AI, Issues,     │ REST │  (the only API the  │
│  Push notifications           │ + WS │   app talks to)     │
└───────────────────────────────┘      └─────────────────────┘
```

## Why Cantinarr?

- **Zero-config requesting** -- Your users never see API keys, TVDB IDs, or quality profiles. They browse, they tap, it works.
- **TMDB + Trakt for discovery** -- The best metadata, images, and trending data, proxied through the server so keys stay off devices. Sonarr's TVDB dependency is invisible.
- **Automatic ID bridging** -- TMDB-to-TVDB translation with Trakt fallback. The #1 source of failed Sonarr adds, solved.
- **Books too** -- A Chaptarr (Readarr-API) module with per-format smarts: request the ebook, the audiobook, or both; owned-aware search; per-format monitoring from the author page. Access is granted per user.
- **Request approvals** -- Optional approval queue, globally or per user. Admins also control per-user season choice, quality choice, and default quality profiles. Approve/deny lands as a push notification for the requester.
- **AI assistant** -- "What should I watch tonight?" Every user can bring a personal Anthropic, OpenAI, or Gemini API key, or link OpenAI (OAuth) with a one-time ChatGPT browser code—even without included access, and their choice never has to match the server's provider. Admins can configure the same providers as an included server profile and grant that shared access per user. A personal provider is an explicit override; Cantinarr never silently spends the shared account when that override needs attention. The assistant searches your library, checks availability, requests for you, and gives admins conversational queue and release control.
- **AI remediation agent** -- Users tap "Report a problem" (or Cantinarr detects one in the queue); each report is bound to the exact Radarr/Sonarr instance and begins with a quiet observation window. Cantinarr gives Sonarr/Radarr time to retry or replace a download before it alerts anyone or starts the agent; a persistent quiet problem then enters the supervised workflow. Recovery cancels stale proposals before dispatch. Automatic resolution requires an exact changed file plus a matching post-incident import record—not queue disappearance or a file that was already there. Remediation is server-owned: it always uses the admin's shared API key or shared OpenAI OAuth connection and never a reporter's personal provider or per-user included-access grant. Admins may give remediation its own tested model designation while keeping that global provider and credential.
- **MCP server** -- The same 26 AI tools are exposed as a [Model Context Protocol](https://modelcontextprotocol.io/) endpoint at `/mcp`, with OAuth discovery, browser/passkey login, dynamic client registration, and persistent rotating refresh tokens. This inbound OAuth lets external clients access Cantinarr; it is separate from the outbound personal/shared OpenAI OAuth used by Codex chat. Every tool can be toggled on/off from Settings > AI Tools.
- **Deep *arr control** -- SABnzbd, qBittorrent, NZBGet, and Transmission modules with live queue management, plus drill-down Radarr/Sonarr control: series → season → episode with per-item progress, quality, and history; episode multi-select with batch search; long-press action menus; Edit Series; interactive release search everywhere.
- **Import Doctor** -- when a download is stuck, Cantinarr explains *why* in plain English (sample file, un-extracted archive, unconfirmed TheXEM mapping, "not an upgrade", unparseable/invalid file, remote-path-mapping or download-client problems, stalled torrent, permissions...) and offers **one-click fixes** with full transparency: manual/force import with the candidate files shown, remove + blocklist + re-search, hand-off to a tool like Unpackerr, or rescan. The same diagnosis backs the app, the AI assistant, the remediation agent, and MCP.
- **Flexible requests** -- request a whole title in one tap, or pick exactly which **seasons** (or book **formats**) you want; partially-available shows surface per-season availability and a one-tap path to request the rest.
- **Always in sync** -- availability is computed live from the arrs (never from a stale snapshot), and one-tap, server-managed Radarr/Sonarr webhooks push manual imports, deletes, and adds into the app the moment they happen without exposing callback credentials to a device.
- **Push notifications** -- APNs via a self-hosted push gateway with zero-config auto-enrollment: new-content alerts for everyone, approval/issue alerts for admins, per-user preference toggles, deep links into the right screen.
- **Plex onboarding** -- new users request access right from the in-app guide with their Plex email. Link your Plex account once and the server invite is one tap from the Users screen -- or fully automatic, with the user pushed a "check your inbox" the moment it's sent.
- **Tautulli** -- watch what's playing on Plex right now: active streams with quality/transcode badges, watch history, and top movies/shows/users stats.
- **Secrets encrypted at rest** -- arr API keys, download-client passwords, webhook tokens, shared and personal AI credentials, and OpenAI OAuth authorization are AES-256-GCM encrypted in the database.
- **Household-friendly** -- Connect links, passwordless by default, role-based access, per-user default instances. Admins manage services; users just browse and request.
- **Guided setup** -- a live checklist wizard derived from what's actually configured: every step opens the real settings screen, progress can't go stale, and newly shipped features appear on the list automatically.
- **Single container** -- The static Go API/web server plus a pinned Codex app-server helper, with one exposed port. Runs great on a Raspberry Pi or NAS.

## Quick Start

```bash
git clone https://github.com/windoze95/cantinarr.git
cd cantinarr
docker compose up -d
```

Or skip the clone and use the published image: `ghcr.io/windoze95/cantinarr:latest`.

Open `http://your-server:8585` -- the setup wizard walks you through creating an admin account. Then configure your services (TMDB, Radarr, Sonarr, etc.) from **Settings > Providers & Credentials** and **Settings > Add Instance** in the admin UI. Configure an included AI provider there and grant it per user, or let each person bring a provider under **Settings > AI Access**.

### From Source

```bash
# Server (requires Go 1.25+)
cd server
go run ./cmd/server

# App (requires Flutter stable, Dart SDK 3.3+)
cd app
flutter pub get
flutter run
```

`make` builds the full stack (Flutter web → embedded in the Go binary).

## Repository Structure

```
cantinarr/
├── server/                 # Go backend -- see server/README.md
│   ├── cmd/server/         # Entry point
│   └── internal/           # ai, api, arr, auth, cache, chaptarr, codexapp,
│                           # config, credentials, db, discover, downloads, instance,
│                           # mcp, mcpserver, nzbget, proxy, push, qbittorrent,
│                           # radarr, remediation, request, sabnzbd, secrets,
│                           # sonarr, tautulli, tmdb, trakt, transmission,
│                           # web, webhooks, websocket
│
├── app/                    # Flutter client (iOS, web) -- see app/README.md
│   ├── lib/
│   │   ├── core/           # Models, networking, realtime, theme, widgets
│   │   ├── features/       # auth, discover, request, dashboard, sonarr,
│   │   │                   # radarr, chaptarr, downloads, tautulli, issues,
│   │   │                   # ai_assistant, notifications, settings, ...
│   │   └── navigation/     # GoRouter with auth guard
│   └── test/
│
├── Dockerfile              # Multi-stage build (Flutter web + Go)
├── docker-compose.yml      # Full-stack deployment (push env pre-wired)
├── AGENTS.md               # Contributor/agent operating manual (CLAUDE.md imports it)
└── README.md               # This file
```

## Configuration

Shared service credentials are managed through the admin UI -- no environment variables are needed for API keys. AI is different from the other integrations: an admin can configure a server profile using an API key or a shared OpenAI (OAuth) link, while every user can independently configure the same choices as a personal override. API keys and OAuth authorization stay encrypted and server-side. Every provider, model, remediation-model override, or key save -- and every completed OAuth selection -- must complete one small real, tool-free, low-reasoning message-response turn before Cantinarr activates it. Validation reports a safe actionable category for an invalid credential, unsupported model/access, exhausted quota, or temporary provider outage without exposing upstream secrets. OpenAI OAuth offers the recommended Codex model plus GPT-5.6 Sol, Terra, and Luna.

The server also runs one small shared-model health turn every 24 hours by default. A failure opens one deduplicated admin-only issue; a later successful turn resolves it. Admins who want zero background AI usage can disable this check in **Settings > Providers & Credentials** without weakening the mandatory save-time test. The remediation agent remains independent of this monitor and always resolves credentials directly from the admin's shared profile.

Included AI is an explicit per-user entitlement for new accounts; the initial admin starts enabled. Upgrades preserve the previous global-provider behavior for existing users so access does not disappear, after which the admin can revoke or grant it from **Settings > Users**. Enabling an OpenAI OAuth-backed grant shows the shared-account allowance and cost warning before it is applied.

| Setting | Where | Description |
|---|---|---|
| TMDB access token | Admin UI | Required for media discovery and search ([get one here](https://www.themoviedb.org/settings/api)) |
| Radarr/Sonarr instances | Admin UI | Add via Settings > Add Instance |
| Chaptarr instance | Admin UI | Books module; grant access per user from the instance editor or user settings |
| SABnzbd/qBittorrent/NZBGet/Transmission | Admin UI | Download client modules (queue, history, speeds) |
| Tautulli instance | Admin UI | Plex activity, watch history, stats |
| Anthropic/OpenAI/Gemini API key | Admin UI | Enables shared API-key-backed AI chat and autonomous remediation |
| OpenAI (OAuth) | Personal link under Settings > AI Access, or an admin-managed shared link | Uses a ChatGPT account's Codex allowance for the selected personal or included model; the admin-shared link also powers server-owned remediation. Per-user shared chat access is opt-in and carries a quota/cost warning |
| Trakt client ID | Admin UI | Enhances discovery + fallback ID bridging |

Optional server env vars for deployment tuning:

| Variable | Default | Description |
|---|---|---|
| `CANTINARR_PORT` | `8585` | HTTP listen port |
| `CANTINARR_SERVER_NAME` | `Cantinarr` | Display name shown in clients |
| `CANTINARR_PUBLIC_URL` | direct request origin | Trusted public origin (for example `https://cantinarr.example.com`) used when installing authenticated Radarr/Sonarr webhooks; recommended behind a reverse proxy |
| `CANTINARR_JWT_SECRET` | auto-generated | HMAC secret for signing short-lived access tokens. Device sessions do not depend on it: changing it never signs anyone out |
| `CANTINARR_ENCRYPTION_KEY` | auto-generated key file | Base64 32-byte key for secrets-at-rest (default: `/config/encryption.key`) |
| `CANTINARR_AI_PROVIDER` | `anthropic` | Fallback provider for the included server AI profile when none is saved in the admin UI (`anthropic`, `openai`, `gemini`, or `codex`) |
| `CANTINARR_AI_MODEL` | provider default | Fallback model for the included server AI profile when none is saved in the admin UI |
| `CANTINARR_CODEX_BIN` | auto-discovered | Optional path to `codex-app-server` or the full `codex` CLI; container images bundle the tested 0.144.3 app-server at `/usr/local/bin/codex-app-server` |
| `CANTINARR_CODEX_RUNTIME_DIR` | `/dev/shm/cantinarr-codex` | Absolute Linux tmpfs/ramfs directory used for server-owned, ephemeral per-session Codex state; if it already exists, it must be owned by the server user with mode `0700` |
| `CANTINARR_PUSH_GATEWAY_URL` | unset | Push gateway origin -- setting it enables push notifications (auto-enrolls on first start) |
| `CANTINARR_PUSH_API_KEY` | unset | Optional pinned gateway key (blank = auto-enroll) |
| `CANTINARR_PUSH_ENROLL_TOKEN` | unset | Only for gateways with gated enrollment |
| `CANTINARR_APPLE_APP_IDS` | unset | `TeamID.BundleID` values for native Apple passkeys (`/.well-known/apple-app-site-association`) |
| `CANTINARR_ANDROID_PACKAGE_NAME` | `codes.julian.cantinarr` | Android package name for native passkeys |
| `CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS` | unset | Android signing cert fingerprints for `/.well-known/assetlinks.json` |
| `CANTINARR_WEBAUTHN_EXTRA_ORIGINS` | unset | Additional WebAuthn origins to trust |
| `CANTINARR_DISABLE_UPDATE_CHECK` | unset | Set to `1` to disable the periodic GitHub release check behind the admin "update available" banner |

OpenAI (OAuth) source deployments use Codex app-server and are supported only on Linux; non-Linux hosts report this provider unavailable even when a Codex binary is installed. The runtime directory's parent must exist, and the directory must be on tmpfs or ramfs—not persistent storage. Give each concurrently running Cantinarr process its own runtime directory; startup removes stale `session-*` entries from that dedicated root. The official container uses its private Docker `/dev/shm` tmpfs. Use the tested Codex 0.144.3 release or a protocol-compatible build.

Native app passkeys require a public HTTPS server domain associated with the app (AASA for Apple, Digital Asset Links for Android). Browser passkey setup remains available when native association isn't possible. See [`server/README.md`](server/README.md#configuration) for details.

By default, users are passwordless and passkeyless: a connect link starts a permanent device session, so household members never deal with credentials. A session never expires -- not from idle time, server restarts, upgrades, or secret rotation -- and ends only when an admin revokes the device (**Settings > Devices**) or deletes the user. Admins grant a password and/or passkey per user from **Settings > Users** when a user needs one. A password is what authorizes MCP clients on deployments served over plain HTTP, where passkeys are unavailable (WebAuthn requires a secure context). Disabling a method is a real revoke -- it clears the stored password or deletes the user's passkeys. To recover access, an admin issues a fresh connect link.

## How It Works

### For Users
1. Admin sends you a connect link
2. Open the link on your device -- it creates your account and connects automatically
3. Browse movies, TV shows, and books powered by TMDB, Trakt, and Chaptarr
4. Tap "Request" on anything you want -- pick seasons or book formats if you like
5. Watch download progress live; get a push when your request is approved and when it's ready
6. Something wrong with a file? Tap "Report a problem"; Cantinarr quietly watches for an in-flight Radarr/Sonarr recovery, then investigates only if the problem persists
7. Ask the AI assistant for recommendations or to make requests for you. Use the included server provider when granted, or choose your own provider under **Settings > AI Access**

### For Admins
1. Deploy the container and complete the setup wizard
2. Add your shared API credentials and service instances from Settings; for included AI, either add an Anthropic/OpenAI/Gemini key or link a shared OpenAI (OAuth) account
3. Generate connect links for your household, grant included AI access where wanted, and pin per-user default instances if you run several
4. Optionally require approval for requests -- pending ones arrive as push notifications
5. Tap **Configure instant updates** on each Radarr/Sonarr instance so the server installs its authenticated webhook
6. Manage everything from the app -- queues, stuck imports, issues, agent fixes. No config files.
7. When a newer release ships, an in-app banner points you to it; optionally set an **Update Portal** link (**Settings > Admin**) to jump straight to your container manager. See [`docs/updating.md`](docs/updating.md).

### ID Bridge (TMDB-to-TVDB)

The core technical challenge: TMDB has better metadata and APIs, but Sonarr only accepts TVDB IDs. Cantinarr solves this transparently:

```
Request: "Add The Last of Us" (TMDB ID 100088)

1. Cache check     -> miss
2. TMDB external_ids API -> tvdb_id: 392256 (hit!)
3. Cache result (30 days)
4. Sonarr lookup by tvdb:392256 -> exact match
5. Add to Sonarr with the user's effective quality profile + root folder
```

If TMDB doesn't have a TVDB mapping (rare), the bridge falls back to Trakt's cross-reference database, then to a title+year search as a last resort.

Movies don't need bridging -- Radarr natively supports TMDB IDs. Books are keyed by Chaptarr/Readarr `foreignBookId` directly.

## Tech Stack

| Component | Technology |
|---|---|
| Server | Go 1.25, Chi router, SQLite (pure Go) |
| Client | Flutter (Dart), Riverpod, GoRouter |
| Auth | JWT (HS256), bcrypt, connect tokens, WebAuthn passkeys |
| AI | Personal or admin-shared Anthropic, OpenAI, and Gemini API credentials, plus personal or shared OpenAI OAuth via the bundled pinned Codex app-server; SSE app streaming |
| MCP | [mcp-go](https://github.com/mark3labs/mcp-go), Streamable HTTP + inbound Cantinarr OAuth |
| Real-time | gorilla/websocket + arr webhooks |
| Push | Self-hosted push gateway (APNs) |
| Discovery | TMDB API v3, Trakt API v2 (server-proxied) |
| Packaging | Multi-stage Docker with a checksum-verified pinned Codex app-server, go:embed, GHCR (`ghcr.io/windoze95/cantinarr`) |

## API Reference

Full API documentation is in [`server/README.md`](server/README.md#api-reference).

## Contributing

Contributions are welcome! Please open an issue to discuss your idea before submitting a PR. [`AGENTS.md`](AGENTS.md) is the operating manual for contributors and AI agents, and the [master regression catalog](docs/testing/README.md) is the behavioral contract every change must reconcile.

## License

AGPL-3.0 — See [LICENSE](LICENSE) for details.

Copyright (c) 2026 Julian Dice
