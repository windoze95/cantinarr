---
title: Updates and release channels
description: Know which channel you run, update without losing data, and check both app and server versions.
sidebar:
  order: 8
---

The server and phone apps update separately. **Settings > About** shows both versions. Updating the app on your phone does not update the server in your home.

## Choose a channel

| Image tag | What it follows | Use it when |
| --- | --- | --- |
| `latest` | Stable releases | You want the stable server channel |
| A numbered version | One specific stable release | You want to choose exactly when to update |
| `edge` | Successful builds from main | You want current development changes and accept that they may be newer than stable |
| `X.Y.Z-rc.<run>` | A frozen release candidate build | You are deliberately testing that candidate |
| `pr-N` | A pull request preview | You are testing that PR in a suitable test environment |

`X.Y.Z`, `<run>`, and `N` above describe tag formats. Select a real published tag from the project's release or image records.

These docs follow main. A page can describe a merged feature before it reaches `latest`. If your installation lacks a documented control, compare its version before changing permissions or rebuilding your setup.

## Update Docker Compose

Make a [backup](/install/backups/) first. In the directory containing your Compose file:

```sh
docker compose pull cantinarr
docker compose up -d cantinarr
docker compose ps
docker compose logs --tail=100 cantinarr
```

The same `/config` mount preserves your data. Do not delete it or replace it with a new empty volume.

If you pin a numbered tag, edit the image value to the version you intend to run before pulling. Pulling the same immutable tag does not select a newer version.

## Update a docker run installation

Pull the intended image, stop and remove the old container, then recreate it with your saved original command and the same config mount, network, ports, and environment. Removing the container is different from deleting its data directory.

Use your deployment's recorded settings. A shortened command that omits your encryption key, callback address, or library mounts can start a server with the wrong behavior.

## Update a platform installation

Use the platform's update action, then check **Settings > About**. Catalogs that pin a version may need a catalog update before they offer a new release.

For native Linux installations, stop the service, replace the server and bundled helper using the new verified release archive, retain `/config`, and restart. See [Linux binaries](/install/linux/).

## Update the phone app

Use TestFlight on iPhone and iPad, or Google Play's beta channel on Android. An uploaded build can still be processing or waiting for distribution before it appears on your device.

Version compatibility notices are advisory. They tell you when one side is older than the other side supports. Update the indicated component and check again. The app-wide update banner is not the server's update mechanism.

## Check the result

After an update, sign in, open a connected library, inspect an existing request, and test anything whose settings changed. Confirm instant updates and push delivery if those matter to your household.

If a problem starts after upgrading, record both versions and the symptom. Restore the pre-upgrade backup with its matching old version if a rollback is necessary. Do not assume an older image can safely read a newer database.

## Release checks

`CANTINARR_DISABLE_UPDATE_CHECK=1` disables the server's periodic GitHub release check. It does not stop Docker, your app catalog, TestFlight, or Google Play from offering updates.

Maintainers can find the exact publication procedure in the [release playbook](/contributing/generated/releases/).
