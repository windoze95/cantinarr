# Required regression fixtures

Use these shared accounts, services, media states, and failure fixtures when executing applicable catalog cases. Keep credentials and other secrets out of evidence.

Every environment—including local, CI, disposable-lab, manual, external-service,
and physical-device runs—must use synthetic assets explicitly released as CC0
or content whose public-domain status has been verified and recorded. Never
connect a household library, copy private metadata, or acquire unlicensed media
for a test. The private lab's fixture allowlist, rights/provenance records, and
safety audit are the reference implementation of this rule.

- Two admins (`Admin A`, `Admin B`), a default requester, and a requester with no Chaptarr/included-AI grant.
- One instance of each supported service — Radarr, Sonarr, Chaptarr, SABnzbd, qBittorrent, NZBGet, Transmission, and Tautulli — plus a second instance of at least one arr for the admin exploratory session.
- A Plex owner account with two owned servers plus a shared server and player resources, and movie/show/music/photo libraries; at least four disposable registered recipient accounts, one already-shared account, one pending-but-unaccepted account, and one unregistered email. Put a uniquely named marker item in every test library, and ensure the owner can remove shares plus create/delete temporary libraries between cases.
- Movie and TV fixtures that can be walked through unavailable, requested, downloading, partial, and available states; a TV fixture with Specials and enough real seasons for a noncontiguous selection.
- A book fixture requestable in both ebook and audiobook formats.
- Stuck queue fixtures covering the Import Doctor fix paths — remove, blocklist + re-search, category hand-off, and rescan — plus a valid manual-import candidate.
- Two physical push-capable devices for one account, another device for an admin, and one stale/dead push token.
- Personal and shared test credentials for every enabled AI provider, including a real ChatGPT account for the OAuth device flows.
