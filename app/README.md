# Cantinarr Flutter App

The client for [Cantinarr](https://github.com/windoze95/cantinarr) -- an intelligent self-hosted media manager for Plex and Jellyfin households.

Built with Flutter; iOS, Android, and web are the shipping targets (web is embedded in the server binary). One dark, warm cinematic theme, TMDB/Trakt-powered discovery, one-tap requests with approvals, deep *arr control, books, download-client management, stuck-download diagnosis and supervised fixes, an AI assistant, and push notifications -- all app API traffic goes through the Cantinarr backend, while a ChatGPT account link explicitly hands authorization off to the external browser.

```
┌──────────────────────────────────────────────────────┐
│                   Cantinarr App                      │
│                                                      │
│  Discover · Requests · Movies · TV · Books ·         │
│  Downloads · Tautulli · Issues · AI · Settings       │
│                        │                             │
│                        ▼                             │
│         ┌─────────────────────────────────┐          │
│         │       Cantinarr Backend         │          │
│         │  REST + SSE + WebSocket + push  │          │
│         │ (discovery, requests, arr proxy,│          │
│         │   AI, issues, auth -- all keys  │          │
│         │        stay server-side)        │          │
│         └─────────────────────────────────┘          │
│                                                      │
│  (images load from the TMDB CDN; no API key needed)  │
└──────────────────────────────────────────────────────┘
```

## Design

A single dark-first, cinematic theme designed for couch browsing and high-signal admin work. Its near-black sign face, espresso/umber layers, glowing amber-gold, and ember highlights come directly from the Cantinarr logo. A static, pointer-transparent ambient gradient sits behind translucent page scaffolds, while semantic Material 3 surfaces provide restrained depth for navigation, forms, grouped settings, and action docks. The futuristic feel comes from precision, depth, and interaction—not a cold blue cyber palette.

The shared design foundation also owns typography, spacing, shape, and motion tokens. Reusable ambient-canvas, panel, section-header, media-card, and featured-hero primitives keep discovery and management screens visually related without wrapping every row in another card or adding perpetual background animation.

| Color | Hex | Usage |
|---|---|---|
| Background | `#0C0805` | Warm near-black ambient canvas |
| Surface | `#15100C` | Navigation, sheets, sign-face chrome |
| Surface variant | `#201710` | Cards, fields, grouped content |
| Raised surface | `#2A1E14` | Elevated umber panels and hero controls |
| Amber accent | `#F2AC2D` | Primary actions, active navigation, logo glow |
| Ember signal | `#F47A2E` | Secondary highlights, AI, and live signals |
| Text primary | `#F6F0E8` | Warm-cream headings and body copy |
| Text secondary | `#B7A99D` | Supporting copy and labels |
| Text muted | `#A09286` | Disabled, unavailable, and low-emphasis metadata |
| Available / success | `#72CC91` | Ready on the media server |
| Requested / pending | `#F4C66A` | Awaiting approval or acquisition |
| Downloading / info | `#D98A58` | In progress and informational state |

## Features

### Discovery & search
- **TMDB + Trakt rows** -- trending, popular movies/TV, top rated, upcoming; all proxied through the backend so keys stay server-side (poster/backdrop images load straight from the TMDB CDN).
- **Module-global search bar** -- debounced multi-search from every primary library/discovery surface. Secondary work screens hide it to avoid stacking global search above local filters. Results carry **requester-vocabulary availability chips** (Available / Partially Available / Requested -- never arr jargon), matched against the user's default library by TMDB id (title + premiere year as the fallback, so same-titled shows never borrow each other's state) and kept fresh via WebSocket pings.
- **Search-to-AI hand-off** -- a query that looks like a question (or returns nothing) lights up an AI affordance, and a floating **Ask AI** pill that surfaces when you pause on a typed query switches explicitly -- sticking until the field empties -- for prompts the heuristic would read as a title; sending opens the dedicated assistant with the prompt already in flight.

### Discover
- **Movies / TV tabs** -- discovery rows plus live library rows from the user's default instances: Downloading Soon, Recently Downloaded (ordered by when the file landed, never when the title was added -- Radarr carries the file date on the movie, Sonarr does not, so series recency comes from its import history), Airing Next (soonest air date first).
- **Headline row + spotlight hero** -- the top row and the hero above it come from whichever feed the admin selected in Settings > Discover: TMDB weekly trending (the default), Trakt trending, or TMDB's all-time popularity ranking. The row titles itself after the feed that answered ("Trending This Week" / "Trending Now" / "Popular ..."), so it never labels a trending feed as popular.
- **Cinematic title pages** -- movie/show detail opens under a full-bleed, unblurred backdrop hero: the image parallaxes at half scroll speed (with an iOS overscroll stretch-zoom and a settle-in on load), then hands off to a pinned marquee bar carrying the title and back control. Titles without artwork get a warm ambient-glow stage instead.
- **Releases tab** -- a unified movie + episode release timeline with list and month-calendar views.
- **Books tab** -- appears only for users with a Chaptarr grant: opens on a Recently Added row (newest book-file imports, eBook and Audiobook shown separately since they arrive at different times) above owned-aware book search, whose tappable results open the richer book detail where per-format request controls live (see Books). The row is absent for users with no Chaptarr grant and hides while a search is active.

