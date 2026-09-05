# Cantinarr

**Your media server just learned to run itself.**

**[cantinarr.com](https://cantinarr.com)** · **[Discord](https://discord.gg/zAgRwGwmVB)** · **[Live demo](https://demo.cantinarr.com)** · **[iPhone beta](https://testflight.apple.com/join/bCPDwCsD)** · **[Android beta](https://cantinarr.com/#android-beta)** · **[Request a feature](https://cantinarr.com/roadmap/)**

Discover and request movies, TV shows, books, and music. Get push notifications. Manage Radarr, Sonarr, Chaptarr, Lidarr, and your download clients. When downloads get stuck, Cantinarr diagnoses the cause and recommends the next step. You set the agent's operating boundaries. Your household gets the simple experience; you keep control of access, approvals, and quality.

```
┌──────────────────────────────────────────────────────────────┐
│  Cantinarr Server (Go, single container, port 8585)          │
│                                                              │
│  ┌──────────┐ ┌───────────┐ ┌─────────┐ ┌────────────────┐   │
│  │ Auth/JWT │ │ Requests  │ │ Issues +│ │ AI Chat        │   │
│  │ Passkeys │ │+ Approvals│ │ AI Agent│ │ + 41 AI Tools  │   │
│  └──────────┘ └─────┬─────┘ └─────────┘ └────────────────┘   │
│                     │                                        │
│  ┌──────────────────┴───────────────────┐  ┌──────────────┐  │
│  │  ID Bridge: TMDB → Trakt → TVDB      │  │ TMDB/Trakt   │  │
│  │  (cached 30 days)                    │  │ discovery    │  │
│  └───┬──────────┬──────────┬──────────┬──┘  └──────────────┘ │
│      │          │          │          │                      │
│  ┌───┴───┐ ┌────┴───┐ ┌────┴─────┐ ┌──┴───┐ ┌────────────┐   │
│  │Radarr │ │ Sonarr │ │ Chaptarr │ │Lidarr│ │Flutter Web │   │
│  └───┬───┘ └────┬───┘ └────┬─────┘ └──┬───┘ │ (embedded) │   │
│      │          │          │          │     └────────────┘   │
└──────┼──────────┼──────────┼──────────┼──────────────────────┘
       │          │          │          │   ▲ webhooks push
  ┌────▼───┐ ┌────▼───┐ ┌────▼─────┐ ┌──▼───┐  external changes
  │ Radarr │ │ Sonarr │ │ Chaptarr │ │Lidarr│  back instantly
  └────────┘ └────────┘ └──────────┘ └──────┘  (+ SABnzbd,
                                      qBittorrent, NZBGet,
                                      Transmission, Deluge,
                                      ruTorrent, Tautulli,
                                      Tracearr, push gateway)

┌───────────────────────────────┐
│  Cantinarr App (Flutter)      │      ┌─────────────────────┐
│  Discovery, Requests, Books,  │─────>│  Cantinarr Backend  │
│  Music, Arr control, Issues,  │ REST │  (the only API the  │
│  AI, Push notifications       │ + WS │   app talks to)     │
└───────────────────────────────┘      └─────────────────────┘
```

Single sign-on is available on web, iOS and Android with an external OpenID Connect provider. Configure it in **Settings > Single sign-on**; optional account creation, exact group restrictions and SSO-only policy preserve local administrator recovery. See [OIDC setup](docs/oidc-setup.md).

## Why Cantinarr?

- **Zero-config requesting** -- Your users never see API keys, TVDB IDs, or quality profiles. They browse, they tap, it works.
- **TMDB + Trakt for discovery** -- The best metadata, images, and trending data, proxied through the server so keys stay off devices -- and TMDB works out of the box on a built-in key, no signup needed. Sonarr's TVDB dependency is invisible. Every discovery row keeps loading and opens into a full grid, and a Browse page filters by genre, release year, and rating.
- **You choose what "popular" means** -- The headline row on the Movies and TV tabs reads TMDB weekly trending, or Trakt trending (ranked by who is actually watching), or TMDB's all-time popularity ranking. Connect Trakt and the rows switch to it automatically -- no second setting to find. An English-only switch keeps the discovery and recommendation rows to English-language originals; it ships on, and search always finds everything either way.
- **Automatic ID bridging** -- TMDB-to-TVDB translation with Trakt fallback. The #1 source of failed Sonarr adds, solved.
- **Books too** -- A Chaptarr (Readarr-API) module with per-format smarts: tap a book's eBook or Audiobook row to request that format; monitored formats read **Requested** until they download; a book page links out to its own page on Goodreads and Open Library (and Hardcover when Chaptarr's metadata names it), never a guessed search; owned-aware search and plain per-format controls stay pinned to the selected, authorized Chaptarr instance. Access is granted per user, and the Books tab opens on a Recently Added row so a book that just landed is visible without searching for it, plus Authors and Series rows -- Chaptarr has no popular feed, so the library's own authors and series are what you browse when you don't have a title in mind; tapping one lists the whole run with each title's per-format state, so a half-finished series shows exactly which books are missing.
- **Music too** -- A Lidarr module: search albums and artists from the Music tab, tap Request on an album, and the artist is added with exactly that album monitored -- one request never subscribes the whole discography. Access is granted per user like Books, the tab opens on Recently Added and Artists rows built from the library itself (Lidarr has no popular feed either), and requests stay pinned to the selected, authorized Lidarr instance. The module carries the rest of the stack too: a push when an album lands, album release dates on the calendar, the Import Doctor and interactive release search for stuck downloads, per-track file downloads, and Report a problem with the same agent repair as books. Full setup in [`docs/music-setup.md`](docs/music-setup.md).
- **Take available files with you** -- Optional, resumable downloads let signed-in users save exact ebook, audiobook, movie, and episode files from their authorized library. Cantinarr re-checks the live arr file record before issuing a short-lived, file-scoped link without putting arr credentials in the URL.
- **Request approvals** -- Optional approval queue, globally or per user. Admins also control per-user season choice, quality choice, and default quality profiles. Approve/deny lands as a push notification for the requester.
- **Kids accounts** -- Mark a user as a child and set what they can see: the highest movie and TV rating for your region, whether unrated titles show at all, and whole genres to hide. The server does the filtering for that account everywhere a title can appear -- the discovery rows from TMDB and Trakt, search, title pages, people, the library rows, the AI assistant, and push alerts -- so nothing is trusted to the device, a rating that cannot be read hides the title rather than showing it, and requests from a kids account start out needing approval. Their own Settings say it in plain words: Kids account, movies up to PG, shows up to TV-PG. Books and music carry no ratings and stay grant-only per user, so grant those to a child on purpose.
- **AI assistant** -- "What should I watch tonight?" Every user can bring a personal Anthropic, OpenAI, Gemini, or xAI Grok API key, or link a subscription account with a one-time browser code -- OpenAI (OAuth) through ChatGPT, or xAI Grok (OAuth) through SuperGrok / X Premium+ -- even without included access, and their choice never has to match the server's provider. Admins can configure the same providers as an included server profile and grant that shared access per user. A personal provider is an explicit override; Cantinarr never silently spends the shared account when that override needs attention. The assistant searches your library, checks availability, requests for you, and gives admins conversational queue and release control.
- **Local AI** -- A first-class **Local (OpenAI-compatible)** provider runs shared AI against your own server (llama.cpp, vLLM, Ollama): enter a base URL and model ID, pin a reasoning effort, and skip the API key entirely (most local servers ignore auth; an optional token slot covers proxies that don't). Assistant traffic never leaves your network, and the save-time test proves the endpoint before anything is stored.
- **AI remediation agent** -- Users tap "Report a problem" (or Cantinarr detects one itself, in the queue or as it imports); each report is bound to the exact Radarr/Sonarr instance and begins with a quiet observation window. Cantinarr gives Sonarr/Radarr time to retry or replace a download before it alerts anyone or starts the agent; a persistent quiet problem then enters the supervised workflow. Recovery cancels stale proposals before dispatch. Automatic resolution requires an exact changed file plus a matching post-incident import record—not queue disappearance or a file that was already there. **One whole class of this never needs reporting.** The moment Sonarr says it imported an episode that has not aired yet, Cantinarr checks that season against its own air dates: a file your service imported *before* that episode aired cannot be that episode, and a season already holding files for episodes that have not aired is content that does not exist yet. When that is what happened, the season is already waiting in your issues with a fix attached — one issue for the whole season however many bogus files arrived, and an instance with no instant updates configured gets the same check on a quarter-hour timer instead. One approval fixes it: the agent proposes deleting exactly those files, blocklisting the releases that delivered them, and searching for replacements — only the episodes that have actually aired, leaving the rest of the season for your service to grab as it comes out. One problem, one decision; you are never asked to approve the second half of a fix you already approved. When the fix lands on something you reported, you are the one who says whether it worked -- **"This is fixed"** closes your own report, so an admin is never asked to adjudicate content they haven't watched. Tired of approving the same fix? Checking **"Always approve"** on an approval arms a standing rule for that exact problem-and-fix pair (force imports and destructive queue actions stay separate opt-ins): future matches are approved and executed without paging you, and the rule pauses itself the moment a fix fails or an issue closes out unresolved. **Some problems should not be repaired again.** When the same fault keeps coming back on one service -- on separate days, not just across a dozen titles in one bad minute -- Cantinarr says so once and names the setting that would stop it: the free-space floor, the remote path mapping, the indexer whose torrents have no seeders behind them. That is advice and never an edit; Cantinarr changes no setting on your services. Closing the notice is what mutes it -- for a couple of months if you fixed it, for a year if you told Cantinarr you are not going to -- and it only ever comes back if the problem does. Where there is no honest answer to give, nothing is raised at all. Remediation is server-owned: it always uses the admin's shared API key or shared OAuth connection (OpenAI or xAI Grok) and never a reporter's personal provider or per-user included-access grant. Admins may give remediation its own tested model designation while keeping that global provider and credential.
- **MCP server** -- 40 of the 41 in-app AI tools are exposed as a [Model Context Protocol](https://modelcontextprotocol.io/) endpoint at `/mcp`, with OAuth discovery, browser/passkey login, dynamic client registration, and persistent rotating refresh tokens. Only the quality-profile apply tool remains in-app-only because its one-use safety handoff depends on authenticated in-app chat-turn provenance; the external preview instead parks a proposal an admin approves in the app. This inbound OAuth lets external clients access Cantinarr; it is separate from the outbound personal/shared OpenAI OAuth used by Codex chat. Every tool can be toggled on/off from Settings > AI Tools.
- **Deep *arr control** -- SABnzbd, qBittorrent, NZBGet, Transmission, Deluge, and ruTorrent modules with live queue management (an aggregate All view with a master pause across every client when several are configured), plus drill-down Radarr/Sonarr control: series → season → episode with per-item progress, quality, and history; episode multi-select with batch search; long-press action menus; Edit Series and Edit Movie; links out to IMDb, TMDB, and Trakt (plus TheTVDB for series), and from a Chaptarr book to Goodreads, Open Library, and the Hardcover page Chaptarr declares; interactive release search everywhere. Admin AI/MCP tools can inspect quality profiles and import or update native/TRaSH custom formats across Radarr, Sonarr, Chaptarr, and Lidarr. After an explicit admin request, in-app AI previews and autonomously applies a narrow profile score, cutoff, or upgrade-policy change in the same authenticated chat turn. AI/MCP profile and custom-format writes are recorded under Settings > Configuration history for later review and live comparison. Each applied quality-profile update can be restored once, only while Cantinarr's instance, profile, and dependency guards still match; the linked restore is final, and custom-format entries are review-only.
- **Import Doctor** -- when a download is stuck, Cantinarr explains *why* in plain English (sample file, un-extracted archive, unconfirmed TheXEM mapping, "not an upgrade", unparseable/invalid file, remote-path-mapping or download-client problems, stalled torrent, permissions...) and offers **one-click fixes** with full transparency: manual/force import with the candidate files shown, remove + blocklist + re-search, hand-off to a tool like Unpackerr, or rescan. Cantinarr clears the stuck item and leaves the replacement to your service's own settings — with one exception: when the download was only an upgrade for something you already have *and* nothing asked for it (your service picked it up on its own), it is simply dropped. Your copy stays watchable, and a better version is still picked up whenever one shows up. The same diagnosis backs the app, the AI assistant, the remediation agent, and MCP.
- **Flexible requests** -- request a whole title in one tap, or pick exactly which **seasons** (or book **formats**) you want; partially-available shows surface per-season availability and a one-tap path to request the rest.
- **Always in sync** -- availability is computed live from the arrs (never from a stale snapshot), and server-managed Radarr/Sonarr/Chaptarr/Lidarr webhooks -- installed automatically the moment you add an instance -- push manual imports, deletes, and adds into the app the moment they happen without exposing callback credentials to a device. Books gain the most: an ebook can finish downloading between two polls, so instant updates are what make its "ready to read" alert reliable.
- **Push notifications** -- APNs (iOS) and FCM (Android) via a self-hosted push gateway with zero-config auto-enrollment: new-content alerts for movies, episodes, and books, approval/issue alerts for admins, per-user preference toggles, deep links into the right screen.
- **Plex, Jellyfin, and Emby access** -- connect your media server and choose which users get access. On Jellyfin or Emby each of them creates their own account from the app with a password only they know, or links one they already have by signing in with it once (administrator accounts included; Cantinarr never changes those); on Plex they sign in with their own Plex account, or share its email, and the invite goes out the moment you grant them (or on its own, with auto-approve). Already have users on the server? Import them from Users: each picked account becomes a Cantinarr user of the same name, granted and linked, with a connect link to hand out. Everyone sees the libraries you picked and the address to sign in at, an available title's page gets a **Watch on Plex** / **Watch on Jellyfin** / **Watch on Emby** button that opens the matching title (looked up live with their own access); when Plex cannot verify the title, **Open Plex** opens the sign-in address instead, and the app shows the live state: invite pending, accepted, or switched off. Take access away and a Jellyfin or Emby account is switched off rather than deleted, so watch history survives, and a Plex share is removed; grant again and it comes back (for someone still connected to your Plex account, without another invite).
- **Monitoring** -- watch what's playing right now: active streams with quality/transcode badges and the server they play from, watch history, and top movies/shows/users stats. Tautulli covers Plex; Tracearr covers Plex, Jellyfin, and Emby from one instance, and both feed the same module.
- **Secrets encrypted at rest** -- arr API keys, download-client passwords, webhook tokens, shared and personal AI credentials, and OpenAI/xAI OAuth authorizations are AES-256-GCM encrypted in the database.
- **Household-friendly** -- Connect links, passwordless by default, role-based access, kids accounts, per-user default instances. Admins manage services; users just browse and request.
- **Guided setup** -- a live checklist wizard derived from what's actually configured: every step opens the real settings screen, progress can't go stale, and newly shipped features appear on the list automatically.
- **Single container** -- The static Go API/web server plus a pinned Codex app-server helper, with one exposed port. Runs great on a Raspberry Pi or NAS.

## Quick Start

```bash
git clone https://github.com/windoze95/cantinarr.git
cd cantinarr
docker compose up -d
```

This pulls the published image (`ghcr.io/windoze95/cantinarr:latest`); updating later
is `docker compose pull && docker compose up -d`. To build the image from your
checkout instead, layer the dev override:
`docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build`.

Or add Cantinarr to an existing stack (Portainer, Dockhand, etc.) -- no clone
needed; this minimal service is the whole setup:

```yaml
services:
  cantinarr:
    image: ghcr.io/windoze95/cantinarr:latest
    ports:
      - "8585:8585"
    volumes:
      - ./config:/config
    # Optional: enables push notifications (see Configuration)
    # environment:
    #   - CANTINARR_PUSH_GATEWAY_URL=https://push.cantinarr.com
    restart: unless-stopped
```

Update it later with `docker compose pull && docker compose up -d`. The container
runs as root unless you set `PUID`/`PGID` in its environment (see Configuration
below), which also makes `./config` belong to that user on start -- the convention
Synology, Unraid, and other linuxserver-style stacks expect.

Open `http://your-server:8585` -- the setup wizard walks you through creating an admin account. Discovery and search work immediately on the built-in TMDB key. Then connect your services (Radarr, Sonarr, etc.) from **Settings > Providers & Credentials** and **Settings > Add Instance** in the admin UI. Configure an included AI provider there (fresh installs preselect OpenAI OAuth with the fast GPT-5.6 Luna model -- connecting a ChatGPT account is all it takes) and grant it per user, or let each person bring a provider under **Settings > AI Access**.

### Unraid

Search **Cantinarr** in the **Apps** tab. The listing was approved on 2026-08-16
and appears once the next Community Applications build publishes; its template
lives in [`windoze95/cantinarr-unraid`](https://github.com/windoze95/cantinarr-unraid).

If it is not there yet, add the file to Unraid's user-template folder yourself,
since Unraid removed custom template repositories:

```bash
curl -o /boot/config/plugins/dockerMan/templates-user/my-cantinarr.xml \
  https://raw.githubusercontent.com/windoze95/cantinarr-unraid/main/templates/cantinarr.xml
```

Then **Docker > Add Container**, pick `Cantinarr` from the Template dropdown, and
Apply. It publishes port 8585 and keeps the database and encryption key in
`/mnt/user/appdata/cantinarr`. Two advanced fields are worth opening: **Public
URL**, the origin your arrs POST webhooks back to, and the read-only **Media
library** mount plus **Media roots**, which together turn on completed-media
downloads.

### Prebuilt binaries (no Docker)

Every release attaches `cantinarr-linux-amd64.tar.gz` and `cantinarr-linux-arm64.tar.gz`
(with `.sha256` checksums), extracted from the same build as the published image. Each
contains the `cantinarr` server with the web app embedded, the pinned `codex-app-server`
runtime it can spawn for ChatGPT-subscription AI access, and license notices:

```bash
tar -xzf cantinarr-linux-amd64.tar.gz
install -m 0755 cantinarr codex-app-server /usr/local/bin/
mkdir -p /config   # database + generated encryption key live here
cantinarr          # serves everything on 8585 (CANTINARR_PORT overrides)
```

### From Source

```bash
# Server (requires Go 1.25+)
cd server
go run ./cmd/server

# App (requires Flutter stable, Dart SDK 3.4+)
cd app
flutter pub get
flutter run
```

`make` builds the full stack (Flutter web → embedded in the Go binary).

Published images stamp the web bundle with a build number that **Settings > About**
reports. Building the image yourself, pass your own with
`docker build --build-arg APP_BUILD_NUMBER=<n> .`; leave it out and About falls back
to `pubspec.yaml`'s placeholder.

## Get the app

The mobile apps are in beta ahead of their public store launch. Every one of these
talks only to your own server, so stand one up first (above) -- or open the
[live demo](https://demo.cantinarr.com) in a browser to look around.

- **iPhone and iPad** -- join the public beta on
  [TestFlight](https://testflight.apple.com/join/bCPDwCsD). No invite needed.
- **Android** -- [ask for a tester slot](https://cantinarr.com/#android-beta). Play
  testing is closed, so testers are added by hand: email **windoze95@proton.me**
  with the address associated with your Play Store (Google) account -- that exact
  address is what Google needs to let you in -- and you'll get the opt-in link back.
- **Any browser** -- your server already serves the full app at
  `http://your-server:8585`. Nothing to install.

## Repository Structure

```
cantinarr/
├── server/                 # Go backend -- see server/README.md
│   ├── cmd/server/         # Entry point
│   └── internal/           # ai, api, arr, auth, cache, chaptarr, codexapp,
│                           # config, credentials, db, deluge, discover, downloads, emby, grokoauth, httpx, instance, jellyfin,
│                           # mcp, mcpserver, mediaaccess, mediafiles, mediapath, mediaserver, nzbget, proxy, push, qbittorrent,
│                           # radarr, remediation, request, rutorrent, sabnzbd, secrets,
│                           # sonarr, tautulli, tmdb, tracearr, trakt,
│                           # transmission, watchhistory, web, webhooks,
│                           # websocket
│
├── app/                    # Flutter client (iOS, web) -- see app/README.md
│   ├── lib/
│   │   ├── core/           # Models, networking, realtime, theme, widgets
│   │   ├── features/       # auth, discover, request, dashboard, sonarr,
│   │   │                   # radarr, chaptarr, downloads, media_download, monitoring, issues,
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

Shared service credentials are managed through the admin UI -- no environment variables are needed for API keys. AI is different from the other integrations: an admin can configure a server profile using an API key or a shared OAuth link (OpenAI or xAI Grok), while every user can independently configure the same choices as a personal override. API keys and OAuth authorization stay encrypted and server-side. Self-hosted AI is its own provider: pick **Local (OpenAI-compatible)** in **Settings > Providers & Credentials**, enter the server's base URL (llama.cpp `llama-server`, vLLM, Ollama, and similar; use the endpoint's final URL, usually ending in `/v1` -- redirects are not followed) and the model ID it hosts. No API key is needed (an optional token slot covers proxies that check auth), the same save-time test proves the endpoint before anything is stored, and personal OpenAI keys are unaffected and keep using api.openai.com. Both the Local and hosted OpenAI providers offer a reasoning-effort pin (Auto/None/Minimal/Low/Medium/High) -- None keeps thinking-heavy local models fast, Auto preserves the provider's own default. Every provider, model, remediation-model override, or key save -- and every completed OAuth selection -- must complete one small real, tool-free, low-reasoning message-response turn before Cantinarr activates it. Validation reports a safe actionable category for an invalid credential, unsupported model/access, exhausted quota, or temporary provider outage without exposing upstream secrets. OpenAI OAuth offers the recommended Codex model plus GPT-5.6 Sol, Terra, and Luna. xAI Grok (OAuth) signs in with a SuperGrok or X Premium+ account via xAI's device flow and serves the same Grok models as the API-key path.

The server also runs one small shared-model health turn every 24 hours by default. A failure opens one deduplicated admin-only issue; a later successful turn resolves it. Admins who want zero background AI usage can disable this check in **Settings > Providers & Credentials** without weakening the mandatory save-time test. The remediation agent remains independent of this monitor and always resolves credentials directly from the admin's shared profile.

Included AI is an explicit per-user entitlement for new accounts; the initial admin starts enabled. Upgrades preserve the previous global-provider behavior for existing users so access does not disappear, after which the admin can revoke or grant it from **Settings > Users**. Enabling an OpenAI OAuth-backed grant shows the shared-account allowance and cost warning before it is applied.

| Setting | Where | Description |
|---|---|---|
| TMDB access token | Admin UI | Optional -- discovery and search ship working on Cantinarr's built-in public key; add your own token to use your TMDB account instead ([get one here](https://www.themoviedb.org/settings/api)) |
| Radarr/Sonarr instances | Admin UI | Add via Settings > Add Instance |
| Chaptarr instance | Admin UI | Books module; grant access per user from the instance editor or user settings -- full walkthrough in [`docs/books-setup.md`](docs/books-setup.md) |
| Lidarr instance | Admin UI | Music module; grant access per user the same way -- full walkthrough in [`docs/music-setup.md`](docs/music-setup.md) |
| SABnzbd/qBittorrent/NZBGet/Transmission/Deluge/ruTorrent | Admin UI | Download client modules (queue, history, speeds) |
| Tautulli or Tracearr instance | Admin UI | Monitoring: live streams, watch history, stats. Tautulli watches Plex; Tracearr watches Plex, Jellyfin, and Emby (public API key from its Settings > General) |
| Plex, Jellyfin, or Emby instance | Admin UI | Media server access: per-user grants, shared libraries, and the sign-in address users see; Plex links a plex.tv account with a PIN and picks the server to share |
| Anthropic/OpenAI/Gemini/xAI API key | Admin UI | Enables shared API-key-backed AI chat and autonomous remediation |
| OpenAI reasoning effort | Admin UI | Optional; pins `reasoning_effort` for the shared OpenAI provider (none/minimal/low/medium/high). Auto sends no effort field; endpoints that reject the field fall back automatically |
| Local (OpenAI-compatible) | Admin UI | First-class shared provider for self-hosted OpenAI-compatible servers: required base URL and model ID, optional key/token, own reasoning-effort pin. Shared profile only -- never selectable as a personal provider |
| OpenAI (OAuth) | Personal link under Settings > AI Access, or an admin-managed shared link | Uses a ChatGPT account's Codex allowance for the selected personal or included model; the admin-shared link also powers server-owned remediation. Per-user shared chat access is opt-in and carries a quota/cost warning |
| xAI Grok (OAuth) | Personal link under Settings > AI Access, or an admin-managed shared link | Uses an xAI account's Grok subscription allowance (SuperGrok or X Premium+) via xAI's device flow instead of a metered API key; the admin-shared link also powers server-owned remediation |
| Trakt client ID | Admin UI | Enhances discovery + fallback ID bridging; required to select the Trakt trending source under Settings > Discovery, which the headline rows then adopt automatically |
| Discovery row source | Admin UI | Settings > Discovery: which feed backs the headline rows (Trakt when configured, else TMDB trending), plus the English-only filter (on by default) |
| Outbound proxy | Admin UI | Settings > Outbound Proxy: an `http`, `https`, `socks5`, or `socks5h` proxy for the server's internet-bound traffic only (TMDB, Trakt, hosted AI providers, plex.tv, the GitHub update check, the push relay), with an optional username and password stored encrypted -- the password is write-only. Arr instances, download clients, Plex Media Server, Jellyfin/Emby, Tautulli/Tracearr, and the Local AI provider are never proxied, though a Local AI endpoint that lives out on the internet can be switched onto the proxy from its own settings. **Test** fetches TMDB through the proxy and reports the server's reason on failure; a blank address clears it |

**Instance URLs are dialed only by the Cantinarr server** -- phones and browsers never contact them, so cluster-internal names (Docker service names like `http://radarr:7878`, Kubernetes cluster DNS, Tailscale MagicDNS) are the recommended form, and the arrs never need to be exposed outside their network. One topology exception: a container that shares another container's network stack (`network_mode: container:<gateway>`, or Unraid's `Container` network type -- common when routing a service through a VPN gateway) has no address or DNS name of its own, so `http://chaptarr:8789` never resolves. Point the instance URL at the gateway that publishes the port instead. The in-app **Test Connection** button runs from the server too, so it tells the truth about these URLs. Plain `http` is fully supported on a trusted network; `https` needs a certificate the server's container trusts (mount an internal CA into the image trust store -- a self-signed cert otherwise fails the connection test with an x509 error). Four service-specific notes: SABnzbd's hostname verification rejects service names it doesn't know, so add the name to its `host_whitelist` (Config > Special) or set the container's hostname to match; for Transmission enter just `scheme://host:port` -- Cantinarr appends `/transmission/rpc`; for Deluge enter the web UI address (port 8112 by default, e.g. `http://deluge:8112`) -- Cantinarr appends `/json` -- and the only credential is its web UI password, since Deluge has no username; for ruTorrent enter the ruTorrent address (e.g. `http://rutorrent:8080`, or the path ruTorrent is served under) -- Cantinarr appends `/plugins/httprpc/action.php` to speak XML-RPC to rTorrent and removes files through the erasedata plugin, both of which ship with ruTorrent, so ruTorrent must have been opened at least once (that is when it learns rTorrent's command names; until then Cantinarr says so and keeps the torrent) -- and the HTTP Basic username and password are optional, needed only when the web server asks for them. Poster and fanart images load on devices straight from the TMDB/TVDB CDNs, so client devices still need internet egress to those hosts.

**Internet-bound traffic can ride a proxy** -- the pattern Radarr and Sonarr users know from Settings > General > Proxy, for a server whose metadata and AI traffic should leave through a VPN: run Privoxy or 3proxy beside the VPN gateway on the same Docker host, then enter it under **Settings > Outbound Proxy** as an `http`, `https`, `socks5`, or `socks5h` URL (`http://proxy:8118`, no path) with an optional username and password. The password is write-only and the whole setting is stored encrypted; **Test** fetches TMDB's configuration through the candidate proxy and reports the server's reason when it fails (wrong port, wrong password, and so on). Only traffic that leaves for the internet takes that route: TMDB, Trakt (the API and the artwork relay), the hosted AI providers (Anthropic, OpenAI, Gemini, xAI Grok, and the bundled Codex app-server through its environment), plex.tv, the GitHub update check, and the push relay. Arr instances, download clients, Jellyfin and Emby, Tautulli and Tracearr, the instant-updates webhook install, the instance connection test, and the Local (OpenAI-compatible) AI provider are dialed directly -- not even `HTTP_PROXY` in the environment changes that -- so `NO_PROXY` never needs your arr hosts, and a deployment that relied on `HTTP_PROXY` to reach its arrs now dials them directly. The Local AI provider is the one you can move, because its endpoint is the only one you type in yourself: if it is a rented GPU box or any other server out on the internet rather than one on your own network, turn on **Route through the outbound proxy** beside its base URL under **Settings > Providers & Credentials** and that endpoint alone joins the internet-bound list above. It is off by default, and Cantinarr will not guess it from the address, because split-horizon DNS and Tailscale both hand out names that an address test reads wrong. The in-app setting proxies every internet-bound host with no bypass list; when it is empty, the standard `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` variables (Go's semantics, lower-case names accepted) apply to the same traffic instead. Two caveats: a self-hosted push relay on the LAN counts as internet-bound, so that layout belongs on the env vars with `NO_PROXY` rather than the in-app setting; and the bundled Codex app-server reads the proxy through its environment, so use an HTTP or HTTPS proxy when OpenAI (OAuth) is the AI provider -- Cantinarr does not verify its SOCKS support. The proxy is server-side only: devices keep loading posters straight from the TMDB/TVDB CDNs as above.

Completed-media downloads are deliberately opt-in because Radarr, Sonarr, Chaptarr, and Lidarr report paths but do not serve those file bytes through their APIs. Configuration has two layers: the deployment makes each wanted library read-only to Cantinarr and lists the Cantinarr-visible boundary in `CANTINARR_MEDIA_ROOTS`, then the admin maps each media instance's reported path to a folder inside that boundary from the instance editor. The two paths do not have to match, and an arr source may use POSIX, Windows drive, or UNC syntax regardless of the Cantinarr host OS. For Docker, for example, mount `- /mnt/nas/media:/media:ro`, set `CANTINARR_MEDIA_ROOTS=/media`, and map Radarr's `/data/media/movies` to `/media/movies`; a native server instead uses an absolute local directory readable by its process. A Chaptarr instance may have separate mappings for `/ebooks`, `/audiobooks`, `/yana-ebooks`, and `/yana-audiobooks`; folder names never determine the book format. Albums are delivered per track, never repackaged into an archive.

Download controls are enabled per instance: an instance offers downloads only after an admin saves explicit path mappings for it, and every instance starts with media downloads off. Cantinarr accepts only live file IDs from a user's effective Radarr/Sonarr instance or granted Chaptarr or Lidarr instance, refuses files outside that instance's mappings and the global roots, and gives the app a short-lived file-scoped link so large files stream through the browser or operating system without buffering in Flutter. The feature covers the primary files indexed by the arrs, not arbitrary files, subtitles, or extras found on disk.

Optional server env vars for deployment tuning:

| Variable | Default | Description |
|---|---|---|
| `CANTINARR_PORT` | `8585` | HTTP listen port. Kubernetes service-link values (`tcp://…`) injected by a Service named `cantinarr` are ignored in favor of the default; set a numeric value to override |
| `CANTINARR_SERVER_NAME` | `Cantinarr` | Display name shown in clients |
| `CANTINARR_ARR_CALLBACK_URL` | direct request origin | Origin the Radarr/Sonarr/Chaptarr/Lidarr containers POST webhooks back to, so it must be resolvable and reachable **from the arrs themselves** -- in same-network/cluster deployments a cluster-internal origin like `http://cantinarr:8585` is usually the right value. Set it explicitly behind a reverse proxy (forwarded headers are deliberately ignored). Formerly `CANTINARR_PUBLIC_URL`, which stays accepted forever (the new name wins when both are set); it was renamed because "public URL" suggested the user-facing address, which is the in-app **Settings > External Address** instead |
| `CANTINARR_OAUTH_ISSUER` | request-derived origin | Canonical external HTTPS origin for inbound MCP OAuth metadata, token audience, and browser-origin checks; setting it also enables stable RFC 9207 authorization-response `iss` and permits that origin to call `/mcp`. Set it behind a reverse proxy and keep it stable (changing it makes existing audience-bound MCP tokens reconnect); do not substitute the arr-reachable `CANTINARR_ARR_CALLBACK_URL` |
| `CANTINARR_MCP_ALLOWED_ORIGINS` | unset | Comma-separated additional browser origins allowed to call `/mcp`. If neither this nor `CANTINARR_OAUTH_ISSUER` is configured, requests that supply `Origin` are rejected; native and server-side MCP clients need no entry |
| `CANTINARR_JWT_SECRET` | auto-generated | HMAC secret for signing short-lived access tokens. Device sessions do not depend on it: changing it never signs anyone out |
| `CANTINARR_ENCRYPTION_KEY` | auto-generated key file | Base64 32-byte key for secrets-at-rest (default: `/config/encryption.key`) |
| `CANTINARR_AI_PROVIDER` | `codex` | Fallback provider for the included server AI profile when none is saved in the admin UI (`anthropic`, `openai`, `gemini`, `grok`, `codex`, or `grok_oauth`) |
| `CANTINARR_AI_MODEL` | provider default | Fallback model for the included server AI profile when none is saved in the admin UI |
| `CANTINARR_CODEX_BIN` | auto-discovered | Optional path to `codex-app-server` or the full `codex` CLI; container images bundle the tested 0.144.3 app-server at `/usr/local/bin/codex-app-server` |
| `CANTINARR_CODEX_RUNTIME_DIR` | `/dev/shm/cantinarr-codex` | Absolute Linux tmpfs/ramfs directory used for server-owned, ephemeral per-session Codex state; if it already exists, it must be owned by the server user with mode `0700` |
| `CANTINARR_MEDIA_ROOTS` | unset | Comma-separated absolute paths forming the outer filesystem allowlist for completed-media downloads. Empty disables file downloads. Mount libraries read-only inside these Cantinarr-visible roots, then map each arr-reported prefix to a path beneath them in that instance's settings; `/` is refused |
| `CANTINARR_PUSH_GATEWAY_URL` | unset | Push gateway origin -- setting it enables push notifications (auto-enrolls on first start). The community relay is `https://push.cantinarr.com`; its former name `https://push.julian.codes` is still accepted and rewritten to the new one at start (same gateway, same enrollment) |
| `CANTINARR_PUSH_API_KEY` | unset | Optional pinned gateway key (blank = auto-enroll) |
| `CANTINARR_PUSH_ENROLL_TOKEN` | unset | Only for gateways with gated enrollment |
| `CANTINARR_APPLE_APP_IDS` | unset | `TeamID.BundleID` values for native Apple passkeys (`/.well-known/apple-app-site-association`) |
| `CANTINARR_ANDROID_PACKAGE_NAME` | `codes.julian.cantinarr` | Android package name for native passkeys |
| `CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS` | unset | Android signing cert fingerprints for `/.well-known/assetlinks.json` |
| `CANTINARR_WEBAUTHN_EXTRA_ORIGINS` | unset | Additional WebAuthn origins to trust |
| `CANTINARR_DISABLE_UPDATE_CHECK` | unset | Set to `1` to disable the periodic GitHub release check behind the admin update-status endpoint |
| `HTTP_PROXY` / `HTTPS_PROXY` | unset | Standard proxy variables (Go's semantics; lower-case names accepted) for the server's internet-bound traffic only -- TMDB, Trakt, hosted AI providers, plex.tv, the GitHub update check, and the push relay. An address saved under **Settings > Outbound Proxy** wins whenever one is set. Arr instances, download clients, Plex Media Server, Jellyfin/Emby, Tautulli/Tracearr, and the Local AI provider are dialed directly no matter what these say |
| `NO_PROXY` | unset | Hosts the env-var proxy skips (Go's semantics). It never needs your arr, download-client, or media-server hosts, because LAN instance traffic is never proxied; it is the right tool for a self-hosted push relay on the LAN, which the in-app setting would proxy |
| `PUID` | unset (runs as root) | Container image only. Run the server as this user id: on every start the image takes ownership of `/config` for it, so the database and encryption key it writes are owned by that user on the host (the linuxserver-style convention Synology and Unraid stacks expect). Ignored when the container is already started as a non-root user (compose `user:`, TrueNAS) |
| `PGID` | same as `PUID` | Group id to pair with `PUID`; ignored without it |

Source image builds also accept the Docker build argument
`CANTINARR_E2E_WEB_SEMANTICS` (default `false`). It exists only for the
disposable private lab: setting it to `true` compiles deterministic Maestro
labels into the Flutter web bundle. Official production images keep the
default and preserve normal browser accessibility semantics.

OpenAI (OAuth) source deployments use Codex app-server and are supported only on Linux; non-Linux hosts report this provider unavailable even when a Codex binary is installed. The runtime directory's parent must exist, and the directory must be on tmpfs or ramfs—not persistent storage. Give each concurrently running Cantinarr process its own runtime directory; startup removes stale `session-*` entries from that dedicated root. The official container uses its private Docker `/dev/shm` tmpfs. Use the tested Codex 0.144.3 release or a protocol-compatible build.

Native app passkeys require a public HTTPS server domain associated with the app (AASA for Apple, Digital Asset Links for Android). Browser passkey setup remains available when native association isn't possible. See [`server/README.md`](server/README.md#configuration) for details.

By default, users are passwordless and passkeyless: a connect link starts a permanent device session, so household members never deal with credentials. Each link signs one device into the app, once. A session never expires -- not from idle time, server restarts, upgrades, or secret rotation -- and ends only when an admin revokes the device (**Settings > Devices**) or deletes the user. Admins grant a password and/or passkey per user from **Settings > Users** when a user needs one -- that is also the durable way to sign in on the web, where clearing browser data wipes the device session a link created. A password is what authorizes MCP clients on deployments served over plain HTTP, where passkeys are unavailable (WebAuthn requires a secure context). Disabling a method is a real revoke -- it clears the stored password or deletes the user's passkeys. To recover access, an admin issues a fresh connect link.

Connect links embed a server address. Set **Settings > External Address** to the origin people reach your server through (a reverse proxy domain, a public IP) and links are built from it; left unset, a link uses the address the generating admin's own app is connected with, which usually only works on the admin's network -- the invite dialog says so when that happens.

## How It Works

### For Users
1. Admin sends you a connect link
2. Open the link on your device -- it creates your account and connects automatically
3. Browse movies, TV shows, books, and music powered by TMDB, Trakt, Chaptarr, and Lidarr
4. Tap "Request" on anything you want -- pick seasons for a show, tap a book's eBook or Audiobook row to request that format, or request an album
5. Watch download progress live and get push notifications
6. Something wrong with a file? Tap "Report a problem"; Cantinarr quietly watches for an in-flight Radarr/Sonarr recovery, then investigates only if the problem persists
7. Ask the AI assistant for recommendations or to make requests for you. Use the included server provider when granted, or choose your own provider under **Settings > AI Access**
8. Sharing a media server? Open **Watch on Plex** / **Watch on Jellyfin** / **Watch on Emby** in the menu: create your account or sign in with one you already have (Jellyfin, Emby), or sign in with Plex or share your Plex email and accept the invite (Plex), then sign in at the address shown; an available title's page in Cantinarr also has a **Watch on** button when the media server confirms the match, or **Open Plex** when an exact Plex link cannot be verified

### For Admins
1. Deploy the container and complete the setup wizard
2. Add your shared API credentials and service instances from Settings; for included AI, either add an Anthropic/OpenAI/Gemini/xAI key or link a shared OpenAI (OAuth) or xAI Grok (OAuth) account; a Plex, Jellyfin, or Emby instance also chooses which users get access there
3. Invite your household from **Settings > Users** (set **Settings > External Address** first so links work away from home), grant included AI access where wanted, and pin per-user default instances if you run several
4. Optionally require approval for requests -- pending ones arrive as push notifications
5. Instant updates come on by themselves: adding a Radarr/Sonarr/Chaptarr/Lidarr instance installs the server's authenticated webhook automatically (books need it most -- an ebook can finish downloading between two polls). Each instance's edit screen shows the live state and a **Configure instant updates** button to repair it -- e.g. after changing `CANTINARR_ARR_CALLBACK_URL`
6. Manage everything from the app -- queues, stuck imports, issues, agent fixes. No config files.
7. Updating means pulling the newer image and recreating the container -- see [`docs/updating.md`](docs/updating.md). Optionally set an **Update Portal** link (**Settings > Admin**) so an in-app update warning jumps straight to your container manager.

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

If TMDB doesn't have a TVDB mapping (rare), the bridge falls back to Trakt's cross-reference database, then to a Sonarr title search as a last resort -- accepted only when the candidate's premiere year matches TMDB's (±1), because same-titled series (a reboot vs the original) are distinct records and a request fails rather than fulfilling the wrong one.

Movies don't need bridging -- Radarr natively supports TMDB IDs. Books are keyed by Chaptarr/Readarr `foreignBookId` and albums by their MusicBrainz release-group id directly.

## Tech Stack

| Component | Technology |
|---|---|
| Server | Go 1.25, Chi router, SQLite (pure Go) |
| Client | Flutter (Dart), Riverpod, GoRouter |
| Auth | JWT (HS256), bcrypt, connect tokens, WebAuthn passkeys, external OIDC sign-in with PKCE |
| AI | Personal or admin-shared Anthropic, OpenAI, Gemini, and xAI Grok API credentials, plus personal or shared OpenAI OAuth via the bundled pinned Codex app-server and xAI Grok OAuth via xAI's device flow; SSE app streaming |
| MCP | [mcp-go](https://github.com/mark3labs/mcp-go), Streamable HTTP + inbound Cantinarr OAuth |
| Real-time | gorilla/websocket + arr webhooks |
| Push | Self-hosted push gateway (APNs) |
| Discovery | TMDB API v3, Trakt API v2 (server-proxied) |
| Packaging | Multi-stage Docker with a checksum-verified pinned Codex app-server, go:embed, GHCR (`ghcr.io/windoze95/cantinarr`) |

## API Reference

Full API documentation is in [`server/README.md`](server/README.md#api-reference).

## Related Projects

- [**mam-chaptarr-protonvpn-skill**](https://github.com/windoze95/mam-chaptarr-protonvpn-skill) -- an agent skill for building the layer *below* Cantinarr's books module: a VPN-isolated Gluetun/ProtonVPN stack running Chaptarr and qBittorrent in one network namespace, with forwarded-port sync, separated indexer and tracker-host sessions, and end-to-end verification. Useful if you're standing up book automation from scratch and want the container topology right the first time. Independent project; [`docs/books-setup.md`](docs/books-setup.md) shows where it fits.

## Contributing

Pull requests are welcome. Three ways to shape the project:

- **Pull requests** -- fork, branch, and open one. Run the checks first (`go vet ./...` and `go test ./...` from `server/`; `flutter analyze --no-fatal-infos` and `flutter test` from `app/`), and update any doc your change makes untrue. CI runs the same checks on every PR, and a first-time contributor's run needs one approval from the maintainer before it starts. For anything large, open an issue first so the shape can be agreed before you build it.
- **Bugs and technical issues** -- open one on the [issue tracker](https://github.com/windoze95/cantinarr/issues).
- **Feature requests** -- post and vote at [cantinarr.com/roadmap](https://cantinarr.com/roadmap/), no account needed.

[`AGENTS.md`](AGENTS.md) is the operating manual for the repo: branch protocol, verification commands, architecture conventions, and the documentation standard. It applies to human contributors and AI agents alike.

## Community

Questions, setup help, and release news: [Discord](https://discord.gg/zAgRwGwmVB). Bugs still go to the [issue tracker](https://github.com/windoze95/cantinarr/issues) and feature requests to the [roadmap](https://cantinarr.com/roadmap/), so they stay searchable and votable.

## Support

If Cantinarr is useful to you, you can support its development through [GitHub Sponsors](https://github.com/sponsors/windoze95).

## Attribution

This product uses the TMDB API but is not endorsed or certified by [TMDB](https://www.themoviedb.org/).

## License

AGPL-3.0 — See [LICENSE](LICENSE) for details.

Copyright (c) 2026 Julian Dice
