# Demo Server — Stage A Shared Contract (FROZEN)

Every identifier, constant, and seed value in this document is frozen. Domain
implementers reference ONLY identifiers listed here plus identifiers they
define themselves; prefix your own package-level names with a domain-distinct
prefix (e.g. `disc…`, `arrR…`, `bookQ…`) so parallel files never collide. If a
shared type lacks a field you need, add a domain-local supplementary map keyed
by the entity id in your own file — do NOT edit `types.go`, `state.go`,
`ws.go`, `auth.go`, or `main.go`.

Package facts: single package `main`, module `github.com/windoze95/cantinarr-demo`,
go 1.25, port **8484**, chi v5 router. Allowed deps: chi v5, go-chi/cors,
golang-jwt/jwt/v5, gorilla/websocket, google/uuid, stdlib (incl. `embed`,
`image/png`). No new modules. Files will not compile alone until all siblings
land — still run `gofmt -l` on your files (must be clean) and eyeball imports.

---

## 1. JSON rules (the whole game)

- Integer counters marshal as JSON **integers** (use int types, never float64).
- Fractional ratings (e.g. arr `ratings.value`) are float64 with a fractional
  value, or omitted — an int there blanks app screens.
- Arrays the app iterates are `[]` (empty slice), NEVER null. Initialize every
  slice.
- Error bodies are `{"error":"<message>"}` via `writeErr`, always
  `Content-Type: application/json`; exact message strings from the specs where
  documented (the app displays them verbatim).
- Field casing exactly as specced: Cantinarr API = snake_case; fake arr APIs
  (radarr/sonarr/chaptarr proxies) = camelCase.
- Never return secrets or credential values — presence booleans only.
- All seeded content must be public-domain works or fiction invented for the
  demo.

Helpers (types.go):

| Identifier | Semantics |
|---|---|
| `writeJSON(w, status, v)` | Marshal `v` with `Content-Type: application/json` and the status code |
| `writeErr(w, status, msg)` | Write `{"error": msg}` (JSON) with the status code |
| `queryInt(r, key, fallback) int` | Integer query param with fallback on absent/malformed |
| `strPtr(s) *string`, `intPtr(i) *int`, `boolPtr(b) *bool` | Pointer builders for seed data |
| `yearOf(date string) int` | Year from `"YYYY-MM-DD"`; 0 when unknown |

## 2. Shared constants (types.go / state.go)

Roles: `roleAdmin` = `"admin"`, `roleUser` = `"user"`.

Media types: `mediaTypeMovie` `mediaTypeTV` `mediaTypeBook` (= `movie`/`tv`/`book`).
Service types: `serviceRadarr` `serviceSonarr` `serviceChaptarr` `serviceSabnzbd`
`serviceNzbget` `serviceQbittorrent` `serviceTransmission` `serviceTautulli`
(exact strings, lowercase). Media types describe media, service types describe
services — never conflate.

Request statuses (every REST surface): `statusUnavailable` `statusRequested`
`statusPending` `statusDenied` `statusDownloading` `statusPartial` (`"partial"`)
`statusAvailable`. WS-only spelling: `wsStatusPartiallyAvailable`
(`"partially_available"`) — used ONLY on the `request_status_changed` WS event.
Sending `"partial"` over WS or `"partially_available"` over REST breaks the app.

Book formats: `bookFormatEbook` `bookFormatAudiobook` `bookFormatBoth`.

WS event name constants: `evtDownloadProgress` `evtRequestStatusChanged`
`evtDownloadsQueue` `evtArrQueueChanged` `evtRequestPending`
`evtRequestDecision` `evtIssueCreated` `evtIssueUpdated`
`evtAgentActionPending` `evtAgentActionDecided` `evtAgentAutoapprovalPaused`
`evtRemediationAutodispatchDisabled` `evtPlexAccessRequest` `evtPlexInviteSent`.