### Requests
- **One-tap requesting** with status-aware labels: Request → Pending (awaiting approval) → Requested → Downloading → **Available**; partially-available shows get **Request More**, which jumps to the season picker. The ready state stays provider-neutral rather than assuming a particular playback app.
- **Season-level choice** -- per-season availability, multi-select, "Request N seasons"; shown only to users the admin has allowed to choose (others inherit the default scope).
- **Book formats** -- the eBook and Audiobook rows on book detail are the request action: tapping a still-open row requests exactly that format and names the outcome. Each row continuously translates live Chaptarr truth into Available / Downloading / Requested / Pending approval / Request denied, and a still-open row carries its own Request action instead of a state label. A monitored format is already Requested, failures stay unknown instead of becoming requestable, and only the still-open rows are tappable. A request the server confirmed is never re-offered while that book is on screen, even if the next status read has not caught up with the arr yet — only an admin's denial or newer live truth moves it back off Requested, so a requester can neither double-request nor read the lag as failure. When Chaptarr files the created record under its own canonical id, the detail page follows the server-reported `canonical_foreign_id` automatically, so ownership, downloads, and later status reads stay bound to the record the request actually created.
- **Available-file downloads** -- when enabled for the selected media instance, an exact live movie, eBook, or audiobook file can be downloaded from its requester detail; TV seasons present their available episodes as individual choices so each tap starts only one download. A global deployment allowlist and the instance's own path mappings must both cover the file, and download buttons appear only for files a server-side coverage check confirms the mappings can serve (unknown verdicts fail open; ticket issuance stays the authority).
- **Live status** -- request state and download progress update in real time over WebSocket, including changes made directly in the arrs (webhooks).
- **A request the library isn't ready for says so** -- a book accepted while Chaptarr is still importing its author reads **Waiting for library**, not Requested, with a standing explanation that the request is saved and being retried automatically and that nothing is expected of the requester. There is no spinner, ETA, or Retry button, because none of them would be true; the format stays covered and unrequestable, and the row moves itself on to Requested / Downloading / Available, or to Pending approval if the server eventually hands it to a human. Servers that don't send a wait behave exactly as before.
- **The approval queue shows what it is deciding** (admin) -- each pending row leads with the title's poster and opens the content itself on tap: movies and TV to their detail page, books to the exact Chaptarr record the request named (pinned to that library, so an approval reviewed after the admin switched drawers still lands on the right record). Artwork is best-effort decoration -- a pending book has no cover to resolve yet, and a slow TMDB is abandoned rather than held open -- so rows fall back to a media-type placeholder, and a legacy row that stored no identity is simply not tappable. Approve and deny stay where they were either way. A queue row that isn't a routine yes/no says so: a request whose automatic add already ran and failed names that and the action that actually resolves it, because approving it just replays the same failed add. A book the library couldn't match keeps Approve and Deny and says to add it in the library first. A request whose author-import wait ended -- the library declared the import failed, it was cancelled there, or the add failed along the way -- swaps Approve for **Try again**: one tap replays the add, which completes the request on the spot if the author has landed since, or re-queues the import and puts the row back under the server's automatic watch; Deny stays as the close. All of these still count toward the badge; a person really does have to act on them. Below the queue, a separate **Waiting for library** section lists the requests the server is retrying on its own -- requester, title, format, pinned library, how long it has waited, and when it was last tried -- with no Approve or Deny, because the server refuses an early approval on them. Nothing in that section is counted by the Approvals badge, the hamburger dot, or a push: those still mean a person must act. If the list can't be read, the section says so rather than showing an empty list; a server too old to have it shows no section at all.

### Media management (admin)
- **Drill-down** -- library → movie detail, or series → season → episode, with per-item download progress, quality/size, history, and messages, proxied from Radarr/Sonarr API v3 with credential fields scrubbed server-side.
- **File delivery parity** -- the Radarr movie, Sonarr episode, and Chaptarr format details expose the same per-instance download actions as requester details, backed by short-lived same-origin tickets rather than arr URLs or credentials.
- **Open in Radarr/Sonarr/Chaptarr** -- on a requester detail page, admins get a jump into the exact matching arr item, shown only once the title actually exists there (it appears right after a request adds it). Movies link to Radarr, TV to Sonarr, and books link to their grouped eBook/Audiobook records in Chaptarr.
- **Explicit safe removal** -- destructive library actions live in each row's labeled overflow menu rather than behind a swipe gesture; confirmation is mandatory and deleting files from disk is always opt-in.
- **Sonarr episode power tools** -- long-press action menus (per-episode monitoring included), episode **multi-select with batch search** (quick-select All / Undownloaded), batch **delete files**, an **All Seasons** view, per-season/series monitor toggles, **Edit Series** (profile, type, path, tags, season folders), and external links (IMDb/TheTVDB/TMDB/Trakt).
- **Monitoring reads at a glance** -- whatever Sonarr is not looking for fades: unmonitored episodes and seasons are dimmed where they sit. A season's bookmark fills only halfway whenever the season is partly monitored, either way round -- episodes left out of a monitored season, and episodes monitored on their own inside a season that is not -- so a whole bookmark means everything is being watched and a hollow one means nothing is. Neither is a state the availability line can express, because Sonarr's season statistics cannot separate the episodes left out from the ones that have simply not aired, so the episode list is read for the counts behind it.
- **Season cards grade what you asked for, then account for the rest** -- the fraction is Sonarr's own monitored-and-aired count, and everything it leaves out is split into buckets that, with the episodes on disk, add back up to the whole season -- **missing** for aired episodes nothing is working on, **downloading** for what is still transferring, **waiting to import** for what has landed and is stuck in front of the import step, and **unaired**/**unmonitored** for the rest -- so a caught-up season that is still airing reads "11/11 Episodes Available • 2 unaired" instead of looking finished, and an episode already in the queue is never reported as missing. Green is reserved for a season that is genuinely all on disk, and each count is bound to its own words so a narrow phone wraps between phrases, never between "13" and what it counts.
- **Interactive release search** -- per-episode, per-season, per-movie, and per-book: live indexer results with smart sorting, seeders/leechers, and rejection reasons; tap to grab.
- **Import Doctor** -- any stuck queue item explains itself in plain English with the raw arr messages shown for transparency, then offers ordered one-click fixes (manual/force import with candidates preview, remove, blocklist + re-search, category hand-off, rescan). One shared rule engine drives Sonarr, Radarr, and Chaptarr, mirrored from the server's classifier.

