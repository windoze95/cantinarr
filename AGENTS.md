# Agent Instructions

Operating manual for AI agents and human contributors. `CLAUDE.md` imports this file — this is the single canonical copy; edit it here, never there.

## Collaboration

- Do not just agree with the user by default. If a request would weaken the project, hurt maintainability, reduce correctness, or make the task outcome worse, push back clearly and suggest the better path.

## Git workflow

- Before starting any PR-sized change: `git fetch origin`, make sure local `main` is even with `origin/main`, and create a feature branch from that fresh base.
- Branch names follow `feat/…`, `fix/…`, `docs/…`, `chore/…`.
- Do not open PRs directly from `main`. If work accidentally happens on `main`, verify `main` is still even with `origin/main`, then move the work to a feature branch before committing.
- Preserve user work. Do not revert or delete unrelated local changes or untracked files.
- When the change is ready: commit on the feature branch, push it right away, and open a ready-for-review PR (never a draft) with `gh pr create` unless the user explicitly asks not to.
- After opening a PR, monitor every required CI check to completion and merge only after they are all green. If a check fails, diagnose and fix it on the same branch, push the fix, and wait for the rerun to pass before merging; never merge with pending or failing required checks.
- After a PR merges, do not reuse its branch — start the next change from a fresh `main`.

## Verification

- Server changes: run `go vet ./...` and `go test ./...` from `server/`.
- App changes: run `flutter analyze --no-fatal-infos` and `flutter test` from `app/`. Golden tests live in `app/test` (committed `goldens/*.png` beside their test file); regenerate them with `flutter test --update-goldens` from `app/`.
- CI runs exactly those on every PR (Go tests with `-race`), plus a `CGO_ENABLED=0` server build and a `flutter build web --release`. A PR is not done if any of them fail. The same suite re-runs on every push to `main` and on a weekly schedule (toolchain drift); a red `main` run is a defect to fix promptly.
- **The `main` run is the release gate, not just an alarm.** It is the only run that ever tests the exact tree that ships: PR checks run against a merge preview computed from whatever `main` was at the time, and branch protection doesn't require branches to be up to date, so two independently green PRs can still land a broken `main`. Every shipping workflow — `docker.yml`, `testflight.yml`, `playstore.yml` — therefore opens with a `gate` job (`require-ci-green.yml`) that waits for the `CI` run on that exact SHA and fails the workflow if it isn't green. Nothing is published, uploaded, or tagged before that. A `v*` tag reuses the CI verdict from the `main` push of the same commit.
- Pull-request builds are deliberately *not* gated: a PR's Docker build is one of its own checks, and the `pr-N` preview image should stay pullable while the rest of the checks run. The gate only ever holds back an irreversible step.
- Codex integration changes are also proved against the checksum-verified pinned app-server in CI. The Docker workflow builds and smoke-tests both Dockerfiles, including bundled license notices, before publishing the root image to GHCR.
- iOS release builds happen only in CI (`testflight.yml`, auto-deploys on `main` when iOS-relevant `app/**` paths change — web/android/desktop subdirs, `app/test/**`, `app/tool/**`, markdown files, and store-listing metadata/screenshots are excluded; listing copy syncs via `storelisting.yml` instead). Don't assume a local iOS toolchain; when one isn't available, sanity-check Swift with `swiftc -parse` and let CI prove the build.
- iOS signing is manual, via the `IOS_PROVISIONING_PROFILE_BASE64` secret. Changing app capabilities/entitlements invalidates the profile — regenerate it and update the secret.
- Android release builds happen only in CI too (`playstore.yml`, builds a signed AAB on `main` when Android-relevant `app/**` paths change — web/ios/desktop subdirs, `app/test/**`, `app/tool/**`, markdown files, and store-listing metadata are excluded — and uploads it to the Play **alpha** track by default when `PLAY_SERVICE_ACCOUNT_JSON` is set; manual dispatch can pick `internal`). PRs that touch `app/android/**`, `app/pubspec.yaml`, or the workflow get a build-only check (no upload). No local Android SDK is assumed; let CI prove the build.
- Android signing uses the `ANDROID_KEYSTORE_*` secrets (the upload keystore lives outside the repo). Store pipelines, secrets, and the one-time console setup are documented in `docs/store-release.md`.
- Merges to `main` publish `ghcr.io/windoze95/cantinarr` (`latest`; version tags on `v*` releases) once that commit's CI run is green. A `v*` tag on a merged `main` commit additionally auto-creates the GitHub Release that the server's update checker and the app's update banner consume. Images are stamped with `git describe` output, so `latest` images know the nearest release they contain and nag admins on release cadence, never per merge.
- Mention any tests or checks that could not be run.

## Releases & versioning

