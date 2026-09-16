---
title: Tdarr transcoding progress
description: See active workers and library counts, with clear labels for idle, stale, and unavailable data.
sidebar:
  order: 9
---

The **Transcoding** module shows read-only Tdarr progress for administrators. It has **Activity** and **Libraries** tabs and an instance selector when you connect multiple Tdarr servers.

## Add Tdarr

Open **Settings > Add Instance > Tdarr**. Enter the server API URL, normally port **8266**. The WebUI's usual port, 8265, is not the API address.

If Tdarr authentication is enabled, use an API key from **Tools > API Keys**. Otherwise the key can be omitted. Test and save.

A blank field while editing preserves the saved key. Use **Remove saved API key** to explicitly test and save an unauthenticated connection.

## Activity

Workers are grouped by node. A worker can show the filename, expandable source path, transcode or health-check operation, CPU/GPU label, and Tdarr's reported current-step progress, FPS, or ETA.

Those numbers describe what Tdarr reports for the current work. Cantinarr does not invent an overall job percentage or total completion time when Tdarr does not supply one.

**Idle nodes** means nodes are connected without active work. **No connected nodes** is a different state and should be checked in Tdarr.

## Libraries

The tab starts with all-library totals. Select a library to read its status counts. Unknown counts say **Unavailable**, rather than displaying zero. If a selected library was removed, choose another explicitly.

## Refresh and stale data

Activity refreshes every 10 seconds and Libraries every 30 seconds while visible and foregrounded. The last successful timestamp stays visible. A later failure shows **Showing stale data** and offers Retry.

Switching instances clears the previous snapshot so another server's numbers are not mistaken for the new selection.

## What this module controls

It does not start, stop, pause, reprioritize, or configure Tdarr jobs. It does not map jobs to Cantinarr title availability. Use Tdarr itself for those operations.

Tdarr support may be newer than your stable server. Check [versions and channels](/install/updates/) if the service type is missing.
