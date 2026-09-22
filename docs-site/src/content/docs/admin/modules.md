---
title: Modules and navigation
description: Decide which optional catalogs appear and keep the attention menu useful.
sidebar:
  order: 11
---

Cantinarr's navigation follows the configured services, account permissions, and a few explicit visibility choices. Hiding a shortcut does not remove the underlying data or revoke access.

## Unconfigured discovery tabs

Administrators can browse supported catalogs before setting up their service. An unconfigured tab offers **Set up** and **Hide this tab**. Setup returns to the same catalog or title.

Change these choices under **Settings > Modules > Discover > Discover tabs**. Hiding an unconfigured tab affects the server's users. Connecting its service restores the tab automatically, even when the service is currently offline. Removing that service makes the saved hide preference relevant again.

Releases hides only when Movies, TV Shows, and Music are all hidden. An empty release schedule alone does not hide it.

## Setup checklist

The checklist reads the server's actual configuration. **Skip** removes an optional item from reminders and progress counts. Skipped items stay visible on the checklist and can be restored.

Skipping does not configure the feature, grant a user access, or change Discover visibility. The Settings entry remains available after the navigation reminder disappears.

## Needs attention

Administrators can keep Approvals, Issues, Agent fixes, and Profile approvals pinned, or show them only when relevant work exists. These preferences are local to the device.

The group's badge reflects its actionable entries. Quietly observed issues can remain accessible without increasing the actionable count. Settings always offers a way to reopen a hidden queue.

## Media server access guide

Users can hide this shortcut after finishing setup. Its Settings entry remains. A new server grant restores the shortcut so the person can finish the new account setup.

## When visibility looks wrong

Check the current role, service grant, module setting, and server version. A disconnected service, an unconfigured service, and an unauthorized account are different conditions. See [missing content](/troubleshooting/missing-content/).
