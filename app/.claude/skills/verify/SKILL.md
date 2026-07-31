---
name: verify
description: Drive the Cantinarr Flutter app in a browser to verify UI changes at runtime — no backend required. Use when a change to app/lib needs visual/behavioral verification (layout, navigation, theming).
---

# Verifying the Cantinarr app at runtime

The app normally needs a Cantinarr server to get past login. `DBPath` is
hardcoded to `/config/` in the Go server, so it won't boot on macOS, and the
published Docker image needs a working Docker daemon. For UI verification,
skip the server entirely:

## Preview harness (no backend)

`test/preview/preview_main.dart` boots the REAL app (router, shell, screens,
theme) with a faked authenticated admin (all modules + two Radarr instances)
and a stub Dio adapter. Data screens show their empty/error states; chrome,
navigation, and layout are the real code paths.

```bash
cd app
flutter run -d web-server -t test/preview/preview_main.dart \
  --web-port 7777 --web-hostname 127.0.0.1
# wait for "is being served at", then drive http://127.0.0.1:7777 in Chrome
```

Drive with the claude-in-chrome tools. Gotchas learned the hard way:

- **Debug-web page transitions are slow (2–4s)** — a screenshot right after a
  click captures a mid-transition frame. Routed pages paint an opaque ambient
  backdrop, so a mid-transition frame is a clean dissolve (lateral surfaces)
  or slide (pushes) — two screens showing *through* each other is a
  regression, not debug-web noise. Still `wait` 3–4s and re-screenshot before
  judging final layout.
- **`resize_window` may not change the CSS viewport** (window managers /
  non-100% zoom). Confirm with `javascript_tool`:
  `window.innerWidth` — the app's breakpoints key off CSS px (desktop ≥ 900).
  If you can't get below 900, cover mobile via the widget tests instead
  (`test/shell/adaptive_layout_test.dart` pumps 390x844 and 1400x900).
- The stub adapter returns `[]` for unknown paths; screens expecting a map
  shape (e.g. Radarr queue) show their error state — that's the stub, not a
  bug. Extend `_StubAdapter` in preview_main.dart if a screen's shape matters.
- Real-server slice: the auth screen can be pointed at
  `https://demo.cantinarr.com` to exercise the server-status flow (no creds,
  so stop at the login view).

## What to check for layout changes

Desktop (≥900 CSS px): persistent sidebar (no hamburger), active module
expanded into its pages, NO BottomNavigationBar, search bar/results/chat
capped via `AppBreakpoints.readableContentWidth`. Mobile (<900): hamburger
drawer, per-module bottom nav. The adaptive widget tests assert both.
