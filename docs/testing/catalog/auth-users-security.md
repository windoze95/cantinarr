# Authentication, navigation, users, and security

Identity, device sessions, authorization, navigation, user administration, privacy, and failure containment.

Use the [run template](../run-template.md) to record executions of these cases.

## Authentication, accounts, devices, and authorization

- [ ] `AUTH-001` · P0 · UI/API — On a fresh server, create the first admin once; verify a second setup attempt is rejected and cannot replace the admin.
- [ ] `AUTH-002` · P0 · UI — Connect with a new-user link; verify the intended username is created, the link establishes a device session, and the user lands on the original internal destination or dashboard.
- [ ] `AUTH-003` · P0 · API — Redeem the same one-time connect token again; verify it cannot create another account/session.
- [ ] `AUTH-004` · P0 · UI — Issue another connect/device link for an existing username; verify it attaches a new device to the same user rather than creating a duplicate.
- [ ] `AUTH-005` · P0 · UI — Let a connect link expire, then issue a fresh one; verify the expired link fails clearly and the new link succeeds.
- [ ] `AUTH-006` · P0 · SEC — Try an external, scheme-relative, and malformed return target through login/connect; verify navigation only returns to an app-internal path.
- [ ] `AUTH-007` · P0 · AUTO/UI — Log in with an enabled password using correct and incorrect credentials; verify only the correct password creates a session and errors do not reveal whether another username exists.
- [ ] `AUTH-008` · P0 · API — Exceed the public login/setup/connect/passkey rate limit from one IP; verify throttling after the documented allowance and recovery after the window.
- [ ] `AUTH-009` · P0 · UI — Enable password for a user, let them set it, then disable password; verify subsequent password login fails and existing device sessions follow the documented device policy.
- [ ] `AUTH-010` · P0 · SEC — Attempt to set a password for a user whose admin toggle is off; verify both UI and direct API reject it.
- [ ] `AUTH-011` · P0 · LIVE/UI — Register and use a native passkey on an associated HTTPS iOS deployment; verify creation, login, listing, naming, and deletion.
- [ ] `AUTH-012` · P0 · LIVE/UI — Repeat passkey registration/login on Android with matching `assetlinks.json`; verify the native credential provider accepts the RP.
- [ ] `AUTH-013` · P1 · LIVE/UI — Register and use a browser passkey on supported desktop/web; verify origin/RP validation and browser fallback.
- [ ] `AUTH-014` · P0 · SEC — Attempt passkey registration/login from an untrusted origin or mismatched RP; verify it fails without creating a credential.
- [ ] `AUTH-015` · P0 · UI — Disable passkeys for a user with credentials; verify stored passkeys are revoked and can no longer authenticate.
- [ ] `AUTH-016` · P1 · UI — Remove one of multiple passkeys; verify only that credential stops working and the remaining passkey still authenticates.
- [ ] `AUTH-017` · P0 · UI/API — Refresh before/after access-token expiry and across restart; verify the same opaque `cnr1.` token is returned/reusable byte-for-byte. Redeem one legacy JWT refresh token and verify one-time migration to stable opaque form; transient 5xx preserves auth and genuine revocation returns 401.
- [ ] `AUTH-018` · P0 · SEC — Revoke the current device from another admin session; verify its app refresh, WebSocket, push registration, and MCP tokens lose access promptly.
- [ ] `AUTH-019` · P0 · UI — Delete a non-self user; verify their devices, tokens, grants, and access are removed without deleting another user's records.
- [ ] `AUTH-020` · P0 · SEC — Attempt self-deletion, self-demotion when it would violate invariants, and deletion of protected/current admin states; verify guardrails match the UI and API.
- [ ] `AUTH-021` · P0 · UI — Promote a user to admin and demote them again; verify routes/menu/permissions update without stale privileged access.
- [ ] `AUTH-022` · P0 · SEC — As a requester, directly navigate/call every admin root (`/radarr`, `/sonarr`, `/chaptarr`, `/downloads`, `/tautulli`, `/approvals`, admin issues/actions/runs, setup, privileged settings); verify UI redirects and APIs return 403.
- [ ] `AUTH-023` · P0 · SEC — As a requester, read allowed Radarr/Sonarr proxy resources but attempt writes, commands, interactive searches, config endpoints, and non-arr proxies; verify only the read-only allowlist succeeds.
- [ ] `AUTH-024` · P0 · SEC — As an issue reporter, open their own issue and another user's issue; verify only their own thread is visible/repliable while admins can access both.
- [ ] `AUTH-025` · P1 · UI — Show the Devices screen with current and other devices; verify model, last-seen, user, and “This device” are accurate and revoke confirmation targets the right ID.
- [ ] `AUTH-026` · P1 · CHAOS — Revoke a device while that device is offline, then reconnect; verify it cannot refresh or silently recreate its session.
- [ ] `AUTH-027` · P1 · API — Change the user role/grants while a long AI/tool turn is running; verify interactive dispatch reauthorizes and stops a now-unauthorized actor.
- [ ] `AUTH-028` · P1 · UI — Verify there is no ordinary logout affordance and that the documented device-revocation recovery path is usable.
- [ ] `AUTH-029` · P1 · SEC — Confirm AASA and Digital Asset Links responses contain only configured app/package identities, correct content types, and no secrets.
- [ ] `AUTH-030` · P1 · UI — On plain HTTP, verify passkey limitations are explained and an enabled password can authorize MCP login.
- [ ] `AUTH-031` · P1 · UI — During the first-login optional passkey offer, navigate/reload/skip/complete it; verify the saved internal destination is restored exactly once.
- [ ] `AUTH-032` · P1 · CHAOS — Lose VPN/network during authentication restore and recover it; verify the saved connection remains and the app retries rather than deleting local auth state.
- [ ] `AUTH-033` · P0 · UI — Load the embedded web app from root and a deep route; verify it auto-detects `Uri.base.origin`, checks that exact origin once, and never asks for/changes to an unrelated server URL.
- [ ] `AUTH-034` · P0 · UI — On native, enter host with no scheme, HTTP/HTTPS, surrounding whitespace, one/many trailing slashes, malformed scheme/host, 404, refusal, TLS failure, and timeout; verify deterministic normalization plus editable, non-secret recovery copy.
- [ ] `AUTH-035` · P0 · UI/API — Paste connect links with missing/wrong scheme, host, token, duplicate parameters, invalid encoding, whitespace, expired/reused token, and hostile extra parameters; verify safe rejection before unintended connection/session creation.
- [ ] `AUTH-036` · P0 · UI — Redeem equivalent valid `cantinarr://connect` links by native deep link and paste; verify identical normalized server/account/device outcome, one token consumption, and an already-authenticated app does not switch accounts silently.

