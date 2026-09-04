# Media services and download clients

End-to-end journeys against real Radarr, Sonarr, Chaptarr, download-client, Tautulli, and Tracearr services — grabs, Import Doctor fixes, and per-client remove semantics. Screen contracts, queue normalization, and authorization are proven by the hermetic suites.

Use the [run template](../run-template.md) to record executions of these cases.

## Radarr administration

- [ ] `RAD-005` · P0 · LIVE — Open interactive movie release search; verify sorting, quality, size, protocol, indexer, seeders/leechers, rejection reasons, and grab the selected release only.
- [ ] `RAD-014` · P0 · LIVE — Exercise remove, blocklist + re-search, category hand-off, and rescan fixes; verify confirmation/parameters and resulting queue/library state.
- [ ] `RAD-015` · P1 · UI/LIVE — Open a movie's page against a real Radarr; verify the External links sheet opens the movie on IMDb, TMDB, and Trakt, Edit Movie round-trips profile, minimum availability, path, and tags at Radarr with every unmodelled field intact, the action menu's monitor toggle and Refresh Movie reflect at Radarr, and Remove Movie keeps files by default and deletes them only on opt-in.

## Sonarr administration

- [ ] `SON-013` · P0 · LIVE — Open interactive release search for a series season and exact episode; verify scope, sorting/metadata/rejections, and selected grab at Sonarr.
- [ ] `SON-019` · P0 · LIVE — Exercise remove, blocklist + scope-correct re-search, category hand-off, and series rescan; verify queue/history/library outcome.

## Chaptarr books

- [ ] `BOOK-003` · P0 · UI/LIVE — From the selected Chaptarr instance, request both formats; verify exactly one ebook record and one audiobook record share the foreignBookId/group as one logical title, each moves Requested → Downloading → Available independently, returning from Chaptarr refreshes immediately, and repeating or approving concurrently cannot create a duplicate same-format record in that or another instance.
- [ ] `BOOK-014` · P0 · LIVE — Run supported Import Doctor classifications/fixes for Chaptarr, including exact queue/manual-import scope; verify no title-level mutation occurs without a durable book ID.
- [ ] `BOOK-015` · P1 · UI/LIVE — Request a book whose author the live Chaptarr metadata service has never seen, so the add is refused with a queued author import. As the requester, verify the format reads **Waiting for library** with its standing explanation on a fresh visit (not just at submission), cannot be requested again, and offers no retry or ETA. As an admin, verify the request is absent from the approval queue and every badge/push, and present in **Waiting for library** with the requester, pinned library, waiting-since, and a last-attempt that advances within one retry interval. Then let the author import land: verify the request completes with no approval and no owner push, and that the waiting row and the requester's explanation both disappear on their own. Restart the server mid-wait and verify the admin row reports the attempt as unknown rather than as days old.

## Lidarr music

