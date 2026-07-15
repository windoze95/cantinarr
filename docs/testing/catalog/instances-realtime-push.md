# Instances, realtime behavior, and push

External service configuration, defaults, webhooks, cache freshness, WebSocket behavior, and push delivery.

Use the [run template](../run-template.md) to record executions of these cases.

## Credentials, instances, defaults, and webhooks

- [ ] `INST-001` · P0 · UI/LIVE — Create and test one valid instance of each supported type: Radarr, Sonarr, Chaptarr, SABnzbd, qBittorrent, NZBGet, Transmission, and Tautulli; verify type-specific credentials and capabilities work.
- [ ] `INST-002` · P0 · UI/CHAOS — Repeat creation with bad URL, bad credentials, wrong service type, TLS failure, timeout, and unreachable host; verify no unusable instance is silently reported healthy and secrets are absent from errors.
- [ ] `INST-003` · P0 · UI — Edit name, URL, credential, and type-appropriate fields; verify a successful retest updates the correct instance and dependent screens use the new client.
- [ ] `INST-004` · P0 · SEC — Fetch/list/edit instances and inspect app/server logs; verify API keys/passwords are write-only or redacted and never returned through models, proxy errors, URLs, or logs.
- [ ] `INST-005` · P0 · UI — Delete each instance type with confirmation; verify it disappears from navigation/providers and unrelated instances/defaults remain intact.
- [ ] `INST-006` · P1 · CHAOS — Delete an instance referenced by user defaults, an issue, or active UI state; verify foreign-reference behavior is safe, historical audit remains intelligible, and no other instance is substituted silently.
- [ ] `INST-007` · P0 · UI — With two same-type instances, mark the second global default and accept takeover; verify exactly one default remains and all unpinned users switch.
- [ ] `INST-008` · P1 · UI — Cancel global-default takeover; verify neither default flag changes.
- [ ] `INST-009` · P0 · UI — Pin different Radarr/Sonarr defaults per user; verify discovery availability, requests, live rows, details, and AI request tools use that user's pinned instance.
- [ ] `INST-010` · P0 · UI — Grant a user one Chaptarr instance; verify it grants Books access and all book search/library/request operations stay on that exact instance.
- [ ] `INST-011` · P0 · UI — Reassign a Chaptarr user to a sibling instance, confirm the warning, and verify the old assignment is removed; cancel once and verify it remains unchanged.
- [ ] `INST-012` · P1 · UI — Bulk-assign an exact set of users from an instance-centric screen; verify additions/removals are exact and do not modify other service types.
- [ ] `INST-013` · P0 · LIVE — Configure instant updates on Radarr and Sonarr; verify Cantinarr rotates a server-only credential, upserts one managed Connect webhook, and the app never receives the secret.
- [ ] `INST-014` · P0 · LIVE — Trigger Grab, Download/import, Add, library Delete, and file Delete for Radarr/Sonarr; verify exact instance/media invalidation and notification. Send Rename and verify 200 acknowledgement with no broadcast, push, or immediate mutation.
- [ ] `INST-015` · P0 · SEC — Call a webhook with missing/wrong Basic auth, query-string credentials, wrong instance, and encoded/malformed paths; verify rejection and no event mutation.
- [ ] `INST-016` · P1 · LIVE — Reconfigure the same webhook repeatedly; verify one managed webhook is updated, credentials rotate safely, and the previous credential stops working.
- [ ] `INST-017` · P1 · CHAOS — Make arr accept webhook creation but lose the response; retry and verify no duplicate unmanaged hooks or secret disclosure.
- [ ] `INST-018` · P0 · SEC — Proxy JSON containing nested API keys, authorization/cookie fields, URL userinfo, and secret query parameters; verify all are recursively scrubbed.
- [ ] `INST-019` · P0 · SEC — Return malformed, encoded, and oversized JSON from an arr proxy; verify it fails closed rather than forwarding unsanitized content.
- [ ] `INST-020` · P1 · LIVE — Verify TMDB, Trakt, and AI shared credentials save only after their connection/validation contract succeeds; failed replacement leaves the previously working value active.
- [ ] `INST-021` · P1 · UI — Delete each shared credential; verify its feature becomes unavailable with actionable UI while unrelated providers remain configured.
- [ ] `INST-022` · P1 · UI — Configure and clear the optional Trakt credential; verify enhanced rows/fallback disappear gracefully and TMDB discovery continues.
- [ ] `INST-023` · P1 · API — Change a credential while clients are cached; verify the next request uses the new value and the old secret is not retained as active behavior.
- [ ] `INST-024` · P2 · UI — Use names/URLs containing Unicode, spaces, long-but-valid values, and trailing slashes; verify canonical requests and labels without duplicate slashes or broken layout.

