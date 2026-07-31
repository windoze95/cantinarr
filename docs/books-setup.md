# Book automation setup

Books differ from movies and TV in two ways worth knowing before you start:

- **Chaptarr has no global default instance.** The per-user pin *is* the access grant, so a user who hasn't been pinned to a Chaptarr instance doesn't see the Books tab at all.
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

**Settings → Add Instance**, service type `chaptarr`, then the URL and API key. Save runs a live connection check from the server — the same host that will dial it in production — so a green result means what it says.

Chaptarr speaks the Readarr `/api/v1` API. Enter just the base URL; Cantinarr appends the API path.

## 3. Grant access per user

This is the step people miss. Unlike Radarr and Sonarr, Chaptarr has no global default — pinning a user to a Chaptarr instance is how you grant that user access to books.

Pin from either side: the instance editor, or **Settings → Users** for one person. Un-pinning revokes access. Admins see Chaptarr without a pin; everyone else needs one, and until they have it `services.chaptarr` stays `false` and the Books tab stays hidden.

Running more than one Chaptarr instance is fine — pin different households or different libraries to different instances.

## 4. Turn on instant updates

Open the instance and tap **Configure instant updates**. The server rotates a per-instance credential and installs its own authenticated webhook in Chaptarr; the secret moves server-to-server and never reaches a device.

Set `CANTINARR_PUBLIC_URL` first if Cantinarr sits behind a reverse proxy. The callback has to be resolvable **from inside the Chaptarr container**, so in Docker or Kubernetes a cluster-internal origin like `http://cantinarr:8585` is usually the right value.

Without this, Cantinarr falls back to polling, and a fast ebook grab can land and be announced late — or, if it imports and finishes between two polls, look like nothing happened.

## 5. Optional — let people download the files

Off by default, and deliberately two-layered. Chaptarr reports file paths but doesn't serve the bytes, so the deployment has to hand Cantinarr the files itself:

1. Mount each library read-only into the container and list the visible boundary in `CANTINARR_MEDIA_ROOTS` (for example `- /mnt/nas/books:/media/books:ro` with `CANTINARR_MEDIA_ROOTS=/media`).
2. In the instance editor, map each path Chaptarr reports to a folder inside that boundary.

A Chaptarr instance often needs several mappings — `/ebooks`, `/audiobooks`, and any per-library variants. The two sides don't have to match, and folder names never determine the book format; the format comes from the arr's own file record.

An instance offers downloads only once explicit mappings are saved for it.

## 6. Verify

- The Books tab appears for a pinned non-admin user, opening on **Recently Added**.
- Searching a title returns results, and requesting an eBook or Audiobook row reads **Requested** until it downloads.
- A grab that completes in Chaptarr flips the row to available within seconds, not on the next poll — that's the webhook working.
- If downloads are on, a completed book offers a working download from a device.

## Requests that land in the approval queue instead

Adding a book Chaptarr doesn't already track means finding its metadata record again. Cantinarr fetches it by id first — Chaptarr's lookup answers a `foreignBookId` term with that exact record — and falls back to replaying the requester's own search term (stored on pending rows so approval later uses it too), then the exact title, then the title's headline without its subtitle and trailing parentheticals. Only an exact `foreignBookId` match is ever accepted: when the metadata provider keeps two works for one title, the id fetch of one may return the other (canonical) sibling, and the fallbacks re-find the exact row the requester chose instead of substituting it.

When none of them find it, the request is **saved as pending** rather than failed, and the requester is told so. Resolve it from the admin side: add the author (or the book) in Chaptarr directly, then approve the pending request — approval replays the add, and a book whose author is already tracked no longer depends on the lookup. Denying it is the other valid answer. Either way the request stays visible instead of disappearing.
