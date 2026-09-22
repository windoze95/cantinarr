---
title: Issues and problem reports
description: Read the evidence, respond to people, and close or reopen a report with an accurate outcome.
sidebar:
  order: 5
---

An issue records a problem and its follow-up. It can begin with a user's report, a detected queue problem, or a system check. The issue stays tied to its exact instance and media scope.

## Read the issue before acting

Check the title, library, affected episodes or format, current state, and timeline. Two reports about the same title in different libraries can describe different files.

For a failed download, read the queue and import evidence. For wrong audio, a wrong episode, or a bad copy, inspect the imported file and the title's history. Those problems often arrive after the queue is empty.

## Observation and investigation

Cantinarr can observe an apparent problem before escalating it. Temporary tracker delays, ongoing arr activity, and a recovery that has not yet settled should not immediately become a destructive repair.

Quietly tracked or recovering issues can remain visible without adding to the actionable badge. A failed or incomplete upstream read is not treated as proof that the problem disappeared.

An administrator can review the evidence, reply, or use the configured agent workflow. See [supervised repairs](/admin/remediation/).

## Respond to the person

Ask for concrete missing information: episode number, playback app, error message, audio language, or the point where a file fails. Keep the conversation in the existing thread where possible.

Distinguish “the command was sent” from “the user can play the correct file.” After a repair, inspect the live result and ask for playback confirmation when only the viewer can establish it.

## Close with the right outcome

- **Mark resolved** means the problem has been addressed.
- **Close without fix** records the decision not to fix it.
- **Dismiss** is a separate disposition where available.

Completion notes are optional for both **Mark resolved** and **Close without fix**. Leaving the note blank still records an attributed default message. Do not enter filler just to close a report.

## Reopen a report

Administrators can reopen eligible resolved reports. This returns the issue to administrator attention and preserves the previous discussion and resolution history. If another action changed the issue first, refresh and review the latest state rather than resubmitting a stale decision.

## Repeated problems

Cantinarr can identify a recurring pattern and suggest which service setting to inspect. These notices are advice. They do not change configuration automatically.

A repeated path mapping failure or stalled torrent pattern is often better addressed at its source than by approving the same one-off repair each time. The notice identifies the measured pattern and relevant system.
