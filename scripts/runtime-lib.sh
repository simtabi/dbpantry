#!/usr/bin/env bash
# shellcheck shell=bash
#
# The container runtime layer (design.md D-60).
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
    IFS=$' \t\n' read -r -a _c <<< "$MDB_CTR_CMD"
    "${_c[@]}" "$@"
}

MDB_RT_ROOT="${MDB_RT_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." 2>/dev/null && pwd)}"
export MDB_RT_ROOT

# shellcheck source=env-lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/env-lib.sh"

# COMPOSE_FILE and COMPOSE_PROFILES, as Compose v2 reads them: the environment
# first, then the instance's .env (mdb_env_default_file). Prints the value or nothing.
_mdb_rt_compose_var() {
    local key="$1" val
    eval "val=\${$key:-}"
    local envf
    envf="$(mdb_env_default_file)"
    if [[ -z "$val" && -f "$envf" ]]; then
        val="$(mdb_env_get "$key" "$envf")"
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
    IFS=$' \t\n' read -r -a _c <<< "$MDB_COMPOSE_CMD"
    if mdb_home_separate; then
        _mdb_compose_home "$@"
        return
    fi
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

# A compose file named relative to the kit, as an absolute path: the instance's
# copy first (the generated compose.engines.yml, compose.proxy.yml and
# compose.prod.engines.yml live there), then the kit's (docker-compose.yml and
# the overlays). An absolute path is returned unchanged.
#
# A generated file is always the instance's, present or not: a checkout that
# is also its own instance holds generated files for that instance, and a
# separate home that has not generated its own yet must not read them (they
# carry the other instance's ports). A missing one is left out of the list.
_MDB_GENERATED_COMPOSE=" compose.engines.yml compose.proxy.yml compose.prod.engines.yml "
_mdb_compose_file() {
    local rel="${1#./}"
    case "$1" in
        /*) printf '%s\n' "$1" ;;
        *)  if [[ -f "$MDB_HOME/$rel" || "$_MDB_GENERATED_COMPOSE" == *" $rel "* ]]; then printf '%s\n' "$MDB_HOME/$rel"
            else printf '%s\n' "$MDB_RT_ROOT/$rel"; fi ;;
    esac
}

# mdb_compose when the instance lives apart from the kit (design.md D-84).
#
# The project directory stays the kit, so build contexts (./images) and the
# config files compose mounts from it resolve as in a checkout; Compose
# resolves every relative path against the project directory, which is the
# first -f file's directory. What moves is the instance: its .env reaches
# Compose through an absolute --env-file (a relative one resolves against the
# caller's directory, docs.docker.com/compose/env-file/), and every compose
# file is passed as an absolute -f, found home-first, because a relative
# COMPOSE_FILE entry would resolve against the kit, where the generated files
# are not. Bind mounts of the instance read ${MDB_HOME:-.}, exported by
# env-lib.sh.
#
# Only the global options before the subcommand are rewritten: after it, -f is
# a subcommand flag (`logs -f` follows the log) and is passed through as is.
_mdb_compose_home() {
    local -a _hc_opts=() _hc_files=() _hc_rest=()
    local a has_f=0 has_p=0 f p profiles
    while (( $# )); do
        a="$1"
        case "$a" in
            -f|--file)          shift; _hc_files+=("$(_mdb_compose_file "${1:-}")"); has_f=1 ;;
            --file=*)           _hc_files+=("$(_mdb_compose_file "${a#--file=}")"); has_f=1 ;;
            --profile)          _hc_opts+=("$a" "${2:-}"); shift; has_p=1 ;;
            --profile=*)        _hc_opts+=("$a"); has_p=1 ;;
            -p|--project-name|--env-file|--project-directory|--ansi|--progress|--parallel)
                                _hc_opts+=("$a" "${2:-}"); shift ;;
            -*)                 _hc_opts+=("$a") ;;
            *)                  break ;;
        esac
        shift
    done
    _hc_rest=("$@")
    if (( ! has_f )); then
        for f in $(_mdb_rt_compose_var COMPOSE_FILE | tr ':' ' '); do
            f="$(_mdb_compose_file "$f")"
            [[ -f "$f" ]] && _hc_files+=("$f")
        done
    fi
    if (( ! has_p )) && [[ "$MDB_COMPOSE_FLAVOR" != compose-v2 ]]; then
        profiles="$(_mdb_rt_compose_var COMPOSE_PROFILES)"
        for p in ${profiles//,/ }; do _hc_opts+=(--profile "$p"); done
    fi
    local -a _hc_pre=(--project-directory "$MDB_RT_ROOT")
    [[ -f "$MDB_HOME/.env" ]] && _hc_pre+=(--env-file "$MDB_HOME/.env")
    for f in ${_hc_files[@]+"${_hc_files[@]}"}; do _hc_pre+=(-f "$f"); done
    local -a _c
    IFS=$' \t\n' read -r -a _c <<< "$MDB_COMPOSE_CMD"
    # The backup sidecars bind-mount ${MDB_BACKUP_DIR:-./backups}. A relative
    # value (.env.example ships `backups`) would resolve against the kit, so it
    # is handed to Compose absolute, resolved against the home as
    # scripts/backup resolves it.
    MDB_BACKUP_DIR="$(mdb_backup_dir "$MDB_HOME" "$MDB_HOME/.env")" \
        "${_c[@]}" "${_hc_pre[@]}" ${_hc_opts[@]+"${_hc_opts[@]}"} ${_hc_rest[@]+"${_hc_rest[@]}"}
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
# <project>_<volume>, but that prefix is shared by a project whose name
# extends this one (mdb and mdb_dev), so a volume Compose labelled with
# another project is left out; one with no label (created by hand, or by
# an implementation that does not label) is kept, as before. The name is
# matched as text, never as a pattern.
mdb_project_volumes() {
    local project="${1:-$(mdb_project_name)}" v owner
    mdb_ctr volume ls --format '{{.Name}}' 2>/dev/null \
        | awk -v p="${project}_" 'index($0, p) == 1' | sort \
        | while IFS= read -r v; do
            owner="$(mdb_ctr volume inspect -f '{{index .Labels "com.docker.compose.project"}}' "$v" 2>/dev/null || true)"
            [[ -z "$owner" || "$owner" == "$project" ]] && printf '%s\n' "$v"
        done
    return 0
}

# MDB_CADDY_CONFIG_HASH: a checksum of every file Caddy reads once, at start
# -- its Caddyfile, the layer-4 routes and the web CA (design.md D-112) --
# which docker-compose.yml puts on the caddy service as a label. Compose
# recreates a container when its definition changes, never when a
# bind-mounted file does, and Caddy runs with its admin API off, so without
# this a re-rendered Caddyfile or a renewed web CA was never served. Exported
# by each Compose backend's up; cksum is in every POSIX system.
mdb_export_caddy_config_hash() {
    local home f files=()
    home="$(mdb_instance_dir)"
    for f in config/caddy/Caddyfile config/caddy/layer4.caddy \
             certs/web/root.crt certs/web/intermediate.crt certs/web/intermediate.key; do
        [[ -f "$home/$f" ]] && files+=("$home/$f")
    done
    if (( ${#files[@]} )); then
        MDB_CADDY_CONFIG_HASH="$(cat "${files[@]}" | cksum | awk '{print $1 "-" $2}')"
    else
        MDB_CADDY_CONFIG_HASH=""
    fi
    export MDB_CADDY_CONFIG_HASH
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
    _mdb_compose_home _mdb_compose_file \
    2>/dev/null || true
