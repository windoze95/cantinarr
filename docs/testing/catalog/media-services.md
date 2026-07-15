# Media services and download clients

Radarr, Sonarr, Chaptarr, download-client, queue, and Tautulli behavior.

Use the [run template](../run-template.md) to record executions of these cases.

## Radarr administration

- [ ] `RAD-001` · P0 · UI/LIVE — Load a large Radarr library; verify paging/filter/search, movie status bars, file size/quality, monitored state, and instance selector are accurate.
- [ ] `RAD-002` · P1 · UI — Switch between two Radarr instances while loading/filtering; verify rows/actions never mix instances and the selected instance remains visible.
- [ ] `RAD-003` · P0 · UI — Open movie detail from library/discovery; verify poster/title/year/runtime/rating/overview, status, file quality/size/release group/relative path, matching queue/messages, history, report action, and Automatic/Interactive actions belong to the exact movie.
- [ ] `RAD-004` · P0 · LIVE — Run Automatic search and verify one exact MovieSearch command; use app refresh/pull-to-refresh and verify detail/queue/history reload without issuing an arr Refresh command. Rescan is tested only through Import Doctor.
- [ ] `RAD-005` · P0 · LIVE — Open interactive movie release search; verify sorting, quality, size, protocol, indexer, seeders/leechers, rejection reasons, and grab the selected release only.
- [ ] `RAD-006` · P0 · SEC — Verify raw release capability/GUID secrets are not exposed beyond supported UI/proxy contracts and rejected releases cannot be grabbed accidentally.
- [ ] `RAD-007` · P0 · LIVE — Remove a movie, cancel once, then confirm with delete-files unchecked; verify Radarr record is removed and disk files remain.
- [ ] `RAD-008` · P0 · LIVE — Remove a disposable movie with delete-files checked; verify explicit confirmation and both Radarr record and intended files are removed—nothing else.
- [ ] `RAD-009` · P0 · UI — Verify no swipe or unlabeled gesture can perform deletion and delete-files is never preselected.
- [ ] `RAD-010` · P0 · LIVE — Load queue/history/wanted/calendar; verify supported paging, timestamps/event types, Wanted Missing/Cutoff segmentation, calendar dates, and links target the right movie without expecting nonexistent local search controls.
- [ ] `RAD-011` · P1 · UI — Handle queue entries with unknown movie, missing size/ETA, warnings/errors, stalled state, and completed-but-importing state without crash or misleading completion.
- [ ] `RAD-012` · P0 · LIVE — For each Radarr Import Doctor class, verify explanation/raw messages and ordered supported fixes match server classification.
- [ ] `RAD-013` · P0 · LIVE — Preview manual import candidates, select valid files, execute normal/force import, and verify quality/language blobs round-trip and only the intended movie files import.
- [ ] `RAD-014` · P0 · LIVE — Exercise remove, blocklist + re-search, category hand-off, and rescan fixes; verify confirmation/parameters and resulting queue/library state.
- [ ] `RAD-015` · P1 · CHAOS — Lose the response to a destructive/command action; reload external state before retry and verify the UI does not claim a result it cannot prove.
- [ ] `RAD-016` · P0 · SEC — As requester, browse permitted Radarr reads and verify all movie commands, release searches, mutations, configuration, and deletion remain admin-only.
- [ ] `RAD-017` · P1 · LIVE — Make a manual Radarr add/delete/import while the library screen is open; verify webhook/WebSocket refreshes without duplicate rows or stale availability.
- [ ] `RAD-018` · P1 · UI — Verify library-only local filter/search clear/count behavior; on queue/history/calendar verify load/empty/error/retry/paging/scroll, and on Wanted verify Missing/Cutoff segmentation plus refresh.
- [ ] `RAD-019` · P0 · LIVE — On a disposable Radarr queue item, test cancel, neither flag, remove-from-client only, blocklist only, and both; verify safe defaults, exact instance/item, external-client effect, search behavior, and refreshed queue.

## Sonarr administration

