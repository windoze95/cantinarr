---
title: Notifications that do not arrive
description: Check the server, account, device, and event independently.
sidebar:
  order: 7
---

## Start with a test notification

Use the appropriate Cantinarr self-test or administrator test, then inspect the actual phone. If a test works but a content alert does not, the event or category is the next place to investigate.

## Check the four delivery gates

1. **Gateway:** the server has a configured push gateway and can reach it.
2. **Server policy:** push and the relevant category are allowed.
3. **User preference:** this person has enabled receiving push and that category.
4. **Device:** the app is signed in, registered, and allowed notifications by the operating system.

Master switches apply to tests too. A muted test is not evidence of a broken relay. Read the setting's explanation when a category is unavailable.

## A title became available without an alert

Check the relevant instance's **Instant updates**. Small ebook downloads can finish between polls, making a working webhook especially useful.

Confirm the title and format changed in the intended library and the event category is enabled for that recipient. An ebook import does not prove the audiobook is ready.

## An old device receives alerts

Review **Connected Devices** and revoke the old device if it should no longer have access. A phone reinstall can require fresh sign-in and notification permission.

## A self-hosted relay cannot be reached

Gateway traffic uses the external transport even when the gateway is on the LAN. A saved in-app outbound proxy has no bypass list. Use the environment-variable proxy path with a suitable `NO_PROXY` entry for that deployment.

## Discord posts or mentions are missing

In **Discord Notifications > Server Discord Notifications**, use **Send test message**, then check the target channel or thread. This test does not save, enable, or mention anyone.

Check **Send notifications to Discord** and the relevant category under **Events**. For a personal ping, also enable **Allow Discord mentions**, then save **Mention me in Discord**, your numeric user IDs, and event choices on the personal page. Use **Test my mentions** and check Discord server/channel notification settings.

Availability requires a successful library read and the requested content in the selected library. TV episodes are grouped over 60 seconds; albums must be complete. Existing files establish a baseline when availability is enabled and are not announced. Read recent deliveries and any warning about a library that cannot be read or a TV match that needs attention in **Request Defaults > TV matches**. An **unconfirmed** send is not resent automatically because it may already have posted.

Remove webhook URLs and device tokens from any screenshots or support reports. See [Discord setup](/integrations/discord/) and [push setup](/integrations/push/).
