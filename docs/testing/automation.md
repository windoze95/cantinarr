# Test automation

Cantinarr has 584 parent cases, not 584 equivalent UI scripts. The explicit
Plex vectors plus required download-client and MCP mode matrices raise the
conservative minimum to 802 executions before prose variants are expanded.
Automation therefore uses the cheapest layer that can prove each behavior,
while the catalog ID remains the stable traceability key.

## Coverage ledgers

[coverage-plan.json](coverage-plan.json) classifies every one of the 584 parent
cases by a dominant owner, disposition, and any additional recommended proof
layers. It is a planning and ownership ledger only: classification does not
mean that a test exists, ran, passed, or completed the case.

| Planning classification | Cases |
|---|---:|
| Go/API dominant | 181 |
| Flutter/widget dominant | 166 |
| Maestro/web dominant | 13 |
| Patrol/native dominant | 15 |
| Manual/external dominant | 209 |
| **Total** | **584** |

The same plan classifies 364 cases as automatable, 210 as hybrid, 6 as manual,
and 4 as blocked by known product gaps. Hybrid and manual cases retain their
external-service, physical-device, privacy, accessibility, performance, or
human-review obligations even when deterministic automation covers part of
the behavior.

[automation.json](automation.json) is the separate execution-proof manifest.
Its current 33 mappings are deliberately conservative:

| Status | Parent cases | Current sources |
|---|---:|---|
| Automated | 18 | 12 Go cases, 5 CI baseline cases, 1 Maestro case |
| Partial | 15 | 3 Go cases, 1 Flutter widget case, 3 Maestro cases, 7 CI/release cases, 1 store-listing capture-tool case |

An `automated` mapping means the listed evidence covers every catalog clause
and applicable vector for that parent. A `partial` mapping names exactly what
exists and what remains. Cases without a manifest mapping have ownership but
no claimed proof. No Patrol suite is implemented yet.

## Layer ownership

| Layer | Primary responsibility |
|---|---|
| Go unit/API/integration | Contracts, authorization matrices, sanitization, concurrency, idempotency, cache behavior, protocol vectors, and most controlled failures |
| Flutter unit/widget | State matrices, validation, labels, loading/error/empty states, layouts, and provider behavior |
| Maestro | A thin set of black-box web journeys across authentication and role-specific navigation against the disposable lab; add other surfaces only when the web driver can prove them reliably |
| Patrol | Native-only boundaries that benefit from Dart assertions: passkeys, notification permissions/taps, deep links, external browser/WebView handoff, app lifecycle, and network controls |
| Manual/external | Physical-device delivery, real Plex share truth, store submission, cross-browser/accessibility audits, low-end performance, and exploratory sessions |

Maestro is the first black-box browser lane because it can drive the actual
same-origin web app served by the lab without changing application dependencies.
Patrol should be introduced with the first native boundary case, not as a
second copy of the Maestro suite. A UI success message is never sufficient for
a case that also requires API, upstream, filesystem, or realtime proof.

## Current Maestro smoke suite

The web suite contains three isolated flows. It completes `AUTH-007` and
records honest partial coverage for `AUTH-022`, `NAV-001`, and `NAV-003` using
the lab's second admin and no-grants requester. Search is not claimed: Maestro
web can focus Flutter's fixed search field but does not reliably inject text
into its hidden editing element, so that attempted pilot was removed.

The suite runs locally, not on Maestro Cloud. The DigitalOcean firewall remains
SSH-only from the operator's exact address, and the lab-owned wrapper opens one
temporary `127.0.0.1` tunnel. Each suite chooses a fresh loopback origin so
Chromium cannot mask a newly deployed candidate with an older Flutter cache.
Generated passwords are read from the lab's ignored mode-0600 environment file,
passed only in `MAESTRO_*` process environment, exact-byte scrubbed from local
runner output, and never placed in command arguments or committed files. That
scrub cannot remove pixels, encoded values, session tokens, or unrelated
secrets, and a host crash or `SIGKILL` can prevent post-run cleanup.

Lab candidates set the Docker build argument
`CANTINARR_E2E_WEB_SEMANTICS=true`. Production images default it to `false`;
the build-time seam provides deterministic lab selectors without changing
ordinary browser accessibility behavior.

Prerequisites:

- the private `cantinarr-lab` checkout next to `src/`, with a provisioned lab;
- Java 17 or 21;
- Maestro CLI exactly 2.6.1, the version proved by this harness; and
- the normal lab tools and SSH identity described by the lab operator guide.

