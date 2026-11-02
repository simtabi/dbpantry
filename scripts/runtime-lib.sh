#!/usr/bin/env bash
# shellcheck shell=bash
#
# The container runtime layer (DESIGN.md D-60).
#
# Every script talks to the container engine through this file and never names
# a runtime CLI itself. That is the whole abstraction: one place decides which
# CLI and which Compose implementation to call, and everything else says
# mdb_ctr / mdb_compose.
#
# Selection, in precedence order:
#   CONTAINER_RUNTIME=<adapter>                force one (default: auto);
#                                              adapters live in scripts/runtimes/
#   MDB_COMPOSE="<command ...>"                force the Compose command too,
#                                              e.g. MDB_COMPOSE=podman-compose
#
# Runtimes are adapters (scripts/runtimes/NN-<name>.sh, interface documented in
# scripts/runtimes/README.md). The core below names none of them: auto picks the
# first adapter, in filename order, whose CLI is INSTALLED and whose engine is
# REACHABLE. Today that is docker, podman, nerdctl. Docker-API runtimes (Docker Desktop, OrbStack, Colima,
# Rancher Desktop in moby mode) are all "docker" here: they differ in how the
# daemon is provided, not in the CLI, so they need no separate code path.
#
# The result is exported (MDB_RUNTIME, MDB_CTR_CMD, MDB_COMPOSE_CMD,
# MDB_COMPOSE_FLAVOR) so child processes -- make -> script -> bash -c -- resolve
# once and agree, rather than probing again and possibly disagreeing.
#
# Written for bash 3.2 (macOS): no associative arrays, no ${x,,}, no mapfile.
# Deliberately no `set -e`: sourced libraries must not change caller options.

_mdb_rt_err() { printf 'dbpantry: %s\n' "$*" >&2; }

# --- adapter registry ---------------------------------------------------------
MDB_RUNTIME_ADAPTERS=""

# Called by each adapter file. Exports the adapter's functions so `bash -c`
# children (make -> script -> verify harness) can call them too.
mdb_runtime_register() {
    local name="$1" f
    case " $MDB_RUNTIME_ADAPTERS " in *" $name "*) ;; *) MDB_RUNTIME_ADAPTERS="${MDB_RUNTIME_ADAPTERS:+$MDB_RUNTIME_ADAPTERS }$name" ;; esac
    for f in $(compgen -A function "_mdb_rt_${name}_" 2>/dev/null); do export -f "${f?}"; done
}

# Call an adapter hook if the adapter defines it. Returns 127 if it doesn't.
_mdb_rt_hook() {
    local name="$1" hook="$2"; shift 2
    declare -F "_mdb_rt_${name}_${hook}" >/dev/null 2>&1 || return 127
    "_mdb_rt_${name}_${hook}" "$@"
}

# Built-in adapters first, then any in MDB_RUNTIME_ADAPTER_DIRS (colon-separated),
# for an adapter kept outside the repo or under test.
_mdb_rt_load_adapters() {
    local dir f dirs
    dirs="$(dirname "${BASH_SOURCE[0]}")/runtimes${MDB_RUNTIME_ADAPTER_DIRS:+:$MDB_RUNTIME_ADAPTER_DIRS}"
    for dir in ${dirs//:/ }; do
        for f in "$dir"/[0-9]*.sh; do
            [[ -f "$f" ]] || continue
            # shellcheck source=/dev/null
            . "$f"
        done
    done
    # Register again now the functions exist: the files call register first.
    for f in $MDB_RUNTIME_ADAPTERS; do mdb_runtime_register "$f"; done
    export MDB_RUNTIME_ADAPTERS
}
_mdb_rt_load_adapters

# Resolve the Compose command for a runtime. Prints "<flavor> <command...>".
#   compose-v2      the Compose Specification reference implementation
#                   (docker compose, docker-compose v2+, or podman compose
#                   delegating to it). Everything is supported.
#   podman-compose  the Python implementation: no --wait, partial ps --format.
#   nerdctl         nerdctl compose: no --wait.
# MDB_COMPOSE overrides the adapter.
_mdb_rt_resolve_compose() {
    local rt="$1"
    if [[ -n "${MDB_COMPOSE:-}" ]]; then
        case "$MDB_COMPOSE" in
            *podman-compose*) printf 'podman-compose %s\n' "$MDB_COMPOSE" ;;
            *nerdctl*)        printf 'nerdctl %s\n' "$MDB_COMPOSE" ;;
            *)                printf 'compose-v2 %s\n' "$MDB_COMPOSE" ;;
        esac
        return 0
    fi
    _mdb_rt_hook "$rt" compose && return 0
    _mdb_rt_err "no Compose implementation found for $rt (see docs/guides/runtimes.md)"
    return 1
}

