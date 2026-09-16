---
title: What Cantinarr does
description: How Cantinarr fits into a media setup, what it needs, and what your household will use it for.
sidebar:
  order: 1
---

Cantinarr gives your household one place to discover movies, shows, books, and music, request something new, and see when it is ready. It also gives the person running the server a place to manage requests, connected services, and problems.

You run the Cantinarr server yourself. The web app comes with it. The iPhone and Android apps connect to that same server.

## The parts of your setup

| Part | What it does | Examples |
| --- | --- | --- |
| Cantinarr | Discovery, requests, permissions, notifications, and administration | The app you and your household open |
| Library manager | Finds releases and organizes imported files | Radarr for movies, Sonarr for TV, Chaptarr for books, Lidarr for music |
| Download client | Transfers files chosen by a library manager | SABnzbd, qBittorrent, NZBGet, Transmission, Deluge, ruTorrent |
| Media server | Makes your existing library available to watch or listen to | Plex, Jellyfin, Emby, Audiobookshelf |
| Catalog | Provides title information, artwork, or discovery lists | TMDB, Trakt, Hardcover, MusicBrainz, ListenBrainz |

You do not need every service. Connect the ones that match what you use. A movie setup might be Cantinarr, Radarr, a download client, and Plex. A book setup can use Chaptarr and Audiobookshelf.

## What happens to a request

1. Someone finds a title in Cantinarr and requests it.
2. Cantinarr saves the request. If approval is required, an administrator reviews it.
3. Cantinarr sends the approved request to the right library manager.
4. That service searches its configured sources and sends a release to its download client.
5. The library manager imports the completed files. Cantinarr updates the title's availability.
6. Your media server scans the library. The person opens it in their playback or listening app.

Each part has its own job. A request can be accepted before a release exists. A completed download can still need importing. A file in Radarr can still be waiting for Plex to scan it. The [status guide](/use/status/) explains these differences.

## What you need to provide

To browse, you only need Cantinarr. Movie and TV discovery includes a built-in TMDB key.

To acquire media, you need a working library manager, its sources, its storage, and its download client. Cantinarr connects to those services; it does not install or configure an entire media stack for you.

To watch or listen, you need a playback app and access to the relevant media server. Request permission and playback permission are separate.

AI, external sign-in, push notifications, Discord, monitoring, and Apple TV control are optional. You can use ordinary discovery and requesting without an AI account.

## Who sees what

- **Administrators** configure the server, connect services, manage people, review approvals, and resolve problems.
- **Users** see the discovery, request, and library access their administrator allows.
- **Kids accounts** also have server-enforced movie and TV restrictions. Books and music need an explicit library grant because those catalogs do not supply age ratings.

A missing tab often means a permission or setup requirement is not met. It does not automatically mean the app failed to load.

## Pick your next step

- Running the server: [set up Cantinarr](/start/quickstart/).
- Joining someone else's server: [the household guide](/start/for-households/).
- Working on an existing installation: [the troubleshooting directory](/troubleshooting/).
- Looking up an unfamiliar word: [the glossary](/reference/glossary/).

These docs follow the current main branch. Check **Settings > About** for your server and app versions if a control described here is missing. See [updates and release channels](/install/updates/).
