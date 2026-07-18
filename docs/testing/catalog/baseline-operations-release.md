# Build, operations, usability, and release

Real-install and upgrade truth, destructive operational failures, product-wide accessibility/compatibility/performance audits, release and store pipelines, and exploratory coverage. Build gates and contract checks live in the hermetic Go/Flutter suites and CI, not here.

Use the [run template](../run-template.md) to record executions of these cases.

## Install, upgrade, persistence, and operations

- [ ] `OPS-001` · P0 · UI/API — Start with an empty `/config`; complete first-run setup and verify one admin, a writable WAL database, and a generated encryption key are created.
- [ ] `OPS-005` · P0 · CHAOS — Rotate `CANTINARR_JWT_SECRET`; verify permanent device sessions refresh successfully and nobody is signed out solely because of rotation.
- [ ] `OPS-006` · P0 · SEC — Remove or replace the encryption key while retaining the DB; verify encrypted values fail closed with actionable server diagnostics and are never exposed as ciphertext/plaintext to clients.
- [ ] `OPS-007` · P0 · API — Upgrade a copy of the oldest supported populated database to the candidate; verify all in-code migrations run once and existing accounts, grants, requests, Plex fields, and notification defaults remain correct.
- [ ] `OPS-009` · P1 · API — Restore a DB plus its matching encryption key into a fresh container; verify all integrations and sessions recover.
- [ ] `OPS-011` · P1 · CHAOS — Fill the config filesystem or make it read-only during a settings write; verify the write fails visibly, prior state remains usable, and no partial secret/config is reported saved.
- [ ] `OPS-012` · P1 · CHAOS — Stop each upstream service while Cantinarr runs; verify the affected screen shows a retryable error while unrelated modules remain usable.
- [ ] `OPS-013` · P1 · UI — Change server public URL/proxy origin within supported configuration; verify connect links, webhook URLs, WebSocket, passkeys, and embedded SPA routing use the correct trusted origin.
- [ ] `OPS-014` · P1 · API — Launch two processes against unsupported shared state/runtime arrangements; verify unsafe Codex runtime ownership or persistent filesystem use is rejected rather than silently shared.

## Product-wide accessibility, compatibility, and performance audits

- [ ] `UX-005` · P1 · UI — Operate all routes, cards, tabs, menus, sheets, dialogs, checkboxes, and icon actions by keyboard; verify logical focus, visible focus, Enter/Space/Escape/back behavior.
- [ ] `UX-006` · P1 · UI — Use VoiceOver/TalkBack/browser accessibility tree; verify media identity/status, selected tabs, progress, form errors, counts, and icon actions have nonduplicative labels.
- [ ] `UX-007` · P1 · UI — Test 200% text scale, device bold text, long English/Unicode values, RTL/bidi input, and small screens; verify no clipped critical copy/actions or spoofed labels.
- [ ] `UX-013` · P1 · UI — Verify browser direct refresh and offline/cache recovery for every SPA route, then upgrade server assets and confirm the old cached app cannot become permanently unusable.
- [ ] `UX-014` · P1 · UI — Verify native/web deep links with encoded paths, cold start, auth required, invalid record, and already-open destination; `cantinarr://passkeys` navigates to passkey creation.
- [ ] `PERF-001` · P1 · API/UI — Load production-scale libraries, 10k history rows, 1k queue rows, 500 users/devices, and long issue transcripts; verify bounded pagination/memory and responsive interaction.
- [ ] `PERF-005` · P2 · UI — Scroll long image-heavy grids/lists on representative low-end iOS/Android and web hardware; verify stable frame rate, bounded cache, and no progressive memory leak.

## Release and store operations

- [ ] `REL-004` · P1 · CHAOS — Fail/slow GitHub update lookup; verify cached best-effort behavior never blocks server/app and honors the disable env var.
- [ ] `REL-007` · P0 · LIVE — Pull the published GHCR image/tag on clean amd64 and arm64 hosts; verify `latest`, version tags, startup, health, embedded app, persistence, and bundled notices.
- [ ] `REL-008` · P0 · LIVE — Exercise documented upgrade with a production-like `/config`, verify data and rollback prerequisites, and ensure the guide contains no destructive/incorrect command.
- [ ] `REL-009` · P1 · LIVE — Run TestFlight workflow for an iOS-relevant change; verify signed build installs, entitlements/passkeys/push/deep links work, and excluded paths do not trigger unintended builds.
- [ ] `REL-010` · P1 · LIVE — Run Play beta workflow for Android-relevant changes; verify signed AAB, package/version, passkeys/deep links, build-only PR behavior, and no upload without service-account secret.
- [ ] `REL-011` · P1 · API — Verify store-listing-only changes use the listing workflow, copy/assets land in intended storefronts, and do not trigger irrelevant native builds.
- [ ] `REL-017` · P1 · UI — Build `app/test/preview/screenshot_main.dart`, run `app/tool/screenshots/shoot.js`, and verify every required store image has deterministic populated data, documented dimensions/fastlane placement, no live credentials, and no clipped UI.

## Exploratory and compatibility pass

- [ ] `EXP-001` · P2 · UI — Run a 60-minute unscripted requester session across discover/search/request/status/guide/AI with network changes; record confusion, stale state, and crashes.
- [ ] `EXP-002` · P2 · UI — Run a 90-minute admin session across every module with two instances, concurrent Admin B changes, and mixed external mutations; record wrong-target or stale-control risks.
- [ ] `EXP-003` · P2 · LIVE — Repeat the highest-risk integration flows against the oldest and newest supported upstream Plex/arr/download/Tautulli versions.
- [ ] `EXP-004` · P2 · UI — Run Chrome, Safari, and supported mobile web plus current iOS/Android release builds with slow 3G/high latency and intermittent VPN.
- [ ] `EXP-005` · P2 · SEC — Perform a focused abuse pass as a curious household requester using browser devtools/direct API calls, guessed IDs, and prompt injection; record any information or mutation beyond role.
