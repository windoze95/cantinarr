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

## Discord tests or requests are missing

Use **Send test message**, then look at the target channel. A test does not save or enable the integration by itself.

For real requests, check whether they needed approval and whether **Include automatically approved requests** was enabled. Read recent delivery states. An **unconfirmed** send is not automatically resent because it may already have posted.

Remove webhook URLs and device tokens from any screenshots or support reports. See [Discord setup](/integrations/discord/) and [push setup](/integrations/push/).
