---
title: Approvals and request allowances
description: Decide what people can request, when approval is needed, and how rolling limits are counted.
sidebar:
  order: 4
---

Use **Settings > Request Settings** for defaults, then the user's request settings for exceptions. Check both layers when one person sees a different request flow.

## Approval rules

You can require approval for new requests globally or per user. Pending approvals appear in the administrator's approval queue. The request is already saved while awaiting a decision.

Review the exact title, target library, seasons or book formats, and selected quality before approving. A delivery outage is separate from approval. Approved requests can wait for service recovery without re-entering the approval queue.

## Request history

Open **Approvals > History** to see saved requests from everyone on the server, including requests accepted without manual approval. If Approvals is hidden in the navigation menu, open **Settings > Approvals**, then choose **History**.

Search by title or filter by requester, media type, and decision. The list starts with the newest saved request; **Load older requests** reads the next page. Shared book requests also appear when filtering by a subscriber.

Select a row to see the target library, requested seasons or book formats, recorded reviewer and decision date, and any denial reason. Older or automatic requests can have no reviewer or decision date recorded. A missing value is shown explicitly.

An approval records a decision, not proof that a file is available now. Choose **View title** to check current availability in the requested library. Historical books or albums without a usable identity or library have no title link. Removed files do not erase decisions, but this screen cannot recover request records already deleted from the server.

If history cannot load, use **Retry**. A refresh failure keeps the last successful results visible with a warning. An older server without this endpoint prompts you to update it.

## Season and quality choices

Choose whether users may select TV seasons and quality profiles. If choice is disabled, the configured defaults apply. Quality profiles come from the relevant library manager; their names and behavior are not universal between instances.

TV specials are a separate scope. For a series whose TMDB and TVDB season organization differs, correct the identity mapping before approving the wrong season. See [TV matches](/admin/tv-matches/).

## Rolling allowances

Allowances can cover these categories independently:

| Category | What is counted |
| --- | --- |
| Movies | Accepted new movie requests |
| TV | Newly requested ordinary seasons |
| Ebooks | Newly requested ebook formats |
| Audiobooks | Newly requested audiobook formats |
| Albums | Newly requested music releases |

Choose a rolling 1-, 7-, or 30-day window. All allowances start unlimited. Kids inherit ordinary User defaults. Administrators are exempt.

Accepted work counts even when it is pending approval. Duplicate requests, delivery retries, and already available or fully monitored work do not charge again. TV specials are excluded from the ordinary season allowance. Selecting both book formats must fit both categories together.

A rolling window does not reset for everyone at midnight. Each accepted request ages out of its configured window. Users can inspect their own allowance in Account settings.

## Avoid confusing approval with storage policy

An allowance limits requesting. It is not a disk quota, retention policy, or guarantee of delivery. External services still control their own search, download, and import behavior.

Cancelling a saved request does not mean an imported file was removed. Deleting a file in a media manager does not automatically erase the request history.

## Review notifications and navigation

Phone categories can notify administrators about requests needing review. Discord can post those requests to one configured channel. Automatically approved request alerts are a separate option.

The **Needs attention** navigation group can hide an empty queue. Open it from Settings when needed, or change its device-local visibility choice. A hidden navigation entry does not disable approvals.
