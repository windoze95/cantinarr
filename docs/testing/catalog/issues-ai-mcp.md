# Issues, AI, and MCP

Issue reporting and remediation, AI accounts and chat, tool settings, and all user/admin MCP contracts.

Use the [run template](../run-template.md) to record executions of these cases.

## Issues, passive recovery, and AI remediation

- [ ] `ISS-001` · P0 · UI/SEC — Toggle problem reporting off/on; verify the affordance and API are both disabled/enabled, not merely hidden.
- [ ] `ISS-002` · P0 · UI/API — Report movie, whole-series, season, exact episode, and exact S00 special problems; verify immutable media type, instance ID, arr ID, season/episode scope, and reporter are exact.
- [ ] `ISS-003` · P0 · SEC — Forge movie-on-Sonarr, TV-on-Radarr, another user's/inaccessible instance, nonexistent media, unsupported media type, or unavailable title; verify 4xx and no issue/agent side effect.
- [ ] `ISS-004` · P1 · UI/API — Exercise every category; require bounded explanation for “Something else,” preserve safe Unicode text, and reject empty/oversized/malformed input.
- [ ] `ISS-005` · P0 · API/AUTO — Report the same media/scope/instance twice and the same title on two instances; verify true duplicate handling without merging distinct incidents.
- [ ] `ISS-006` · P0 · UI — Verify a new issue starts in Watching/Recovery state with requester-safe copy, no reply/typing/completion controls, no admin attention badge, no push, and no agent run.
- [ ] `ISS-007` · P0 · LIVE — During minimum watch/quiet windows, replace/progress the queue item; verify one incident tracks the new attempt, resets quiet timing, and remains passive.
- [ ] `ISS-008` · P0 · LIVE — Complete an exact successful import during observation; verify the issue resolves silently only after matching file/import proof and configured settle time.
- [ ] `ISS-009` · P0 · LIVE — Remove a queue item without import, expose a pre-existing file, or import a different movie/episode; verify none falsely proves recovery.
- [ ] `ISS-010` · P0 · CHAOS — Return incomplete/capped/failed/out-of-order queue snapshots; verify uncertainty never masquerades as empty/recovered and durable timing remains monotonic.
- [ ] `ISS-011` · P0 · CHAOS — Keep arr proof unavailable past the allowed window; verify Needs a closer look/Needs Admin rather than false resolution or unsafely starting mutations.
- [ ] `ISS-012` · P0 · UI — Verify admin lists separate Needs attention, Tracking, and Closed; passive rows are muted/read, remain discoverable, and never inflate actionable badge.
- [ ] `ISS-013` · P0 · UI — Open an actionable issue as admin and reporter; verify admin view marks it read, reporter view does not, and later non-admin status/reply reflags it.
- [ ] `ISS-014` · P1 · UI — Toggle mark-resolved-as-read; verify clean automatic/agent resolution follows the setting while manual unread history remains truthful.
- [ ] `ISS-015` · P0 · SEC — As reporter/unrelated user/admin, read and reply; verify reporter only owns their thread, unrelated access is denied, and admin access/audit is complete.
- [ ] `ISS-016` · P0 · LIVE — Have the agent ask a reporter-only question; verify Awaiting your reply, requester-safe prompt, bounded reply, same-run resume, and no duplicate run.
- [ ] `ISS-017` · P1 · CHAOS — Let reporter timeout expire across restart; verify terminal provenance/copy is correct and a late reply cannot silently resurrect a closed issue.
- [ ] `ISS-018` · P0 · UI/API — Set investigate-only mode; run a persistent issue and verify investigation/audit can complete but no mutation proposal is stored or shown.
- [ ] `ISS-019` · P0 · UI/API — Set supervised mode; verify each supported proposal card names action, service, instance name + immutable ID, exact media/queue scope, typed params, and passive rationale.
- [ ] `ISS-020` · P0 · SEC — Verify a requester sees a read-only “waiting on an admin” proposal and cannot approve/deny via UI or direct API.
- [ ] `ISS-021` · P0 · SEC/LIVE — For release grab, verify stored/API data contains only a one-way reference; approval re-searches exact scope and rejects missing, duplicate, stale, or metadata-mismatched releases.
- [ ] `ISS-022` · P0 · SEC/LIVE — For episode or Specials issues, verify trigger/search/grab cannot broaden to season/series; movie and book scopes likewise fail closed on mismatches.
- [ ] `ISS-023` · P0 · SEC/LIVE — For normal/force manual import, refetch candidates and verify only files matching exact issue identity can execute even if approved params are tampered.
- [ ] `ISS-024` · P0 · LIVE — Approve every supported action type; confirmation must repeat exact target, one executor call occurs, durable result appears, and issue/run state advances truthfully.
- [ ] `ISS-025` · P0 · LIVE — Deny with and without note; verify the old card freezes with decision, the same run resumes investigation, and no mutation executes.
- [ ] `ISS-026` · P0 · CHAOS — Have two admins decide concurrently; verify one CAS winner, loser receives conflict/reloads winner, and executor runs at most once.
- [ ] `ISS-027` · P0 · LIVE/CHAOS — Start arr recovery immediately before and immediately after action claim; verify proposal becomes superseded, run aborts to passive recovery, and executor is not called.
- [ ] `ISS-028` · P0 · CHAOS — Kill the server after dispatch but before result persistence; on restart verify Outcome unknown/Needs Admin, no replay, and no second proposal until human verification.
- [ ] `ISS-029` · P0 · CHAOS/AUTO — Produce partial/ambiguous executor result; verify it stops at Needs Admin and never claims resolution.
- [ ] `ISS-030` · P0 · UI/API — Mark resolved and Close without fix with required bounded notes; verify actor, disposition, note, proposal supersession, run abort, and aggregate closure commit atomically.
- [ ] `ISS-031` · P0 · UI/API — Dismiss an issue and compare with reviewed completion; verify distinct provenance/copy/audit and no implication that a fix was verified.
- [ ] `ISS-032` · P0 · CHAOS — Race admin completion against another decision/recovery; verify 409 and winner reload without overwriting audit.
- [ ] `ISS-033` · P1 · UI — Reopen closed threads; verify full messages, action decisions/results, run summaries, closure provenance, ordered step ledger, token counts, and stop reason remain reachable.
- [ ] `ISS-034` · P1 · CHAOS — Fail refresh while cached issue/action/run data is visible; verify persistent stale warning/retry, retained useful data, and unsafe action controls disabled.
- [ ] `ISS-035` · P0 · API — Hit per-run tool-step, turn, token, active wall-clock, concurrency, daily-run, and reporter-reply budgets at boundaries; verify the documented safe terminal/paused state.
- [ ] `ISS-036` · P0 · CHAOS — Trigger repeated agent give-ups; verify circuit breaker disables auto-dispatch at threshold, sends one admin warning, preserves manual/supervised control, and requires deliberate re-enable.
- [ ] `ISS-037` · P0 · SEC/AUTO — Configure personal AI for the reporter and remove their included grant; verify remediation still uses only the admin's current shared provider/credential.
- [ ] `ISS-038` · P0 · LIVE — Save a valid remediation model override, change shared provider, and run; verify stale provider-bound override is ignored until a new real validation succeeds.
- [ ] `ISS-039` · P0 · SEC — Put credentials/URLs/tokens in report text, model output, tool results/errors, resume results, and audit text; verify model-facing/persisted derived data is scrubbed while authorized UI retains the original reporter message.
- [ ] `ISS-040` · P1 · API — Exercise AI health system issue creation/dedup/recovery; verify it is admin-only, never enters remediation queue, and closes with `ai_health_restored`.
- [ ] `ISS-041` · P1 · SEC — For a book issue, allow only durable exact queue/manual-import actions; unsupported title-level book mutations must fail closed.
- [ ] `ISS-042` · P1 · UI — Verify all issue statuses/provenance values, including unknown future values, render safe non-crashing labels and never expose internal arr/agent jargon to requesters.