### Books (Chaptarr)
- **Per-format everything** -- a title's ebook and audiobook are separate records; the author page groups tracked titles first and gives each format its own accessible tracking control (an empty control adds and searches that format).
- **Requester book detail** -- search results stay navigable before and after a request, with author, publication metadata, synopsis, genres, and separate eBook/Audiobook request states; admins can continue into the exact live Chaptarr title. A page that can't bind to its own library record shows a plain "Your library may already have this book" pointer with each candidate's real state, tappable through to the record that owns the truth -- so a duplicate catalog listing doesn't become a duplicate request.
- **Owned-aware search** -- library titles are injected into lookup results and floated to the top with format-specific Available / Requested summaries; distinct records are never merged. A lookup row that can't be tied to exactly one library record claims no ownership and targets no library id -- it's marked "May be the same as a book listed above", and every record it could be is listed above it.
- **Full module** -- library with author drill-down, queue with Import Doctor, history, and wanted (missing / cutoff unmet).

### Downloads & Tautulli (admin)
- **Unified download queue** across SABnzbd, qBittorrent, NZBGet, and Transmission: pause/resume all or per item, remove (optionally with data; NZBGet removes the queue item only, files stay on disk), speeds, ETAs -- live via WebSocket snapshots. With several clients the default **All** view merges every queue into one list (usenet clients first, matching the client menu; each item badged with its client) with summed speed and a **master pause/resume across every client**; history merges newest-first the same way, and a client that stops responding is named in a banner instead of silently missing from the aggregate.
- **Tautulli** -- current Plex streams with quality/transcode badges, watch history, and top-stats.

