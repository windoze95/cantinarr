# Updating Cantinarr

Cantinarr ships as a Docker image at `ghcr.io/windoze95/cantinarr`. Updating means
pulling the newer image and recreating the container — the exact steps depend on how
you run it. Admins see an in-app banner when a newer release is available; it links
here, or to your own management portal if you've set one
(**Settings → Admin → Update Portal**).

Your data lives in the `/config` volume, so recreating the container keeps your
database and settings.

## Docker Compose

```sh
docker compose pull
docker compose up -d
```

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
does the pull-and-recreate for you. Point the in-app banner straight at that page by
setting **Settings → Admin → Update Portal** to its URL (e.g. `http://tower.local/Docker`).

## Pinning a version

Prefer to control upgrades explicitly? Pin a tag instead of `latest` — e.g.
`ghcr.io/windoze95/cantinarr:1.4.0` — and bump it when you choose. The banner compares
your running version against the latest published GitHub release.

## Turning the check off

The update check is best-effort and only runs on tagged release builds. To disable it
entirely, set `CANTINARR_DISABLE_UPDATE_CHECK=1` in the container's environment.
