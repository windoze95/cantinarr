# Music automation setup

Music works like books, minus the part that makes books complicated:

- **Lidarr has no global default instance.** A per-user pin or an explicit access grant authorizes the instance, so a requester without either doesn't see the Music tab at all — exactly the Chaptarr rule.
- **One album is one record.** There is no eBook/Audiobook-style format split, so a request is a single tap and a single status.
- **A single can finish downloading between two polls.** Instant updates aren't a nicety here; they're what makes the "ready to play" notification reliable.

This page is the whole path, in order.

## 1. Have a working Lidarr

Cantinarr manages an existing Lidarr instance — it doesn't deploy one. What it needs from you:

- A URL the **Cantinarr server** can reach. Clients never dial instance URLs, so cluster-internal names are fine and preferred; see the [instance URL guidance](../README.md#configuration).
- A Lidarr API key.
- Lidarr itself already working: a root folder, an indexer, a download client, and grabs that actually complete.

Lidarr fetches artist and album metadata from its own metadata service (api.lidarr.audio) at add time, so the server running Lidarr needs outbound access to it — Cantinarr saves a request before delivery and retries temporary failures while that service is down.

## 2. Add the instance

**Settings → Add Instance**, service type `lidarr` (the Setup Checklist's **Music** row opens this same form with Lidarr already selected), then the URL and API key. Save runs a live connection check from the server — the same host that will dial it in production — so a green result means what it says.

Lidarr speaks the Servarr `/api/v1` API. Enter just the base URL; Cantinarr appends the API path.

## 3. Grant access per user

This is the step people miss, and it works exactly like books. Unlike Radarr and Sonarr, Lidarr has no global default — pinning a user or granting an instance gives that user access to music.

Pin from either side: the instance editor, or **Settings → Users** for one person. Remove both the pin and any explicit grants to revoke access. Admins see Lidarr without a grant; everyone else needs one, and until they have it `services.lidarr` stays `false` and the Music tab stays hidden.

Running more than one Lidarr instance is fine — pin different households or different libraries to different instances.

## 4. Check instant updates

Adding the instance already turned these on: the server rotates a per-instance credential and installs its own authenticated webhook in Lidarr the moment the instance is created; the secret moves server-to-server and never reaches a device. The create confirmation says whether it worked.

If it couldn't — most commonly because the callback wasn't reachable — open the instance: the **Instant updates** section shows the live state, read from Lidarr itself, and **Configure instant updates** re-runs the install. Set `CANTINARR_ARR_CALLBACK_URL` first if Cantinarr sits behind a reverse proxy. The callback has to be resolvable **from inside the Lidarr container**, so in Docker or Kubernetes a cluster-internal origin like `http://cantinarr:8585` is usually the right value.

Without this, Cantinarr falls back to polling, and a fast grab can land and be announced late — or show up on the Music tab only on the next 30-second check instead of right away. The "New music available" push rides the same webhook, so instant updates are what make it land the moment an album imports.

## 5. How requests pick profiles

Requests never ask the user to choose quality — the instance's own configuration decides, deterministically:

- The root folder is the first accessible one Lidarr reports.
- The quality and metadata profiles come from that root folder's **defaults** (Lidarr's root folders carry per-folder default profiles — set them in Lidarr under Settings → Media Management → Root Folders). A folder without a default falls back to the first profile of each kind, skipping Lidarr's hidden "None" metadata profile.

A request adds the artist with only the requested album monitored, so one request never subscribes an artist's whole discography. Adding more of an artist later is more requests — or the admin monitoring albums directly in the Lidarr module.

## 6. Optional — let people download the files

Off by default, and the same two layers as every other module. Lidarr reports file paths but doesn't serve the bytes, so the deployment has to hand Cantinarr the files itself:

1. Mount each library read-only into the container and list the visible boundary in `CANTINARR_MEDIA_ROOTS` (for example `- /mnt/nas/music:/media/music:ro` with `CANTINARR_MEDIA_ROOTS=/media`).
2. In the instance editor, map each path Lidarr reports to a folder inside that boundary.

An instance offers downloads only once explicit mappings are saved for it. With them in place, an owned album's detail page grows a **Download tracks** button: albums are delivered per track — each row named by its track number and title, with quality and size — never repackaged into an archive.

## 7. Verify

- The Music tab appears for a granted non-admin user, opening on **Popular Albums → New Releases → Browse by genre → Recently Added → Artists**. Library rows appear once the library holds something.
- Searching an album or artist returns results, and requesting an album reads **Requested** until it downloads.
- A grab that completes in Lidarr flips the album to available within seconds, not on the next poll — that's the webhook working.
- If downloads are on, an owned album's detail offers its tracks as working downloads from a device.

## Saved requests and automatic recovery

Search shows one list on desktop and mobile: albums, EPs, and singles from MusicBrainz, with artists loading independently below them. Live library records and saved requests attach by exact MusicBrainz ID in the selected Lidarr instance. Matching library-only entries supplement the catalog; same-title releases and same-name artists stay separate. An artist opens a paginated discography even before Lidarr knows that artist. Search, discovery, library entries, and discographies all open the same album page. Back keeps the query, loaded pages, instance, and your place.

A search has a ten-second deadline, including provider pacing and retries; changing the query cancels abandoned work. Album results do not wait for artist or availability reads. A provider failure leaves usable results visible and says what could not be checked. Album credits link by artist ID. New requests use the album's release-group ID; existing catalog references, including explicit release IDs resolved to release groups, remain supported. Only verified canonical aliases can change that identity.

Requests are saved and acknowledged before any Lidarr or MusicBrainz call, then a durable worker handles delivery. The album page keeps one control: **Request**, **Waiting for approval**, **Requested**, **Downloading**, **Available**, or **Needs attention**. Approval is a policy decision; a provider outage leaves an approved request **Requested**, with a message explaining the delivery wait. Saved receipt refreshes never wait for live availability, and failed refreshes preserve the accepted receipt and controls. Retries start after one minute, double up to six hours, honor longer upstream `Retry-After` values, and survive restarts. Fifty failed attempts or a configuration/identity problem leaves the request saved with **Needs attention**. The album page and the admin **Saved requests** list offer retry and cancellation. Delivery waits do not appear in the approval badge. Admins can still inspect and cancel saved requests after the original instance is removed; retrying requires the original instance and current access.

Every attempt uses the original authorized Lidarr instance and re-reads its current albums before changing anything. If a previous add succeeded but its response was lost, the next attempt reconciles the existing album. Only the requested album is monitored. Library changes and MusicBrainz canonical aliases remain authoritative; a completed delivery is not a stored availability claim.

## Discover albums without a search term

Admins can browse music feeds, genres, artwork, and cold album links before connecting Lidarr. **Connect Lidarr to request music** opens the existing instance form with Lidarr selected; saving returns to the album and enables its request controls. The Music tab has a fixed **Set up Lidarr** / **Hide this tab** footer until a Lidarr instance is configured. Hiding affects everyone and can be changed under **Settings > Modules > Discover > Discover tabs**. A configured instance restores the tab automatically, even when offline; removal makes the saved preference apply again. The footer replaces the toolbar setup shortcut, and setup returns to the catalog after saving or cancelling. Older servers keep setup and disable Hide with an update explanation. Library rows and status badges need a connected instance. For requesters, discovery works as soon as the account has access to a Lidarr instance. No ListenBrainz or MusicBrainz account, API key, or deployment setting is needed. This is also an explicit grant for a kids account: music has no age ratings.

The opening Popular Albums and New Releases pages start loading when you enter Discovery, before opening Music. Once a row or grid opens, it fetches one page ahead and preloads nearby covers. Warmed metadata stays separate from live Lidarr availability, and access changes clear it.

- **Popular Albums** follows ListenBrainz's release-group chart order after keeping albums and EPs. Choose **This week**, **This month**, or **This year**.
- **New Releases** shows albums and EPs from today and the preceding 29 calendar days, newest first. Future releases and dates without a known day are excluded.
- **Browse by genre** offers Pop, Rock, Hip-Hop, R&B, Electronic, Jazz, Classical, Metal, Country, Folk, Blues, and Reggae. MusicBrainz orders these by its tag matching, not popularity.

**See all** opens a paginated grid. The period, genre, and selected Lidarr instance travel in the link; opening an album and going back keeps your place. Covers come from Cover Art Archive through Cantinarr, with an album icon when no cover is available. Repeated appearances of one MusicBrainz ID are shown once; distinct IDs with the same title remain separate.

External metadata is cached for one hour for feeds, six hours for genre searches, and 24 hours for album details and covers. A failed row offers Retry; a failed refresh keeps the previous results with a notice. Library availability still comes from the existing live music-status reads and instant updates. Requesting a discovered album follows the same direct-request or approval path as search, including durable delivery retries and saved requests that need attention.

Cantinarr must be able to reach ListenBrainz, MusicBrainz, Cover Art Archive, and its Internet Archive artwork hosts. These calls honor the server's outbound proxy. The TMDB/Trakt source and English-only settings apply to movies and TV. Older callers retain the album/EP search default unless they send `include_singles=true`; existing native and catalog-reference request payloads remain accepted.
