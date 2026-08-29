# Plex linking, libraries, and invitations

Plex ownership, server selection, explicit and all-library scopes, current and future libraries, invites, revocation, and lifecycle truth. Every case here proves state on the plex.tv side that no fake can stand in for; email validation, settings persistence, the grant/invite/reconcile paths, the boot migration of the pre-instance Plex link, and injected-failure vectors are proven by the hermetic suites.

Use the [run template](../run-template.md) to record executions of these cases.

## Plex linking, library sharing, and invitations

These are real Plex end-to-end tests. "Invite sent" is not proven by Cantinarr's card or push. For every share-scope case, capture the exact outgoing/stored global section IDs, Plex Manage Library Access, and a recipient view of each uniquely named marker item. Accept the initial invite before future-library checks, remove shares before reusing recipients, and distinguish pending-invite 422 behavior from accepted-share 422 behavior.

- [ ] `PLEX-007` · P0 · UI/LIVE — Add a Plex instance: link a valid PIN from the instance editor; verify the linked plex.tv username appears, the owned servers list, the instance saves with the chosen server, Test Connection passes through the stored token after a reopen, and a relink replaces the token without touching grants or shares.
- [ ] `PLEX-010` · P0 · LIVE — List servers for an account with owned servers, a shared server, and player/client resources; verify only owned Plex Media Servers appear in the picker.
- [ ] `PLEX-015` · P0 · LIVE — Load movie, show, music, and photo libraries; verify title/type and Plex-global section IDs map to the correct server sections, not local section keys.
- [ ] `PLEX-018` · P0 · UI/LIVE — Check several non-adjacent libraries, save, reload, and have a granted user ask for their invite; verify exactly those global section IDs are shared regardless of list order.
- [ ] `PLEX-020` · P0 · UI/LIVE — With all libraries unchecked, invite and accept from a brand-new account; verify that account can open every current library on the selected server.
- [ ] `PLEX-021` · P0 · LIVE — Keep "all libraries" (`[]`) saved, add a new Plex library, then invite and accept from another new account without resaving Cantinarr; verify the new library is included automatically.
- [ ] `PLEX-022` · P0 · LIVE — Invite and accept while "all libraries" (`[]`) is saved, then add a new Plex library after acceptance; verify the already-shared account gains that future library without a new invite.
- [ ] `PLEX-023` · P0 · LIVE — Save an explicit subset, invite and accept, add a new Plex library, then test the existing share and another new invite; verify the new library is not silently included and the explicit subset remains exact. Then untick a library in the editor; verify the accepted share loses it (`PUT shared_servers/{id}`) and a still-pending invite is left as sent.
- [ ] `PLEX-031` · P0 · UI/LIVE — A user shares their Plex email from the guide while already granted; verify one invite at plex.tv with the selected server/libraries and the exact trimmed, lower-cased email, the guide shows the invite as pending, the user gets `plex_invite_sent`, and the admin push says it was sent.
- [ ] `PLEX-032` · P0 · UI/LIVE — With **auto-approve** on, an ungranted user shares a Plex email; verify the grant appears, one invite goes out without admin action, the guide flips from the ask card to the pending invite, and admins are told it was sent. With auto-approve off, verify no invite, the admin push says the user is waiting, the drawer badge counts them, and ticking them under User Access sends the invite.
- [ ] `PLEX-041` · P0 · LIVE — Ask for the invite with an email/account that already has server access; verify no invite is sent, the row is recorded as linked (not created by Cantinarr), no "check your inbox" push is sent, and the guide shows the share as accepted.
- [ ] `PLEX-042` · P1 · UI/LIVE — Ask again while the original invite is still pending; verify the app says the invite is already on its way, plex.tv receives no second share request, no second push goes out, and the pending scope is unchanged.
- [ ] `PLEX-061` · P0 · LIVE — Explicitly check every current library (nonempty ID list), invite/accept, then add a future library; verify the recipient does **not** gain that new library, proving explicit-current differs from unchecked/all-future mode.
- [ ] `PLEX-070` · P1 · LIVE — Observe a fresh invite before acceptance, after acceptance, after decline/expiry/cancel, and after owner revoke; verify external Plex truth manually and that the guide reads each from plex.tv (pending, accepted, gone) rather than from Cantinarr's record, and that a share gone on its own is never re-invited by an unrelated grant write.
- [ ] `PLEX-078` · P0 · CHAOS/LIVE — Let Plex commit a share but drop the response until Cantinarr times out; prove external access exists while no row or push exists, then ask again and verify the existing share is adopted without a second email.
- [ ] `PLEX-082` · P1 · LIVE — Invite an unregistered email, record Plex's exact response/pending state, then create/accept that account; verify the guide shows pending until acceptance and never infers it.
- [ ] `PLEX-085` · P0 · CHAOS/UI/API — Revoke the Plex token externally, then inspect Test Connection, the setup checklist, the guide (`verified: false`, never "gone"), and a new invite; verify no misleading operational state, no row, and that Relink in the editor recovers everything.
- [ ] `PLEX-087` · P0 · UI/LIVE — Untick a granted user with an accepted share; verify the share disappears from Manage Library Access and the guide shows no account. Tick them again; verify the share comes back (an account still connected to the owner gets it accepted at once; anyone else gets a new invite to accept) with the instance's current library selection, and that no "check your email" push goes out when nothing was emailed.
- [ ] `PLEX-090` · P1 · UI/LIVE — A user with a pending or accepted share changes their Plex email; verify the share Cantinarr sent is removed, a new invite goes to the new address, and a share an admin linked by hand is refused with the app telling them to ask the admin.
- [ ] `PLEX-091` · P0 · LIVE — Delete a Cantinarr user with an accepted share; verify the share is gone from plex.tv and their Plex account is untouched.
- [ ] `PLEX-092` · P1 · CHAOS/LIVE — Block plex.tv, grant a user who has shared an email; verify no invite and a logged failure, then unblock and verify the drift sweep sends it within five minutes. Toggle the same user's Jellyfin grant meanwhile; verify no Plex email results.

### Plex vector subresults

Do not check `PLEX-070` until every applicable vector below passes. Each row is external plex.tv truth; record evidence/defect per row so a partial pass is visible.

| Parent | Vector | Expected | Result / evidence |
|---|---|---|---|
| PLEX-070 | Invite pending | External pending recorded; guide shows the invite as pending | |
| PLEX-070 | Recipient accepted | External access works; guide shows the share as accepted with where to sign in | |
| PLEX-070 | Recipient declined | External decline recorded; guide shows no account and offers to share again | |
| PLEX-070 | Invite expired | External expiry recorded; guide shows no account and offers to share again | |
| PLEX-070 | Owner canceled pending invite | External cancel recorded; guide shows no account; a Jellyfin grant toggle sends no new invite | |
| PLEX-070 | Owner revoked accepted share | Access gone externally; guide shows no account; a Jellyfin grant toggle sends no new invite | |