- [ ] `MUS-001` · P0 · UI/LIVE — From the selected Lidarr instance, request an album the library does not track; verify the artist is added with exactly that album monitored (`monitorNewItems` none, no other albums monitored), the request reads Requested → Downloading → Available as the grab completes, repeating the request cannot create a duplicate record, and an already-complete album answers Available without mutating the library.
- [ ] `MUS-002` · P1 · UI/LIVE — Verify the grant model end to end: a user with no Lidarr pin sees no Music tab and every music route degrades to the movies dashboard; pinning them (from either the instance editor or their user settings) surfaces the tab; un-pinning revokes it; holding a Chaptarr grant alone opens nothing.
- [ ] `MUS-003` · P1 · LIVE — With instant updates configured, import an album directly in Lidarr and verify the Music tab's Recently Added row and the album's status refresh within seconds (webhook), then disable the webhook and verify the poller still catches the change on its next pass.
- [ ] `MUS-004` · P1 · UI/LIVE — On a real Lidarr with monitored albums whose release dates straddle a month boundary and a local-time day boundary (an album dated the 1st in UTC viewed from a UTC-6 device), open the Music module's Calendar tab and the dashboard Releases tab; verify every album lands on the calendar date Lidarr shows for it (never shifted by the device timezone), that a requester without a Lidarr grant sees no album rows at all, and that the Releases tab's music rows resolve through the requester's granted instance rather than any global default.
- [ ] `MUS-005` · P0 · LIVE — On a real Lidarr with indexers configured, leave a download stuck (an unextracted archive or a folder with nothing importable) and open the Music queue: verify the card opens the Import Doctor only when Lidarr flags the item, that the doctor's verdict and one-click fixes match Lidarr's own state afterwards, and that the manual-import candidates read as matched or **no album match** per file, so an unmatched file is never silently skipped by the import. Then from an album row use **Choose a download**: verify the release list carries size, protocol, seeders, indexer, and rejection reasons, and that grabbing one sends exactly that release to Lidarr's download client.
- [ ] `MUS-006` · P1 · UI/LIVE — Map the Lidarr instance's music root read-only beneath `CANTINARR_MEDIA_ROOTS`, open an owned album as a requester, and download one track from the per-track chooser; verify the track label matches Lidarr's track number and title, the bytes match the file Lidarr holds, the transfer resumes with HTTP Range, and that an unmapped sibling Lidarr instance shows no download control while the mapped one does.

## Completed media files

- [ ] `FILE-001` · P1 · UI/LIVE — Mount disposable libraries read-only beneath `CANTINARR_MEDIA_ROOTS`, then configure per-instance arr-path → Cantinarr-path mappings. Include two instances that report the same source prefix but map to files with different bytes, plus one Chaptarr instance with separate `/ebooks`, `/audiobooks`, `/yana-ebooks`, and `/yana-audiobooks` mappings. Download an ebook, one file from a multi-file audiobook, a movie, and an episode from their live detail surfaces; verify names/bytes match the exact instance records and a movie/episode transfer resumes with HTTP Range. Leave one newly created instance unmapped and verify its controls are absent while mapped siblings remain enabled; omit one path from a partially mapped instance and verify the server refuses that file without exposing either path. Verify each requester is limited to their effective or granted instance and removing every explicit mapping disables only that instance's controls. Then remove the global root from the environment and mount, restart/recreate the server, refresh/relaunch the app, and verify all download controls are disabled. On an upgraded instance whose stored mode still carries the retired legacy identity value, verify downloads read as disabled until explicit mappings are saved; a newly created instance with no mappings must start disabled.

## Download clients and unified downloads

Run client-specific cases once for **each** of SABnzbd, qBittorrent, NZBGet, Transmission, and Deluge; do not accept one client as proof for the other adapters.

- [ ] `DOWN-003` · P0 · LIVE — Pause and resume one active item per client; verify exact external item state and UI convergence.
- [ ] `DOWN-005` · P0 · LIVE — Remove a disposable item with data/files preserved; verify queue removal and data retention using that client's semantics. NZBGet offers no delete-files choice and the dialog states files stay on disk.
- [ ] `DOWN-006` · P0 · LIVE — Remove a disposable item with data/files deletion explicitly selected (not offered for NZBGet); verify confirmation and exact external deletion.

## Tautulli and Tracearr

- [ ] `TAUT-001` · P0 · LIVE — Load active direct-play, direct-stream, video-transcode, and audio-transcode sessions; verify user/title/player/progress/quality/decision badges and session count.
- [ ] `TRR-001` · P0 · LIVE — Against a Tracearr instance watching a real Jellyfin (or Plex/Emby) server: add it with its public API key and confirm a wrong key fails the connection test naming the rejected key and no host; with a live and a paused session, verify the Monitoring Activity badges (decision, quality, the server's name, a media badge for music) and the count; verify History is newest-first with the server in each row and the coverage note at the end; verify Stats ranks movies/shows/users for 7 and 30 days with the "Based on N plays since …" note, and that a second read of the same window is served without new Tracearr requests.
