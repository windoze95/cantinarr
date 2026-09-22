---
title: What each status means
description: Tell an approval wait, a delivery delay, a download, and a ready file apart.
sidebar:
  order: 3
---

Start with the message on the title or request itself. A short status label summarizes a stage; the accompanying text explains what is waiting.

| Status or message | What it tells you | What to do next |
| --- | --- | --- |
| **Request** | You can ask for this title or format | Review the options and submit |
| **Checking…** | The app has not finished checking the library | Wait for the result; use Retry if the check fails |
| **Waiting for approval** | Your request is saved and needs an administrator's decision | Follow the request; an administrator reviews it |
| **Requested** | The title is requested, but not yet available | Read any delivery explanation and check whether a release exists |
| **Downloading** | There is current download activity | Wait, or have an administrator inspect a stalled transfer |
| **Partial** | The library contains some of the relevant content | Open the detail page to see which seasons or files exist |
| **Available** | The selected library reports the relevant file or content | Open it with an authorized playback or listening account |
| **Needs attention** | A saved request needs a decision or a corrected configuration | Read the cause, fix it, then retry if offered |
| **Unavailable / could not check** | A service or provider could not be read | Retry or investigate the connection; do not treat this as a missing title |

## Requested does not promise an immediate download

A movie can be requested before its home release. A show can have unaired episodes. A book or album can lack a suitable release in your configured sources. Cantinarr can accept the request while the library manager waits.

The administrator should check whether the request reached the intended library before changing download settings.

## Downloaded is not always imported

A download client finishing a transfer is one step. The library manager still needs to identify the files, read them, and import them into the library. A path, permission, archive, or identity problem can stop that step.

See [downloads and imports](/troubleshooting/downloads/) if the transfer finished but the library does not show the file.

## Available is not a playback guarantee

Availability comes from the live library manager. Plex, Jellyfin, Emby, or Audiobookshelf may still need to scan the file. Your playback account must also be authorized for that library.

An available file can have an incorrect episode, audio track, or copy. Report that from the title. The empty download queue is normal after import and does not prove the file is correct.

## Libraries and formats have their own state

A title available in one Radarr instance may be missing from another. A book's ebook can be available while the audiobook is still requested. Changing the selected library or format changes the question Cantinarr is answering.

When asking for help, include the selected library, the format or seasons, and the exact message you see.
