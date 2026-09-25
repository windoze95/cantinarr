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

## Library views

Open the **Library** tab in Radarr, Sonarr, Chaptarr, or Lidarr, then choose **List** or **Grid** beside the library filter controls. On narrow screens, the switch uses list and grid icons in the search row. Each icon has a tooltip and an accessible label.

On phones, scrolling down fades and collapses the library title and counts to make room for more items. The filter field, filter menu, and view switch stay visible. Scroll up to bring the title and counts back.

Grid displays movie and series posters, author covers, or artist artwork. Phones show three columns; wider screens fit more. Select an item to open its details. The visible item actions work with a pointer or keyboard in either view.

Switching views keeps your current search, status filter, and results. Each module remembers its own choice on this device, including after an app restart. The choice applies to all configured instances of that module. List is the initial default.

If artwork cannot load, the item keeps its name and a placeholder icon. If the library cannot load, use **Retry** in the error message. An empty result after a successful load means no items matched; clear the search or change the status filter to broaden it.

## Setup checklist

The checklist reads the server's actual configuration. **Skip** removes an optional item from reminders and progress counts. Skipped items stay visible on the checklist and can be restored.

Skipping does not configure the feature, grant a user access, or change Discover visibility. The Settings entry remains available after the navigation reminder disappears.

## Needs attention

Administrators can keep Approvals, Issues, Agent fixes, and Profile approvals pinned, or show them only when relevant work exists. These preferences are local to the device.

On desktop, choosing a queue keeps **Needs attention** expanded. Select **Needs attention** again to collapse it. On mobile, the group resets when the navigation drawer closes.

The displayed entry is highlighted in the menu. **Issues** stays highlighted in issue threads, and **Agent fixes** stays highlighted on run details. The highlight follows the current page when using Back or opening a direct link.

The group's badge reflects its actionable entries. Quietly observed issues can remain accessible without increasing the actionable count. Settings always offers a way to reopen a hidden queue.

## Media server access guide

Users can hide this shortcut after finishing setup. Its Settings entry remains. A new server grant restores the shortcut so the person can finish the new account setup.

## When visibility looks wrong

Check the current role, service grant, module setting, and server version. A disconnected service, an unconfigured service, and an unauthorized account are different conditions. See [missing content](/troubleshooting/missing-content/).
