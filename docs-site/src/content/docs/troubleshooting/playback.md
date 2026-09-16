---
title: Playback and file downloads
description: Trace an available title through its media account, app link, or mapped file path.
sidebar:
  order: 6
---

## The title is Available but does not appear in Plex or Jellyfin

Check that the media server scans the same library location the arr imported into. Confirm the scan has completed and the correct media identity is matched. Then check the recipient's actual library access.

Cantinarr's availability comes from the library manager. It does not mean every playback server has indexed or shared the file yet.

## The open action is missing

Check the media-server grant, linked account, exact title match, and **Address users open**. A blank user-facing address intentionally hides links. A matching title outside the user's allowed libraries cannot authorize a handoff.

For Audiobookshelf, also inspect library, tag, and content restrictions, plus the actual Chaptarr audiobook record.

## Plex says the invitation is pending

Have the recipient sign into their own Plex account and accept the existing library invitation. Refresh **Users** in Cantinarr afterward. A similarly named managed Home profile is not interchangeable with the invited account.

If a fresh access read fails, **Server access unconfirmed** means the state is unknown. It does not mean the invitation was accepted or revoked.

## The wrong playback app opens

Check **Settings > Account > Video apps** or **Listening apps**, then the instance's administrator default. Install and sign into the chosen native app first.

Web and desktop use browser links. iPhone/iPad and Android do not have identical supported app choices. See [playback preferences](/use/playback/).

## A file download fails

Check the path at each boundary:

1. The live arr file record exists and belongs to this user's library.
2. The instance has downloads enabled with an explicit path mapping.
3. The mapped target is inside `CANTINARR_MEDIA_ROOTS`.
4. The host folder is mounted into Cantinarr at that target.
5. The Cantinarr process can read the actual file.

Use a fresh download action if a short-lived link expired. Do not change a read-only media mount to writable; file delivery does not need write access.

The supported set is indexed primary media files. A subtitle or extra sitting beside the file is not automatically downloadable through Cantinarr.

## Apple TV did not show the title

A sent command is an acknowledgement from the TV protocol, not proof of the screen. Check the TV, respond to the Infuse prompt with **Confirm Open** while it is visible, and verify that Infuse has the relevant library configured.

Separate subnets need working mDNS forwarding as well as reachability to the advertised Companion port. Pairing alone does not remove the later discovery requirement. Follow [Apple TV troubleshooting](/integrations/guides/apple-tv/#server-requirements-and-troubleshooting).
