---
title: Access, credentials, and recovery
description: Keep the server recoverable and understand what revokes access to it.
sidebar:
  order: 12
---

Protect the server's administrator account, persistent data, and public sign-in address. Cantinarr keeps service credentials on the server and encrypts them at rest, but the server administrator still controls the machine and its data.

## Preserve the encryption key

Back up `/config` as a unit. If you provide `CANTINARR_ENCRYPTION_KEY` externally, preserve that exact value in your secret manager. A new random key cannot decrypt older stored service credentials.

Changing `CANTINARR_JWT_SECRET` is not a device-revocation procedure. Stable device sessions do not depend on that value. Use **Connected Devices** when you need to revoke a session.

## Use a stable secure origin

Use HTTPS for remote access and configured OIDC. Keep the intended hostname stable for browser sign-in and passkeys. Configure the exact MCP issuer when serving MCP behind a proxy.

The server can use HTTP to reach trusted local services. An HTTPS service needs a certificate the server trusts; a browser exception does not apply to the container.

## Review people and devices

Remove obsolete device sessions and review administrator roles. Keep an administrator's working local recovery password before requiring external SSO. Cantinarr protects the last such recovery administrator while the requirement is active.

Provider logout, group changes, or account disablement do not immediately end existing Cantinarr sessions. Revoke devices or identity links when immediate removal is needed.

## Grants have separate effects

Removing a Chaptarr or Lidarr grant removes that library access. Removing a media-server grant can disable a linked account or remove a Plex share. Unlinking Plex sign-in revokes that sign-in path while preserving separate library access.

Check the exact control and confirmation instead of assuming every form of “unlink” does the same thing.

## Keep file access narrow

Mount only the media directories needed for optional file downloads, and mount them read-only. Limit `CANTINARR_MEDIA_ROOTS` and each instance mapping. An ordinary request setup does not need host media mounted into Cantinarr at all.

## Share diagnostics carefully

Use small, relevant log excerpts. Never publish the database, encryption key, API keys, session tokens, connect links, or webhook URLs. AI tool debug mode records bounded metadata, not a license to expose raw credentials or private payloads.

See [backup and restore](/install/backups/), [sign-in recovery](/troubleshooting/sign-in/), and the [privacy policy](/reference/generated/privacy/).
