# Media services and download clients

End-to-end journeys against real Radarr, Sonarr, Chaptarr, download-client, and Tautulli services — grabs, Import Doctor fixes, and per-client remove semantics. Screen contracts, queue normalization, and authorization are proven by the hermetic suites.

Use the [run template](../run-template.md) to record executions of these cases.

## Radarr administration

- [ ] `RAD-005` · P0 · LIVE — Open interactive movie release search; verify sorting, quality, size, protocol, indexer, seeders/leechers, rejection reasons, and grab the selected release only.
- [ ] `RAD-014` · P0 · LIVE — Exercise remove, blocklist + re-search, category hand-off, and rescan fixes; verify confirmation/parameters and resulting queue/library state.

## Sonarr administration

- [ ] `SON-013` · P0 · LIVE — Open interactive release search for a series season and exact episode; verify scope, sorting/metadata/rejections, and selected grab at Sonarr.
- [ ] `SON-019` · P0 · LIVE — Exercise remove, blocklist + scope-correct re-search, category hand-off, and series rescan; verify queue/history/library outcome.

## Chaptarr books

- [ ] `BOOK-003` · P0 · UI/LIVE — From the selected Chaptarr instance, request both formats; verify exactly one ebook record and one audiobook record share the foreignBookId/group as one logical title, each moves Requested → Downloading → Available independently, returning from Chaptarr refreshes immediately, and repeating or approving concurrently cannot create a duplicate same-format record in that or another instance.
- [ ] `BOOK-004` · P0 · UI/LIVE — Search a title with multiple plausible catalog records and multiple publications of one record; verify the request sheet labels the strongest title/author result as the closest match, shows useful publication evidence, keeps every user-distinguishable alternative selectable without exposing local IDs, and groups editions that differ only by a hidden ID. Reproduce a lookup where a partial title returns the intended record but the complete title returns an empty list; verify the complete query still shows only strongly matching fallback rows and the request replays the successful query before enforcing the same exact work/author/publication. Include a safely matched lookup row whose `foreignBookId` differs from the live library record; verify direct and detail-page requests preserve the lookup identity while status, history, and the missing-format mutation stay on the canonical library work. Include a real lookup edition with `format: null` and an `isEbook` hint and verify the hint is not submitted as an authoritative publication selection. Choose a non-first authoritative publication and confirm the same external author/edition survives direct submission, admin approval, and restart, while a changed or missing selector fails before any `BookSearch`. With a non-default Chaptarr selected, trigger a same-user token/config refresh and verify the selection does not jump to another library. Exercise a brand-new author, a brand-new title for an existing author, and a missing sibling format; verify Cantinarr selects exactly one matching authoritative edition, preserves the other format, and waits for the monitor readback and `BookSearch` acknowledgement (not indexer completion). Interrupt the web response after Chaptarr accepts the request and verify the app shows a still-checking state without sending another request. Restart Cantinarr once while the add is awaiting catalog materialization and once after an acknowledged first format of a `both` request but before the second completes; verify the durable job resumes without a status-page click, sends no duplicate book add, never repeats the acknowledged first-format search, completes each concrete history row once, and returns to the normal per-format state. Repeat the lost-response/restart checks through an admin-approved request and confirm its pending row finalizes once without another approval click or duplicate mutation. Also interrupt an unacknowledged search response and verify no replacement search is sent before the evidence guard expires; then repoint the same instance ID to a disposable empty Chaptarr and verify old endpoint checkpoints/history do not hide either format on the new binding.
- [ ] `BOOK-014` · P0 · LIVE — Run supported Import Doctor classifications/fixes for Chaptarr, including exact queue/manual-import scope; verify no title-level mutation occurs without a durable book ID.

## Completed media files

- [ ] `FILE-001` · P1 · UI/LIVE — Mount disposable libraries read-only beneath `CANTINARR_MEDIA_ROOTS`, then configure per-instance arr-path → Cantinarr-path mappings. Include two instances that report the same source prefix but map to files with different bytes, plus one Chaptarr instance with separate `/ebooks`, `/audiobooks`, `/yana-ebooks`, and `/yana-audiobooks` mappings. Download an ebook, one file from a multi-file audiobook, a movie, and an episode from their live detail surfaces; verify names/bytes match the exact instance records and a movie/episode transfer resumes with HTTP Range. Leave one newly created instance unmapped and verify its controls are absent while mapped siblings remain enabled; omit one path from a partially mapped instance and verify the server refuses that file without exposing either path. Verify each requester is limited to their effective or granted instance and removing every explicit mapping disables only that instance's controls. Then remove the global root from the environment and mount, restart/recreate the server, refresh/relaunch the app, and verify all download controls are disabled. On an upgraded instance with no explicit mappings, also verify the documented legacy identity behavior until mappings are saved; a newly created instance with no mappings must start disabled.

## Download clients and unified downloads

Run client-specific cases once for **each** of SABnzbd, qBittorrent, NZBGet, and Transmission; do not accept one client as proof for the other adapters.

- [ ] `DOWN-003` · P0 · LIVE — Pause and resume one active item per client; verify exact external item state and UI convergence.
- [ ] `DOWN-005` · P0 · LIVE — Remove a disposable item with data/files preserved; verify queue removal and data retention using that client's semantics. NZBGet offers no delete-files choice and the dialog states files stay on disk.
- [ ] `DOWN-006` · P0 · LIVE — Remove a disposable item with data/files deletion explicitly selected (not offered for NZBGet); verify confirmation and exact external deletion.

## Tautulli

- [ ] `TAUT-001` · P0 · LIVE — Load active direct-play, direct-stream, video-transcode, and audio-transcode sessions; verify user/title/player/progress/quality/decision badges and session count.
