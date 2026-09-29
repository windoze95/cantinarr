# Cantinarr App Privacy Policy

Effective: September 25, 2026

Cantinarr is an open-source client app for self-hosted Cantinarr servers. Your app sends API requests to the server you chose and loads artwork from external image hosts. Your server's administrator controls your household's accounts, library connections, and saved requests. Optional push notifications send device delivery information and notification content through the configured push gateway, which may be Cantinarr's community relay.

## What the app connects to

- **Your Cantinarr server.** You (or your household's admin) choose the address. The app's browsing, searching, requesting, management, and AI API traffic goes to that server. The Cantinarr project has no access to it.
- **Artwork hosts.** Poster and backdrop art can load directly from TMDB's content delivery network (`image.tmdb.org`), subject to [TMDB's privacy policy](https://www.themoviedb.org/privacy-policy). Book and music artwork can use other image hosts supplied by their catalogs. A direct image request gives that host your network address and the requested image URL.
- **Sign-in and account-link providers.** When you start a supported sign-in or account-link flow, the app can open your configured identity provider, Plex, Hardcover, OpenAI, or xAI in a browser. For example, **Settings > AI Access** can show a one-time code for a personal OpenAI or xAI provider. The person linking that account signs in and approves it on the provider's page; the provider account password is entered there.
- **Platform push services.** The native app uses Apple's Push Notification service (APNs) on iOS and Firebase Cloud Messaging (FCM) on Android for device registration and notification delivery. The push flow is described below.

Cantinarr includes no ads, advertising tracking, analytics SDK, or crash-reporting SDK.

## Data stored on your server

Your server stores what it needs to run your household: your display name and role, your requests and issue reports, session/device records (device model names, for the admin's Devices screen), notification preferences, whether the admin granted you included AI access, and the email address used for a Plex invite if you choose to share it. If you add a personal AI key or an OpenAI or xAI OAuth link, the server stores that credential encrypted at rest. An OAuth link also stores the linked account's display metadata (email, plan, and for ChatGPT its usage windows) for its owner-facing settings screen. You can remove your personal provider or connection from **Settings > AI Access**. The included server profile is controlled by the admin. Server data lives wherever the server's admin deployed it and is controlled by that admin.

## AI assistant and external providers

Each user may choose a personal assistant provider: Anthropic, OpenAI, Google Gemini, or xAI Grok through their own API key, or OpenAI's ChatGPT/Codex service or xAI's Grok subscription service through their own account link. This works without included access and the personal provider need not match the server's provider. The server admin may configure the same choices as an included provider and grant access per user. A personal choice is an explicit override; if its key, authorization, runtime, or allowance is unavailable, Cantinarr reports that problem instead of silently sending the request to or charging the included provider. Removing the override explicitly returns the user to included access when granted.

When you use the assistant, your prompts, conversation context, and tool results are sent by your self-hosted server to the effective provider so it can produce a response. The admin may select **Local (OpenAI-compatible)** and configure their own endpoint; requests through that included profile go to the configured endpoint using the admin's network and proxy settings. A personal OpenAI key continues to reach OpenAI itself. With OpenAI (OAuth), those requests consume the selected personal or included ChatGPT account's Codex allowance and rate limits; with xAI Grok (OAuth), they consume the linked xAI account's Grok subscription allowance. People granted an included OAuth provider share that allowance; the admin sees a sharing and quota/cost warning before enabling access. Ordinary users can see that their source is included, but cannot see the shared account's email, plan, authorization, or usage windows. Saving an AI provider, model, key, or OAuth selection sends one small test message and requires a response. When the admin leaves daily shared-model testing enabled, the server also sends at most one small background test turn every 24 hours; the admin can disable it.

Autonomous issue remediation is a server-owned admin feature, separate from a user's interactive assistant access. When an admin enables it, Cantinarr sends the reported problem, relevant Radarr/Sonarr state, and remediation tool results to the admin's shared AI provider, using either its shared model or an admin-selected remediation model override tested against that provider. It does this regardless of the reporter's personal AI settings or included-access grant, never uses a reporter's personal credential, and consumes the server's shared API quota or shared OpenAI/xAI OAuth usage meter. Credential-scrubbed remediation transcripts and audit steps are stored in the server database so admins can review what the agent did.

Your self-hosted server keeps the assistant's conversation context in process memory so follow-up messages remain grounded. It becomes inaccessible after four hours of inactivity and is evicted on later assistant activity or a server restart; it is deleted immediately when a provider turn fails and is never written to the Cantinarr database. The chosen AI provider may retain or process submitted data under its own terms and the account or API key selected by you or your server admin.

A ChatGPT or xAI account link is outbound authorization from your server to that provider. It is separate from Cantinarr's inbound MCP OAuth, which lets an external MCP client sign in to your Cantinarr server. Data sent to an AI provider is governed by that provider and the account or API-key terms chosen by you or your server admin.

## Push notifications (iOS and Android)

Your server has no default push gateway. Its administrator enables push by setting `CANTINARR_PUSH_GATEWAY_URL`, and notification delivery also follows server policy, your account preferences, and your phone's permissions. The documented setup uses the community relay at `https://push.cantinarr.com`; administrators can choose another compatible gateway or leave push disabled.

For push registration, your app sends an APNs token on iOS or an FCM token on Android to your server. The server sends the configured gateway that token, the platform, a device identifier, and your server-local user identifier. Automatic gateway enrollment also sends the server's configured name. To deliver a notification, the server sends recipient identifiers, the notification title and body, and data used to open the relevant app screen. Depending on the enabled notification category, this can include media titles, request or issue details, and related record identifiers.

The gateway passes notifications to Apple's APNs or Google's FCM. The community relay operator processes the delivery data and notification content. Its enrollment record includes the server name and network address, its device registry stores tokens and identifiers, and its delivery log records notification titles, timestamps, delivery counts, and errors. Notification content is readable by the relay; HTTPS protects transport to the community relay but does not make notifications end-to-end encrypted. A self-hosted gateway is controlled by its own operator.

You can turn off **Receive push notifications** or individual categories in the app's notification settings. Disabling delivery does not itself delete an existing gateway registration. Signing out attempts to unregister the device; an offline server or gateway can prevent that attempt from completing. Revoking a device also removes its Cantinarr access. Contact your server administrator about a remaining registration. Apple's and Google's handling of push data is governed by their own policies.

## Community relay and support

Using the community push relay sends the registration and notification data described above to the relay operated for Cantinarr. Ordinary library browsing and AI requests go to your self-hosted server and its selected providers. The relay does not receive your instance API keys or AI provider credentials as part of push registration or delivery.

For questions about information stored on your server or a device registration, contact your server administrator. For community relay data questions, use the project contact below and provide only a non-secret reference; never post a push token, gateway key, connection link, or notification payload in a public issue. If you contact support through GitHub issues, what you post is public and governed by GitHub's terms.

## Children

Cantinarr is a household media-management tool and is not directed at children.

## Changes

Changes to this policy are made in the open, in the [Cantinarr repository](https://github.com/windoze95/cantinarr); this file's git history is the changelog.

## Contact

Questions: open an issue at https://github.com/windoze95/cantinarr/issues
