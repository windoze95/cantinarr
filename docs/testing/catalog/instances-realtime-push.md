# Instances, realtime behavior, and push

Connecting real services, managed-webhook truth against real arrs, end-to-end realtime convergence, and real APNs/FCM delivery on physical devices. Instance CRUD contracts, event mapping, WebSocket authorization, and preference logic are proven by the hermetic suites.

Use the [run template](../run-template.md) to record executions of these cases.

## Real service connections and managed webhooks

- [ ] `INST-001` · P0 · UI/LIVE — Create and test one valid instance of each supported type: Radarr, Sonarr, Chaptarr, SABnzbd, qBittorrent, NZBGet, Transmission, Tautulli, Tracearr, Jellyfin, Emby, and Plex; verify type-specific credentials and capabilities work (Plex through the PIN link rather than a URL and key; qBittorrent both with its WebUI password and, on 5.2 or newer, with an API key from Options > WebUI, and switching a saved instance from one to the other).
- [ ] `INST-013` · P0 · LIVE — Add a real Radarr and Sonarr instance and verify creating it alone installs the managed Connect webhook (the create confirmation reports it, and the edit screen's status line reads "Instant updates are on." from the arr itself); re-run **Configure instant updates** and verify Cantinarr rotates a server-only credential, updates the one managed record instead of duplicating it, and the app never receives the secret. Delete the record in the arr and verify the edit screen reads as not configured rather than trusting any stored state.
- [ ] `INST-020` · P0 · LIVE — Configure instant updates on a real Chaptarr; verify the managed `Cantinarr` record appears under Connect with an import event enabled, and that a fork exposing no import toggle fails the configure call with a readable message instead of saving a webhook that never fires.
- [ ] `INST-021` · P1 · LIVE — Confirm the Chaptarr webhook installs against the Readarr-lineage `/api/v1` notification API and that repeating the configure call updates the same record rather than adding a duplicate.

## Realtime convergence

- [ ] `RT-007` · P0 · LIVE — Import a movie and several episodes; verify availability/events and new-content notification once despite webhook + poll overlap.

## Push delivery and notification taps

- [ ] `PUSH-004` · P0 · LIVE — Register, rotate, and delete push tokens (APNs and FCM) for one/multiple devices; verify tokens bind to authenticated device/user and another user cannot alter them.
- [ ] `PUSH-006` · P0 · LIVE/UI — On iOS and Android 13+, cover not-determined, allowed, denied, settings redirect, and return-to-app; verify permission controls and token registration reflect actual OS state (Android: heads-up render with the correct small icon, cold- and warm-tap deep links, uninstall→prune→reinstall re-registration).
- [ ] `PUSH-013` · P0 · LIVE — Import movie, multiple episodes, and a Chaptarr book format; verify opted-in audiences (a book alert reaches only that instance's assigned users — an admin assigned to a sibling book instance is not paged, an admin with no books assignment is), correct movie/series/book-format copy, collapse keys, no duplicate from poll/webhook overlap, and silence for a failed/removed book download leaving the queue.
- [ ] `PUSH-025` · P0 · LIVE — Restart the container with a Chaptarr book download in flight and let it import while the server is down; verify the alert arrives on the first poll after boot (books have no webhook, so this is their only witness), arrives once, and carries the right format copy.
- [ ] `PUSH-026` · P1 · LIVE — Restart with a movie/episode download in flight; verify exactly one alert across the webhook and the resumed poll diff, that a title added directly in the arr and grabbed **and** imported entirely during the downtime still alerts once on the first poll after boot (import-history catch-up), and that a restart more than 6 hours after the import stays silent.
- [ ] `PUSH-027` · P1 · LIVE — Restart with the push gateway unreachable, then restore it; verify the resumed alert waits for enrollment rather than being lost, and that a gateway left down past the 6-hour cutoff drops the batch without wedging the poller.
- [ ] `PUSH-028` · P0 · LIVE — With Chaptarr instant updates configured, request a small ebook that imports in under 30 seconds; verify the alert arrives (the queue poller alone cannot witness it) and arrives exactly once despite the poller also running.
- [ ] `PUSH-029` · P1 · LIVE — Verify a Chaptarr rename, retag, or delete refreshes the app without ever pushing a "ready" alert, and that a real Chaptarr import event name outside the recognized set still leaves the poller to alert.
- [ ] `PUSH-030` · P0 · LIVE — With Lidarr instant updates configured, request an album that imports in under 30 seconds; verify the **New music available** alert arrives exactly once (webhook and poller overlapping) with the album-by-artist copy, reaches only users pinned to or granted that Lidarr instance (an admin with no Lidarr assignment is paged, one assigned to a sibling instance is not), and opens the album detail from the notification. Then disable the webhook, import another album directly in Lidarr, and verify the poller alerts once on its next pass; upgrade an owned album to a better quality and verify no "new music" alert fires (the upgrade path stays quiet by design); finally restart the container with an album download in flight and verify one alert, not two, after boot.
- [ ] `PUSH-017` · P0 · LIVE/UI — Tap every notification type from foreground, background, and terminated app; verify exact detail/approval/issue/action/users/Plex/settings destination (book request decisions and new-book alerts open the book detail via the payload's foreign id; older payloads without one open the Books tab) and no duplicate navigation.
- [ ] `PUSH-023` · P1 · LIVE — Use self-test from preferences and admin per-user diagnostics; verify delivered/no-token/not-configured/partial/failure results and no test notification changes product badges.
