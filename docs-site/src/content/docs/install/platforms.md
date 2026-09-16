---
title: Unraid and app catalogs
description: Install through your platform while keeping the same port, data, and connection requirements.
sidebar:
  order: 2
---

App catalogs package the same Cantinarr server. Their forms and update schedules differ, but the important values stay the same: HTTP port 8585 inside the container, persistent data at `/config`, and an address that your devices can reach.

## Unraid

1. Open **Apps** and search for **Cantinarr**.
2. Review the appdata location. The template normally uses `/mnt/user/appdata/cantinarr`.
3. Review the published port and apply the template.
4. Open the container's WebUI and create the administrator account.

The template is maintained in [cantinarr-unraid](https://github.com/windoze95/cantinarr-unraid). If the listing is not available in your catalog, install its template manually:

```sh
curl --fail --location \
  https://raw.githubusercontent.com/windoze95/cantinarr-unraid/main/templates/cantinarr.xml \
  --output /boot/config/plugins/dockerMan/templates-user/my-cantinarr.xml
```

Then open **Docker > Add Container** and select Cantinarr from the template list.

### Understand the advanced fields

The template's callback or legacy **Public URL** field is the address your library managers use for instant updates. It is not necessarily the address your family opens. Set the latter in **Settings > External Address** inside Cantinarr.

The optional read-only media mount and **Media roots** value support file downloads. Leave them unused unless you want that feature, then finish with the instance's path mappings.

### Services using another container's network

An Unraid container using the **Container** network type shares another container's network stack. It has no separate reachable service address. Enter the gateway container's address and the port it publishes. See [VPN gateway connections](/install/networking/#services-behind-a-vpn-gateway).

## Portainer, CasaOS, BigBear, TrueNAS, and Umbrel

Use your platform's current application entry when available. Review its image version and persistent volume before applying an update.

| Catalog | Maintained entry | Update behavior |
| --- | --- | --- |
| Portainer templates | [lissy93/portainer-templates](https://github.com/lissy93/portainer-templates) | Tracks the stable image channel |
| CasaOS | [CasaOS-AppStore](https://github.com/IceWhaleTech/CasaOS-AppStore) | Tracks the stable image channel |
| BigBear | [big-bear-universal-apps](https://github.com/bigbeartechworld/big-bear-universal-apps/tree/main/apps/cantinarr) | Uses a pinned version updated by its catalog |
| TrueNAS | [truenas/apps](https://github.com/truenas/apps) | Uses a pinned version updated by its catalog |
| Umbrel | [umbrel-apps](https://github.com/getumbrel/umbrel-apps) | Uses a version and image digest updated through a catalog PR |

A source entry or submitted PR does not guarantee that a platform already offers the app or its newest version. If it is absent, use the [Docker installation](/install/docker/) where the platform supports custom containers, or wait for that catalog's publication.

On platforms that run containers as a non-root user, make sure the configured user can write the data volume. Do not make the whole media library writable to solve a config-directory permission error.

## After any platform installation

Complete [first setup](/start/quickstart/#create-your-administrator-account), then [back up the data folder](/install/backups/). Platform installation does not configure Radarr, Sonarr, download clients, or playback accounts for you.