Auth/session constants (state.go): `demoJWTSecret` = `"demo-jwt-secret-cantinarr"`,
`demoConnectTokenStr` = `"demo0000000000000000000000000000000000000000000000000000connect1"`
(the multi-use reviewer token, bound to user 2), `accessTokenTTL` 15 min,
`connectTokenTTL` 7 days, `refreshPrefix` = `"cnr1."`.

## 3. Frozen seed IDs

### Instances (constants in state.go — use the constants, never re-type the strings)

| Constant | ID | Type | Name | URL | is_default | media_downloads | mappings |
|---|---|---|---|---|---|---|---|
| `instRadarr` | `radarr-1a2b3c4d` | radarr | Radarr | http://radarr:7878 | true | true | `/movies` → `/media/movies` |
| `instSonarr` | `sonarr-5e6f7a8b` | sonarr | Sonarr | http://sonarr:8989 | true | true | `/tv` → `/media/tv` |
| `instChaptarr` | `chaptarr-9c0d1e2f` | chaptarr | Chaptarr | http://chaptarr:8787 | **false — chaptarr is never default** | true | `/books` → `/media/books` |
| `instSab` | `sabnzbd-3f4a5b6c` | sabnzbd | SABnzbd | http://sabnzbd:8080 | true | false | `[]` |
| `instTautulli` | `tautulli-7d8e9f0a` | tautulli | Tautulli | http://tautulli:8181 | true | false | `[]` |

New instance ids use the current format `<service_type>-<8hex>`. The canonical
media root is **`/media`** (`GET /api/instances/media-roots` → `["/media"]`);
arr file-path fixtures live under `/movies`, `/tv`, `/books` (arr-side) which
map to `/media/movies`, `/media/tv`, `/media/books` (cantinarr-side).

### Users

| ID | Username | Password | Role | Flags |
|---|---|---|---|---|
| 1 | `admin` | `demo` | admin | `PasswordEnabled` `PasskeyEnabled` `HasPassword` `AISharedEnabled` all true |
| 2 | `user` | `demo` | user | `PasswordEnabled` `HasPassword` `AISharedEnabled` true; `RequireApproval = &true` (per-user approval override → the approvals loop is demonstrable); `DefaultInstances = {"chaptarr": instChaptarr}` (Books access grant) |
| 3 | `riley` | — (no password; connect-only) | user | `HasPendingInvite:true` (a live single-use connect token is seeded for riley), `PlexEmail:"riley@example.net"`, `PlexInvitedAt:nil` (drives the "Plex invites waiting" badge), `PasswordEnabled`/`PasskeyEnabled`/`HasPassword` false |

New users minted by connect-token get ids 4+ (`demoNextUserID`).

### Devices (one per real user; frozen ids)

| Constant | ID | User | Name | Platform |
|---|---|---|---|---|
| `seedDeviceAdmin` | `b1a9c8d7-0e2f-4a6b-8c1d-3e5f7a9b0c2d` | 1 | Admin's iPhone | ios |
| `seedDeviceUser` | `d4c3b2a1-5f6e-4d7c-9b8a-1f2e3d4c5b6a` | 2 | Pixel 9 | android |

Each device owns one stable `cnr1.<64hex>` refresh token (random per process;
never expires, never rotates).

### Connect tokens

- `demoConnectTokenStr` → user 2, expires ~10 years out, **exempt from
  single-use** (reviewers reuse it). Never mark it redeemed.
- One random single-use token → riley (backs `has_pending_invite:true`).

## 4. State accessors (state.go)

Shared data types:

```go
type DemoUser struct {
    ID int; Username, Role, Password string        // Password plaintext, never serialized
    PlexEmail string; PlexInvitedAt *time.Time; CreatedAt time.Time
    AISharedEnabled, HasPendingInvite, PasswordEnabled, PasskeyEnabled, HasPassword bool
    DefaultInstances map[string]string             // service_type -> instance id (pin / chaptarr grant)
    RequireApproval *bool                          // nil = inherit global; &bool = per-user override
}
type DemoDevice struct {
    ID string; UserID int; Name, Platform, HardwareID string
    CreatedAt, LastSeen time.Time
}
type DemoInstance struct {
    ID, ServiceType, Name, URL, Username string
    IsDefault, MediaDownloads bool
    MediaPathMappings []map[string]string          // {"arr_path","cantinarr_path"}; never nil
}
```

