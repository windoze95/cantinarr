---
title: Discord request alerts
description: Send new-request notices to one channel and understand delivery results.
sidebar:
  order: 7
---

Open **Settings > Notifications > Discord Notifications** as an administrator. The integration is optional and uses a Discord channel webhook.

## Set it up

1. In the intended Discord text channel, create a webhook using an account with the required channel permissions.
2. Copy its webhook URL into Cantinarr's write-only field.
3. Use **Send test message** and check the actual channel.
4. Enable the integration and save.

The test can use an entered or already saved destination without saving or enabling it. A successful test and a saved active configuration are separate steps.

## Choose which requests are sent

By default, the integration concerns new requests that need approval. **Include automatically approved requests** is a separate option and starts off.

Turning that option off cancels only the automatically approved notices still waiting to send. It does not remove messages already posted to Discord.

## Understand what is shared

Channel readers can see media titles, requester usernames, media types, and approval state. Choose a channel whose audience should receive that information.

Treat the webhook URL as a credential. Do not post it in screenshots, bug reports, or these docs. Leaving its field blank preserves a saved destination; **Remove** clears and disables it.

## Read delivery history

Recent deliveries distinguish waiting, delivered, failed, cancelled, and unconfirmed sends. An unconfirmed send may already have reached Discord, so Cantinarr does not automatically resend it and risk a duplicate.

Refresh the history and inspect the channel before taking further action. Refresh keeps unsaved editor changes. Leaving the screen with an edited draft offers the normal keep-or-discard choice.

Discord alerts do not replace in-app approvals. Review and decide the request in Cantinarr.
