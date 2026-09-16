---
title: Outbound proxy
description: Route server internet traffic through a proxy while keeping local service connections direct.
sidebar:
  order: 11
---

An outbound proxy affects requests Cantinarr sends to the internet. It is separate from a reverse proxy, which lets people reach Cantinarr.

## Configure it

Open **Settings > Outbound Proxy**. Enter an `http`, `https`, `socks5`, or `socks5h` proxy address with no path, plus a username and password if needed.

For example, `http://proxy:8118` is appropriate only when that proxy really runs on port 8118 and is reachable from the Cantinarr server.

Use **Test**. The test requests TMDB's configuration through the proposed proxy. Save after it succeeds. A blank address clears the setting. A blank password on a later edit preserves the stored password rather than displaying it.

## Which traffic uses it

Internet-bound services include metadata providers, hosted AI providers, plex.tv, the GitHub release check, and the push relay. The in-app proxy setting applies to this traffic without a bypass list.

Library managers, download clients, media-server instances, monitoring instances, and the default Local AI connection use direct transport. Standard `HTTP_PROXY` settings do not force these internal connections through a proxy.

The distinction is based on the service's purpose, not a guess about whether its hostname looks local.

## Explicit exceptions

A Local AI endpoint can opt into external proxy routing with **Route through the outbound proxy**. Use that when an admin-entered endpoint is an internet-hosted model service.

An external OIDC provider has its own **Use outbound proxy** choice. It covers discovery, key retrieval, token exchange, and UserInfo. It starts off so self-hosted identity providers remain directly reachable.

## Environment variables

When the in-app proxy is empty, the standard `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` environment variables apply to external traffic under Go's proxy rules. A saved in-app proxy wins over those variables.

For a self-hosted push relay on the LAN, use the environment-variable configuration with an appropriate `NO_PROXY` entry. The push relay is still classified as external traffic, and the in-app setting has no bypass list.

## OpenAI OAuth helper

The bundled helper receives proxy configuration through its environment. Use an HTTP or HTTPS proxy for this provider; Cantinarr does not verify the helper's SOCKS support.

## Devices still load artwork

This setting affects the server. Devices still need their own access to the artwork hosts they load directly. A passing server proxy test does not prove a phone can reach those CDNs.

If a local instance stops working, inspect its direct route and DNS. Adding its hostname to `NO_PROXY` is not a fix for an internal transport that already bypasses the proxy.
