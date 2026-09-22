---
title: Instances and library selection
description: Connect services, set defaults, and understand how multiple libraries are assigned.
sidebar:
  order: 2
---

An **instance** is one connected installation of a service. Two Radarr servers are two instances, even if they contain some of the same movies.

## Add an instance

Open **Settings > Add Instance**, choose the service type, and enter a name, base URL, and the credentials that service requires. The connection test runs from the Cantinarr server.

Enter a base URL, not an arbitrary API endpoint. Service-specific differences are covered in the [integration directory](/integrations/).

Credentials are stored encrypted on the server. On settings that preserve a saved secret when the field is blank, leave it blank to keep it. Use an explicit removal control when you mean to clear it.

## Radarr and Sonarr defaults

Each service can have one global default. A user can have a selected instance of their own, and can also be granted other instances. Choosing a new global default can require takeover confirmation.

Use recognizable names such as “Movies” and “Movies 4K.” The name tells people which library they are requesting into. A default is a selection rule, not a copy or synchronization operation.

## Books and music

Chaptarr and Lidarr do not have a global default for requester access. Grant each person access or pin them to the intended instance. This applies to kids accounts too.

An administrator seeing a book or music catalog does not prove an ordinary user can see it. Administrators can browse supported metadata before setup; requests and library status still need the relevant service.

## Playback servers

Plex, Jellyfin, Emby, and Audiobookshelf use per-user grants rather than a global default. Choose shared libraries and an **Address users open** that the recipient's device can reach.

An internal connection URL can work for server management while being unusable for playback. **Use same URL** is only appropriate when users really can reach that same address.

## What changes when you edit a connection

Saving a URL, credentials, or library selection can affect future reads and actions. It does not move files from one external service to another. Existing requests stay tied to their recorded target library.

After changing a library manager's address, recheck **Instant updates**. The connection test proves Cantinarr can reach the service; the webhook needs the service to reach Cantinarr as well.

## Removing an instance

Review existing requests, issue history, user grants, file mappings, and playback access before removal. Saved requests may remain inspectable after their original instance is removed, but retrying needs the original target and current authorization. Adding a similarly named instance is not an identity-preserving replacement.

## Verify the result

Open a title as the intended user, confirm the selected library, and compare the live result with the service's own UI. Keep similarly named records separate when their IDs or instances differ.
