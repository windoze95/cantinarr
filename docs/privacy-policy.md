# Cantinarr App Privacy Policy

Effective: July 13, 2026

Cantinarr is an open-source client app for self-hosted Cantinarr servers. The short version: your app sends API requests to the server you chose, loads artwork from TMDB, and opens third-party sign-in pages only when you explicitly start an account link. The Cantinarr project does not run a hosted service that can see your household's data.

## What the app connects to

- **Your Cantinarr server.** You (or your household's admin) choose the address. The app's browsing, searching, requesting, management, and AI API traffic goes to that server. The Cantinarr project has no access to it.
- **The TMDB image CDN** (`image.tmdb.org`). Poster and backdrop art is loaded directly from TMDB's content delivery network. Those image requests are subject to [TMDB's privacy policy](https://www.themoviedb.org/privacy-policy).
- **ChatGPT in your browser, only when you choose Connect ChatGPT.** **Settings > AI Access** can open OpenAI's device-authorization page and show a one-time code for your personal provider. An admin can use the same explicit flow for an included server provider. The person linking the account signs in and approves it on OpenAI's page; the ChatGPT password never passes through the Cantinarr app or server.

There are no ads, no analytics, no crash-reporting SDKs, and no tracking of any kind in the app.

## Data stored on your server

Your server (not the project) stores what it needs to run your household: your display name and role, your requests and issue reports, session/device records (device model names, for the admin's Devices screen), notification preferences, whether the admin granted you included AI access, and — only if you choose to share it — the email address used for a Plex invite. If you add a personal AI key or link ChatGPT, the server stores that credential encrypted at rest. A ChatGPT link also stores account and usage metadata for its owner-facing settings screen. You can remove your personal provider or connection from **Settings > AI Access**. The included server profile is controlled by the admin. Server data lives wherever the server's admin deployed it and is controlled by that admin.

## AI assistant and external providers

Each user may choose a personal assistant provider: Anthropic, OpenAI, or Google Gemini through their own API key, or OpenAI's ChatGPT/Codex service through their own account link. This works without included access and the personal provider need not match the server's provider. The server admin may configure the same choices as an included provider and grant access per user. A personal choice is an explicit override; if its key, authorization, runtime, or allowance is unavailable, Cantinarr reports that problem instead of silently sending the request to or charging the included provider. Removing the override explicitly returns the user to included access when granted.

When you use the assistant, your prompts, conversation context, and tool results are sent by your self-hosted server to the effective provider so it can produce a response. With ChatGPT (Codex), those requests consume the selected personal or included account's subscription allowance and rate limits. People granted an included ChatGPT provider share that allowance; the admin sees a sharing and quota/cost warning before enabling access. Ordinary users can see that their source is included, but cannot see the shared account's email, plan, authorization, or usage windows.

Autonomous issue remediation is a server-owned admin feature, separate from a user's interactive assistant access. When an admin enables it, Cantinarr sends the reported problem, relevant Radarr/Sonarr state, and remediation tool results to the admin's shared AI provider. It does this regardless of the reporter's personal AI settings or included-access grant, never uses a reporter's personal credential, and consumes the server's shared API quota or shared ChatGPT usage meter. Credential-scrubbed remediation transcripts and audit steps are stored in the server database so admins can review what the agent did.

Your self-hosted server keeps the assistant's conversation context in process memory so follow-up messages remain grounded. It becomes inaccessible after four hours of inactivity and is evicted on later assistant activity or a server restart; it is deleted immediately when a provider turn fails and is never written to the Cantinarr database. The chosen AI provider may retain or process submitted data under its own terms and the account or API key selected by you or your server admin.

A ChatGPT account link is outbound authorization from your server to OpenAI. It is separate from Cantinarr's inbound MCP OAuth, which lets an external MCP client sign in to your Cantinarr server. Data sent to an AI provider is governed by that provider and the account or API-key terms chosen by you or your server admin.

## Push notifications (iOS)

If your server has push notifications configured, the app registers an Apple push token with **your server**, which relays notifications through its configured push gateway to Apple's APNs. The token is used only to deliver your notifications and can be removed by revoking the device from the server's Devices screen.

## Data the developer collects

None. The app and server send nothing to the developer. AI traffic goes from your self-hosted server to the effective personal or admin-shared provider, not through a Cantinarr-operated service. If you contact support (GitHub issues), whatever you post there is public and governed by GitHub's terms.

## Children

Cantinarr is a household media-management tool and is not directed at children.

## Changes

Changes to this policy are made in the open, in the [Cantinarr repository](https://github.com/windoze95/cantinarr); this file's git history is the changelog.

## Contact

Questions: open an issue at https://github.com/windoze95/cantinarr/issues
