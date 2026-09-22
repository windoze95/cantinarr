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

Sonarr can keep several separately listed TMDB titles under one series, such as **Monster (2022)**. Opening that series from **TV Shows > Recently Downloaded**, **Airing Next**, or a TV episode in **Releases** opens the regular title page with its poster, overview, status, and every library season together. There is no title chooser. The page keeps the library the entry came from.

Select seasons as you would for any other series. Cantinarr translates their numbers to the source titles internally and applies the same approval rules and request allowances. A request spanning several source titles can partly succeed. If that happens, the page refreshes accepted seasons and asks you to review the remaining ones before retrying. Problem reports start with a season so they reach the correct source title. Search results and story-specific notifications retain their separate catalog identities.

Missing, paused, conflicting, or temporarily unreadable matches do not prevent the series from opening. Each affected season stays visible with an explanation and cannot be requested until its match can be verified. Other seasons remain usable. This also covers newly announced seasons and series with no working matches. Availability still comes from the live library; an unreadable status says **Unknown**, not **Not added**. Use **Retry TV match** after correcting a match or restoring access.

The season picker's **All**, **First**, and **Latest** controls select only requestable seasons. For accounts without season choice, an **All seasons** policy skips blocked seasons; **First season**, **Latest season**, and **Pilot only** never silently substitute a different season when their intended target is blocked. Problem reports require a verified source match too.

Kids-account limits remain separate from request matching. Every season must have an identifiable source that the account may see before the combined parent's artwork and overview are shown. An unknown source or unreadable rating keeps the combined page unavailable; pausing a match does not bypass a rating limit.

These links read current library data and mappings on opening and refresh. A removed library, revoked grant, or unreadable series shows an unavailable or retry page with working back navigation. It never switches the TV tab to Movies.

This navigation requires a server that supports TV library resolution. With an older server, cards without a usable catalog ID cannot be opened.

## If an edit is rejected

Another edit may have changed the mapping since you opened it. Refresh and review the latest revision. Do not repeatedly submit a stale mapping or use a different title to bypass an unresolved match.

For implementation detail, see [TV identity and season corrections](/reference/generated/architecture/tv-identity-and-season-corrections/).
