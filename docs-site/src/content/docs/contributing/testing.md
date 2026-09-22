---
title: Test the right thing
description: Separate source-level checks from live service, device, and release proof.
sidebar:
  order: 2
---

Automated tests prove repeatable application behavior. Live checks prove that a real service, device, or store did the expected thing. A useful result says which kind of proof it provides.

## Automated checks

Server changes require `go vet ./...` and `go test ./...` from `server/`. CI runs Go tests with the race detector and builds a static server binary.

App changes require the pinned Flutter SDK, locked dependency installation, analysis, and tests. CI also builds the release web app.

Release tooling, the Apple TV helper, and the pinned Codex protocol integration have their own checks in CI. Documentation builds validate links, source references, settings coverage, and the site output.

## Manual cases

The maintained [manual test catalog](https://github.com/windoze95/cantinarr/tree/main/docs/testing) covers the things suites cannot fully establish:

- Real third-party account and library access.
- Device sign-in, push receipt, playback, and television handoffs.
- Store availability, release promotion, and upgrade behavior.
- Controlled failures, accessibility checks, and exploratory sessions.

Use its [fixtures](https://github.com/windoze95/cantinarr/blob/main/docs/testing/fixtures.md), [environment requirements](https://github.com/windoze95/cantinarr/blob/main/docs/testing/environments.md), and [run template](https://github.com/windoze95/cantinarr/blob/main/docs/testing/run-template.md). Do not create production requests or destructive jobs just to check a configuration.

## Record the actual outcome

- A passed connection test proves that call succeeded from the server.
- An accepted invitation is separate from sending it.
- A playback command being sent is separate from seeing the right screen.
- A successful store upload is separate from tester or production availability.
- A green PR build is separate from the exact merged checkout's CI evidence.

Use **PASS**, **FAIL**, **BLOCKED**, or **N/A**, with the observed evidence and versions. Do not mark a case passed because a neighboring layer worked.

## Disposable integration testing

Use the project's disposable lab and public-domain fixtures for repeatable integrations. The private Maestro runner has its own boundaries and artifact handling. Follow the [automation guide](https://github.com/windoze95/cantinarr/blob/main/docs/testing/automation.md).

Run `make check-test-automation` when changing the manual catalog, automation traceability, or lab flows. Keep raw credentials and private UI artifacts out of public test reports.