The official installer supports an exact version:

```sh
brew install openjdk@21
export MAESTRO_VERSION=2.6.1
curl -Ls https://get.maestro.mobile.dev | bash
```

Run the already-deployed candidate:

```sh
make maestro-lab-smoke
```

Build and deploy the current Cantinarr checkout first, then test it:

```sh
E2E_ARGS=--deploy make maestro-lab-smoke
```

Start from a fully reset lab state when a stateful suite requires it:

```sh
E2E_ARGS="--deploy --reset" make maestro-lab-smoke
```

`--reset` is intentionally opt-in because it deletes and reseeds every lab
Docker volume. It does not destroy or broaden the Droplet. `make destroy` in
the private lab repo remains the billable-resource teardown.

The runner reports the ordinary Maestro exit result and keeps
lab-password-scrubbed JUnit XML under the ignored private
`e2e/maestro/.artifacts/suites/` tree. Native debug, console, and screenshot
artifacts are deleted after normal, failed, and catchable interrupted runs;
they are not an evidence channel. A host crash or `SIGKILL` can prevent that
cleanup. There is no bespoke Markdown or screenshot-report pipeline. Use
[run-template.md](run-template.md) when a human run record is needed.

## Exact proof schema

The manifest uses schema version 2 and one object per catalog case:

```json
{
  "case_id": "RAD-009",
  "status": "partial",
  "scope": "Current widget proof; gesture vectors remain.",
  "evidence": [
    {
      "kind": "flutter-test",
      "path": "app/test/radarr/movie_menu_delete_test.dart",
      "selector": "menu shows a confirmation with delete-files unchecked"
    }
  ]
}
```

Evidence kinds are `go-test`, `flutter-test`, `maestro-flow`,
`workflow-step`, `script-check`, and, once introduced, `patrol-test`. Paths are
repository-relative and selectors identify an exact test function, test name,
flow name, workflow step, or check marker. Each evidence source also carries
its catalog ID in a nearby test name or comment so renames and stale mappings
fail validation instead of silently drifting.

`e2e/maestro/suites.json` maps each executable flow to one of the five fixed
lab identities. `scripts/check_test_automation.py` validates the 584-case plan,
catalog and manifest parity, exact evidence selectors, proof-layer ownership,
Maestro flow safety and suite membership, and both directions of the `AUTO`
rule: every completed proof carries `AUTO`, and every `AUTO` case has a
completed manifest proof.

When converting a case:

1. Split its assertions by proof surface; do not force API or chaos assertions through UI taps.
2. Add lower-level proof first where applicable, then one black-box journey only if visible integration behavior remains.
3. Put the catalog ID in the exact test, flow, workflow step, or nearby comment and add a scoped manifest mapping.
4. Use `partial` until every clause and required vector has proof; add `AUTO` only when the parent is complete.
5. Run `make check-test-automation`, the relevant normal tests, and the live lab or physical-device lane when required.

PR CI validates the catalog, classification plan, manifest, and flow wiring but
does not receive DigitalOcean, SSH, or lab credentials. Live lab execution
belongs on the operator workstation today. A future scheduled/manual job must
use a protected self-hosted runner or equivalent private-access design; never
use `pull_request_target` to execute arbitrary PR code with lab credentials.

## Selector policy and Patrol boundary

The Maestro pilot pairs stable `Semantics.identifier` values with deterministic,
lab-build-only labels for navigation and media detail, plus unique accessible
labels for auth fields. The lab build seam enables specialized labels before
`runApp` and explicitly turns on Flutter's semantic DOM afterward; flows target
only the wrapper-supplied loopback origin. Maestro's current web hierarchy omits
Flutter's fixed desktop shell chrome, so navigation flows use a 500×1400 outer
window and open the drawer as an active semantic route.

Ordinary production browser sessions keep Flutter's default lazy semantic DOM
and native widget semantics. Before a surface accumulates many flows, add stable
identifiers for request actions, dialogs, tabs, instance selectors, and
destructive confirmations. Flutter `Key`/`ValueKey` values are not Maestro E2E
selectors.

Patrol should start with the smallest native suite that proves platform state
Maestro web cannot: native passkeys, APNs permission and notification taps,
`cantinarr://` deep links, external OAuth/browser handoff, secure-session
restore, and background/foreground behavior. Real APNs, passkeys, Plex, and
store cases still require reviewed disposable accounts or dedicated hardware;
the driver does not remove that dependency.
