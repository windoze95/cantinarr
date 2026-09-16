---
title: Kids accounts and ratings
description: Set movie and TV limits and understand where those limits do and do not apply.
sidebar:
  order: 3
---

A kids account adds a content policy to an ordinary user. Cantinarr enforces that policy on the server, including discovery, search, details, requests, relevant library responses, AI discovery tools, and title handoffs.

## Configure an account

Open the user in **Settings > Users** and configure the kids content policy. Review the rating region, maximum movie rating, maximum TV rating, and other policy choices presented by your version.

Choose the region deliberately. Rating names and their meaning depend on the classification system. Test a known allowed title and a known blocked title using the child's account.

Administrators cannot also be kids accounts. Administrative access and content restrictions are incompatible roles.

## Missing and unreadable ratings

A rating that cannot be read is not evidence that a title is suitable. Cantinarr fails closed when it cannot reliably apply the policy, and reports the failed check instead of showing a shorter list as though it were complete.

An unrecognized or absent rating is handled under the configured policy for unrated content. If a title looks incorrectly hidden, check its rating data and region before raising the account's overall limit.

## Books and music need explicit access

Book and music catalogs do not supply the movie and TV ratings used by this policy. Granting access to a Chaptarr or Lidarr instance is therefore a separate, deliberate decision for a kids account.

Do not describe that grant as age-filtered access. Review the contents of the library you are granting.

## Playback still has its own rules

Cantinarr checks its authorized title links, but it does not replace Plex, Jellyfin, Emby, Audiobookshelf, or Infuse's own account and library restrictions. Configure those services for the child as well.

Kids accounts cannot list or control paired Apple TVs. An older TV grant does not override that restriction.

## Verify from the child's account

Check search, discovery rows, direct title links, requests, and the playback account. Testing only the administrator's screen does not exercise the policy.

If rating reads fail, fix the metadata connection. Turning off the policy to hide an upstream error changes access for the whole account.
