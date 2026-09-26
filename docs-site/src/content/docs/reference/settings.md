---
title: Settings directory
description: Find the screen that owns a setting and the guide that explains what changing it does.
sidebar:
  order: 1
---

Use the search field inside Cantinarr's **Settings** to jump to a control. Results show the owning screen and section, then highlight the control when you open it. Results are filtered by your role, permissions, and server capabilities.

The [complete searchable-settings catalog](/reference/generated/settings/) is built directly from the app's settings registry. The task map below explains where to begin.

## Your account

| You want to | Look for | Read |
| --- | --- | --- |
| Change sign-in methods | Account, Password, Passkeys, Linked sign-in | [Account settings](/use/account/) |
| Choose a personal AI provider | AI Access | [The assistant](/use/assistant/) |
| See remaining requests | Request allowance | [Requests](/use/requests/) |
| Choose playback apps | Video apps, Listening apps | [Playback](/use/playback/) |
| Mute a category | Push Notifications | [Notifications](/integrations/push/) |
| Switch servers | Sign out | [Apps](/use/apps/) |

## Server and household

| You want to | Look for | Read |
| --- | --- | --- |
| Connect a service | Add Instance | [Integrations](/integrations/) |
| Invite someone or change access | Users | [Accounts and invitations](/admin/users/) |
| Restrict a child's content | The user's kids policy | [Kids accounts](/admin/kids/) |
| Require request approval | Request Settings / Request Defaults | [Request policy](/admin/request-policy/) |
| Set limits per person | User request settings and defaults | [Allowances](/admin/request-policy/#rolling-allowances) |
| Repair a TV identity | TV matches | [TV corrections](/admin/tv-matches/) |
| Fix invitation URLs | External Address | [Networking](/install/networking/) |
| Configure external sign-in | Single sign-on or Plex sign-in | [OIDC](/integrations/guides/oidc/), [Plex](/integrations/guides/plex-sign-in/) |
| Revoke a device | Connected Devices | [Security](/admin/security/) |

## Optional features

| You want to | Look for | Read |
| --- | --- | --- |
| Choose catalog feeds or language | Discover | [Catalog providers](/integrations/discovery-providers/) |
| Mark 4K titles on covers | Discover > Show 4K badges | [4K on covers](/use/status/#4k-on-covers) |
| Hide an unconfigured tab | Modules > Discover > Discover tabs | [Navigation](/admin/modules/) |
| Configure included AI | Providers & Credentials | [AI administration](/admin/ai/) |
| Control assistant tools | AI Tools | [MCP](/integrations/mcp/) |
| Configure automatic investigations | AI Remediation | [Supervised repairs](/admin/remediation/) |
| Pause a standing repair rule | Agent Auto-Approvals | [Standing approvals](/admin/remediation/#standing-auto-approvals) |
| Review a profile edit | Configuration History / Profile approvals | [Configuration changes](/admin/configuration-history/) |
| Enable downloaded-file access | Instance path mappings and deployment media roots | [File downloads](/admin/file-downloads/) |
| Connect a television | Apple TVs | [Apple TV setup](/integrations/guides/apple-tv/) |
| Choose Discord mentions and server events | Discord Notifications > Server Discord Notifications (admins) | [Discord](/integrations/discord/) |
| Route metadata and AI traffic | Outbound Proxy | [Proxy setup](/integrations/outbound-proxy/) |

## Deployment-only choices

Ports, persistent storage, encryption keys, callback origin, native passkey association values, and filesystem allowlists are configured outside the app. Start with [environment variables and configuration](/install/configuration/) to choose the settings you need, then use the [complete variable reference](/reference/generated/environment/) for defaults and formats.

## A missing or differently named control

Check your role and both app and server versions. Some screen titles and entry-point labels differ, for example Request Settings leading to Request Defaults. Settings search is often the shortest path when you know the control's name.