# Resolve once and export. Idempotent; cheap after the first call.
mdb_runtime_init() {
    if [[ -n "${MDB_RUNTIME:-}" && -n "${MDB_CTR_CMD:-}" && -n "${MDB_COMPOSE_CMD:-}" ]]; then
        return 0
    fi
    local want="${CONTAINER_RUNTIME:-auto}" rt="" c installed="" resolved alias ctr
    case "$want" in
        auto|'')
            for c in $MDB_RUNTIME_ADAPTERS; do
                _mdb_rt_hook "$c" present || continue
                [[ -z "$installed" ]] && installed="$c"
                if _mdb_rt_hook "$c" reachable; then rt="$c"; break; fi
            done
            # Nothing answers: keep the first installed one so the error that
            # follows names a real CLI rather than "no runtime".
            rt="${rt:-$installed}"
            ;;
        *)
            case " $MDB_RUNTIME_ADAPTERS " in
                *" $want "*) rt="$want" ;;
                *) _mdb_rt_err "CONTAINER_RUNTIME=$want is not one of: auto $MDB_RUNTIME_ADAPTERS (adapters in scripts/runtimes/)"; return 1 ;;
            esac ;;
    esac
    if [[ -z "$rt" ]]; then
        _mdb_rt_err "no container runtime found; install Docker, Podman or nerdctl (docs/guides/runtimes.md)"
        return 1
    fi
    _mdb_rt_hook "$rt" present || { _mdb_rt_err "CONTAINER_RUNTIME=$rt but its CLI is not on PATH"; return 1; }
    alias="$(_mdb_rt_hook "$rt" alias 2>/dev/null || true)"
    if [[ -n "$alias" && "$alias" != "$rt" ]]; then
        case " $MDB_RUNTIME_ADAPTERS " in *" $alias "*) rt="$alias" ;; esac
    fi
    resolved="$(_mdb_rt_resolve_compose "$rt")" || return 1
    if declare -F "_mdb_rt_${rt}_validate" >/dev/null 2>&1; then
        "_mdb_rt_${rt}_validate" "$resolved" || return 1
    fi
    ctr="$(_mdb_rt_hook "$rt" ctr 2>/dev/null || true)"
    MDB_RUNTIME="$rt"
    MDB_CTR_CMD="${ctr:-$rt}"
    MDB_COMPOSE_FLAVOR="${resolved%% *}"
    MDB_COMPOSE_CMD="${resolved#* }"
    export MDB_RUNTIME MDB_CTR_CMD MDB_COMPOSE_FLAVOR MDB_COMPOSE_CMD
}

# The container CLI: mdb_ctr run|exec|volume|image|... with Docker CLI syntax,
# which Podman and nerdctl both implement for everything this repo uses.
mdb_ctr() {
    mdb_runtime_init || return 125
    local -a _c
    read -r -a _c <<< "$MDB_CTR_CMD"
    "${_c[@]}" "$@"
}

MDB_RT_ROOT="${MDB_RT_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." 2>/dev/null && pwd)}"
export MDB_RT_ROOT

# shellcheck source=env-lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/env-lib.sh"

# COMPOSE_FILE and COMPOSE_PROFILES, as Compose v2 reads them: the environment
# first, then the project's .env. Prints the value or nothing.
_mdb_rt_compose_var() {
    local key="$1" val
    eval "val=\${$key:-}"
    if [[ -z "$val" && -f "$MDB_RT_ROOT/.env" ]]; then
        val="$(mdb_env_get "$key" "$MDB_RT_ROOT/.env")"
    fi
    printf '%s' "$val"
}

