# Music automation setup

Music works like books, minus the part that makes books complicated:

- **Lidarr has no global default instance.** The per-user pin *is* the access grant, so a user who hasn't been pinned to a Lidarr instance doesn't see the Music tab at all — exactly the Chaptarr rule.
- **One album is one record.** There is no eBook/Audiobook-style format split, so a request is a single tap and a single status.
- **A single can finish downloading between two polls.** Instant updates aren't a nicety here; they're what makes the "ready to play" notification reliable.

This page is the whole path, in order.

## 1. Have a working Lidarr

Cantinarr manages an existing Lidarr instance — it doesn't deploy one. What it needs from you:

- A URL the **Cantinarr server** can reach. Clients never dial instance URLs, so cluster-internal names are fine and preferred; see the [instance URL guidance](../README.md#configuration).
- A Lidarr API key.
- Lidarr itself already working: a root folder, an indexer, a download client, and grabs that actually complete.

Lidarr fetches artist and album metadata from its own metadata service (api.lidarr.audio) at add time, so the server running Lidarr needs outbound access to it — an add made while that service is down fails immediately with a clear error rather than sitting in a queue.

## 2. Add the instance

**Settings → Add Instance**, service type `lidarr` (the Setup Checklist's **Music** row opens this same form with Lidarr already selected), then the URL and API key. Save runs a live connection check from the server — the same host that will dial it in production — so a green result means what it says.

Lidarr speaks the Servarr `/api/v1` API. Enter just the base URL; Cantinarr appends the API path.

## 3. Grant access per user

This is the step people miss, and it works exactly like books. Unlike Radarr and Sonarr, Lidarr has no global default — pinning a user to a Lidarr instance is how you grant that user access to music.

Pin from either side: the instance editor, or **Settings → Users** for one person. Un-pinning revokes access. Admins see Lidarr without a pin; everyone else needs one, and until they have it `services.lidarr` stays `false` and the Music tab stays hidden.

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

- The Music tab appears for a pinned non-admin user, opening on **Recently Added** and **Artists** rows once the library holds something.
- Searching an album or artist returns results, and requesting an album reads **Requested** until it downloads.
- A grab that completes in Lidarr flips the album to available within seconds, not on the next poll — that's the webhook working.
- If downloads are on, an owned album's detail offers its tracks as working downloads from a device.

## Requests that land in the approval queue instead

Adding an album Lidarr doesn't already track means finding its metadata record again. Cantinarr fetches it by id first — Lidarr answers a `lidarr:<id>` term with that exact record — and falls back to replaying the requester's own search term (stored on pending rows so approval later uses it too), then the title forms. Only an exact id match is ever accepted: MusicBrainz merges release-groups, and when the provider declares the requested id an alias of a record the library already tracks, the request completes that record instead of creating a twin.

When no term finds it, the request is **saved as pending** rather than failed, and the requester is told so. Resolve it from the admin side: add the artist (or the album) in Lidarr directly, then approve the pending request — approval replays the add. Denying it is the other valid answer. Either way the request stays visible instead of disappearing.