## AI credentials, access, OAuth, and assistant chat

- [ ] `AI-001` · P0 · UI — With no personal source and no included grant, verify assistant/settings explain unavailable access and expose only legitimate setup choices.
- [ ] `AI-002` · P0 · UI/API — Grant/revoke included AI for regular and admin users; require the shared-account quota/cost warning for OAuth-backed grants and apply revocation immediately.
- [ ] `AI-003` · P0 · UI/API — Configure personal Anthropic, OpenAI, and Gemini providers while included access is absent/present/different; verify the personal selection is explicit and caller-scoped.
- [ ] `AI-004` · P0 · SEC/LIVE — Break a selected personal key/model/OAuth link; verify requests fail as personal and never silently spend the included account.
- [ ] `AI-005` · P0 · UI — Delete personal selection; verify fallback to included only when granted, otherwise unavailable, without deleting unrelated stored provider credentials unexpectedly.
- [ ] `AI-006` · P0 · SEC — Save/replace/delete personal/shared API keys and reopen settings/network/logs/DB; verify write-only flags in APIs and AES-encrypted secret storage.
- [ ] `AI-007` · P0 · LIVE — For each personal/shared provider, save a valid provider+model+key candidate; verify one real tool-free low-reasoning turn completes before atomic activation.
- [ ] `AI-008` · P0 · LIVE/CHAOS — Test invalid credential, unsupported model/access, quota/rate limit, temporary outage, timeout, malformed/empty response, and partial-stream failure; verify safe distinct category and prior working profile unchanged.
- [ ] `AI-009` · P0 · API/CHAOS — Have two admins save provider/model/key concurrently or fail one field in a multi-field save; verify no mismatched/partial shared profile becomes active.
- [ ] `AI-010` · P1 · LIVE — Verify shared health check runs at most once per 24 hours across restarts; disabling it stops background turns but never mandatory save validation.
- [ ] `AI-011` · P1 · CHAOS — Fail then recover the health check; verify one deduplicated admin system issue and clean automatic resolution on next success.
- [ ] `AI-012` · P0 · LIVE/UI — Start a personal OpenAI OAuth device flow; verify only the exact trusted HTTPS OpenAI origin opens, code/expiry/poll interval render, and tokens never pass through app state.
- [ ] `AI-013` · P0 · LIVE/UI — Complete personal OAuth; verify account identity/usage windows, model validation, selected source, restart persistence, and chat.
- [ ] `AI-014` · P0 · LIVE/UI — Repeat the device flow for admin-shared OAuth; verify only admins see shared identity/plan/usage and granted users learn only that access is included.
- [ ] `AI-015` · P1 · UI/CHAOS — Reopen, cancel, navigate away from, locally/server-expire, or fail a pending OAuth flow; verify server cleanup and no orphan authorization.
- [ ] `AI-016` · P1 · UI — Disconnect personal/shared OAuth when connected, provider changed, runtime unavailable, or model validation failed; verify removal remains possible and scope is correct.
- [ ] `AI-017` · P1 · API — On non-Linux source deployment verify OAuth runtime unavailable; on official Linux image verify private tmpfs runtime ownership/mode and successful operation.
- [ ] `AI-018` · P0 · UI/LIVE — Start chat and verify ordered SSE frames: conversation ID, text, tool start/end, media results, error if any, then `[DONE]`; app never leaves a permanent typing state.
- [ ] `AI-019` · P0 · UI — Ask for recommendations/search; verify visible tool activity and carousel cards have correct media identity, images, navigation, and remain visible during streaming.
- [ ] `AI-020` · P0 · LIVE — Ask the assistant to check status, list requests, and request movie/TV; verify effective user instance/policy/approval/season permissions match the normal UI.
- [ ] `AI-021` · P0 · SEC — As requester, prompt-inject/guess admin tools and instance IDs; verify no queue/library/history/health/release/import/mutation access.
- [ ] `AI-022` · P0 · LIVE — As admin, exercise queue/calendar/library/history/health/doctor/release/manual-import/remediation tools; verify exact instance/media scope and the same safety gates as direct UI.
- [ ] `AI-023` · P0 · CHAOS — Revoke role, device, or included grant and disable a tool while a turn is paused/running; verify dispatch reauthorization terminates safely rather than returning privileged data.
- [ ] `AI-024` · P0 · CHAOS — Lose provider/network response before/after a tool mutation; verify no automatic replay duplicates a request/grab/import/remediation action.
- [ ] `AI-025` · P0 · UI — Close/reopen assistant and send follow-ups; verify one conversation persists across navigation and keeps grounded context until its idle expiry.
- [ ] `AI-026` · P0 · API — Change user, personal/included source, provider account/key fingerprint, model, OAuth generation, restart, or fail a turn; verify prior conversation cannot be resumed under the new binding.
- [ ] `AI-027` · P1 · API — Reach byte bounds/eviction/4-hour backend inactivity and app's focused-session idle behavior; verify whole signed continuation pairs are discarded rather than truncated/corrupted.
- [ ] `AI-028` · P0 · API — Attempt a second turn for one user, exceed 16 server turns, and exceed four included turns; verify nonblocking rejection before provider call/spend and recovery when slots free.
- [ ] `AI-029` · P1 · UI — Cancel, background, disconnect, and navigate during streaming; verify resources stop/settle, composer state recovers, and partial output is labeled truthfully.
- [ ] `AI-030` · P0 · SEC — Put secrets in prompts/tool inputs/upstream errors and inspect stream, transcript, logs, debug records, and carousel; verify credential scrubber catches nested JSON, headers, URL userinfo, and query parameters.
- [ ] `AI-031` · P1 · UI — Verify provider/model catalogs, including recommended and named Codex models, tolerate unknown/deprecated models and never silently select another paid model.
- [ ] `AI-032` · P1 · CHAOS — Restart or rotate the shared OAuth credential while concurrent users chat; verify serialized account refresh and actual requesting-user tool permissions remain separate.

