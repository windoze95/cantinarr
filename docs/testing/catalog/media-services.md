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

- [ ] `BOOK-003` · P0 · UI/LIVE — Request both formats; verify exactly one ebook record and one audiobook record share the foreignBookId/group as one logical title, with no duplicate same-format record.
- [ ] `BOOK-014` · P0 · LIVE — Run supported Import Doctor classifications/fixes for Chaptarr, including exact queue/manual-import scope; verify no title-level mutation occurs without a durable book ID.

## Download clients and unified downloads

Run client-specific cases once for **each** of SABnzbd, qBittorrent, NZBGet, and Transmission; do not accept one client as proof for the other adapters.

- [ ] `DOWN-003` · P0 · LIVE — Pause and resume one active item per client; verify exact external item state and UI convergence.
- [ ] `DOWN-005` · P0 · LIVE — Remove a disposable item with data/files preserved; verify queue removal and data retention using that client's semantics. NZBGet offers no delete-files choice and the dialog states files stay on disk.
- [ ] `DOWN-006` · P0 · LIVE — Remove a disposable item with data/files deletion explicitly selected (not offered for NZBGet); verify confirmation and exact external deletion.

## Tautulli

- [ ] `TAUT-001` · P0 · LIVE — Load active direct-play, direct-stream, video-transcode, and audio-transcode sessions; verify user/title/player/progress/quality/decision badges and session count.
