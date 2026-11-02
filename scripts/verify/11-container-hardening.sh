#!/usr/bin/env bash
# verify: engines run non-root, cap-dropped, and read-only where tolerated
# tags: security hardening
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# design.md D-18 and D-19.
#
# D-19: s6-overlay must start as root to run its init stages, then drop to the
# engine user. So the container user IS root, and the honest assertion is that
# no *engine* process runs as uid 0 -- not that the container is rootless.
#
# D-18: read_only rootfs needs an explicit tmpfs list per engine. If an engine
# cannot tolerate it, that belongs in the decision log, not in a silently
# dropped hardening claim.

img="$(image_name pg)"
need_image "$img"

name="mdb-verify-harden-$$"
vol="mdb-verify-harden-vol-$$"
track_container "$name"
track_volume "$vol"

mdb_ctr volume create "$vol" >/dev/null

# This is the explicit tmpfs list D-18 promised. read_only applies to the ROOT
# filesystem, so everything the engine legitimately writes to needs somewhere
# real to live:
#   PGDATA                      -> a volume; data is not ephemeral
#   /run                        -> s6 state, and our generated conf and certs
#   /var/run/postgresql         -> the unix socket
#   /tmp                        -> query temp files
#   /docker-entrypoint-initdb.d -> the provisioning scripts we generate for
#                                  the official entrypoint to consume
#
# Two of those need `exec`, because Docker mounts --tmpfs noexec by default:
# s6 executes /run/s6/basedir/bin/init, and the official entrypoint executes
# the initdb.d scripts. Without it the container dies at stage0 with
# "Permission denied" and exit 126, which reads like a file-ownership problem
# and is not one.
mdb_ctr run -d --name "$name" \
    -e POSTGRES_PASSWORD="${TW_VERIFY}" \
    --read-only \
    --cap-drop ALL --cap-add CHOWN --cap-add SETUID --cap-add SETGID \
    --cap-add DAC_OVERRIDE --cap-add FOWNER \
    --security-opt no-new-privileges \
    -v "$vol:/var/lib/postgresql/data" \
    --tmpfs /run:rw,exec,nosuid,size=64m \
    --tmpfs /tmp:rw,nosuid,size=64m \
    --tmpfs /var/run/postgresql:rw,nosuid,size=8m \
    --tmpfs /docker-entrypoint-initdb.d:rw,exec,nosuid,size=8m \
    "$img" >/dev/null || vfail "container failed to start read-only with dropped capabilities"

wait_ready 60 "postgres to accept connections read-only" mdb_ctr exec "$name" pg_isready -U postgres
vinfo "engine boots with --read-only, cap-drop ALL, and no-new-privileges"

# No engine process may run as root.
root_procs="$(mdb_ctr exec "$name" ps -eo user,comm 2>/dev/null | awk '$1=="root" && $2 ~ /postgres/ {print $2}' | sort -u || true)"
[[ -z "$root_procs" ]] || vfail "postgres processes running as root: $root_procs"
vinfo "no postgres process runs as uid 0"

# The data directory must not be world-readable.
perms="$(mdb_ctr exec "$name" stat -c '%a' /var/lib/postgresql/data 2>/dev/null || echo '?')"
case "$perms" in
    700|750) vinfo "PGDATA permissions $perms" ;;
    *) vfail "PGDATA permissions are $perms; PostgreSQL requires 700 or 750" ;;
esac
