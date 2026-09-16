---
title: Get help or report a bug
description: Gather a small, clear report that someone else can act on.
sidebar:
  order: 9
---

For a problem with one title in your household, start with its [problem report](/use/report-problem/). Your server administrator can inspect the connected services and files.

For a Cantinarr bug, use [GitHub Issues](https://github.com/windoze95/cantinarr/issues). For setup discussion, join the [Cantinarr Discord](https://discord.gg/zAgRwGwmVB). Feature suggestions belong on the [feature board](https://cantinarr.com/roadmap/).

## Include these details

```text
What I was trying to do:
What I expected:
What actually happened:

Steps to reproduce:
1.
2.
3.

Cantinarr server version:
App version and platform:
Image channel or installation method:
Affected service and version:
Time of the problem, including time zone:
Exact error message:
```

Add the selected library and relevant title, episode, or format when needed. A screenshot can help explain layout or the exact visible state. A short, scrubbed log excerpt around the same time is usually more useful than a full log archive.

## Keep credentials out

Do not include API keys, OAuth or session tokens, connect links, passwords, Discord webhook URLs, your encryption key, the database, or the complete config folder.

Check browser address bars and query strings in screenshots. A URL can contain a private ticket even when it looks like an ordinary page link.

## Say what was actually verified

Useful distinctions include:

- “Test Connection passed, but Radarr's callback failed.”
- “The request appears in Sonarr, but all releases are rejected there.”
- “The command was accepted, but the Apple TV still shows its home screen.”
- “The build uploaded, but TestFlight does not offer it on the device yet.”

These observations point to different systems. They save more time than saying the whole setup is broken.

## A documentation problem

Every page has an **Edit page** link. If a step is wrong or unclear, include the page link and the point where the instructions stopped matching your screen. There is no need to know the underlying code to report an unclear explanation.
