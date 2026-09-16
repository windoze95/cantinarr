---
title: Requests that do not progress
description: Find out whether a saved request needs approval, service recovery, a release, or administrator attention.
sidebar:
  order: 4
---

## First, open the saved request

Read the status and explanation. Record its target library, exact title, and requested seasons or formats. Do not start with the download queue if the request has not reached the library manager yet.

## Waiting for approval

An administrator needs to approve or deny it. Check the approval queue and applicable per-user policy. A push notification is only a reminder; missing push delivery does not remove the request from that queue.

## Requested with a delivery warning

The request is saved but delivery to its original library is waiting. Check the instance connection, API credentials, upstream availability, and identity error described in the message.

Temporary failures retry automatically and survive restarts. For book and music delivery, retries back off from one minute up to six hours, honor longer upstream retry guidance, and can eventually stop for attention after repeated failures.

Fix the cause before using Retry. Resubmitting the same title or changing the global default does not move this saved request to a different library.

## Requested, already present in the library manager

Now inspect the library manager:

1. Is this the exact intended title and, for TV, the right seasons?
2. Is the requested scope monitored?
3. Has the content been released or aired?
4. Do configured indexers return a suitable release?
5. Do the selected quality, language, and custom-format rules accept it?
6. Is the download client available and able to use the target storage?

The service's own search rejection reasons are more useful here than repeatedly retrying the Cantinarr request.

## Needs attention

Read the durable failure explanation. A removed instance, invalid identity, missing configuration, or exhausted automatic retry path needs intervention.

An administrator can inspect saved requests even when their original instance was removed. Retrying still requires that original target and current access. Creating a new instance with the same display name does not make it the same recorded instance.

## Only one book format progressed

Check ebook and audiobook status independently. One can be available while the other waits. Inspect the exact Chaptarr format record, approval state, and delivery explanation for the missing format.

## The wrong show or season was added

Use [TV match corrections](/admin/tv-matches/) and review affected-request previews. A correction does not automatically delete old files or rewrite monitoring. Make those decisions from the actual library state.

## A transfer exists, but the file never becomes available

Continue with [download and import troubleshooting](/troubleshooting/downloads/). That is a later step than request intake and delivery.
