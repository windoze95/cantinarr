---
title: Environment variables and configuration
description: Choose the variables your setup needs, understand their defaults, and apply them without losing your settings.
sidebar:
  order: 4
---

Environment variables configure the Cantinarr server when it starts. The phone and web apps use that server's configuration. Most choices, including library connections, credentials, request rules, and user access, belong in **Settings** inside Cantinarr.

The [complete variable reference](/reference/generated/environment/) lists each supported deployment variable and its default. Use this guide to decide which ones your installation needs and where to put them.

## What the starter example includes

The [first setup example](/start/quickstart/) publishes port 8585, preserves `/config`, and explicitly sets:

```yaml
    environment:
      CANTINARR_PUSH_GATEWAY_URL: "https://push.cantinarr.com"
```

That line enables phone push through the community relay. Registration is automatic; no API key is needed. The server has **no default push gateway**. If you leave the variable unset or empty, push is disabled. A working gateway also needs [server, account, and phone notification permissions](/integrations/push/).

For a normal first installation, you do not need to invent JWT or encryption secrets. Cantinarr creates and preserves them. Keep the whole `/config` volume and its original encryption key when updating or restoring.

## Choose variables for your setup

| You need to | Variables to consider | What happens if you leave them unset |
| --- | --- | --- |
| Enable phone notifications | `CANTINARR_PUSH_GATEWAY_URL` | Push is disabled |
| Use a pinned gateway key or a restricted enrollment service | `CANTINARR_PUSH_API_KEY`, `CANTINARR_PUSH_ENROLL_TOKEN` | With a gateway URL, Cantinarr enrolls automatically; no enrollment token is sent |
| Name this server | `CANTINARR_SERVER_NAME` | The display name is `Cantinarr` |
| Change the server's internal listen port | `CANTINARR_PORT` | It listens on 8585; changing only the host port in Compose does not require this variable |
| Give library managers a reliable callback address | `CANTINARR_ARR_CALLBACK_URL` | Callbacks use the direct request origin, which may be wrong behind a proxy |
| Serve MCP through a stable HTTPS address | `CANTINARR_OAUTH_ISSUER` | The issuer is derived from the request |
| Allow additional browser origins to call MCP | `CANTINARR_MCP_ALLOWED_ORIGINS` | Only a configured issuer's origin is allowed; without either setting, browser requests carrying `Origin` are rejected |
| Enable downloads of completed library files | `CANTINARR_MEDIA_ROOTS` | File downloads are disabled; enabling them also requires readable media and instance path mappings |
| Run the container under a chosen user and group | `PUID`, `PGID` | The official image runs as root; `PGID` defaults to `PUID` when `PUID` is set |
| Supply an existing externally managed encryption or signing secret | `CANTINARR_ENCRYPTION_KEY`, `CANTINARR_JWT_SECRET` | The server creates and stores its own values |
| Set fallback choices for the included AI provider | `CANTINARR_AI_PROVIDER`, `CANTINARR_AI_MODEL` | Untouched installs select OpenAI OAuth; saved choices in Settings take precedence |
| Use a custom Codex helper location or memory-backed directory | `CANTINARR_CODEX_BIN`, `CANTINARR_CODEX_RUNTIME_DIR` | The server finds its helper and uses `/dev/shm/cantinarr-codex`; official images provide both |
| Associate a native app with your HTTPS domain | `CANTINARR_APPLE_APP_IDS`, `CANTINARR_ANDROID_PACKAGE_NAME`, `CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS`, `CANTINARR_WEBAUTHN_EXTRA_ORIGINS` | No extra association IDs, fingerprints, or origins are added; the Android package defaults to `codes.julian.cantinarr` |
| Use the system proxy configuration for external requests | `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` | External requests go directly unless an outbound proxy is saved in Settings |
| Disable Cantinarr's periodic release lookup | `CANTINARR_DISABLE_UPDATE_CHECK` | The server can check for releases; this is separate from container and app-store updates |

For addresses, follow [networking](/install/networking/). For file access, follow [file downloads](/admin/file-downloads/). Native passkey association values must match the app's signing identity; do not copy another application's identifiers into your configuration. Browser passkeys can still work without native association.

## Put variables in the right place

### Docker Compose

Add values to the existing `environment` block under the `cantinarr` service. Quote values in YAML, including numeric IDs and boolean-looking strings. Do not add a second `environment` block to the same service.

For example, a server on the same Docker network as its library managers could use:

```yaml
    environment:
      CANTINARR_PUSH_GATEWAY_URL: "https://push.cantinarr.com"
      CANTINARR_SERVER_NAME: "Our library"
      CANTINARR_ARR_CALLBACK_URL: "http://cantinarr:8585"
```