## Navigation, shell, setup checklist, and cross-platform layout

- [ ] `NAV-001` · P0 · UI — On compact and wide layouts, open every drawer/sidebar destination and every bottom-tab branch; verify selected state, back behavior, and preserved per-module tab stacks.
- [ ] `NAV-002` · P0 · UI — Reload/deep-link every declared route; verify authenticated routes render inside the shell and malformed media/user IDs redirect or show the defensive invalid-link screen without crashing.
- [ ] `NAV-003` · P0 · UI — As a requester, verify admin destinations are absent; as admin, verify destinations appear only when their service/attention conditions apply.
- [ ] `NAV-004` · P0 · UI — Grant and revoke Chaptarr access while signed in; verify the Books dashboard tab and Chaptarr access appear/disappear and direct `/dashboard/books` access redirects when ungranted.
- [ ] `NAV-005` · P1 · UI — Toggle Approvals, Issues, and Agent fixes between pinned and conditional; verify each menu entry follows its own setting and the switches remain recoverable from Settings.
- [ ] `NAV-006` · P1 · UI — With zero, passive-tracking, and actionable issues, verify conditional Issues visibility follows active/tracking state while its numeric badge counts actionable only.
- [ ] `NAV-007` · P1 · UI — Create/clear pending approvals, agent actions, and Plex invite waiters; verify menu entries, counts, and hamburger attention dot update live without duplicate entries.
- [ ] `NAV-008` · P0 · UI — Run the setup checklist on a partially configured server; verify every item is derived from live configuration, opens the real settings route, and re-evaluates on return.
- [ ] `NAV-009` · P1 · API/UI — Inject a newer-server/unknown setup item; verify it renders generically and contributes to counts without crashing the older client.
- [ ] `NAV-010` · P1 · UI — Mute and unmute the setup drawer reminder; verify the wizard remains reachable and live counts continue updating.
- [ ] `NAV-011` · P1 · UI — Hide the Watch on Plex guide from the guide and re-enable it from Settings; verify both menu and preference persist across restart per device.
- [ ] `NAV-012` · P1 · UI — Open About and verify app/server version, links, and update guidance match the running build.
- [ ] `NAV-013` · P0 · UI — On every primary surface, use global search; verify secondary work screens hide it and local filters do not conflict with global search state.
- [ ] `NAV-014` · P1 · UI — Start a search, change module, return, clear, and rapidly type; verify debounce, cancellation, focus, and results belong to the latest query.
- [ ] `NAV-015` · P1 · UI — Submit a question-like or no-result query to AI; verify the exact prompt is handed off and starts once, while a normal result query does not force AI.
- [ ] `NAV-016` · P0 · UI — Resize/rotate at phone, tablet, desktop, and narrow-web breakpoints; verify no overflow, unreachable actions, duplicate navigation, or content hidden behind system insets.
- [ ] `NAV-017` · P1 · UI — Use browser back/forward and native back gestures across detail sheets, secondary routes, and tab branches; verify no unexpected exit or route reset.
- [ ] `NAV-018` · P1 · UI — Background/foreground the native app on each major screen; verify data refreshes where required and unsaved destructive dialogs never execute themselves.

