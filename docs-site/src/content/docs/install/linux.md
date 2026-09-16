---
title: Linux binaries
description: Run the server without Docker, preserve its data, and account for the optional helper runtimes.
sidebar:
  order: 3
---

Use the Linux archive attached to the [Cantinarr release](https://github.com/windoze95/cantinarr/releases) you want to install. Releases provide AMD64 and ARM64 archives with matching SHA-256 checksum files.

The archive contains the server with its web app, the pinned `codex-app-server` helper, and license notices. It is extracted from the published image build rather than built independently for the archive.

## Check and unpack

Download both the archive and its `.sha256` file into the same directory. This example uses AMD64; substitute the ARM64 filenames for an ARM64 machine:

```sh
sha256sum -c cantinarr-linux-amd64.tar.gz.sha256
tar -xzf cantinarr-linux-amd64.tar.gz
sudo install -m 0755 cantinarr codex-app-server /usr/local/bin/
```

Stop if the checksum does not match. Keep the included license notices with your installed distribution.

## Create a service account and data directory

For a new Debian or Ubuntu host using a dedicated account:

```sh
sudo useradd --system --home /config --shell /usr/sbin/nologin cantinarr
sudo install -d -o cantinarr -g cantinarr -m 0700 /config
```

If the account or data directory already exists, inspect it and preserve its contents. Do not replace an existing configuration or change ownership of another application's data.

The server uses `/config/cantinarr.db` and, by default, `/config/encryption.key`. It does not expose a general database-path environment setting.

## Run under systemd

Save the following as `/etc/systemd/system/cantinarr.service` on a systemd host:

```ini
[Unit]
Description=Cantinarr
After=network-online.target
Wants=network-online.target

[Service]
User=cantinarr
Group=cantinarr
WorkingDirectory=/config
ExecStart=/usr/local/bin/cantinarr
EnvironmentFile=-/etc/cantinarr.env
Restart=on-failure
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

Use `/etc/cantinarr.env` for the [environment variables](/reference/generated/environment/) you need. Protect that file if it contains secrets. Keep existing externally supplied encryption keys unchanged during migration.

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now cantinarr
sudo systemctl status cantinarr
sudo journalctl -u cantinarr -n 100 --no-pager
```

Open `http://YOUR-SERVER-IP:8585`, then complete [first setup](/start/quickstart/#create-your-administrator-account).

## Optional helpers

The bundled Codex helper supports OpenAI OAuth-backed AI. Its ephemeral state needs the configured Linux memory-backed runtime directory, normally `/dev/shm/cantinarr-codex`, owned by the server user with mode 0700 when pre-created.

Apple TV support uses a separate Python helper and its locked dependencies. The binary archive alone is not a complete native Apple TV setup. Follow the native installation section in [Apple TV setup](/integrations/guides/apple-tv/#server-requirements-and-troubleshooting).

## Updates and migration

Stop the service, back up the whole `/config` directory, verify and install the intended release archive, and restart. Keep the same process identity, secrets, and media permissions. Validate saved instance connections afterward.

Use the old version with its corresponding backup if you must roll back. See [backup and restore](/install/backups/).
