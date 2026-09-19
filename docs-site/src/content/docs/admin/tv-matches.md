---
title: Correct a TV match
description: Repair a TMDB-to-TVDB series or season mismatch without silently rewriting existing requests.
sidebar:
  order: 9
---

Cantinarr browses TV information from TMDB while Sonarr uses TVDB identities. Usually the bridge is automatic. Some shows have different names, series splits, or season organization across those catalogs.

## Open the correction editor

As an administrator, use **Settings > Request Settings > TV matches**, or open **Report a problem > Correct TV match** on the TV detail page.

The correction shortcut can appear even before a request succeeds when an identity problem blocks requesting. Opening it does not submit a request or issue report.

## Select the exact target

Search Sonarr or enter the intended TVDB ID. Review the title and map every source season explicitly to its target season. Do not assume the season numbers happen to agree.

The editor shows whether a mapping is bundled with Cantinarr or is a local override. Save the correction only when the target and season mapping are understood.

## What the actions mean

- **Save** creates or updates your local override.
- **Pause matching** blocks resolution for that entry while you investigate.
- **Restore bundled / default** removes the local override behavior.

These controls do not change Sonarr monitoring, move existing files, or rewrite the recorded history of older requests.

## Deal with affected requests

Review the affected-request preview. It distinguishes recorded and intended targets, the pinned library, and the exact season or pilot scope. Legacy targets that cannot be verified stay labeled as such.

Confirming a correction creates a linked corrective request through the normal request and approval path. The original history, files, and monitoring remain intact. Inspect those separately before removing anything from Sonarr.

## Open a combined library series

Sonarr can keep several separately listed TMDB titles under one series, such as **Monster (2022)**. Opening that series from **TV Shows > Recently Downloaded** or **Airing Next** displays **Choose a title** when its seasons map to several titles. Choose the story you want. A TV episode in **Releases** already identifies a season, so it opens that season's mapped title directly. Both keep the library the entry came from.

These links use the current mappings on each tap. If resolution fails, you stay on the current tab. Retry after checking library access and the mappings in **TV matches**. Paused, overlapping, or unmapped seasons must be resolved before Cantinarr can offer a complete title list. Rating limits still apply to each title.

This navigation requires a server that supports TV library resolution. With an older server, cards without a usable catalog ID cannot be opened.

## If an edit is rejected

Another edit may have changed the mapping since you opened it. Refresh and review the latest revision. Do not repeatedly submit a stale mapping or use a different title to bypass an unresolved match.

For implementation detail, see [TV identity and season corrections](/reference/generated/architecture/tv-identity-and-season-corrections/).
