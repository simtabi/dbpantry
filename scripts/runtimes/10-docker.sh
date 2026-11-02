# shellcheck shell=bash
# Runtime adapter: Docker Engine and every Docker-API product (see README.md).
# Docker Desktop, OrbStack, Colima and Rancher Desktop (moby) differ in how the
# daemon is provided, not in the CLI, so they share this adapter. So does
# Apple's `container` tool on macOS 26, through socktainer, which serves the
# Docker API on top of it (docs/guides/runtime-apple-container.md).

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

# The Docker API endpoint the CLI talks to: DOCKER_HOST, else the context's.
_mdb_rt_docker_endpoint() {
    if [[ -n "${DOCKER_HOST:-}" ]]; then printf '%s\n' "$DOCKER_HOST"; return 0; fi
    docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true
}

# socktainer listens on <home>/.socktainer/container.sock and names itself in
# `docker version`. The endpoint test costs no daemon call, so it runs first.
_mdb_rt_docker_socktainer() {
    case "$(_mdb_rt_docker_endpoint)" in *"/.socktainer/"*) return 0 ;; esac
    [[ "${1:-}" == quick ]] && return 1
    docker version --format '{{range .Server.Components}}{{.Name}} {{end}}' 2>/dev/null | grep -qw socktainer
}

# Apple's container runs every container in its own VM, sized when it is
# created, 1 GiB unless Compose asks for more. The engines size their caches
# from MDB_<ENGINE>_MEM_LIMIT (PG's default 4g gives shared_buffers = 1GB), so
# without compose.apple.yml PG would start with its whole VM as shared buffers
# and be killed under load. Refuse rather than run it that way.
_mdb_rt_docker_validate() {
    _mdb_rt_docker_socktainer quick || return 0
    case ":$(_mdb_rt_compose_var COMPOSE_FILE):" in *":compose.apple.yml:"*) return 0 ;; esac
    _mdb_rt_err "the Docker API here is socktainer (Apple container), which gives each
          container a VM of 1 GiB unless told otherwise. Add compose.apple.yml to the end
          of COMPOSE_FILE in .env so every engine gets the memory it is tuned for:
              COMPOSE_FILE=...:compose.ha.yml:compose.apple.yml
          See docs/guides/runtime-apple-container.md."
    return 1
}

# socktainer builds with BuildKit's docker-container driver, which leaves the
# result in the build cache unless told to load it into the image store.
_mdb_rt_docker_build() {
    if _mdb_rt_docker_socktainer quick; then
        mdb_ctr build --load "$@"
    else
        mdb_ctr build "$@"
    fi
}

_mdb_rt_docker_rootless() {
    docker info --format '{{.SecurityOptions}}' 2>/dev/null | grep -q rootless
}

_mdb_rt_docker_provider() {
    local name os ctx
    name="$(docker info --format '{{.Name}}' 2>/dev/null || true)"
    os="$(docker info --format '{{.OperatingSystem}}' 2>/dev/null || true)"
    ctx="$(docker context show 2>/dev/null || true)"
    if _mdb_rt_docker_socktainer; then printf 'apple-container (socktainer)\n'; return 0; fi
    case "$name|$os|$ctx" in
        *rancher*|*Rancher*)     printf 'rancher-desktop\n' ;;
        *orbstack*|*OrbStack*)   printf 'orbstack\n' ;;
        *colima*)                printf 'colima\n' ;;
        *'Docker Desktop'*|*desktop-linux*) printf 'docker-desktop\n' ;;
        *podman*|*Podman*)       printf 'podman-docker-api\n' ;;
        *)                       printf 'docker-engine\n' ;;
    esac
}
