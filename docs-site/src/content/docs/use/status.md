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

## 4K on covers

Turn on **Settings > Display > 4K badges** to mark titles your library holds in 4K. The setting applies to the device you change it on. Posters then carry a small **4K** tag in the bottom-right corner, and search results show a **4K** chip beside the status. Screen readers hear "Available in 4K".

| Title | Shows 4K when |
| --- | --- |
| Movie | Radarr measured the movie's file at 4K |
| Show | Every aired episode is in the library and Sonarr measured every one of those files at 4K |

A show with some seasons or episodes in 4K and others in HD shows no tag. So does a show that is still missing episodes, even if every file it has is 4K.

The tag describes the same library as the status beside it, your default library. If you also have access to a separate 4K library, open the title and use its library chips.

Cantinarr reads the picture size Radarr and Sonarr measured from the file. The file's quality name is not enough, because with file analysis switched off the arr takes it from the release name. A 4K title with no tag usually means the arr has not analyzed that file. An administrator can open the arr's **Settings > Media Management** with **Show Advanced** on, and check that **Analyze video files** (Radarr) or **Analyse video files** (Sonarr) is enabled. Then run **Refresh & Scan** on the title in the arr.

## Libraries and formats have their own state

A title available in one Radarr instance may be missing from another. A book's ebook can be available while the audiobook is still requested. Changing the selected library or format changes the question Cantinarr is answering.

When asking for help, include the selected library, the format or seasons, and the exact message you see.
