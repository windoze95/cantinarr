---
title: Audiobookshelf listening
description: Connect audiobook access and listening apps while keeping book requests tied to Chaptarr.
sidebar:
  order: 4
---

Chaptarr requests and organizes books. Audiobookshelf serves the audiobook library to listeners. Connect both if you want Cantinarr to request an audiobook and later offer an authorized listening link.

## Add Audiobookshelf

Open **Settings > Add Instance**, select Audiobookshelf, and enter its reachable connection details. Run the live test, then choose the default libraries and intended user grants.

Set **Address users open** to the browser or app address the listeners can reach. An internal Docker hostname is usually unsuitable for a phone outside that network.

## Review library access

Use the library controls shown for the instance and user. Audiobookshelf's actual library, tag, and content restrictions remain relevant. A Cantinarr account link alone does not prove a listener can access every item.

Use the media-server access guide to complete account setup or link an existing account. Verify access using that person's actual Audiobookshelf account.

## Connect the book request side

Grant the person access to the appropriate Chaptarr library too. Books require their own per-user grant. When an audiobook becomes available, Cantinarr resolves the authorized Chaptarr audio identity and checks the live Audiobookshelf access before offering the title handoff.

A book's ebook availability does not prove its audiobook is ready. Different format records and similarly named books are not merged by title.

## Choose listening apps

Administrators can set defaults in the Audiobookshelf instance. Users can override them under **Settings > Account > Listening apps**.

- iPhone/iPad: Browser, Audiobookshelf, or ShelfPlayer.
- Android: Browser, Audiobookshelf, or TheShelf.
- Web and desktop: browser.

Install and sign into the chosen app first. A failed native launch falls back to the original browser link.

## If Listen is missing

Check the Chaptarr audiobook file, the user's Chaptarr grant, the Audiobookshelf account and library restrictions, the exact item match, and the address users open. A successful admin API connection does not prove a listener can read that item.

See [playback troubleshooting](/troubleshooting/playback/) and the [complete book setup](/integrations/guides/books/).
