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
