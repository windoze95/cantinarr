# Required regression fixtures

Use these shared accounts, services, media states, and failure fixtures when executing applicable catalog cases. Keep credentials and other secrets out of evidence.

Every environment—including local, CI, disposable-lab, manual, external-service,
and physical-device runs—must use synthetic assets explicitly released as CC0
or content whose public-domain status has been verified and recorded. Never
connect a household library, copy private metadata, or acquire unlicensed media
for a test. The private lab's fixture allowlist, rights/provenance records, and
safety audit are the reference implementation of this rule.

- Two admins (`Admin A`, `Admin B`) and at least three requesters: a default user, a restricted-policy user, and a user with no Chaptarr/included-AI grant.
- Two Radarr instances and two Sonarr instances with different libraries/defaults; two Chaptarr instances; one of each download client (SABnzbd, qBittorrent, NZBGet, Transmission); Tautulli; and at least one deliberately unreachable instance.
- A Plex owner account with two owned servers and movie/show/music/photo libraries; at least four disposable registered recipient accounts, one already-shared account, one pending-but-unaccepted account, and one unregistered email. Put a uniquely named marker item in every test library, and ensure the owner can remove shares plus create/delete temporary libraries between cases.
- Movie fixtures in unavailable, pending, requested, downloading, partial, and available states; TV fixtures with Specials, missing seasons, a missing episode, an unmonitored season, and an ended complete show.
- Book fixtures with ebook-only, audiobook-only, both formats, monitored-without-file, duplicate-title records, missing, and cutoff-unmet states.
- Queue fixtures for normal progress and every Import Doctor class: sample, archive/unpack, TheXEM mapping, not-an-upgrade, unparseable/invalid, remote path, client unavailable, stalled torrent, permission failure, and a valid manual-import candidate.
- Two physical push-capable devices for one account, another device for an admin, and one stale/dead push token.
- Personal and shared test credentials for every enabled AI provider, plus invalid-key, no-model-access, quota-exhausted (or mocked equivalent), and temporarily unavailable fixtures.
