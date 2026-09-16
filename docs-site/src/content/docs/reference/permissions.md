---
title: Roles and access boundaries
description: Understand what a role permits and which additional grants or policies still apply.
sidebar:
  order: 3
---

Cantinarr has administrator and user roles. Kids accounts are ordinary users with an additional content policy. Separate grants determine access to particular libraries, media-server accounts, TVs, and included AI.

| Capability | Administrator | Ordinary user |
| --- | --- | --- |
| Discover and request | Yes | Yes, within current grants, policy, and allowances |
| Download indexed files | With configured roots and mappings | With permission and access to the relevant instance and file |
| Browse allowed arr records | Yes | Read-only browsing of authorized records; kids policy also applies |
| Use chat | With a working personal or shared provider | With a personal provider or an included grant |
| Authorize an MCP client | Yes | Yes, with the same account restrictions |
| Change server credentials and instances | Yes | No |
| Manage people and approvals | Yes | No |
| Write arr settings or run admin management tools | Yes | No |
| View download-client and monitoring administration | Yes | No |
| Read Tdarr activity | Yes | No |
| Control a paired Apple TV | Yes | Only explicitly granted adults; never kids accounts |

## A role is not a media-server share

Plex, Jellyfin, Emby, and Audiobookshelf have their own accounts and library restrictions. Cantinarr manages explicit grants and reads current access where required. An administrator's broad management view is not proof of another person's playback access.

## Books and music

Requesters need an explicit Chaptarr or Lidarr grant or pin. There is no global default that makes these catalogs available to every requester. For a kids account, this is a deliberate grant without movie-style age ratings.

## Included AI

The included-provider grant is independent of role. A personal provider can be used without that grant, but it does not expand the user's tool permissions.

## Revocation

Sensitive operations recheck current account and device authority. Revoking a device also revokes its MCP access. Removing an upstream provider group is not an immediate Cantinarr session revocation.

For the precise role-to-permission implementation, see the [permission definitions](https://github.com/windoze95/cantinarr/blob/main/server/internal/auth/permissions.go). For task instructions, see [accounts](/admin/users/), [kids policies](/admin/kids/), and [media-server grants](/integrations/media-servers/).
