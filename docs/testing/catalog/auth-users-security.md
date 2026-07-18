# Authentication, navigation, and security

Real credential ceremonies, deep links dispatched by the OS, the Maestro-backed smoke journeys, app lifecycle, and privacy audits. Authorization matrices, token semantics, and validation vectors are proven by the hermetic suites.

Use the [run template](../run-template.md) to record executions of these cases.

## Authentication ceremonies and device journeys

- [ ] `AUTH-007` · P0 · AUTO/UI — Log in with an enabled password using correct and incorrect credentials; verify only the correct password creates a session and errors do not reveal whether another username exists.
- [ ] `AUTH-011` · P0 · LIVE/UI — Register and use a native passkey on an associated HTTPS iOS deployment; verify creation, login, listing, naming, and deletion.
- [ ] `AUTH-012` · P0 · LIVE/UI — Repeat passkey registration/login on Android with matching `assetlinks.json`; verify the native credential provider accepts the RP.
- [ ] `AUTH-013` · P1 · LIVE/UI — Register and use a browser passkey on supported desktop/web; verify origin/RP validation and browser fallback.
- [ ] `AUTH-022` · P0 · SEC — As a requester, directly navigate/call every admin root (`/radarr`, `/sonarr`, `/chaptarr`, `/downloads`, `/tautulli`, `/approvals`, admin issues/actions/runs, setup, privileged settings); verify UI redirects and APIs return 403.
- [ ] `AUTH-036` · P0 · UI — Redeem equivalent valid `cantinarr://connect` links by native deep link and paste; verify identical normalized server/account/device outcome, one token consumption, and an already-authenticated app does not switch accounts silently.

## Navigation smoke and app lifecycle

- [ ] `NAV-001` · P0 · UI — On compact and wide layouts, open every drawer/sidebar destination and every bottom-tab branch; verify selected state, back behavior, and preserved per-module tab stacks.
- [ ] `NAV-003` · P0 · UI — As a requester, verify admin destinations are absent; as admin, verify destinations appear only when their service/attention conditions apply.
- [ ] `NAV-018` · P1 · UI — Background/foreground the native app on each major screen; verify data refreshes where required and unsaved destructive dialogs never execute themselves.

## Privacy audits

- [ ] `SEC-019` · P1 · SEC — Verify backups/support bundles/crash reports and app local storage contain only expected tokens/state and documented protection; no provider/Plex/upstream secrets on device.
- [ ] `SEC-020` · P1 · SEC — Compare actual network destinations/storage with privacy policy; verify no undocumented telemetry or client-side third-party API keys.
