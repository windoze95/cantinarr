---
title: Monitoring with Tautulli or Tracearr
description: Connect live streams, watch history, and statistics without confusing them with download activity.
sidebar:
  order: 8
---

Monitoring shows playback activity. Downloads shows file transfers. Transcoding shows Tdarr jobs. These are different systems and can all be quiet or active independently.

## Choose the service

- **Tautulli** monitors Plex.
- **Tracearr** can monitor Plex, Jellyfin, and Emby.

Set up the monitoring service and its media-server connection first. Cantinarr reads what that service can report; it does not substitute for its initial setup or historical data collection.

## Add it to Cantinarr

Open **Settings > Add Instance**, select Tautulli or Tracearr, and enter its reachable base URL and API key. Tracearr's public API key is available in its **Settings > General**.

Test and save, then open **Monitoring**. With more than one monitoring instance, choose the one you intend to inspect.

## Read the result

Compare a known active playback session with the monitoring service's own view. Check whether playback is direct or transcoded and which server reports it.

Watch history and statistics depend on the monitoring service's retained data. An empty time range does not prove a person has never watched the title, and an unavailable service is not an empty activity list.

## If numbers differ

Check the selected instance, time range, server, and provider response. A live session and a historical completed session can be recorded at different times.

Monitoring is an administrator surface. Granting a playback account does not grant a user access to everyone else's viewing statistics.
