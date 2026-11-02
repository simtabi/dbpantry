# shellcheck shell=bash
# Runtime adapter: Podman, native or through podman machine (see README.md).

mdb_runtime_register podman

# Podman prints a banner on every `podman compose` call that delegates to an
# external provider. It goes to stderr, but checks capture stderr for their
# failure messages and the banner drowns the real error.
export PODMAN_COMPOSE_WARNING_LOGS="${PODMAN_COMPOSE_WARNING_LOGS:-false}"

_mdb_rt_podman_present()   { command -v podman >/dev/null 2>&1; }
_mdb_rt_podman_reachable() { podman info >/dev/null 2>&1; }

# `podman compose` delegates to docker-compose (compose-v2) or podman-compose.
_mdb_rt_podman_compose() {
    local out
    out="$(podman compose version 2>&1 || true)"
    case "$out" in
        *podman-compose*) printf 'podman-compose podman compose\n'; return 0 ;;
        *'ompose version'*|*'ompose'*v[2-9]*) printf 'compose-v2 podman compose\n'; return 0 ;;
    esac
    if command -v podman-compose >/dev/null 2>&1; then
        printf 'podman-compose podman-compose\n'; return 0
    fi
    return 1
}

# On a podman machine (macOS, Windows), `podman compose` hands its provider the
# machine's forwarded API socket, whatever CONTAINER_CONNECTION or --connection
# says, and that socket is rootful when the machine is. The CLI, meanwhile,
# follows the connection. A rootless connection on a rootful machine therefore
# splits the stack across two container stores: compose creates in one, mdb_ctr
# looks in the other, and every health check fails. Found by testing; refuse it.
_mdb_rt_podman_split_store() {
    local machine_rootful cli_rootless
    machine_rootful="$(podman machine inspect --format '{{.Rootful}}' 2>/dev/null | head -1 || true)"
    [[ -n "$machine_rootful" ]] || return 1     # native Linux: no machine, no split
    cli_rootless="$(podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null || true)"
    [[ -n "$cli_rootless" ]] || return 1
    [[ "$machine_rootful" == true && "$cli_rootless" == true ]] && return 0
    [[ "$machine_rootful" == false && "$cli_rootless" == false ]] && return 0
    return 1
}

# $1 = the resolved "<flavor> <command>".
_mdb_rt_podman_validate() {
    [[ "$1" == "compose-v2 podman compose" ]] || return 0
    _mdb_rt_podman_split_store || return 0
    _mdb_rt_err "the podman connection (${CONTAINER_CONNECTION:-default}) and the podman machine disagree about rootless:
          \`podman compose\` always uses the machine's API socket, so containers would be
          created in one store and looked for in the other. Use the machine's own mode
          (unset CONTAINER_CONNECTION), switch it (podman machine set --rootful=false),
          or use podman-compose, which follows the connection (MDB_COMPOSE=podman-compose).
          See docs/guides/runtime-podman.md."
    return 1
}

# Podman builds OCI-format images by default, and the OCI image format has no
# HEALTHCHECK: buildah drops it, and every engine's `compose up --wait` then
# fails or, worse, reports a container with no healthcheck as ready. Docker
# format keeps it. Verified on Podman 6.1: plain `podman build` -> no
# healthcheck; `--format docker` -> kept.
_mdb_rt_podman_build() { mdb_ctr build --format docker "$@"; }

_mdb_rt_podman_rootless() {
    [[ "$(podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null || true)" == true ]]
}

_mdb_rt_podman_provider() {
    if podman info --format '{{.Host.ServiceIsRemote}}' 2>/dev/null | grep -q true; then
        printf 'podman-machine\n'
    else
        printf 'podman\n'
    fi
}