| Identifier | Semantics |
|---|---|
| `allUsers() []*DemoUser` | Every account, id ascending |
| `userByID(int) *DemoUser` | Lookup or nil |
| `userByName(string) *DemoUser` | Lookup or nil |
| `createInvitedUser(name) *DemoUser` | Find-or-create: role user, no password, password/passkey disabled |
| `deleteUser(id) error` | Removes user + devices + tokens; `errUserNotFound`, `errLastAdmin` (self-delete check is the handler's job) |
| `withUser(id, fn func(*DemoUser)) bool` | Mutate user fields under the state lock (role, flags, DefaultInstances, RequireApproval, PlexInvitedAt…); false when absent |
| `setUserPassword(id, pw) bool` | Store password, `HasPassword=true` |
| `setUserPlexEmail(id, email) (changed, ok bool)` | Store email; a CHANGE clears `PlexInvitedAt` |
| `permissionsFor(role) []string` | Exact sorted permission lists (admin 19, user 6 — see below); unknown role → `[]` |
| `userSummaryJSON(u) map[string]any` | One `GET /api/admin/users` element: id, username, role, permissions, created_at, device_count, has_password, password_enabled, passkey_enabled, `ai_shared_enabled`, `has_pending_invite`, plex_email always present; `plex_invited_at` only when set |
| `userAuthJSON(u) map[string]any` | TokenResponse `user` object: id, username, role, permissions, password_enabled, passkey_enabled, plex_email, created_at (+ plex_invited_at only when set) |
| `deviceUpsert(userID, hardwareID, name, platform) *DemoDevice` | Reuse the user's device matching non-empty hardwareID, else new UUID device + fresh stable refresh token; empty name → `"Unknown Device"` |
| `devicesJSON() []map[string]any` | Admin device list, `last_seen_at` DESC, exactly six fields per row: id, user_id, username, device_name, created_at, last_seen_at |
| `revokeDevice(id) bool` | Remove device, kill refresh token (`refresh` then answers 401 `device has been revoked`); false when unknown |
| `deviceRevoked(id) bool` | Whether an id was revoked (requireAuth uses it to kill live access tokens) |
| `issueSession(u, d) map[string]any` | Full TokenResponse: fresh 15-min HS256 `access_token`, the device's STABLE `cnr1.` `refresh_token`, `user` (userAuthJSON), `device_id` |
| `refreshSession(token) (map[string]any, error)` | Echoes the SAME refresh token; errors `errDeviceRevoked` (`"device has been revoked"`) / `errInvalidRefreshToken` (`"invalid refresh token"`) — both 401 |
| `mintAccessToken(u, deviceID) (string, error)` | 15-min HS256 JWT (claims: user_id, username, role, device_id, exp, iat) signed `demoJWTSecret` |
| `parseAccessClaims(token) (*demoClaims, error)` | Validate an access JWT; `demoClaims{UserID int, Username, Role, DeviceID string}` |
| `mintConnectToken(username) (link string, expiresAt time.Time)` | Find-or-create user, set `HasPendingInvite`, 7-day single-use token; link `cantinarr://connect?token=<64hex>&server=<urlencoded demoServerURL>` (the demo always advertises DEMO_SERVER_URL; the request's `server_url` is not echoed — deliberate simplification) |
| `redeemConnectToken(token) (*DemoUser, error)` | Errors verbatim: `"connect token not found"` / `"this link has already been used"` / `"this link has expired"`; reviewer token never burns |
| `instanceByID(string) *DemoInstance` | Lookup or nil |
| `allInstances() []*DemoInstance` | Stable seed order (radarr, sonarr, chaptarr, sabnzbd, tautulli, then created) |
| `withInstance(id, fn func(*DemoInstance)) bool` | Mutate instance fields under the state lock |
| `visibleInstances(u) []*DemoInstance` | Admin: all. User: effective radarr + sonarr + granted chaptarr. Renderers MUST emit `is_default:true` on EVERY entry of a non-admin's list |
| `effectiveInstanceFor(u, serviceType) *DemoInstance` | Per-user pin → global default → first of type; **chaptarr: pin only, no fallback** (nil = no Books access) |

Permission lists (lexicographically sorted, matching the real
`PermissionsForRole`):

- user (6): `["ai:chat","arr:browse","mcp:access","media:discover","media:download","media:request"]`
- admin (19): `["admin:*","ai:chat","ai_tools:manage","arr:browse","arr:read","arr:search","credentials:manage","downloads:manage","downloads:read","instances:manage","mcp:access","media:discover","media:download","media:request","monitoring:read","remediation:manage","requests:manage","system:read","users:manage"]`

**Locking rule:** `stateMu` guards the core store and every accessor above
acquires it internally. Domain files must NEVER lock `stateMu` directly, never
call an accessor from inside a `withUser`/`withInstance` callback, and must
guard their own domain-local maps with their own prefixed mutex (e.g.
`var reqMu sync.Mutex`).

**Seeding rule:** seed domain data in your own file's `init()` (or lazily).
Reference users/instances by the frozen ids/constants above — do NOT call
state accessors from `init()` (cross-file init order is not guaranteed).

## 5. Middleware & context (main.go)

- `requireAuth` — validates `Authorization: Bearer <jwt>`, checks device
  revocation, puts the `*DemoUser` in the request context. Errors (all JSON):
  401 `missing authorization header` / `invalid authorization format` /
  `invalid or expired token`.
- `requireAdmin` — 403 `{"error":"permission denied"}` for non-admins. Mount
  after requireAuth: `r.With(requireAdmin).Get(...)` or a `r.Route` group with
  `r.Use(requireAdmin)`.
- `userFrom(r *http.Request) *DemoUser` — the authenticated user (never nil
  inside a requireAuth-wrapped handler).

**Mount rules for `register<Domain>(r chi.Router)` functions:**

1. Your register function receives the **authenticated** `/api` subrouter
   (requireAuth already applied) — exceptions: `registerAuth`, `registerWS`,
   and `registerMediaFiles` receive the public `/api` router
   (`registerMediaFiles` must apply `requireAuth` itself to
   `/media-files/coverage` and `/media-files/tickets`, leaving
   `GET|HEAD /media-files/download/{ticket}` public/self-authorizing).
2. Register full paths from the `/api` root (e.g.
   `r.With(requireAdmin).Get("/admin/issues", h)` or
   `r.Route("/admin/agent-actions", …)`). **Never** mount a bare
   `r.Route("/admin", …)` or any prefix another domain also owns — chi panics
   on duplicate mounts. Distinct full prefixes per domain only.
3. Admin gating is your job, per route, via `requireAdmin`.
4. The `/api` router pre-sets `Content-Type: application/json`; override
   explicitly for SSE (`text/event-stream`) and image bytes (MediaCover).

Register functions `main.go` mounts (must exist, exact names):
`registerConfig`, `registerUsersAdmin`, `registerPlex`,
`registerNotifications`, `registerRequests`, `registerRequestsAdmin`,
`registerBooks`, `registerDiscover`, `registerTrakt`, `registerAI`,
`registerAIAdmin`, `registerIssues`, `registerRemediation`,
`registerInstances`, `registerDownloads`, `registerTautulli`,
`registerMediaFiles` (public — see rule 1). Stage A provides `registerAuth` +
`registerWS`. `registerInstances` also owns the proxy dispatcher
`/instances/{instanceID}/*` → `handleRadarrProxy` / `handleSonarrProxy` /
`handleChaptarrProxy` (hook table, §7).

`startSimulations()` is a no-op hook in main.go; the integration pass wires
the tickers there. Domain files must NOT define it.

`demoServerURL` (main.go) is the advertised base URL from `DEMO_SERVER_URL`
(default `http://localhost:8484`).

## 6. WebSocket hub (ws.go)

- `registerWS(r chi.Router)` — mounts `GET /ws` (Stage A mounts it; don't).
- `wsStartHub()` — starts the fan-out goroutine (main() calls it; don't).
- `wsBroadcast(event string, data map[string]any)` — every connected client.
- `wsToAdmins(event string, data map[string]any)` — admin connections only.
- `wsToUser(userID int, event string, data map[string]any)` — that user only.

`data` nil is sent as `{}`. Envelope is always `{"type": event, "data": {…}}`.
Use the `evt…` constants for event names. Payload gotchas:

- `download_progress.progress` is a **0..1 fraction**; REST/downloads-queue
  `progress` is **0–100**. Do not mix.
- `request_status_changed.status` uses `wsStatusPartiallyAvailable`
  (`"partially_available"`) for incomplete TV; REST uses `statusPartial`.
- `arr_queue_changed` needs BOTH `instance_id` and `service_type`.
- Audiences: `request_pending`, `issue_created`, `agent_action_*`,
  `plex_access_request`, `downloads_queue` → admins; `request_decision`,
  `plex_invite_sent` → the specific user; `issue_updated` → reporter AND
  admins; `download_progress`, `request_status_changed`, `arr_queue_changed`
  → broadcast.

## 7. Cross-domain hooks (you implement, everyone calls)

These functions MUST be exported by the named domain with these EXACT
signatures. Callers may invoke any of them; nobody else defines them.

**D3 discover** (owns the movie/TV/person catalog — port old-demo `data.go`):

```go
findMovie(tmdbID int) (*DemoMovie, bool)
findShow(tmdbID int) (*DemoShow, bool)
var demoMovies []*DemoMovie   // seed order = old-demo order (18 PD films)
var demoShows  []*DemoShow    // 6 fictional shows, tmdb 90001–90006, tvdb 390001–390006
discoveryPrefsSaved() bool    // true once seeded/saved (demo seeds SAVED)
```

**D1 plex**:

```go
plexConfigured() bool         // linked + server selected (demo seeds linked)
```

**D7 arr (radarr + sonarr fakes)**:

```go
handleRadarrProxy(w http.ResponseWriter, r *http.Request, inst *DemoInstance, isAdmin bool, rest string)
handleSonarrProxy(w http.ResponseWriter, r *http.Request, inst *DemoInstance, isAdmin bool, rest string)
arrOnRequestStarted(tmdbID int, mediaType string)                  // seed/refresh the fake queue item
arrOnRequestCompleted(tmdbID int, mediaType string, seasons []int) // flip hasFile / episode stats at completion
```

`rest` is the path after `/api/instances/{id}/` (e.g. `api/v3/movie`), query
string still on `r`. The handler enforces the non-admin allowlist (GET-only,
effective instance only): radarr `movie`, `movie/{id}`, `calendar`, `queue`,
`history`, `wanted/missing`, `wanted/cutoff` (sonarr equivalent incl.
`series`, `episode`, `queue/details`).

**D8 chaptarr**:

```go
handleChaptarrProxy(w http.ResponseWriter, r *http.Request, inst *DemoInstance, isAdmin bool, rest string)
bookByForeignID(foreignID string) (*DemoBook, bool)
allBooks() []*DemoBook
chaptarrOnBookRequested(foreignID, format string)  // monitored + queue item + arr_queue_changed
chaptarrOnBookAvailable(foreignID, format string)  // downloaded + file ids + library digest flip
```

**D2 requests**:

```go
requestStatusForTmdb(tmdbID int, mediaType string) (status string, progress float64) // REST spellings; progress 0..1
startDownloadSimulation(tmdbID int, mediaType string, instanceID string, seasons []int)
```

`startDownloadSimulation` runs the ticker: ~10 s `requested` → 20 × 1.5 s
`download_progress` broadcasts (0..1) → terminal `request_status_changed`
(`available`, or `partially_available` WS-spelling for incomplete TV), calling
`arrOnRequestStarted`/`arrOnRequestCompleted` in lockstep.

**D6: none exported.**

## 8. Catalog types (types.go — do not extend; supplement locally)

`DemoMovie` (TmdbID, ImdbID, Title, OriginalTitle, Overview, Tagline,
PosterPath, BackdropPath, ReleaseDate, Genres []DemoGenre, VoteAverage,
VoteCount, Popularity, Runtime, OriginalLanguage; methods `TraktID()` =
tmdb×10, `GenreIDs()`, `Year()`), `DemoShow` (TmdbID, TvdbID, ImdbID, Name,
Overview, Tagline, PosterPath, BackdropPath, FirstAirDate, Genres,
VoteAverage, VoteCount, Popularity, Status, Type, OriginalLanguage,
Seasons []DemoSeason — no season 0; methods TraktID/Year/SeasonCount/
EpisodeCount), `DemoSeason` (ID, SeasonNumber ≥1, Name, EpisodeCount,
AirDate, PosterPath, Episodes []DemoEpisode), `DemoEpisode` (ID,
EpisodeNumber, Name, AirDate, Overview, Runtime), `DemoBook` (ForeignID,
Title, AuthorName, AuthorForeignID, Overview, Year, Formats
map[string]*DemoBookFormat keyed ebook/audiobook; methods CanonicalBookID/
CoverPath), `DemoBookFormat` (Monitored, Downloaded, BookID, FileID),
`DemoPerson` (ID, Name, Biography, Birthday, Deathday, PlaceOfBirth,
ProfilePath, KnownForDept, Popularity, Gender, ImdbID, AlsoKnownAs,
CastCredits/CrewCredits []DemoCredit), `DemoCredit` (TmdbID, MediaType
movie|tv only, Character, Job, Department), `DemoGenre` (ID, Name),
`traktEpisodeID(showTmdbID, episodeNumber)` = tmdb×100+ep.

These are Go-side holders WITHOUT json tags — never `json.Marshal` one
directly; build the per-endpoint map/struct your spec documents (TMDB
snake_case vs arr camelCase).

## 9. Resolved design decisions (binding, verbatim)

- No register endpoint / invite code (dead surface); accounts come from seeds
  + connect links.
- Refresh tokens: opaque `"cnr1."+64hex`, never expire, never rotate, echoed
  back verbatim; 401 only for genuine revocation, 503 for faults.
- WS event `request_status_changed` uses `"partially_available"` ON THE WIRE;
  every REST surface uses `"partial"`.
- Seed discovery settings as SAVED: source `"tmdb_trending"`,
  `english_only:false`, `trakt_configured:true`.
- Instance IDs use the current `<service_type>-<8hex>` format (constants in
  contract.md).
- No rate limiting anywhere. Permissive CORS (AllowedOrigins `*`, allow
  Authorization header) — deliberate divergence so browser-hosted app builds
  can point at the demo.
- Middleware/handler errors all standardize on `application/json`.
- Trakt lists endpoint serves FLAT list objects (what the app parses), not the
  nested relay shape.
- Issue `media_type` only `movie|tv|book`; `AgentStep.kind` and run
  `stop_reason` use the server enums.
- external-settings-changes detail returns the BARE change object.
- media-files feature ENABLED (coverage always answers 200).

Additional Stage A notes: the WS upgrader's `CheckOrigin` allows every origin
(consistent with the permissive-CORS decision); trakt artwork strings are
scheme-less `image.tmdb.org/t/p/w500/...` (the app prefixes `https://`; never
emit trakt.tv-hosted URLs); timestamps are Go `time.Time` → RFC3339.