# Compose, with the Compose Specification's CLI.
#
# The stack is assembled from several files through COMPOSE_FILE in .env, and
# the active profiles arrive in COMPOSE_PROFILES. Compose v2 reads both itself.
# podman-compose and nerdctl compose do not reliably read them from .env, so for
# those the same selection is passed explicitly as -f / --profile -- unless the
# caller already chose files or profiles, which then win, exactly as on v2.
mdb_compose() {
    mdb_runtime_init || return 125
    local -a _c _pre
    read -r -a _c <<< "$MDB_COMPOSE_CMD"
    if [[ "$MDB_COMPOSE_FLAVOR" != compose-v2 ]]; then
        local a has_f=0 has_p=0 files profiles f p
        for a in "$@"; do
            case "$a" in
                -f|--file|--file=*) has_f=1 ;;
                --profile|--profile=*) has_p=1 ;;
            esac
        done
        if (( ! has_f )); then
            files="$(_mdb_rt_compose_var COMPOSE_FILE)"
            for f in ${files//:/ }; do
                [[ -f "$MDB_RT_ROOT/$f" || -f "$f" ]] && _pre+=(-f "$f")
            done
        fi
        if (( ! has_p )); then
            profiles="$(_mdb_rt_compose_var COMPOSE_PROFILES)"
            for p in ${profiles//,/ }; do _pre+=(--profile "$p"); done
        fi
    fi
    "${_c[@]}" ${_pre[@]+"${_pre[@]}"} "$@"
}

# Is the runtime installed AND answering? The replacement for `docker info`.
mdb_runtime_ready() {
    mdb_runtime_init 2>/dev/null || return 1
    mdb_ctr info >/dev/null 2>&1
}

# Build an image, with whatever the adapter needs (Podman: --format docker, so
# HEALTHCHECK survives).
mdb_build() {
    mdb_runtime_init || return 125
    if declare -F "_mdb_rt_${MDB_RUNTIME}_build" >/dev/null 2>&1; then
        "_mdb_rt_${MDB_RUNTIME}_build" "$@"
    else
        mdb_ctr build "$@"
    fi
}

# Is the engine rootless? Rootless engines cannot publish ports below 1024 on
# Linux by default, and Podman's gvproxy cannot bind them to 127.0.0.1 on macOS.
mdb_runtime_rootless() {
    mdb_runtime_init 2>/dev/null || return 1
    _mdb_rt_hook "$MDB_RUNTIME" rootless
}

# Which product provides the engine, for messages and `scripts/runtime info`.
mdb_runtime_provider() {
    mdb_runtime_init 2>/dev/null || { printf 'none\n'; return 0; }
    _mdb_rt_hook "$MDB_RUNTIME" provider || printf '%s\n' "$MDB_RUNTIME"
}

# --- service state, the same on every runtime --------------------------------
#
# These read containers through `compose ps -q` and `inspect`, never through
# `compose ps --format '{{.Service}}'`: Go-template field names in `ps` are the
# one part of the CLI the implementations do not share (podman-compose passes
# the template to `podman ps`, which has no .Service), while `inspect` exposes
# the Compose labels and State.Health identically on Docker, Podman and nerdctl.

# Running services of the project, one per line. Extra args are passed to
# compose before `ps` (e.g. -f files, --profile '*').
mdb_running_services() {
    local ids
    ids="$(mdb_compose "$@" ps -q 2>/dev/null)" || return 0
    [[ -n "$ids" ]] || return 0
    # shellcheck disable=SC2086  # ids are whitespace-separated container ids
    mdb_ctr inspect -f '{{index .Config.Labels "com.docker.compose.service"}} {{.State.Status}}' $ids 2>/dev/null \
        | awk '$2=="running" {print $1}' | sort -u
}

mdb_service_running() {
    local svc="$1"; shift
    mdb_running_services "$@" | grep -qx "$svc"
}

# healthy | unhealthy | starting | none (no healthcheck) | absent
mdb_service_health() {
    local svc="$1" id; shift
    id="$(mdb_compose "$@" ps -q "$svc" 2>/dev/null | head -1)"
    [[ -n "$id" ]] || { printf 'absent\n'; return 0; }
    mdb_ctr inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$id" 2>/dev/null \
        || printf 'absent\n'
}

mdb_service_healthy() {
    [[ "$(mdb_service_health "$@")" == healthy ]]
}

# The Compose project name, resolved the way Compose resolves it: the
# environment, then COMPOSE_PROJECT_NAME in .env, then the default. A script
# run outside make must not fall back to "mdb" while .env names another
# project: that is another checkout's containers and volumes.
mdb_project_name() {
    local p
    p="$(_mdb_rt_compose_var COMPOSE_PROJECT_NAME)"
    printf '%s\n' "${p:-mdb}"
}

# Volumes of the Compose project. Every implementation names them
# <project>_<volume>.
mdb_project_volumes() {
    local project="${1:-$(mdb_project_name)}"
    mdb_ctr volume ls --format '{{.Name}}' 2>/dev/null | grep -E "^${project}_" | sort || true
}

# `compose up -d --wait` where the implementation has --wait, otherwise up -d
# and poll every started container's health the same way --wait does: until
# all are healthy (or running, when they have no healthcheck), or one is
# unhealthy/exited, or MDB_WAIT_TIMEOUT seconds pass.
mdb_compose_up_wait() {
    mdb_runtime_init || return 125
    if [[ "$MDB_COMPOSE_FLAVOR" == compose-v2 ]]; then
        mdb_compose "$@" up -d --wait
        return
    fi
    mdb_compose "$@" up -d || return
    local budget="${MDB_WAIT_TIMEOUT:-300}" i ids id st pending
    for (( i = 0; i < budget; i += 2 )); do
        ids="$(mdb_compose "$@" ps -q 2>/dev/null || true)"
        pending=0
        for id in $ids; do
            st="$(mdb_ctr inspect -f '{{.State.Status}}/{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$id" 2>/dev/null || echo gone/none)"
            case "$st" in
                running/healthy|running/none) ;;
                */unhealthy|exited/*|dead/*)
                    _mdb_rt_err "container $id is $st"; return 1 ;;
                *) pending=$(( pending + 1 )) ;;
            esac
        done
        (( pending == 0 )) && [[ -n "$ids" ]] && return 0
        sleep 2
    done
    _mdb_rt_err "timed out after ${budget}s waiting for services to become healthy"
    return 1
}

# Child `bash -c` strings in the verify harness call these by name.
export -f mdb_runtime_init mdb_runtime_register mdb_project_name mdb_ctr mdb_compose \
    mdb_runtime_ready mdb_build mdb_runtime_rootless mdb_runtime_provider mdb_compose_up_wait \
    mdb_running_services mdb_service_running mdb_service_health mdb_service_healthy \
    mdb_project_volumes _mdb_rt_compose_var _mdb_rt_err _mdb_rt_hook _mdb_rt_resolve_compose \
    2>/dev/null || true
