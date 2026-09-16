---
title: Watch, read, and listen
description: Finish media-server access and open an available title in the app you use.
sidebar:
  order: 5
---

Cantinarr helps you get to a title. Playback happens in Plex, Jellyfin, Emby, Audiobookshelf, Infuse, or the relevant browser experience. The media server and playback app still control their own libraries and playback behavior.

## Finish your media-server access

Open **Media server access** from navigation, or **Settings > Guides > Media server access**. Follow the instructions for each server you have been granted.

- **Plex:** link the intended Plex account and accept the server's invitation if one is pending.
- **Jellyfin, Emby, or Audiobookshelf:** follow the available account creation or connection steps, then sign into that server with the resulting account.
- **Existing account:** your administrator may link it instead of creating another one.

If the guide says **Awaiting Plex acceptance**, accept the existing invitation from the recipient's Plex account. The administrator can refresh Users afterward to read the current state. Sending another invitation is not the first fix.

## Open a title

On an available movie, show, or audiobook, choose the relevant open, watch, or listen action. A missing action can mean there is no verified match in an authorized media-server account, no address users can open, or no supported integration for that title.

Library availability alone does not grant playback access. Your administrator needs to check both the Cantinarr grant and the live media-server account.

## Choose a playback app

On iPhone and iPad, **Settings > Account > Video apps** lets you choose the service's official app, Infuse, Browser, or the administrator's default for each supported video service.

Install Infuse and connect the media servers inside it before selecting it. Cantinarr does not configure Infuse's libraries for you. Android uses the service's official app; web and desktop use the browser.

**Open in Infuse** opens the app on the phone. **Open on [TV name]** sends a title link to a paired Apple TV. They are different actions. See [Apple TV setup](/integrations/guides/apple-tv/).

## Choose a listening app

**Settings > Account > Listening apps** keeps separate iPhone/iPad and Android preferences. iPhone/iPad supports Browser, Audiobookshelf, and ShelfPlayer. Android supports Browser, Audiobookshelf, and TheShelf. Web and desktop use the browser.

Use **Use admin default** to inherit the server's choice. Install and sign in before choosing a native app. A failed native launch falls back to the browser link.

## Save files for another app

If your administrator enables [completed-file downloads](/admin/file-downloads/), use the file download action. The browser or operating system receives the file. Cantinarr does not turn into an offline playback library or include every subtitle and extra found beside it.

## Hide a guide you have finished

The media access guide's **Hide from main navigation** switch hides its shortcut on this device for this Cantinarr account and server. The guide remains in Settings. A new media-server grant restores the shortcut so you can finish the new setup.
