---
title: Connection problems
description: Separate a device-to-server failure from a server-to-service failure or a missing callback.
sidebar:
  order: 1
---

## The app cannot reach Cantinarr

1. Confirm the container or service is running.
2. Open the exact same address in a browser on that device.
3. Check the host port. In `8586:8585`, users open 8586.
4. Check whether the address requires home Wi-Fi or a private-network connection.
5. If using HTTPS, inspect DNS, certificate validity, and the reverse proxy's upstream.

With Compose, use `docker compose ps` and recent logs. A container that repeatedly restarts needs its startup error fixed before investigating mobile settings.

## The service opens on my laptop but fails Test Connection

The test runs from Cantinarr, not your laptop. Check:

- Does the hostname resolve inside Cantinarr's network?
- Is the service on the same Docker network, or does it publish a reachable host port?
- Does `localhost` accidentally point back at Cantinarr?
- Is the configured protocol correct for that port?
- Does the service require a URL base or a hostname allowlist?
- Does the Cantinarr process trust the HTTPS certificate?

For VPN-shared containers, use the gateway address. For Tdarr, use the server API port. For Chaptarr, use its native root URL rather than an ebook or audiobook compatibility prefix.

## Authentication fails

Check the correct credential type: an arr API key, a WebUI password, HTTP Basic credentials, or a service-specific account link are not interchangeable.

A saved secret often appears blank when editing because it is write-only. Do not assume the blank field means it was erased. Use the service's connection test and the form's explicit removal control.

## Live updates do not arrive

Open **Instant updates** on the instance. Check the callback origin from the library manager's side. A proxy login page, unresolvable container name, or blocked route can prevent callbacks while the forward API connection succeeds.

Set the explicit callback environment value, restart Cantinarr, and re-run **Configure instant updates**.

## The page opens, but streaming or live status fails

Check WebSocket forwarding and response buffering in your reverse proxy. Ordinary page requests can succeed while long-lived or streamed requests fail. See [remote access](/install/remote-access/).

## A proxy change broke a local service

Local service instances use direct transport even when proxy environment variables exist. Check the direct route rather than trying to force the service through the outbound proxy.

For metadata or hosted AI failures, test the outbound proxy separately. Device artwork requests still depend on the device's internet connection.
