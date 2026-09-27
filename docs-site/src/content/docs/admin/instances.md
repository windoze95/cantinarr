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

Radarr, Sonarr, Chaptarr, and Lidarr share the same rules. Regular users can access only their assigned instances. A global default chooses a preferred request destination; it never grants access or removes access to another assigned library. Administrators can access all automation instances.

When adding an instance, **Default Instance** starts on if that service has no default. **Automatically add new users** also starts on. Saving either setting does not assign existing users. Enable automatic assignment on several instances when new accounts should receive several libraries.

An explicitly selected, assigned library receives the request. Otherwise Cantinarr uses the user's assigned personal preference, then an assigned global default, then their first assigned instance in configured order. This works even when the global default is outside their assignments. An offline destination produces an error or retry for that destination; requests never move to another instance as a fallback.

Use recognizable names such as “Movies” and “Movies 4K.” Pending requests keep their recorded destination when defaults change. Removing that assignment blocks approval and delivery until access is restored.

## Manage users

Open a saved instance and choose **Manage users**. Search usernames and filter by assignment, role, child account, SSO link, or pending invitation. **Select all matching** selects every matching regular account; administrators are shown as having access to all instances and cannot be selected.

Review the matching and selected counts, then choose **Add selected** or **Remove selected**. Removal shows how many preferences will be cleared. Changing a filter clears the selection. Only selected users change, so a filtered list cannot remove people outside the selection.

Automatic assignment runs once when an account is created through an invitation, media-server import, OIDC, or Plex sign-in. Replacement invitations, identity linking, and later sign-ins do not reapply it. To update existing users, use **Manage users**.

Upgrades preserve existing access as explicit assignments. Existing instances start with automatic assignment off; enable it deliberately for future accounts. Changing or removing a default never restores a revoked assignment.

## Books and music

Chaptarr and Lidarr support global defaults and automatic assignment just like Radarr and Sonarr. Requesters, including kids accounts, still need an assignment to browse and request from those libraries.

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
