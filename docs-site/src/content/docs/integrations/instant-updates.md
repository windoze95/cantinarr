---
title: Instant updates and webhooks
description: Let your library managers tell Cantinarr when content changes, without exposing their credentials to devices.
sidebar:
  order: 5
---

Instant updates let Radarr, Sonarr, Chaptarr, and Lidarr notify Cantinarr about imports and other relevant library changes. They help the app refresh promptly and make short-lived downloads easier to observe.

## Automatic setup

When you create an instance, Cantinarr generates its own per-instance credential and tries to install an authenticated webhook in that service. The save result reports whether this worked.

Open the saved instance's **Instant updates** section to inspect the live upstream state. Use **Configure instant updates** to run the installation again after fixing connectivity.

## Configure the callback address

Set this in the Cantinarr server's environment when the direct request origin is not the address your library managers should call:

```yaml
    environment:
      CANTINARR_ARR_CALLBACK_URL: http://cantinarr:8585
```

That example assumes all the library managers can resolve and reach `cantinarr` on the same network. Use the correct reachable origin for your deployment. It has a scheme and optional port, without an extra path.

[Apply the environment change](/install/configuration/#apply-a-change), then reconfigure each affected instance's instant updates. With Compose, use `docker compose up -d cantinarr`; a restart alone keeps the old container environment.

Forwarded proxy headers are deliberately not trusted to choose a credential-bearing callback destination. Set the value explicitly behind a reverse proxy.

## Keep the addresses separate

**External Address** builds invitations and external sign-in links for people. `CANTINARR_ARR_CALLBACK_URL` is for library-manager callbacks. `CANTINARR_OAUTH_ISSUER` is for inbound MCP authorization.

Using a public hostname for every one of these can work, but it is not required and can introduce avoidable proxy or authentication barriers.

## Verify an actual event

After setup, import or update suitable test content through the library manager's normal workflow and confirm Cantinarr refreshes. A saved webhook configuration alone does not prove the callback reached the server.

For notifications, also verify the server and user's push settings. A library event arriving and a phone receiving a notification are separate checks.

## If updates stop

Inspect the live webhook status, callback reachability from the library manager, reverse-proxy access rules, and whether the instance's credential changed. Repair through **Configure instant updates** instead of copying private webhook credentials into a browser or public log.

See [connection troubleshooting](/troubleshooting/connections/).
