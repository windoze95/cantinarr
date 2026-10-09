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

**OpenAI (OAuth)** offers **OpenAI recommended**, GPT-6 Astra, GPT-6.1 Sol, GPT-6 Sol and Luna, and GPT-5.6 Sol, Terra, and Luna Codex selectors. These choices also appear in the remediation model picker when the shared provider is OpenAI OAuth. These choices use the linked ChatGPT account through Codex. The separate API-key provider uses independently documented API IDs and chooses the endpoint required for tool calling, including Responses for GPT-6 Astra and GPT-6.1 Sol. Access depends on the selected account, so every selection must pass the response test.

The OpenAI API-key provider has a separate model catalog of public API IDs supported by Cantinarr's Chat Completions adapter. Do not select Codex or ChatGPT product names as API model IDs.

## Save and test

Selecting a provider, saving a key, changing a model, or completing OAuth must pass a small real response test before activation. The test does not call management tools.

A validation failure can mean invalid credentials, unavailable model access, quota exhaustion, rate limiting, or a temporary provider outage. Read the category shown. Repeating OAuth is not a useful response to every model-access failure.

## Fall back when a model becomes unavailable

**Fall back to the recommended model** is off by default. Enable it under **Settings > AI Access** for your personal profile, or **Settings > Providers & Credentials** for the shared profile, then save. You can change this preference after a saved model has retired without testing that retired model again. New model and credential selections still require a successful test.

The saved model stays selected. If the provider confirms that it is unavailable, Cantinarr can try one recommended replacement using the same provider, endpoint, account, and billing source. Cost, latency, and capabilities may change. The setting does not move personal usage to included access or local usage to a hosted provider.

- **OpenAI (OAuth):** uses the linked account's Codex model catalog, preferring a compatible recommended upgrade and otherwise its recommended default. **OpenAI recommended** remains a separate selection. A catalog recommendation does not guarantee account access.
- **Hosted API keys and xAI Grok OAuth:** use the explicitly labeled **Cantinarr recommendation** shown beside the switch. These are maintained choices for that integration, not the newest model in an availability list.
- **Local (OpenAI-compatible):** has no standard recommendation metadata. Choose a replacement manually; the fallback control is unavailable.

Chat shows the selected model, replacement attempt, reason, and known capability differences. Administrators receive a system issue with the same details and a provider-settings link; push follows their issue notification preference. Repeated requests using the same profile and replacement do not create more notifications, including after the issue is dismissed or the server restarts. Personal notifications identify the owning user; only that user can edit their personal provider in AI Access.

Invalid credentials, quota or rate limits, temporary outages, unrelated request errors, and a missing API route do not trigger fallback. Chat cannot restart after text or tool activity has begun. If the replacement also fails, or no compatible recommendation is available, the error asks you to review AI settings. Cantinarr does not try a second replacement or rewrite your saved model.

The shared preference also covers a separate **AI Remediation** model override, within its bound provider. Remediation retries only the failed model call, preserving earlier tool results. Its run history records the model change. Save-time probes and daily health tests continue to check the selected model itself.

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
