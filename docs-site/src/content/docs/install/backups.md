---
title: Back up and restore
description: Preserve accounts, settings, requests, and the key needed to read your saved credentials.
sidebar:
  order: 6
---

Back up the **whole `/config` directory**, together with your deployment configuration. The database and encryption key belong together. Copying only `cantinarr.db` can leave a restored server unable to read its saved service credentials.

If you supply `CANTINARR_ENCRYPTION_KEY` yourself, preserve that exact value in your existing secret manager as well. Do not paste it into a support issue or commit it with your Compose file.

## What to keep

- Everything in the directory mounted at `/config`.
- Your Compose file or equivalent container configuration.
- The exact image tag or digest running when you made the backup.
- External environment files or secrets, stored securely.
- Custom certificate trust, network settings, and read-only media mount definitions.

Your media files live elsewhere and need their own backup policy. A Cantinarr backup does not back up Plex, Radarr, Sonarr, Chaptarr, Lidarr, or your downloads.

## Make a consistent Docker backup

The simplest reliable method is a short stop while copying the data. This avoids copying SQLite files while they are changing.

From your Compose directory, with the example `./config` mount:

```sh
docker compose stop cantinarr
tar -czf "cantinarr-config-$(date +%Y%m%d-%H%M%S).tar.gz" config
docker compose start cantinarr
```

Check that the archive was created successfully before moving on. If the copy fails, restart Cantinarr and fix the backup destination before trying again. Copy the completed archive to another device or your usual backup storage.

For a named Docker volume or a platform-managed dataset, use that platform's backup tools to capture the complete volume while Cantinarr is stopped. Do not substitute a path that is not actually mounted into this container.

## Restore onto the same or a new machine

1. Stop Cantinarr. Keep the existing data directory as a separate rollback copy.
2. Restore the archived config directory to the path your deployment mounts at `/config`.
3. Restore any externally supplied encryption key and other deployment secrets.
4. Start with the same Cantinarr version that created the backup.
5. Make sure the server process can write the restored directory.
6. Start the server and sign in.

Do not overlay a restore onto a running database. Do not run the old and restored containers at the same time with the same writable data volume.

## Verify the restore

Check these before calling the restore finished:

- Your existing users and settings are present.
- A saved instance passes its connection test without re-entering its secret.
- Existing requests and issues are visible.
- A device can connect using the intended address.
- Instant updates and notification tests work.
- Optional media downloads still resolve the mapped files.

If the host or address changed, review **External Address**, instance URLs, the arr callback URL, the MCP issuer, and media path mappings. A changed network often explains failures after an otherwise successful restore.

## Restore before downgrading

Newer versions can migrate the database. Replacing the binary or image with an older one is not a complete rollback. Use the older version with its matching pre-upgrade data backup, preserving the encryption key.

See [updating Cantinarr](/install/updates/) for the normal upgrade path.