- [ ] `SON-001` · P0 · UI/LIVE — Load a large Sonarr library; verify series status distinguishes continuing caught-up, ended complete, monitored missing, unmonitored missing, and upcoming.
- [ ] `SON-002` · P1 · UI — Switch Sonarr instances during loading/filtering; verify series, queue, commands, and detail never cross instance boundaries.
- [ ] `SON-003` · P0 · UI — Drill series → All Seasons/season → episode; verify episode totals include unmonitored missing episodes correctly and Specials remain distinct.
- [ ] `SON-004` · P0 · UI — Verify per-episode download progress, air date, monitored/file state, quality/size, history, and messages correspond to the exact episode.
- [ ] `SON-005` · P0 · LIVE — Toggle series, season, and episode monitoring; verify intended flags/episodes change without altering unrelated seasons.
- [ ] `SON-006` · P0 · LIVE — Trigger series, monitored-series, season, and exact episode searches; verify command type and IDs at Sonarr and only intended scope searches.
- [ ] `SON-007` · P0 · LIVE — Long-press a season/episode and use each offered action; verify visible menu scope, confirmation, exact target, and refreshed result.
- [ ] `SON-008` · P0 · UI/LIVE — Enter episode multi-select; test All, Undownloaded, manual mix, clear, and selection persistence; batch search only the selected exact episodes.
- [ ] `SON-009` · P0 · LIVE — Batch-delete files for selected episodes, cancel once, then confirm; verify only selected episode files are removed and series/other files remain.
- [ ] `SON-010` · P0 · LIVE — Delete one disposable episode file with confirmation; verify the episode remains in Sonarr as missing and no adjacent file is touched.
- [ ] `SON-011` · P0 · UI/LIVE — Edit series profile, series type, path, tags, season folders, and monitored settings; verify saved Sonarr state and reload match exactly.
- [ ] `SON-012` · P1 · CHAOS — Submit invalid path/profile/type/tag or lose edit response; verify old state remains/reloads and no partial success toast.
- [ ] `SON-013` · P0 · LIVE — Open interactive release search for a series season and exact episode; verify scope, sorting/metadata/rejections, and selected grab at Sonarr.
- [ ] `SON-014` · P0 · UI — Open IMDb, TheTVDB, TMDB, and Trakt links; verify only present trusted URLs render and each points at the exact series.
- [ ] `SON-015` · P0 · LIVE — Remove a series with files preserved, then a disposable series with files deleted; verify cancellation/default safety and exact external results.
- [ ] `SON-016` · P0 · LIVE — Load queue/history/wanted/calendar; verify missing/cutoff, episode/series context, pagination, local time, and navigation.
- [ ] `SON-017` · P0 · LIVE — Run every Import Doctor classification on Sonarr queue entries; verify shared explanations/raw messages and valid fix ordering.
- [ ] `SON-018` · P0 · LIVE — Preview and execute manual/force import for single-episode, multi-episode, and Specials candidates; verify exact identity filtering prevents cross-episode import.
- [ ] `SON-019` · P0 · LIVE — Exercise remove, blocklist + scope-correct re-search, category hand-off, and series rescan; verify queue/history/library outcome.
- [ ] `SON-020` · P1 · CHAOS — Return inconsistent/missing series, season, episode, queue, or file IDs; verify destructive/search/import actions fail closed rather than falling back by title.
- [ ] `SON-021` · P0 · SEC — As requester, verify permitted reads remain scrubbed and all commands, release searches, edits, file deletion, and series removal are denied.
- [ ] `SON-022` · P1 · LIVE — Make direct Sonarr monitoring/import/delete/add changes while open; verify live refresh and requester availability without duplicate records.
- [ ] `SON-023` · P1 · UI — Verify library-only local filtering/counts; Wanted Missing/Cutoff segmentation; and queue/history/calendar load/empty/error/retry/paging/scroll without expecting nonexistent local search.
- [ ] `SON-024` · P0 · LIVE — On a disposable Sonarr queue item, test cancel, neither flag, remove-from-client only, blocklist only, and both; verify safe defaults, exact instance/item, external-client effect, scope-correct re-search, and refreshed queue.

## Chaptarr books

