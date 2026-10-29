---
title: Accounts and invitations
description: Add people, choose their sign-in methods, and give them the library access they actually need.
sidebar:
  order: 1
---

Open **Settings > Users** as an administrator. Start with an ordinary user for someone who should browse and request. Administrator access includes server configuration and connected-service management.

## Import people from a media server

1. Open **Settings > Users > Import users**, or choose **Import users** on a saved media-server instance.
2. Choose Plex, Jellyfin, Emby, or Audiobookshelf and the intended server. If none is connected, choose **Connect a media server** first.
3. Search and select accounts. New names create ordinary Cantinarr users. To attach an account to someone already in Cantinarr, choose **Link to existing user** and select that person explicitly. Matching usernames never select a person for you.
4. Review the results and finish the remaining sign-in and access steps. You can import up to 200 accounts at a time. **Select all** skips administrators and unresolved mappings.

Remote access management starts off. Importing with it off leaves the existing server account and its libraries unchanged. An already-linked account shows who holds it. Pending invitations, disabled accounts, and protected administrators remain visible.

If the server needs an update, update it before using this workflow. Cantinarr does not fall back to automatic username matching on older servers.

## Finish account setup

Open a user to see **User access**. The same three sections appear in import results:

- **Cantinarr sign-in** shows invitation instructions and linked identities. For Plex, use **Review Plex sign-in** to review the identity and save your choice for sign-in and automatic signup. Neither is enabled by importing. Jellyfin, Emby, and Audiobookshelf passwords remain credentials for those servers, not Cantinarr. If single sign-on is required, the invitation takes the recipient through the configured provider.
- **Request permissions** shows movie, TV, book, and music assignments. **Assign request libraries** adds selected libraries without replacing existing assignments or routing defaults. Leaving requests unassigned is valid.
- **Media server accounts** shows the linked account, management choice, and current access state. Linked, active, pending acceptance, and unconfirmed access mean different things. A failed read is reported as unknown.

Partial failures retain completed results. Retry only unfinished accounts. If an interrupted operation created a user but its outcome cannot be confirmed, review the current accounts and choose the mapping explicitly before continuing. Replacing an invitation invalidates the old link.

## Add a person

1. Create the user with a recognizable username and the intended role.
2. Review their permissions, request policy, and library access.
3. Decide whether to enable a password, passkeys, or a configured sign-in provider.
4. Generate a connect link and send it privately through your usual household channel.

New users normally start with password and passkey sign-in disabled. A connect link provides the initial device sign-in. Enable the ongoing methods you want the person to use instead of assuming a username alone gives them a reusable login.

## Connect links

A connect link signs in one device, once. It expires after seven days. Generating a replacement invalidates the previous link.

Set **Settings > External Address** before sending links to people outside your local network. Otherwise the link uses the address your own app is connected to, which may be a private IP or internal hostname the recipient cannot open.

A connect link is a credential. Do not paste one into a public issue, screenshot, or documentation page.

## Give library access

Assign one or more Radarr, Sonarr, Chaptarr, or Lidarr libraries in the person's request settings. Their optional preferred instance must be one they can access. Without a preference, requests use an assigned global default or their first assigned instance. A default never grants access.

For many people, open the instance's **Manage users** screen. Filter users, select all matching or individual rows, then add or remove the selected assignments. Removing an assignment clears a preference pointing there and leaves other libraries unchanged.

New accounts receive every automation instance with **Automatically add new users** enabled at account creation. The switch starts on for new instances and off for upgraded existing instances. Turning it on later does not change existing users; repeat sign-ins and replacement invitations do not restore revoked access.

See [instances and library selection](/admin/instances/) for multiple-library setups.

## Give playback access separately

Plex, Jellyfin, Emby, and Audiobookshelf grants are separate from request-library access. A user who can request a movie is not automatically entitled to every Plex library.

For Plex, Jellyfin, and Emby, **Library access only** is a separate option for household members who use a shared TV account. An administrator explicitly associates selected scanned libraries with a Cantinarr user without creating a remote account or changing shares. The sole eligible server is preselected only inside this flow, and the separate save applies the association. An unrelated user edit does not assign access. See [media-server availability](/integrations/media-servers/#choose-who-sees-availability).

For a media-server instance, review both **User Access** and the shared or default libraries. Check the resulting account in the actual service. Plex invitations are not active shares until accepted.

## Included AI is also separate

An administrator can grant or revoke included AI for each user. A personal provider remains that person's own choice. New invited users do not automatically receive shared AI access; the initial administrator does.

Before granting a shared OAuth provider, read the app's allowance and cost explanation. Several users can consume the same connected account's limits.

## Change or remove sign-in access

Disabling password sign-in clears the stored password. Disabling passkeys removes registered passkeys. Those actions are more than hiding a button, so review the confirmation.

Use **Connected Devices** to revoke a lost device. Changing an upstream OIDC group or disabling a provider account does not immediately revoke already established Cantinarr sessions. For immediate removal, act on the Cantinarr devices or identity link as described in [SSO setup](/integrations/guides/oidc/).

Device activity timestamps update at most once per minute during normal use. Revocations and permission changes are checked on every authenticated request.

Before deleting a user or removing external access, review the app's confirmation and the effect on the associated media-server account. Revoking a media-server grant can disable an account or remove a Plex share without deleting the external account itself.

## Check as the intended user

Use the recipient's actual account to verify the visible tabs, selected libraries, and ability to open media. An administrator's view is broader and does not prove a regular user has the right access.
