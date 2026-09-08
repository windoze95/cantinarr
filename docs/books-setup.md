# Book automation setup

Books differ from movies and TV in two ways worth knowing before you start:

- **Chaptarr has no global default instance.** Requesters need a per-user pin or explicit instance grant to see Books. Admins see the Chaptarr setup action until a library is connected.
- **An ebook can finish downloading between two polls.** Instant updates aren't a nicety here; they're what makes the "ready to read" notification reliable.

This page is the whole path, in order.

## 1. Have a working Chaptarr

Cantinarr manages an existing Chaptarr instance — it doesn't deploy one. What it needs from you:

- A URL the **Cantinarr server** can reach. Clients never dial instance URLs, so cluster-internal names are fine and preferred; see the [instance URL guidance](../README.md#configuration).
- A Chaptarr API key.
- Chaptarr itself already working: a root folder, an indexer, a download client, and grabs that actually complete.

That last point is the one that eats a weekend. If you're building the Chaptarr side from scratch — particularly routing Chaptarr and its torrent client through a VPN gateway, where the container topology is easy to get subtly wrong — [`mam-chaptarr-protonvpn-skill`](https://github.com/windoze95/mam-chaptarr-protonvpn-skill) is an agent skill that walks the whole build: one Gluetun/ProtonVPN namespace per environment, qBittorrent and Chaptarr attached to it, forwarded-port sync, indexer and tracker-host sessions kept separate, and verification that refuses to accept "the WebUI loads" as proof it works. It's an independent project, not part of Cantinarr, and it covers the layer *below* this page.

One thing that carries straight over: if Chaptarr shares a gateway's network stack (`network_mode: container:<gateway>`), it has no address of its own, so the instance URL you give Cantinarr must name the gateway that publishes the port.

## 2. Add the instance

**Settings → Add Instance**, service type `chaptarr` (the Setup Checklist's **Books** row opens this same form with Chaptarr already selected), then the URL and API key. Save runs a live connection check from the server — the same host that will dial it in production — so a green result means what it says.

Chaptarr speaks the Readarr `/api/v1` API. Enter just the base URL; Cantinarr appends the API path.

Use Chaptarr's **root** URL, never one of its media-scoped prefixes (`/ebook`, `/audiobook`, or a `/readarr/...` compatibility path). Those prefixes exist for Readarr-only clients and change how Chaptarr answers identity lookups; Cantinarr talks to the native API and handles both formats itself.

## 3. Grant access per user

This is the step people miss. Unlike Radarr and Sonarr, Chaptarr has no global default — pinning a user to a Chaptarr instance is how you grant that user access to books.

Pin from either side: the instance editor, or **Settings → Users** for one person. Remove both the pin and any explicit grants to revoke access. Admins see Books before setup unless it was conditionally hidden for the server, and see configured Chaptarr instances without a pin; everyone else needs one, and until they have it `services.chaptarr` stays `false` and the Books tab stays hidden.

Running more than one Chaptarr instance is fine — pin different households or different libraries to different instances.

## 4. Check instant updates

Adding the instance already turned these on: the server rotates a per-instance credential and installs its own authenticated webhook in Chaptarr the moment the instance is created; the secret moves server-to-server and never reaches a device. The create confirmation says whether it worked.

If it couldn't — most commonly because the callback wasn't reachable — open the instance: the **Instant updates** section shows the live state, read from Chaptarr itself, and **Configure instant updates** re-runs the install. Set `CANTINARR_ARR_CALLBACK_URL` first if Cantinarr sits behind a reverse proxy. The callback has to be resolvable **from inside the Chaptarr container**, so in Docker or Kubernetes a cluster-internal origin like `http://cantinarr:8585` is usually the right value.

Without this, Cantinarr falls back to polling, and a fast ebook grab can land and be announced late — or, if it imports and finishes between two polls, look like nothing happened.

The webhook also speeds up "Waiting for library" requests: Chaptarr announces the moment a queued author import lands, and Cantinarr completes the waiting request right then instead of on its next five-minute check.

## 5. Optional — let people download the files

Off by default, and deliberately two-layered. Chaptarr reports file paths but doesn't serve the bytes, so the deployment has to hand Cantinarr the files itself:

1. Mount each library read-only into the container and list the visible boundary in `CANTINARR_MEDIA_ROOTS` (for example `- /mnt/nas/books:/media/books:ro` with `CANTINARR_MEDIA_ROOTS=/media`).
2. In the instance editor, map each path Chaptarr reports to a folder inside that boundary.

A Chaptarr instance often needs several mappings — `/ebooks`, `/audiobooks`, and any per-library variants. The two sides don't have to match, and folder names never determine the book format; the format comes from the arr's own file record.

An instance offers downloads only once explicit mappings are saved for it.

## 6. Verify

- The Books tab appears for a pinned non-admin user, opening on Recently Added, Authors and Series, with native search in the top bar.
- Searching a title returns results, and requesting an eBook or Audiobook row reads **Requested** until it downloads.
- A grab that completes in Chaptarr flips the row to available within seconds, not on the next poll — that's the webhook working.
- If downloads are on, a completed book offers a working download from a device.

## Discover books

Books use the selected Chaptarr instance for search and library browsing. Search results keep Chaptarr's order and each native identity. Books appear as soon as their lookup returns; author results load independently below them. Changing the query cancels the previous search, and interactive searches stop after ten seconds. Ownership updates change badges without rearranging native book results. A catalog record can share library availability when an explicit provider identifier or validated ISBN proves the connection. Distinct results remain separate and keep their selected metadata; matching titles alone do not prove ownership. Author counts on search, the shelf, and detail count titles, so owning both formats counts once. Recently Added, Authors, and Series remain available on the Books tab.

Selecting a book preserves its native ID, metadata, library, and the search term that found it. The book page has one panel for **eBook**, **Audiobook**, and **Request both**. Its controls and scroll position remain in place during polling, refresh failures, and instant updates. Already-owned or requested formats show their current state.

Unconfigured libraries show the existing **Set up Chaptarr** action. Requesters, including kids accounts, still need discovery permission and a Chaptarr grant. Setup returns to Books after saving or cancelling; tab hiding remains under **Settings > Modules > Discover > Discover tabs**.

Open Library search, Popular Books, genres, and request matching have retired. Old work and browse links show a stable message and **Search books**, prefilled when the link includes a title. Cantinarr makes no Open Library discovery or resolution calls. Chaptarr may still supply links to a book's provider pages.

## Saved requests and automatic recovery

A format tap saves the native request and immediately acknowledges it. The durable worker wakes after saving, then checks Chaptarr and delivers in the background. **Waiting for approval** remains a separate gate. Already-owned formats are reconciled without a new library mutation or approval. Temporary failures keep the request saved, retry after one minute, double up to six hours, and honor a longer upstream `Retry-After`. The schedule survives restarts. After 50 failures, or an identity/configuration problem, the request shows **Needs attention**.

The format panel offers a separate retry for each failed format and **Cancel request** for remaining work. Admins can manage saved work under **Settings > Pending requests > Saved requests**. Delivery waits do not increase the approval badge. Shared requests retain each subscriber's requested formats; cancelling a subscription preserves others, and cancelling remaining work never removes delivered files.

Delivery uses the originally selected native ID and instance, rechecks the requester's current access before writes, and never substitutes another title or library. When identifiers establish a library binding, existing formats and missing-format requests use that library record while the receipt keeps the original selection. Conflicting matches need attention rather than risking a duplicate or a wrong-book request. Saved delivery status is independent of current files: the app reads saved state with `include_live=false` and checks availability separately against Chaptarr. The existing default status read still includes live availability for older clients.

Saved history is preserved. Old Open Library requests with verified native bindings continue. Unresolved source requests stop matching and show **Needs attention**, cancellation, and a native search link. Their existing approval requirements remain intact; retry or approval cannot convert an unresolved source into a new native request.

**Waiting for library** means Chaptarr accepted an author import and owns its retry loop. Cantinarr observes its pending-import API and managed webhook without repeatedly adding it. An import that lands resumes the remaining formats; a failed, cancelled, or ambiguous import needs attention. Older Chaptarr versions without that API retain their supported add-probe fallback.

Publication details show edition publisher/format when available. Different editions may have different page counts. Dates more than five years ahead appear as **Date unconfirmed (year)** and move to the undated end of an author's bibliography; Cantinarr keeps the source value rather than inventing a correction.
