---
title: Web and phone apps
description: Install the beta apps, connect to the right server, and switch between devices.
sidebar:
  order: 6
---

Every Cantinarr app connects to a Cantinarr server. Install your own server or get its address from your administrator before signing in.

| Device | How to open Cantinarr |
| --- | --- |
| Browser | Open your server's address; the web app is included |
| iPhone or iPad | Join the [public TestFlight beta](https://testflight.apple.com/join/bCPDwCsD) |
| Android | Join the [Google Play open beta](https://play.google.com/apps/testing/codes.julian.cantinarr) |

The phone apps are beta distribution channels. A new upload can take time to process and reach testers. See [updates](/install/updates/) for the difference between server and app versions.

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
