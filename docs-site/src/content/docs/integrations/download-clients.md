---
title: Download clients
description: Connect queue and history views, use each client's correct address, and avoid common credential traps.
sidebar:
  order: 2
---

Download-client connections let administrators inspect transfer activity, history, speeds, and supported actions in Cantinarr. The library manager still owns its connection to the client and the import of completed media.

Users can follow authorized downloads in the [Content view](/use/downloads/). A direct client connection is needed for administrator controls and unmatched jobs; library-tracked progress remains available without one.

## Connection details

| Service | Address to enter | Credential and common gotcha |
| --- | --- | --- |
| SABnzbd | Its reachable base URL | Use its API key; the hostname must be accepted by SABnzbd's `host_whitelist` |
| qBittorrent | Its WebUI base URL | Use the WebUI username and password; confirm the WebUI is enabled and reachable |
| NZBGet | Its reachable base URL | Use its configured control username and password |
| Transmission | `scheme://host:port` | Cantinarr appends `/transmission/rpc`; use configured RPC credentials |
| Deluge | Its WebUI address, normally port 8112 | Use the WebUI password; there is no username; Cantinarr appends `/json` |
| ruTorrent | Its WebUI base URL, including a configured base path | HTTP Basic credentials only if the web server requires them; Cantinarr uses the HTTPRPC plugin |

Ports above are examples or service defaults. Use the address and published port of your installation.

## Add and verify

1. Open **Settings > Add Instance** and choose the client.
2. Enter its reachable base URL and credentials.
3. Test and save.
4. Open its Downloads view and compare a known queue or history item with the client's own UI.

A healthy but empty queue is different from an unreadable queue. If Cantinarr reports a connection failure, do not infer that there are no downloads.

## SABnzbd hostname verification

If a Docker service name is rejected, add that name to SABnzbd's `host_whitelist` under **Config > Special**, or use the hostname SABnzbd is configured to accept. Keep the connection private to the intended network.

## ruTorrent initialization

Open ruTorrent itself at least once after setup so it can discover rTorrent's command names. Cantinarr uses `/plugins/httprpc/action.php` for XML-RPC and the erasedata plugin for supported file removal. Missing or uninitialized capabilities are reported rather than treated as a successful deletion.

## Clients behind a VPN gateway

Use the gateway's reachable address and the published service port when the client shares that gateway's network namespace. The client's own container name may have no usable address. See [networking](/install/networking/).

## Before removing a download

Read the specific action. Removing a queue record, removing downloaded files, blocklisting a release, and allowing a replacement search have different consequences.

When the problem belongs to an arr import, start from the arr queue and its diagnostic explanation. Deleting the client transfer first can remove evidence the library manager needs. See [download troubleshooting](/troubleshooting/downloads/).
