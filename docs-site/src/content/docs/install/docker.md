---
title: Docker installation
description: Install with Compose or docker run, choose a data location, and set file permissions deliberately.
sidebar:
  order: 1
---

The official image is `ghcr.io/windoze95/cantinarr`. Stable and development images support AMD64 and ARM64. The web app and helper runtimes are included in the image.

## Docker Compose

Create a folder for your deployment and save the following as `compose.yaml`:

```yaml
services:
  cantinarr:
    image: ghcr.io/windoze95/cantinarr:latest
    ports:
      - "8585:8585"
    volumes:
      - ./config:/config
    environment:
      CANTINARR_SERVER_NAME: "Our library"
      CANTINARR_PUSH_GATEWAY_URL: "https://push.cantinarr.com"
    restart: unless-stopped
```

The push gateway setting is optional. Remove it if you do not want phone push notifications. Everything else needed for first boot is included.

```sh
docker compose up -d
docker compose ps
docker compose logs --tail=100 cantinarr
```

The service should stay running. Open `http://YOUR-SERVER-IP:8585`, then follow [first setup](/start/quickstart/#create-your-administrator-account).

## docker run

From the folder where you want to keep the data:

```sh
docker run -d \
  --name cantinarr \
  --restart unless-stopped \
  -p 8585:8585 \
  -v "$(pwd)/config:/config" \
  ghcr.io/windoze95/cantinarr:latest
```

Keep a copy of your full command, including later mounts and environment variables. You will need the same settings when recreating the container.

## Ports and data

| Setting | Meaning |
| --- | --- |
| `8585:8585` | Host port on the left, container port on the right |
| `./config:/config` | Data folder on the host, required persistent location inside the container |
| `restart: unless-stopped` | Start again after a crash or host restart, unless you stopped it deliberately |

If port 8585 is already used, change the left side, for example `8586:8585`. Then browse to port 8586. You do not need to change `CANTINARR_PORT` for this.

Do not mount the same writable config folder into multiple running Cantinarr containers. The server uses SQLite and process-local state for active authorization and pairing flows.

## Run with a chosen user and group

The official container runs as root unless configured otherwise. To use a specific host user and group, add their numeric IDs:

```yaml
    environment:
      PUID: "1000"
      PGID: "1000"
```

Use IDs that are correct for your host. The entrypoint takes ownership of `/config` for that user when starting. It does not need write access to your media libraries.

If the container is already launched as a non-root user through Compose's `user:` setting or your platform, `PUID` and `PGID` do not change that identity. The chosen process user must already be able to write `/config`.

## Reach other containers

Containers can use each other's service names when they share a user-defined Docker network. Containers in unrelated Compose projects do not automatically share one. Add a deliberate shared network, or use the Docker host's address and each service's published port.

See [networking and addresses](/install/networking/) for examples, including services behind a VPN gateway.

## Optional media file access

You do not need to mount media for discovery or ordinary requesting. File mounts are for Cantinarr's optional completed-file downloads.

If you want that feature, mount the required library read-only, set `CANTINARR_MEDIA_ROOTS`, and add a path mapping in the relevant instance. All three are required. Follow [file downloads](/admin/file-downloads/).

## Choose an image channel

- `latest` follows stable releases.
- A numbered tag pins a particular release.
- `edge` follows successful main-branch builds and may include behavior newer than stable.

See [updates](/install/updates/) before changing channels or attempting to go back to an older version.
