---
title: Networking and addresses
description: Choose the right address for the app, connected services, instant updates, and sign-in.
sidebar:
  order: 4
---

Most connection problems come from using an address that works from one place but not another. There are several independent connections in a Cantinarr setup.

## Which address goes where

| Address | Who connects to it | Where you set it |
| --- | --- | --- |
| Cantinarr address | A phone or browser | The app's connect screen |
| External Address | People opening invitations and external sign-in | **Settings > External Address** |
| Instance URL | The Cantinarr server | Each service's instance settings |
| Arr callback URL | Radarr, Sonarr, Chaptarr, or Lidarr | `CANTINARR_ARR_CALLBACK_URL` |
| MCP OAuth issuer | External MCP clients and their browsers | `CANTINARR_OAUTH_ISSUER` |
| Address users open | Playback and listening apps or browsers | Each media-server instance |

These addresses can legitimately differ. Your phone might open `https://media.example.com`, Cantinarr might call `http://radarr:7878`, and Radarr might send events to `http://cantinarr:8585`.

## Inside Docker

`localhost` always means the machine or container making the connection. From inside Cantinarr, `http://localhost:7878` points back to the Cantinarr container. It does not mean the Docker host or Radarr.

On a shared user-defined network, use the service name and container port. Between separate networks, use an address and published port reachable from Cantinarr, or join the services to a deliberate shared network.

Use **Test Connection** inside Cantinarr. That test runs from the server, which is the connection that matters. Opening an address on your laptop proves only that your laptop can reach it.

## Services behind a VPN gateway

If Chaptarr or a download client uses `network_mode: container:<gateway>` or `network_mode: service:<gateway>`, it shares the gateway's network stack. Use the gateway's reachable name or IP and the correct service port.

For example, a service that listens on 8789 inside that shared network might be reached as `http://vpn-gateway:8789`. The actual name, port publication, and network attachment depend on your deployment. Do not assume the service's own container name resolves.

## HTTP and HTTPS

Plain HTTP is supported between trusted local services. With HTTPS, the Cantinarr server must trust the certificate. A browser's accepted certificate exception does not add that certificate to the container's trust store.

For a private certificate authority, add its CA certificate to the server image's trust store using your normal deployment process. Keep that customization across image upgrades. Avoid replacing certificate verification with an insecure bypass.

## Instant updates travel in the other direction

Being able to connect from Cantinarr to Radarr does not prove that Radarr can call back to Cantinarr.

Set `CANTINARR_ARR_CALLBACK_URL` to an origin reachable by all connected library managers. An origin includes the scheme, hostname, and optional port, such as `http://cantinarr:8585`. It has no extra path. Restart Cantinarr after changing the variable, then configure instant updates on each affected instance.

The old `CANTINARR_PUBLIC_URL` name is still accepted for compatibility. It retains the callback meaning. If both names are present, the newer one wins.

## A reverse proxy does not change every address

Your public reverse proxy provides an HTTPS address for people and external clients. It does not require exposing Radarr, Sonarr, or your download clients to the internet.

Set **External Address** for invitations and OIDC. Set the canonical HTTPS `CANTINARR_OAUTH_ISSUER` if you use MCP behind the proxy. Keep the arr callback address separately reachable by your library managers. See [remote access](/install/remote-access/).

## An outbound proxy is a separate setting

**Settings > Outbound Proxy** controls internet requests made by the server, such as metadata and hosted AI calls. It does not route connections to library managers, download clients, or media-server instances. See [outbound proxy setup](/integrations/outbound-proxy/).
