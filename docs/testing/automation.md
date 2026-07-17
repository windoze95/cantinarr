# Test automation

Cantinarr has 584 parent cases, not 584 equivalent UI scripts. The explicit
Plex vectors plus required download-client and MCP mode matrices raise the
conservative minimum to 802 executions before prose variants are expanded.
Automation therefore follows the cheapest layer that can prove each behavior,
while the catalog ID remains the stable traceability key.

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

The initial web suite proves one complete parent (`AUTH-007`) and records
honest partial coverage for `AUTH-022`, `NAV-001`, and `NAV-003`. It runs three
isolated flows as the lab's second admin and no-grants requester. Search is not
claimed: Maestro web can focus Flutter's fixed search field but does not
reliably inject text into its hidden editing element, so that attempted pilot
was removed instead of turning a red flow into nominal coverage.

The suite deliberately runs locally, not on Maestro Cloud. The DigitalOcean
firewall remains SSH-only from the operator's exact address, and the lab-owned
wrapper opens one temporary `127.0.0.1` tunnel. Each suite chooses a fresh
loopback origin so Chromium cannot mask a newly deployed candidate with an
older Flutter service-worker/cache entry. Generated passwords are read
from the lab's ignored mode-0600 environment file, passed only in
`MAESTRO_*` process environment, scrubbed from streaming console output and
completed-run artifacts, and never placed in command arguments or committed
files. Exit and signal traps make a best-effort scrub (or remove the run
directory) if local execution is interrupted; `SIGKILL` cannot run cleanup.
Locally built lab candidates also set the Docker build argument
`CANTINARR_E2E_WEB_SEMANTICS=true`. Production images default it to `false`;
the build-time seam keeps E2E-only flattened labels out of ordinary browser
accessibility while making lab selectors deterministic.

Prerequisites:

- the private `cantinarr-lab` checkout next to `src/`, with a provisioned lab;
- Java 17 or 21;
- Maestro CLI exactly 2.6.1 (the version proved by this harness); and
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

Maestro writes JUnit, logs, and failure screenshots under the ignored
`e2e/maestro/.artifacts/` tree. The runner uses a restrictive umask and
redacts the selected password before reporting the artifact path.

## Traceability and conversion workflow

`automation.json` records `automated` and `partial` proof without copying the
catalog description. `e2e/maestro/suites.json` maps each executable flow to
one of the five fixed lab identities. `scripts/check_test_automation.py`
validates all catalog counts and IDs, proof paths, the flow command allowlist,
repository-helper containment, password-input restrictions, suite membership,
and the rule that a fully automated parent carries the `AUTO` tag.

When converting a case:

1. Split its assertions by proof surface; do not force API or chaos assertions through UI taps.
2. Add the lower-level proof first where applicable, then one Maestro journey only if visible integration behavior remains.
3. Put every catalog ID in the test/flow name or comment and add a scoped mapping in `automation.json`.
4. Use `partial` until every clause and required vector has proof; add `AUTO` only when the parent is complete.
5. Run `make check-test-automation`, the relevant normal tests, and the live lab suite when its environment is required.

PR CI validates the catalog, manifest, and flow wiring but does not receive
DigitalOcean, SSH, or lab credentials. Live lab execution belongs on the
operator workstation today. A future scheduled/manual job must use a protected
self-hosted runner or equivalent private access design; never use
`pull_request_target` to execute arbitrary PR code with lab credentials.

## Selector policy and Patrol boundary

The pilot pairs stable `Semantics.identifier` values with deterministic,
lab-build-only labels for navigation and media detail, plus unique accessible
labels for auth fields. The lab build seam enables the specialized labels
before `runApp` and explicitly turns on Flutter's semantic DOM afterward; flows
target only the wrapper-supplied loopback origin. Maestro's current web
hierarchy omits Flutter's fixed desktop shell
chrome, so navigation flows use a 500×1400 outer window, open the drawer as an
active semantic route, and render its complete inventory to prevent off-screen
false negatives.
Ordinary production browser sessions keep Flutter's default lazy semantic DOM
and native widget semantics. Before a surface accumulates many flows,
add stable identifiers for request actions, dialogs, tabs, instance selectors,
and destructive confirmations. Flutter `Key`/`ValueKey` values are not visible
to Maestro and are not E2E selectors.

Patrol should start with the smallest native suite that proves platform state
Maestro web cannot: native passkeys, APNs permission and notification taps,
`cantinarr://` deep links, external OAuth/browser handoff, secure-session
restore, and background/foreground behavior. Real APNs, passkeys, Plex, and
store cases still require reviewed disposable accounts or dedicated hardware;
the driver does not remove that dependency.
