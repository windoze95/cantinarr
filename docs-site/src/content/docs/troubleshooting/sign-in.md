---
title: Sign-in and access problems
description: Recover an invitation, password, passkey, Plex login, or OIDC flow without creating duplicate accounts.
sidebar:
  order: 2
---

## A connect link says it is invalid

Connect links work once, expire after seven days, and are replaced when a new one is generated. Obtain a fresh link or use a permitted ongoing sign-in method.

If the link contains an unreachable internal address, the administrator should correct **External Address** and generate a new link.

## Password or passkey sign-in is missing

New users can have those methods disabled by policy. An administrator can enable them in **Users**. A username is not itself proof that a password was ever set.

Passkeys also need a supported device and appropriate secure origin. Native app association settings and browser hostname changes can affect them. Do not delete the only working method while testing another.

## OIDC returns to the wrong place

Check **External Address** and the provider's exact registered callback. Register the server's `/api/auth/oidc/callback`, not the native `cantinarr://oidc` return.

Use the canonical external origin when starting the flow. A web flow started on a different LAN origin can redirect to the canonical one before proceeding.

## OIDC discovery passes but sign-in fails

Discovery checks endpoints. It does not prove the client secret, token exchange, or group claims. Run **Test sign-in** with the exact saved configuration.

Check the issuer, secret, PKCE configuration, requested scopes, and exact allowed-group names. Group matching is case-sensitive, and the required claim must be a suitable array. A dotted JSON path is not a supported group-claim name.

## Required SSO is unavailable

Use an administrator's permitted local recovery method. Repair and retest the provider, or turn off the requirement as appropriate. Existing revoked user sessions do not come back automatically after a policy change.

Do not create another user solely because the provider email matches an existing account. Identity links are explicit and are not merged by email.

## Plex approval completed but Cantinarr did not finish

Return to the initiating app or browser tab. Use **Check now** or reopen the flow as offered. Attempts expire, and a server restart invalidates a pending one.

For automatic signup, Cantinarr needs a currently accepted share or verified ownership of a configured Plex server. A pending invitation or friendship does not satisfy that condition.

## My Plex library works but Plex sign-in does not

Media access and sign-in identity are separate. The administrator must enable Plex sign-in and confirm an unambiguous account identity, or you must link it from an already signed-in Cantinarr account.

## A removed provider user can still use Cantinarr

Established sessions do not continuously synchronize with provider group membership or logout. Revoke the Cantinarr device or identity link for immediate removal. Read the complete [OIDC behavior](/integrations/guides/oidc/#session-and-browser-behavior).
