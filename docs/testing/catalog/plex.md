# Plex linking, libraries, and invitations

Plex ownership, server selection, explicit and all-library scopes, current and future libraries, invites, and lifecycle truth. Every case here proves state on the plex.tv side that no fake can stand in for; email validation, settings persistence, and injected-failure vectors are proven by the hermetic suites.

Use the [run template](../run-template.md) to record executions of these cases.

## Plex linking, library sharing, and invitations

These are real Plex end-to-end tests. “Invite sent” is not proven by Cantinarr's timestamp or toast. For every share-scope case, capture the exact outgoing/stored global section IDs, Plex Manage Library Access, and a recipient view of each uniquely named marker item. Accept the initial invite before future-library checks, remove shares before reusing recipients, and distinguish pending-invite 422 behavior from accepted-share 422 behavior.

- [ ] `PLEX-007` · P0 · LIVE — Approve a valid PIN; verify the linked Plex username appears, the server verifies the token, and refresh/restart preserves linked state.
- [ ] `PLEX-010` · P0 · LIVE — List resources for an account with owned servers, a shared server, and player/client resources; verify only owned Plex Media Servers appear.
- [ ] `PLEX-015` · P0 · LIVE — Load movie, show, music, and photo libraries; verify title/type and Plex-global section IDs map to the correct server sections, not local section keys.
- [ ] `PLEX-018` · P0 · UI/LIVE — Check several non-adjacent libraries, save, reload, and invite; verify exactly those global section IDs are shared regardless of list order.
- [ ] `PLEX-020` · P0 · UI/LIVE — With all libraries unchecked, invite and accept from a brand-new account; verify that account can open every current library on the selected server.
- [ ] `PLEX-021` · P0 · LIVE — Keep “all libraries” (`[]`) saved, add a new Plex library, then invite and accept from another new account without resaving Cantinarr; verify the new library is included automatically.
- [ ] `PLEX-022` · P0 · LIVE — Invite and accept while “all libraries” (`[]`) is saved, then add a new Plex library after acceptance; verify the already-shared account gains that future library without a new invite.
- [ ] `PLEX-023` · P0 · LIVE — Save an explicit subset, invite and accept, add a new Plex library, then test the existing share and another new invite; verify the new library is not silently included and the explicit subset remains exact.
- [ ] `PLEX-031` · P0 · UI/LIVE — Send a one-tap invite; verify selected server/libraries and exact trimmed email at Plex, `plex_invited_at` is stamped, Users reload clears the initiating admin's waiter, and a fresh `/me`/reopened guide shows sent.
- [ ] `PLEX-032` · P0 · UI/LIVE — With auto-invite on, share a new Plex email; verify one invite without admin action, settled `/me` has a stamp, a refreshed/reopened guide shows sent, and no backend waiter remains.
- [ ] `PLEX-041` · P0 · LIVE — Invite an email/account that already has server access; verify Plex 422 maps to `already_shared`, the user is stamped, no misleading “check your inbox” push is sent, and the UI says it already has access.
- [ ] `PLEX-042` · P1 · UI/LIVE — Resend while the original invite is still pending; verify Plex's exact response and that Cantinarr never labels the recipient “already has access,” emits no false fresh-email push, and preserves the pending external scope.
- [ ] `PLEX-061` · P0 · LIVE — Explicitly check every current library (nonempty ID list), invite/accept, then add a future library; verify the recipient does **not** gain that new library, proving explicit-current differs from unchecked/all-future mode.
- [ ] `PLEX-070` · P1 · LIVE — Observe a fresh invite before acceptance, after acceptance, after decline/expiry/cancel, and after owner revoke; verify external Plex truth manually and confirm Cantinarr never labels its send timestamp as any of those live states.
- [ ] `PLEX-078` · P0 · CHAOS/LIVE — Let Plex commit a share but drop the response until Cantinarr times out; prove external access exists while local stamp/push do not, then retry and verify duplicate handling reconciles state without claiming a fresh email.
- [ ] `PLEX-082` · P1 · LIVE — Invite an unregistered email, record Plex's exact response/pending state, then create/accept that account; verify Cantinarr stamps only when Plex accepted the share request and never infers acceptance.
- [ ] `PLEX-085` · P0 · CHAOS/UI/API — Revoke the Plex token externally, then inspect status, setup checklist, Users actions, server list, and invite; verify no misleading operational/configured state, no stamp, and a clear relink path.
- [ ] `PLEX-087` · P1 · UI/LIVE — Resend after the recipient accepted access; verify `already_shared` copy is now truthful, no new-email push is sent, and existing libraries are neither removed nor silently broadened.

### Plex vector subresults

Do not check `PLEX-070` until every applicable vector below passes. Each row is external plex.tv truth; record evidence/defect per row so a partial pass is visible.

| Parent | Vector | Expected | Result / evidence |
|---|---|---|---|
| PLEX-070 | Invite pending | External pending recorded; local label only says sent | |
| PLEX-070 | Recipient accepted | External access works; no false local acceptance claim | |
| PLEX-070 | Recipient declined | External decline recorded; no false local live-state claim | |
| PLEX-070 | Invite expired | External expiry recorded; no false local live-state claim | |
| PLEX-070 | Owner canceled pending invite | External cancel recorded; no false local live-state claim | |
| PLEX-070 | Owner revoked accepted share | Access gone externally; no false local live-state claim | |
