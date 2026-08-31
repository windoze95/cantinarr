# Updating Cantinarr

Cantinarr ships as a Docker image at `ghcr.io/windoze95/cantinarr`. Updating means
pulling the newer image and recreating the container — the exact steps depend on how
you run it. The app does not announce new releases: watch
[Releases](https://github.com/windoze95/cantinarr/releases) (or let your container
platform's own update check tell you) and update when you choose.

Your data lives in the `/config` volume, so recreating the container keeps your
database and settings.

## Docker Compose

```sh
docker compose pull
docker compose up -d
```

(If your checkout predates the image-based compose file, run `git pull` once first so
`docker compose pull` has a published image to pull instead of a local build.)

## docker run

```sh
docker pull ghcr.io/windoze95/cantinarr:latest
docker stop cantinarr && docker rm cantinarr
# re-run your original `docker run …` command (same flags and volumes)
```

## Automatic updates (Watchtower)

[Watchtower](https://containrrr.dev/watchtower/) pulls and recreates the container
for you on a schedule:

```sh
docker run -d --name watchtower \
  -v /var/run/docker.sock:/var/run/docker.sock \
  containrrr/watchtower cantinarr
```

## Your platform's update action

If you run Cantinarr through a NAS or container UI (Unraid, Portainer, TrueNAS,
Dockge, …), use that platform's own "check for updates" / "apply update" action — it
does the pull-and-recreate for you. Point Cantinarr's version warnings straight at that
page by setting **Settings → Admin → Update Portal** to its URL (e.g.
`http://tower.local/Docker`).

## Pinning a version

Prefer to control upgrades explicitly? Pin a tag instead of `latest` — e.g.
`ghcr.io/windoze95/cantinarr:1.4.0` — and bump it when you choose.

## Version compatibility

The server and the mobile apps each advertise the oldest counterpart version they
fully support. When a pairing falls below a floor — say a store-updated phone app
against a server that hasn't been updated across a breaking change — the app shows a
warning banner: "update the server" to admins, "update this app" to everyone. These
warnings never block anything, and the floors only move when a release actually
breaks compatibility (both are currently `0.0.0`, i.e. no floor). The web app is
served by the server itself, so it is always exactly in step and never warns about
itself.

## Turning the release check off

The server compares its own version against the latest published GitHub release for
`/api/admin/update-status`. Nothing in the app renders that comparison today, so it
never interrupts anyone. It is best-effort and only runs on builds stamped with a
comparable version (release images, and `latest` images published after the first
release). To stop the server contacting GitHub at all, set
`CANTINARR_DISABLE_UPDATE_CHECK=1` in the container's environment.
