# Build, operations, usability, and release

Build gates, install and upgrade behavior, product-wide usability, performance, release pipelines, and exploratory coverage.

Use the [run template](../run-template.md) to record executions of these cases.

## Release gate and build baseline

- [ ] `BASE-001` · P0 · AUTO — From `server/`, run `go vet ./...`; verify exit 0 with no diagnostics.
- [ ] `BASE-002` · P0 · AUTO — From `server/`, run `go test ./...`; verify every package passes without retries or order dependence.
- [ ] `BASE-003` · P0 · AUTO — From `server/`, run `CGO_ENABLED=0 go build ./cmd/server`; verify a working non-CGO server binary is produced.
- [ ] `BASE-004` · P0 · AUTO — From `app/`, run `flutter analyze --no-fatal-infos`; verify no errors or warnings that CI treats as fatal.
- [ ] `BASE-005` · P0 · AUTO — From `app/`, run `flutter test`; verify all unit and widget tests pass.
- [ ] `BASE-006` · P0 · AUTO — From `app/`, run `flutter build web --release`; serve the output and verify the initial route renders without console/runtime errors.
- [ ] `BASE-007` · P0 · AUTO — Build the root Dockerfile for the target architecture; start it with an empty config volume and verify `/api/health` and the embedded web app.
- [ ] `BASE-008` · P0 · AUTO — Build `server/Dockerfile`; start it and verify the API-only image reaches healthy state.
- [ ] `BASE-009` · P1 · AUTO — Verify both Docker images contain the pinned checksum-verified Codex app-server binary and bundled `LICENSE`/`NOTICE` files on amd64 and arm64.
- [ ] `BASE-010` · P1 · AUTO — Run the CI-equivalent pinned Codex integration smoke test; verify protocol compatibility, startup, one turn, and clean shutdown.
- [ ] `BASE-011` · P1 · AUTO — Parse iOS Swift sources with `swiftc -parse` when a local iOS toolchain is unavailable; otherwise build and launch the release candidate on a physical device.
- [ ] `BASE-012` · P1 · AUTO — Build the signed Android AAB in CI and install an equivalent release build on a physical device; verify launch, networking, deep links, and passkey provider registration.
- [ ] `BASE-013` · P0 · API — Call representative public, user, and admin endpoints; verify JSON content types, stable error envelopes, and no panic/HTML error bodies.
- [ ] `BASE-014` · P1 · CHAOS — Run server and app tests three times with shuffled package/test order where supported; verify no shared-state, clock, port, or race flake.

## Install, upgrade, persistence, and operations

- [ ] `OPS-001` · P0 · UI/API — Start with an empty `/config`; complete first-run setup and verify one admin, a writable WAL database, and a generated encryption key are created.
- [ ] `OPS-002` · P0 · SEC — Inspect a fresh and populated database/config volume; verify API keys, passwords, Plex token, push key, webhook credentials, and OAuth authorization are not plaintext.
- [ ] `OPS-003` · P0 · API — Restart the server after configuring every service; verify users, device sessions, requests, settings, instances, Plex configuration, notification preferences, and encrypted credentials survive.
- [ ] `OPS-004` · P0 · UI — Restart during an authenticated app session; verify the session refreshes and the current user is not sent to login.
- [ ] `OPS-005` · P0 · CHAOS — Rotate `CANTINARR_JWT_SECRET`; verify permanent device sessions refresh successfully and nobody is signed out solely because of rotation.
- [ ] `OPS-006` · P0 · SEC — Remove or replace the encryption key while retaining the DB; verify encrypted values fail closed with actionable server diagnostics and are never exposed as ciphertext/plaintext to clients.
- [ ] `OPS-007` · P0 · API — Upgrade a copy of the oldest supported populated database to the candidate; verify all in-code migrations run once and existing accounts, grants, requests, Plex fields, and notification defaults remain correct.
- [ ] `OPS-008` · P1 · API — Restart the upgraded database repeatedly; verify migrations are idempotent and no duplicate/backfill drift occurs.
- [ ] `OPS-009` · P1 · API — Restore a DB plus its matching encryption key into a fresh container; verify all integrations and sessions recover.
- [ ] `OPS-010` · P1 · API — Restore a DB without its WAL/SHM after a clean shutdown; verify the database opens and committed data is intact.
- [ ] `OPS-011` · P1 · CHAOS — Fill the config filesystem or make it read-only during a settings write; verify the write fails visibly, prior state remains usable, and no partial secret/config is reported saved.
- [ ] `OPS-012` · P1 · CHAOS — Stop each upstream service while Cantinarr runs; verify the affected screen shows a retryable error while unrelated modules remain usable.
- [ ] `OPS-013` · P1 · UI — Change server public URL/proxy origin within supported configuration; verify connect links, webhook URLs, WebSocket, passkeys, and embedded SPA routing use the correct trusted origin.
- [ ] `OPS-014` · P1 · API — Launch two processes against unsupported shared state/runtime arrangements; verify unsafe Codex runtime ownership or persistent filesystem use is rejected rather than silently shared.
- [ ] `OPS-015` · P1 · API — With update checks disabled, a dev build, `latest`, and a semver build, verify only the eligible semver build checks GitHub and failures never block startup.
- [ ] `OPS-016` · P1 · UI — Configure, change, clear, and reject an invalid update-portal URL; verify the update banner target and persisted setting match the accepted value.
- [ ] `OPS-017` · P2 · UI — Dismiss an update banner, restart, then publish/simulate a newer version; verify dismissal is per release and the newer banner returns.

