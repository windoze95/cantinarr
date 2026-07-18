# Instances, realtime behavior, and push

Connecting real services, managed-webhook truth against real arrs, end-to-end realtime convergence, and real APNs delivery on physical devices. Instance CRUD contracts, event mapping, WebSocket authorization, and preference logic are proven by the hermetic suites.

Use the [run template](../run-template.md) to record executions of these cases.

## Real service connections and managed webhooks

- [ ] `INST-001` · P0 · UI/LIVE — Create and test one valid instance of each supported type: Radarr, Sonarr, Chaptarr, SABnzbd, qBittorrent, NZBGet, Transmission, and Tautulli; verify type-specific credentials and capabilities work.
- [ ] `INST-013` · P0 · LIVE — Configure instant updates on Radarr and Sonarr; verify Cantinarr rotates a server-only credential, upserts one managed Connect webhook, and the app never receives the secret.

## Realtime convergence

- [ ] `RT-007` · P0 · LIVE — Import a movie and several episodes; verify availability/events and new-content notification once despite webhook + poll overlap.

## Push delivery and notification taps

- [ ] `PUSH-004` · P0 · LIVE — Register, rotate, and delete APNs tokens for one/multiple devices; verify tokens bind to authenticated device/user and another user cannot alter them.
- [ ] `PUSH-006` · P0 · LIVE/UI — On iOS, cover not-determined, allowed, denied, settings redirect, and return-to-app; verify permission controls and token registration reflect actual OS state.
- [ ] `PUSH-013` · P0 · LIVE — Import movie and multiple episodes; verify opted-in audiences, correct movie/series copy, title collapse keys, and no duplicate from poll/webhook overlap.
- [ ] `PUSH-017` · P0 · LIVE/UI — Tap every notification type from foreground, background, and terminated app; verify exact detail/approval/issue/action/users/Plex/settings destination (book request decisions open the Books tab) and no duplicate navigation.
- [ ] `PUSH-023` · P1 · LIVE — Use self-test from preferences and admin per-user diagnostics; verify delivered/no-token/not-configured/partial/failure results and no test notification changes product badges.