## AI tool settings and MCP OAuth/server

- [ ] `MCP-001` · P0 · UI/API — List all 26 tools; verify name, description, admin marker, enabled state, and documented count agree in Settings, chat, `/mcp`, README, and guide.
- [ ] `MCP-002` · P0 · UI/API — Toggle every tool off/on; verify it disappears/is rejected immediately in both chat and MCP without affecting unrelated tools.
- [ ] `MCP-003` · P0 · SEC — Enable debug for one hour; verify only tool name/timing/status/payload sizes are logged, expiry/extension works, and inputs/outputs/errors are never recorded.
- [ ] `MCP-004` · P0 · API — Fetch all well-known OAuth/protected-resource metadata; verify issuer, resource, scopes, endpoints, and content types match the deployed public origin.
- [ ] `MCP-005` · P0 · SEC/API — Dynamically register valid loopback/native/HTTPS clients and reject unsafe redirect URI changes, duplicate/mismatched redirects, and malformed metadata.
- [ ] `MCP-006` · P0 · SEC/LIVE — Complete authorization-code flow with S256 PKCE, state, exact redirect/resource/scope, and password; verify code is one-time/short-lived and wrong verifier/redirect/resource fails.
- [ ] `MCP-007` · P0 · LIVE — Complete secure-browser passkey authorization and create a first passkey from MCP login for a connect-link-only user; verify correct Cantinarr identity/device.
- [ ] `MCP-008` · P0 · API — Use an access token at `/mcp`, ordinary API, and wrong audience; verify short-lived audience binding and no cross-resource acceptance.
- [ ] `MCP-009` · P0 · SEC/API — Rotate a refresh token, replay the old token, restart server, and advance sliding lifetime boundaries; verify persistence, replay rejection, and one-year policy.
- [ ] `MCP-010` · P0 · SEC — Revoke the device/user/change role; verify active MCP calls and access/refresh tokens promptly lose authorization.
- [ ] `MCP-011` · P0 · API — Exercise Streamable HTTP initialize, GET/POST/DELETE, `Mcp-Session-Id`, reconnect, concurrent sessions, cleanup, malformed JSON-RPC, and expired/unknown session behavior.
- [ ] `MCP-012` · P1 · API — Discover prompt templates and `guide://cantinarr/agent-guide.md`; verify contents are readable, role-safe, current, and survive restart.
- [ ] `MCP-013` · P0 · SEC/AUTO — Verify requester tool enumeration excludes/denies admin tools server-side even if the client supplies a known admin tool name.
- [ ] `MCP-014` · P0 · SEC — For every tool below, repeat enabled happy path, disabled, unauthorized role, invalid input, upstream failure, and secret-redaction checks—not only the happy row.