### Issues & AI remediation
- **Report a problem** -- on any available title (admin-toggleable), bound to the exact active/detail Radarr, Sonarr, or Chaptarr instance and scoped to a movie, series, season, exact episode (including S00 specials -- the requester picker narrows season -> episode when the season's episode count is known), or book (with a format picker when a title exists as both ebook and audiobook); category chips plus free text. A title the caller already has an open report for shows **View your report** instead of a duplicate report entry.
- **My reports** -- the reporter inbox: for non-admins the Issues route (also reachable from Settings) lists their OWN reports in requester words only -- the server rewrites admin diagnostics at the read boundary -- and every report deep-links its thread, where a waiting fix shows whose turn it is, "Did the fix work?" carries the confirm control, and reporter-loop pushes (question asked / fix to confirm / report closed, one "My report updates" preference) land.
- **Issue threads** -- a chat-style thread per issue where the reporter, admins, and the AI agent converse; agent questions flip the issue to "Needs your reply", while inconclusive investigations surface as requester-safe **Needs a closer look** instead of pretending the problem was resolved. New **Watching the download** and **Download recovery in progress** states are passive: reporter-facing copy says the problem is being tracked quietly, without exposing arr/agent/admin workflow vocabulary, and the thread hides typing, replies, and completion controls until recovery is finished or truly needs attention. When a fix has actually been applied to something they reported, the reporter gets **This is fixed** in their own thread -- the only way a subjective report closes without an admin adjudicating content they haven't watched, and it warns that the thread ends because nothing here can be reopened. Replying instead stays the obvious "no, still wrong" path. Admins can finish any actionable thread with an explicit **Mark resolved** or **Close without fix** judgment and a required note after manual verification; concurrent changes are reloaded instead of overwritten, and **Dismiss** remains separate.
- **Week at a glance** -- the admin issue list opens with the agent scoreboard, in two clauses because there are two kinds of number. "Last 7 days" speaks outcome vocabulary: "resolved" counts every problem that ended well -- which is how admins read the word -- with attribution glued to the number ("41 resolved -- 2 by the agent · 1 by your rules · 1 by you · 37 on their own") so the total honors the week while automation claims only its own work. Closures that the closer's own verb said were not fixes stay visible but outside "resolved": "closed by you (no fix)" and "dismissed". "Right now" is state, not history: what needs you and how many rules are paused -- a rule paused in March is not something that happened this week. It sits here rather than on Agent fixes because a quiet week leaves that queue empty, and a scoreboard nobody opens during the quiet weeks cannot be what makes them legible; here the numbers also sit one tab away from the rows they count. Each stat is non-breaking, so a count never splits from the word it counts and a line may only wrap before a separator (the "·"s and the "--" both lead a wrapped line, never dangle).
- **Attention vs tracking** -- the admin issue list separates **Needs attention**, **Tracking**, and **Closed**. Tracking rows are muted and never show an unread dot, while actionable new issues and non-admin status changes retain the read/unread affordance. The drawer issue count excludes passive arr recovery; admin-toggleable "mark resolved issues as read" keeps a cleanly resolved issue from re-flagging. Open issues always load complete, so a filter is never filtering a partial set; closed history is bounded server-side and the Closed tab states how many of the total it is showing rather than presenting a truncated list as the whole record.
- **Agent fixes** -- proposed mutations render as safety-critical approval cards that prominently name the target service, instance name, and immutable instance ID alongside typed summaries, quoted parameters, and passive rationale; every execution requires confirmation showing that same target. When the server offers it, the confirmation also carries an **"Always approve this fix for this problem"** checkbox that arms a standing auto-approval rule for that exact problem-and-fix pair; rule-approved history reads "Approved automatically" with the rule's label, and a rule that pauses itself after a failed fix raises an in-app notice deep-linking the evidence. The dedicated screen keeps separate **Awaiting review** and **History** tabs; when two or more fixes await review, an **Approve all** action confirms once and approves exactly the reviewed list in a single request — each fix still runs at most once, anything already recovering or changed server-side is skipped, and the outcome reports as applied / skipped / needs attention. Issue threads retain terminal actions, run summaries, closure provenance, and links to the full step ledger. Stale proposals and concurrent decisions are reconciled against the server, so the app never claims a denial when another admin's approval won.
- **Live badges** -- Approvals / actionable Issues / Agent fixes counts in the drawer, kept current over WebSocket; quietly observed or actively retrying arr issues are tracked without adding alert pressure. A **Plex invites** entry appears (with count) only while someone is waiting on a Plex invite -- the persistent surface behind the miss-able push -- and lands on the Users screen, where waiting users carry a "Needs Plex invite" tag. A **Setup checklist** entry (with unconfigured-feature count) appears for admins until everything is configured or they mute it from the checklist.
- **Focused attention menu** -- admins can independently keep Approvals, Issues, Agent fixes, and Profile approvals pinned or show each only while requests await approval, an issue needs attention or is being tracked, a fix awaits review, or an external settings change awaits a decision. These device-local switches appear on the queue screens and in Settings, so a hidden entry can always be restored; passive tracking keeps the Issues entry available without inflating its actionable badge.

### AI assistant
- **Multi-provider chat** with incremental SSE streaming on native and web, visible tool activity, and a poster carousel for results. Every user can bring a personal Anthropic, OpenAI, or Gemini API key, or link OpenAI (OAuth) with a ChatGPT browser device code. Admins can configure the same choices as an included server profile and grant it per user. Personal overrides fail closed instead of silently spending shared quota.
- **Server-side tools** -- the assistant searches (movies, TV, and books), checks availability, and requests on your behalf; book results ride the same carousel with external covers and open the book detail page. Admins can triage queues conversationally.
- **Configuration receipts** -- explicit admin requests can update supported connected-app settings in one turn, without copying a confirmation command back into chat. Supported profile and custom-format writes return a trusted review receipt; quality-profile update receipts also lead to a one-time guarded restore when the live state still matches. Assistant prose never creates controls.
- **Persistent session** -- the focused `/assistant` workspace keeps one conversation alive across navigation (30-minute idle expiry).

### Notifications (iOS & Android)
- **Native push on both platforms** via the same `MethodChannel` -- no Flutter Firebase plugin. iOS registers with APNs in `AppDelegate.swift`; Android obtains an FCM token natively (`MainActivity.kt`) and renders the gateway's data-only messages itself (`PushMessagingService.kt`), so presentation matches iOS's always-banner behavior. Tokens register with the backend per device; taps deep-link to the right screen (detail page, approvals, issue thread...).
- `android/app/google-services.json` is committed on purpose: it holds Firebase project identifiers, not secrets (the FCM send credential lives with the push gateway). Self-builders swap it for their own Firebase app's file.
- The app-icon badge (approvals count) is iOS-only -- Android has no numeric badge API, so `setBadgeCount` is a deliberate native no-op and launchers show their standard notification dot.
- **Per-category preferences** -- request decisions, new movies, new episodes, new books (shown with Books access), Plex invite sent, my report updates, and admin-only categories (new requests, issues, agent fixes, Plex access requests), plus a test-push diagnostic.

### Settings
- **Settings search** -- a search field on the main Settings screen that reaches across every settings screen, not just the visible tiles: results come from a curated index of the app's static settings (titles, synonyms, screen and section names), each shown with a "Screen › Section" breadcrumb saying where it lives. Results are filtered by what the signed-in account can actually see (role, permissions, server capabilities). Tapping a result opens the owning screen with `?highlight=<anchor>`, which scrolls the exact control into view and flashes a fading accent ring (reduced-motion devices jump and show a static ring); results that live on the Settings screen itself dismiss the search and reveal their row in place, and action tiles (About, Generate Connect Link, Update Portal…) run directly from the results.
- **Setup Checklist** (admin) -- a live wizard at `/setup`: which features are configured and which aren't, derived by the server from actual configuration (never stored progress, so it's resumable and editable by construction). Each step opens the real settings screen and re-derives on return; unknown items from newer servers still render, which is how future features announce themselves. The optional **Completed media downloads** item points back to the module instances and completes once server roots and at least one effective instance mapping exist. The optional **Discovery rows** item sits directly under Trakt and opens **Settings > Discover**, where the headline-row source, the English-only filter, and the TMDB/Trakt credentials live; because every answer there is valid it completes on saving any choice, and while the choice is still the server's it says so -- with Trakt available (built-in or admin-supplied) the rows adopt it automatically, which this step is the only place to find out. Each section header carries its own outstanding count -- `ESSENTIALS · 1 LEFT`, `NICE TO HAVE · 6 LEFT`, and `· DONE` in green once a section is clear -- so the state is legible before scanning rows. The rows put the weight on what's unfinished: a completed one dims to a receipt (grey title, checkmark, no chevron) while an outstanding one keeps full-strength copy and ends in a **Set up** chip, so what's left is countable down the edge of the list instead of by reading every description. That chip turns red only on rows the server cannot work without -- metadata, or the arrs while *no* library exists at all -- which is the same capability rule the tile count uses, so a movies-only server never sees an alarm on Sonarr. A row with nowhere to send you (`push` is a server env var) shows no chip rather than promising a tap. Surfaced as a Settings tile with an "X of Y features configured" subtitle and a muteable drawer reminder with the remaining count. The tile's count is coloured: amber while anything is unconfigured, **red** when the server is missing something it cannot work without (no library service at all -- TMDB ships with a built-in key, so metadata only goes red on a server built without one), green once nothing is left. That red keys on *capability*, not on unticked rows -- a movies-only server never connects Sonarr and is not broken.
- **Needs-attention navigation** (admin) -- device-local parity switches for Approvals, Issues, Agent fixes, and Profile approvals control whether each queue stays pinned in the menu or appears only while it has active work. The same rows live in Settings, where each row also opens its queue -- so a queue whose menu entry is hidden always has a stable doorway, and the switch rides along as the row's trailing control.
- **Agent Auto-Approvals** (admin, Settings) -- manages the standing rules armed from the approve dialog: each card shows the rule's fixed label, Active/Paused state with the server's pause reason, and its approved/resolved track record, with Pause / Resume / Delete actions (delete confirms first; decided fixes keep their audit history).
- **Instances** (admin) -- add/edit all eight service types; test connections; set the global default (single-default invariant with takeover confirmation) or per-user default pins; assign users to a Chaptarr instance (the Books access grant); assigning a user pinned to a sibling instance asks for confirmation before removing them from it. Radarr, Sonarr, and Chaptarr forms also accept repeatable media path mappings from the path that instance reports to a read-only, server-approved Cantinarr path; no mappings means downloads are off for a new instance. When editing a saved instance the form lists the library folders the arr reports live (tap one to start a mapping) and warns on any mapping whose source path matches none of them. Instant updates (the server-managed Connect webhook) are installed automatically when a Radarr/Sonarr/Chaptarr instance is created, with the outcome reported in the create confirmation; the edit screen shows the live webhook state read from the arr itself and a **Configure instant updates** button that re-runs the install -- credentials rotate server-side and the secret never reaches the device.
- **Users** (admin) -- roles, connect links / re-invites / device links, per-user password & passkey enablement (disabling is a real revoke), included-AI grants, per-user request settings (tri-state inherit/on/off + default instances), test push. Enabling an OAuth-backed grant requires an explicit sharing/quota warning. The screen also shows each user's shared Plex email and invite state: with a linked Plex account it's a one-tap **Send Plex invite** (resend supported); otherwise **Invite in Plex…** copies the address and opens Plex's Manage Library Access page.
- **Plex Invites** (admin) -- link a Plex account via the PIN flow, pick the server and libraries invites share, and toggle **auto-invite** (a user sharing their Plex email gets invited with zero admin taps).
- **Request policy** (admin) -- global require-approval, season choice + default scope, quality choice + default profiles.
- **Devices** (admin) -- every connected device with hardware model, last-seen, "This device" badge, and revoke.
- **Credentials** (admin, write-only) -- the included server AI profile: Anthropic/OpenAI/Gemini API keys or a shared OpenAI (OAuth) connection and provider/model selection. AI saves show a testing state and succeed only after one small tool-free, low-reasoning response turn. Validation distinguishes invalid credentials, unsupported model access, exhausted quota, and temporary provider outages without exposing upstream secrets. A default-on daily shared-model test can be disabled to eliminate background usage; failures open one admin issue.
- **AI Access** (self-service) -- choose included access when the admin grants it, or configure a personal Anthropic/OpenAI/Gemini key or OpenAI (OAuth) link at any time, with or without a grant. The included panel is listed first; while included access is the active source the personal panel rolls up into a tappable one-line summary so it reads as the optional override it is. A personal provider need not match the server provider. Personal and included sources are labeled separately, keys are write-only, and a broken personal override is never replaced by surprise shared usage. Key and model are tested and saved together so a failure keeps the prior profile intact and shows the same safe actionable error category.
- **OpenAI OAuth** -- personal and admin-shared device-code flows open ChatGPT sign-in in the browser, poll until approval, perform a small response test, show the owning account's current Codex usage windows, and support disconnecting it. The model picker includes OpenAI recommended and GPT-5.6 Sol, Terra, and Luna. Passwords and OAuth tokens never pass through the app; authorization is encrypted on the server. Only admins can see shared-account identity and usage metadata.
- **AI tools** (admin) -- per-tool toggles for chat + MCP, and a one-hour debug-logging switch.
- **Configuration history** (admin) -- a durable record of AI/MCP quality-profile and custom-format writes, with the initiating admin/source, exact instance and resource, and bounded before/recorded/current differences fetched from the live service. The recorded value is labeled as applied, attempted, or intended according to the outcome. Each successfully applied quality-profile update can be restored once, only while its live state still matches; success appears immediately as a linked append-only restore record that cannot itself be restored. Custom-format history supports live comparison but not restore; generic admin-proxy or managed-webhook writes are not represented.
- **Profile Change Approvals** (admin) -- the consent surface for quality-profile changes proposed by external MCP agents at `/settings/profile-approvals`. Each pending proposal shows the server-rendered diff, the proposing admin and MCP client, and Approve/Reject: approval executes server-side against re-validated live settings (a drifted profile refuses and the agent must re-propose), rejection changes nothing on the arr. A `profile_change_pending` push (sharing the agent-fixes preference) deep-links here; decided proposals stay visible in a Recent section. The drawer's needs-attention menu carries a **Profile approvals** entry with a live pending-count badge (seeded from the list endpoint, kept fresh by `profile_change_pending`/`profile_change_decided` events, counted in the hamburger dot) and the same only-show-while-pending switch as the other admin queues.
- **AI remediation** (admin) -- master switch, auto-dispatch, reporting affordance, mark-resolved-issues-as-read, `supervised`/`investigate_only` mode, an optional remediation-only model override, step/turn/time and daily-run budgets, reporter-reply timeout, and minimum-watch / arr-quiet / recovery-settle timers that delay investigation and alerts while Radarr or Sonarr can still recover on its own. This server-owned agent always follows the currently selected admin shared provider and credential, including the shared OpenAI OAuth connection; it never uses personal credentials or per-user included-access grants. The override must pass a small response test with that shared provider, and a later provider change safely falls back to the shared model until a new override is tested.
- **Discover** (admin, under Modules) -- picks which feed backs the headline discovery row, hosts the write-only TMDB and Trakt credentials those feeds run on, and offers an English-only switch (on by default) that hides titles whose original language is not English from the discovery and recommendation rows. Search is never filtered, and a title the metadata source did not classify is kept rather than hidden. The Trakt source carries a **Recommended** tag and -- because Cantinarr ships a built-in Trakt application -- backs the headline rows out of the box; an admin client ID (entered just below) replaces the built-in app, and on a build without one the row is shown but unselectable, tag included, until an ID is added.
- **Update Portal** (admin) -- optional link to your own container-management portal (e.g. an Unraid or Portainer page). When the server sees a newer published release, an admin-only banner appears app-wide and links here (or to the update guide when unset); it's dismissible per release. The same banner slot also carries the warn-only version-skew notices: "update this app" for everyone when the app is older than the server's floor (never on web -- the served web app is always in step), and "update the server" for admins when the server is older than the app's floor; each dismisses per exact version pair. The About sheet also shows the running server version alongside the app's own version and build number — per distribution channel, so a TestFlight build, a Play build, and the web bundle a self-hosted image serves each report their own number.
- **Project links** -- the About section links to the [GitHub repository](https://github.com/windoze95/cantinarr) and the [public roadmap](https://cantinarr.com/roadmap/) (a "Request a feature" tile -- anonymous voting, no account), plus a [GitHub Sponsors](https://github.com/sponsors/windoze95) donate tile on the web bundle and desktop builds only — store payment policies keep external donation links out of the iOS/Android binaries.
- **Notifications, Passkeys, Password** -- self-service (passkey/password screens appear when admin-enabled).
- **Watch on Plex guide** -- requester-focused walkthrough (install the Plex app, sign in, accept the invite, start watching) with a **Request your invite** step: the user shares their Plex email, admins get a push pointing at the Users screen, and once the invite goes out (one-tap or auto) the card flips to "check your inbox" and the user gets a push. The Settings row always opens the guide; its switch (also offered from the guide itself) controls whether the guide appears in the menu.

### Auth
- **Connect links** -- open one and the account connects instantly (`cantinarr://connect` deep links on iOS); passwordless by default with a long-lived, auto-refreshing session.
- **First-run setup** -- the auth screen walks through server URL → admin account creation → an optional passkey offer. The scheme is optional: a bare address is probed over https first and falls back to http only when https is unreachable (a typed scheme is always respected).
- **Passkeys & passwords** -- native passkey sign-in on associated deployments (iOS/Android/Windows platform plugins, browser fallback), password login where enabled.
- **Session resilience** -- the session survives transport failures and VPN flaps; only a genuine 401 clears it. There is deliberately no logout button -- admins revoke devices server-side.
- **Separate OAuth directions** -- ChatGPT device authorization is an explicit outbound sign-in that lets Cantinarr use a personal or admin-shared Codex allowance. Cantinarr's MCP OAuth is a different inbound login that lets an external client access Cantinarr.

## Getting Started

### Prerequisites
- Flutter (stable channel), Dart SDK 3.4+
- A running [Cantinarr server](../server/)

### Run the app
```bash
cd app
flutter pub get
flutter run
```

Native iOS passkeys require iOS 16+, the Associated Domains entitlement for the server domain (`webcredentials:your.domain`), and the server publishing an AASA file with the app's `TeamID.BundleID`. Push requires a push-gateway-enabled server, plus the APNs entitlement (production) on iOS or a registered Firebase Android app (`google-services.json`, with FCM enabled on the gateway) on Android; Android 13+ additionally prompts for the notification runtime permission on first sign-in. See the [server README](../server/README.md#configuration) for the deployment env vars.

### Build for web (embedded in server)
```bash
flutter build web --release
# Output in build/web/ -- copied into the Go binary during the Docker build (or `make`)
```

## Architecture

Feature-first structure with data / logic / ui layers per feature. State is Riverpod with hand-written providers and hand-rolled `fromJson` models throughout -- no codegen. The backend is the only API surface:

| Concern | Data source | Why |
|---|---|---|
| Discovery, search, media detail | Backend `/api/discover`, `/api/media`, `/api/trakt` | TMDB/Trakt keys stay server-side |
| Poster/backdrop images | TMDB CDN (direct) + one shared tuned image cache | CDN images need no key |
| Requests & approvals | Backend `/api/requests`, `/api/admin/requests` | ID bridging + policy live server-side |
| Arr management | Backend `/api/instances/{id}/api/v3` (credential-scrubbed proxy) | API keys never reach devices; reads allowed for users, writes admin-only |
| Books | Backend proxy to Chaptarr (Readarr API v1) | Per-user grant enforced server-side |
| Media file downloads | Backend `/api/media-files/tickets` | Per-instance path capability controls the UI; the short-lived same-origin URL contains no arr host, filesystem path, or credentials |
| AI chat | Backend `/api/ai/chat` (SSE) | Tool execution stays server-side; chat uses the resolved personal provider or a granted included provider |
| Connected-app configuration history | Backend `/api/admin/external-settings-changes` | Durable AI/MCP profile/custom-format audit and live comparison; quality-profile restore payloads stay server-owned |
| Profile change approvals | Backend `/api/admin/profile-change-proposals` | External-MCP profile proposals decided in-app; plans and drift hashes stay server-owned — the device only sends approve/reject |
| AI settings | Backend `/api/ai/settings` + write-only personal credential routes | The app sees provider/model, source, validation errors, and configured booleans, never secret values |
| OpenAI OAuth | Personal `/api/ai/codex/*` or admin-shared `/api/admin/ai/codex/*` + explicit ChatGPT browser sign-in | Device code and scope-appropriate safe status reach the app; OAuth tokens remain encrypted on the server |
| Realtime | Backend `/api/ws` | Queue snapshots, status pings, badges |
| Push | Native APNs/FCM token → backend → push gateway | No Flutter Firebase plugin; Android uses the native `firebase-messaging` SDK |

Realtime consumption is provider-based: a raw event stream fans out into typed, auto-disposing providers (`downloads_queue`, `arr_queue_changed`, `request_status_changed`, `request_decision`, `issue_*`, `agent_action_*`, `remediation_autodispatch_disabled`); screens pair WS pings with silent refetch and a polling fallback, so a dead socket degrades gracefully.

## Project Structure

```
app/lib/
├── main.dart / app.dart          # Entry, MaterialApp, deep-link listener
├── core/
│   ├── config/                   # Timeouts, debounce, TMDB image URL helpers
│   ├── models/                   # BackendConnection, UserProfile, AppModule
│   ├── network/                  # Dio client + JWT refresh interceptor, WS client,
│   │                             #   shared image cache (1000 objects / 30-day stale)
│   ├── providers/                # Realtime event fan-out, instances, modules
│   ├── storage/                  # Secure tokens + stable device identity, prefs
│   ├── theme/                    # Semantic color, type, spacing, shape, motion tokens
│   └── widgets/                  # Ambient canvas, panels, heroes, media primitives, sheets...
├── features/
│   ├── auth/                     # Auth screen (setup/login/connect), passkeys, session
│   ├── ai_assistant/             # SSE chat, source-aware AI settings, media carousel, OpenAI OAuth
│   ├── chaptarr/                 # Books module: library/queue/history/wanted + doctor
│   ├── config_changes/           # Connected-app settings receipts, history, live diff + restore
│   ├── dashboard/                # Movies/TV/Releases/Books home tabs
│   ├── discover/                 # Discovery rows + multi-search (backend-proxied)
│   ├── downloads/                # Unified download-client queue + history
│   ├── issues/                   # Report-a-problem, threads, agent approvals + audit
│   ├── media_detail/             # Detail screens, season table, request surface
│   ├── media_download/           # Short-lived ticket model/service + shared download controls
│   ├── notifications/            # push registration (APNs/FCM), prefs, deep-link routing
│   ├── person/                   # Cast/crew detail sheet
│   ├── radarr/                   # Movie management: library/queue/history/wanted/calendar
│   ├── request/                  # Request buttons, options sheet, status sheet
│   ├── settings/                 # Everything under Settings (see Features)
│   ├── setup_wizard/             # Live setup checklist wizard + Plex guide
│   ├── shell/                    # App shell: navigation + search-to-AI hand-off
│   ├── sonarr/                   # TV management + episode tools + import doctor engine
│   └── tautulli/                 # Plex activity/history/stats
└── navigation/app_router.dart    # GoRouter: shell + module tab shells + guards
```

## Navigation

One authenticated shell hosts both module pages and secondary work screens over the shared ambient canvas. The chrome adapts at 900px: below it, a hamburger drawer lists modules and each module shows its pages in a floating bottom dock; at 900px+ the drawer becomes a layered persistent sidebar whose active module expands into its pages, and the bottom dock disappears. Detail, settings, approval, issue, and assistant routes therefore keep the desktop command sidebar instead of dropping users into a disconnected navigation mode. The global discovery bar appears on primary module surfaces and yields to each secondary screen's own focused controls. Line-length-sensitive surfaces (search results, chat, detail pages, settings forms) cap and center their content on desktop; modal bottom sheets cap at 640px. Sheets open through `showAppSheet` and wrap their content in `AppSheet`: the theme owns the card and the single drag handle, and the body fills the sheet width, scrolls past 85% of the screen instead of clipping, and stays clear of the keyboard and home indicator. A sheet that sizes itself (a `DraggableScrollableSheet`, e.g. person detail) opts out and paints its own card.

| Module | Tabs | Access |
|---|---|---|
| `/dashboard` | Movies · TV · Releases · Books¹ | everyone |
| `/radarr` | Library · Queue · History · Wanted · Calendar | admin |
| `/sonarr` | Library · Queue · History · Wanted · Calendar | admin |
| `/chaptarr` | Library · Queue · History · Wanted | admin |
| `/downloads` | Queue · History | admin |
| `/tautulli` | Activity · History · Stats | admin |

¹ Books appears only with a Chaptarr grant.

`/login` is the only route outside the authenticated shell. Secondary routes inside it include `/assistant`, `/detail/:type/:id` (movie/tv by TMDB id; `book` by Chaptarr foreign id, gated on the books grant — the target of book request-decision push taps and of approval-queue row taps), `/approvals`, `/issues`, `/issues/:id`, `/agent-actions`, `/agent-runs/:id`, `/settings/ai`, `/settings/chatgpt`, `/settings/agent-approval-rules`, `/settings/change-history`, `/settings/change-history/:id`, `/settings/profile-approvals`, `/settings/discovery`, `/settings/...`, `/plex-guide`, and `/setup`.

The router guard redirects unauthenticated users to `/login`, remembers safe internal deep-link targets through sign-in, centrally bounces non-admins from admin routes, and gates Books on the user's Chaptarr grant. Modules with multiple instances get an instance selector in the drawer/app bar.

## Key Dependencies

| Package | Purpose |
|---|---|
| `flutter_riverpod` | State management (hand-written providers) |
| `go_router` | Shell + stateful tab-shell routing with guards |
| `dio` | HTTP client with auth/refresh interceptor |
| `http` | Fetch-backed incremental AI chat streaming on web |
| `web_socket_channel` | Realtime backend events |
| `cached_network_image` + `flutter_cache_manager` | Tuned shared image cache |
| `flutter_secure_storage` / `shared_preferences` | Tokens + device id / lightweight prefs |
| `passkeys_ios` / `passkeys_android` / `passkeys_windows` | Native WebAuthn |
| `app_links` | `cantinarr://` connect-link deep linking |
| `url_launcher` | External links (ChatGPT device authorization, trailers, IMDb/TVDB/TMDB/Trakt, GitHub/roadmap/donate) |
| `device_info_plus` / `package_info_plus` | Device naming, version display |
| `shimmer`, `intl`, `uuid` | Loading placeholders, formatting, ids |

## Platforms & CI

- **iOS**, **Android**, and **web** are the shipping targets. Web is built in CI and embedded in the server image; iOS auto-deploys to TestFlight on `main` (manual signing via repo secrets); Android auto-deploys a signed AAB to the Play Store beta track on `main` (upload-keystore signing via repo secrets — see [docs/store-release.md](../docs/store-release.md)). macOS/Windows/Linux directories are unbuilt scaffolding.
- CI runs `flutter analyze --no-fatal-infos`, `flutter test`, and `flutter build web --release` on every PR.
- Store listing copy, graphics, and screenshots live in `android/fastlane` + `ios/fastlane` and sync to both consoles on merge (`storelisting.yml`). Screenshots are generated from the demo-data harness `test/preview/screenshot_main.dart` via `tool/screenshots/` — see [docs/store-release.md](../docs/store-release.md).

## License

See the root repository for license information.
