---
title: Phone push notifications
description: Enable the relay, choose server categories, and confirm delivery to a real device.
sidebar:
  order: 6
---

Push notifications are optional. They can tell people when content is ready, when a request is decided, or when an administrator needs to review something.

## Enable a gateway

For the community relay, add this to the server's environment and recreate or restart it:

```yaml
    environment:
      CANTINARR_PUSH_GATEWAY_URL: https://push.cantinarr.com
```

With no explicit key, the server enrolls automatically and keeps its issued key in the database. Keep `/config` persistent. `CANTINARR_PUSH_API_KEY` is for an intentionally pinned gateway key, and `CANTINARR_PUSH_ENROLL_TOKEN` is for a gateway that requires enrollment authorization.

Leave the gateway URL unset to disable the gateway path.

## Review server policy

Open **Settings > Notifications > Push Notifications > Server settings** as an administrator. Allow push, then choose which categories the server may send.

These controls permit delivery. They do not override a person's opt-out. Tests also respect the relevant master switches.

## Review the recipient's settings

On the phone, allow Cantinarr notifications in the operating system. Inside Cantinarr, enable **Receive push notifications** and the desired categories.

A category can be disabled by server or account policy. Read the explanation beside the control before troubleshooting the relay.

## Test delivery

Use the available self-test or administrator test, then confirm the notification appears on the actual device. A server-side accepted response only proves the send was accepted at that stage.

For content notifications, also confirm [instant updates](/integrations/instant-updates/) so a fast import is not missed between polls.

## Multiple devices and sign-out

Notification registration belongs to a device and user. Revoking a device removes its access. Signing out attempts to unregister push and always clears the local session, even if the server is offline.

If the app has been reinstalled or restored, sign in and check notification permission again. A stale device entry is not proof that the current installation is registered correctly.

## Privacy and network routing

The configured gateway relays notification information and device delivery tokens to the platform push service. This is separate from ordinary browsing of your private server.

Gateway traffic uses the server's external transport. A self-hosted gateway on your LAN still belongs to that class. If it must bypass a proxy, use the standard proxy environment with an appropriate `NO_PROXY` entry rather than an in-app proxy that applies to all external traffic.

For a failing test, use [notification troubleshooting](/troubleshooting/notifications/).
