# Discovery and requests

Discovery, search, detail views, availability, requester policy, approval, and request lifecycle behavior.

Use the [run template](../run-template.md) to record executions of these cases.

## Discovery, search, media detail, and release timeline

- [ ] `DISC-001` · P0 · UI/LIVE — Load actual dashboards: movie spotlight, Popular Movies, Top Rated, Coming Soon, Most Anticipated, Downloading Soon, Recently Downloaded; series spotlight, Popular TV Shows, Most Anticipated, Recently Downloaded, Airing Next. Verify identity/image/date/status/detail target.
- [ ] `DISC-002` · P0 · CHAOS — Remove or invalidate TMDB credentials; verify discovery/search/detail fail with actionable empty/error states and no API token reaches the device.
- [ ] `DISC-003` · P1 · API — Call movie/TV discover with page, genre, provider/region, year, and sort combinations; verify exact upstream forwarding, cache-key separation, page boundaries, and changed filters never reuse prior results.
- [ ] `DISC-004` · P0 · UI — Search an exact movie, TV show, and person; verify each record keeps its media type/identity and opens the correct movie, TV, or person view.
- [ ] `DISC-005` · P0 · UI — Search a title with two distinct matching records or duplicate library entries; verify no silent dedupe/merge and each result remains selectable.
- [ ] `DISC-006` · P0 · UI/LIVE — Search as two users pinned to different default arr instances; verify availability chips are computed against each user's live default library, not shared cached state.
- [ ] `DISC-007` · P0 · UI/LIVE — Move a title through unavailable, requested, downloading, partial, and available outside Cantinarr; verify search/detail chips converge over webhook/WebSocket/refetch with requester vocabulary only.
- [ ] `DISC-008` · P1 · CHAOS — Return results out of order for rapidly changed searches; verify stale responses never replace the newest query.
- [ ] `DISC-009` · P1 · UI — Search with empty, whitespace-only, punctuation, Unicode, apostrophe, long, and URL-like input; verify safe encoding, no injection/crash, and sensible empty states.
- [ ] `DISC-010` · P0 · UI — Open movie detail; verify artwork/title, rating, tagline, genres, overview, request/status actions, admin arr link, trusted trailer, recommendations, and similar titles map to that movie with safe missing-field fallbacks.
- [ ] `DISC-011` · P0 · UI — Open TV detail; verify artwork/title, rating, tagline, genres, overview, real-season availability/request controls, admin arr link, trailer, recommendations/similar; requester season selection deliberately excludes Specials.
- [ ] `DISC-012` · P1 · UI — Open person detail and credits; verify movie/TV credits retain their media type, dates, character/job, and link to the correct detail.
- [ ] `DISC-013` · P1 · CHAOS — Use missing poster/backdrop/profile URLs and image 404/timeouts; verify stable placeholders, readable layout, and no endless shimmer.
- [ ] `DISC-014` · P1 · UI — Verify long titles, summaries, localized Unicode, unknown dates, and absent metadata do not overflow or display literal null values.
- [ ] `DISC-015` · P0 · UI/LIVE — As admin, request/add a movie then TV show from discovery; verify “Open in Radarr/Sonarr” appears only after the exact ID exists and links to the matching item/instance.
- [ ] `DISC-016` · P0 · SEC/UI — As requester, verify arr deep links never appear even when the title exists; as admin, verify no link appears for a nonmatching same-title record.
- [ ] `DISC-017` · P1 · LIVE — With Trakt configured, load trending, popular, public lists/items, calendar, anticipated, and recommendations; verify IDs/media types bridge to usable details.
- [ ] `DISC-018` · P1 · CHAOS — Disable/fail Trakt while TMDB works; verify TMDB-backed discovery remains functional and Trakt-only sections fail independently.
- [ ] `DISC-019` · P0 · UI — In the Releases tab list view, combine movie releases and TV episodes; verify chronological ordering, media labels, availability, and links.
- [ ] `DISC-020` · P0 · UI — In month-calendar view, cross month/year boundaries and today; verify correct local-day placement, navigation, and no UTC off-by-one.
- [ ] `DISC-021` · P1 · UI — Verify repeated release events for distinct episodes/titles remain distinct while the same event is not duplicated by refresh.
- [ ] `DISC-022` · P1 · CHAOS — Drop/reconnect WebSocket on dashboard rows and release timeline; verify one subscription, no duplicate rows/events, and eventual fresh data.
- [ ] `DISC-023` · P1 · UI — Pull-to-refresh/retry each discovery/detail surface during loading, empty, error, and loaded states; verify one current request and preserved navigation.
- [ ] `DISC-024` · P1 · SEC — Inspect all discovery and Trakt requests/responses/logs; verify TMDB/Trakt credentials remain server-side and secret-bearing upstream URLs are scrubbed.
- [ ] `DISC-025` · P1 · API — Call `/api/discover/trending` across supported media/window parameters and `/api/discover/movies/now-playing`; verify bounded current identities even though these are not dashboard row names.
- [ ] `DISC-026` · P0 · UI/LIVE — On Movies, verify spotlight plus Downloading Soon orders active downloads first then monitored/missing fallback, Recently Downloaded reflects imports, and direct import/resume refresh converges without duplicates.
- [ ] `DISC-027` · P0 · UI/LIVE — On TV, verify spotlight, Recently Downloaded, and Airing Next uses unique series from the next seven-day calendar without duplicate shows or wrong episode links.
- [ ] `DISC-028` · P1 · CHAOS/UI — With no default instance or one live-row source failing, verify TMDB discovery remains usable, only affected live rows degrade, and refresh recovers them.
- [ ] `DISC-029` · P1 · UI/LIVE — Cover primary YouTube trailer, fallback YouTube video, no video, and invalid/missing key; Watch Trailer appears only when appropriate and launches the exact trusted target once.

