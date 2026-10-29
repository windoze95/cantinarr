---
title: Plex, Jellyfin, and Emby
description: Scan existing movies and shows, associate household libraries, and manage playback accounts.
sidebar:
  order: 3
---

Media-server connections can report movies and episodes that already exist in Plex, Jellyfin, or Emby, including titles that Radarr and Sonarr do not manage. They also manage access to those playback services.

Scanning only reads your libraries. It never adds a show to Sonarr, creates a request, or starts a download, including for shows that are still airing.

## Connect a service

Open **Settings > Add Instance** and select Plex, Jellyfin, or Emby. Use a connection address the Cantinarr server can reach.

For Plex, follow the browser PIN flow with the account that owns the server, then select the server to share. For Jellyfin and Emby, use the service credentials requested by the form. Test before selecting libraries.

## Scan existing movies and shows

1. Open the saved server under **Settings**.
2. In **Library availability**, enable scanning and select the movie and TV libraries to include.
3. Save, then use **Scan now** to check the first inventory. Review the progress, last successful scan, and any error.

Scan selection is separate from **Shared libraries**. Changing which libraries are scanned does not change any media-server account or sharing permission. Empty selection scans nothing, and existing installations start with scanning off.

Cantinarr checks additions and changes every five minutes and performs a full reconciliation every 24 hours. Before making a missing-content request, it verifies current coverage again. A failed scan or inaccessible server is an unknown result, not proof that content is missing. Retry the scan or fix the connection before relying on it.

## Choose who sees availability

For a user with a linked account, Cantinarr checks that account's current access to the selected libraries. A server owner seeing a title does not prove that another account can see it.

For a household that shares one account on the TV, open the user's **Media server accounts** section under **Settings > Users** and choose **Library access only**. Select the server and libraries, review the selection, and save. The user does not need a personal Plex, Jellyfin, or Emby account for this association.

If the selected provider has only one eligible connected server, the picker preselects that server and its current scan libraries. Nothing is assigned until the administrator saves this separate access choice. Adding another scan library later does not expand the saved association.

A library-only association affects availability and missing-content decisions. It does not create an account, send an invitation, grant playback credentials, or allow TV control. Kids' content limits still apply. Linking an individual account replaces the library-only association; unlinking it does not restore the old association.

## Request missing content or another copy

Movie and TV details can show **Available** from a media server while the selected Radarr or Sonarr library has a different status. The library chips continue to describe each acquisition library's own files and requests.

Use the ordinary request action to acquire missing content. For a partly available season, Cantinarr counts distinct episodes across accessible sources and searches only for verified missing episodes. An explicit **Follow selected seasons** request keeps checking that selected season for newly aired missing episodes. It does not add other seasons or begin following shows merely discovered by a scan.

Episode numbering must map safely to catalog identities. Combined episode files with ambiguous ordering remain unverified. When air metadata supplies only a date, automatic searches wait until that UTC date has ended.

Use **Request in library** when you deliberately want a copy in a particular authorized Radarr or Sonarr library, such as a different quality target. Existing approval and quota rules still apply.

**Stop following** ends future Cantinarr scheduling. It does not delete files, cancel shared downloads, or change monitoring already configured in Sonarr. Existing Sonarr automation can still acquire content independently of Cantinarr.

## Set the address users open

This is the URL used for browser and app links. It can differ from the internal connection URL.

For example, Cantinarr can manage `http://jellyfin:8096` while users open an HTTPS hostname. Leaving the user-facing address blank hides the associated links. **Use same URL** copies the internal address only when you explicitly choose it. Plex normally offers its hosted web app address.

## Choose libraries and people

After a successful live connection check, select the shared libraries and users. These media services have no global default that grants everyone access.

Revoking a grant can disable the linked Jellyfin or Emby account, or remove the Plex share. Regranting can reactivate the account or send a new invitation. Review the confirmation and live result; a Cantinarr checkbox is not independent proof of the recipient's experience.

## Existing accounts

Open **Settings > Users > Import users**, or **Import users** on the saved instance. Choose the server, select accounts, and explicitly select a Cantinarr user for any existing-account mapping. Search and selection work the same way for Plex, Jellyfin, Emby, and Audiobookshelf. See [accounts and invitations](/admin/users/#import-people-from-a-media-server) for the full workflow.

Import results lead to separate sign-in, request-permission, and media-access steps. Remote access management starts off. Check the actual external identity; similar usernames or a typed email do not select an existing Cantinarr account.

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