## Product-wide usability, accessibility, compatibility, and performance

- [ ] `UX-001` · P0 · UI — On every list/form/screen, verify initial loading, empty, loaded, initial error+retry, stale refresh failure, and pull-to-refresh states without infinite spinners.
- [ ] `UX-002` · P0 · UI — Double-tap/press Enter rapidly on request, save, link, invite, grab, approve, delete, import, and remediation actions; verify controls disable and no duplicate external mutation.
- [ ] `UX-003` · P0 · UI — Verify every destructive action names target, requires confirmation, defaults to preserving files/data, and cancel/background/back is inert.
- [ ] `UX-004` · P1 · UI — Navigate away, resize/rotate, background/foreground, or lose network during non-destructive saves; verify clear final state and no wrong-context completion toast.
- [ ] `UX-005` · P1 · UI — Operate all routes, cards, tabs, menus, sheets, dialogs, checkboxes, and icon actions by keyboard; verify logical focus, visible focus, Enter/Space/Escape/back behavior.
- [ ] `UX-006` · P1 · UI — Use VoiceOver/TalkBack/browser accessibility tree; verify media identity/status, selected tabs, progress, form errors, counts, and icon actions have nonduplicative labels.
- [ ] `UX-007` · P1 · UI — Test 200% text scale, device bold text, long English/Unicode values, RTL/bidi input, and small screens; verify no clipped critical copy/actions or spoofed labels.
- [ ] `UX-008` · P1 · UI — Test missing/broken/slow poster/backdrop/avatar; verify consistent placeholder, semantics, cached-image identity, and no wrong-image reuse.
- [ ] `UX-009` · P1 · UI/API — Test UTC±12/14, DST transitions, locale date formats, invalid/missing timestamps, and clock skew; verify release/history/expiry/last-seen grouping and no off-by-one day.
- [ ] `UX-010` · P0 · UI — Audit requester surfaces; verify Available/Partially Available/Requested/Downloading language and no monitored/cutoff/unmet/arr workflow jargon outside admin context.
- [ ] `UX-011` · P0 · API/UI — Test old-app/new-server unknown fields and new-app/old-server missing fields; display may degrade generically but mutation must fail closed when required identity/scope is absent.
- [ ] `UX-012` · P0 · UI — Verify distinct same-title/year/library records remain distinct everywhere: discovery, global search, books, library, requests, releases, AI carousel, and MCP output.
- [ ] `UX-013` · P1 · UI — Verify browser direct refresh and offline/cache recovery for every SPA route, then upgrade server assets and confirm the old cached app cannot become permanently unusable.
- [ ] `UX-014` · P1 · UI — Verify native/web deep links with encoded paths, cold start, auth required, invalid record, and already-open destination.
- [ ] `PERF-001` · P1 · API/UI — Load production-scale libraries, 10k history rows, 1k queue rows, 500 users/devices, and long issue transcripts; verify bounded pagination/memory and responsive interaction.
- [ ] `PERF-002` · P1 · API — Connect many WebSocket clients and churn reconnects; verify bounded goroutines/subscriptions, authorized fan-out, and prompt cleanup.
- [ ] `PERF-003` · P1 · API — Run concurrent requests, approvals, settings writes, queue polling, webhooks, and remediation against SQLite; verify no prolonged lock storm, partial invariant, or data loss.
- [ ] `PERF-004` · P1 · API — Measure upstream timeout/cancellation for Plex, arrs, downloads, Tautulli, push, TMDB/Trakt, and AI; verify request cancellation propagates and server remains responsive.
- [ ] `PERF-005` · P2 · UI — Scroll long image-heavy grids/lists on representative low-end iOS/Android and web hardware; verify stable frame rate, bounded cache, and no progressive memory leak.

