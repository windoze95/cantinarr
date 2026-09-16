---
title: Plex, Jellyfin, and Emby
description: Grant playback access, choose shared libraries, and connect titles to a user's actual media account.
sidebar:
  order: 3
---

Media-server connections manage access to existing playback services. They are separate from Radarr and Sonarr, which manage acquisition and imported library files.

## Connect a service

Open **Settings > Add Instance** and select Plex, Jellyfin, or Emby. Use a connection address the Cantinarr server can reach.

For Plex, follow the browser PIN flow with the account that owns the server, then select the server to share. For Jellyfin and Emby, use the service credentials requested by the form. Test before selecting libraries.

## Set the address users open

This is the URL used for browser and app links. It can differ from the internal connection URL.

For example, Cantinarr can manage `http://jellyfin:8096` while users open an HTTPS hostname. Leaving the user-facing address blank hides the associated links. **Use same URL** copies the internal address only when you explicitly choose it. Plex normally offers its hosted web app address.

## Choose libraries and people

After a successful live connection check, select the shared libraries and users. These media services have no global default that grants everyone access.

Revoking a grant can disable the linked Jellyfin or Emby account, or remove the Plex share. Regranting can reactivate the account or send a new invitation. Review the confirmation and live result; a Cantinarr checkbox is not independent proof of the recipient's experience.

## Existing accounts

Import or explicitly link an existing media-server account where offered. Check the actual external identity. Similar usernames or a typed email are not enough to prove that two accounts belong together.

A media-account link and a Cantinarr sign-in identity are separate. Plex library access does not automatically enable **Continue with Plex**. Configure and review that under [Plex sign-in](/integrations/guides/plex-sign-in/).

## Plex invitation states

| State | Meaning |
| --- | --- |
| Awaiting Plex acceptance | The invitation exists; the recipient still needs to accept it |
| Active on server | A fresh successful read confirms the accepted access |
| Server access unconfirmed | Cantinarr could not verify the current state |

Have the recipient accept the invitation in their own Plex account, then refresh **Users**. A Plex Home managed profile and a separate Plex account with a similar name are not the same identity.

## Title links

Cantinarr offers supported title handoffs only after matching the title in an authorized media account. Media-server scans can lag behind an arr import. Check the playback service directly if the file is available in Radarr but the title is not yet openable.

For iPhone/iPad app defaults, see [playback preferences](/use/playback/). For the TV remote path, see [Infuse on Apple TV](/integrations/guides/apple-tv/).

## Verify with the recipient

Use the recipient's account to confirm that only intended libraries are visible and a known title opens. An owner account can see more libraries and is not an adequate access test.
