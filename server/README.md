# Cantinarr Server

The backend brain for [Cantinarr](https://github.com/windoze95/cantinarr) -- the self-hosted server behind its discovery, request, management, and repair loop.

A single Go binary that bridges your arr stack, serves the web UI, and keeps API keys off user devices. Drop it on your NAS, point it at Radarr/Sonarr (and Chaptarr for books), and generate connect links for family and friends.

```
                     Cantinarr Server (:8585)
  ┌───────────────────────────────────────────────────────────┐
  │  Auth (JWT/passkeys)   Requests + Approvals   AI Chat     │
  │        │                     │                   │        │
  │        │              ┌──────┴──────┐      38 AI Tools    │
  │        │              │  ID Bridge  │            │        │
  │        │              └──┬───────┬──┘     AI Remediation  │
  │        │                 │       │            Agent       │
  │   Instance registry ── Radarr  Sonarr  Chaptarr           │
  │        │                 ▲       ▲                        │
  │   Scrubbed arr proxy ────┘       └──── Webhook receiver   │
  │                                                           │
  │  Downloads (SAB/qBit/NZBGet/Transmission)   Tautulli      │
  │  WebSocket hub          Push gateway client               │
  │  TMDB/Trakt discover proxy      Flutter web (embedded)    │
  └───────────────────────────────────────────────────────────┘
```

## Features

- **One-tap requests with an approval queue** -- Users browse and tap request; the server handles ID bridging, arr lookups, quality profiles, and root folders. Admins can require approval globally or per user, and per user allow season-level choice, quality choice, and per-service default quality profiles.
- **Books via Chaptarr** -- A Readarr-API (v1) books module with per-format (ebook/audiobook) monitoring, requesting, and library awareness. Chaptarr access is granted per user by an admin.
- **Completed-media downloads** -- Opt-in delivery of ebook, audiobook, movie, and episode files from read-only library mounts. Deployment roots form the outer filesystem allowlist; per-instance path mappings translate each arr namespace into that boundary. The server accepts only live arr file IDs and streams through short-lived file-scoped links with HEAD and Range support.
- **Automatic ID bridging** -- Transparently translates TMDB IDs to TVDB IDs for Sonarr. Falls back to Trakt cross-references, then a premiere-year-verified title search (a same-titled series years apart is never substituted). Results cached in SQLite for 30 days.
- **Availability computed live** -- Request status is derived from the arrs' real episode/file state (never from a stale snapshot or monitored-only stats), refreshed by queue polling and instant arr webhooks.
- **Connect link auth, passwordless by default** -- Admins generate connect links; redeeming one starts a permanent device session (an opaque refresh token validated against the DB -- never expires, never rotates, independent of the JWT secret) that mints month-long access JWTs (stamped with an `access` scope so they can never pass the legacy-refresh amnesty gate). Expiry adds no security -- every request re-validates the user and device against the DB, so revocation is instant regardless -- it only decides how often a client must complete the refresh round-trip. Sessions end only by device revocation or user deletion. Passwords and passkeys (WebAuthn, incl. native iOS/Android/Windows) are admin-gated per user.
- **AI assistant + remediation agent** -- Interactive chat resolves a personal Anthropic/OpenAI/Gemini key or OpenAI (OAuth) link first; that personal choice works without an included-access grant and need not match the server provider. An admin-funded provider is available only to users granted included access. A selected personal provider fails closed instead of silently consuming the shared account. The autonomous investigation agent is server-owned, always uses the admin shared API key or shared OpenAI OAuth connection without consulting user grants, may use a separately tested remediation model designation, and proposes fixes an admin approves.
- **MCP server** -- 36 of the 38 in-app AI tools are exposed at `/mcp` (Streamable HTTP) with full inbound Cantinarr OAuth: discovery metadata, dynamic client registration, PKCE, browser/passkey login, rotating refresh tokens. Admin settings tools inspect quality profiles and import or update native/TRaSH custom formats on Radarr, Sonarr, and Chaptarr. Profile preview/apply stays in-app-only because its one-use, same-turn handoff depends on authenticated in-app chat provenance. After an explicit admin request, the assistant can preview and apply autonomously within that turn. AI/MCP profile and custom-format writes are recorded in durable configuration history with live comparison; one-time guarded restore is limited to applied quality-profile updates. This is separate from the outbound OpenAI OAuth account link used by Codex chat.
- **Import Doctor** -- Plain-English diagnosis of stuck downloads with one-click fixes (manual/force import, remove+blocklist+re-search, blocklist-without-re-search for a stuck upgrade, category hand-off, rescan), shared by the app, the AI assistant, and MCP. A second TV detector catches a season Sonarr filled before its episodes aired, at the moment those files import rather than whenever someone gets around to reporting them.
- **Push notifications** -- APNs delivery through a self-hosted push gateway with zero-config auto-enrollment, per-user preference categories, and admin-scoped alerts.
- **Real-time updates** -- WebSocket hub polls arr queues (30s) and download clients (15s) and pushes progress, queue snapshots, and change pings; arr webhooks make external changes (manual imports, deletes) land instantly.
- **Arr proxy** -- Read-only Radarr/Sonarr browsing for users, ordinary-method passthrough for admins (tunnel/reflection methods blocked for everyone), without exposing API keys.
- **Secrets encrypted at rest** -- Instance API keys/passwords, personal and shared AI credentials, OpenAI OAuth authorization, webhook tokens, and the JWT secret are AES-256-GCM encrypted in SQLite.
- **Flutter web embed** -- The web build ships inside the binary via `go:embed`. One container, one port, API + UI.
- **Single Alpine image** -- A static, no-CGO Go server plus the pinned Codex app-server helper, with one exposed port.

## Quick Start

### Docker Compose (recommended)

```yaml
services:
  cantinarr:
    image: ghcr.io/windoze95/cantinarr:latest
    ports:
      - "8585:8585"
    volumes:
      - ./config:/config
    environment:
      # Optional: enables push notifications (see Configuration)
      - CANTINARR_PUSH_GATEWAY_URL=https://push.julian.codes
    restart: unless-stopped
```

```bash
docker compose up -d
```

Open `http://your-server:8585` -- the setup wizard creates your admin account. Then configure API credentials and service instances from the admin UI. The admin may configure an included AI profile and grant it per user; every user may instead bring a personal provider under **Settings > AI Access**.

### From Source

```bash
# Requires Go 1.25+
cd server
go build -o cantinarr ./cmd/server
./cantinarr
```

## Configuration

Service credentials (TMDB, Trakt), the admin's included AI profile, and all service instances (Radarr, Sonarr, Chaptarr, SABnzbd, qBittorrent, NZBGet, Transmission, Tautulli) are managed through the admin UI. The included AI profile and each user's optional personal override can use Anthropic/OpenAI/Gemini API keys or OpenAI OAuth backed by a ChatGPT account. No environment variables are needed for credentials.

Optional env vars for deployment tuning (a `.env` file next to the binary is auto-loaded):

| Variable | Default | Description |
|---|---|---|
| `CANTINARR_PORT` | `8585` | HTTP listen port. Kubernetes service-link values (`tcp://…`) injected by a Service named `cantinarr` are ignored in favor of the default; set a numeric value to override |
| `CANTINARR_SERVER_NAME` | `Cantinarr` | Display name shown in clients |
| `CANTINARR_PUBLIC_URL` | direct request origin | Origin the Radarr/Sonarr/Chaptarr containers POST webhooks back to, so it must be resolvable and reachable from the arrs themselves -- in same-network/cluster deployments a cluster-internal origin like `http://cantinarr:8585` is usually the right value. Set it explicitly behind a reverse proxy because forwarded host/protocol headers are deliberately ignored |
| `CANTINARR_OAUTH_ISSUER` | request-derived origin | Canonical external HTTPS origin for inbound MCP OAuth metadata, token audience, and browser-origin checks; setting it also enables stable RFC 9207 authorization-response `iss` and permits that origin to call `/mcp`. Set it behind a reverse proxy and keep it stable (changing it makes existing audience-bound MCP tokens reconnect); it is intentionally separate from the arr-reachable `CANTINARR_PUBLIC_URL` |
| `CANTINARR_MCP_ALLOWED_ORIGINS` | unset | Comma-separated additional browser origins allowed to call `/mcp`. If neither this nor `CANTINARR_OAUTH_ISSUER` is configured, requests that supply `Origin` are rejected; native and server-side clients omit `Origin` and need no entry |
| `CANTINARR_JWT_SECRET` | auto-generated | HMAC secret for signing short-lived access tokens (persisted encrypted when auto-generated). Opaque device-session refresh tokens do not depend on it, so changing it never signs devices out |
| `CANTINARR_ENCRYPTION_KEY` | auto-generated key file | Base64 32-byte key for secrets-at-rest (default: `/config/encryption.key`) |
| `CANTINARR_AI_PROVIDER` | `anthropic` | Fallback provider for the included server AI profile when none is saved in the admin UI (`anthropic`, `openai`, `gemini`, `codex`) |
| `CANTINARR_AI_MODEL` | provider default | Fallback model for the included server AI profile when none is saved in the admin UI |
| `CANTINARR_CODEX_BIN` | auto-discovered | Optional path to `codex-app-server` or the full `codex` CLI; official container images bundle the tested 0.144.3 app-server at `/usr/local/bin/codex-app-server` |
| `CANTINARR_CODEX_RUNTIME_DIR` | `/dev/shm/cantinarr-codex` | Absolute Linux tmpfs/ramfs directory used for server-owned, ephemeral per-session Codex state; if it already exists, it must be owned by the server user with mode `0700` |
| `CANTINARR_MEDIA_ROOTS` | unset | Comma-separated absolute server/container paths forming the outer filesystem allowlist for completed-media downloads. Empty disables downloads. Mount libraries read-only beneath these roots, then map each arr-reported prefix to a path inside them from that instance's settings; `/` and aliases of `/` are refused |
| `CANTINARR_PUSH_GATEWAY_URL` | unset | Push gateway origin; setting it **enables** push notifications |
| `CANTINARR_PUSH_API_KEY` | unset | Optional pinned gateway key -- leave blank and the server auto-enrolls on first start, persisting its issued key encrypted in the DB |
| `CANTINARR_PUSH_ENROLL_TOKEN` | unset | Shared enroll token, only for gateways with gated enrollment |
| `CANTINARR_APPLE_APP_IDS` | unset | Comma-separated `TeamID.BundleID` values served in `/.well-known/apple-app-site-association` for native Apple passkeys |
| `CANTINARR_ANDROID_PACKAGE_NAME` | `codes.julian.cantinarr` | Android package name for native passkeys |
| `CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS` | unset | Comma-separated Android signing cert SHA-256 fingerprints for `/.well-known/assetlinks.json` and Android WebAuthn origins |
| `CANTINARR_WEBAUTHN_EXTRA_ORIGINS` | unset | Additional WebAuthn origins to trust (e.g. non-standard HTTPS ports) |
| `CANTINARR_DISABLE_UPDATE_CHECK` | unset | Set to `1` to disable the periodic GitHub release check behind the admin "update available" banner |

OpenAI OAuth source deployments use Codex app-server and are supported only on Linux; non-Linux hosts report this provider unavailable even when a Codex binary is installed. The runtime directory's parent must exist, and the directory must be on tmpfs or ramfs—not persistent storage. Give each concurrently running Cantinarr process its own runtime directory; startup removes stale `session-*` entries from that dedicated root. The official container uses its private Docker `/dev/shm` tmpfs. Use the tested Codex 0.144.3 release or a protocol-compatible build.

The database lives at `/config/cantinarr.db` (SQLite, WAL mode). Keep the `/config` volume -- it holds the DB and the auto-generated encryption key, and encrypted secrets are unrecoverable without that key.

Native app passkeys require a public HTTPS domain associated with the app: Apple devices verify the AASA file, Android credential providers verify `assetlinks.json`. Browser passkey setup remains available when native association isn't possible.

## API Reference

Auth levels: **public**, **user** (any signed-in account), **admin**. Public auth endpoints are rate-limited to 10 requests/minute/IP. Internally, authorization is permission-based (`users:manage`, `requests:manage`, `remediation:manage`, `instances:manage`, `arr:browse`, ...) -- "admin" below means the admin role holds the required permission and the user role doesn't.

### Auth & account
```
GET    /api/auth/status                    # public: is setup complete, login methods
POST   /api/auth/setup                     # public: first-run admin account creation
POST   /api/auth/login                     # public: { username, password } -> tokens
POST   /api/auth/refresh                   # public: mint access token (refresh token is stable; legacy JWT refresh tokens migrate to the opaque scheme; 401 only on genuine revocation, 503 on transient faults)
POST   /api/auth/connect                   # public: redeem a connect-link token -> session
POST   /api/auth/passkey/login/begin|finish   # public: WebAuthn login
POST   /api/auth/passkey/setup/begin|finish   # public: passkey setup via short-lived link
GET    /api/auth/me                        # user: profile + permissions
POST   /api/auth/password                  # user: set password (admin-enabled users only)
POST   /api/auth/plex-email                # user: share the email for a Plex invite (notifies admins on change)
POST   /api/auth/passkey/register/begin|finish  # user: add passkey (admin-enabled only)
POST   /api/auth/passkey/setup-link        # user: mint a browser passkey-setup URL
GET    /api/auth/passkeys                  # user: list own passkeys
DELETE /api/auth/passkeys/{credentialID}   # user: remove own passkey
```

### Users, devices & connect links (admin)
```
POST   /api/admin/connect-token            # mint a connect link for a user
GET    /api/admin/devices                  # all connected devices
DELETE /api/admin/devices/{deviceID}       # revoke a device (kills its sessions + MCP tokens)
GET    /api/admin/users
PATCH  /api/admin/users/{userID}                 # change role
PATCH  /api/admin/users/{userID}/auth-methods    # enable/disable password & passkey per user
PUT    /api/admin/users/{userID}/ai-access       # grant/revoke use of the admin-funded AI profile
DELETE /api/admin/users/{userID}
POST   /api/admin/users/{userID}/test-push       # delivery diagnostics for one user
GET|PUT /api/admin/users/{userID}/default-instances  # pin per-user default arr instances;
                                                     # for Chaptarr this doubles as the access grant
POST   /api/admin/users/{userID}/plex-invite     # send the Plex invite for the user's shared email
```

### Plex invites (admin)
```
GET    /api/admin/plex/status              # linked account + invite config (never the token)
POST   /api/admin/plex/link/begin          # start the PIN flow -> { pin_id, code, url }
POST   /api/admin/plex/link/check          # poll the PIN; stores the token once approved
DELETE /api/admin/plex/link                # unlink + forget invite settings
GET    /api/admin/plex/servers             # linked account's owned Plex Media Servers
GET    /api/admin/plex/servers/{machineID}/libraries  # sections for the library picker
PUT    /api/admin/plex/settings            # server, shared libraries, auto-invite toggle
```

### Setup status (admin)
```
GET    /api/admin/setup-status             # live-derived checklist of configured/unconfigured features
```
Re-derived from actual configuration on every request (never stored), so the app's setup wizard is resumable and can't go stale. New features surface themselves by adding an item here; clients render unknown keys generically. Completed-media downloads are an optional item and count as configured once deployment roots are present and at least one Radarr, Sonarr, or Chaptarr instance has an effective media path mapping. The optional `discovery_prefs` item follows `trakt` and points at [Discovery settings](#discovery-settings-admin); it grades whether an admin has saved a discovery choice rather than which one, and its description names a configured Trakt key that no headline row is using yet -- connecting Trakt adds its lists and calendar, but the headline row keeps its source until someone changes it.

### Update status (admin)
```
GET    /api/admin/update-status            # latest-release comparison + management-portal URL
PUT    /api/admin/update-status            # set the management-portal URL ({ management_url })
```
Backs the app's "update available" banner. The server compares its own build version against the latest published GitHub release (`windoze95/cantinarr`), cached ~12h, best-effort and non-blocking; only real semver-tagged builds check (dev/`latest`/PR builds never contact GitHub), and `CANTINARR_DISABLE_UPDATE_CHECK=1` turns it off. `management_url` is an optional admin-set link to a container-management portal the banner points at. Unlike instance URLs (which only the server dials), this link opens **on the admin's own devices**, so it must be reachable from them -- a cluster-internal name that only the server resolves won't work from a phone. The running version is also surfaced to all clients (for the About screen) in `/api/config` as `version`.

### Discovery settings (admin)
```
GET    /api/admin/discovery-settings       # source + english_only, the valid sources, trakt_configured
PUT    /api/admin/discovery-settings       # set the row source ({ source, english_only })
```
Decides what backs the headline row on the Movies and TV tabs. `source` is one of `tmdb_trending` (TMDB's weekly trending feed), `trakt_trending` (Trakt trending, ranked by who is watching right now), or `tmdb_popular` (TMDB's lifetime popularity ranking). TMDB's popularity value is a lifetime score, so `tmdb_popular` fills with long-running catalogue shows and nightly talk shows -- the trending feeds are short-window and self-correcting, which is why one of them is the default. `trakt_configured` reports whether the Trakt source can be selected at all; picking it without a Trakt client ID falls back to `tmdb_trending` rather than blanking the row -- and so does a Trakt call that fails, since a third-party outage is not the admin's configuration problem and the headline row is the landing screen. Either fallback is visible in the response rather than silent: `source` always names the feed that actually answered, so the client retitles the row. An outage fallback is cached against the *chosen* source, so a sustained one costs a single upstream attempt per TTL instead of one per request, and recovery is picked up on the next miss. The server logs each fallback.

Until an admin saves a choice, the default source **follows the Trakt credential**: `trakt_trending` whenever Trakt is configured, `tmdb_trending` otherwise. Adding the client ID is itself the statement that Trakt should be used, so the rows adopt it with no second step. That default is derived per read, never written -- removing the credential moves the rows back instead of stranding them on a source that can no longer answer, and adoption never masquerades as an admin's decision.

`english_only` drops titles whose original language is not English from the discovery and recommendation feeds. **It is on by default** and stays on until an admin saves a choice: the rows are a shelf rather than a catalogue, and an admin who wants everything flips one switch while an admin who never opens the screen would otherwise never learn the filter exists. It never applies to search or to detail lookups -- a title you went looking for stays findable -- and a title the metadata source did not classify is kept rather than hidden.

A `PUT` replaces both fields, so a caller that omits `english_only` is turning the filter **off**, not leaving it alone; send the full pair. That `PUT` is also what satisfies the `discovery_prefs` step of the [setup checklist](#setup-status-admin). Every source and either `english_only` value is a legitimate answer, so that step grades whether the admin has saved a choice at all -- not which choice. Until then the stored `discovery_source` is empty, which is the one marker for "never decided": it is what both defaults above key on, and it is how a deliberate pick stays distinguishable from the same value arrived at by default. Any future setter that writes one discovery field must write the other too.

### AI configuration history (admin)
```
GET    /api/admin/external-settings-changes          # list AI/MCP profile/custom-format writes
GET    /api/admin/external-settings-changes/{id}     # detail with live profile/custom-format comparison
POST   /api/admin/external-settings-changes/{id}/revert  # guarded quality-profile restore only
```
The app exposes these records at Settings > Configuration history. Timeline responses stay lightweight; selecting one record reads the live profile or custom format and projects a safe before/recorded/current comparison without sending server-owned raw snapshots to a device. The recorded value is labeled as applied, attempted, or intended according to the outcome. Each successfully applied quality-profile update can be restored once while the instance binding, live profile, and relevant dependencies still match the recorded applied state. Success appends a linked restore record instead of rewriting history; that record cannot be restored again, and the source update remains consumed even if its applied values later return. An executing or outcome-unknown restore also blocks replay because Cantinarr cannot safely infer the external result; only a definitively failed attempt may retry after the live guards pass again. Custom-format entries support live comparison but not restore. Generic admin-proxy writes and managed-webhook changes are outside this history.

### Requests
```
POST   /api/requests                       # user: create (movie/tv by tmdb_id;
                                           #   books by foreign_id + book_format; optional instance_id)
GET    /api/requests                       # user: own request history
GET    /api/requests/options               # user: what this user may choose (seasons, quality)
GET    /api/requests/book-status           # user: per-format live state by foreign_id; optional instance_id
GET    /api/requests/book-library          # user: owned/monitored digest; optional instance_id (brief cache)
GET    /api/requests/book-recent           # user: newest book-file imports; optional instance_id, limit (cached)
GET    /api/requests/{tmdb_id}/status      # user: live availability + download progress
GET    /api/admin/requests                 # admin: pending approval queue; rows carry the book's
                                           #   foreign_id and a best-effort TMDB poster_path (movie/tv)
POST   /api/admin/requests/{id}/approve    # admin: approve (executes the stored request once)
POST   /api/admin/requests/{id}/deny       # admin: deny with optional reason
GET|PUT /api/admin/request-settings        # admin: global policy (require_approval,
                                           #   allow_season_choice, default scope/quality...)
GET|PUT /api/admin/users/{userID}/request-settings  # admin: per-user overrides
```
Request statuses: `unavailable`, `requested`, `pending` (awaiting approval), `denied`, `downloading`, `partial`, `available`.

### Issues & AI remediation
```
POST   /api/issues                         # user: report a problem; requires instance_id plus media scope — tmdb/tvdb ids
GET    /api/issues                         # reporter inbox: the caller's OWN reports, newest first, requester copy applied
GET    /api/admin/agent-digest             # the agent scoreboard: rolling window of resolved/zero-touch/rule-approved counts, tokens, and what needs a human; incidents that cleared before promotion are excluded, not reported
GET    /api/admin/agent-approval-rules/candidates  # triples the admin has hand-approved and could automate (grounded; active-ruled triples excluded)
POST   /api/admin/agent-approval-rules     # arm a rule from the catalog — server re-checks the triple was actually hand-approved
                                           #   for movie/tv, foreign_id (+book_format when both formats exist) for book
                                           #   (instance must be Radarr for movie, Sonarr for tv, Chaptarr for book;
                                           #   gated by allow_reporting); returns {issue_id,status}; initial status is
                                           #   observing or recovering
GET    /api/issues/{id}                    # reporter or admin: issue thread (an admin viewing marks it read)
POST   /api/issues/{id}/reply              # reporter or admin: reply (answers agent questions)
POST   /api/issues/{id}/confirm-fixed      # reporter ONLY: close their own report as fixed
GET    /api/admin/issues?status=           # admin: issue queue (user-reported + auto-detected; each row carries read/unread)
POST   /api/admin/issues/{id}/dismiss      # admin
POST   /api/admin/issues/{id}/resolve      # admin: { disposition: resolved|wont_fix, note: required, <=8192 bytes }
                                           #   transactional reviewed completion; races return 409
GET    /api/admin/issues/{id}/activity     # admin: durable action + run history for one issue
GET|PUT /api/admin/remediation-settings    # admin: master switch, auto-dispatch, reporting,
                                           #   mark-resolved-as-read, mode, optional model_override
                                           #   (provider/credential always follow the shared selection),
                                           #   step/turn/time and daily-run budgets,
                                           #   observation_min_minutes (10), observation_quiet_minutes (5),
                                           #   observation_settle_minutes (2)
GET    /api/admin/agent-actions?status=    # admin: awaiting queue, or status=all for history;
                                           #   actions include immutable instance_id + instance name/service;
                                           #   release GUIDs are one-way fingerprints, never raw indexer capabilities
GET    /api/admin/agent-actions/{id}       # admin: reconcile one durable action after a lost response
POST   /api/admin/agent-actions/{id}/approve   # admin: claims and dispatches the stored proposal once;
                                           #   body {override?, remember?} — remember additionally arms a
                                           #   standing auto-approval rule for this (problem, fix, facet)
POST   /api/admin/agent-actions/approve-batch  # admin: approve an explicit list of reviewed proposals;
                                           #   body {ids} (≤100), decided sequentially by the single-
                                           #   approve core; 200 with per-item verdicts (durable action
                                           #   status, or skipped/error with a reason) — one conflict
                                           #   never fails the rest
POST   /api/admin/agent-actions/{id}/deny      # admin: denial resumes the investigation
GET    /api/admin/agent-runs/{id}          # admin: full audit trail of one agent run
GET    /api/admin/agent-approval-rules     # admin: standing auto-approval rules with status + counters
POST   /api/admin/agent-approval-rules/{id}/pause    # admin: stop a rule matching (idempotent)
POST   /api/admin/agent-approval-rules/{id}/resume   # admin: re-arm a paused rule (idempotent)
DELETE /api/admin/agent-approval-rules/{id}          # admin: remove a rule; history keeps its attribution
```

### Discover & media (user)
```
GET /api/discover/trending | /discover/movies/popular | /discover/tv/popular
GET /api/discover/movies/featured | /discover/tv/featured  # the configured headline row
GET /api/discover/movies/top-rated | upcoming | now-playing
GET /api/discover/movies | /api/discover/tv          # filterable discover
GET /api/search                                      # multi-search
GET /api/media/movie/{id} | /api/media/tv/{id}       # detail (+ /recommendations, /similar)
GET /api/media/person/{id} | /api/media/person/{id}/credits
GET /api/genres/movie | /api/genres/tv | /api/providers/movie
GET /api/trakt/trending | popular | lists | lists/{user}/{slug}/items
GET /api/trakt/calendar | anticipated | recommendations
GET /api/trakt/images/{host}/*                       # Trakt artwork relay for the web client
```
TMDB and Trakt are proxied server-side -- client devices never hold those keys. `/featured` serves whichever feed the admin selected (see [Discovery settings](#discovery-settings-admin)), normalized to the TMDB page shape plus a `source` field so one client parser handles every source and the row can name what it is showing. The discovery and recommendation feeds also honor the admin's `english_only` preference; search and detail lookups never do.

Trakt's artwork CDN (`media.trakt.tv` today; `walter*.trakt.tv` before July 2026 -- Trakt migrates these hosts) is public but sends no CORS headers, so a browser-rendered client cannot fetch it directly the way it can TMDB's CDN. `/api/trakt/images/{host}/*` relays any `*.trakt.tv` host (strictly validated, `images/…` paths only, no query passthrough) so the web app loads Trakt posters same-origin and a CDN migration on Trakt's side cannot blank them again; native clients keep hitting the CDN directly.

### AI chat (user)
```
POST   /api/ai/chat                         # SSE-streamed conversation with tool use
GET    /api/ai/available                    # effective availability + personal|shared|none source
GET    /api/ai/settings                     # provider catalog, write-only credential flags, effective source
PUT    /api/ai/settings                     # test + atomically save personal provider/model/key override
DELETE /api/ai/settings                     # clear personal override and return to granted included access
PUT    /api/ai/credentials/{provider}       # test + set/replace one personal API key (write-only)
DELETE /api/ai/credentials/{provider}       # erase one personal API key
GET    /api/ai/codex/status                 # personal linked state, safe account metadata, usage windows
POST   /api/ai/codex/device/begin           # begin ChatGPT device authorization
GET    /api/ai/codex/device/{flowID}        # poll this user's pending device flow
DELETE /api/ai/codex/device/{flowID}        # cancel this user's pending device flow
DELETE /api/ai/codex                        # unlink this user's ChatGPT account
```
All personal settings, credentials, Codex accounts, and device flows derive ownership from the authenticated caller; no user ID is accepted from the client. `device/begin` returns a verification URL, one-time code, flow ID, expiry, and polling interval; the app opens the explicit ChatGPT browser sign-in while keeping access and refresh tokens off the device. API keys and completed OAuth authorization are AES-256-GCM encrypted at rest, and responses expose configured booleans rather than secret values. A provider, model, key, or completed OAuth selection is accepted only after the exact candidate completes a small tool-free response turn; a failure leaves the previous key and selection unchanged.

Resolution is deterministic: a personal selection row is the explicit override; otherwise a user with an included-access grant receives the admin's shared provider. If the personal key, OAuth link, runtime, or allowance is unavailable, that request fails as personal instead of silently switching accounts and spending shared quota. The source is resolved once per request and never changes during a provider turn. Chat admission is non-blocking and cost-aware: one active turn per user, 16 turns server-wide, and at most four included-provider turns; excess requests are rejected before any provider call.

### Included AI profile (admin)
```
GET|PUT /api/admin/credentials                  # shared profile + daily-check state; AI writes test before commit
DELETE  /api/admin/credentials/{key}            # erase one shared API key
GET     /api/admin/ai/codex/status              # shared OpenAI OAuth status + admin-only usage metadata
POST    /api/admin/ai/codex/device/begin        # begin shared OpenAI OAuth device authorization
GET     /api/admin/ai/codex/device/{flowID}     # initiating admin polls the pending shared flow
DELETE  /api/admin/ai/codex/device/{flowID}     # initiating admin cancels the pending shared flow
DELETE  /api/admin/ai/codex                     # unlink the shared OpenAI OAuth account
```
The included profile supports the same Anthropic, OpenAI, Gemini, and OpenAI (OAuth) choices as a personal profile. OpenAI OAuth exposes the recommended Codex model plus GPT-5.6 Sol, Terra, and Luna. Grants are independent of roles and are changed per user. The initial admin starts granted, newly invited users start without a grant, and the one-time migration keeps existing users enabled to preserve the former global-provider behavior. Shared account identity, plan, rate-limit windows, and authorization stay admin-only; granted users learn only that their effective source is included. Codex execution separates the singleton credential identity from the requesting Cantinarr identity: refresh state serializes against the shared account, while tool permissions always use the actual caller's current user ID and role. Interactive tool dispatch rechecks the current user, device, role, and (for included AI) shared grant at execution time; a revoked or invalid actor terminates the turn instead of being converted into a model-visible tool error.

Every shared provider/model/API-key save, remediation-model override, and completed OAuth selection performs one real, tool-free, low-reasoning message-response turn before success is reported. The probe uses a bounded response budget, retries only transient failures that occur before any output, and returns a redacted actionable category for invalid credentials/connections, unsupported model access, quota/rate limits, temporary upstream failures, or invalid responses. The settings endpoint commits a candidate profile atomically only after all supplied AI credentials pass. A remediation override is tested with the live shared provider and credential and stored with that provider binding; if the global provider later changes, runs safely fall back to its global model until an override is tested for the new provider. A separate shared-model monitor defaults on and runs at most once every 24 hours; its durable last-check timestamp prevents restart-driven usage. Failure creates or refreshes one `source=system` admin-only issue, and the next successful scheduled or save-time turn resolves it with `resolution_kind=ai_health_restored`. Admins can disable only this background monitor; save-time validation remains mandatory. Neither the monitor nor its issue enters the remediation job queue, and the remediation runner continues to resolve only the admin-global provider and credential without user settings or grants.

The chat request accepts an optional `conversation_id`; the server replays its provider-neutral stored transcript (including tool results and provider-signed continuation state) so follow-up turns keep full grounding across Anthropic, OpenAI, Gemini, and Codex. A transcript is bound to the Cantinarr user, personal/included source, provider account or one-way credential fingerprint, selected model, and the current in-memory OAuth connection generation; changing any of those starts a fresh conversation. Signed provider state is kept atomic and byte-for-byte or the oversized turn is discarded—it is never truncated into an invalid signature pair. Transcripts are byte-bounded and kept only in process memory: they become inaccessible after four hours of inactivity, are evicted by later chat activity, and disappear on restart or a failed provider turn. Prompts, conversation context, and scrubbed tool results are sent to the selected provider (OpenAI for Codex). SSE frames: `{conversation_id}`, `{text}`, `{tool_start: {name, label}}`, `{tool_end: {name, ok}}`, `{media_results}`, `{error}`, then `[DONE]`.

### AI tool toggles (admin)
```
GET    /api/admin/ai-tools          # list tools: { name, description, enabled, admin_only }
PUT    /api/admin/ai-tools/{name}   # { enabled } -- applies to chat and /mcp immediately
PUT    /api/admin/ai-tools/debug    # toggle tool debug mode
```
Tool debug mode records names, timing, status, and payload sizes only; tool inputs, outputs, and error bodies are never written to logs. Every MCP tool result also crosses a shared credential scrubber before it can reach chat, `/mcp`, or the remediation agent, including nested JSON, authorization/cookie headers, URL userinfo, and secret-bearing query parameters.

### Instances & arr proxy
```
GET|POST /api/instances                      # admin: list/create
GET    /api/instances/media-roots             # admin: deployment-approved Cantinarr roots for the mapping editor
POST   /api/instances/test                   # admin: dry-run connectivity check, dialed from the server
PUT|DELETE /api/instances/{instanceID}       # admin: update/delete
GET|PUT /api/instances/{instanceID}/users    # admin: which users are pinned/assigned here
POST   /api/instances/{instanceID}/webhook   # admin: rotate credentials and upsert a managed arr webhook
ANY    /api/instances/{instanceID}/*         # proxy to the instance's own API; JSON secrets are redacted,
                                             # and upstream redirects are remapped onto this route (off-origin ones become 502)
```
The proxy allows read-only Radarr/Sonarr browsing (library, queue, history, wanted, calendar) for regular users; writes, commands, interactive search, config, and all non-arr services require admin. Requesters are bound to their own effective instance -- their pin, or the deterministic global default/fallback -- exactly as `/api/config` reports it; a sibling instance the admin has hidden cannot be reached by guessing its ID, and instance authorization is classified from stored metadata so an undecryptable secret can never widen access. JSON responses are bounded and recursively scrubbed for credential fields and secret-bearing URL query parameters before they reach any client. An encoded, malformed, streaming, or oversized JSON response fails closed rather than bypassing that scrubber.

Radarr, Sonarr, and Chaptarr create/update payloads may include `media_path_mappings`, an ordered array of `{ "arr_path": "...", "cantinarr_path": "..." }` objects. Each row translates an absolute path prefix reported by that one instance into a server-visible directory beneath `CANTINARR_MEDIA_ROOTS`; source prefixes may use POSIX, Windows drive, or UNC syntax independently of the server OS. An empty array disables completed-media downloads for that instance. The admin-only `media-roots` endpoint supplies the approved target roots used by the editor. Mapping paths are admin-only instance configuration and never appear in the requester `/api/config` payload.

The proxy is also a transport trust boundary. `CONNECT`, `TRACE`, and `TRACK` are rejected before instance resolution or any upstream contact, and an upstream protocol upgrade (`101`) is refused, so the HTTP proxy can never become an opaque tunnel or reflect the injected `X-Api-Key`. Inbound Cantinarr session cookies and every forwarded credential, client-identity assertion (reverse-proxy `X-Auth-*`/`Remote-*`/mTLS headers), routing/method-override, and request-trailer header terminate here; only the instance's own `X-Api-Key` is added outbound. Upstream responses are marked private and non-cacheable, and nginx/lighttpd internal-redirect controls (`X-Accel-*`, `X-Sendfile`, `X-Reproxy-URL`) plus response trailers are stripped so a fronting web server cannot be steered by an upstream header.

### Completed media downloads (user)

```
POST     /api/media-files/tickets                    # authenticated: { instance_id, file_id }
POST     /api/media-files/coverage                   # authenticated: { instance_id, paths[] } -> { covered[] }
GET|HEAD /api/media-files/download/{ticket}          # public bearer capability until expires_at
```

Ticket issuance requires `media:download`, re-checks the caller's current account role, and limits requesters to their effective Radarr/Sonarr instance or explicitly granted Chaptarr instance. Administrators may select any configured arr instance. The client supplies only an instance ID and live arr file ID; it never supplies a path. Cantinarr re-fetches that exact file record from Radarr (`moviefile`), Sonarr (`episodefile`), or Chaptarr (`bookfile`) both when issuing the ticket and for every transfer.

Bytes are available only when two boundaries agree: the live arr path must match a mapping owned by that exact instance, and the mapped Cantinarr path must remain beneath a configured `CANTINARR_MEDIA_ROOTS` root. In Docker the target is the container path of a read-only mount; for a native server it is an absolute local directory readable by the server process. This lets instances that both report `/ebooks` safely map to different mounts, and lets a Chaptarr instance map as many independent ebook/audiobook trees as it uses. Mapping does not infer media format from a directory name; the exact Chaptarr record remains authoritative. Files outside either boundary, directory entries, and symlink escapes are refused.

Downloads have no implicit configuration: every instance starts disabled and serves files only through mappings an admin explicitly saved. (Databases that briefly carried the retired pre-launch identity-bridge value read it as disabled.) `/api/config.services.media_downloads` remains a compatibility aggregate and is true when at least one instance visible to the current user has an effective download mapping; each returned instance also reports its own path-free download capability so current clients show controls only for the selected instance. Neither response exposes roots or mapping paths.

The `coverage` endpoint takes up to 200 arr-reported paths under the same authorization as ticket issuance and answers, per path, whether this instance's current mappings would translate it into an allowed root. The verdict is purely lexical -- it never touches the filesystem -- so it reveals mapping shape (which the ticket flow's per-file conflict already does) but nothing about which files exist. Clients use it to withhold download affordances for files no mapping covers -- for example an audiobook tree that is deliberately not mounted -- instead of offering buttons that can only fail; an instance-level capability alone cannot express that per-file truth.

Tickets are opaque, bounded, reusable for ten minutes so browser HEAD probes and resumed Range requests work, and contain neither JWTs nor server paths. Every GET/HEAD re-checks that the user still exists, uses the user's current role, re-checks effective-instance access for non-admins, re-fetches the live file record, and applies the current mapping. Responses are attachment-only `application/octet-stream` with no-store, no-referrer, nosniff, same-origin resource policy, and sandbox CSP headers; errors never expose arr hosts, filesystem paths, or OS details. Delivery covers the primary files indexed by Radarr, Sonarr, and Chaptarr, not arbitrary neighboring files, subtitles, extras, or directories. Multi-file audiobooks remain individual file choices rather than being packaged into an archive.

### Downloads & monitoring (admin)
```
GET    /api/downloads/{instanceID}/queue     # unified SABnzbd/qBittorrent/NZBGet/Transmission
POST   /api/downloads/{instanceID}/pause|resume            # whole client
POST   /api/downloads/{instanceID}/queue/{itemID}/pause|resume
DELETE /api/downloads/{instanceID}/queue/{itemID}?deleteData=bool
GET    /api/downloads/{instanceID}/history?limit=50
GET    /api/tautulli/{instanceID}/activity   # current Plex streams + bandwidth
GET    /api/tautulli/{instanceID}/history?limit=50
GET    /api/tautulli/{instanceID}/stats?days=30
```

### Push & notification preferences (user)
```
POST   /api/devices/push-token               # register this device's APNs token
DELETE /api/devices/push-token/{deviceID}
GET|PUT /api/notifications/preferences       # per-category opt in/out
POST   /api/notifications/test               # test push to own devices
```

### Webhooks (credential-authenticated, no session)
```
POST   /api/webhooks/arr/{instanceID}             # Sonarr/Radarr/Chaptarr -> Connect -> Webhook (Basic Auth)
```
The instance editor's **Configure instant updates** action asks the server to rotate a per-instance credential and create or update a `Cantinarr` Connect webhook in Radarr/Sonarr/Chaptarr. The secret moves only between servers: instance API responses and the app never receive it. Managed records use webhook Basic Auth; query-string credentials are rejected and access logs omit all query strings. Set `CANTINARR_PUBLIC_URL` when Cantinarr is behind a reverse proxy; callback generation uses that trusted origin and never trusts client-supplied forwarded headers. The callback must be resolvable and reachable from inside the arr containers. Cantinarr explicitly acknowledges the lineage's warning for a private or cluster-local callback because Cantinarr constructs it from the configured or validated request origin rather than accepting an arbitrary callback from the app, while real validation failures remain fatal; creates still run the arr's callback test, and updates run that test explicitly before saving. A validation failure surfaces the arr's own verdict in the admin-facing error (extracted from the lineage validation shape only — never the raw body, and with credentials redacted) plus the exact callback URL that was registered, so "Unable to send test message" reads directly as a reachability problem with the URL to check. In Docker/k8s topologies a cluster-internal origin (`http://cantinarr:8585`) is usually correct; a public FQDN works only if the arrs can egress (or hairpin) to it. The configurator can still recognize an old copy/paste record by its callback path and migrate it. Rotation keeps the current and pending credentials valid until the arr accepts the update, and configuration is serialized per instance, so failed or concurrent attempts cannot break a working hook. Handled events -- `Grab`, `Download`, `MovieAdded`/`SeriesAdd`, `MovieDelete`/`SeriesDelete`, `MovieFileDelete`, `EpisodeFileDelete` -- invalidate availability, broadcast WebSocket updates, and (for imports) send new-content pushes; `Test` and everything else is acknowledged with 200 so the arr's Test button just works. A Sonarr `Download` does one thing more: the payload's own `episodes[]` air dates are read to spot an import that landed on an episode which has not aired yet, and that lone comparison is what hands a pre-air season fill to the detector below without anyone reporting it.

### MCP & OAuth (external tool access)
```
POST|GET|DELETE /mcp                         # MCP Streamable HTTP (JSON-RPC + SSE)
GET  /.well-known/oauth-protected-resource[/mcp]
GET  /.well-known/oauth-authorization-server | /.well-known/openid-configuration
POST /oauth/register                         # dynamic client registration
GET|POST /oauth/authorize                    # browser login (password or passkey) + consent
POST /oauth/token                            # code/refresh grants, PKCE, rotating refresh
GET  /passkeys/setup | /passkeys/create      # passkey pages for MCP/browser setup links
```
These endpoints are Cantinarr's **inbound** OAuth authorization server: an external MCP client signs in to access Cantinarr. They do not link a ChatGPT account. Personal and admin-shared Codex chat use separate **outbound** device flows under `/api/ai/codex/*` and `/api/admin/ai/codex/*`.

When `CANTINARR_OAUTH_ISSUER` is set, authorization-server metadata advertises RFC 9207 issuer identification and successful password/passkey authorization responses include the exact canonical `iss`; without it, Cantinarr preserves request-derived legacy metadata and does not claim stable authorization-response issuer support. The configured issuer must use HTTPS so metadata, redirects, token audience, and auth challenges share one secure canonical external origin. Dynamic client registration accepts and echoes OpenID Connect `application_type` values `native` and `web` (omitted legacy values default to `web`); explicit web registrations require HTTPS redirects. Trusted browser preflights accept the current session header plus MCP protocol-version and method/name routing headers. Supplied origins must match the configured issuer or an entry in `CANTINARR_MCP_ALLOWED_ORIGINS`; when neither is configured all browser origins are rejected before authentication, while native/server clients normally send no `Origin` and are unaffected.

### Real-time
```
WS     /api/ws                  # WebSocket (JWT via subprotocol header)
```

WebSocket events:
- `download_progress` -- `{ tmdb_id, media_type, progress, status }`
- `request_status_changed` -- `{ tmdb_id, media_type, status }` (queue polling **and** arr webhooks; status here is `available`, `partially_available`, `requested`, or `unavailable` -- note the longer spelling vs the REST `partial`)
- `downloads_queue` -- full download-client queue snapshot `{ instance_id, paused, speed_bps, items }`, sent on change
- `arr_queue_changed` -- `{ instance_id, service_type }` invalidation ping; clients refetch via REST
- targeted events fanned out per user/admin: `request_pending`, `request_decision`, `issue_created`, `issue_updated`, `agent_action_pending`, `agent_action_decided`, `agent_autoapproval_paused`, `remediation_autodispatch_disabled`, `plex_access_request`, `plex_invite_sent`, `issue_question`, `issue_fix_confirm`, `issue_closed`

## Architecture

### ID Bridge (TMDB-to-TVDB)

TMDB has the best metadata and images, but Sonarr only speaks TVDB. The bridge translates transparently:

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
4. Sonarr title search as fallback (premiere year must match TMDB's, ±1)
  |
  v
GET sonarr/api/v3/series/lookup?term=tvdb:81189  (exact match)
POST sonarr/api/v3/series  (add with the user's effective defaults)
```

Movies skip bridging entirely -- Radarr natively supports `term=tmdb:{id}`. Books have no TMDB id at all; they're keyed by the Chaptarr/Readarr `foreignBookId` plus a strict `book_format` (`ebook`, `audiobook`, or `both`). Book request bodies may include `instance_id`; status and library endpoints accept the same field as a query parameter. The server authorizes that selection and persists it with new pending/history rows, so approval always targets the instance the requester was viewing. Omitted IDs on new requests resolve through the requester's effective grant (or the admin fallback). Legacy rows are deliberately left unscoped because today's default cannot prove their original library; a legacy pending book row must be resubmitted instead of being approved against a guessed instance.

Adding a brand-new book needs its full metadata record (editions are round-tripped verbatim into the add payload), so the record has to be found again through `book/lookup`. The foreignBookId itself is tried first: Chaptarr answers an id term with an exact fetch of that record (verified live -- an unknown id returns empty), the same deterministic resolution Radarr gives movies via `term=tmdb:{id}`. The fallbacks exist for the one caveat and for older forks: the provider can keep two works for one title and resolves an id fetch of the alias to the canonical sibling, which the exact-`foreignBookId` gate refuses rather than substituting a record the requester didn't choose. That refusal governs which record may be *added*; before adding anything, the same id fetch guards against duplication the other way -- when the provider declares the requested id an alias of a record the library *already tracks*, the request completes that record (monitor/search or add its missing sibling format under the library's id) instead of creating a twin, and the response carries `canonical_foreign_id` so the client re-addresses the book. Book request bodies therefore carry an optional `search_term` -- the text the requester actually searched, the one query already proven to return the exact row (Chaptarr's own web UI never faces this; it posts the search row straight back, while Cantinarr's clients keep only the id and title). It is stored on pending rows, so approval days later replays the same starting point. Last come the requester's exact title and the title's headline with subtitle and trailing parentheticals dropped -- a long full title routinely returns zero fuzzy results while its headline finds the book. Only an exact `foreignBookId` match is ever accepted, so extra terms can find the right record but never substitute a different one. When no term knows the id there is nothing to add or monitor, so the request is parked as `pending` with an explanatory `message` on the create response instead of being dropped: an admin can add the author in Chaptarr and approve it, or deny it. Chaptarr 0.9.879+ refusing the add because its metadata service does not know the book's author yet (the fork queues the author for an asynchronous import and rejects the add until it lands) parks differently, because it is a *time* problem rather than a decision: the row is marked server-owned (`request_log.park_reason = 'author_import'`), the requester is told it completes automatically and reads it back as `requested`, and no approval surface counts or pages it. A maintenance sweep retries those rows every few minutes through the shared fulfillment core and completes them silently once the import lands (no approver recorded, no decision push to the owner; non-owner waiters are still notified). Approving one early is refused with the plan and leaves the row untouched. A park stalled past 24 hours raises one auto-resolving `source=system` issue per instance (dedupe key `system:book-import-stall:<instance>`); at 7 days the sweep gives up and demotes the row to an ordinary approval-queue item, firing the `request_pending` page that was withheld at park time -- the moment a human decision first exists. A retry that fails for any reason other than the still-pending import demotes the same way. Parking stays specific to those self-healing cases -- a lookup that could not be performed at all, a missing root folder, or ambiguous profiles still fail the request outright, because those are answers the requester needs to see.

### Requests, approvals & live availability

A request is recorded in `request_log`, then either executed immediately or parked as `pending` when approval is required (globally or for that user). Approval replays the stored request -- season scope, quality choice, immutable concrete book format, and pinned Chaptarr instance -- exactly once; book format cannot be changed during approval. Denial notifies the requester with the reason. Book `both` operations return `book_formats` with a result for each concrete format. On partial approval, successful formats are recorded as requested/available while failed coverage remains pending for retry; a subscriber whose entire requested slice failed is not sent a false approval. Pending book coverage is overlap-aware (`both` conflicts with either concrete format) and shared by canonical title + instance across users; every subscriber sees their own concrete pending coverage in personal history, while the admin row exposes only the safe instance name and requester count. Approval materializes each subscriber's successful formats in personal history and sends a format-scoped decision; denial history is likewise personal, and unrelated users remain requestable.

For a brand-new Chaptarr title, Cantinarr resolves each concrete format independently: it selects the unique quality profile typed `ebook` or `audiobook`, the corresponding metadata profile typed `2` or `1`, and one accessible root path matching that format. Legacy untyped entries retain deterministic format-name, sole-profile, or unique-`Default` fallbacks, and a sole generic root remains valid; ambiguous choices fail with an admin-fixable error. Adding a missing sibling format beside an existing canonical book instead reuses that author's live quality profile, metadata profile, and root path for the requested format. Bounded in-process striped locks serialize conflicting canonical-book mutations and instance projection refreshes without a single global network-call lock; the supported deployment remains the repository's single-process SQLite server, not multiple independent writers.

Availability is **always derived live from the arrs**: TV availability comes from the real episode list (aired episodes with files), never from Sonarr's monitored-only percentage -- so a show with one monitored season never reads "available" while most of it is missing. Series with some-but-not-all aired episodes read `partial`, with per-season detail and a one-tap "request more" path that adds seasons without unmonitoring what's already there. Stale request rows are reconciled against reality (a "requested" title the arr has since imported reads `available`; a deleted one falls back to `unavailable`).

Chaptarr can file a created book record under its own canonical `foreignBookId` rather than the metadata lookup id the request used. Fulfilled book rows therefore persist the numeric Chaptarr record id (`request_log.book_record_id`), and book status reads resolve live truth through that id when the requested foreign id no longer matches the library -- returning `canonical_foreign_id` (on both the create response and `book-status`) so clients re-address the book by the id the library reports today. A stored record id whose record is gone still heals to `unavailable`, so deleting the record in Chaptarr keeps making the format requestable again.

`book-recent` backs the app's Recently Added row. Recency is the newest `bookFile.dateAdded` on a record -- when the file landed, never when the book was added to the library -- so a title requested months ago and downloaded today sorts first. eBook and Audiobook are separate Chaptarr records that arrive at different times, so they are returned as separate entries. Chaptarr exposes no library-wide file read (its bookfile API demands an author, book, or id filter), so the digest is assembled by a bounded per-author fan-out on the server and cached per instance; a partial fan-out fails rather than returning a list that could silently omit the very import the user is looking for. The Chaptarr webhook drops the cached digest on import, so a book that lands surfaces immediately. Covers are emitted only as the relative `/MediaCover` path the requester proxy allowlists, or the metadata provider's CDN copy -- never an arr-origin URL a client cannot reach. A user with no Chaptarr grant gets an empty list rather than an error.

Freshness has three layers: WebSocket queue polling (30s), instant arr webhooks for out-of-band changes, and short-TTL caches that mutations/events invalidate. Book status is a per-instance live projection: a file is `available`, a healthy active item in the fully paginated Chaptarr queue is `downloading`, and monitored-without-a-file is `requested`, even when no Cantinarr request-log row exists. Warning, blocked, failed, and error queue rows remain `requested` rather than claiming active progress. The projection and reduced owned-books digest are cached briefly across search-result calls and invalidated together after mutations. Live state outranks stale decided history. When legacy Chaptarr data cannot be mapped safely to eBook versus audiobook, targeted status returns `status_known: false` and the library digest marks that title the same way; clients must present an unknown/unresolved state rather than treating it as requestable.

### Instances & per-user defaults

The instance registry supports eight service types: `radarr`, `sonarr`, `chaptarr`, `sabnzbd`, `qbittorrent`, `nzbget`, `transmission`, `tautulli`. Stored URLs must be absolute `http`/`https` with no credentials, query, or fragment, and every create/update (plus the dry-run `POST /api/instances/test`) proves reachability with a live connection check **from the server** -- the only host that ever dials these URLs. Clients never receive them (`/api/config` omits the URL field), so cluster-internal names like `http://radarr:7878` are fully supported and the arrs need no exposure beyond the server's network. `https` instances need a certificate the server container trusts: add an internal CA to the image trust store, or use plain `http` on a trusted network -- a self-signed cert otherwise fails the connection check with an x509 error. At most one instance per service type is the global default (enforced in the store -- setting a new default clears the old one). Admins can additionally pin a per-user default per service type, which wins over the global flag; for Chaptarr -- which has no global default -- the per-user pin **is** the access grant. `/api/config` returns a per-user filtered view: regular users only see their effective default instances, and `services.chaptarr` is `false` without a grant.

### AI remediation agent

The issue system turns "my episode won't download" into a supervised agent workflow:

1. **Observe, then report or detect** -- users tap "Report a problem" on media (admin-toggleable); every report names the exact active/detail Radarr, Sonarr, or Chaptarr instance, and otherwise-identical reports against different instances remain distinct. A book report names the library's foreignBookId (plus the format when a title exists as both ebook and audiobook — two distinct records, never silently merged) and the server resolves the durable Chaptarr record ids live at intake. Auto-detection watches all three services' queues: the Chaptarr poller ends with the same complete-snapshot auto-dispatch pass as Radarr/Sonarr. Every user report and auto detection starts silently as `observing`/`recovering`: read, excluded from the badge, no push, no agent run, and no proposal. Successful complete queue snapshots are cached briefly and drive durable observation; incomplete/capped or failed reads are never interpreted as an empty queue. Replacement download IDs stay in one incident keyed by exact instance + movie/episode scope (including exact S00 specials), and every observed ID is retained for recovery attribution. A problem is promoted once only after both the configured minimum age (10 minutes) and unchanged quiet window (5 minutes); absence must also pass the settle window (2 minutes). Continuous connection/proof uncertainty lasting the minimum window becomes `needs_admin` without starting the agent, so reports neither alert prematurely nor disappear forever. Queue disappearance or file presence alone never proves resolution. `arr_state_cleared` requires the exact live file plus an exact-media import-history record that binds its file ID to one observed download ID. If Cantinarr's first baseline already contains that file, the queue response must have supplied the exact media's file ID (or known absence), any supplied positive ID must match the live/imported file, and the receipt must be no older than the queue attempt's arr-provided `added` time. This handles imports that beat the baseline and already-imported queue rows without trusting cross-service clocks. Cantinarr persists only the compact validated receipt (history/download/file IDs and timestamp), never raw history data. One incident recovers the opposite way: a stuck **upgrade** that Cantinarr deliberately abandoned (`blocklist_only`) is resolved by the library file staying exactly as the baseline recorded it, because nothing was ever missing and the fix's whole purpose was to stop chasing a replacement. That branch is gated on the server's own dispatched abandon fix and runs only after the exact queue target is proven gone — without both, "queue row gone, file unchanged" still means a download that quietly died, which reaches an administrator.
2. **Investigate** -- a server-owned AI agent follows the currently selected admin shared provider and credential and runs a budgeted tool loop against read-only arr state bound to that issue's instance and media scope. That read set is an exact hardcoded allow list -- `diagnose_queue`, `get_manual_import_candidates`, `search_releases`, `get_queue`, `get_history`, `get_library`, `get_arr_health`, `get_episode_timeline`, `get_book_timeline`, `get_media_file_details`, `get_service_config`, `get_quality_profiles`, `get_custom_formats` -- and it, not an RBAC role, is the enforcement boundary: only those definitions are offered to the model, and dispatch refuses any other name before the tool server is ever called. The two settings views are read-only and were added because several diagnoses -- "Not an upgrade", "Not a Custom Format upgrade" -- are verdicts the service reached from its own configuration, so without them the agent can see the refusal but never the reason for it; `scopeReadToolInput` pins both to the issue's own instance (they are the only read tools that do not receive it, and their resolver would otherwise fall back to the service default) and strips the keys that select the raw-JSON forms, leaving only the bounded summary. The write tools stay unreachable. By default it also follows the shared model; an admin may instead save a remediation-only model override after a real response test. It uses only admin-global credentials: the shared Anthropic/OpenAI/Gemini API key or shared OpenAI OAuth connection. Reporter identity, personal AI settings, per-user included-access grants, and legacy remediation provider/model fields never participate in provider resolution. The tested override is bound to its provider, so a later provider change falls back to that provider's shared model instead of sending a stale designation. Budgets cover total tool calls, accumulated active wall-clock time across approval/reporter pauses, and daily run count. API-key providers receive `max_turn_tokens` as a request cap. Codex app-server has no equivalent request field, so Cantinarr records its per-turn usage notifications and interrupts once reported output reaches the configured ceiling. That is a best-effort guard rather than a hard cap: notification timing can let a response exceed the boundary before interruption. Wall-clock, concurrency, daily-run, and tool-step bounds remain independent safeguards. Each turn's authoritative scope also carries the issue's remediation memory: every fix already dispatched, the arr download it acted on, and whether the arr put that same download back afterwards. Without it a fresh run starts from an empty transcript, re-reads the Import Doctor's prescriptive suggestion, and re-derives the fix that already failed. Only identity and clock fields cross into that system-role block — the arr's own result text stays at user-role trust with the rest of the untrusted incident data.
3. **Ask** -- if the agent needs information only the reporter has, the issue flips to `awaiting_user` and the reporter answers in the issue thread.
4. **Propose** -- in `supervised` mode, mutating fixes (grab release, remediate queue, manual import, trigger search, rescan, delete media files) become typed `agent_actions` that always require admin confirmation. `investigate_only` mode records no proposal. The server validates the action against the issue's authoritative instance/media/queue/download/episode scope, permits only one active proposal, and stores an admin override separately from the agent's immutable proposal. For a release grab, the server binds title, quality, size, protocol, indexer, and rejection details from the latest exact scoped search; the approval card shows that server-observed metadata. Raw indexer capabilities are replaced by one-way references before persistence or API delivery. Approval refreshes the exact movie, season, or episode search, requires both the reference and metadata to match, and resolves the live capability only in memory for immediate dispatch; episode reports also trigger only an episode search. A manual import filters the just-fetched candidates by the same movie/series/episode identity even when `force` is approved. Deleting already-imported files is bound the same way: the action's TMDB id must be the issue's, a season- or episode-scoped issue pins the season, an issue naming one exact episode may delete that one episode's file and no other, and a book issue cannot propose the kind at all. No queue row is left to re-check there -- the download it cleans up finished successfully, possibly weeks earlier -- so the library lookup from the issue's own TMDB id is the identity gate, and files are deleted before any release is blocklisted, because a replacement search fired while the old file is still on disk is refused as "not an upgrade". That one action is the whole repair: it deletes, blocklists, and then replaces, so one problem produces one approval rather than asking an admin to authorise the second half of a decision they already made. The replacement is a TV season search narrowed to the episodes that have already aired and are missing a file, resolved from live air dates at DISPATCH, never at proposal time: episodes air while a proposal waits for an admin, so a set frozen when the fix was written would search for content that still does not exist or skip content that now does. It is skipped in exactly one case -- blocklisting already triggered the service's own failed-download handling -- because there the service has dispatched the search itself and a second would only duplicate it. An empty set is a completed search, not a failure. There is deliberately no aired-only variant on `trigger_search`: leaving one in the agent's vocabulary would let it split the repair back apart, so the single-approval guarantee is structural rather than a line of prompt. Book issues carry the durable Chaptarr author/book record ids captured from the queue snapshot; title-level book mutations (trigger search, rescan, release grabs) validate against those exact ids — a single-book search must name the issue's book, an author search/rescan its author, and a book release grab re-searches that exact book — and every one fails closed on a legacy issue that lacks them. The book recovery witness binds the import-history record (`bookFileImported`, the Readarr-lineage event vocabulary) by exact book + download identity; Chaptarr history carries no reliable file id, so a present file id must name the exact current file while a missing one additionally requires the receipt to postdate the download's own attempt (a reused download id cannot resurrect a stale import as proof). It tracks the newest book file id across multi-file records, never closes while the incident's queue row still signals a problem (a partially imported multi-part audiobook promotes for attention instead), and escalates to `needs_admin` when no receipt exists. Chaptarr queue snapshots carry the same complete-or-error contract as Radarr/Sonarr: truncated, oversized, or duplicate-id responses are read failures, never a shorter queue.
5. **Decide** -- every approval card and confirmation names the exact target service, instance name, and immutable instance ID. Approval uses a compare-and-swap claim so retries reconcile the durable state instead of dispatching again; denial (with an optional note) resumes the investigation. A fresh exact-scope recovery check runs both before and immediately after the execution claim: if the arr has begun retrying/replacing, the proposal is superseded, its run is aborted, the issue returns silently to `recovering`, and the executor is never called. A losing concurrent decision returns `409 Conflict`, prompting the app to re-read the winner instead of claiming the attempted decision succeeded. Recovery never hides `needs_admin`, `executing`, or `outcome_unknown`. A process loss after dispatch cannot prove the remote outcome, so startup marks that action `outcome_unknown` and never guesses or silently replays it. Partial or unknown outcomes stop at `needs_admin` and abort the parked run; the model cannot propose another mutation until a human has verified remote state.

   **Standing auto-approval rules** remove the repeat approvals without removing the gate's machinery. Approving a fix with `remember: true` arms a rule keyed on the exact triple of the issue's problem label (`issues.problem_kind` — the Import Doctor's verdict or the pre-air season finding; a USER report earns the same label when it attaches to a diagnosed queue row or the server's own season check trips at proposal time, so a rule covers its dispatch too — the reporter still owns the close, and an undiagnosed report never matches anything), the action kind, and a per-kind safety facet (`manual_import` force vs. plain; each `remediate_queue` sub-action separately; `delete_media_files` also blocklisting the release (`blocklist`) vs. removing only the files (`files_only`)). Rules are global across instances and media types, but never wider than the triple the admin actually reviewed. Once a minute, the observation sweeper approves any gated proposal an active rule matches through the very same decision core — CAS claim, both recovery preflights, single dispatch, outcome recording — with `decided_by` null and `auto_rule_id` set for audit, and the model's resume says a standing rule (not an admin) approved. Arming a rule is retroactive: matching proposals already waiting are approved on the next sweep, and a proposal auto-approved inside the push hold-down never pages. Trust lasts exactly as long as the track record is clean: the first failed or unverifiable auto-approved outcome pauses the rule **in the same transaction** that records the outcome, and an issue a rule acted on that later ends in any non-resolved pipeline verdict (give-up, `wont_fix`) pauses it too, while `resolved` closes increment its resolved counter (admin dismiss/complete are neutral). A pause posts a system message on the evidence issue and an `agent_autoapproval_paused` alert; re-arming is an explicit admin action (`resume`, or checking remember again on the next manual approval). A restart that interrupts an auto-approved `executing` action pauses its rule at boot, before the interrupted action is repaired to `outcome_unknown`. A rule may also never replay a fix unattended against a release it already ran on: every dispatched queue-scoped action records the arr download it acted on (`agent_actions.target_download_id`, taken from the issue's download identity, which the executor's identity gate has already proven matches the live queue row), and a proposal repeating that same kind *and* facet against the download the issue is still holding is left for manual approval while the rule stands down. A fix that ran and did not hold is not a clean track record — an arr blocklist can match on the release title rather than the release itself, so "remove, blocklist, re-search" can be followed seconds later by a re-grab of the identical download.
6. **Complete or audit** -- when judgment or manual verification is required (especially `needs_admin`/`outcome_unknown`), an admin can explicitly mark the issue `resolved` or `wont_fix` with a required bounded note. The note, admin actor, aggregate close, proposed-action supersession, and parked-run abort commit together under `admin_completed`; a race returns `409` and the app reloads the winner. **Dismiss** remains a separate `admin_dismissed` workflow and does not claim review. Every action and run remains reachable from the issue, and runs persist their ordered step ledger (`agent_runs`/`agent_steps`) with token counts and stop reason. Model-facing issue text, tool results/errors, resume outcomes, transcripts, and audit text are credential-scrubbed before they are sent or stored; the reporter's original thread message remains intact for the reporter/admin UI.

**The reporter closes their own report.** A user-reported issue can never close itself, and that is correct: "this is the wrong episode" is a judgment, and the server cannot prove a judgment satisfied -- the conclusion gate refuses every non-auto source for exactly that reason. The consequence was that *every* user report ended at `needs_admin`, including the ones the agent diagnosed, repaired and verified, so an administrator's job became rubber-stamping someone else's opinion about content they had not watched. `POST /api/issues/{id}/confirm-fixed` closes it as `reporter_confirmed` on the one judgment that counts. It is available only to the issue's own reporter -- an admin using it would record their verdict as the reporter's, and admins already have `/api/admin/issues/{id}/resolve` -- and only while the issue is open, a fix has actually reached the *arr, and nothing is mid-dispatch. Each of those rules out a different wrong closure: an auto incident has a typed proof and needs no opinion, "confirmed fixed" must never record approval of a fix that never ran, and a confirmation racing an executing action would close over an outcome nobody has seen. It is an explicit action and never an inference from a reply: a free-text "yeah looks good" read by a model is not a closure decision. The confirmation is written to the thread as the reporter's own message *before* the close, because a closed issue rejects replies outright, and the single-issue read carries `can_confirm_fixed` so only the reporter's client renders the control. There is no reopen path anywhere in the product, so the app's confirm step says the thread ends and that a problem which returns should be reported again -- the same thing the reply-TTL close already tells people.

Auto-dispatch has a circuit breaker: repeated agent give-ups disable it and notify admins. A missing provider is not a give-up: when remediation is enabled but no shared AI provider resolves, a detected issue simply stays `open` and is re-enqueued until one exists, one deduped `source=system` issue (`system:remediation-provider`) says so once, and the first successful resolve closes it with `resolution_kind=remediation_provider_configured` — the breaker counts agent failures, never configuration gaps, so a provider-less install can no longer silently switch its own auto-dispatch off. A tool-less answer or exhausted investigation becomes `needs_admin` rather than falsely resolving the report. Issue statuses: `observing`, `recovering`, `open`, `investigating`, `awaiting_user`, `awaiting_approval`, `awaiting_confirmation`, `needs_admin`, `resolved`, `wont_fix`, `failed`, `dismissed`. `awaiting_confirmation` is a user report whose fix has EXECUTED and now waits on the reporter's own verdict: it closes read (never paging the admin queue), the reporter is pushed (`issue_fix_confirm`), a single day-3 nudge re-asks (`confirm_nudged_at` makes it exactly once, without resetting the escalation clock), and an unanswered wait is handed to an admin as `needs_admin` at day 7 — the verdict is never fabricated, but a wait may not hold an issue open forever. The other two reporter-loop pushes are `issue_question` (the agent parked on ask_reporter) and `issue_closed` (any terminal close of their report except one they made themselves); all three ride one user-scoped `issue_report_update` preference, on by default. Terminal issues also expose `resolution`, `resolution_kind`, and `closed_at`; current provenance kinds are `agent_concluded`, `arr_state_cleared`, `reporter_confirmed`, `reporter_timeout`, `admin_completed`, `admin_dismissed`, `ai_health_restored`, `push_delivery_restored`, `book_import_cleared`, `remediation_provider_configured`, `prevention_setting_changed`, and `legacy_unknown`.

Each issue also carries an admin **read/unread** flag: promoted issues start unread, as does an import-time pre-air finding (written straight to `open` rather than tracked), any non-admin (agent/system/reporter) status change re-flags it unread, and an admin opening the thread (or dismissing it) marks it read. Passive `observing`/`recovering` incidents stay read and do not count in the drawer's Issues badge. The `mark_resolved_as_read` setting (default on) keeps a cleanly resolved issue read instead of re-flagging it.

### Import Doctor

One shared classifier (`internal/arr/doctor.go`) explains stuck queue items in plain English -- sample files, un-extracted archives, unconfirmed TheXEM mappings, "not an upgrade", unparseable/invalid files, remote-path-mapping or download-client problems, stalled torrents, permissions -- and maps each to ordered one-click fixes: process monitored downloads, manual/force import (candidates shown first, `quality`/`languages` blobs round-tripped verbatim), remove, blocklist + re-search, blocklist without re-search, change category (hand-off to e.g. Unpackerr), rescan.  The blocklist pair encodes one boundary: **Cantinarr clears the stuck item — the part no service will ever do by itself — and does not decide the replacement.** `blocklist_search` removes and blocklists, and stops there; blocklisting is precisely what triggers the service's own failed-download handling, so its settings govern whether a replacement is searched for, including whether a release a human hand-picked may be substituted. Cantinarr adds no search of its own: an agent that did would produce behaviour no human clicking the same button gets, and would override a setting the administrator already made. `blocklist_only` additionally suppresses that replacement search, and is chosen only when **both** halves hold — the queue snapshot proves the movie/episode already has a file, **and** the grab's `releaseSource` says the service picked the release up on its own (an RSS pass) rather than because something went looking. Each half rules out a different mistake. Without a copy on disk there is a real gap, and RSS alone will not fill it: an RSS feed carries only newly *posted* releases and the services schedule no periodic search, so a release already sitting in an indexer is reachable only by searching for it. And if a search produced the download, someone wanted the thing now, and quietly declining to replace it overrules them. Provenance costs a history read, so it is resolved only for the verdicts where it changes the answer (`Diagnosis.ReplacementIsOptional`), and unknown provenance or unknown file state — Chaptarr reports neither — always falls back to the service's ordinary behaviour. The same catalog backs the app UI (Sonarr, Radarr, and Chaptarr), the AI assistant, the remediation agent, and the MCP tools; `diagnose_queue` over MCP prints the exact next tool call per item.

Every problem label the classifier produces is an **exported constant** (`arr.Problem…`) rather than a literal inside a rule table. Those labels are persisted verbatim as `issues.problem_kind` and become `agent_approval_rules` keys, so renaming one silently orphans every standing rule armed on it and every stored row that carries it -- a hazard that looked exactly like editing copy while each label lived as a string in its own rule. The rule tables, the prevention catalog below, and any future consumer now name the same bytes through the same identifier, so a label and the advice keyed on it cannot drift apart.

**A truncated import reports itself.** The failure class every queue-shaped detector structurally misses: a sample clip or truncated encode that IMPORTS leaves a healthy-looking queue and a full-looking library, and sits there until a human presses play. The Sonarr Download webhook now hands every completed import to a sentinel that judges the file against the arr's own analysis: a file running under 40% of the show's own per-episode runtime is not a short episode — it is not the episode. The evidence is Sonarr's ffprobe runtime; a file the arr has not analyzed yet gets NO verdict (blindness is never treated as evidence), a show without a stored runtime gets none either, and the conservative threshold keeps specials and short episodes clear. Advisory-first by design: the notice opens at `needs_admin` with the SAMPLE problem label — one cause, one label, so the prevention rollup and its minimum-size advice already speak for the pattern — and no agent run. Running the agent on an episode-scoped, queue-less auto issue needs its own recovery proof (the same design the pre-air class earned); that upgrade is deliberate follow-up work, not a bolt-on.

**Pre-air season fill** is a second detector beside the Doctor (`internal/arr/season.go`), for the failure the Doctor structurally cannot see: by the time anyone notices the wrong episodes, the download has finished, imported, and left the queue -- on a season grabbed ahead of its air dates, weeks earlier. `BuildSeasonTimeline` is therefore a pure function over typed episode facts rather than a queue classifier, and it turns on one rule: **a file the service imported before its episode aired cannot be that episode**, and a season already holding files for episodes that have not aired is content that has not been released. Aired is decided by the air date and only the air date -- deliberately not `sonarr.SeriesCompletion`'s definition, which counts an episode as aired once it merely holds a file and would fold every impossible file straight into the aired count. An episode carrying no air date is neither aired nor unaired and is excluded from every bucket rather than guessed at. The finding needs two unaired episodes holding files, not one: a single one is an everyday air-date slip and the file is probably genuine, while a batch is content no air-date error explains. `get_episode_timeline` renders the season episode by episode and, when the finding trips, prints the exact repair as a SINGLE call -- `delete_media_files` over the episodes whose files predate their own air time -- and says not to follow it with a search. That one action deletes, blocklists, and then searches the episodes that have already aired, leaving the rest of the season for the service to grab as it comes out. The same kind now repairs a WRONG BOOK: scoped to the issue's own durable Chaptarr record id, it deletes the record's file(s), marks the delivering grabs failed through the same history/failed route, and leaves the replacement to Chaptarr's failed-download handling when that is on (books have no air dates, so there is no aired-only half; a file that arrived after the fix was proposed is spared). The one thing that calls its search off is the service having been triggered to look itself: marking a grab failed is what starts the service's own failed-download handling, so when that setting is on the repair leaves the replacement to it and says so, and only searches when the service will not. That is the same boundary PR #363 drew -- never duplicate or overrule the administrator's own replacement policy -- without making them approve a single repair twice. Deletion runs before blocklisting, and `POST /api/v3/history/failed/{id}` -- the services' own "Mark as Failed" button -- is the only route to blocklist a release that already imported, since neither Sonarr nor Radarr exposes an add-to-blocklist endpoint. Nothing scans the library, and nothing in this server ever does -- but the detector no longer waits to be asked.

**Caught at import, not at complaint.** Sonarr's `Download` callback fires on every import and upgrade, and the managed webhook already subscribes to both (`onDownload`, `onUpgrade`), so the same detector runs the moment an impossible file lands -- no report, no queue row, nobody having sat down to watch the wrong thing first. Cantinarr now parses `episodes[]` off that payload (`id`, `episodeNumber`, `seasonNumber`, `airDateUtc`), the fields it discarded until this, because an episode's own air date is the only thing that can say a file claiming to be it cannot possibly be it. The gate is deliberately free: an `airDateUtc` still in the future is one comparison against the clock and no network at all, and the check stops at the first one it finds, so an ordinary import costs a few comparisons and only the impossible case ever pays for a library read. The handler decides nothing beyond that -- one early file is an everyday air-date slip, and the two-file threshold belongs to the detector -- so it reports the (series, season) once and the witness re-reads the whole live season before judging it.

What the finding opens is ONE `source=auto` issue for the **season**. A pack that imports as nine episodes is one problem; keying per episode would open nine issues, run the agent nine times, and ask an admin to approve one decision nine times -- the same one-problem-one-decision rule the repair itself follows, applied at intake instead. The dedupe key is season-scoped (`pre-air:` + instance + TVDB identity, TMDB only for a series Sonarr holds without one, + season number) and namespaced away from the queue observer's incident keys, so the open-issue unique index turns a webhook retry, or the second and ninth file of the same batch, into a refreshed `detail` and an incremented `occurrences` rather than a second row. It opens at `open`, not `needs_admin`, because the agent is what turns a finding into a proposal and only `open`/`investigating` issues are ever enqueued -- which is exactly why no `source=system` issue ever gets a run: the health sinks (shared-AI health, push delivery, book-import stall, remediation-provider, the auto-dispatch breaker) and the recurrence notice below all open at `needs_admin`. `problem_kind` is set to the persisted label `Content that has not aired yet`; every one of those system issues leaves it NULL and can therefore never match a standing auto-approval rule, while this one can, so an admin who has reviewed this repair once may arm one for it. It is written unread, so it counts in the admin Issues badge from the moment it exists -- unlike a queue observation, which starts silent inside a tracking window and only marks itself unread if it survives to promotion, this finding is terminal as soon as it is made: those files exist and cannot become genuine. The page still rides the ordinary 3-minute `issue_created` hold-down rather than firing immediately, because a batch import delivers a webhook per file within minutes and none of it is urgent. No `issue_observations` row is written -- this is not a queue incident, and the queue sweeper must not adopt it.

Instant updates are an admin action, so an instance whose webhook was never configured would otherwise be uncovered. A fallback pass rides the observation sweeper's existing goroutine, every 15th one-minute tick, rather than a timer of its own: every arr library read in the service already happens on that worker, and a second timer would race it for the one DB connection. It is still not a scan. One history page per Sonarr instance per pass names only the seasons something actually imported into (`downloadFolderImported`, the same event vocabulary the content-alert catch-up reads), filtered by an in-memory per-instance watermark that starts 6 hours back on the first pass after a restart -- long enough to catch a batch that landed while Cantinarr was down, short enough that a cold start is not a sweep of everything -- and at most 8 distinct seasons are examined per pass, logged when that cap bites rather than silently truncated. A quiet instance therefore costs exactly one arr call per quarter hour and reads nothing else, and repeats are free because the dedupe index makes a second open a no-op. The watermark advances only after a pass completes, so a failed read re-examines its window instead of stepping over it.

**A season-scoped issue could close through neither existing recovery proof.** `exactRecoveryProven` and `upgradeAbandonProven` both read `issue_observations`, which this class deliberately does not have, and both go through `exactIssueFileState`, which fails closed on `episode_number = 0` -- correct for a queue incident, where *which exact file* is the whole question, and simply the wrong question to ask about a season. So this class carries its own proof, in two typed, server-computed halves that never rest on the model's reading of its own tool output. `get_episode_timeline`, when scoped to a single season, emits a `season_clean` verification whose `target_present` is the live finding, and the runner sets the same "target cleared" flag from it that `get_queue`'s `queue_target` already sets, so the conclusion gate opens for this issue class without being loosened for any other; a season Sonarr does not hold gets no verdict at all, so an absent season can never read as a repaired one. Then, before the issue actually closes, the server re-reads the live season and requires that **nothing** unaired still holds a file -- not merely that the finding stopped tripping, which, the threshold being two, would let a single impossible file left behind pass as repaired. Both intake paths are TV and Sonarr only: Radarr has no equivalent, the fallback reads Sonarr history alone, and neither ever looks at a season nothing recently imported into.

### Recurring problems

The Doctor explains one stuck download and the agent repairs it, but nothing in the product has ever said **"this is the fourth time this month, and repairing it again will not help"** -- an admin would have to spot that themselves, across issues the agent handled and they mostly never saw. An hourly pass rolls recent auto-detected issues up per `(instance, problem label)` and, where there is honest advice to give, raises one issue naming the setting that would stop it. The advice catalog is `internal/arr/prevention.go`, beside the Doctor whose labels key it; the measuring is `internal/remediation/prevention.go`.

**It is advice, and it never writes configuration.** Nothing here changes a setting on any service, and that is not a staging decision: most of what a notice names is outside what Cantinarr could write anyway -- there is no client support for release profiles, indexer settings, or delay profiles, and the download-client config surface is read-only -- while inventing a config change out of one week's incidents would be worse than saying nothing. Every notice ends by saying so in as many words.

**"Keeps happening" is three separate calendar days**, and the day count is the condition that carries the meaning. A pattern must clear all three thresholds together over a 90-day window: at least 3 issues, across at least 2 distinct media, on at least 3 distinct days. An auto issue is opened per exact media scope, so ONE five-minute event -- a download client dropping out, a disk filling, a wrong path mapping -- fans out into dozens of issues across dozens of titles inside the same minute. Counting issues and distinct titles alone, Cantinarr would announce "this keeps happening" about something that happened once, which is precisely the lie this feature exists to avoid. Separate days are recurrence; fan-out is not. Distinct media stays as a secondary guard so one title flapping for a week cannot pass either. The roll-up also requires the instance to still exist: deleting an instance does not delete its issues, and nobody needs advice about the settings of an *arr that was removed last week.

Two details of that query are load-bearing. Days are counted with `substr(created_at, 1, 10)` rather than `date()`, because `issues.created_at` carries two encodings -- the SQLite driver stores a bound `time.Time` in Go's own `String()` form, while a row that omits the column gets SQLite's `CURRENT_TIMESTAMP` -- and `date()` returns NULL for the first, which would silently collapse every distinct-day count to 1 and make the threshold meaningless. Both encodings share the leading `YYYY-MM-DD`. And `source = 'auto'` is written as a literal rather than bound, against this package's usual habit, because it is the predicate on the partial `idx_issues_problem_recurrence`: only a literal lets SQLite use that index as a covering index and satisfy the `GROUP BY` from index order instead of scanning `issues` into a temp B-tree.

**A label with no honest preventative answer gets no catalog entry, and an absent entry means no notice is ever raised for that label.** "Waiting to import" and "Already imported" are ordinary states; "Import blocked" and "Download error" are too generic to advise on. Manufacturing advice to fill the table would teach an admin to skip the table. Each entry also carries a scope -- `instance`, `client`, or `disk` -- because the answer is frequently not the *arr the incident was detected on: two Sonarrs pointed at one qBittorrent each raise their own notice about the same box, and the notice says which system the change belongs to.

The notice itself is one `source=system` issue at `needs_admin` (`media_type` `system`, `tmdb_id` 0), keyed `system:prevention:<instance>:<hash of instance|label>` so it is namespaced away from queue incidents, the pre-air keys, and the health sinks. It states the measurement first -- how many times, across how many titles, on how many days -- because those counts are the whole argument for the notice existing, then why repeating the repair cannot stop it, then the places to look in the service's own menu vocabulary. Its own `problem_kind` is left **NULL**, so it can never be counted as an occurrence of the very problem it describes (nor match a standing auto-approval rule). Unlike the health sinks, which page immediately, its alert goes through the ordinary 3-minute `issue_created` hold-down: none of this is urgent, and a notice closed inside that window should never have paged at all. Every field of the advice's INSTRUCTIONS is a server-side code constant, and the instance is named by its display name -- never its URL. The measurement side now also quotes the live values of the settings the notice names, where a section is readable (indexer min seeders, download-client summaries, remote path mappings) -- bounded, secret-free summaries built inside the arr client. And the advice notices being taken: the hourly pass re-reads the named section on every open notice, and a CHANGE from the captured baseline resolves it as `prevention_setting_changed` -- unchanged values never resolve, a notice with no captured baseline never resolves on first sight, and a pattern that re-forms from newer incidents raises fresh.

**The mute already existed.** `prevention_notices` is the durable memory of what has been said and when; the wait before saying it again is chosen by what the admin did about the last one, using the three closures the thread screen already offers on a `needs_admin` issue:

| The admin chose | Next raise no sooner than |
|---|---|
| **Mark resolved** | 60 days |
| **Dismiss** | 180 days |
| **Close without fix** | 365 days |

"Close without fix" is literally an administrator saying they have decided not to fix this, so it is the longest -- and once a notice has been raised three times every cooldown becomes that longest one, whatever the admin chose, because a cause nobody has fixed after three tellings will not be fixed by hearing it more often. Nothing is re-raised while the last notice is still open in front of the admin -- but its counts are kept current, because the counts are the whole argument for the notice and an admin who leaves it open for two months should not still be reading the three occurrences it was raised on. That refresh deliberately does not mark it unread again: re-flagging it every hour would be the nag this design exists to avoid. A served cooldown is not sufficient on its own either: the pattern must **re-form from incidents newer than the last raise**, so a cause that actually stopped never returns whatever the cooldown says, and a sliding 90-day history can never re-page an admin on the strength of incidents they have already seen. There is no new route, no new setting, and no client change anywhere in this -- closing the thread is the control.

The pass rides the observation sweeper's existing goroutine, every 60th one-minute tick, for the same reason the pre-air fallback rides it: a second timer would race reconciliation for the one DB connection. Recurrence is measured in days, so hourly is already far finer than the thing it looks at, and it is a DB-only pass -- no arr is contacted. Decisions and owed pushes pace themselves on their own faster clock (rule approvals strictly before the flushes, exactly as before), so a slow arr in the observation sweep can no longer delay a standing rule's approval or an admin's page — and a notice's hold-down still starts the moment it is raised.

### Push notifications

Cantinarr never holds APNs credentials; it talks to a self-hosted push gateway. Setting `CANTINARR_PUSH_GATEWAY_URL` enables push -- with no API key the server **auto-enrolls** on first start and persists its issued key encrypted in the DB (delete the `push_api_key` settings row to force re-enrollment). Enrollment self-heals: a gateway that's down at boot is retried every 60s, and stored device tokens are re-registered once it comes up.

**Delivery failures report themselves.** Sends are fire-and-forget and are never retried, so a gateway outage silently swallows alerts -- and a notification nobody receives is indistinguishable from one nobody needed to send, which makes push the one dependency that cannot report its own failure through the usual channel. Two consecutive failed sends therefore open one `source=system` admin-only issue (dedupe key `system:push-delivery`); further failures refresh it and count the notifications lost in `occurrences`, and the next successful send resolves it with `resolution_kind=push_delivery_restored`. One failure is treated as a blip. No probe is involved: real sends already report success or failure, which is cheaper than a synthetic test and reports what actually failed. The issue is the right surface precisely because it is visible in the app without any notification being delivered; the alert it raises rides the usual hold-down and lands once delivery recovers, telling the admin their alerts were lost.

Notification categories (per-user preferences; admin-scoped ones are enforced in SQL, not just defaults):

| Category | Default | Audience | Sent when |
|---|---|---|---|
| `request_decision` | off | requester | their request is approved/denied |
| `request_pending` | on | admins | a new request needs review (badge = queue depth) |
| `new_movie` | on | everyone | a movie finishes importing (collapse-keyed per title) |
| `new_episode` | on | everyone | new episode(s) import for a series |
| `new_book` | on | that instance's assigned users — an assignment scopes admins too; an admin with no books assignment hears every instance | a Chaptarr book import lands (witnessed by queue polling; per format, since a title's ebook and audiobook are separate records) |
| `issue_created` | on | admins | a tracked problem becomes actionable after the quiet recovery window, or durable status proof remains unavailable — held behind a 3-minute confirmation and coalesced per source (see below) |
| `agent_action_pending` | on | admins | the agent proposed a fix needing approval — held behind the same 3-minute confirmation and coalesced (see below) |
| `agent_autoapproval_paused` | on (shares the `agent_action_pending` preference) | admins | a standing auto-approval rule disarmed itself after a failed or unverifiable outcome — fixed body, collapse-keyed per rule, deep-links the triggering issue. Rules the boot repair pauses (a restart interrupted an auto-approved fix) are announced once at worker start through this same event |
| `remediation_autodispatch_disabled` | on (shares the `agent_action_pending` preference) | admins | the circuit breaker switched automatic problem detection off; the durable record is its own auto-resolving system issue (`system:remediation-breaker`), closed by re-enabling auto-dispatch |
| `plex_access_request` | on | admins | a user shared their Plex email for a server invite (collapse-keyed per user; body says whether auto-invite already handled it) |
| `plex_invite_sent` | on | requester | their Plex invite email went out (one-tap or auto) |
| `issue_report_update` | on | requester | reporter-loop beats about their OWN report: the assistant asked them a question (`issue_question`), an applied fix awaits their confirmation (`issue_fix_confirm`, re-asked once at day 3), or the report closed (`issue_closed`, except a close they made themselves) |
| `agent_digest` | on | admins | the weekly agent scoreboard — the one push that reports success ("Last 7 days: 5 resolved · 3 zero-touch · 2 by your rules"), skipped entirely on a week with nothing to say |

Bodies are server-authored templates (untrusted text never hits the lock screen), sends are fire-and-forget with a 30s timeout, a 10-minute dedupe window absorbs the overlap between queue polling and webhooks, and tokens the gateway reports dead are pruned automatically. That window is held in `content_alert_claims`, so it survives a restart — durable, not permanent: a claim whose send failed is re-claimable once the window lapses, and a database error fails open (a duplicate beats a silenced channel). The same ledger backs a storm breaker: at most 12 distinct content alerts fan out per 10-minute window, so a mass job — a bulk manual import firing one webhook per title — delivers the first dozen and suppresses (and logs) the rest instead of paging every opted-in household member once per title; the app's live view is the answer past that rate. Payloads carry deep-link data (`type`, `tmdb_id`/`issue_id`/`user_id`; book payloads — request decisions and `new_book` alerts — add `foreign_id`, the Chaptarr foreignBookId, plus `title`, `book_format`, and the pinned `instance_id`, since books store `tmdb_id` 0) the app routes on tap. Books have two `new_book` witnesses. The Chaptarr webhook announces an import the moment it lands, which matters because a small ebook can be grabbed and imported inside a single 30s poll interval and would otherwise never be witnessed at all. The Chaptarr queue poller is the fallback for instances without instant updates configured, and for imports of files already on disk, which the Readarr lineage does not announce. Either way the rule is the same: a record with a file on disk imported; one without a file failed and stays silent. The two dedupe against each other through the same `content_alert_claims` window, so an import announced by both alerts once.

`issue_created` has its own damping, because incidents are scoped exactly — one per movie, per episode, per book record — so a single batch cause (a season's downloads stalling together, a download client dropping out) promotes a whole wave at once. Promotion is an edge, not a verdict: the next complete queue snapshot routinely hands a promoted incident back to passive tracking, and the agent can close it outright, so alerting on the edge announced "did not recover automatically" about downloads that were in fact recovering. Instead, promotion queues the owed push in `issue_alert_queue` and the observation sweeper delivers it only once the issue has stayed out of tracking for a 3-minute hold-down. An incident that falls back to tracking restarts the clock (the row survives, so a problem that later sticks still pages), and one that closes inside the window is dropped unannounced. Whatever clears the hold-down on the same sweep is announced as **one** alert per source: a single issue keeps its `issue_id` deep link, while a wave carries a server-computed `count`, a collapse id of `issue_created:<source>` so a later summary replaces rather than stacks, and no `issue_id` (the app opens the Issues list).

`agent_action_pending` gets the same treatment for the same reason one step later in the pipeline: a batch cause dispatches a batch of runs, and each parks its own proposal — but a wave parks in a trickle (a dozen runs across two workers spread over several minutes), not in one instant. Parking a proposal queues the owed push in `agent_action_alert_queue`; the observation sweeper delivers only once the wave has stayed quiet for the 3-minute hold-down (no new proposal parked), with a 15-minute ceiling from the oldest owed row so a continuous trickle cannot defer the page forever. A proposal decided or superseded inside the window (or whose issue closed) is dropped unannounced — the admin was already acting, or there is nothing left to approve — and unlike issue alerts there is no re-arm rule: supersession deletes the row, and a later re-proposal queues a fresh one. A proposal a standing auto-approval rule approves is dropped the same way: the rule sweep runs just before the alert flush on the same sweeper tick, so automated fixes never page anyone. Whatever clears together is **one** push: a single proposal keeps its `issue_id` deep link and the original copy, while a wave carries a server-computed `count`, the plural copy, a collapse id of `agent_action_pending`, and no `issue_id` (the app opens the approval queue either way).

The poller's queue memory is durable (`arr_queue_witness`), so a completion that landed while the server was down is witnessed on the first poll after boot instead of being lost to a re-seed from empty. Completions no queue snapshot ever saw — content added directly in the arr, grabbed, and fully imported while Cantinarr was down (or while the arr/network was unreachable for 5+ minutes, which is gated the same way without needing a restart) — are recovered by a bounded import-history catch-up: one page of the arr's own import event log since the last successful poll (`eventType=3`, re-matched by name — `downloadFolderImported`, or the Readarr-lineage `bookFileImported` vocabulary shared with the webhook receiver), the same events its webhook would have delivered. History may only ever add alerts: a failed read degrades to the witnessed departures, and every recovered id passes the same live re-verification before anything is announced. The resume is bounded: windows older than 6 hours are dropped whole (the user has long since found the content in the app), the merged batch — departures plus catch-up — announces at most 10 alerts per instance (above that, or when one page cannot prove it covered the window, the whole batch is dropped in favor of the app's live view), and a boot resume waits for the push gateway to enroll before announcing so recovered alerts are not dropped into an unenrolled client. Membership is persisted before anything is announced, so a crashlooping container cannot replay alerts. On a first boot there is nothing stored, so an upgrade can never produce a burst. Only set membership is ever read back — every departure is re-verified live against the arr before it is announced — and rows are dropped when an instance's URL changes or the instance is deleted.

### Plex invites

Linking a Plex account (Settings > Plex Invites in the app) uses plex.tv's PIN flow: the server mints a PIN, the admin approves it in the browser, and the resulting token is stored AES-encrypted in the settings table (it never appears in any API response). With a server and libraries selected, `POST /api/admin/users/{id}/plex-invite` shares them with the user's email via plex.tv's `shared_servers` API — and with **auto-invite** on, the same happens with zero taps the moment a user shares their email from the Watch on Plex guide. A duplicate share (the account already has access) is treated as soft success. Sending an invite stamps `users.plex_invited_at` (a record of Cantinarr's action, not live Plex state) and pushes `plex_invite_sent` to the user; changing the email clears the stamp since the old invite went to the old address. The stable `X-Plex-Client-Identifier` survives unlink/relink.

### MCP server endpoint

Cantinarr exposes its tools as a [Model Context Protocol](https://modelcontextprotocol.io/) server at `/mcp` (Streamable HTTP, session tracked via `Mcp-Session-Id`). External clients (Claude Desktop, Claude Code, Codex, ...) discover auth from the well-known metadata, register dynamically, and log in through a browser page -- with a Cantinarr password or a passkey. This browser login grants an external client access **to Cantinarr**; it is unrelated to the AI Access device-code flows that grant Cantinarr outbound OpenAI OAuth access to a personal or admin-shared ChatGPT account. Connect-link-only users can create their first passkey from the MCP login flow; a password is what authorizes MCP on plain-HTTP deployments where WebAuthn is unavailable. Initialization reports the running Cantinarr build version, advertises only the implemented tool/resource/prompt behavior, and tells clients where to load the operating guide.

Access tokens are short-lived and audience-bound to `/mcp`. Refresh tokens are persisted, rotate on use, have a one-year sliding lifetime, and are tied to a Cantinarr device record -- revoking the device revokes the MCP client. Registered clients and token state live in the database, so they survive restarts and upgrades.

The MCP server also publishes prompt templates and a `guide://cantinarr/agent-guide.md` resource so external agents pick up the same operating habits as the built-in assistant (trending behavior, `display_media` carousel use, request-status checks before requests, admin download-triage rules). Tool declarations include human-readable titles and explicit read-only, destructive, idempotency, and open-world hints. Media-capable tools reference the `ui://cantinarr/media-results.html` MCP App; its resource declaration and returned content both carry the image-domain CSP metadata enforced by compliant hosts.

Authenticated MCP request observability records only bounded protocol metadata: JSON-RPC method and safe target name, protocol/lifecycle era, whether client capabilities were supplied, sanitized client name/version, HTTP status, classified outcome, and duration. Authentication and permission checks run before any observation body read. Logs never contain bearer or session tokens, tool arguments or results, capability values, resource URIs, or JSON-RPC error text. This makes discovery failures and legacy fallback sequences diagnosable without turning protocol logs into a content audit trail.

**Client example**:
```json
{
  "mcpServers": {
    "cantinarr": { "url": "http://your-server:8585/mcp" }
  }
}
```

### MCP tools

The registry contains 38 in-app AI tools; 36 are also exposed through `/mcp`. `preview_profile_change` and `apply_profile_change` are deliberately hidden from external MCP because their one-use handoff depends on authenticated in-app chat-turn provenance. The remediation agent receives a constrained read-only subset plus issue-scoped human gates. Every shared tool can be disabled from Settings > AI Tools. Interactive execution reauthorizes the current device and role immediately before each tool and rechecks the included-AI grant when shared billing is in use. Tools marked **admin** require the admin role (either flagged directly or gated by a permission the user role doesn't hold):

| Tool | Description |
|---|---|
| `search_movies` | Search TMDB for movies |
| `search_movie_collections` | Search TMDB for movie franchises/collections |
| `search_tv_shows` | Search TMDB for TV shows |
| `search_books` | Search the user's book server by title/author; results carry the foreign_book_id the other book tools take |
| `get_trending` | Trending movies/shows by day or week |
| `get_movie_details` | Full movie metadata |
| `get_tv_details` | Full TV show metadata |
| `get_recommendations` | Similar content suggestions |
| `check_request_status` | Is this on my server? (movies/TV by tmdb_id; books by foreign_book_id with per-format state) |
| `get_request_options` | Show the current user's selectable request options and quality profiles |
| `request_media` | Add to Radarr/Sonarr/Chaptarr (books by foreign_book_id + optional format), optionally choosing an allowed quality profile (honors the approval queue) |
| `list_my_requests` | User's request history |
| `display_media` | Curate the visual results carousel (movies/TV verified via TMDB; books verified against the user's own lookup by foreign_book_id) |
| `get_queue` | Combined arr download queue (admin) |
| `get_calendar` | Upcoming releases (admin) |
| `get_library` | What's on the server, filterable; for books, drills from the author list into one author's books (or one exact book) with the book ids the per-book action tools take (admin) |
| `get_history` | Recent grabs/imports/failures; a call scoped to one title reads that title's own history from the service instead of filtering a page of global events, so an old event is still found (admin) |
| `trigger_search` | Kick off an automatic download search; a TV season search can be narrowed to `aired_only`, resolving the aired-and-missing episodes at call time. This narrowing is available to an admin here but deliberately not in the remediation agent's action vocabulary, where replacing what a bad import destroyed belongs to `delete_media_files` (admin) |
| `search_releases` | Exact movie, season, episode, or book indexer search; returns one-way release references, never raw GUID capabilities (admin) |
| `grab_release` | Freshly re-search the supplied exact media scope and download the unique release matching its one-way reference + indexer id (admin) |
| `remove_queue_item` | Remove/blocklist a queue item (admin) |
| `get_disk_space` | Disk space across instances (admin) |
| `get_arr_health` | Arr system health: download client, remote path mapping, indexers, disk, root folders (admin) |
| `get_episode_timeline` | Lay one TV season out episode by episode -- air date, whether it has aired, and the file the library holds for it -- and flag files the service imported before that episode aired (admin) |
| `get_media_file_details` | Inspect the file(s) the library holds for one movie or TV season: resolution, codecs, audio languages, embedded subtitles, runtime, size, and import date -- the arr's own analysis of what is on disk; a file the arr has not analyzed says so explicitly (admin) |
| `get_service_config` | Read-only summary of one settings section -- indexers (min seeders, priority), delay profiles, release profiles, download clients (category), remote path mappings -- bounded and secret-free: credentials and URLs never leave the arr client (admin) |
| `get_book_timeline` | Join what the library holds for one book (files, import dates) to what happened (grab and import history with download identities) -- the receipts a wrong-book report is judged from (admin) |
| `list_arr_instances` | Configured arr instances with the instance IDs the settings tools accept (admin) |
| `get_quality_profiles` | Quality profile summaries, one profile's full stored JSON by id, and optionally the live Radarr/Sonarr language catalog with IDs that may vary by service/version (admin) |
| `get_custom_formats` | Custom format summaries, or one format's full stored JSON by id (admin) |
| `upsert_custom_format` | Create/update a native or TRaSH custom format by exact name; creates enter profiles at score 0, updates preserve profile scores without recomputing stored file matches, and every AI/MCP write is recorded as readable, non-restorable history (admin) |
| `preview_profile_change` | Build a read-only diff for one profile and mint a one-use reference valid only in the same authenticated chat turn after an explicit admin request (in-app chat only, admin) |
| `apply_profile_change` | Autonomously consume that same-turn reference, refuse detected stale state, verify the complete result, and record durable before/after history (in-app chat only, admin) |
| `diagnose_queue` | Import Doctor: explain stuck items + print the exact next call (admin) |
| `get_manual_import_candidates` | List a stuck download's files, mappings, rejections (admin) |
| `execute_manual_import` | Force a download's files into the library (admin) |
| `remediate_queue_item` | One-click queue fix: remove, blocklist (service decides replacement), blocklist without replacement, change category (admin) |
| `rescan_media` | Rescan a movie/series on disk and run the import pass (admin) |

Custom-format tools probe the configured instance's live collection endpoint rather than trusting a stored version. Sonarr requires v4 for custom formats; a collection-read 404 is reported as either an older/incompatible build or a stored instance URL missing its service URL base, because those cases are indistinguishable at that API boundary. Write-side 404s stay concrete so a concurrently deleted record is not misdiagnosed as an old service. Every successful AI/MCP custom-format create or update receives a verified readback and durable history entry with live comparison; failed or ambiguous attempts retain outcome-aware history for reconciliation. Custom-format entries cannot be restored.

Quality-profile mutation is intentionally narrow: one existing profile, one full-object `PUT`, and only upgrade policy, an already-allowed quality/group cutoff, score thresholds, existing custom-format scores, plus Radarr's profile language. It does not create/delete/rename profiles, toggle or reorder qualities, create custom formats, or batch profiles. An explicit admin request lets the assistant preview and apply within the same authenticated chat turn; the admin never has to copy or type the one-use reference. Preview binds the actor, device, issuing chat turn, exact service instance and current URL/API-key fingerprint, profile, complete custom-format collection, relevant language catalog, and desired full object. The resolved instance remains pinned even if the service default changes. Apply consumes the random reference before remote I/O, reauthorizes, rebuilds from fresh JSON, checks the bound state immediately before writing, verifies the complete stored profile afterward, and records server-held before/applied snapshots. Any detected stale state, expired/superseded/restarted/used reference, or ambiguous write outcome requires a new preview.

The final guards are optimistic, not an atomic compare-and-swap: Cantinarr serializes its own settings writes, but a direct arr UI/API edit—or a local authorization, tool-toggle, URL, or API-key change—can still race the last check and `PUT`. Settings > Configuration history safely projects the recorded differences and current live comparison. A guarded restore is offered once, only while the live profile, its relevant dependencies, and the instance binding still match the applied update; success creates a linked, non-restorable history entry and permanently consumes that update's restore action. Direct arr edits or connection changes make restore unavailable rather than overwriting newer work. Typed arr HTTP 400 validation details are projected through a bounded, redacted exception used only for credential-free custom-format/profile endpoints; all other error bodies remain discarded.

Language IDs may vary by service and version, so they are read live from each Radarr/Sonarr instance (`get_quality_profiles` with `include_languages`) instead of hardcoded or reused. Sonarr v4 language behavior comes from scoring an existing `LanguageSpecification` custom format; it has no persistent profile-language write. Radarr's profile-level language is a hard release filter and must be `Any` when a language custom format has a nonzero score. Chaptarr supports the scalar/cutoff/custom-format-score profile changes but no release-language specification. These settings influence future release selection for media assigned to the profile: they do not inspect or remux downloaded streams, change file-level default audio/subtitle flags, guarantee playback language, or retroactively replace files.

### Database

SQLite (pure Go driver) with WAL mode. **The live schema is code**: `internal/db/db.go` -- the `initSQL` create statements plus an in-code list of tolerant `ALTER TABLE` migrations with one-time backfills. There are no SQL migration files.

| Area | Tables |
|---|---|
| Accounts & sessions | `users`, `refresh_tokens`, `connect_tokens`, `devices` (hardware-id deduped), `webauthn_credentials` |
| Requests | `request_log` (approval + season/quality/book-format/instance capture, the fulfilled Chaptarr `book_record_id` so book status survives foreignBookId re-keys, and `park_reason` marking server-owned author-import parks), `book_request_waiters` (shared pending subscribers + their concrete format coverage), `user_request_settings` |
| Instances | `service_instances` (encrypted keys/passwords + current/pending server-only webhook credentials + per-instance media path mappings/legacy mode), `user_default_instances`, `arr_queue_witness` (durable per-instance queue-departure completion witness; its `observed_at` doubles as the import-history catch-up cursor; one row per instance, ignored past 6h) |
| Push | `push_tokens` (one per device), `notification_prefs`, `content_alert_claims` (durable new-content dedupe, 10-minute window; also counted as the 12-per-window alert storm breaker) |
| AI access | `user_ai_settings` (explicit personal selection), `user_ai_credentials` (per-provider encrypted personal API keys), `user_codex_accounts` (personal encrypted OpenAI OAuth authorization), `shared_codex_account` (singleton encrypted included authorization); `users.ai_shared_enabled` stores the included-access grant, while `settings` stores the daily health-check switch/timestamp |
| AI configuration history | `external_setting_changes` (append-only AI/MCP quality-profile/custom-format outcomes, server-held before/applied snapshots, and linked quality-profile restores) |
| Remediation | `issues` (exact arr-scoped reports plus admin-only system alerts, with closure provenance and the `problem_kind` label on auto-detected rows -- the Import Doctor's verdict, or the pre-air season finding; book issues store the Chaptarr author/book record ids in place of TMDB/TVDB identity), `issue_observations` (durable retry/settle clocks, baseline + compact import receipt), `issue_observation_downloads` (incident download IDs + arr attempt/file boundaries), `issue_observation_attempts` (transition audit), `remediation_queue_snapshots` (latest successful minimal typed snapshot), `remediation_observation_failures` (bounded outage timer), `remediation_observation_watermarks` (monotonic per-instance success/failure ordering), `issue_alert_queue` (owed admin pushes, held for the promotion hold-down and coalesced on delivery), `agent_action_alert_queue` (owed approval pushes, held and coalesced the same way), `issue_messages`, `agent_runs`, `agent_steps`, `agent_actions` (one active proposal per issue; immutable proposal + approved params; `auto_rule_id` attributes a standing-rule decision; `target_download_id` freezes the arr download a dispatched fix acted on, so a repeat of an ineffective remedy is distinguishable from a first attempt on a new release), `agent_approval_rules` (admin-armed standing auto-approvals keyed problem × fix × facet, with counters and self-pause state), `prevention_notices` (one durable row per instance × problem label Cantinarr has told an admin keeps happening -- the measured counts, the raise count that caps the cooldown, and the issue that carried it; the issue is the dismissable surface, this row is the memory that decides whether to speak again and how soon). The recurrence roll-up behind it is served by the partial index `idx_issues_problem_recurrence` on `issues` |
| MCP OAuth | `oauth_clients`, `oauth_authorization_codes`, `oauth_refresh_tokens` |
| Misc | `settings` (encrypted KV: JWT secret, push key, request policy, Plex token + invite config, and the `server_settings` blob holding the management-portal URL plus the discovery row source/language preferences), `tmdb_tvdb_cache` (30-day TTL) |

## Project Structure

```
server/
├── cmd/server/main.go        # Entry point, dependency wiring
├── internal/
│   ├── ai/                   # Multi-provider chat: SSE handler, API-key providers
│   │                         #   provider-neutral streaming + conversation store
│   ├── api/router.go         # Chi router: routes, CORS, permissions, /api/config payload
│   ├── arr/                  # Import Doctor, pre-air season detector, safe settings HTTP/validation boundaries
│   ├── auth/                 # JWT, connect links, users/devices, WebAuthn, OAuth AS, RBAC
│   ├── cache/                # Small TTL cache used by request-side digests
│   ├── chaptarr/             # Chaptarr (Readarr v1) client for the books module
│   ├── codexapp/             # Scoped personal/shared Codex auth, chat, usage + lifecycle
│   ├── config/               # Env config (port, name, passkey/push/Codex settings)
│   ├── credentials/          # External credential registry + lazy client caching
│   ├── db/db.go              # SQLite setup, WAL, THE live schema + in-code migrations
│   ├── discover/             # TMDB/Trakt discovery + media detail proxy handlers
│   ├── downloads/            # Unified download-client queue API across all four clients
│   ├── instance/             # Instance registry, defaults invariant, per-user pins, safe webhook rotation
│   ├── mcp/                  # 38 registered tools, toggles, tool server (36 also exposed through external MCP)
│   ├── mcpserver/            # MCP Streamable HTTP endpoint, prompts, agent guide (mcp-go)
│   ├── mediafiles/           # Ticketed, instance-mapped + root-confined media streaming
│   ├── mediapath/            # Cross-platform arr-path validation and local translation
│   ├── nzbget/               # NZBGet JSON-RPC client
│   ├── plex/                 # plex.tv PIN link + shared_servers invites (one-tap & auto)
│   ├── proxy/                # Credential-scrubbing arr reverse proxy (read-only for users)
│   ├── push/                 # Push gateway client, auto-enroll, prefs, notifier
│   ├── qbittorrent/          # qBittorrent WebUI v2 client
│   ├── radarr/               # Radarr API v3 client
│   ├── remediation/          # Issues, agent runner, approvals, auto-dispatch, budgets
│   ├── request/              # Request orchestration, approvals, live availability
│   ├── sabnzbd/              # SABnzbd JSON API client
│   ├── secrets/              # AES-256-GCM secrets-at-rest
│   ├── sonarr/               # Sonarr API v3 client
│   ├── tautulli/             # Tautulli activity/history/stats client
│   ├── tmdb/                 # TMDB client + ID bridge
│   ├── trakt/                # Trakt client (discovery + fallback ID resolver)
│   ├── transmission/         # Transmission RPC client
│   ├── web/                  # Flutter web embed (go:embed) + SPA file server
│   ├── webhooks/             # Arr webhook receiver (server-managed per-instance Basic auth)
│   └── websocket/            # Hub: queue polling, event fan-out, complete observation feed
├── Dockerfile                # API-only build
└── go.mod
```

## Tech Stack

- **Go 1.25** with [Chi](https://github.com/go-chi/chi) router
- **SQLite** via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO)
- **JWT** via [golang-jwt](https://github.com/golang-jwt/jwt), **WebAuthn** via [go-webauthn](https://github.com/go-webauthn/webauthn)
- **WebSocket** via [gorilla/websocket](https://github.com/gorilla/websocket)
- **MCP** via [mcp-go](https://github.com/mark3labs/mcp-go) (Streamable HTTP)
- **Anthropic Messages API**, **OpenAI Chat Completions API**, and **Gemini streamGenerateContent**, plus scoped personal/shared OpenAI OAuth through Codex app-server in `internal/codexapp`
- **Codex app-server** bundled by both Dockerfiles with a pinned `CODEX_VERSION` and checksum-verified amd64/arm64 binaries; its upstream Apache-2.0 `LICENSE` and `NOTICE` ship under `/usr/share/licenses/codex-app-server/`. Source runs can override discovery with `CANTINARR_CODEX_BIN`.

## License

See the root repository for license information.
