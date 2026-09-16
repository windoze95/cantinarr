---
title: Remote access and HTTPS
description: Give phones and browsers a stable address while preserving callbacks, sign-in, and live updates.
sidebar:
  order: 6
---

Choose how users will reach Cantinarr outside your home: a private-network connection they join, or a public HTTPS address routed through your reverse proxy. Use a dedicated hostname, such as `media.example.com`, rather than placing the app under an extra URL path.

Do not expose the library managers just to make Cantinarr work. The Cantinarr server can keep reaching them through internal addresses.

## Configure the hostname

Point your intended hostname at the reverse proxy or tunnel that serves it. Configure a valid certificate and route the full origin to Cantinarr's port. The examples below assume Cantinarr is reachable by the proxy at `127.0.0.1:8585`; use the actual upstream address in your deployment.

If the proxy is another container, `127.0.0.1` means that proxy container. Use a shared-network service name or reachable host address instead.

## Caddy example

```text title="Caddyfile"
media.example.com {
    reverse_proxy 127.0.0.1:8585
}
```

Caddy's reverse proxy supports WebSocket upgrades. Its streaming behavior is described in the [official reverse_proxy reference](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy). Configure DNS, certificate issuance, and inbound reachability as required by your existing Caddy deployment.

## Nginx example

In the `http` context, define the upgrade mapping:

```nginx
map $http_upgrade $cantinarr_connection_upgrade {
    default upgrade;
    '' close;
}
```

Inside your existing TLS-enabled server block for the Cantinarr hostname:

```nginx
location / {
    proxy_pass http://127.0.0.1:8585;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $cantinarr_connection_upgrade;
    proxy_buffering off;
    proxy_read_timeout 3600s;
}
```

This location block does not install a certificate or configure your whole Nginx host. Explicit upgrade headers support WebSockets; buffering is disabled for streamed responses. See [Nginx's WebSocket proxy documentation](https://nginx.org/en/docs/http/websocket.html).

## Set Cantinarr's addresses

1. Set **Settings > External Address** to `https://media.example.com` for invitations and OIDC.
2. If using MCP, set `CANTINARR_OAUTH_ISSUER=https://media.example.com` in the server environment.
3. Set `CANTINARR_ARR_CALLBACK_URL` to an origin the library managers can reach. This may remain `http://cantinarr:8585` internally.
4. Set each media server's **Address users open** to that service's own reachable address.

[Apply environment changes](/install/configuration/#apply-a-change) by recreating the Compose container, then reconfigure affected instant-update webhooks. These fields serve different callers. See [the address reference](/install/networking/).

## Extra authentication in front of the app

A proxy login page can interrupt mobile API calls, OAuth discovery, webhooks, and MCP. A browser that already has the proxy's cookie can work while every other caller fails.

Use Cantinarr's supported sign-in and authorization paths, or deliberately configure your access gateway for every required non-browser client. Do not assume a successful browser visit proves native clients and callbacks work.

## Verify from outside

Using a device outside your home network, check the app, a new invitation, sign-in, a live status change, and any enabled MCP connection. Separately verify the library managers can still send instant updates.

If AI output arrives all at once or live status stops refreshing, inspect streaming and WebSocket support in the proxy. If OIDC returns to the wrong address, check the exact external origin and registered callback.
