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

## 5. Optional — connect Hardcover

Open the instance and find the **Hardcover** section, just above Instant updates. Choose **Connect Hardcover**, then **Open Hardcover**. Enter the displayed code at [hardcover.app/link](https://hardcover.app/link) and approve Cantinarr's public catalog access. **Copy code** copies only the one-time code. Keep the dialog open: it checks automatically, including when you return to the app. If the code expires or authorization is declined, choose **Start again**. **Cancel** ends that attempt. No public server address, inbound callback, client secret, or personal developer app is needed.

Cantinarr's built-in public OAuth app requests only `read:catalog:data`. It cannot read your private library, profile, or email, or change your Hardcover account. The server verifies the catalog queries before changing the connection. Replacing a connection preserves the current one until the replacement succeeds. OAuth access and refresh tokens stay encrypted on the server and never reach the app. **Reconnect Hardcover** appears if Hardcover no longer accepts renewal; temporary provider failures preserve the connection so you can try again. Completed connections survive server restarts; unfinished codes must be started again.

Connecting turns on the **Trending Books** row at the top of the Books tab for everyone with a grant on this instance (see Discover books below). Connecting, replacing, or disconnecting refreshes the Books tab immediately. Otherwise the list is cached for 30 minutes per instance. OAuth credentials are read or renewed only when the server needs a new upstream list, rather than on every view or status check.

With multiple Chaptarr instances, **Apply to all** links the explicitly listed instances to the verified OAuth connection, replacing any existing Hardcover connections there. **Only this instance**, or dismissing the prompt, limits the change to this instance. An instance changed by another admin while the prompt was open is reported for retry. Individual failures name the affected instance; successful changes stay in place. Sharing a connection serializes renewal so instances do not reuse a rotated refresh token. Later replacements and disconnections affect only the instance being edited. Removing the last local link deletes the stored credential; account-wide revocation is available under [Hardcover's Authorized Apps](https://hardcover.app/account/api/authorized-apps).

**Use an API token instead** retains the previous setup: paste a token from Hardcover → Settings → API, allowing public catalog reads, and connect. Verification uses the catalog rather than your profile, and distinguishes missing catalog permission from rejected credentials or an unreachable provider. Applying an API token to all copies it to each listed instance independently. Older Cantinarr servers keep this token interface.

This connection serves Cantinarr's trending feed. **Chaptarr continues managing its own metadata credential**; connecting or disconnecting Hardcover here does not update Chaptarr.

## 6. Optional — let people download the files

Off by default, and deliberately two-layered. Chaptarr reports file paths but doesn't serve the bytes, so the deployment has to hand Cantinarr the files itself:

1. Mount each library read-only into the container and list the visible boundary in `CANTINARR_MEDIA_ROOTS` (for example `- /mnt/nas/books:/media/books:ro` with `CANTINARR_MEDIA_ROOTS=/media`).
2. In the instance editor, map each path Chaptarr reports to a folder inside that boundary.

A Chaptarr instance often needs several mappings — `/ebooks`, `/audiobooks`, and any per-library variants. The two sides don't have to match, and folder names never determine the book format; the format comes from the arr's own file record.

An instance offers downloads only once explicit mappings are saved for it.

## 7. Optional — listen with Audiobookshelf

Audiobookshelf manages listening and its own library scans. Give it access to the audiobook files Chaptarr imports, and confirm those files play in Audiobookshelf before connecting it to Cantinarr. Cantinarr does not copy the files or synchronize listening progress.

1. In Audiobookshelf, create an API key for an active administrator account. The integration is verified against Audiobookshelf 2.36.0; it uses API keys, not a user's legacy token.
2. In Cantinarr, open **Settings → Add Instance → Audiobookshelf**. Enter its base URL as the Cantinarr server can reach it and the administrator API key. Test the connection, then select **Shared libraries**. Selecting none allows all libraries, including future ones.
3. Set **Address users open** to the Audiobookshelf address your users can reach in a browser or app. This enables sign-in, **Open Audiobookshelf**, and **Listen in Audiobookshelf** links; leaving it blank hides those links. If users can reach the connection URL above, **Use same URL** copies it into this field. Otherwise enter the address they use; Cantinarr never copies an internal address automatically.
4. Under **User Access**, grant the people who should listen. They also need their own Chaptarr access to see the books in Cantinarr. Neither service grants the other, and Audiobookshelf has no global default.
5. Users open **Audiobookshelf access** in the menu (**Media server access** when video servers are also shared) to create an account with their chosen password or link an existing local account by signing in once. Admins can instead link or import existing accounts from **Settings → Users**.

The guide shows all account cards first, each labeled with its service and server name, then separate instructions for each service you can use. Audiobookshelf has browser and app-download links and explains its separate Chaptarr-access requirement. If video servers are also shared, their installation and sign-in instructions stay visible. Each server has its own account or invitation; use that server’s credentials.

**Hide from main navigation**, fixed below the guide title, immediately hides the mobile and desktop shortcut without closing the page. You can use it before setup is complete, while an invitation is pending, or when a connection fails. Open **Settings → Guides → Media server access** (also searchable in Settings) to switch it off again. The choice is saved on this device/browser for this Cantinarr server and user. A newly granted media-server instance — including another Audiobookshelf server — restores the shortcut until you hide it again. Renaming a server, changing account status, removing access, or a failed connection does not reset the preference. Grant information refreshes on configuration changes, reconnect, and app resume, keeping the last successful result if a read fails.

Accounts Cantinarr creates are ordinary users restricted to the selected libraries, with downloads enabled and explicit content and library editing disabled. Change additional content permissions in Audiobookshelf itself. New accounts are **Managed by Cantinarr**: removing their grant disables the account while preserving history; restoring the grant enables it again. Existing accounts linked by a user or linked/imported by an admin are **Linked only** by default. Removing their Cantinarr grant leaves their Audiobookshelf access unchanged. In **Settings → Users**, an admin can choose **Manage … access…** to apply the current grant to the existing account, or **Stop managing … access…** to leave its remote state alone and cancel pending changes. Enabling management preserves the existing library selections. **Unlink … account** forgets the connection while retaining the grant and the remote account. Root and administrator accounts are never changed. Connections present before this management feature retain their previous managed behavior on upgrade; review them in Users.

After Chaptarr reports an audiobook as **Available**, its book page offers **Listen in Audiobookshelf** only when a live exact ASIN or ISBN match proves a playable copy is visible to the linked account. Cantinarr reads the audiobook edition identifiers, including when Chaptarr serves editions separately from the book record. If several copies match, choose the server/library/narration you want. The link opens that item's browser page. Ebook-only availability does not qualify.

**Open Audiobookshelf** is a general shortcut when an exact copy cannot be verified, including before the account is linked or the new files are scanned. It also appears when Audiobookshelf cannot be reached, without a lookup notice or **Check again** button. It does not claim the audiobook is present. Book refreshes, account changes, and returning to Cantinarr refresh the lookup automatically. Library, tag, and explicit-content restrictions apply to every exact link, including changes made directly in Audiobookshelf.

## 8. Verify

- The Books tab appears for a pinned non-admin user, opening on Recently Added, Authors and Series, with native search in the top bar.
- Searching a title returns results, and requesting an eBook or Audiobook row reads **Requested** until it downloads.
- A grab that completes in Chaptarr flips the row to available within seconds, not on the next poll — that's the webhook working.
- If downloads are on, a completed book offers a working download from a device.
- If Audiobookshelf is connected, a scanned available audiobook opens the correct copy as the linked user; a copy in an unshared library never gets an exact listening link.

## Discover books

Books use the selected Chaptarr instance for search and library browsing. Search results keep Chaptarr's order and each native identity. Books appear as soon as their lookup returns; author results load independently below them. Changing the query cancels the previous search, and interactive searches stop after ten seconds. Ownership updates change badges without rearranging native book results. A catalog record can share library availability when an explicit provider identifier or validated ISBN proves the connection. Distinct results remain separate and keep their selected metadata; matching titles alone do not prove ownership. Author counts on search, the shelf, and detail count titles, so owning both formats counts once. The Books tab opens with **Trending Books** once Hardcover is connected (step 5): the top 50 on Hardcover right now, each card carrying the library's real Available / Partial / Requested state by exact `hc:` id or ISBN match, and opening the same book page a search result would. Until an admin connects Hardcover, admins see a **Connect Hardcover in Chaptarr settings** button in its place and requesters see nothing there. Recently Added, Authors, and Series follow beneath it.

Selecting a book preserves its native ID, edition, complete search record, library, and the search term that found it. The page displays all supplied synopsis text; a source-truncated preview remains as supplied. It omits standalone alternate-cover notices and shows publication information and provider links when available. A missing publication year falls back to the matched library record. Opening, hovering over, or scrolling search results makes no extra metadata lookup; direct links without a supplied record retain the exact-ID lookup and title fallback. The book page has one panel for **eBook**, **Audiobook**, and **Request both**. Its controls stay mounted while reading a long synopsis. The compact Formats card keeps request actions on the right, with download controls appearing when files are available. Genres and optional library actions follow the synopsis. Loaded details, request state, and scroll position survive same-book route refreshes; live availability, request, and download checks continue during polling and instant updates. Already-owned or requested formats show their current state. A pending availability check shows **Checking…**; **Retry** appears only after a failed check.

Unconfigured libraries show the existing **Set up Chaptarr** action. Requesters, including kids accounts, still need discovery permission and a Chaptarr grant. Setup returns to Books after saving or cancelling; tab hiding remains under **Settings > Modules > Discover > Discover tabs**.

Open Library search, Popular Books, genres, and request matching have retired. Old work and browse links show a stable message and **Search books**, prefilled when the link includes a title. Cantinarr makes no Open Library discovery or resolution calls. Chaptarr may still supply links to a book's provider pages.

## Saved requests and automatic recovery

A format tap saves the native request and immediately acknowledges it. The durable worker wakes after saving, then checks Chaptarr and delivers in the background. **Waiting for approval** remains a separate gate. Already-owned formats are reconciled without a new library mutation or approval. Temporary failures keep the request saved, retry after one minute, double up to six hours, and honor a longer upstream `Retry-After`. The schedule survives restarts. After 50 failures, or an identity/configuration problem, the request shows **Needs attention**.

The format panel offers a separate retry for each failed format and **Cancel request** for remaining work. Admins can manage saved work under **Settings > Pending requests > Saved requests**. Delivery waits do not increase the approval badge. Shared requests retain each subscriber's requested formats; cancelling a subscription preserves others, and cancelling remaining work never removes delivered files.

Delivery uses the originally selected native ID and instance, rechecks the requester's current access before writes, and never substitutes another title or library. When identifiers establish a library binding, existing formats and missing-format requests use that library record while the receipt keeps the original selection. Conflicting matches need attention rather than risking a duplicate or a wrong-book request. Saved delivery status is independent of current files: the app reads saved state with `include_live=false` and checks availability separately against Chaptarr. The existing default status read still includes live availability for older clients.

Saved history is preserved. Old Open Library requests with verified native bindings continue. Unresolved source requests stop matching and show **Needs attention**, cancellation, and a native search link. Their existing approval requirements remain intact; retry or approval cannot convert an unresolved source into a new native request.

**Waiting for library** means Chaptarr accepted an author import and owns its retry loop. Cantinarr observes its pending-import API and managed webhook without repeatedly adding it. An import that lands resumes the remaining formats; a failed, cancelled, or ambiguous import needs attention. Older Chaptarr versions without that API retain their supported add-probe fallback.

Publication details name their catalog or library source and show edition publisher/format when available. Different editions may have different page counts. Dates more than five years ahead appear as **Date unconfirmed (year)** and move to the undated end of an author's bibliography; Cantinarr keeps the source value rather than inventing a correction.
