---
title: Accounts and invitations
description: Add people, choose their sign-in methods, and give them the library access they actually need.
sidebar:
  order: 1
---

Open **Settings > Users** as an administrator. Start with an ordinary user for someone who should browse and request. Administrator access includes server configuration and connected-service management.

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

Radarr and Sonarr can have a global default, per-user selections, and additional granted libraries. Chaptarr and Lidarr require a per-user pin or explicit grant for requesters.

The instance editor's **User Access** list is additive: checking a second library gives access alongside the current default. It does not move existing requests or files. Unchecking removes that library's access, including a legacy pin to it.

See [instances and library selection](/admin/instances/) for multiple-library setups.

## Give playback access separately

Plex, Jellyfin, Emby, and Audiobookshelf grants are separate from request-library access. A user who can request a movie is not automatically entitled to every Plex library.

For a media-server instance, review both **User Access** and the shared or default libraries. Check the resulting account in the actual service. Plex invitations are not active shares until accepted.

## Included AI is also separate

An administrator can grant or revoke included AI for each user. A personal provider remains that person's own choice. New invited users do not automatically receive shared AI access; the initial administrator does.

Before granting a shared OAuth provider, read the app's allowance and cost explanation. Several users can consume the same connected account's limits.

## Change or remove sign-in access

Disabling password sign-in clears the stored password. Disabling passkeys removes registered passkeys. Those actions are more than hiding a button, so review the confirmation.

Use **Connected Devices** to revoke a lost device. Changing an upstream OIDC group or disabling a provider account does not immediately revoke already established Cantinarr sessions. For immediate removal, act on the Cantinarr devices or identity link as described in [SSO setup](/integrations/guides/oidc/).

Before deleting a user or removing external access, review the app's confirmation and the effect on the associated media-server account. Revoking a media-server grant can disable an account or remove a Plex share without deleting the external account itself.

## Check as the intended user

Use the recipient's actual account to verify the visible tabs, selected libraries, and ability to open media. An administrator's view is broader and does not prove a regular user has the right access.