## Requests, availability, policies, and approvals

- [ ] `REQ-001` · P0 · UI/LIVE — Request a new movie with no approval required; verify the correct user's Radarr instance receives the exact TMDB ID, configured root/profile, monitored flag, and search action once.
- [ ] `REQ-002` · P0 · LIVE — Request a movie already in Radarr with a file; verify no duplicate add/search and Cantinarr reports Available.
- [ ] `REQ-003` · P0 · LIVE — Request a movie already in Radarr without a file and monitored; verify no duplicate record and Requested/Downloading state reflects live progress.
- [ ] `REQ-004` · P0 · LIVE — Request an existing unmonitored movie without a file; verify it is re-monitored and searched, preserving the existing record.
- [ ] `REQ-005` · P0 · UI/LIVE — Request a new TV series with each coarse scope (`all`, `first`, `latest`, `pilot`); verify Sonarr monitoring/search behavior exactly matches the chosen scope and Specials are not accidentally included.
- [ ] `REQ-006` · P0 · UI/LIVE — Select a noncontiguous set of real seasons; verify Specials are absent from requester UI and only selected real seasons are stored, monitored, and searched in sorted/deduplicated order.
- [ ] `REQ-007` · P0 · LIVE — Request additional seasons for an existing series; verify monitoring is additive, chosen season episodes are monitored/searched, and previously monitored seasons are never unmonitored.
- [ ] `REQ-008` · P0 · LIVE — Request pilot scope for an existing series; verify S01E01 is monitored/searched without incorrectly marking all of season 1.
- [ ] `REQ-009` · P0 · LIVE — Request `all`/`first`/`latest` for an existing partially complete/dormant series; verify incomplete target seasons revive while complete files and unrelated monitoring remain intact.
- [ ] `REQ-010` · P0 · API — Force the TMDB→TVDB bridge primary lookup to succeed; verify Sonarr receives the correct TVDB ID and cache is reused within its TTL.
- [ ] `REQ-011` · P0 · API — Force the primary ID bridge to miss and Trakt fallback to succeed; verify the resolved TVDB ID is used once and stored with the request.
- [ ] `REQ-012` · P0 · CHAOS — Make external-ID/Trakt resolution miss and Sonarr title fallback return no authoritative match or ambiguous/mismatched candidates; verify no wrong series is added and the user gets an actionable failure.
- [ ] `REQ-013` · P0 · UI — With season choice enabled, verify season checkboxes/status chips, select/clear, Request N seasons, and Request More include only requestable seasons.
- [ ] `REQ-014` · P0 · UI/SEC — With season choice disabled globally or for a user, verify the picker is status-only and a forged explicit list is ignored in favor of effective policy.
- [ ] `REQ-015` · P0 · UI — Set global default scope to all/first/latest/pilot; verify users inheriting policy get that scope and the choice label is accurate.
- [ ] `REQ-016` · P0 · UI — Set each per-user tri-state policy (inherit/on/off) for approval, season choice, and quality choice; verify effective options and request behavior for that user only.
- [ ] `REQ-017` · P0 · UI/LIVE — Enable quality choice and pick valid Radarr/Sonarr profiles; verify the selected profile reaches the exact arr add and is stored on pending requests.
- [ ] `REQ-018` · P0 · SEC — Disable quality choice and forge a profile ID; verify effective default profile is used and an invalid/cross-instance profile cannot be injected.
- [ ] `REQ-019` · P1 · CHAOS — Delete/change a configured quality profile after a request becomes pending; verify approval shows the stored choice and fails safely or requires an explicit valid override—never a random profile.
- [ ] `REQ-020` · P0 · UI — With approval required, submit movie, TV coarse-scope, explicit-season, ebook, audiobook, and both-format requests; verify no arr mutation yet and every queue row preserves exact user/media/options.
- [ ] `REQ-021` · P0 · LIVE — Approve each pending media type without override; verify it executes the stored request once, records approver/time, leaves the queue, updates requester history/state, and sends configured decision push.
- [ ] `REQ-022` · P0 · LIVE — Approve TV with scope/quality overrides and pending ebook/audiobook/both requests after overriding format each direction; verify only the final approved exact scope/profile/format set executes once and audit/status reflect it.
- [ ] `REQ-023` · P0 · UI — Deny with empty and supplied reasons; verify terminal Denied state, optional reason visibility, queue removal, no arr mutation, and requester notification when enabled.
- [ ] `REQ-024` · P0 · API/CHAOS — Have two admins approve/deny the same request concurrently and retry after a lost response; verify at-most-once arr execution and the loser reloads the durable winner.
- [ ] `REQ-025` · P0 · CHAOS — Make arr add/search fail during direct request or approval; verify truthful failure/pending behavior, no false Available/approved claim, and a safe retry path without duplicate add.
- [ ] `REQ-026` · P0 · API — Submit the same request rapidly/repeatedly from UI, API, and AI; verify duplicate pending/log/arr mutations are prevented while legitimate Request More/new-format requests remain possible.
- [ ] `REQ-027` · P0 · UI — Verify request buttons and detail sheets for unavailable, pending, denied, requested, downloading, partial, and available states use the correct enabled action and requester vocabulary.
- [ ] `REQ-028` · P0 · UI — For a partially available series, use Request More; verify it opens season status, prevents choosing complete/covered seasons, and requests only remaining selections.
- [ ] `REQ-029` · P0 · LIVE — Change arr state directly (manual import, delete file, unmonitor, replace queue item); verify status is recomputed live rather than trusting request history.
- [ ] `REQ-030` · P0 · UI — Inspect one user's request history as that user and another user; verify users see only their own rows while admins see only the intended pending admin queue.
- [ ] `REQ-031` · P1 · UI — Verify status progress handles unknown total, 0%, partial, 100%, failed queue, and item removed before import without NaN/false Available.
- [ ] `REQ-032` · P1 · CHAOS — Switch a user's default instance after a request; verify new availability/requests use the new instance while historical/audit context is not silently rewritten.
- [ ] `REQ-033` · P1 · UI — Change global/per-user policies while a request is pending; verify the pending row retains its submitted exact choices and admin override is explicit.
- [ ] `REQ-034` · P1 · SEC — Forge unsupported media type, bad/negative IDs, malformed season JSON, invalid scope/format/profile, and cross-service combinations; verify 4xx and zero upstream mutation.
- [ ] `REQ-035` · P1 · UI — Create and resolve pending requests while the requester/admin is on another device; verify WebSocket badges, list rows, buttons, and history converge without manual restart.
- [ ] `REQ-036` · P0 · LIVE — Verify request-pending push goes only to opted-in admins and decision push only to the original opted-in requester, with correct badge and deep link.
- [ ] `REQ-037` · P0 · API/LIVE — As a caller allowed season choice, send explicit seasons `[0,1]`; verify S00 and S01 are preserved/sorted/deduplicated and no other season is monitored/searched despite Specials being hidden in requester UI.
- [ ] `REQ-038` · P0 · LIVE — Make TMDB external IDs and Trakt miss, return one authoritative matching Sonarr title result, and verify that result's TVDB ID is persisted and the exact series is added.
- [ ] `REQ-039` · P1 · API — Exercise the ID-bridge cache just under and over 30 days; verify under-TTL reuse, expired-row refetch/replacement, and no stale cross-media/ID mapping.
- [ ] `REQ-040` · P0 · UI/API — With approval globally required and season/quality choice disabled, submit as admin; verify no pending row and valid admin explicit scope/profile choices execute exactly once without weakening requester policy.
