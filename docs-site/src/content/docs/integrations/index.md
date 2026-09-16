---
title: Connect your services
description: Find the right integration and the credentials, addresses, and access decisions it needs.
sidebar:
  order: 0
---

Add services from **Settings > Add Instance**. The connection URL must work from the Cantinarr server. Credentials stay on the server; users do not need copies of them.

## Library managers

| Service | Purpose | Guide |
| --- | --- | --- |
| Radarr | Movie requests and library management | [Movies and TV](/integrations/radarr-sonarr/) |
| Sonarr | Series, seasons, and episodes | [Movies and TV](/integrations/radarr-sonarr/) |
| Chaptarr | Ebooks and audiobooks | [Book setup](/integrations/guides/books/) |
| Lidarr | Albums, EPs, singles, and music library state | [Music setup](/integrations/guides/music/) |

## Download clients

[Connect a download client](/integrations/download-clients/) for queue, history, speeds, and supported actions. Supported services are SABnzbd, qBittorrent, NZBGet, Transmission, Deluge, and ruTorrent.

The client still needs to be configured in the library manager that sends downloads to it. Adding the same client to Cantinarr does not create that upstream connection.

## Playback, listening, and monitoring

| Service | Purpose | Guide |
| --- | --- | --- |
| Plex, Jellyfin, Emby | Accounts, library grants, and links to available titles | [Media servers](/integrations/media-servers/) |
| Audiobookshelf | Audiobook access and listening links | [Audiobookshelf](/integrations/audiobookshelf/) |
| Tautulli | Plex activity and watch statistics | [Monitoring](/integrations/monitoring/) |
| Tracearr | Plex, Jellyfin, and Emby monitoring | [Monitoring](/integrations/monitoring/) |
| Tdarr | Read-only transcoding activity and library counts | [Transcoding](/integrations/tdarr/) |
| Apple TV with Infuse | Open a verified title on a paired TV | [Apple TV](/integrations/guides/apple-tv/) |

## Other connections

- [Instant updates](/integrations/instant-updates/) from library managers.
- [Phone push notifications](/integrations/push/) and [Discord alerts](/integrations/discord/).
- [OIDC single sign-on](/integrations/guides/oidc/) and [Plex sign-in](/integrations/guides/plex-sign-in/).
- [Discovery providers](/integrations/discovery-providers/) for catalogs and recommendations.
- [AI providers](/admin/ai/) for the assistant and remediation.
- [MCP clients](/integrations/mcp/) for external tools.
- [Outbound proxy](/integrations/outbound-proxy/) for server internet traffic.

## Check both directions

A successful instance test proves Cantinarr can reach that service. It does not prove the service can send instant updates back, a phone can reach a playback address, or a user has permission to open the library. The relevant guide includes those follow-up checks.