- **Two release trains, deliberately decoupled.** The server ships by git tag; the store apps ship by their own workflows. They version independently — the About sheet shows both, and the skew floors (below) own compatibility between them.
- **Cutting a server release**: `git tag vX.Y.Z <sha> && git push origin vX.Y.Z`, where the SHA is a `main` commit whose CI run is green (the docker workflow's gate re-verifies and refuses otherwise). Everything downstream is automatic: versioned multi-arch images, then the GitHub Release with notes generated from merged PR titles — so write PR titles as user-facing changelog lines. Never move or delete a published tag; fix a bad release by tagging a new patch. A `-`-suffixed tag (`v0.2.0-rc.1`) is a prerelease channel: images and Release publish, but `releases/latest` — and therefore the update banner — ignores it.
- **Versioning is semver-ish `0.y.z` pre-1.0**: bump the minor for features (and, pre-1.0, breaking changes), the patch for fixes. Tag when `main` has accumulated a coherent user-facing story, not on a schedule. `latest` is a channel, not a version — every green merge replaces it, and a new tag is what tells both `latest` and pinned users they've fallen behind.
- **Version-skew floors move only alongside the breaking change that forces them, in the same PR.** Raise `MinAppVersion` (`server/internal/version/version.go`) when the server stops supporting old apps; raise `minServerVersion` (`app/lib/core/utils/version_compat.dart`) when the app stops supporting old servers. Both are warn-only by design — never turn a floor violation into a hard block as a side effect of a bump; that would be its own deliberate change.
- **Breaking-change ordering when the server drops old-app support**: ship the new app first — bump the pubspec version, let TestFlight/Play process it, dispatch the App Store submission, and wait for Apple's approval — and only then cut the server release that raises `min_app_version`. Apple reviews every App Store submission regardless of bump size, so reversing the order strands store users behind "update this app" banners pointing at an update that doesn't exist yet.

## Manual test checklist

- `docs/testing/` is the manual-only layer: cases that genuinely need a human or a real environment — live third-party truth, physical devices, store/release operations, chaos no suite can stage, and audits/exploratory sessions. Update it only when one of those surfaces changes; everything else is proved by the Go/Flutter suites and belongs there, not in the checklist.

## Architecture conventions

- **The live DB schema is code, not SQL files.** It lives in `server/internal/db/db.go` (`initSQL` plus the in-code migration/`ALTER` list). Schema changes go there.
- **Never trust a stored copy of *arr state.** Admins edit Radarr/Sonarr/Chaptarr directly, so any snapshot drifts. Availability and library state are computed live from the arrs; if you must cache, you must also have a freshness story (webhook invalidation, short TTL, or refetch-on-view).
- **Media types vs service types.** `movie`/`tv`/`book` describe media; `radarr`/`sonarr`/`chaptarr` describe services. Store and compare media types — don't substitute one for the other.
- **Never silently dedupe or merge distinct records in search results.** Surface each record and let the user decide (e.g. two library entries for the same title are two results).
- **Instance URLs resolve only from the server; clients must never dereference arr-origin URLs.** Cluster-internal names (`http://radarr:7878`) are a supported production configuration, so anything handed to a client must be client-reachable: use `images[].remoteUrl` (external CDN) for artwork, resolve arr-relative paths through `/api/instances/{id}/…`, and never surface an arr-origin absolute URL from a proxied body. Server-side, keep hosts out of error strings that can reach non-admins.
- **Secrets stay server-side and encrypted.** Instance API keys and credentials are AES-256-GCM encrypted at rest; never log them, return them in API responses, or write them into docs/examples.
- **Requesters and admins speak different languages.** User-facing request UI uses requester vocabulary (Available / Requested / Downloading), not arr jargon (monitored, cutoff, unmet).
- **An empty answer must say whether it is absence or blindness.** A read that finds nothing has said one of two completely different things — the thing is not there, or this code could not see it — and rendered the same way they are indistinguishable, so a reader stops looking. `internal/mcp/absence.go` holds the vocabulary: every empty result says what was actually searched and what an empty answer does and does not rule out. This is not style. Issue #814 went undiagnosed for thirteen days because three reads came back empty and were believed, when one had filtered a page of recent GLOBAL history for a title whose last event was two weeks old.
- **A complaint about content arrives after the queue is empty.** "Wrong episode", "bad copy", "wrong audio" are only visible once a download finished and imported — sometimes weeks earlier — so queue state answers none of them and an empty queue is the expected reading, not a dead end. Diagnose those from the library and the arr's history, and repair them by acting on the imported file, not on a queue row.

## Documentation

Docs are part of the change, not a follow-up. A feature is not merged-complete until the docs that describe that surface are true again.

| Doc | Owns |
|---|---|
| `README.md` | Product pitch, feature list, quick start, configuration & env-var tables |
| `server/README.md` | API route reference, MCP tool table (incl. the tool count), DB tables, WebSocket events, env vars, server package tree |
| `app/README.md` | App features/screens, navigation map, project structure, key dependencies |
| `docs/books-setup.md` | End-to-end book automation setup: Chaptarr instance, per-user access grant (no global default), instant updates, download path mappings |
| `docs/store-release.md` | Store release pipeline: how builds reach TestFlight/Play, signing secrets, one-time store-console setup |
| `AGENTS.md` | Workflows, verification, conventions (this file) |

- When a change touches a documented surface (new route, tool, env var, table, screen, workflow), update the owning doc **in the same PR**. The PR template's docs checklist is there to force the question.
- Numbers drift fastest: tool counts, route lists, env-var tables, version floors (Go, Flutter/Dart). If you add one, update the count everywhere it appears (`grep -ri` for the old number).
- Docs describe shipped reality — never "planned", "upcoming", or aspirational behavior.
- `CLAUDE.md` must remain a thin import of this file so every agent reads one playbook. If you change workflows here, check `CLAUDE.md` still just imports and points.