- [ ] `BOOK-001` · P0 · UI/LIVE — As a granted user, search a new author/book and request ebook; verify the matching ebook root/media type/monitor flags and search are used.
- [ ] `BOOK-002` · P0 · UI/LIVE — Request audiobook; verify the audiobook root/media type/monitor flags are used and ebook state is untouched.
- [ ] `BOOK-003` · P0 · UI/LIVE — Request both formats; verify exactly one ebook record and one audiobook record share the foreignBookId/group as one logical title, with no duplicate same-format record.
- [ ] `BOOK-004` · P0 · LIVE — From ebook-only, request audiobook; verify exactly one separate audiobook record is added/monitored/searched and the ebook record remains untouched.
- [ ] `BOOK-005` · P0 · LIVE — Repeat audiobook-only → ebook; verify exactly one separate ebook record and no mutation/duplication of the audiobook record.
- [ ] `BOOK-006` · P0 · UI — Verify downloaded formats are disabled as Downloaded/In Library, monitored-without-file formats remain requestable, and denied requests become requestable again.
- [ ] `BOOK-007` · P0 · UI — Search duplicate titles/editions and owned records; verify owned results float with chips but distinct records are never merged.
- [ ] `BOOK-008` · P0 · UI/LIVE — On author detail, toggle format bookmarks independently; empty adds/searches only that missing format, while filled unmonitors only that exact format and leaves the sibling unchanged.
- [ ] `BOOK-009` · P0 · UI — As an ungranted user, verify Books tab/routes/API are absent/denied; grant access and verify the correct assigned Chaptarr data appears.
- [ ] `BOOK-010` · P0 · LIVE — Load Chaptarr library/author drill-down with complete, continuing, monitored-missing, unmonitored-missing, ebook-only, and audiobook-only fixtures; verify statuses and totals.
- [ ] `BOOK-011` · P0 · LIVE — Search releases for an exact book/format, review sorting/metadata/rejections, and grab one; verify exact book/format at Chaptarr.
- [ ] `BOOK-012` · P0 · LIVE — Remove an author, cancel once, preserve files by default, then test delete-files on a disposable fixture; verify exact outcomes.
- [ ] `BOOK-013` · P0 · LIVE — Load queue/history/wanted missing/cutoff pages; verify pagination, format labels, author/book navigation, and distinct records.
- [ ] `BOOK-014` · P0 · LIVE — Run supported Import Doctor classifications/fixes for Chaptarr, including exact queue/manual-import scope; verify no title-level mutation occurs without a durable book ID.
- [ ] `BOOK-015` · P0 · LIVE — Preview/force manual import for ebook and audiobook candidates; verify format/media type, quality/language data, and exact file mapping.
- [ ] `BOOK-016` · P1 · CHAOS — Simulate Chaptarr async author refresh or partial add failure; verify best-effort follow-up monitoring/search converges and retry does not duplicate records.
- [ ] `BOOK-017` · P1 · CHAOS — Return stock Servarr bare release arrays, fork envelopes, and unexpected objects; verify accepted shapes render and unexpected shapes fail empty/safely.
- [ ] `BOOK-018` · P1 · LIVE — Reassign the user to another Chaptarr instance while viewing/requesting; verify caches are user+instance safe and no ownership/request leaks across libraries.
- [ ] `BOOK-019` · P1 · LIVE — Change book state directly in Chaptarr; verify the short-TTL digest/refetch updates ownership and request buttons without a permanent snapshot.
- [ ] `BOOK-020` · P1 · UI — Verify long titles/authors, missing covers/editions, local filters, pagination, empty/error/retry states, and no duplicate search results.
- [ ] `BOOK-021` · P0 · LIVE — On a disposable Chaptarr queue item, test cancel, neither flag, remove-from-client only, blocklist only, and both; verify safe defaults, exact instance/book-format item, external-client effect, and refreshed queue.
- [ ] `BOOK-022` · P0 · UI/LIVE — For a logical title with both format records, start Automatic and Interactive search; choose ebook/audiobook and verify exact format/book ID, cancel is inert, and a single-format title skips the prompt.
- [ ] `BOOK-023` · P1 · LIVE — Run author-level Automatic search; verify the exact author ID and active Chaptarr instance receive one command and sibling instances/authors remain untouched.

## Download clients and unified downloads

Run client-specific cases once for **each** of SABnzbd, qBittorrent, NZBGet, and Transmission; do not accept one client as proof for the other adapters.

