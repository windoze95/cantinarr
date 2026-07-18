# Cantinarr manual test checklist

This folder is the manual-only layer of Cantinarr testing: the behaviors that
the hermetic suites genuinely cannot prove. A case earns its place here only
when proving it needs a human and/or a real environment — live third-party
truth (plex.tv, TMDB/Trakt, AI providers, APNs, GitHub), physical devices and
native ceremonies, store and release operations, destructive/chaos operations
no suite can stage, accessibility/compatibility/performance audits, timed
exploratory sessions, and a small set of end-to-end release-day journeys.
Cases are grouped by product area, shared setup is listed in
[fixtures](fixtures.md), environment and credential needs per layer are in
[environments](environments.md), and the [run template](run-template.md) can
be copied when recording a test run.

Everything else — contracts, authorization, validation vectors, state
matrices, availability computation, event mapping, tool contracts — is
machine-proven by the ordinary test suites that gate every merge: `go test`
under `server/`, `flutter test` under `app/`, and the private-lab Maestro
smoke suite. See [automation](automation.md) for the layer boundaries and the
private-lab runner. The checklist stays a lightweight human document: tags
such as **AUTO** are informational hints, not machine-enforced claims.

## Test areas

| Area | Case prefixes | Cases |
|---|---|---:|
| [Build, operations, usability, and release](catalog/baseline-operations-release.md) | OPS, UX, PERF, REL, EXP | 28 |
| [Authentication, navigation, and security](catalog/auth-users-security.md) | AUTH, NAV, SEC | 11 |
| [Instances, realtime behavior, and push](catalog/instances-realtime-push.md) | INST, RT, PUSH | 8 |
| [Plex linking, libraries, and invitations](catalog/plex.md) | PLEX | 18 |
| [Discovery and requests](catalog/discovery-requests.md) | DISC, REQ | 6 |
| [Media services and download clients](catalog/media-services.md) | RAD, SON, BOOK, DOWN, TAUT | 10 |
| [Issues, AI, and MCP](catalog/issues-ai-mcp.md) | ISS, AI, MCP | 8 |
| **Total** | | **89** |

Case IDs are stable and never renumbered, so deleted cases leave gaps in the
sequences; a gap means the behavior moved into the automated suites, not that
a case is missing.

## Running the checklist

1. Prepare the accounts, services, and data needed for the selected cases using [fixtures](fixtures.md).
2. Choose the applicable product areas and case IDs.
3. Copy the [run template](run-template.md) and record results, notes, and evidence there. Leave the committed checklist unchecked so it stays reusable.
4. For third-party integrations, verify both Cantinarr's result and the resulting state in Plex, the arr service, download client, push device, or AI provider.
5. When a case has a vector table, run every applicable row before marking the parent case complete.

Run `make check-test-automation` after adding or changing catalog cases,
Maestro flows, or suites; it validates the catalog line format, the area
counts above, and Maestro flow safety. Run the current private-lab UI
smoke suite with `make maestro-lab-smoke`; it never targets a public server or
a household setup. The local runner retains lab-password-scrubbed Maestro JUnit
XML under the ignored private artifacts tree and deletes native debug, console,
and screenshot output after normal, failed, and catchable interrupted runs. A
host crash or `SIGKILL` can prevent cleanup, so never publish raw UI artifacts.
Record reviewed live results with the [run template](run-template.md).

Use `PASS` when the expected behavior is observed, `FAIL` when it is not, `BLOCKED` when the test cannot be completed, and `N/A` when the case does not apply to the tested configuration.

Priorities describe impact:

- **P0**: critical behavior or a release blocker for the affected area.
- **P1**: serious behavior that should be covered for changes in the affected area.
- **P2**: extended, compatibility, or exploratory coverage.

Tags describe the proof surface: **AUTO** machine-driven repository or CI proof, **API** direct contract check, **UI** app behavior, **LIVE** real third-party service/device, **CHAOS** controlled failure/recovery, and **SEC** authorization/privacy.
