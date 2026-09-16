---
title: Discover and search
description: Find movies, shows, books, and music, and understand which library a result belongs to.
sidebar:
  order: 1
---

Open **Discover** to browse the catalogs available to your account. Movies and TV use TMDB and, when configured, Trakt. Books use your selected Chaptarr library and optional Hardcover trending. Music uses MusicBrainz and ListenBrainz, with library status from Lidarr.

## Use the right search context

The search field follows the active discovery tab:

| Where you are | What search looks for |
| --- | --- |
| Movies, TV Shows, or Releases | Movies, shows, and people |
| Books | Books and authors through the selected Chaptarr instance |
| Music | Albums, EPs, singles, and artists |

Watch the placeholder or the icon beside your query. A query on Books does not also search the movie catalog. Secondary administration screens can have their own local filters instead of the global search.

Results and library status can arrive separately. If the catalog loaded but the library could not be checked, read the notice before assuming a title is missing. **Checking…** is not an availability result.

## Explore movies and shows

Open **See all** on a discovery row for a full grid. **Browse by genre** opens a filtered view. Movie and TV browsing can narrow by genre, release year, rating, original language, streaming service and region, keywords, or studio.

The region matters for release and streaming information. A title available from a service in one country may not be offered in another.

An administrator can choose the source of headline rows and restrict discovery recommendations to English-language originals. That language preference does not restrict ordinary title search. Kids policies still apply across search and discovery.

## Explore books

Book search uses the selected Chaptarr instance's native records. Check the author, title, edition information, and ebook or audiobook availability. Different records can have the same title; Cantinarr keeps those records separate.

**Trending Books** uses the Hardcover connection selected for that Chaptarr instance. Recently Added, Authors, and Series come from the connected library. A missing Hardcover connection does not mean your native book library stopped working.

Older Open Library discovery links are retired. Use **Search books** to find the title in the current catalog. See [book requests](/use/books-music/).

## Explore music

Search returns albums, EPs, and singles, with artists loading independently. Opening an artist shows their discography. **Popular Albums**, **New Releases**, and genre feeds focus on albums and EPs.

Music matches use MusicBrainz identities, not the album title alone. Two releases with similar artwork or names may be distinct records. Check the artist and release before requesting.

## Missing a tab

Books and Music need a per-user library grant for requesters. Administrators can browse some catalogs before connecting a service, and can hide unconfigured tabs for the server.

Check [missing tabs and titles](/troubleshooting/missing-content/) before reinstalling the app. An empty list, a hidden module, and a failed provider connection each need a different fix.
