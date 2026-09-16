---
title: Set up Cantinarr
description: A complete first setup, from an empty folder to a working request.
sidebar:
  order: 2
---

This guide uses Docker Compose. If you use Unraid or another app catalog, start with [platform installation](/install/platforms/), then return to **Create your administrator account** below.

## Before you start

Have a machine that can run Docker, a folder for Cantinarr's data, and the address of a working Radarr, Sonarr, Chaptarr, or Lidarr instance. You can connect services later if you want to explore discovery first.

Cantinarr needs one persistent data folder and one HTTP port. Its web app is included. There is no separate database server to set up.

## Start the server

Create a folder named `cantinarr`. Save this as `compose.yaml` inside it:

```yaml title="compose.yaml"
services:
  cantinarr:
    image: ghcr.io/windoze95/cantinarr:latest
    ports:
      - "8585:8585"
    volumes:
      - ./config:/config
    restart: unless-stopped
```

Open a terminal in that folder and run:

```sh
docker compose up -d
```

Open `http://YOUR-SERVER-IP:8585` in a browser. Replace `YOUR-SERVER-IP` with your Docker machine's address. `localhost` works only when the browser is on that machine.

**You should see:** the first-run setup screen. If the page does not open, check [server connection problems](/troubleshooting/connections/).

:::caution[Keep the config folder]
The `config` folder holds your database and encryption key. Keep it when recreating or updating the container. Back it up as a pair. Without the original key, saved service credentials cannot be decrypted.
:::

## Create your administrator account

Follow the setup screen to create the first administrator. Choose credentials you can keep securely. Open **Settings** after signing in.

Discovery works with the built-in TMDB key. The setup checklist shows optional features you can configure or skip. Skipping a checklist item only removes its reminder; it does not grant access or disable a configured feature.

## Connect a library

1. Open **Settings > Add Instance**.
2. Choose the service type, then give it a name you will recognize.
3. Enter its base URL and credentials. Use an address the **Cantinarr server** can reach.
4. Test the connection and save.
5. For Radarr or Sonarr, choose the appropriate default library. For Chaptarr and Lidarr, grant access to each person who should use them.

On a shared Docker network, a Radarr address might be `http://radarr:7878`. On separate machines, use the reachable host address and published port. Do not use `localhost` to mean another container.

Follow the relevant setup guide for profiles, folders, and access:

- [Movies and TV](/integrations/radarr-sonarr/)
- [Books](/integrations/guides/books/)
- [Music](/integrations/guides/music/)

**You should see:** a successful connection result and the service in Settings. A successful connection proves the API is reachable. It does not prove the service has working sources, disk access, or a download client.

## Check instant updates

Cantinarr tries to install its authenticated webhook when you add a library manager. This lets imports and other changes reach the app promptly.

Open the saved instance and inspect **Instant updates**. If setup failed, set an address the library manager can reach, then choose **Configure instant updates**. See [instant updates](/integrations/instant-updates/).

## Make one request

Choose a title your connected library does not already contain. Request it, complete any approval, and verify that the exact title appears in the selected library manager.

If a suitable release exists, follow the download through import. If it has not been released yet, **Requested** is a valid result. Cantinarr cannot make a release available sooner.

## Connect your people

Open **Settings > Users**, create a user, review their permissions and library access, and generate a connect link. A connect link signs one device in once. Passwords, passkeys, or configured single sign-on handle later sign-ins.

Before inviting anyone outside your home, configure a reachable HTTPS address and set **Settings > External Address**. See [remote access](/install/remote-access/) and [accounts and invitations](/admin/users/).

## Finish with the things you use

- [Connect Plex, Jellyfin, or Emby](/integrations/media-servers/) so people can open available titles.
- [Enable phone notifications](/integrations/push/) when requests are ready.
- [Set approvals and request allowances](/admin/request-policy/).
- [Create a backup](/install/backups/) before you depend on the server.

You can stop here and use Cantinarr. The rest of the setup checklist is available whenever you need it.
