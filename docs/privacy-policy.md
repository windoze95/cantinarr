# Cantinarr App Privacy Policy

Effective: July 13, 2026

Cantinarr is an open-source client app for self-hosted Cantinarr servers. The short version: your app sends API requests to the server you chose, loads artwork from TMDB, and opens third-party sign-in pages only when you explicitly start an account link. The Cantinarr project does not run a hosted service that can see your household's data.

## What the app connects to

- **Your Cantinarr server.** You (or your household's admin) choose the address. The app's browsing, searching, requesting, management, and AI API traffic goes to that server. The Cantinarr project has no access to it.
- **The TMDB image CDN** (`image.tmdb.org`). Poster and backdrop art is loaded directly from TMDB's content delivery network. Those image requests are subject to [TMDB's privacy policy](https://www.themoviedb.org/privacy-policy).
- **ChatGPT in your browser, only when you choose Connect ChatGPT.** If your server admin selected ChatGPT (Codex), **Settings > ChatGPT** opens OpenAI's device-authorization page and shows a one-time code. You sign in and approve the connection in that browser page; your ChatGPT password never passes through the Cantinarr app or server.

There are no ads, no analytics, no crash-reporting SDKs, and no tracking of any kind in the app.

## Data stored on your server

Your server (not the project) stores what it needs to run your household: your display name and role, your requests and issue reports, session/device records (device model names, for the admin's Devices screen), notification preferences, and — only if you choose to share it — the email address used for a Plex invite. If you link ChatGPT, it also stores your ChatGPT authorization encrypted at rest, plus account and usage metadata used by the ChatGPT settings screen. You can remove that connection from **Settings > ChatGPT**. Server data lives wherever the server's admin deployed it and is controlled by that admin.

## AI assistant and external providers

The server admin chooses the assistant provider: Anthropic, OpenAI, or Google Gemini through an admin-supplied API key, or OpenAI's ChatGPT/Codex service through each user's own account link. When you use the assistant, your prompts, conversation context, and tool results are sent by your self-hosted server to that chosen provider so it can produce a response. With ChatGPT (Codex), those requests use your linked ChatGPT account's subscription allowance and rate limits; if you do not link an account, chat is unavailable while that provider is selected.

Your self-hosted server keeps the assistant's conversation context in process memory so follow-up messages remain grounded. It becomes inaccessible after four hours of inactivity and is evicted on later assistant activity or a server restart; it is deleted immediately when a provider turn fails and is never written to the Cantinarr database. The chosen AI provider may retain or process submitted data under its own terms and the account or API key selected by you or your server admin.

The ChatGPT account link is outbound authorization from your server to OpenAI. It is separate from Cantinarr's inbound MCP OAuth, which lets an external MCP client sign in to your Cantinarr server. Data sent to an AI provider is governed by that provider and the account or API-key terms chosen by you or your server admin.

## Push notifications (iOS)

If your server has push notifications configured, the app registers an Apple push token with **your server**, which relays notifications through its configured push gateway to Apple's APNs. The token is used only to deliver your notifications and can be removed by revoking the device from the server's Devices screen.

## Data the developer collects

None. The app and server send nothing to the developer. AI traffic goes from your self-hosted server to the provider selected by your server admin, not through a Cantinarr-operated service. If you contact support (GitHub issues), whatever you post there is public and governed by GitHub's terms.

## Children

Cantinarr is a household media-management tool and is not directed at children.

## Changes

Changes to this policy are made in the open, in the [Cantinarr repository](https://github.com/windoze95/cantinarr); this file's git history is the changelog.

## Contact

Questions: open an issue at https://github.com/windoze95/cantinarr/issues
