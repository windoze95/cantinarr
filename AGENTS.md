# Agent Instructions

Operating manual for AI agents and human contributors. `CLAUDE.md` imports this file — this is the single canonical copy; edit it here, never there.

## Collaboration

- Do not just agree with the user by default. If a request would weaken the project, hurt maintainability, reduce correctness, or make the task outcome worse, push back clearly and suggest the better path.

## Git workflow

- Before starting any PR-sized change: `git fetch origin`, make sure local `main` is even with `origin/main`, and create a feature branch from that fresh base.
- Branch names follow `feat/…`, `fix/…`, `docs/…`, `chore/…`.
- Do not open PRs directly from `main`. If work accidentally happens on `main`, verify `main` is still even with `origin/main`, then move the work to a feature branch before committing.
- Preserve user work. Do not revert or delete unrelated local changes or untracked files.
- When the change is ready: commit on the feature branch, push it right away, and open a ready-for-review PR (never a draft) with `gh pr create` unless the user explicitly asks not to.
- After opening a PR, monitor every required CI check to completion and merge only after they are all green. If a check fails, diagnose and fix it on the same branch, push the fix, and wait for the rerun to pass before merging; never merge with pending or failing required checks.
- After a PR merges, do not reuse its branch — start the next change from a fresh `main`.

## Verification

- Server changes: run `go vet ./...` and `go test ./...` from `server/`.
- App changes: run `flutter analyze --no-fatal-infos` and `flutter test` from `app/`.
- Catalog changes: run `python3 docs/testing/check_catalog.py` from the repository root.
- CI validates the regression catalog and runs the server/app checks above on every PR, plus a `CGO_ENABLED=0` server build and a `flutter build web --release`. A PR is not done if any of them fail.
- Codex integration changes are also proved against the checksum-verified pinned app-server in CI. The Docker workflow builds and smoke-tests both Dockerfiles, including bundled license notices, before publishing the root image to GHCR.
- iOS release builds happen only in CI (`testflight.yml`, auto-deploys on `main` when iOS-relevant `app/**` paths change — web/android/desktop subdirs and store-listing metadata/screenshots are excluded; listing copy syncs via `storelisting.yml` instead). Don't assume a local iOS toolchain; when one isn't available, sanity-check Swift with `swiftc -parse` and let CI prove the build.
- iOS signing is manual, via the `IOS_PROVISIONING_PROFILE_BASE64` secret. Changing app capabilities/entitlements invalidates the profile — regenerate it and update the secret.
- Android release builds happen only in CI too (`playstore.yml`, builds a signed AAB on `main` when Android-relevant `app/**` paths change — web/ios/desktop subdirs and store-listing metadata are excluded — and uploads it to the Play beta track when `PLAY_SERVICE_ACCOUNT_JSON` is set). PRs that touch `app/android/**`, `app/pubspec.yaml`, or the workflow get a build-only check (no upload). No local Android SDK is assumed; let CI prove the build.
- Android signing uses the `ANDROID_KEYSTORE_*` secrets (the upload keystore lives outside the repo). Store pipelines, secrets, and the one-time console setup are documented in `docs/store-release.md`.
- Merges to `main` publish `ghcr.io/windoze95/cantinarr` (`latest`; version tags on `v*` releases).
- Site changes (`site/`, plain static HTML/CSS, no build step): merges to `main` deploy `site/` to Cloudflare Pages via `site.yml` when the `CLOUDFLARE_API_TOKEN`/`CLOUDFLARE_ACCOUNT_ID` secrets are set; manual deploy is `npx wrangler pages deploy site --project-name=cantinarr`.
- Mention any tests or checks that could not be run.

## Regression test catalog

[`docs/testing/README.md`](docs/testing/README.md) is the canonical behavioral regression catalog. Treat it as part of implementation, not release-only paperwork or a replacement for unit/integration tests.

