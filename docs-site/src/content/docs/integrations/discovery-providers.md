---
title: Catalogs and discovery providers
description: Know where title information comes from and which provider settings affect which tabs.
sidebar:
  order: 12
---

Catalog metadata and your own library are different sources. A catalog can know about a title long before your library has a file, and a library can be temporarily unreadable while catalog search still works.

## Movies and TV

TMDB supplies core movie and TV discovery and search. Cantinarr includes a built-in public key, so first-time browsing works without a personal TMDB registration. Administrators can supply their own token in settings.

Trakt can provide a trending source and assist TV identity bridging. Configure its client ID through the discovery or credential settings. The headline source follows the available configuration and selected discovery preference.

**Only show English-language titles** affects movie and TV discovery and recommendation rows. It starts on. Ordinary search still finds titles in other languages, subject to account content policy.

**Show 4K badges** marks movie and show covers whose library copy is 4K, for everyone on the server. It starts off. See [4K on covers](/use/status/#4k-on-covers) for how a title qualifies.

Streaming-service filters depend on the selected region. Catalog streaming information is not proof of access to your self-hosted library or to a commercial subscription.

## Books and Hardcover

Native book search and library state come from the selected Chaptarr instance. For **Trending Books**, open that instance's settings and choose **Connect Hardcover**, or configure its supported API-token connection.

The selected connection belongs to the instance. Deliberate sharing of a connection does not make every user entitled to every Chaptarr library. Existing instance authorization still applies.

Open Library discovery and resolution are retired. Old links show a stable explanation and a way back to book search. See [book setup](/integrations/guides/books/).

## Music

MusicBrainz supplies album, release, and artist identities. ListenBrainz supplies popularity charts. Cover Art Archive and its artwork hosts supply covers.

No personal account or API key is required for the standard music discovery flow. The server needs internet access to those services. New Releases focuses on recent dated albums and EPs, while search also includes singles.

## Freshness and failures

External metadata can be cached, while availability is read from your connected library. A cached cover or title does not make a stale library snapshot authoritative.

If one provider fails, read the specific notice. Usable results may remain while a separate artist, author, or availability read is unavailable. **No matches** should be interpreted only within the source and filters actually searched.

For a missing title or tab, use [missing-content troubleshooting](/troubleshooting/missing-content/).
