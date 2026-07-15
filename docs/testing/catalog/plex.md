# Plex linking, libraries, and invitations

Plex ownership, server selection, explicit and all-library scopes, current and future libraries, invites, lifecycle truth, errors, and races.

These are canonical, run-neutral definitions. Keep every active case unchecked; record executions only in a copy assembled with the [run template](../run-template.md).

## Plex onboarding, linking, library sharing, and invitations

These are real Plex end-to-end tests. “Invite sent” is not proven by Cantinarr's timestamp or toast. For every share-scope case, capture the exact outgoing/stored global section IDs, Plex Manage Library Access, and a recipient view of each uniquely named marker item. Accept the initial invite before future-library checks, remove shares before reusing recipients, and distinguish pending-invite 422 behavior from accepted-share 422 behavior.

- [ ] `PLEX-001` · P0 · SEC/UI — As a requester, direct-call/open all admin Plex endpoints/settings; verify 403/redirect and no account/server/library metadata leaks.
- [ ] `PLEX-002` · P0 · UI/LIVE — From unlinked state, start the PIN flow; verify the external URL is exactly Plex's trusted app origin, carries the stable client ID/code, and no Plex token enters the app URL or logs.
- [ ] `PLEX-003` · P1 · UI/LIVE — Leave a PIN unapproved; verify automatic polling remains in waiting state, “check now” gives appropriate guidance, and no linked state/settings are created.
- [ ] `PLEX-004` · P1 · UI/LIVE — Reopen the approval page from the waiting state and approve it; verify the same flow links once and polling stops.
- [ ] `PLEX-005` · P1 · UI/LIVE — Cancel an in-progress PIN flow; verify local polling/buttons reset, a later link starts cleanly, and cancellation alone does not unlink an already linked account.
- [ ] `PLEX-006` · P1 · CHAOS — Let the Plex PIN expire or return an invalid PIN; verify a retryable error and no token/account persistence.
- [ ] `PLEX-007` · P0 · LIVE — Approve a valid PIN; verify the linked Plex username appears, the server verifies the token, and refresh/restart preserves linked state.
- [ ] `PLEX-008` · P0 · SEC — Inspect `/api/admin/plex/status`, other API responses, DB, client/server logs, network inspector, and crash output; verify the Plex token is only encrypted at rest and never returned/logged.
- [ ] `PLEX-009` · P1 · CHAOS — Make Plex token verification fail after PIN approval; verify Cantinarr does not store the token or claim the account linked.
- [ ] `PLEX-010` · P0 · LIVE — List resources for an account with owned servers, a shared server, and player/client resources; verify only owned Plex Media Servers appear.
- [ ] `PLEX-011` · P1 · UI — Link an account with no owned server; verify “No owned servers found,” no configuration can be saved, and the manual explanation is accurate.
- [ ] `PLEX-012` · P0 · UI/LIVE — Select each of two owned servers; verify the matching machine identifier/name are saved and only that server receives later shares.
- [ ] `PLEX-013` · P0 · UI — Switch servers before saving; verify the old server's selected library IDs are cleared and the new server's libraries load before selection.
- [ ] `PLEX-014` · P1 · CHAOS — Fail the server-list request; verify linked state remains, a retry message appears, and no phantom server or destructive reset is saved.
- [ ] `PLEX-015` · P0 · LIVE — Load movie, show, music, and photo libraries; verify title/type and Plex-global section IDs map to the correct server sections, not local section keys.
- [ ] `PLEX-016` · P1 · CHAOS — Fail or return malformed XML for a library-list request; verify a retryable error, no fake checked libraries, and Save cannot silently broaden an existing subset.
- [ ] `PLEX-017` · P0 · UI/LIVE — Check one library, save, reload/restart, and invite a new Plex account; verify exactly that library is shared and every unchecked current library is absent.
- [ ] `PLEX-018` · P0 · UI/LIVE — Check several non-adjacent libraries, save, reload, and invite; verify exactly those global section IDs are shared regardless of list order.
- [ ] `PLEX-019` · P0 · UI/LIVE — Check then uncheck every library and save; verify the API persists `library_section_ids: []` and the UI reloads with all boxes unchecked—not with a stale subset.
- [ ] `PLEX-020` · P0 · UI/LIVE — With all libraries unchecked, invite and accept from a brand-new account; verify that account can open every current library on the selected server.
- [ ] `PLEX-021` · P0 · LIVE — Keep “all libraries” (`[]`) saved, add a new Plex library, then invite and accept from another new account without resaving Cantinarr; verify the new library is included automatically.
- [ ] `PLEX-022` · P0 · LIVE — Invite and accept while “all libraries” (`[]`) is saved, then add a new Plex library after acceptance; verify the already-shared account gains that future library without a new invite.
- [ ] `PLEX-023` · P0 · LIVE — Save an explicit subset, invite and accept, add a new Plex library, then test the existing share and another new invite; verify the new library is not silently included and the explicit subset remains exact.
- [ ] `PLEX-024` · P1 · LIVE — Rename a selected Plex library; verify its stable section ID remains selected/shared and the updated title appears after reload.
- [ ] `PLEX-025` · P1 · LIVE/CHAOS — Delete a selected Plex library; verify Cantinarr never converts the stale explicit selection into “all,” and any invite/save error is visible and repairable.
- [ ] `PLEX-026` · P1 · UI — Toggle auto-invite without pressing Save, leave/reload, and verify persisted behavior did not change; then Save and verify the exact toggle survives restart.
- [ ] `PLEX-027` · P0 · UI — Attempt Save with no server; verify “Pick a server first,” no configured status, and no auto-invite activation.
- [ ] `PLEX-028` · P1 · CHAOS — Fail settings persistence; verify Save reports failure, re-enables controls, and the previous server/library/auto-invite configuration remains authoritative.
- [ ] `PLEX-029` · P0 · UI/API — With Plex unlinked or linked-but-no-server, share a valid email as a user; verify no automatic invite occurs, admins are notified of a manual waiter, and the Users tag/drawer count appear.
- [ ] `PLEX-030` · P0 · UI/LIVE — With auto-invite off and valid configuration, share a new Plex email; verify no Plex share occurs until an admin explicitly chooses Send Plex invite.
- [ ] `PLEX-031` · P0 · UI/LIVE — Send a one-tap invite; verify selected server/libraries and exact trimmed email at Plex, `plex_invited_at` is stamped, Users reload clears the initiating admin's waiter, and a fresh `/me`/reopened guide shows sent.
- [ ] `PLEX-032` · P0 · UI/LIVE — With auto-invite on, share a new Plex email; verify one invite without admin action, settled `/me` has a stamp, a refreshed/reopened guide shows sent, and no backend waiter remains.
- [ ] `PLEX-033` · P0 · LIVE — Repeat auto-invite with all libraries unchecked; verify the external Plex share receives all current libraries and retains the future-library behavior in `PLEX-021`/`PLEX-022`.
- [ ] `PLEX-034` · P0 · LIVE — Repeat auto-invite with an explicit subset; verify only the chosen section IDs are shared.
- [ ] `PLEX-035` · P0 · UI/API — Submit an email with leading/trailing whitespace; verify it is trimmed once in storage, display, admin state, and the Plex invite payload.
- [ ] `PLEX-036` · P1 · UI/API — Submit empty, whitespace, no-`@`, missing local/domain, internal whitespace/tab/newline, and over-254-byte values; verify client/server reject each and no email/stamp/notification/invite changes.
- [ ] `PLEX-037` · P1 · UI/API — Require UI/server to accept `first.last+tag@sub.example`, `a@b`, and a valid exactly-254-ASCII-byte address; reject multiple `@`, internal `\r`/Unicode whitespace, and any UTF-8 address over 254 bytes with zero state/side effects.
- [ ] `PLEX-038` · P0 · API/LIVE — Resubmit the identical stored email after an invite; verify no new admin notification, no auto-invite, and the existing `plex_invited_at` remains set locally and after profile refresh.
- [ ] `PLEX-039` · P0 · UI/LIVE — Change an invited user's email; verify the old invite stamp clears immediately, waiting state returns, admins are notified, and manual/auto invite targets only the new address.
- [ ] `PLEX-040` · P1 · CHAOS — Change the email rapidly twice while auto-invite is running; verify final user state corresponds to the last address, no invite is sent to an unintended stale address, and admin badge/state settles correctly.
- [ ] `PLEX-041` · P0 · LIVE — Invite an email/account that already has server access; verify Plex 422 maps to `already_shared`, the user is stamped, no misleading “check your inbox” push is sent, and the UI says it already has access.
- [ ] `PLEX-042` · P1 · UI/LIVE — Resend while the original invite is still pending; verify Plex's exact response and that Cantinarr never labels the recipient “already has access,” emits no false fresh-email push, and preserves the pending external scope.
- [ ] `PLEX-043` · P0 · CHAOS — Separately inject failures known to occur before Plex commits (connection refusal/TLS, 401/403/429/5xx, and malformed non-2xx error response); verify no stamp/requester success, auto-invite emits failed admin copy, and manual invite shows only its local failure/retry UI.
- [ ] `PLEX-044` · P1 · CHAOS — Expire/revoke the linked Plex token after configuration; verify listing/inviting fails visibly, token/status is not exposed, and no user is stamped until relink + successful retry.
- [ ] `PLEX-045` · P0 · UI — Without configured one-tap invites, choose “Invite in Plex…” for a waiting user; verify the exact email reaches the clipboard and the trusted Plex Manage Library Access page opens.
- [ ] `PLEX-046` · P1 · CHAOS — Deny clipboard or external-browser launch where the platform permits; verify the app remains stable and gives enough information to complete/retry manually.
- [ ] `PLEX-047` · P0 · UI — Verify users with no Plex email have no send/manual invite action or Plex state tag; waiting users show email + “Needs Plex invite”; stamped users show “Plex invite sent” + Resend.
- [ ] `PLEX-048` · P0 · UI — With waiters 0, 1, and several, verify exact drawer visibility/count and Users destination; email/auto-invite events refresh all admins, while Admin A's manual invite explicitly reloads and clears Admin A without negative/duplicate counts.
- [ ] `PLEX-049` · P1 · CHAOS — Drop the WebSocket during email/invite changes; reconnect or manually refresh and verify the waiter count and Users state converge to backend truth without negative/duplicate counts.
- [ ] `PLEX-050` · P0 · LIVE — With admin Plex-access-request push enabled, test manual-waiting, auto-sent, and auto-failed states; verify fixed privacy-safe body text, correct deep link to Users, per-user collapse ID, and no username/email on the lock screen.
- [ ] `PLEX-051` · P0 · LIVE — With requester push enabled, use two clean recipient accounts for one fresh manual and one fresh auto invite; verify exactly one push per account, Watch on Plex deep link, sent state after refresh, and no push for duplicate/already-shared.
- [ ] `PLEX-052` · P1 · LIVE — Disable each Plex notification preference independently; verify the disabled push is suppressed while WebSocket/UI state and the other category still work.
- [ ] `PLEX-053` · P0 · UI — In the Watch on Plex guide, test no-email, waiting, and sent states across two devices; verify exact email/change action/copy and that entry or explicit profile refresh reads backend truth. Live update while already open is tested separately.
- [ ] `PLEX-054` · P1 · UI — Hide/re-enable the guide before and after sharing an email; verify preference changes navigation only and never clears email/invite state.
- [ ] `PLEX-055` · P0 · UI/LIVE — Unlink and cancel once, then confirm; verify token/account/server/library/auto-invite settings are forgotten, existing Plex shares remain intact, and Users falls back to manual invite actions.
- [ ] `PLEX-056` · P0 · LIVE/API — Relink after unlink; verify the stable `X-Plex-Client-Identifier` is reused while the old token/config are not, and a server must be selected again.
- [ ] `PLEX-057` · P1 · LIVE — Share account A, change settings, then invite fresh account B; verify B gets the new scope while A retains the old external scope. Resend A must not claim `already_shared` updated its permissions; existing shares require manual Plex changes until update-share exists.
- [ ] `PLEX-058` · P1 · CHAOS — Trigger two admins to invite the same waiter concurrently; verify Plex duplicate handling leaves one coherent stamped state, clears the waiter once, and emits at most one truthful fresh-invite user notification.
- [ ] `PLEX-059` · P1 · API — With valid Plex configuration and an upstream call counter: nonnumeric ID → 400, non-positive ID → 400, absent/deleted user → 404, and existing user with no email → 409; verify zero Plex calls/stamps/notifications.
- [ ] `PLEX-060` · P1 · SEC — Verify the stored invite timestamp is described/used only as “Cantinarr sent/recognized the invite,” never as proof the recipient accepted it or still has live Plex access.
- [ ] `PLEX-061` · P0 · LIVE — Explicitly check every current library (nonempty ID list), invite/accept, then add a future library; verify the recipient does **not** gain that new library, proving explicit-current differs from unchecked/all-future mode.
- [ ] `PLEX-062` · P0 · CHAOS/SEC — Corrupt, delete, or store `null`/malformed `plex_library_ids`; verify Cantinarr fails closed and never interprets damaged configuration as empty/all-library access.
- [ ] `PLEX-063` · P0 · CHAOS — Fail a library fetch after selecting a new server, then press Save; verify the app blocks the save or preserves a known explicit scope instead of persisting empty/all by accident.
- [ ] `PLEX-064` · P0 · CHAOS — Fail one of the server/name/library/auto-invite setting writes; verify the update is atomic and no partially mixed configuration becomes active.
- [ ] `PLEX-065` · P0 · LIVE/API — Return duplicate-share 422 and unrelated invalid-email/payload/permission 422 responses; verify only a positively identified duplicate maps to `already_shared`.
- [ ] `PLEX-066` · P0 · CHAOS — Make DB invite-timestamp persistence fail after Plex succeeds; verify the endpoint does not report durable success/send a success push while the user still counts as waiting, and recovery can reconcile safely.
- [ ] `PLEX-067` · P1 · LIVE — Enable auto-invite when users are already waiting; verify there is no retroactive sweep, zero Plex calls/stamps occur for them, and every existing waiter remains visible for manual invite until a new email change event.
- [ ] `PLEX-068` · P1 · LIVE — Configure Plex after users already shared emails; verify configuration alone makes zero invite calls/stamps and all preexisting waiters remain visible/actionable for manual invite.
- [ ] `PLEX-069` · P1 · UI/LIVE — Complete the manual “Invite in Plex…” fallback; verify Cantinarr does not falsely stamp it, and record that without external sync/reconciliation the user and badge truthfully remain waiting rather than pretending success.
- [ ] `PLEX-070` · P1 · LIVE — Observe a fresh invite before acceptance, after acceptance, after decline/expiry/cancel, and after owner revoke; verify external Plex truth manually and confirm Cantinarr never labels its send timestamp as any of those live states.
- [ ] `PLEX-071` · P1 · LIVE — Change accepted user's library permissions directly in Plex and have the recipient leave the share; verify Cantinarr does not claim live access and the test record documents the unsynchronized external state.
- [ ] `PLEX-072` · P1 · LIVE — Delete the Cantinarr user after they accept Plex access; verify local records/badges disappear but external Plex access is not claimed revoked and is cleaned up deliberately in Plex.
- [ ] `PLEX-073` · P1 · CHAOS/UI — Poll an expired PIN and a Plex response slower than the 3-second timer; verify polling terminates/does not overlap, link success renders once, and no duplicate load/snackbar occurs.
- [ ] `PLEX-074` · P1 · CHAOS/UI — Rapidly switch owned servers while library requests complete out of order; verify only the currently selected server's libraries render and can be saved.
- [ ] `PLEX-075` · P1 · API/SEC — Submit duplicate/zero/negative/stale/foreign-server section IDs and a machine/name mismatch directly; verify unowned/invalid access scopes are rejected rather than trusted from the client.
- [ ] `PLEX-076` · P1 · GAP/UI/API — After a successful manual fallback, use the required explicit truthful reconciliation/marking flow; verify it clears waiting state without claiming acceptance or changing Plex permissions. Retain as a product-gap failure until such a flow exists.
- [ ] `PLEX-077` · P0 · CHAOS/API — Configure owner A/server/subset/auto-invite, corrupt/delete only the token, then relink owner B without Unlink; verify old machine/library/auto settings are cleared or revalidated and no stale-owner invite can be sent.
- [ ] `PLEX-078` · P0 · CHAOS/LIVE — Let Plex commit a share but drop the response until Cantinarr times out; prove external access exists while local stamp/push do not, then retry and verify duplicate handling reconciles state without claiming a fresh email.
- [ ] `PLEX-079` · P0 · CHAOS/LIVE — Pause a manual invite after Cantinarr reads email A, change the user to email B, then release A's response; verify A's invite cannot stamp B as invited.
- [ ] `PLEX-080` · P0 · GAP/UI/RT — Admin A sends a one-tap invite while Admin B stays elsewhere; verify Admin B's waiter count converges without reopening Users/manual refresh. Retain as a realtime-gap failure until an admin event exists.
- [ ] `PLEX-081` · P1 · GAP/UI/RT — Keep the recipient guide open while an admin sends the invite; verify it changes to check-inbox without route reopen/arbitrary delay. Retain as a realtime-gap failure until the profile consumes the event.
- [ ] `PLEX-082` · P1 · LIVE — Invite an unregistered email, record Plex's exact response/pending state, then create/accept that account; verify Cantinarr stamps only when Plex accepted the share request and never infers acceptance.
- [ ] `PLEX-083` · P0 · API/SEC — Omit `library_section_ids` and send it as explicit `null`; verify neither silently authorizes empty/all-library access unless the request explicitly selected that documented mode.
- [ ] `PLEX-084` · P1 · GAP/UI/API — As a waiting (unstamped) user, withdraw the shared email; verify email/stamp clear, waiting/admin counts converge, no invite runs, and later auto-invite has no stale target. Retain as a product-gap failure until withdrawal is supported.
- [ ] `PLEX-085` · P0 · CHAOS/UI/API — Revoke the Plex token externally, then inspect status, setup checklist, Users actions, server list, and invite; verify no misleading operational/configured state, no stamp, and a clear relink path.
- [ ] `PLEX-086` · P1 · CHAOS — Delay auto-invite before Plex commits, let the email POST return, then restart; verify no stamp/sent copy, user remains a waiter after restart, and one manual retry sends/stamps exactly once.
- [ ] `PLEX-087` · P1 · UI/LIVE — Resend after the recipient accepted access; verify `already_shared` copy is now truthful, no new-email push is sent, and existing libraries are neither removed nor silently broadened.