- [ ] `DOWN-001` · P0 · LIVE — Load each client's queue; verify item ID/name/category/status, size/progress, speed, ETA, client/instance identity, and aggregate rates map correctly.
- [ ] `DOWN-002` · P0 · LIVE — Load each client's history; verify completed/failed status, timestamps, size/category, supported history limit/ordering, and client identity without assuming pagination.
- [ ] `DOWN-003` · P0 · LIVE — Pause and resume one active item per client; verify exact external item state and UI convergence.
- [ ] `DOWN-004` · P0 · LIVE — Pause all and resume all per client; verify only the selected instance/client changes and commands are not repeated.
- [ ] `DOWN-005` · P0 · LIVE — Remove a disposable item with data/files preserved; verify queue removal and data retention using that client's semantics.
- [ ] `DOWN-006` · P0 · LIVE — Remove a disposable item with data/files deletion explicitly selected; verify confirmation and exact external deletion.
- [ ] `DOWN-007` · P0 · UI — Cancel every remove dialog and verify no external mutation; delete-data must always default off.
- [ ] `DOWN-008` · P0 · UI — Switch among multiple download clients while requests are in flight; verify queue/history/actions never target the previously selected instance.
- [ ] `DOWN-009` · P1 · LIVE — Cover paused, queued, downloading, stalled, checking, seeding/post-processing, completed, warning, and failed statuses per applicable client; verify readable normalized labels.
- [ ] `DOWN-010` · P1 · UI — Cover zero/unknown size, infinite/unknown ETA, zero speed, Unicode names, duplicate names with distinct IDs, and very large values; verify stable formatting and identity.
- [ ] `DOWN-011` · P0 · SEC — As requester, direct-call download read/mutation routes; verify all are denied and no client metadata leaks.
- [ ] `DOWN-012` · P1 · CHAOS — Stop each client during load and during pause/resume/delete; verify scoped retryable errors, no optimistic false state, and safe external-state refresh before retry.
- [ ] `DOWN-013` · P1 · CHAOS — Lose a mutation response after the client applied it; refresh and reconcile before allowing repeat, especially destructive delete.
- [ ] `DOWN-014` · P1 · LIVE — Verify WebSocket queue snapshots update rate/progress/state without duplicates, survive reconnect, and stop after leaving the module or forced auth loss from current-device revocation.
- [ ] `DOWN-015` · P1 · SEC — Inspect URLs/logs/errors for client usernames, passwords, cookies, API keys, and torrent hashes where sensitive; verify appropriate scrubbing.
- [ ] `DOWN-016` · P1 · UI — Verify queue/history tab state, pull-to-refresh, empty/error/retry, scroll restoration, and compact/wide layouts for every adapter without expecting nonexistent local filter/search.

## Tautulli

- [ ] `TAUT-001` · P0 · LIVE — Load active direct-play, direct-stream, video-transcode, and audio-transcode sessions; verify user/title/player/progress/quality/decision badges and session count.
- [ ] `TAUT-002` · P1 · LIVE — Cover paused/buffering/remote/local/multiple-user streams and missing media/image/session fields; verify truthful labels and stable layout.
- [ ] `TAUT-003` · P0 · LIVE — Load watch history; verify user/media/title/timestamps/duration/platform plus supported history limit/ordering without assuming pagination.
- [ ] `TAUT-004` · P0 · LIVE — Load top movies, shows, and users for supported periods; verify stat type, label, play count, ordering, and empty categories without inventing unsupported duration.
- [ ] `TAUT-005` · P0 · SEC — As requester, verify Tautulli menu/routes/API are denied and usernames/watch history are not exposed.
- [ ] `TAUT-006` · P1 · CHAOS — Fail Tautulli authentication, connection, or one command; verify only the affected tab errors and retry recovers after credentials/service return.
- [ ] `TAUT-007` · P1 · UI — Switch Tautulli instances/tabs while loading; verify no mixed stream/history/stats results and local refresh works.
- [ ] `TAUT-008` · P1 · SEC — Verify Tautulli API key and upstream URLs are absent from client responses/logging.
- [ ] `TAUT-009` · P1 · LIVE/UI — Start/change/end a stream while Activity stays visible; verify 10-second refresh converges without overlapping requests, instance changes discard stale results, and timer stops on screen disposal.
