# Build, operations, usability, and release

Real-install and upgrade truth, destructive operational failures, product-wide accessibility/compatibility/performance audits, release and store pipelines, and exploratory coverage. Build gates and contract checks live in the hermetic Go/Flutter suites and CI, not here.

Use the [run template](../run-template.md) to record executions of these cases.

## Install, upgrade, persistence, and operations

- [ ] `OPS-001` · P0 · UI/API — Start with an empty `/config`; complete first-run setup and verify one admin, a writable WAL database, and a generated encryption key are created.
- [ ] `OPS-005` · P0 · CHAOS — Rotate `CANTINARR_JWT_SECRET`; verify permanent device sessions refresh successfully and nobody is signed out solely because of rotation.
- [ ] `OPS-006` · P0 · SEC — Remove or replace the encryption key while retaining the DB; verify encrypted values fail closed with actionable server diagnostics and are never exposed as ciphertext/plaintext to clients.
- [ ] `OPS-007` · P0 · API — Upgrade a copy of the oldest supported populated database to the candidate; verify all in-code migrations run once and existing accounts, grants, requests, and notification defaults remain correct, and that a pre-instance Plex link (settings-table token, selected server, invited users) boots into a Plex instance with those users granted and their shares recorded, once.
- [ ] `OPS-009` · P1 · API — Restore a DB plus its matching encryption key into a fresh container; verify all integrations and sessions recover.
- [ ] `OPS-011` · P1 · CHAOS — Fill the config filesystem or make it read-only during a settings write; verify the write fails visibly, prior state remains usable, and no partial secret/config is reported saved.
- [ ] `OPS-012` · P1 · CHAOS — Stop each upstream service while Cantinarr runs; verify the affected screen shows a retryable error while unrelated modules remain usable.
- [ ] `OPS-013` · P1 · UI — Change server public URL/proxy origin within supported configuration; verify connect links, webhook URLs, WebSocket, passkeys, and embedded SPA routing use the correct trusted origin.
- [ ] `OPS-014` · P1 · API — Launch two processes against unsupported shared state/runtime arrangements; verify unsafe Codex runtime ownership or persistent filesystem use is rejected rather than silently shared.
- [ ] `OPS-015` · P2 · LIVE — Set an outbound proxy (a VPN-tunnelled Privoxy/3proxy) under Settings > Outbound Proxy; verify TMDB, Trakt, hosted AI, plex.tv, update-check, and push-relay traffic exits via the proxy (its access log) while arr, download-client, Jellyfin/Emby, and Local AI calls stay direct; with the Local (OpenAI-compatible) provider selected, verify its endpoint stays direct by default and leaves via the proxy once **Route through the outbound proxy** is on, and that saving the switch runs its own validation turn over the new route; verify Test reports a wrong port and a wrong password with the server's reason; verify `HTTP_PROXY` alone gives the same split with no `NO_PROXY`.

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
- [ ] `REL-007` · P0 · LIVE — Pull the candidate by digest on clean amd64 and arm64 hosts; verify startup, health, embedded app, persistence, and bundled notices. After stable promotion verify `X.Y.Z`, `X.Y`, and `latest` resolve to that exact multi-arch digest, main updates only `edge`, and release tarballs contain the binaries from that image.
- [ ] `REL-008` · P0 · LIVE — Exercise documented upgrade with a production-like `/config`, verify data and rollback prerequisites, and ensure the guide contains no destructive/incorrect command.
- [ ] `REL-009` · P1 · LIVE — Run TestFlight workflow for an iOS-relevant change; verify signed build installs, entitlements/passkeys/push/deep links work, and excluded paths do not trigger unintended builds.
- [ ] `REL-010` · P1 · LIVE — Run Play beta workflow for Android-relevant changes; verify the same version code and release notes reach Open testing (`beta`) and Closed testing (`alpha`) from one AAB upload, and both reviewed releases are available in the configured countries. Verify an existing closed tester receives the update without changing enrollment and a Google account outside the closed-test email lists can join through the README opt-in link and install it. Verify signed AAB, package/version, passkeys/deep links, explicit single-track `beta`/`alpha`/`internal` dispatch, `draft` stays draft on both tracks, build-only PR behavior, and no upload without service-account secret.
- [ ] `REL-011` · P1 · API — Verify store-listing-only changes use the listing workflow, copy/assets land in intended storefronts, and do not trigger irrelevant native builds. During a candidate freeze, main listing updates pause and release-branch metadata supplies the selected submission.
- [ ] `REL-017` · P1 · UI — Build `app/test/preview/screenshot_main.dart`, run `app/tool/screenshots/shoot.js`, and verify every required store image has deterministic populated data, documented dimensions/fastlane placement, no live credentials, and no clipped UI.
- [ ] `REL-018` · P0 · LIVE — Freeze one release branch while main advances; verify public TestFlight and both Play beta tracks receive only candidate builds, existing public links/enrollments keep working, and the owner receives the same candidate on Android internal. Install the recorded iOS/Android builds against the candidate server and supported older counterparts; record actual device/upgrade/compatibility results in the combined candidate manifest. After deleting the release branch, dispatch main and verify public betas resume.
- [ ] `REL-019` · P0 · LIVE — Dispatch a reviewed PR phone preview. Verify its exact merge SHA matches CI and the server preview, tester notes identify the PR, and only the owner's internal audiences receive it. In App Store Connect verify Internal Only and no external/production eligibility; in Play verify only internal changes, all other tester lists are unselected there, and public tracks retain their prior builds. Install on both phones, test the changed feature, then verify return to a main/candidate build.
- [ ] `REL-020` · P0 · LIVE — Submit the selected candidate iOS run with a newer preview present; verify App Store Connect selects the recorded version/build, stays at manual release after review, and uses candidate listing metadata. Promote the selected Android run and verify its exact version code reaches production, including any Play review/managed-publishing step. Confirm actual store install/update availability before a server release that raises the app compatibility floor; archive the combined record with the release.

## Exploratory and compatibility pass

- [ ] `EXP-001` · P2 · UI — Run a 60-minute unscripted requester session across discover/search/request/status/guide/AI with network changes; record confusion, stale state, and crashes.
- [ ] `EXP-002` · P2 · UI — Run a 90-minute admin session across every module with two instances, concurrent Admin B changes, and mixed external mutations; record wrong-target or stale-control risks.
- [ ] `EXP-003` · P2 · LIVE — Repeat the highest-risk integration flows against the oldest and newest supported upstream Plex/arr/download/Tautulli/Tracearr versions.
- [ ] `EXP-004` · P2 · UI — Run Chrome, Safari, and supported mobile web plus current iOS/Android release builds with slow 3G/high latency and intermittent VPN.
- [ ] `EXP-005` · P2 · SEC — Perform a focused abuse pass as a curious household requester using browser devtools/direct API calls, guessed IDs, and prompt injection; record any information or mutation beyond role.