### Plex vector subresults

Do not check the parent case until every applicable vector below passes. Record evidence/defect per row so a partial pass is visible.

| Parent | Vector | Expected | Result / evidence |
|---|---|---|---|
| PLEX-036 | Empty / ASCII-whitespace-only | Reject; zero state/side effects | |
| PLEX-036 | No `@` | Reject; zero state/side effects | |
| PLEX-036 | `@nothing-before` | Reject; zero state/side effects | |
| PLEX-036 | `nothing-after@` | Reject; zero state/side effects | |
| PLEX-036 | Internal space | Reject; zero state/side effects | |
| PLEX-036 | Internal tab | Reject; zero state/side effects | |
| PLEX-036 | Internal newline | Reject; zero state/side effects | |
| PLEX-036 | Valid address over 254 bytes | Reject; zero state/side effects | |
| PLEX-037 | `first.last+tag@sub.example` | Accept identically in UI/server | |
| PLEX-037 | `a@b` | Accept identically in UI/server | |
| PLEX-037 | Valid exactly 254 ASCII bytes | Accept identically in UI/server | |
| PLEX-037 | Multiple `@` | Reject identically in UI/server | |
| PLEX-037 | Internal carriage return | Reject identically in UI/server | |
| PLEX-037 | Internal Unicode whitespace | Reject identically in UI/server | |
| PLEX-037 | ≤254 characters but >254 UTF-8 bytes | Reject identically in UI/server | |
| PLEX-043 | Connection refused/DNS | Fail before commit; no stamp/success | |
| PLEX-043 | TLS verification failure | Fail before commit; no stamp/success | |
| PLEX-043 | Plex 401 | Fail before commit; no stamp/success | |
| PLEX-043 | Plex 403 | Fail before commit; no stamp/success | |
| PLEX-043 | Plex 429 | Fail before commit; no stamp/success | |
| PLEX-043 | Plex 5xx | Fail before commit; no stamp/success | |
| PLEX-043 | Malformed non-2xx error body | Fail safely; no stamp/success | |
| PLEX-059 | Nonnumeric user ID | 400; zero Plex calls | |
| PLEX-059 | User ID `0` | 400; zero Plex calls | |
| PLEX-059 | Negative user ID | 400; zero Plex calls | |
| PLEX-059 | Never-existing user | 404; zero Plex calls | |
| PLEX-059 | Deleted user | 404; zero Plex calls | |
| PLEX-059 | Existing user, no email | 409; zero Plex calls | |
| PLEX-070 | Invite pending | External pending recorded; local label only says sent | |
| PLEX-070 | Recipient accepted | External access works; no false local acceptance claim | |
| PLEX-070 | Recipient declined | External decline recorded; no false local live-state claim | |
| PLEX-070 | Invite expired | External expiry recorded; no false local live-state claim | |
| PLEX-070 | Owner canceled pending invite | External cancel recorded; no false local live-state claim | |
| PLEX-070 | Owner revoked accepted share | Access gone externally; no false local live-state claim | |
| PLEX-075 | Duplicate section IDs | Reject; no setting change | |
| PLEX-075 | Section ID `0` | Reject; no setting change | |
| PLEX-075 | Negative section ID | Reject; no setting change | |
| PLEX-075 | Deleted/stale section ID | Reject; no setting change | |
| PLEX-075 | Section ID from another server | Reject; no setting change | |
| PLEX-075 | Machine ID / display-name mismatch | Reject; no setting change | |