- [ ] `MCP-015` · P1 · UI — Verify inbound MCP login copy/URLs cannot be confused with outbound OpenAI/ChatGPT device authorization.

### MCP user tools (run in chat and an external MCP client)

- [ ] `MCP-016` · P0 · LIVE — `search_movies`: search exact/ambiguous/empty/Unicode input; verify distinct TMDB movie identities and bounded results.
- [ ] `MCP-017` · P1 · LIVE — `search_movie_collections`: find a franchise and verify collection/part identities without merging unrelated remakes.
- [ ] `MCP-018` · P0 · LIVE — `search_tv_shows`: verify exact TMDB TV identities and no movie-type substitution.
- [ ] `MCP-019` · P1 · LIVE — `get_trending`: exercise movie/TV/all and day/week inputs; verify current bounded media results.
- [ ] `MCP-020` · P0 · LIVE — `get_movie_details`: verify metadata and live request status for an exact movie ID.
- [ ] `MCP-021` · P0 · LIVE — `get_tv_details`: verify metadata/seasons and live status for an exact TV ID.
- [ ] `MCP-022` · P1 · LIVE — `get_recommendations`: verify requested media type/source and distinct navigable results.
- [ ] `MCP-023` · P0 · LIVE — `check_request_status`: cover unavailable/requested/downloading/partial/available on the caller's effective instance.
- [ ] `MCP-024` · P0 · LIVE — `request_media`: request movie/TV with allowed scope; verify normal approval/policy/idempotency and no privilege bypass.
- [ ] `MCP-025` · P0 · LIVE — `list_my_requests`: verify caller-only history, accurate media/scope/status, and no other user records.
- [ ] `MCP-026` · P1 · UI/API — `display_media`: curate valid results, reject invented/mismatched IDs, and render a stable carousel without mutating media.

