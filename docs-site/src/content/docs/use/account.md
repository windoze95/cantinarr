---
title: Account, sign-in, and preferences
description: Manage your own sign-in methods, AI access, notification choices, and request allowance.
sidebar:
  order: 7
---

Open **Settings > Account** for choices tied to your Cantinarr user. The account tile identifies the signed-in user and role. A kids account also shows its movie and TV limits there.

<span id="interface-language"></span>

## App and metadata languages

Choose **Settings > Account > App language** to follow your device language or select **English** or **Français**. French devices use French automatically; other unsupported languages use English. The choice takes effect immediately and stays on this device when you sign out.

Choose **Metadata language** to control movie and TV titles and descriptions. **Follow app language** is the default; choose **English** or **Français** to use another metadata language while keeping the interface as it is. This choice also stays on this device.

Available translations appear in discovery, search, details, library rows, Releases, Downloads Content, and My reports title labels. Posters, genres, and biographies also use provider translations when available. Missing translations fall back to English or the original source text. A combined TV library entry can keep its source title because it contains several catalog series; individual verified story or episode labels can still be translated.

These choices do not change the Discover original-language filter, audio or subtitle tracks, or the country used for streaming services and release dates. Book and music editions, administrator service records, notifications, connected-service messages, assistant replies, and text you wrote retain their source language.

If the language is unexpected, check both settings. Choose **System default** under App language to follow the device again. If Metadata language says the server needs an update, ask your administrator to update Cantinarr; your saved choice will apply when the server supports it. A title remaining in its source language can mean the provider has no translation or Cantinarr cannot safely match that library record.

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

## App icon

On iPhone, iPad, or Android, open **Settings > Account > App icon**. Choose **Play Sun** (the default), **Play Sign**, **Play Dome**, or **Original**. Your choice stays on this device across restarts and sign-outs. The home screen may take a moment to refresh.

The navigation logo stays Play Sun. The browser and desktop apps use the default icon and do not show this setting. If a change fails, reopen **App icon** to check the current choice and try again.
