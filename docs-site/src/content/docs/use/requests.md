---
title: Make and follow a request
description: Choose the title and library you want, understand approvals, and recover a saved request without starting over.
sidebar:
  order: 2
---

Open a title and choose **Request**. The options depend on the media type, your account permissions, and the libraries you can use.

## Review the choices

- **Library:** where the request should go. If you can access more than one library, choose carefully. A 4K library and a standard library may have different contents.
- **Seasons:** the TV seasons you want, when your account permits selecting them. Specials are separate from ordinary seasons.
- **Quality:** an administrator-provided profile, when your account permits choosing one. A profile guides the library manager's future release selection.
- **Format:** ebook, audiobook, or both for books.

If a choice is absent, your administrator may have set it for your account. There is no need to look up an API key or a TVDB ID to make a normal request.

## After you submit

Cantinarr saves the request before contacting the library manager. This is why an accepted request can remain visible during a provider outage.

If your account needs approval, it waits for an administrator. Otherwise, delivery starts in the background. Cantinarr checks the selected library again when delivering, including after a retry, so a lost network response does not automatically mean it should add the title again.

Open **Requests** to follow what you asked for. The title's live availability and its saved request are different pieces of information. See [status meanings](/use/status/).

## Approval and delivery are different

**Waiting for approval** needs an administrator's decision. A message about contacting Radarr, Sonarr, Chaptarr, or Lidarr means delivery is waiting on a service. Approving the request again cannot repair a service connection.

An administrator can review approval choices, including permitted season changes. A denied request is not a failed download. Read the decision before requesting again.

## Request allowances

Your administrator may limit movies, TV seasons, ebooks, audiobooks, or albums within a rolling time window. Open **Settings > Account > Request allowance** to see your account's details.

If a request would exceed an allowance, the message names the affected category. Selecting both book formats must fit both allowances. Requesting something you already requested does not buy a second place in the queue.

## Needs attention, retries, and cancellation

Temporary delivery failures are retried in the background. A request that requires a configuration or identity decision can show **Needs attention**. Read its explanation, correct the cause, then use the offered retry action.

Use cancellation when you no longer want a saved request and the control is available. Cancelling your saved request is not the same as deleting an already imported file or cancelling someone else's shared request. An administrator manages those separate actions.

Changing a default library later does not silently move an existing saved request. It stays tied to its original library. See [request troubleshooting](/troubleshooting/requests/).

## TV names and season numbers

TMDB and Sonarr's TVDB catalog sometimes organize a show differently. If the wrong series or seasons are selected, report the exact mismatch. An administrator can use **Correct TV match**. Do not keep requesting a similarly named show as a workaround.

[TV match corrections](/admin/tv-matches/) explain how corrections preserve request history and avoid silently altering existing files.
