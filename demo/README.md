# Cantinarr Demo Server

A self-contained mock backend for App Store distribution review. Simulates the full Cantinarr API with in-memory data — no external services or databases required.

## Running

```bash
cd demo
go run .
```

The server starts on **port 8484**.

## Demo Credentials

| Username | Password | Role  |
|----------|----------|-------|
| admin    | demo     | admin |
| user     | demo     | user  |

New accounts can be registered with invite code: `DEMO42`

## What's Simulated

- Authentication (JWT login, registration, token refresh)
- Media discovery and search (movies, TV, persons)
- Download requests with progress simulation via WebSocket
- Radarr/Sonarr API proxies (quality profiles, root folders)
- Trakt integration (trending, popular, calendar, lists)
- AI chat with streaming SSE responses
- All content uses public domain films and metadata

## Branch Workflow

This code lives on the `demo` branch. To pull in the latest changes from `main`:

```bash
git checkout demo
git merge main
git push origin demo
```

Do not merge `demo` into `main` — demo-specific code should stay on this branch.
