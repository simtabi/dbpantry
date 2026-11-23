#!/usr/bin/env bash
# verify: the binary finds the container runtime when PATH is only /usr/bin:/bin
# tags: fast structure runtime
# phase: 1
#
# Escaped bug (design.md D-88): a dashboard started from an agent or launchd
# inherited PATH=/usr/bin:/bin and reported "Services are unavailable" with
# exit status 125, while `docker` lived at /usr/local/bin (Docker Desktop).
# scriptEnv appends the well-known CLI directories; this check starts the binary
# with a thin PATH and asserts runtime name still resolves when the host has
# a CLI only outside /usr/bin:/bin. When the CLI is already in /usr/bin the
# thin PATH is enough on its own and the check still passes.

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_dbpantry

thin="/usr/bin:/bin"
# Prefer the real login PATH's docker when present, so we know where the host
# CLI lives before we strip PATH.
host_docker="$(command -v docker 2>/dev/null || true)"
host_podman="$(command -v podman 2>/dev/null || true)"
host_nerdctl="$(command -v nerdctl 2>/dev/null || true)"

if [[ -z "$host_docker$host_podman$host_nerdctl" ]]; then
    vinfo "no container CLI on this host; skip thin-PATH probe"
    exit 0
fi

# A CLI already under /usr/bin or /bin does not exercise the extras; still
# assert the binary answers with a thin PATH.
thin_has=0
PATH="$thin" command -v docker >/dev/null 2>&1 && thin_has=1
PATH="$thin" command -v podman >/dev/null 2>&1 && thin_has=1
PATH="$thin" command -v nerdctl >/dev/null 2>&1 && thin_has=1

got="$(env -i PATH="$thin" HOME="$HOME" TMPDIR="${TMPDIR:-/tmp}" \
    ${DOCKER_HOST:+DOCKER_HOST="$DOCKER_HOST"} \
    ${DOCKER_CONFIG:+DOCKER_CONFIG="$DOCKER_CONFIG"} \
    ${CONTAINER_RUNTIME:+CONTAINER_RUNTIME="$CONTAINER_RUNTIME"} \
    "$DBPANTRY_BIN" --home "${MDB_HOME:-$MDB_ROOT}" runtime name 2>&1)" || {
    vfail "dbpantry runtime name failed under PATH=$thin:
$(printf '%s\n' "$got" | sed 's/^/       /')"
}
case "$got" in
    docker|podman|nerdctl) ;;
    *) vfail "runtime name under thin PATH was '$got' (want docker|podman|nerdctl)" ;;
esac
if [[ "$thin_has" -eq 0 ]]; then
    vinfo "thin PATH had no CLI; binary extras found $got (host had ${host_docker:-${host_podman:-$host_nerdctl}})"
else
    vinfo "thin PATH already had a CLI; binary still answered $got"
fi