## Setup, updates, site, store, and release operations

- [ ] `REL-001` · P0 · UI/API — On a fresh/fully/partially configured server, verify every current setup item (TMDB, Radarr, Sonarr, Chaptarr, downloads, Tautulli, Trakt, shared AI, push, Plex) derives from actual state with correct essential/optional counts.
- [ ] `REL-002` · P1 · UI/API — Remove each configured integration and return from its destination; verify setup completion reverses immediately rather than storing stale progress.
- [ ] `REL-003` · P0 · API/UI — Simulate newer/equal/older/malformed latest versions; verify only newer semver produces an admin banner and requesters never see update controls.
- [ ] `REL-004` · P1 · CHAOS — Fail/slow GitHub update lookup; verify cached best-effort behavior never blocks server/app and honors the disable env var.
- [ ] `REL-005` · P1 · UI — Save valid HTTP/HTTPS management portals, reject unsafe/invalid URLs, clear it, and verify banner target falls back to update guide.
- [ ] `REL-006` · P0 · UI — Verify About shows exact app/server build versions and all update/privacy/support/repository links use the intended trusted destinations.
- [ ] `REL-007` · P0 · LIVE — Pull the published GHCR image/tag on clean amd64 and arm64 hosts; verify `latest`, version tags, startup, health, embedded app, persistence, and bundled notices.
- [ ] `REL-008` · P0 · LIVE — Exercise documented upgrade with a production-like `/config`, verify data and rollback prerequisites, and ensure the guide contains no destructive/incorrect command.
- [ ] `REL-009` · P1 · LIVE — Run TestFlight workflow for an iOS-relevant change; verify signed build installs, entitlements/passkeys/push/deep links work, and excluded paths do not trigger unintended builds.
- [ ] `REL-010` · P1 · LIVE — Run Play beta workflow for Android-relevant changes; verify signed AAB, package/version, passkeys/deep links, build-only PR behavior, and no upload without service-account secret.
- [ ] `REL-011` · P1 · API — Verify store-listing-only changes use the listing workflow, copy/assets land in intended storefronts, and do not trigger irrelevant native builds.
- [ ] `REL-012` · P1 · UI — Validate marketing site homepage/404/header policy at phone/desktop sizes; verify navigation, screenshots, self-host snippet, demo, store badges, privacy, and canonical links.
- [ ] `REL-013` · P1 · LIVE — Deploy `site/` through Cloudflare workflow/manual Wrangler path; verify correct project, cache/security headers, assets/fonts, and live smoke without a build step.
- [ ] `REL-014` · P1 · SEC/UI — Run link checker, HTML validation, accessibility audit, image-alt/contrast/keyboard checks, and verify no secret/env data is emitted into the static site.
- [ ] `REL-015` · P0 · API — Compare shipped behavior with `README.md`, `server/README.md`, `app/README.md`, update/store docs, privacy policy, site copy, route/tool/env/table counts, and version floors; record any drift as a release defect.
- [ ] `REL-016` · P0 · API — Verify `CLAUDE.md` remains only a thin import/pointer to `AGENTS.md` and contributor/test/release instructions do not conflict.
- [ ] `REL-017` · P1 · UI/AUTO — Build `app/test/preview/screenshot_main.dart`, run `app/tool/screenshots/shoot.js`, and verify every required store image has deterministic populated data, documented dimensions/fastlane placement, no live credentials, and no clipped UI.

## Exploratory and compatibility pass

- [ ] `EXP-001` · P2 · UI — Run a 60-minute unscripted requester session across discover/search/request/status/guide/AI with network changes; record confusion, stale state, and crashes.
- [ ] `EXP-002` · P2 · UI — Run a 90-minute admin session across every module with two instances, concurrent Admin B changes, and mixed external mutations; record wrong-target or stale-control risks.
- [ ] `EXP-003` · P2 · LIVE — Repeat the highest-risk integration flows against the oldest and newest supported upstream Plex/arr/download/Tautulli versions.
- [ ] `EXP-004` · P2 · UI — Run Chrome, Safari, and supported mobile web plus current iOS/Android release builds with slow 3G/high latency and intermittent VPN.
- [ ] `EXP-005` · P2 · SEC — Perform a focused abuse pass as a curious household requester using browser devtools/direct API calls, guessed IDs, and prompt injection; record any information or mutation beyond role.
