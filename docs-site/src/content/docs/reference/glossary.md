---
title: Plain-language glossary
description: The words you will see in the app and guides, explained without assuming you already run a media server.
sidebar:
  order: 2
---

| Word | Meaning in Cantinarr |
| --- | --- |
| Administrator | A person who can configure Cantinarr and manage its connected services and users |
| Arr / library manager | A service such as Radarr, Sonarr, Chaptarr, or Lidarr that finds and organizes media |
| Instance | One connected installation of a service; two Radarr servers are two instances |
| Catalog | Title information and discovery results, which can include content you do not own |
| Library | Media tracked by a particular connected service; it is not the entire external catalog |
| Request | A saved intent to obtain a specific title, format, or TV scope in a particular library |
| Approval | An administrator's decision about whether a request or proposed action may proceed |
| Delivery | Sending an accepted request to the library manager; separate from downloading the media |
| Download client | Software that transfers files, such as qBittorrent or SABnzbd |
| Import | The library manager recognizing a completed file and placing or recording it in the library |
| Media server | Software such as Plex or Jellyfin that serves your existing files to playback apps |
| Quality profile | Rules in a library manager that determine which releases it accepts and when it upgrades |
| Custom format | A release-scoring rule used by a supported library manager; it does not rewrite a media file |
| Monitored | An arr setting saying the service should look for that content; it is not the same as available |
| Root folder | The library manager's destination folder for organized media |
| Path mapping | A translation between the path one service reports and the path another process can read |
| Grant | Explicit permission for an account to use a library, media server, TV, or included AI service |
| Pin / user default | A selected instance for a particular user, instead of relying on the service's global default |
| Webhook | A service calling Cantinarr when something changes, used for instant updates |
| Callback URL | The address the calling service uses to send an event or finish an authorization flow |
| Origin | A scheme, hostname, and optional port, without an extra path, such as `https://media.example.com` |
| Reverse proxy | A server that accepts requests for your public hostname and forwards them to Cantinarr |
| Outbound proxy | A proxy Cantinarr uses when it sends selected traffic to internet services |
| OIDC / single sign-on | Signing into Cantinarr using an external identity provider |
| OAuth | A protocol for granting access; Cantinarr uses separate flows for MCP clients and AI-provider connections |
| Passkey | A sign-in credential managed by your device or password manager, bound to the site's identity |
| Connect link | A private, single-use invitation that signs one device into an account |
| MCP | A protocol that lets an external assistant discover and call Cantinarr tools with authorization |
| Included AI | The server's shared provider, available only when granted to a user |
| Personal provider | A user's own AI credential or subscription link, taking precedence over included access |
| Remediation | Investigating and repairing a problem, under the administrator's configured boundaries |
| Blocklist | Tell the library manager to avoid a particular bad release; replacement behavior depends on the action |
| Stale | Data from an earlier successful read that could not be refreshed |
| Unknown / unavailable | Cantinarr could not establish the current answer; this does not mean zero or absent |
| Release candidate | A frozen build being tested before stable promotion |
| Image tag | A Docker image label such as `latest`, `edge`, or a numbered version |
| Digest | An identifier for a specific image's contents, used when exact build identity matters |

For status labels such as Requested, Downloading, Partial, and Available, use the [status guide](/use/status/).
