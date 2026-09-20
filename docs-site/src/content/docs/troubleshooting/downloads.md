---
title: Downloads and imports
description: Diagnose a stuck transfer, a failed import, or incorrect content that is already in the library.
sidebar:
  order: 5
---

A download client transfers files. The library manager imports them. Playback services scan and serve them. Check which step actually failed before selecting a repair.

## The transfer is still active

Inspect the client state, progress, peers or provider connection, free space, and recent activity. A torrent briefly reporting “stalled” while connecting is not enough evidence to remove and blocklist it.

Cantinarr's observation windows let transient problems settle before automatic escalation. A failed upstream read is not treated as an empty queue or confirmed recovery.

## Progress or the menu count is unavailable

Open [Downloads](/use/downloads/) and check the incomplete-activity notice. `?` in the menu means Cantinarr cannot read an exact current count. It does not mean zero. Retry after the named library manager or download client is reachable again.

Missing artwork uses a placeholder and does not hide a known title. Missing episode or track identity shows **Detailed contents could not be identified**. A season pack or album has shared job progress; Cantinarr does not divide it into invented episode or track percentages.

If an administrator sees **Client controls unavailable**, compare the client's configured endpoint in the library manager with its Cantinarr connection. Matching names or categories cannot establish that they are the same client. DNS aliases and differing proxy paths may leave the relationship unverified; the existing Clients view still offers the directly connected client's controls.

## The transfer finished, but import is blocked

Open the relevant arr queue and its Import Doctor explanation. Common causes include:

| Finding | Check before taking action |
| --- | --- |
| Remote path mapping | Can the library manager see the path reported by the download client? |
| Permission denied | Can the library manager's process read the source and write its own destination? |
| Unextracted archive | Is the expected unpacking step configured and finished? |
| Sample or invalid media | Is the file actually the requested content and of a plausible duration? |
| Not an upgrade | Does the existing library file already satisfy the selected profile? |
| Unrecognized identity | Does the candidate match the intended movie, episode, book, or track? |
| Missing disk space | Which filesystem is full: downloads, import destination, or temporary work? |

These are the library manager's paths and permissions. Cantinarr's optional media-download path mappings solve a different problem and do not repair Radarr or Sonarr's own imports.

## Manual import

Review candidates and their rejection reasons before forcing an import. Check the exact episode or track mapping, file size, and expected format. Forcing the wrong file can create an “available” title that still contains incorrect content.

Use the narrowest supported action and verify the imported file afterward. A successful action response is only the first check.

## Remove, blocklist, and replace

Read whether the action removes a queue item, deletes files, blocks a release, or permits the library manager to search again. **Blocklist without replacement** deliberately suppresses replacement.

For verified unaired TV content, Cantinarr protects the repair path from immediately searching for an episode that has not aired. This can resolve a bad early download as removed while waiting to air. It does not claim the episode became available.

## The wrong content is already imported

An empty queue is expected. Start from the library file and the title's own grab and import history.

For TV, inspect episode identity, air time, import time, runtime, and the actual file. For audio complaints, inspect the file's analyzed audio tracks and languages. For books and music, use the exact book format or album/track identity.

Do not filter a short page of global recent history and assume it covers an older title. Title-scoped history is needed when the relevant event happened weeks ago.

Report or investigate the exact imported file. A new search alone does not remove or prove the incorrect file that is currently being played.

## The same problem keeps returning

Review recurring-problem notices for patterns across separate days and titles. A source configuration change may be more useful than repeating a repair. These notices provide advice and do not silently change external settings.

See [issues](/admin/issues/) and [supervised repairs](/admin/remediation/) for review and follow-up.
