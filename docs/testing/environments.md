# Test and dev environments

What each testing layer needs to run — and where credentials actually live.

## The automated suites need no credentials

`go test ./...` (from `server/`), `flutter test` (from `app/`), and
`make check-test-automation` are fully hermetic: integration behavior is
proved against in-process `httptest` fakes and fixture payloads, the
database is throwaway SQLite, and no network account is required. CI runs
exactly these, credential-free. If a new test needs a real account or a
live service, it belongs in the live-lab or manual layers described in
[automation](automation.md), not in the suites.

## Where real credentials live

Cantinarr's integration credentials are not environment variables. Radarr,
Sonarr, Chaptarr, download clients, Tautulli, TMDB, Trakt, AI provider
keys, and the Plex account link are all entered through the admin UI at
runtime and stored AES-256-GCM encrypted in the SQLite database
(`service_instances` rows and the settings KV). Environment variables only
tune boot and deployment (port, public URL, push gateway, passkey
origins); the full table lives in the root
[README](../../README.md#configuration), and none are required to boot —
the JWT secret and encryption key auto-generate on first start.

## Self-hostable with no account

A fully functional *arr environment needs no third-party accounts. Every
service below runs locally (the private `cantinarr-lab` repo provisions
exactly this on a disposable droplet), each with only its own locally
generated API key or local username/password:

- Radarr, Sonarr, Chaptarr — local API key from each service's own settings
- SABnzbd, NZBGet, qBittorrent, Transmission — local key or local credentials
- Tautulli — local API key (meaningful data needs a Plex server feeding it)
- Arr Connect webhooks — Cantinarr mints and installs its own per-instance
  tokens
- Push gateway enrollment — account-free against any reachable gateway
  (delivery is different; see below)

## Genuinely live-only

Only these need a real account, and only for live verification — never for
the suites:

- **plex.tv** — the whole Plex integration (PIN link, server/library
  listing, invites) is plex.tv-side; it needs a real Plex account owning a
  claimed Plex Media Server.
- **TMDB** — discovery/search needs a v4 read token from a free
  themoviedb.org account.
- **Trakt** (optional) — a Trakt account plus a registered app for the
  client ID.
- **AI provider keys** — Anthropic/OpenAI/Gemini keys are validated with a
  real model turn at save time, so dummy keys cannot be configured; the
  Codex provider needs a real ChatGPT account and the pinned app-server
  binary.
- **APNs delivery** — the push gateway holds the Apple credentials;
  Cantinarr-side enrollment needs none, but a push reaching a device is
  Apple-live.
- **GitHub update check** — live but anonymous; disable with
  `CANTINARR_DISABLE_UPDATE_CHECK`.

Keep every real credential out of the repo, out of test code, and out of
CI: the suites must stay runnable on a fresh clone with nothing but Go and
Flutter installed.