## Managed arr webhooks, WebSocket, caching, and freshness

- [ ] `RT-001` · P0 · LIVE — Verify managed webhook creation uses trusted `CANTINARR_PUBLIC_URL` behind a proxy and never derives a callback from hostile Forwarded/Host headers.
- [ ] `RT-002` · P0 · LIVE — Reconfigure/upsert concurrently; verify one managed hook, current/pending credential rotation window, and eventual rejection of the old credential.
- [ ] `RT-003` · P1 · LIVE — Upgrade a legacy callback-path webhook; verify migration to the current authenticated endpoint without duplicate notifications.
- [ ] `RT-004` · P0 · CHAOS — Fail webhook rotation before/after remote upsert; verify the last working credential remains usable or recovery is explicit—never an unknowable silent break.
- [ ] `RT-005` · P0 · API — Send webhook Test/unknown events; verify 200 acknowledgement with no user-visible mutation.
- [ ] `RT-006` · P0 · LIVE — Send Radarr movie and Sonarr series add/delete/file-delete/grab events; verify only exact instance/media caches/statuses invalidate.
- [ ] `RT-007` · P0 · LIVE — Import a movie and several episodes; verify availability/events and new-content notification once despite webhook + poll overlap.
- [ ] `RT-008` · P0 · SEC — Send missing/wrong/query-string credentials, wrong instance type, path traversal/encoding, huge body, and malformed JSON; verify safe rejection and no access-log secret/query leakage.
- [ ] `RT-009` · P0 · API — Connect WebSocket with valid/invalid/expired/revoked subprotocol auth; verify only the valid current user receives a connection.
- [ ] `RT-010` · P0 · SEC — Produce events for two users pinned to different instances plus admin-only events; verify each socket receives only authorized/relevant data.
- [ ] `RT-011` · P0 · CHAOS — Drop network, restart server, rotate tokens, sleep/wake device, and reconnect; verify one subscription and refetch/backfill repairs missed state.
- [ ] `RT-012` · P0 · API — Replay a valid authenticated webhook and deliver duplicate/out-of-order events; verify safe acknowledgement/idempotence, newer state never regresses, and duplicate content notification/action is suppressed.
- [ ] `RT-013` · P1 · API/UI — Verify REST `partial` and any realtime variant map to one Partially Available state, including older/newer payload compatibility.
- [ ] `RT-014` · P1 · CHAOS — Send malformed/version-skew/unknown events; verify ignored/generic-safe handling and polling repairs state without a crash.
- [ ] `RT-015` · P0 · LIVE — Make direct arr changes with no webhook; verify short polling/refetch paths eventually show truth and no permanent stored snapshot wins.
- [ ] `RT-016` · P0 · API — Verify book library digest TTL/invalidation after add/import/delete and strict user+Chaptarr-instance cache keys.
- [ ] `RT-017` · P1 · UI — Revoke the current device, demote the actor, or dispose the screen during an in-flight refresh/subscription; verify stale completion cannot repopulate private state after auth/scope loss.
- [ ] `RT-018` · P1 · CHAOS — Fail a silent refresh while old data exists; verify it is retained with appropriate freshness/error feedback and never represented as newly verified.

## Push gateway, preferences, delivery, and deep links

