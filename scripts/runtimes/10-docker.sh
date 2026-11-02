# shellcheck shell=bash
# Runtime adapter: Docker Engine and every Docker-API product (see readme.md).
# Docker Desktop, OrbStack, Colima and Rancher Desktop (moby) differ in how the
# daemon is provided, not in the CLI, so they share this adapter.

mdb_runtime_register docker

_mdb_rt_docker_present()   { command -v docker >/dev/null 2>&1; }
_mdb_rt_docker_reachable() { docker info >/dev/null 2>&1; }

# podman-docker installs a `docker` that execs podman.
_mdb_rt_docker_alias() {
    if docker --version 2>/dev/null | grep -qi podman && command -v podman >/dev/null 2>&1; then
        printf 'podman\n'
    fi
}

_mdb_rt_docker_compose() {
    local out
    if docker compose version >/dev/null 2>&1; then
        printf 'compose-v2 docker compose\n'; return 0
    fi
    if command -v docker-compose >/dev/null 2>&1; then
        out="$(docker-compose version --short 2>/dev/null || true)"
        case "$out" in
            1.*|'') _mdb_rt_err "docker-compose v1 is not supported; install Compose v2"; return 1 ;;
            *) printf 'compose-v2 docker-compose\n'; return 0 ;;
        esac
    fi
    return 1
}

_mdb_rt_docker_rootless() {
    docker info --format '{{.SecurityOptions}}' 2>/dev/null | grep -q rootless
}

_mdb_rt_docker_provider() {
    local name os ctx
    name="$(docker info --format '{{.Name}}' 2>/dev/null || true)"
    os="$(docker info --format '{{.OperatingSystem}}' 2>/dev/null || true)"
    ctx="$(docker context show 2>/dev/null || true)"
    case "$name|$os|$ctx" in
        *rancher*|*Rancher*)     printf 'rancher-desktop\n' ;;
        *orbstack*|*OrbStack*)   printf 'orbstack\n' ;;
        *colima*)                printf 'colima\n' ;;
        *'Docker Desktop'*|*desktop-linux*) printf 'docker-desktop\n' ;;
        *podman*|*Podman*)       printf 'podman-docker-api\n' ;;
        *)                       printf 'docker-engine\n' ;;
    esac
}
