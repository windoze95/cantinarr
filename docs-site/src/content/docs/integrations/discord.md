---
title: Discord notifications
description: Get request and report updates in a Discord channel, with optional personal mentions.
sidebar:
  order: 7
---

Cantinarr can post request decisions, availability, failures, and problem-report updates to a Discord channel or thread. Each person can choose personal mentions. This uses a webhook, works without phone push, and does not send direct messages.

## Connect a channel

You need a Cantinarr administrator account and permission to create a webhook in the intended Discord channel.

1. In Discord, open **Edit Channel > Integrations > Webhooks**, create a webhook, and copy its URL.
2. In Cantinarr, open **Settings > Notifications > Discord Notifications > Server Discord Notifications**.
3. Paste the webhook URL. Under **Thread and appearance**, optionally set an existing thread ID, display name, avatar URL, or posters. Discord does not allow display names that contain "Discord" or "Clyde".
4. Use **Send test message** and check the channel or thread. The test uses the entered settings without saving or mentioning anyone.
5. Enable **Send notifications to Discord**, select categories under **Events**, and **Save**.

New installations start with requests awaiting approval selected. Existing installations retain their submission choices; additional categories and mentions start off. Turning a category off cancels its waiting posts. Enabling it does not replay old events.

## Get personal mentions

An administrator must enable **Allow Discord mentions** on the server page. Then each user can:

1. Open **Settings > Notifications > Discord Notifications**.
2. In Discord, enable **User Settings > Advanced > Developer Mode**, then copy the numeric user ID from their profile.
3. Paste that ID into **Discord user IDs**. Multiple IDs can be separated with commas, up to ten.
4. Enable **Mention me in Discord**, choose events, and **Save**.
5. Use **Test my mentions**, then check Discord and its channel notification settings.

Requesters can select approval, decline, availability, and updates to their reports. Admins can also select new requests, processing failures, and newly surfaced reports. Cantinarr excludes the acting account from personal mentions and checks current library access before mentioning a requester. Shared-book subscribers are included for their requested format.

Server-disabled categories explain why they cannot deliver. Personal choices are saved separately from phone push. The personal test uses saved IDs only and never mentions a role.

## Choose events and role mentions

Server event choices cover requests awaiting approval, automatic approval, manual approval, denial, availability, and processing that needs attention. Failures mean exhausted delivery retries or a failed book import; ordinary download waits do not generate failure posts.

User problem reports have separate created, reply, resolved, and reopened events. New-report notices follow Cantinarr's existing observation period. Messages contain a fixed summary and a report link, without copying thread replies or diagnostics. Closing a report without a fix does not announce resolution.

Under **Role mentions**, an administrator can enter a numeric Discord role ID and choose which events ping it. Role pings also require **Allow Discord mentions**. Other mentions, including automatic `@everyone` parsing, are disabled.

## Understand availability

Availability follows the requested library: a movie with a file, each requested book format, a complete album, or imported episodes within the requested TV selection. A pilot request announces the pilot. TV corrections retain the selected story's numbering.

Imports for the same title and library collect for 60 seconds before posting. Webhooks wake the checks; background polling covers missed webhooks and restarts. Library caching can add a short delay. File upgrades and repeated observations do not create another availability post.

When availability is enabled or its destination changes, existing files establish a baseline and are not announced. If a library is unavailable during that first check, the baseline waits for a successful read. Repairing a TV request does not announce episodes that are already in the library.

The server settings show a warning when a library cannot be read or a TV match needs attention in **Request Defaults > TV matches**. Older TV requests that did not choose specific seasons (from before Cantinarr 0.12), selections that include Specials, and paused TV matches are not checked for availability.

## Understand sharing and delivery history

Channel readers can see selected media titles, media types, requester usernames, library names, and event states. Personal mentions expose the IDs you enter. Choose a channel with the intended audience. A configured **External Address** adds links back to Cantinarr.

Treat the webhook URL as a credential. Leaving the field blank preserves the saved destination; **Remove webhook** clears and disables it. Do not include it in screenshots or support reports.

Recent deliveries distinguish waiting, delivered, failed, cancelled, grouped, and unconfirmed sends. Large mention audiences are divided into separately tracked posts. An unconfirmed send may already have reached Discord, so Cantinarr does not automatically resend it. Refresh keeps unsaved editor changes.

If a post or ping is missing, follow [notification troubleshooting](/troubleshooting/notifications/). Review requests and reports in Cantinarr using the message link.
