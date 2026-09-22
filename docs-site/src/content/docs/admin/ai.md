---
title: AI providers and shared access
description: Configure a working provider, grant access deliberately, and understand validation and background usage.
sidebar:
  order: 7
---

Open **Settings > Providers & Credentials** for the server's included AI profile. Each person manages a personal override under **Settings > AI Access**.

AI is optional. Discovery, normal requests, approvals, and library management do not require every user to have an AI provider.

## Choose a provider

The app supports hosted API-key providers, supported OAuth subscription connections, and a shared **Local (OpenAI-compatible)** provider. Use the provider and model choices shown by your running version; provider model catalogs can change.

| Choice | What you supply | Who uses its allowance |
| --- | --- | --- |
| Hosted API key | The provider's API credential and an available model | The selected personal or shared API account |
| OpenAI (OAuth) | An explicit ChatGPT account link | The connected account's applicable Codex allowance |
| xAI Grok (OAuth) | The supported xAI subscription account link | The connected account's subscription allowance |
| Local (OpenAI-compatible) | The server's final base URL and model ID, with an optional token | Your configured model server |

## Save and test

Selecting a provider, saving a key, changing a model, or completing OAuth must pass a small real response test before activation. The test does not call management tools.

A validation failure can mean invalid credentials, unavailable model access, quota exhaustion, rate limiting, or a temporary provider outage. Read the category shown. Repeating OAuth is not a useful response to every model-access failure.

## Grant included access

Use **Settings > Users** to grant the shared provider to individual people. The initial administrator starts enabled. New invited users do not automatically receive it. Upgraded installations can preserve older access behavior for existing accounts.

A personal provider takes precedence over included access. Personal failures do not silently fall back to the shared account. The user must explicitly remove the personal override to return to included access.

## Local model servers

Choose **Local (OpenAI-compatible)** in the shared profile. Enter the endpoint's final base URL, commonly ending in `/v1`, and the exact model ID it serves. Cantinarr does not follow redirects for this endpoint.

This is a shared-only provider. A person's normal OpenAI API key continues to use OpenAI's hosted endpoint.

Reasoning effort can be left on **Auto** to use the provider's default, or pinned to an offered value. Unsupported effort fields can fall back as described by the provider adapter. Test the actual model before granting access.

The local endpoint connects directly by default. If it is a hosted GPU service that should use your outbound proxy, explicitly turn on **Route through the outbound proxy**. Cantinarr does not guess the network class from the hostname.

## Shared-model health checks

The server can send at most one small shared-model test every 24 hours. Failures open a deduplicated administrator issue; a later successful test resolves it.

Turn off **Daily shared-model test** if you want no background usage from that monitor. Save-time tests remain required. Remediation has its own independent limits and continues to use the shared profile when enabled.

## Tool access and data

**Settings > AI Tools** enables or disables shared tools. Tool permissions still depend on the current caller's role and grants. A provider connection does not grant administrative access.

Prompts, context, and scrubbed tool results go to the effective provider. Read the [privacy policy](/reference/generated/privacy/) and [MCP integration guide](/integrations/mcp/) for the separate external-client path.
