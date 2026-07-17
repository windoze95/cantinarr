# Cantinarr master test checklist

This folder is the master list of end-to-end Cantinarr behavior to verify. Cases are grouped by product area, shared setup is listed in [fixtures](fixtures.md), and the [run template](run-template.md) can be copied when recording a test run.

Machine-driven coverage lives in the ordinary test suites — `go test` under
`server/`, `flutter test` under `app/`, and the private-lab Maestro smoke
suite — not in a per-case coverage ledger. See [automation](automation.md)
for the layer boundaries and the private-lab runner. The checklist stays a
lightweight human document: tags such as **AUTO** are informational hints,
not machine-enforced claims.

## Test areas

| Area | Case prefixes | Cases |
|---|---|---:|
| [Build, operations, usability, and release](catalog/baseline-operations-release.md) | BASE, OPS, UX, PERF, REL, EXP | 69 |
| [Authentication, navigation, users, and security](catalog/auth-users-security.md) | AUTH, NAV, USER, SEC | 88 |
| [Instances, realtime behavior, and push](catalog/instances-realtime-push.md) | INST, RT, PUSH | 65 |
| [Plex linking, libraries, and invitations](catalog/plex.md) | PLEX | 87 |
| [Discovery and requests](catalog/discovery-requests.md) | DISC, REQ | 69 |
| [Media services and download clients](catalog/media-services.md) | RAD, SON, BOOK, DOWN, TAUT | 91 |
| [Issues, AI, and MCP](catalog/issues-ai-mcp.md) | ISS, AI, MCP | 115 |
| **Total** | | **584** |

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

Tags describe the proof surface: **AUTO** machine-driven repository or CI proof, **API** direct contract check, **UI** app behavior, **LIVE** real third-party service/device, **CHAOS** controlled failure/recovery, **SEC** authorization/privacy, **RT** realtime convergence, and **GAP** expected behavior that is known not to be implemented yet.
