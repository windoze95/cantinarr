---
title: Review configuration changes
description: Understand profile proposals, trusted change receipts, and when a saved change can be restored.
sidebar:
  order: 10
---

Cantinarr records supported AI and MCP changes to external quality profiles and custom formats. Open **Settings > Configuration History** to read what changed and compare it with the service's current state.

## In-app assistant changes

An explicit administrator request can preview and apply a supported quality-profile change within one authenticated chat turn. Cantinarr returns a trusted review receipt after checking the actual result.

The assistant's prose does not create an approval or restore control. Use the controls rendered from the server's recorded action.

## External MCP proposals

An external MCP client can prepare a quality-profile proposal. It cannot use the in-app-only apply handoff to finish that write. Review the durable proposal in **Profile approvals** inside Cantinarr.

A new proposal is a reason to inspect the diff, not a reason to assume the external service already changed.

## Scope of supported changes

Quality-profile writes are deliberately narrow. They can adjust supported upgrade policy, an already allowed cutoff, score thresholds, existing custom-format scores, and Radarr's profile language.

They do not create, delete, rename, or reorder whole profiles, or turn that action into a batch across every instance. Language behavior also differs across Radarr, Sonarr, and Chaptarr. Consult the [tool reference](/reference/generated/architecture/mcp-tools/) for exact limits.

## Restore a profile change

A guarded restore is offered only while the live profile, relevant dependencies, and instance connection still match the recorded applied state. A direct edit in the service can make restore unavailable.

Restore is one-use. A successful restore creates its own history entry rather than erasing the original action. If the live state changed, review a fresh proposal instead of overwriting newer work.

Custom-format writes have readable history but are not restorable through this control.

## Future selection is different from existing media

Changing a quality or language rule influences future release selection. It does not inspect or remux downloaded streams, change an existing file's default audio track, or guarantee a playback language.

For a wrong-audio complaint, inspect the actual imported file and its history before changing a profile. See [download and import troubleshooting](/troubleshooting/downloads/).
