---
title: Apps built for Seerr
description: Connect Maintainerr, Dashbrr, Homepage, and other apps that read requests from Seerr, Overseerr, or Jellyseerr.
sidebar:
  order: 12
---

Cantinarr answers the Seerr API, so an app that reads requests from Seerr, Overseerr, or Jellyseerr can read Cantinarr's movie and TV requests instead. Nothing changes on the app's side: give it Cantinarr's address and a key, and it treats Cantinarr as Seerr.

What the apps get from it:

- **Maintainerr** learns who requested a title and when, so its rules can compare the requester with who watched it, and it can name the requester in a pre-deletion notification. With **Force reset Seerr record** on, it also clears the request when it removes the title.
- **Homepage** shows request counts in its Seerr widget.
- **Dashbrr** lists recent requests and approves or declines waiting ones.

Books and music are not included. The Seerr API has no shape for them.

## Before you start

- An administrator account. The key acts as the administrator who issued it: it can read every movie and TV request, approve or decline waiting requests, and delete requests. It stops working if that account is deleted or loses the administrator role.
- The address the other app can reach Cantinarr at. **Settings > Seerr-compatible API** shows the address your own app is connected with, which is a starting point. An app on another machine or in another container may need a different one, such as the server's LAN address or its container name. Do not add `/api/v1`; the app adds that itself.

## Issue the key

1. Open **Settings > Seerr-compatible API**.
2. Choose **Issue key**. The key appears hidden; use **Copy key** to copy it, or **Show key** to read it.
3. In the other app, open its Seerr (or Overseerr or Jellyseerr) settings. Enter Cantinarr's address as the URL and the copied key as the API key.
4. Run the app's connection test. Maintainerr's test reads Cantinarr's version and reports it as the Seerr version.

A passing test proves the app can reach Cantinarr and the key is accepted. It does not prove the app's rules or widgets match what you expect; check those in the app itself.

## Replace or revoke the key

**Replace key** issues a new key and stops the old one at once. Every app holding the old key stops working until you paste the new one into it.

**Revoke key** removes the key. Apps get an error until you issue a new one. Both actions ask before they run.

## What the apps see

Availability is read live from Radarr and Sonarr each time an app asks, the same way the rest of Cantinarr works. A title removed from a library reads as gone at once, and its request can be made again. Maintainerr's availability sync therefore has nothing to fix; running it only refreshes Cantinarr's short library cache.

A request an app deletes disappears from the requester's history as well, and any request allowance it used is released. A request whose delivery is being written at that moment is refused until it settles.

Requesters are identified by the name the media server knows: the Plex username for an account signed in with Plex or holding an accepted Plex share, the Jellyfin or Emby username for a linked account, and the Cantinarr username otherwise. Maintainerr compares that name with its watch history.

## When something fails

- **The app's test fails with an authentication error.** The key was not pasted completely, or it was replaced or revoked since. Copy the current key from **Settings > Seerr-compatible API** and paste it again.
- **The app's test fails to connect.** The address is wrong from where the app runs. A container usually cannot use `localhost` for another container; use the server's LAN address or the container name, and include the port.
- **The app reports Cantinarr as unavailable, and its log shows a 503.** Cantinarr could not read a Radarr or Sonarr library at that moment. It answers with an error rather than a shorter list of requests, and apps built for Seerr retry. Check the instance under **Settings** and the arr itself.
- **A request shows the wrong requester name in Maintainerr.** Maintainerr matches by media-server username. Check the person's linked account under **Settings > Users**.