## Users, local account invitations, and devices

- [ ] `USER-001` · P0 · UI — Render users covering admin/requester, self, 0/1/many devices, active/expired/no connect token, password/passkey on/off, included AI on/off, Plex email/stamp combinations; verify every independent tag is truthful.
- [ ] `USER-002` · P0 · UI/API — Generate/copy/reissue connect links for a new name, pending account, expired invite, connected user, and user with all devices revoked; verify intended account/device semantics and one active link behavior.
- [ ] `USER-003` · P0 · API — Redeem concurrent connect links for the same new username; verify one user identity and safe token consumption without duplicate accounts.
- [ ] `USER-004` · P0 · UI/API — Promote/demote another user and attempt to demote/delete the last admin; verify permission changes are immediate and the admin invariant holds.
- [ ] `USER-005` · P0 · UI/API — Delete a user after canceling once; verify their local sessions/devices/grants/preferences/credentials become inaccessible while other users and external Plex access are not falsely claimed removed.
- [ ] `USER-006` · P0 · UI/API — Enable/disable password and passkey methods; verify confirmation, actual credential revocation, menu tags, and recovery through a fresh connect link.
- [ ] `USER-007` · P0 · UI — Grant shared OAuth-backed included AI and cancel/accept the warning; verify cancellation changes nothing and acceptance changes only the target user.
- [ ] `USER-008` · P0 · UI — Configure each per-user request/default-instance setting, navigate away/back/restart, and verify tri-state inheritance plus pins persist exactly.
- [ ] `USER-009` · P0 · LIVE — Send per-user test push for delivered, no-token, gateway-disabled, dead-token, and upstream-error states; verify diagnostic counts/results target only that user's devices.
- [ ] `USER-010` · P1 · UI — Test Users list initial failure, retry, refresh during an action, large user count, duplicate-like names, long Unicode name/email, and concurrent edits without acting on the wrong row.
- [ ] `USER-011` · P0 · UI — Verify Cantinarr connect-link `Invited`/`Invite expired` tags remain distinct from Plex `Needs Plex invite`/`Plex invite sent` states in every cross-product.
- [ ] `USER-012` · P1 · CHAOS — Authenticate to Server A and load Users/badges/providers; revoke that current device from another admin, complete forced auth loss, then connect the app's single stored session to Server B. Verify no cached A state can repopulate B.

## Security, privacy, and failure containment

