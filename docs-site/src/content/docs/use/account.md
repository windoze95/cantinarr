---
title: Account, sign-in, and preferences
description: Manage your own sign-in methods, AI access, notification choices, and request allowance.
sidebar:
  order: 7
---

Open **Settings > Account** for choices tied to your Cantinarr user. The account tile identifies the signed-in user and role. A kids account also shows its movie and TV limits there.

## Passwords and passkeys

Your administrator chooses whether your account can use passwords, passkeys, or both. Manage the permitted methods from your account settings.

Passkeys depend on the site origin and device platform. Use the intended HTTPS server address and follow the device's prompt. A passkey created for one hostname is not automatically a passkey for a different hostname. Native passkey support also needs the correct application association settings on the server.

If a passkey control is unavailable, check account permission, HTTPS, server association settings, and the device's support before deleting existing credentials.

## Link a sign-in provider

If the administrator configured OIDC or Plex sign-in, use **Linked sign-in** to attach it while signed into your existing Cantinarr account. Linking preserves your requests, role, and grants.

Matching email addresses do not automatically merge accounts. OIDC uses the provider's issuer and subject. Plex uses its numeric account identity. If an identity is already linked elsewhere, an administrator must review the conflict.

Before unlinking your only provider, keep another permitted sign-in method. Required OIDC sign-in can prevent using a local password or Plex for a regular user.

## AI access

**Settings > AI Access** shows whether you use an included server provider or a personal provider. A personal provider is an explicit override. If it fails, Cantinarr reports that failure instead of silently switching to the server's shared allowance.

Remove the personal override to return to included access when your administrator has granted it. See [using the assistant](/use/assistant/).

## Notification and app preferences

Use **Settings > Notifications > Push Notifications** to choose your own categories. A disabled choice explains whether the server or account policy prevents it. Permission in the phone's operating system is separate.

Choose supported video and listening apps under Account. Those choices do not create an account in the selected playback app.

## Request allowance

The allowance page shows the rolling limits that apply to your account. An allowance reset follows its time window, not necessarily midnight. Your administrator controls defaults and exceptions.

## Unsaved changes

Settings pages with a Save action preserve your draft when a save fails. Leaving an edited page offers **Keep editing** or **Discard changes**. Controls that save immediately do not require a second Save.

Settings search can find controls across the app. Its results respect your role and permissions, so you will not see every administrator setting on an ordinary account.
