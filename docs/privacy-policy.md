# Cantinarr App Privacy Policy

Effective: July 5, 2026

Cantinarr is an open-source client app for self-hosted Cantinarr servers. The short version: the app talks to your server and to no one else, and the project does not run any service that could see your data.

## What the app connects to

- **Your Cantinarr server.** You (or your household's admin) choose the address. Everything the app does — browsing, searching, requesting, managing — is a request to that server. The Cantinarr project has no access to it.
- **The TMDB image CDN** (`image.tmdb.org`). Poster and backdrop art is loaded directly from TMDB's content delivery network. Those image requests are subject to [TMDB's privacy policy](https://www.themoviedb.org/privacy-policy).

There are no ads, no analytics, no crash-reporting SDKs, and no tracking of any kind in the app.

## Data stored on your server

Your server (not the project) stores what it needs to run your household: your display name and role, your requests and issue reports, session/device records (device model names, for the admin's Devices screen), notification preferences, and — only if you choose to share it — the email address used for a Plex invite. Server data lives wherever the server's admin deployed it.

## Push notifications (iOS)

If your server has push notifications configured, the app registers an Apple push token with **your server**, which relays notifications through its configured push gateway to Apple's APNs. The token is used only to deliver your notifications and can be removed by revoking the device from the server's Devices screen.

## Data the developer collects

None. The app sends nothing to the developer. If you contact support (GitHub issues), whatever you post there is public and governed by GitHub's terms.

## Children

Cantinarr is a household media-management tool and is not directed at children.

## Changes

Changes to this policy are made in the open, in the [Cantinarr repository](https://github.com/windoze95/cantinarr); this file's git history is the changelog.

## Contact

Questions: open an issue at https://github.com/windoze95/cantinarr/issues
