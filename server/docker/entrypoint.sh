#!/bin/sh
# Container entrypoint: optionally run Cantinarr as a non-root user.
#
# Set PUID (and PGID, which defaults to PUID) to run the server as that user,
# the convention linuxserver-style images use and what Synology and Unraid
# stacks expect. On every start the entrypoint takes ownership of /config for
# that user, so the database and the encryption key the server writes there
# belong to the same uid/gid on the host, then drops privileges with su-exec
# before exec'ing the command.
#
# With PUID unset the container behaves exactly as it always has and runs as
# root. A container already started as a non-root user (compose `user:`,
# TrueNAS, a Kubernetes securityContext) is left alone: nothing to chown,
# nothing to drop. su-exec takes numeric ids, so no passwd or group entry is
# ever created and an id that already exists in the image (Unraid's PGID=100
# is Alpine's `users`) needs no special casing.
set -eu

log() { echo "cantinarr entrypoint: $*" >&2; }

is_uint() {
    case "$1" in
        '' | *[!0-9]*) return 1 ;;
        *) return 0 ;;
    esac
}

if [ "$(id -u)" -ne 0 ]; then
    exec "$@"
fi

if [ -z "${PUID:-}" ]; then
    if [ -n "${PGID:-}" ]; then
        log "PGID is set but PUID is not; running as root (set PUID to run as another user)"
    fi
    exec "$@"
fi

PGID="${PGID:-$PUID}"
if ! is_uint "$PUID" || ! is_uint "$PGID"; then
    log "PUID and PGID must be non-negative integers (got PUID=$PUID PGID=$PGID)"
    exit 1
fi

if [ "$PUID" -eq 0 ]; then
    exec "$@"
fi

if ! chown -R "$PUID:$PGID" /config; then
    log "could not take ownership of /config for $PUID:$PGID; continuing"
fi
log "running as $PUID:$PGID"
# su-exec resets HOME to / for a uid with no passwd entry, so HOME is set
# after the drop; env exec's the command, so the server is still PID 1.
exec su-exec "$PUID:$PGID" env HOME=/config "$@"
