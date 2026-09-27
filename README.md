<p align="center">
  <img src="docs-site/public/icon.png" alt="Cantinarr logo" width="120">
</p>

<h1 align="center">Cantinarr</h1>

<p align="center">Self-hosted media requests and server management.</p>

<p align="center">
  <a href="https://github.com/windoze95/cantinarr/actions/workflows/ci.yml"><img src="https://github.com/windoze95/cantinarr/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="https://github.com/windoze95/cantinarr/releases/latest"><img src="https://img.shields.io/github/v/release/windoze95/cantinarr" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/windoze95/cantinarr" alt="License: AGPL-3.0"></a>
  <a href="https://discord.gg/zAgRwGwmVB"><img src="https://img.shields.io/badge/Discord-join%20chat-5865F2?logo=discord&amp;logoColor=white" alt="Join Discord"></a>
</p>

<p align="center">
  <a href="https://testflight.apple.com/join/bCPDwCsD"><img src="https://img.shields.io/badge/TestFlight-iOS%20beta-0D96F6?style=for-the-badge&amp;logo=apple&amp;logoColor=white" alt="iPhone and iPad beta on TestFlight"></a>
  <a href="https://play.google.com/apps/testing/codes.julian.cantinarr"><img src="https://img.shields.io/badge/Google_Play-Android%20beta-414141?style=for-the-badge&amp;logo=googleplay&amp;logoColor=white" alt="Android beta on Google Play"></a>
</p>

<p align="center">
  <a href="https://cantinarr.com">Website</a> ·
  <a href="https://docs.cantinarr.com">Documentation</a> ·
  <a href="https://demo.cantinarr.com">Live demo</a>
</p>

Cantinarr lets your household browse and request movies, TV shows, ebooks, audiobooks, and music. Administrators manage requests, libraries, and download clients from the same app. It connects to Radarr, Sonarr, Chaptarr, and Lidarr.

## Preview

<p align="center">
  <a href="app/ios/fastlane/screenshots/en-US/iphone69_01_discover.png"><img src="app/ios/fastlane/screenshots/en-US/iphone69_01_discover.png" alt="Discover movies and see what is available or requested" width="24%"></a>
  <a href="app/ios/fastlane/screenshots/en-US/iphone69_02_seasons.png"><img src="app/ios/fastlane/screenshots/en-US/iphone69_02_seasons.png" alt="Choose TV seasons to request and follow their availability" width="24%"></a>
  <a href="app/ios/fastlane/screenshots/en-US/iphone69_04_books.png"><img src="app/ios/fastlane/screenshots/en-US/iphone69_04_books.png" alt="Browse ebooks and audiobooks by title, author, or series" width="24%"></a>
  <a href="docs/images/music.jpg"><img src="docs/images/music.jpg" alt="Discover albums and genres with Music enabled in the navigation" width="24%"></a>
</p>

Navigation adapts to your services and permissions.

## Features

- Movie, season, ebook, audiobook, and album requests with approvals and per-user limits.
- Live availability, download progress, and push or Discord notifications.
- Library and download queue management across multiple service instances.
- Plex, Jellyfin, Emby, and Audiobookshelf account access and playback links.
- Connect links, passkeys, Plex sign-in, and OpenID Connect.
- Kids accounts with movie and TV rating limits.
- An optional [AI agent](https://docs.cantinarr.com/use/assistant/) for finding media, making requests, and admin troubleshooting, plus [supervised repairs](https://docs.cantinarr.com/admin/remediation/) for persistent download problems.
- An [MCP server](https://docs.cantinarr.com/integrations/mcp/) and a [Seerr-compatible API](https://docs.cantinarr.com/integrations/seerr-api/) for external tools.

## Getting started

Run the server with [Docker](https://docs.cantinarr.com/install/docker/), [Unraid](https://docs.cantinarr.com/install/platforms/), or a [Linux binary](https://docs.cantinarr.com/install/linux/), then follow the [setup guide](https://docs.cantinarr.com/start/quickstart/) to connect your services and invite your household.

The web app is included. Mobile apps connect to your server and are available in beta:

- **iPhone and iPad:** [TestFlight](https://testflight.apple.com/join/bCPDwCsD)
- **Android:** [Google Play](https://play.google.com/apps/testing/codes.julian.cantinarr)

## Documentation

- [Using Cantinarr](https://docs.cantinarr.com/start/for-households/)
- [Configuration](docs/configuration.md) and [integrations](https://docs.cantinarr.com/integrations/)
- [Troubleshooting](https://docs.cantinarr.com/troubleshooting/)
- [API reference](server/README.md#api-reference) and [building from source](https://docs.cantinarr.com/contributing/development/)

## Community

Join [Discord](https://discord.gg/zAgRwGwmVB) for help and discussion. Report bugs on [GitHub](https://github.com/windoze95/cantinarr/issues) or suggest and vote on features on the [roadmap](https://cantinarr.com/roadmap/).

Pull requests are welcome. See the [contributor guide](https://docs.cantinarr.com/contributing/development/) and [repository instructions](AGENTS.md). You can support development through [GitHub Sponsors](https://github.com/sponsors/windoze95).

## License

[AGPL-3.0](LICENSE). Copyright (c) 2026 Julian Dice.

This product uses the TMDB API but is not endorsed or certified by [TMDB](https://www.themoviedb.org/).