- [ ] `SEC-001` · P0 · SEC — Inventory every DB/settings secret and verify AES-256-GCM ciphertext for arr/download/Tautulli/Plex/AI/API/OAuth/push/webhook credentials, with no plaintext duplicate columns/files.
- [ ] `SEC-002` · P0 · SEC — Tamper with ciphertext/tag and use the wrong key; verify authenticated decryption fails closed and never overwrites the stored value with empty/default data.
- [ ] `SEC-003` · P0 · SEC — Inspect every credential/instance/status/config/user API; verify only safe metadata/configured booleans, never secret values or encrypted blobs.
- [ ] `SEC-004` · P0 · SEC — Verify incoming Cantinarr Authorization/Cookie/forwarding headers are stripped before upstream proxy calls and only the intended upstream auth is applied.
- [ ] `SEC-005` · P0 · SEC — Scrub nested/case-varied secret keys, arrays, headers, URL userinfo, percent-encoded and repeated query secrets across proxy, MCP, chat, and remediation.
- [ ] `SEC-006` · P0 · SEC — Feed misleading content types, malformed/encoded/deeply nested/oversized JSON and compressed bombs; verify bounded fail-closed handling without forwarding raw bodies.
- [ ] `SEC-007` · P1 · SEC — Verify legitimate non-JSON/streaming/SSE responses remain usable only on intended routes and cannot bypass the structured scrubber.
- [ ] `SEC-008` · P0 · SEC — Review request logs under success/error attacks; verify no query string, auth/cookie header, request body, dynamic secret path, user email, or upstream error body is logged.
- [ ] `SEC-009` · P0 · SEC — Make each upstream redirect to another host; verify clients carrying credentials do not follow or leak headers.
- [ ] `SEC-010` · P0 · SEC — Run the route inventory as anonymous/requester/admin with direct HTTP, not UI; verify every permission boundary and no method/path variant bypass.
- [ ] `SEC-011` · P0 · SEC — Test CORS preflight/actual calls from same origin, hostile web origin, null origin, wildcard MCP clients, and credentialed mode; verify API and MCP policies remain distinct.
- [ ] `SEC-012` · P0 · SEC — Test WebSocket origin/subprotocol/token handling and hostile forwarded headers behind proxy; verify trusted deployment works without cross-site socket access.
- [ ] `SEC-013` · P0 · SEC — Render HTML/script/Markdown/control/bidi/huge Unicode in every user/model/upstream text surface; verify passive escaped text, no executable link/control injection, and safe layout.
- [ ] `SEC-014` · P0 · SEC — Verify OAuth redirect, PKCE, state, audience/resource, authorization-code replay, refresh replay, and wrong-resource failures do not consume or expose valid tokens incorrectly.
- [ ] `SEC-015` · P1 · SEC — Verify public auth/device-flow rate limits cannot be bypassed with route variants and authenticated device-flow churn does not starve household login.
- [ ] `SEC-016` · P0 · SEC — Attempt SSRF through instance URLs, webhook public URL, AI verification URL, management portal, images, and MCP redirects; verify supported schemes/hosts/redirect rules and no cloud metadata access.
- [ ] `SEC-017` · P1 · SEC — Attempt SQL/JSON/path/command injection in IDs, filters, instance names, emails, download item IDs, release refs, and filenames; verify parameterization/escaping and no unintended mutation.
- [ ] `SEC-018` · P0 · SEC — Confirm regular-user `/api/config` reveals only effective services/grants; admin config may list instances but never credentials or webhook secrets.
- [ ] `SEC-019` · P1 · SEC — Verify backups/support bundles/crash reports and app local storage contain only expected tokens/state and documented protection; no provider/Plex/upstream secrets on device.
- [ ] `SEC-020` · P1 · SEC — Compare actual network destinations/storage with privacy policy; verify no undocumented telemetry or client-side third-party API keys.
- [ ] `SEC-021` · P1 · CHAOS — Trigger panics/errors in background Plex, push, WebSocket, cache, and remediation work; verify recovery contains the fault, preserves server health, and logs redacted context.
- [ ] `SEC-022` · P1 · API — Run fuzz/property checks for handler JSON decoders, ID/path parsing, proxy sanitizer, email validator, season scope, and notification payload conversion; verify no panic or unsafe default.