- Before changing user-visible behavior, an API or route, screen, setting, schema, event, permission boundary, integration, deployment path, or workflow, search the catalog for affected case IDs. Reconcile those cases before declaring the change complete.
- Update the catalog in the same PR as the behavior. Update an existing case when its setup, action, or expected result intentionally changes. Add a case for new behavior, for a defect that exposed missing regression coverage, or for a materially different boundary or failure mode. A bug fix must identify an existing case that would have caught it, or add/strengthen one when none does.
- For every new or changed behavior, cover all applicable dimensions: happy path; empty/minimum/maximum/invalid input; anonymous/requester/admin authorization; persistence and restart/upgrade; cancellation, timeout, retry, idempotency, duplicate submission, concurrency, and partial failure; cache freshness, realtime, and cross-device behavior; external source-of-truth verification; backward compatibility; and secret/privacy/logging containment. Explain intentionally inapplicable high-risk dimensions in the PR.
- Never weaken an expected result merely to match broken implementation. Record the defect. A `GAP` case requires a linked, accepted issue or decision in `docs/testing/known-gaps.md`; it records required but unshipped behavior and is never a release waiver. GAP ledger rows are permanent: when behavior ships or is deliberately withdrawn, remove the tag but retain the row with a resolved/withdrawn status and decision reference. An in-scope P0 GAP blocks release, while P1 requires the normal recorded exception. Product docs must continue to describe shipped reality.
- Case IDs are permanent and globally unique. Use `PREFIX-NNN`; a new prefix starts at `001`, otherwise use one greater than the highest number ever assigned in that domain, including retired IDs. Never renumber or reuse an ID. Keep an ID when behavior is renamed. When shipped behavior is removed, retire its ID in `docs/testing/retired-cases.md` with a substantive reason, replacement or `None`, and PR or release reference.
- The `AUTO` tag means the case includes machine-driven repository or CI proof; its definition names the command, workflow, build, or automated portion and may still require manual/external assertions. Existing automated coverage does not replace a behavioral case. Add a durable `AUTO: path/to/test::name` annotation when a named test automates the behavior, and update it in the same PR if the test moves, is renamed, or is deleted.
- When a case adds or changes a role, account, data state, upstream service/version, device, failure injection, evidence, or cleanup requirement, update `docs/testing/fixtures.md` and the run cleanup guidance in the same PR.
- Keep committed catalog files run-neutral: active definitions stay `[ ]`, vector results and run metadata stay blank, and PASS/FAIL/BLOCKED/N/A annotations, dates, screenshots, and evidence links belong only in a release/issue artifact or a copy outside the worktree. Do not add run copies to `.gitignore`.
- Update the catalog dashboard whenever cases are added or retired, then run `python3 docs/testing/check_catalog.py`. Every PR must list case IDs added, updated, retired, or confirmed accurate; `Affected case IDs: no change — <reason>` requires a specific reason. CI rejects missing or contradictory catalog declarations.
- Catalog maintenance does not replace updates to the owning product documentation below.

## Architecture conventions

- **The live DB schema is code, not SQL files.** It lives in `server/internal/db/db.go` (`initSQL` plus the in-code migration/`ALTER` list). Schema changes go there.
- **Never trust a stored copy of *arr state.** Admins edit Radarr/Sonarr/Chaptarr directly, so any snapshot drifts. Availability and library state are computed live from the arrs; if you must cache, you must also have a freshness story (webhook invalidation, short TTL, or refetch-on-view).
- **Media types vs service types.** `movie`/`tv`/`book` describe media; `radarr`/`sonarr`/`chaptarr` describe services. Store and compare media types — don't substitute one for the other.
- **Never silently dedupe or merge distinct records in search results.** Surface each record and let the user decide (e.g. two library entries for the same title are two results).
- **Secrets stay server-side and encrypted.** Instance API keys and credentials are AES-256-GCM encrypted at rest; never log them, return them in API responses, or write them into docs/examples.
- **Requesters and admins speak different languages.** User-facing request UI uses requester vocabulary (Available / Requested / Downloading), not arr jargon (monitored, cutoff, unmet).

## Documentation

Docs are part of the change, not a follow-up. A feature is not merged-complete until the docs that describe that surface are true again.

| Doc | Owns |
|---|---|
| `README.md` | Product pitch, feature list, quick start, configuration & env-var tables |
| `server/README.md` | API route reference, MCP tool table (incl. the tool count), DB tables, WebSocket events, env vars, server package tree |
| `app/README.md` | App features/screens, navigation map, project structure, key dependencies |
| `docs/testing/README.md` | Canonical regression catalog, stable case IDs, coverage scope, counts, and release qualification rules |
| `docs/store-release.md` | Store release pipeline: how builds reach TestFlight/Play, signing secrets, one-time store-console setup |
| `site/` | Marketing site (cantinarr.com): public pitch, store badges/links, live-demo link, self-host snippet |
| `AGENTS.md` | Workflows, verification, conventions (this file) |

- When a change touches a documented surface (new route, tool, env var, table, screen, workflow), update the owning doc **in the same PR**. The PR template's docs checklist is there to force the question.
- Numbers drift fastest: tool counts, route lists, env-var tables, version floors (Go, Flutter/Dart). If you add one, update the count everywhere it appears (`grep -ri` for the old number).
- Product docs describe shipped reality — never "planned", "upcoming", or aspirational behavior. Required but unimplemented behavior belongs only in an explicitly tagged catalog GAP until it ships.
- `CLAUDE.md` must remain a thin import of this file so every agent reads one playbook. If you change workflows here, check `CLAUDE.md` still just imports and points.
