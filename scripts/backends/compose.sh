#!/usr/bin/env bash
# shellcheck shell=bash
# Backend: Compose on a container runtime (Docker, Podman, nerdctl, and the
# Docker-API products, including Apple container through socktainer). The
# runtime is chosen by runtime-lib.sh (CONTAINER_RUNTIME, D-60).

_mdb_be_compose_up() {
    # Auto-heal overlapping MDB_NETWORK_SUBNET before compose creates the net
    # (Docker: "Pool overlaps with other one on this address space", D-103).
    # shellcheck source=../network-lib.sh
    . "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/network-lib.sh"
    mdb_network_ensure_subnet || return 1
    mdb_export_caddy_config_hash
    mdb_compose_up_wait "$@"
}
_mdb_be_compose_down()   { mdb_compose --profile '*' "$@" down --remove-orphans; }
_mdb_be_compose_status() { mdb_compose --profile '*' "$@" ps; }
_mdb_be_compose_running() { mdb_running_services "$@"; }
_mdb_be_compose_logs()   { mdb_compose --profile '*' logs "$@"; }
# Restart in place: the container keeps its volumes and configuration; a
# changed compose file needs `up`, which recreates it.
_mdb_be_compose_restart() { mdb_compose --profile '*' restart "$@"; }
# Start / stop existing services without recreating them (Compose start/stop).
_mdb_be_compose_start() { mdb_compose --profile '*' start "$@"; }
_mdb_be_compose_stop()  { mdb_compose --profile '*' stop "$@"; }

_mdb_be_compose_exec() {
    _mdb_svc_args "$@" || return; shift "$_MDB_SVC_SHIFT"
    if [[ -n "$_MDB_SVC_USER" ]]; then
        mdb_compose exec -T -u "$_MDB_SVC_USER" "$_MDB_SVC" "$@"
    else
        mdb_compose exec -T "$_MDB_SVC" "$@"
    fi
}

_mdb_be_compose_exec_tty() {
    _mdb_svc_args "$@" || return; shift "$_MDB_SVC_SHIFT"
    local t=()
    [[ -t 0 && -t 1 ]] || t=(-T)
    if [[ -n "$_MDB_SVC_USER" ]]; then t+=(-u "$_MDB_SVC_USER"); fi
    mdb_compose exec ${t[@]+"${t[@]}"} "$_MDB_SVC" "$@"
}

_mdb_be_compose_health() { mdb_service_health "$@"; }

_mdb_be_compose_cp() {
    local src="$1" dest="$2" id
    id="$(mdb_compose ps -q "${src%%:*}" 2>/dev/null | head -1)"
    [[ -n "$id" ]] || { _mdb_rt_err "service ${src%%:*} is not running"; return 1; }
    mdb_ctr cp "$id:${src#*:}" "$dest"
}

_mdb_be_compose_volumes()      { mdb_project_volumes "$@"; }
_mdb_be_compose_volume_rm()    { mdb_ctr volume rm "$1"; }
_mdb_be_compose_image_load()   { :; }   # a local build is already in the runtime's store
_mdb_be_compose_secrets_sync() { :; }   # secrets/ is bind-mounted read-only

_mdb_be_compose_info() {
    printf 'backend:   compose (MDB_BACKEND=%s)\n' "$MDB_BACKEND"
}

mdb_backend_register compose
