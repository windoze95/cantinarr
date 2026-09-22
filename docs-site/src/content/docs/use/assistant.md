---
title: Use the AI assistant
description: Ask about your library, request a title, and understand which account pays for the conversation.
sidebar:
  order: 8
---

The assistant can search catalogs, inspect the library information your account may read, and request media under your normal permissions and allowances. Administrators also have management tools.

## Get access

Open **Settings > AI Access**. You can use an administrator-granted included provider, configure a supported personal API key, or link a supported subscription account. The available providers and models are listed in the app.

Saving a provider, credential, or model runs a small real response test. A successful sign-in alone does not prove the selected model works. Read any validation message before changing another setting.

## Ask a concrete question

Examples:

- “Find a short comedy we do not already have.”
- “Is this series available in our TV library?”
- “Request the audiobook for this book.”
- “What happened to my last movie request?”

Check the returned title, year, format, and library before relying on it. Tool activity shows when the assistant reads or changes something. A normal text response is not itself proof that a request was saved.

The search field can offer **Ask AI**. Choosing it explicitly opens the assistant with your question. Ordinary title search remains available without AI.

## Personal and included access

A personal choice overrides the included provider. Cantinarr does not silently spend shared quota when your personal provider is unavailable. Remove the override explicitly to use included access again, if granted.

API-backed providers can charge the selected account. Subscription-backed providers use the connected account's applicable allowance. Several people using an included connection share that connection's allowance.

## What the provider receives

The server sends your prompt, conversation context, and scrubbed tool results to the selected provider. Credentials stay on your Cantinarr server. The provider processes the submitted information under its own terms.

Assistant conversation context is temporary process memory. Server restarts, provider failures, or changes to the effective account or model can start a new conversation. It is not a permanent chat archive. See the [privacy policy](/reference/generated/privacy/).

## For administrators

An explicit request can use supported management tools, including bounded profile changes. Review the trusted receipt shown by Cantinarr. Settings changes can affect future release selection, and do not automatically change audio tracks in existing files.

The interactive assistant and the issue-remediation agent have different roles. The remediation agent always uses the administrator's shared provider and its configured operating limits. Read [AI configuration](/admin/ai/) and [supervised repairs](/admin/remediation/) before enabling automation.
