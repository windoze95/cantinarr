# Test automation

Automation uses the cheapest layer that can prove each behavior. Coverage
lives in the test suites themselves — there is no per-case coverage ledger or
evidence manifest to maintain. `make check-test-automation` validates only
the catalog line format, the README area counts, and Maestro flow safety.

## CI lanes

Every pull request runs the same three checks: `Test catalog` (this folder's
lint plus Maestro flow safety), `Go` (`go vet`, `go test` with the pinned
Codex app-server smoke, and a `CGO_ENABLED=0` build), and `Flutter`
(`flutter analyze`, `flutter test`, and a release web build). The same suite
re-runs on every push to `main` to catch merge skew between independently
green PRs, and on a weekly schedule to catch toolchain drift — the Flutter
`stable` channel and Go toolchain float forward even when the repo does not
change. None of these lanes receive credentials of any kind; see
[environments](environments.md) for what each layer needs, and does not
need, to run.

## Layer ownership

| Layer | Primary responsibility |
|---|---|
| Go unit/API/integration | Contracts, authorization matrices, sanitization, concurrency, idempotency, cache behavior, protocol vectors, and most controlled failures |
| Flutter unit/widget | State matrices, validation, labels, loading/error/empty states, layouts, and provider behavior |
| Maestro | A thin set of black-box web journeys across authentication and role-specific navigation against the disposable lab; add other surfaces only when the web driver can prove them reliably |
| Patrol | Native-only boundaries that benefit from Dart assertions: passkeys, notification permissions/taps, deep links, external browser/WebView handoff, app lifecycle, and network controls |
| Manual/external | Physical-device delivery, real Plex share truth, store submission, cross-browser/accessibility audits, low-end performance, and exploratory sessions |

Go integration proof runs against in-process fakes, never live services:
the service clients take constructor-injected base URLs (`radarr`, `sonarr`,
`chaptarr`, the download clients, `tautulli`, `plex`, the push gateway) and
the AI SDKs honor base-URL environment overrides, so tests point them at
`httptest` servers replaying real API shapes. Contract, authorization, and
failure vectors run deterministically with zero credentials.

When adding coverage for a behavior, split its assertions by proof surface:
add lower-level proof first where applicable, then one black-box journey only
if visible integration behavior remains. Do not force API or chaos assertions
through UI taps. A UI success message is never sufficient for a case that
also requires API, upstream, filesystem, or realtime proof.

Maestro is the first black-box browser lane because it can drive the actual
same-origin web app served by the lab without changing application dependencies.
Patrol should be introduced with the first native boundary case, not as a
second copy of the Maestro suite. No Patrol suite is implemented yet.

## Current Maestro smoke suite

The web suite contains three isolated flows: a password-login journey that
verifies error copy does not reveal whether a username exists, an admin
module-inventory check, and a no-grants requester navigation check that
verifies admin modules stay out of the drawer. Search is not claimed: Maestro
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
the private lab repo remains the billable-resource teardown; destroy the
Droplet after each session — a powered-off Droplet still bills.

The runner reports the ordinary Maestro exit result and keeps
lab-password-scrubbed JUnit XML under the ignored private
`e2e/maestro/.artifacts/suites/` tree. Native debug, console, and screenshot
artifacts are deleted after normal, failed, and catchable interrupted runs;
they are not an evidence channel. A host crash or `SIGKILL` can prevent that
cleanup. There is no bespoke Markdown or screenshot-report pipeline. Use
[run-template.md](run-template.md) when a human run record is needed.

`e2e/maestro/suites.json` maps each suite flow to one of the five fixed lab
identities. Flows outside a suite are still safety-linted, so experimental
flows can land without ceremony. PR CI validates catalog format, counts, and
flow safety but does not receive DigitalOcean, SSH, or lab credentials. Live
lab execution belongs on the operator workstation today. A future
scheduled/manual job must use a protected self-hosted runner or equivalent
private-access design; never use `pull_request_target` to execute arbitrary
PR code with lab credentials.

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
