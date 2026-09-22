---
title: Missing tabs, titles, or settings
description: Check the selected catalog, user grants, content policy, and version before assuming data was lost.
sidebar:
  order: 3
---

## Books or Music is missing

For a requester, check access to the intended Chaptarr or Lidarr instance. These services have no global default that automatically grants everyone access. The person needs a pin or explicit library grant.

For an administrator, check whether the unconfigured tab was hidden under **Settings > Modules > Discover > Discover tabs**. Connecting the service restores it even if the service is later offline.

## A movie or show does not appear

Check the active search context, spelling, title year, filters, and selected library. On a kids account, inspect the rating policy and whether the title's rating could be read.

The English-only discovery choice affects discovery and recommendation rows, not ordinary search. A failed metadata read is different from a successful search with no match.

## An available title looks missing in another view

Confirm both views use the same instance and exact identity. A 4K Radarr and a standard Radarr can have different files. Two similarly named albums or book records can have different IDs.

Do not merge records based only on title or author name. Read the selected instance, format, and identity information before changing a request.

## A book's old link stopped working

Open Library discovery and resolution have retired. Follow **Search books** to find the native Chaptarr result. Unresolved saved requests retain their history and approval requirements rather than guessing at a replacement identity.

## A setting is absent from search

Settings search respects role, permissions, available services, and server capabilities. An ordinary user will not see every administrative control. A newer app connected to an older server may also hide unsupported actions.

Check **Settings > About** for both versions. Use the [settings directory](/reference/settings/) to locate the owning screen.

## An attention queue disappeared

Approvals, Issues, Agent fixes, and Profile approvals can hide while empty. Their device-local visibility choices live in Settings and on the relevant queue screens.

The media access guide can also be hidden locally after setup. Open **Settings > Guides > Media server access** to return to it.

## A list is empty after an error

Read whether the app found no records or could not read them. A timeout or permission failure is not a zero count. Retry the failed read and check the corresponding service before recreating records.
