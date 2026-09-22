---
title: Build and contribute
description: Set up the Go server, Flutter app, and documentation site using the repository's pinned tools.
sidebar:
  order: 1
---

Start from the [Cantinarr repository](https://github.com/windoze95/cantinarr). Read its current [contributor instructions](https://github.com/windoze95/cantinarr/blob/main/AGENTS.md) and any instructions inside the area you change.

## Get the source

```sh
git clone https://github.com/windoze95/cantinarr.git
cd cantinarr
```

Contributions go through a feature branch and a pull request. Start from fresh main, preserve unrelated work, and wait for the required checks. Maintainers handle merging.

## Server

Use the Go version required by `server/go.mod`. From `server/`:

```sh
go vet ./...
go test ./...
go run ./cmd/server
```

The server uses `/config` for its database and generated encryption key. Use a disposable development environment and make that path writable to the intended process. Do not point development work at a household's live data or services.

## Flutter app

Use the exact Flutter SDK version in `app/.flutter-version` and its bundled Dart SDK. From `app/`:

```sh
flutter pub get --enforce-lockfile
flutter analyze --no-fatal-infos --no-pub
flutter test --no-pub
flutter run --no-pub
```

Dependencies are locked in `pubspec.lock`. Intentional dependency or SDK upgrades include the corresponding lockfile and pin changes. Subsequent analyze, test, and build commands use `--no-pub` so they do not silently resolve a different set.

`make` builds Flutter web, copies it into the server's embedded assets, and builds the server. Mobile release builds run in CI.

## Build and test variables

These affect development or source builds. They are separate from [runtime deployment variables](/reference/generated/environment/):

| Name | Purpose |
| --- | --- |
| `CANTINARR_E2E_WEB_SEMANTICS` | Docker build argument and Flutter compile-time flag, default `false`. The private disposable lab enables it for deterministic automation labels. Setting it on an already built container has no effect |
| `APP_BUILD_NUMBER` | Root Docker build argument that sets the Flutter web build number when supplied |
| `VERSION` | Docker build argument for the stamped server version, default `dev` in source builds |
| `CODEX_VERSION` | Docker build argument for the bundled helper version. Keep it aligned with the checksums and tested protocol; it is not a runtime upgrade switch |
| `TARGETARCH` | BuildKit's target architecture, used to select the matching helper artifact |
| `XAI_BASE_URL` | Test-only endpoint override used by the Go provider contract tests. Leave it unset in deployments; configure self-hosted models through the Local AI provider in Settings |
| `CANTINARR_CODEX_APP_SERVER_SMOKE_BINARY` | Test-only path enabling the real pinned helper protocol smoke in CI |

Provider credentials and personal overrides are configured through Cantinarr's supported settings. Do not copy test endpoint overrides into production examples.

## Documentation site

Use Node 22.12 or newer and Python 3.9 or newer. From `docs-site/`:

```sh
npm ci
npm run dev
```

The site uses Astro and Starlight with local fonts and Pagefind search. `npm run build` synchronizes maintained references, builds every page and search index, and validates the result.

Edit task guides under `docs-site/src/content/docs/`. Edit canonical integration or technical content in its owning repository document; generated copies are recreated during the build.

## Keep changes documented

Update the task guide and the owning reference in the same PR as behavior changes. New routes, settings, environment variables, or service types should be findable from the relevant index.

Use clear outcomes and real examples. Avoid em dashes, marketing filler, unexplained internal jargon, and claims about unreleased production availability. See [writing and maintaining these docs](/contributing/documentation/).
