---
title: Find the problem
description: Start with the symptom you can see, then check the part of the setup responsible for it.
sidebar:
  order: 0
---

Before changing settings, note what works and what does not. A working website, a passing instance test, an accepted request, an imported file, and successful playback each prove a different step.

| What you see | Start here |
| --- | --- |
| The app cannot open the server | [Connections and addresses](/troubleshooting/connections/) |
| An instance works in my browser but fails its test | [Connections and addresses](/troubleshooting/connections/) |
| Sign-in loops, a link expired, or SSO fails | [Sign-in and access](/troubleshooting/sign-in/) |
| Books, Music, a title, or a setting is missing | [Missing tabs and content](/troubleshooting/missing-content/) |
| A request stays Requested or Needs attention | [Requests that do not progress](/troubleshooting/requests/) |
| A transfer finishes but the title stays unavailable | [Downloads and imports](/troubleshooting/downloads/) |
| Wrong episode, wrong audio, or a bad copy | [Downloads and imports](/troubleshooting/downloads/#the-wrong-content-is-already-imported) |
| A title is available but will not open or download | [Playback and file downloads](/troubleshooting/playback/) |
| A notification does not reach a phone or Discord | [Notifications](/troubleshooting/notifications/) |
| The assistant cannot connect or a model fails | [AI and MCP](/troubleshooting/ai/) |
| Tdarr shows stale data or no workers | [Tdarr progress](/integrations/tdarr/) |
| Apple TV discovery or title handoff fails | [Apple TV setup](/integrations/guides/apple-tv/#server-requirements-and-troubleshooting) |

## Read errors literally

**No results** means the specific search found nothing. **Could not check** means it could not establish the answer. **Showing stale data** means the last successful result is still displayed after a later failure.

Do not turn an unavailable check into a conclusion that the title, account, file, or download does not exist.

## Gather a small useful example

Record the app and server versions from **Settings > About**, the affected service and instance, one exact title or request, the time, and the error text. Then follow the relevant guide.

For a Docker server, recent logs can help:

```sh
docker compose logs --since=15m --tail=200 cantinarr
```

Review logs before sharing them. Remove tokens, private URLs, usernames where unnecessary, and personal media details. Do not post your whole config folder or database.

If the guide does not resolve it, [prepare a useful support report](/troubleshooting/get-help/).