- [ ] `PUSH-001` · P0 · API — Start with explicit gateway key; verify no enrollment call and successful authenticated send.
- [ ] `PUSH-002` · P0 · API/SEC — Start with gateway URL and no key; verify one auto-enrollment, encrypted persisted key, restart reuse, and no key in logs/responses.
- [ ] `PUSH-003` · P1 · CHAOS — Keep gateway down at boot then restore it; verify periodic self-healing enrollment and stored device-token reconciliation without restart.
- [ ] `PUSH-004` · P0 · LIVE — Register, rotate, and delete APNs tokens for one/multiple devices; verify tokens bind to authenticated device/user and another user cannot alter them.
- [ ] `PUSH-005` · P1 · LIVE — Return a dead-token result; verify that token is pruned while healthy tokens and unrelated devices remain.
- [ ] `PUSH-006` · P0 · LIVE/UI — On iOS, cover not-determined, allowed, denied, settings redirect, and return-to-app; verify permission controls and token registration reflect actual OS state.
- [ ] `PUSH-007` · P1 · UI — On web/Android, verify unsupported iOS permission controls are hidden/disabled without breaking account preference storage.
- [ ] `PUSH-008` · P0 · UI/API — On a user with no preference row, verify defaults: request decisions off; request pending/new content/issues/actions/Plex categories on as documented and admin scoping still enforced.
- [ ] `PUSH-009` · P0 · UI/API — Toggle every category independently, restart, and force save failure; verify persistence or optimistic rollback without changing other toggles.
- [ ] `PUSH-010` · P0 · SEC — Forge admin-category preference as requester; verify SQL recipient selection still limits pending requests/issues/actions/Plex access requests to admins.
- [ ] `PUSH-011` · P0 · LIVE — Submit a pending request; verify opted-in admins only, queue-depth badge, fixed body, and approval deep link.
- [ ] `PUSH-012` · P0 · LIVE — Approve/deny a request; verify only its opted-in requester receives correct media identity and detail deep link.
- [ ] `PUSH-013` · P0 · LIVE — Import movie and multiple episodes; verify opted-in audiences, correct movie/series copy, title collapse keys, and no duplicate from poll/webhook overlap.
- [ ] `PUSH-014` · P0 · LIVE — Promote an issue to actionable and create an agent action; verify admin pushes/deep links, and verify passive observing/recovering produces neither.
- [ ] `PUSH-015` · P1 · LIVE — Trigger remediation circuit breaker; verify opted-in admins receive the settings deep link once with no model/user-supplied text.
- [ ] `PUSH-016` · P0 · LIVE — Execute all Plex notification cases in `PLEX-050`–`PLEX-052` across multiple devices and verify exact preference separation.
- [ ] `PUSH-017` · P0 · LIVE/UI — Tap every notification type from foreground, background, and terminated app; verify exact detail/approval/issue/action/users/Plex/settings destination and no duplicate navigation.
- [ ] `PUSH-018` · P1 · UI — Tap malformed payloads (missing/bad IDs, unknown type, wrong media type); verify safe no-op/fallback without opening an unrelated record.
- [ ] `PUSH-019` · P0 · SEC — Put attacker-controlled text in usernames, titles, reports, agent output, and emails; verify lock-screen bodies remain server-authored templates and contain no secrets.
- [ ] `PUSH-020` · P1 · LIVE — Fail one recipient among many; verify other sends complete and result/dead-token handling is per device.
- [ ] `PUSH-021` · P1 · API — Verify 10-minute in-process dedupe suppresses duplicate source paths but a genuinely later event after the window sends again.
- [ ] `PUSH-022` · P1 · CHAOS — Restart during fire-and-forget delivery or gateway timeout; verify app state remains correct, server does not block, and no false durable-delivery claim is shown.
- [ ] `PUSH-023` · P1 · LIVE — Use self-test from preferences and admin per-user diagnostics; verify delivered/no-token/not-configured/partial/failure results and no test notification changes product badges.
