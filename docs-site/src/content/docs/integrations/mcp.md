---
title: Connect an MCP client
description: Give an external assistant authorized access to Cantinarr's tools through its own browser sign-in.
sidebar:
  order: 10
---

Cantinarr exposes a Model Context Protocol server at **`/mcp`** using Streamable HTTP. A compatible client can discover tools, sign in through Cantinarr, and act with that user's permissions.

## Use the server endpoint

Enter your reachable Cantinarr origin followed by `/mcp`, for example:

```text
https://media.example.com/mcp
```

Use your client's supported remote HTTP MCP connection method. Some clients use a configuration shaped like this:

```json
{
  "mcpServers": {
    "cantinarr": {
      "url": "https://media.example.com/mcp"
    }
  }
}
```

Client configuration formats differ. This example identifies the endpoint; it is not a universal command for every MCP application.

## Complete browser authorization

The client discovers Cantinarr's OAuth metadata and opens its sign-in page. Sign in with an allowed method, review the client, and choose **Authorize**.

Password, passkey, configured OIDC, and enabled Plex sign-in paths follow the account's policy. Required OIDC still applies to regular users. On plain HTTP, browser passkeys may be unavailable, so a permitted password is needed for that path.

Connect links are not bearer tokens to paste into a client configuration. The external client receives its own authorization tied to a Cantinarr device record.

## Behind a reverse proxy

Set a stable canonical HTTPS origin:

```yaml
    environment:
      CANTINARR_OAUTH_ISSUER: https://media.example.com
```

This is the external address, not the arr callback address. Changing the issuer can require existing clients to reconnect because their tokens are bound to the prior audience.

For browser-hosted clients, configure `CANTINARR_MCP_ALLOWED_ORIGINS` only for additional actual browser origins that should call `/mcp`. If neither it nor the issuer is configured, requests carrying an Origin header are rejected. Native and server-side clients without that header do not need an extra origin entry.

## Permissions and tools

The current account's role, grants, kids policy, and request allowances apply. Administrative tools remain administrative. **Settings > AI Tools** can disable shared tools for chat and MCP.

The server also provides an operating-guide resource and prompt templates. See the [complete tool reference](/reference/generated/architecture/mcp-tools/) for exact arguments and limits described by the maintained server docs.

External profile changes become proposals for review inside Cantinarr. The in-app-only apply tool is not exposed to external clients.

## Revoke a client

Revoke its device in **Connected Devices**. This also invalidates its associated MCP access. Registered clients and token records survive normal server restarts, while revoked devices remain revoked.

## This is separate from an AI provider link

MCP authorization lets another client access Cantinarr. Linking ChatGPT or xAI under **AI Access** lets Cantinarr call that provider. Completing one does not configure the other.

If tools do not load, check the exact endpoint, proxy routing, canonical issuer, browser-origin policy, current device authorization, and tool toggles. Do not paste tokens into a public support report.