Use that callback address only if the library managers can actually reach it. **Settings > External Address** separately controls the address used in invitations and external sign-in.

### A Compose .env file

A host `.env` file supplies values for `${...}` expressions in Compose. Adding a name there does not automatically put it inside the container. Forward it in `environment`, or use a service-level `env_file`. Values in `environment` take precedence over `env_file`. See Docker's [container environment guide](https://docs.docker.com/compose/how-tos/environment-variables/set-environment-variables/).

To read the gateway URL from `.env`, replace the literal value in the starter example's `environment` block with this expression:

```yaml
      CANTINARR_PUSH_GATEWAY_URL: "${CANTINARR_PUSH_GATEWAY_URL-https://push.cantinarr.com}"
```

For that expression, an unset variable selects the community relay; an explicitly empty value disables push. In `.env`, that looks like:

```dotenv
CANTINARR_PUSH_GATEWAY_URL=
```

This fallback belongs to that Compose file. The server's default remains unset. A copied example with a literal URL must be edited directly or changed to use substitution. Docker's [interpolation reference](https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/) explains the difference between `${NAME-default}` and `${NAME:-default}`; the second also replaces an empty value.

Protect files containing secrets and keep them out of source control. Do not paste a rendered Compose configuration into a support request: it can contain expanded secret values.

### Other container managers

Use the platform's environment-variable fields or its stack editor. Preserve the current data volume when applying the change. [Unraid and app catalogs](/install/platforms/) explains the deployment settings shared by those platforms.

### Native Linux or source runs

Set variables in the process environment, for example through the systemd `EnvironmentFile` in the [Linux guide](/install/linux/). The server also reads `.env` from its working directory. Existing process environment variables win over that file. Its location is not automatically the directory containing the executable.

## Apply a change

For Compose, run from the directory containing your deployment file:

```sh
docker compose config --quiet
docker compose up -d cantinarr
docker compose ps
```

The first command validates the configuration without printing its values. The second recreates the container when its configuration changes. [`docker compose restart`](https://docs.docker.com/reference/cli/docker/compose/restart/) alone does not apply changed environment values.

For native services, restart the service after changing its environment. If you changed a systemd unit itself, reload systemd's configuration before restarting it.

Then verify the feature you changed. Test a push on a phone, reconfigure instant updates after changing the callback address, or reconnect an MCP client after changing the issuer. A running container proves the process started; it does not prove those separate connections work.

## Formats and settings that need care

- **Comma-separated values:** media roots, allowed MCP origins, Apple app IDs, Android fingerprints, and extra WebAuthn origins accept lists. Keep each item in the format shown in the reference.
- **Addresses:** callback and issuer values are origins, with a scheme, host, and optional port. Do not append `/api`, credentials, or a query. The MCP issuer requires HTTPS.
- **Filesystem paths:** media roots are absolute paths visible to Cantinarr. A Docker host path is useful only after it is mounted into the container. The filesystem root `/` is not accepted as a media root.
- **Existing encrypted data:** preserve the original encryption key. Supplying a new key does not migrate old secrets. See [backups](/install/backups/).
- **AI defaults:** environment provider/model values are fallbacks. They do not replace a saved selection, configure credentials, or grant anyone AI access. Use [AI settings](/admin/ai/) for those steps.
- **Container ownership:** `PUID` and `PGID` apply to the image entrypoint. They do not change an identity already set by Compose `user:` or the platform.
- **Update lookup:** `CANTINARR_DISABLE_UPDATE_CHECK` treats `1`, `true`, `yes`, and `on` as enabled, ignoring case. It disables Cantinarr's lookup, not your platform's updates.

## Old names and unrelated variables

`CANTINARR_PUBLIC_URL` still means the library-manager callback address. Its newer name, `CANTINARR_ARR_CALLBACK_URL`, wins when both have nonempty values. `CANTINARR_ANDROID_CERT_SHA256` remains the older spelling of the signing-fingerprint setting; the plural name takes precedence when nonempty.

Kubernetes may inject `CANTINARR_SERVICE_HOST`, `CANTINARR_SERVICE_PORT`, and a `tcp://...` value for `CANTINARR_PORT`. These are service-discovery values. Cantinarr recognizes that service-link form and keeps its normal 8585 listen port; supply a numeric port to change it deliberately.

There is no supported `CANTINARR_DB_PATH` override, and current startup does not read `CANTINARR_ADMIN_PASSWORD`. Use the setup screen for the first administrator and preserve the `/config` data location.

Build arguments and test-only variables are covered under [development](/contributing/development/#build-and-test-variables). Putting a build argument in a running container's environment does not rebuild the app.
