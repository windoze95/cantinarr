---
title: AI and MCP problems
description: Identify which provider, account, model, or authorization path is failing.
sidebar:
  order: 8
---

## First, check the effective provider

Open **AI Access**. Is this conversation using a personal provider or included access? A broken personal override does not fall back silently to the shared provider. Remove it explicitly if the user intends to return to included access.

Remediation always uses the administrator's shared provider. A reporting user's working personal account does not repair the agent's shared configuration.

## Saving a provider or model fails

The save requires a small real response. Read the validation category:

| Category | Check |
| --- | --- |
| Invalid credential or connection | The selected account link or API credential |
| Model or access unsupported | Whether that exact model is available to the selected account |
| Quota or rate limit | The account's current allowance and retry guidance |
| Temporary provider failure | The provider and network, then retry later |
| Invalid response | Endpoint compatibility, model behavior, and the final base URL |

Signing in successfully does not guarantee every model is permitted. Repeating the same save against an exhausted quota will not replenish it.

## A local model endpoint fails

Use the final OpenAI-compatible base URL, commonly ending in `/v1`; redirects are not followed. Enter the exact served model ID and any token required by your proxy.

The endpoint connects directly unless its explicit proxy-routing switch is enabled. A reachable URL from your laptop is not proof that the Cantinarr server can reach it.

## OpenAI OAuth helper errors

Official images bundle the pinned helper. Native installations need the matching `codex-app-server` available on the service's PATH, or an explicit `CANTINARR_CODEX_BIN`.

Check the configured memory-backed runtime directory and ownership. A pre-existing directory must be private to the server user with mode 0700. Preserve encrypted credentials in `/config`, not in a manually copied plaintext helper home.

If using an outbound proxy, use HTTP or HTTPS for this provider. Helper SOCKS behavior is not verified by Cantinarr.

## A conversation lost its earlier context

Conversations are temporary process memory, tied to the user, effective provider identity, and model. Restarts, provider failures, or identity/model changes can start a fresh conversation. The app's focused session and server retention have different time boundaries; neither is a permanent transcript archive.

## MCP connects but tools are missing

Check the current account's role, tool toggles, endpoint, and authorization. Administrative tools do not become available just because an external client connected.

Behind a proxy, confirm the canonical HTTPS issuer and any required browser-origin entry. Reconnect if the issuer changed. Use the exact `/mcp` endpoint and Streamable HTTP support.

MCP sign-in grants the external client access to Cantinarr. AI Access OAuth grants Cantinarr access to an AI provider. Troubleshoot the failing direction rather than relinking both accounts.
