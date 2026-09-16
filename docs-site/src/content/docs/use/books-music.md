---
title: Request books and music
description: Choose book formats and album releases without losing track of the library you are using.
sidebar:
  order: 4
---

Books and Music are enabled for each person through access to a Chaptarr or Lidarr instance. If a tab is missing, an administrator must check that grant. A global movie or TV default does not grant book or music access.

## Ebooks and audiobooks

Search from the Books tab and open the exact result you want. Choose the ebook, audiobook, or **Request both**, when available. Each format has its own live availability and delivery progress.

The book's native catalog identity and chosen library remain attached to the request. A provider outage does not require you to find and submit the title again. The saved request can continue retrying after the server restarts.

An ebook and audiobook may be distinct records in the library. Similar titles and author names do not prove they are interchangeable. Report the specific record and format when something is wrong.

### Read or listen

If the administrator enabled file downloads, an available file can be saved to your device. If Audiobookshelf is connected and your account has access, an audiobook may offer **Listen**. These features require separate setup from requesting the book.

Choose your supported listening app in **Settings > Account > Listening apps**. Install the app and sign into the intended Audiobookshelf server first.

### Old book links

Open Library discovery and request matching are retired. An old link may offer **Search books** instead. An unresolved saved request retains its history and approval rules; it is not silently matched to a different native book.

## Albums, EPs, and singles

Search from Music. Album results can appear before artist results finish loading. Open an artist to browse their discography, or open a release directly.

Requesting an album adds or updates the artist in Lidarr with only the selected album monitored. It does not request the artist's entire catalog. Library matching uses MusicBrainz identities, including canonical release-group identity for new requests.

You do not need your own MusicBrainz or ListenBrainz account to use the music catalog.

### Why a release can stay requested

The request may be waiting for approval, delivery to Lidarr, or a suitable release. Read the saved request's explanation. **Needs attention** can indicate a configuration or identity problem that ordinary retrying cannot solve.

If file downloads are enabled, music is offered per track. Cantinarr does not package an album into a new archive.

## For the administrator

Follow the complete [Chaptarr setup](/integrations/guides/books/), [Lidarr setup](/integrations/guides/music/), and [Audiobookshelf guide](/integrations/audiobookshelf/). Keep each format's status separate when diagnosing a problem.
