---
title: Web and phone apps
description: Install the beta apps, connect to the right server, and switch between devices.
sidebar:
  order: 6
---

The phone app needs a separate Cantinarr server running on a computer or NAS. That server connects to Radarr, Sonarr, Plex, and your other services. Installing the phone app does not install the server.

If someone runs Cantinarr for you, ask them for a connection link or the Cantinarr server address. If you are setting it up yourself, follow the [server installation guide](/start/quickstart/) first. It covers Docker and links to Unraid and other app catalogs.

| Device | How to open Cantinarr |
| --- | --- |
| Browser | Open your server's address; the web app is included |
| iPhone or iPad | Join the [public TestFlight beta](https://testflight.apple.com/join/bCPDwCsD) |
| Android | Join the [Google Play open beta](https://play.google.com/apps/testing/codes.julian.cantinarr) |

The phone apps are beta distribution channels. A new upload can take time to process and reach testers. See [updates](/install/updates/) for the difference between server and app versions.

## Connect the phone app

The **Connect to Cantinarr** screen has one **Link or Cantinarr address** field:

- **Have a connection link?** Open it on your phone, or paste the complete link into the field and choose **Connect**. The link supplies the server address and signs you in. If the server requires single sign-on, complete its provider sign-in. An incomplete, expired, or already-used link needs to be replaced by your administrator.
- **Have a server address?** Enter it and choose **Connect**. Use the Cantinarr address, such as `http://192.168.1.10:8585`, not a Radarr, Sonarr, or Plex address. Sign in with a method enabled by your administrator. A newly installed server asks you to create its first administrator account.
- **Need to install the server?** Choose **Set up a Cantinarr server** to open the installation guide. If the browser cannot open, the app offers its address to copy.

The app remembers servers after successful sign-in. When you need to sign in again, the last address is filled in without leaving this screen. **Saved servers** expands the saved choices; **Forget** removes a shortcut, with **Undo** if needed.

**Sign in with passkey** appears on the connection screen when the remembered server and your device support it. Check the server name and address beside the button. A fresh installation needs a connection link or server address first. Servers that require single sign-on show their required sign-in provider after you choose **Connect**; administrator recovery remains on that sign-in screen.

If checking the remembered server fails, choose **Retry saved server**, paste a connection link, or enter another address. A failed check does not remove the saved server.

## Choose the right server address

Use an address reachable from the device. A Docker service name such as `http://cantinarr:8585` may work between containers and still be unusable on your phone.

If the address works on home Wi-Fi but not on cellular data, the server is probably only reachable inside your home. Your administrator needs to provide a remote-access address or a private-network connection. See [remote access](/install/remote-access/).

## Sign in on another device

Use an enabled password, passkey, or sign-in provider, or obtain a fresh connect link. Connect links are single-use and do not serve as permanent login bookmarks.

Sessions appear in the administrator's **Connected Devices** screen. If a device is lost, revoke its session there. Changing a display name does not revoke a device.

## Leave the demo or change servers

Choose **Settings > Sign out**. Confirm, then enter the intended server address from the connect screen. Signing out clears the local connection even when the server cannot be reached.

The [public demo](https://demo.cantinarr.com) is for exploring the interface. Connecting your installed app to the demo does not connect it to your own media library.

## Browser and native differences

Browser playback links open browser destinations. Native app links may open a supported installed media app. Push notification registration and native passkeys depend on the device and server configuration.

External sign-in opens a browser for approval. Finish the flow in the initiating tab or return to the initiating phone app. If the server restarted or the attempt expired, begin a new sign-in rather than reusing the old callback link.
