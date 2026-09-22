---
title: Movies and TV with Radarr and Sonarr
description: Connect movie and TV libraries, choose request defaults, and verify the full request path.
sidebar:
  order: 1
---

Cantinarr works with existing Radarr and Sonarr installations. Before connecting them, make sure each service has a root folder, quality profiles, working indexers, and a configured download client.

## Add the connection

1. Open **Settings > Add Instance**.
2. Choose Radarr or Sonarr and give it a clear name.
3. Enter the base URL, such as `http://radarr:7878` or `http://sonarr:8989` on a shared Docker network.
4. Copy the service's API key from its own settings into Cantinarr.
5. Test and save the instance.

If the service is installed under a URL base, include that base path. Do not append the versioned API endpoint. A service name only works when Cantinarr's network can resolve and reach it.

## Select defaults and access

Choose the global default for each service, then review per-user settings and additional library grants. Existing requests stay pinned to their recorded library when defaults change.

Under **Request Settings**, set approval rules, season choice, quality choice, and default profiles. Profiles are read from the connected service. A name such as “HD” can mean different things on different instances.

## Check instant updates

Cantinarr attempts to install its authenticated webhook when the instance is created. Inspect **Instant updates** in the saved instance and repair the callback connection if necessary.

Radarr and Sonarr must be able to reach `CANTINARR_ARR_CALLBACK_URL`. The address can be internal. See [instant updates](/integrations/instant-updates/).

## Verify a request

Request a known title that is not already in the target library. Confirm the exact title appears in Radarr or Sonarr with the intended root folder, profile, and TV scope.

If it is accepted but nothing downloads, inspect the service's own search results, indexers, and release restrictions. Cantinarr does not supply release sources or make unaired episodes available.

## TV identities

Discovery uses TMDB, while Sonarr uses TVDB. Cantinarr bridges those identities and can apply explicit season corrections. For an incorrect match, use [TV match corrections](/admin/tv-matches/) rather than changing the title text to force a result.

## Administration after setup

The library screens expose supported details, queue and history views, search, and management actions according to role. An ordinary user's browsing access does not grant administrative write access.

For stuck imports, use [Import Doctor](/troubleshooting/downloads/). For wrong content that is already imported, inspect the file and title-scoped history even when the queue is empty.
