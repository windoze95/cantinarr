---
title: Supervised repairs
description: Configure what the issue agent may investigate, review proposed fixes, and manage standing approvals.
sidebar:
  order: 6
---

The remediation agent investigates issues using the administrator's shared AI provider. It does not borrow a reporting user's personal AI credential or require that user to have included AI access.

Open **Settings > AI Remediation** to review its operating limits before enabling automatic dispatch.

## Choose the behavior

- **Investigate only** gathers evidence and explains findings.
- **Prepare a fix** allows the supervised workflow to prepare a proposed action for review.

Enabling the agent and enabling **Auto-dispatch on detected problems** are separate choices. Problem reporting is also a separate setting. Set them to match how much automatic investigation you want.

## Control time and usage

Settings include observation timing, quiet time after arr activity, recovery settle time, maximum steps, output budget, wall-clock limit, daily run cap, follow-up wait, and the number of failed automatic investigations before pausing.

Longer observation windows reduce premature investigations while a download is still recovering. Smaller run limits reduce usage but may leave the agent needing administrator help. The [settings directory](/reference/settings/) locates each control.

## Review a proposed fix

Open **Agent fixes** and read:

1. The exact library and media scope.
2. The evidence for the diagnosis.
3. The proposed action and what it removes, changes, or searches.
4. The expected result and any limitation in the evidence.

Do not approve a file removal just because the queue is empty. Content complaints need imported-file and history evidence.

## Blocklisting and replacement

Removing a bad queue item, blocklisting it, and triggering a replacement are distinct behaviors. The library manager's own failed-download policy can initiate replacement after a normal blocklist action.

**Blocklist without replacement** suppresses that replacement path. Cantinarr applies additional protection for verified unaired TV content so clearing a bad early download does not immediately search for the same unreleased episode again.

Existing library files and monitoring are separate from queue cleanup. Read the action receipt for what actually happened.

## Standing auto-approvals

The approval dialog can offer a standing rule for a supported recurring action. Review its stated scope before enabling it. These rules are not blanket permission for arbitrary agent changes.

Manage them in **Settings > Agent Auto-Approvals**. The list shows active or paused state, pause reasons, and an approval/resolution history. Pause a rule when its pattern no longer fits your setup. Deleting it preserves the audit history of decisions already made.

## Model selection

The agent uses the shared provider and model unless you save a separately tested remediation-model override. Changing the shared provider can invalidate that override's binding and return runs to the shared model until the new override is tested.

The daily shared-model health test is separate from remediation. Disabling that background health test does not disable save-time credential validation or the agent itself.

## Verify the outcome

A successful upstream API response is not always proof of correct media. Check the resulting library, file, and playback when relevant. Issue timelines and trusted action receipts show the server's recorded work; they do not certify what appeared on a television screen.
