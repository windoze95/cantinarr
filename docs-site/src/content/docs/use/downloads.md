---
title: Follow downloads
description: See movie, episode, album, and book progress, filter your requests, and understand the Downloads count.
sidebar:
  order: 5
---

Open **Downloads** in the menu to follow active transfers. The Content view uses titles and artwork from your library managers. Your library access and any kids-account restrictions still apply.

## Find your downloads

1. Open **Downloads**. Administrators select **Content** on the **Queue** tab.
2. Choose **All downloads** to see downloads in your accessible libraries, or **My requests** to follow jobs linked to your saved requests. Your choice is remembered for this account and server on this device.
3. Expand a title to see its jobs. Shows expand into seasons and confirmed episodes. Albums show confirmed tracks when the provider identifies them. Books show their author and ebook or audiobook format when known.

If the administrator restricts visibility to own requests, the screen shows **My requests** and hides the filter. Users do not receive the Clients view, History, client selector, or management actions.

Library instances remain separate, even when they contain the same title. Multiple downloads of a title remain separate jobs. A season pack or album shares one job's progress across its contents. **Detailed contents could not be identified** means the provider has not confirmed which episodes or tracks belong to that job.

## Understand the menu count

The badge counts unfinished download jobs. Queued, paused, stalled, and failed unfinished transfers count. Opening a group does not change it. Completed or seeding jobs, import-only processing, and requests that have not started a download do not count.

For users, the badge follows the current All/My filter and access rules. Administrators always receive the server-wide count, including unmatched jobs. The badge disappears at confirmed zero. A `?` means the exact count is unavailable; check the notice in Content and retry. Foreground updates use events and a 15-second polling fallback, including while another module is open.

## Administrator views and controls

On **Queue**, switch between **Content** and **Clients**. The initial selection is Clients, and your selection is remembered per account/server on this device. History remains available for completed transfers. The client selector stays in the menu beside the count and applies to Clients and History.

Expand a Content title and open a job's action menu to pause, resume, or remove it. Removal applies to the entire download, including a whole season pack or album, and asks for confirmation. The existing option to delete downloaded data depends on the client; NZBGet removes the queue item and leaves files on disk.

Controls appear only when Cantinarr can verify the exact client and job. Progress reported by a library manager still appears when that client is not connected directly in Cantinarr. **Unmatched downloads** contains directly connected client jobs that could not be assigned to content. Only administrators can see their raw names.

## Set user visibility

On **Downloads > Queue**, open **Downloads settings** and choose under **User visibility**:

- **All accessible downloads**: the default. Users can switch between all downloads in their accessible libraries and their own requests.
- **Own requests only**: immediately forces My requests for every non-admin account.

This server-wide preference does not grant access to another library. Book and music access still requires the corresponding instance grant.

If activity is incomplete, use [download troubleshooting](/troubleshooting/downloads/). If a transfer has finished but the title is not available, check the library manager's import queue. Older servers without Content support retain the existing admin Downloads view and do not expose requester Downloads.