### MCP admin tools (run in chat and an external MCP client)

- [ ] `MCP-027` · P0 · LIVE — `get_queue`: filter combined exact instances/services and verify queue identity/status without cross-instance mixing.
- [ ] `MCP-028` · P1 · LIVE — `get_calendar`: exercise movie/series/date range and verify exact release scope/local dates.
- [ ] `MCP-029` · P0 · LIVE — `get_library`: filter media/instance/query and keep distinct library records.
- [ ] `MCP-030` · P0 · LIVE — `get_history`: verify grab/import/failure records and exact instance/media filters.
- [ ] `MCP-031` · P0 · LIVE — `trigger_search`: execute movie/series/season/episode scope and verify no broader search command.
- [ ] `MCP-032` · P0 · SEC/LIVE — `search_releases`: return exact-scope metadata plus one-way references only, never raw capabilities.
- [ ] `MCP-033` · P0 · SEC/LIVE — `grab_release`: re-search authoritative scope and grab the unique matching reference/indexer; reject stale/ambiguous/mismatched input.
- [ ] `MCP-034` · P0 · LIVE — `remove_queue_item`: exercise safe remove and explicit blocklist/re-search flags on the exact download.
- [ ] `MCP-035` · P1 · LIVE — `get_disk_space`: return each instance/disk once with correct units/free space and safe missing fields.
- [ ] `MCP-036` · P1 · LIVE — `get_arr_health`: cover healthy and indexer/client/root/disk/remote-path warnings by exact instance.
- [ ] `MCP-037` · P0 · LIVE — `diagnose_queue`: cover every Import Doctor class and verify its exact suggested next tool call/arguments.
- [ ] `MCP-038` · P0 · SEC/LIVE — `get_manual_import_candidates`: list only the exact stuck download's files/mappings/rejections with scrubbed paths/secrets as required.
- [ ] `MCP-039` · P0 · SEC/LIVE — `execute_manual_import`: normal/force import only confirmed candidates matching exact media/episode identity.
- [ ] `MCP-040` · P0 · LIVE — `remediate_queue_item`: execute remove, blocklist+search, and category hand-off with exact scope and explicit destructive choices.
- [ ] `MCP-041` · P0 · LIVE — `rescan_media`: rescan exact movie/series and verify import pass without cross-title action.
