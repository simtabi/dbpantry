#!/usr/bin/env bash
# shellcheck shell=bash
#
# The deployment backend layer (design.md D-62).
#
# Two axes decide how dbpantry runs:
#   MDB_BACKEND=compose|kubernetes|incus   how the stack is DEPLOYED (default
#                                          compose); backends live in
#                                          scripts/backends/<name>.sh
#   CONTAINER_RUNTIME=auto|docker|...      which container CLI the compose
#                                          backend drives (runtime-lib.sh, D-60)
#
# Stack operations go through service-level verbs, never through a container
# id, because only Compose runtimes have containers named after services:
#
#   mdb_be_up [args]           deploy and wait until every service is healthy
#   mdb_be_down [args]         stop; never deletes data
#   mdb_be_status [args]       what is running, for people
#   mdb_be_running             running services, one per line, for scripts
#   mdb_be_logs [args]         logs of the stack
#   mdb_svc_exec [-u USER] SVC CMD...    run CMD in service SVC, stdin attached
#   mdb_svc_exec_tty [-u USER] SVC CMD... the same with a terminal (psql, mysql shells)
#   mdb_svc_health SVC         healthy | unhealthy | starting | none | absent
#   mdb_svc_cp SVC:PATH DEST   copy a file out of a service
#   mdb_be_volumes             the stack's persistent volumes, one per line
#   mdb_be_volume_rm NAME      delete one of them (make destroy only)
#   mdb_be_image_load REF      make a locally built image visible to the backend
#   mdb_be_secrets_sync        publish secrets/ to the backend
#   mdb_be_info                one line per fact, for `scripts/runtime info`
#   mdb_be_forward             reach the services from this machine, until
#                              interrupted (backends without host ports)
#
# A backend file calls `mdb_backend_register <name>` and defines
# _mdb_be_<name>_<verb> for the verbs above (up down status running logs exec
# exec_tty health cp volumes volume_rm image_load secrets_sync info forward; up, down,
# running, exec, health and volumes are required). A verb a backend lacks fails with a
# message naming the backend, rather than falling through to Compose.
#
# Written for bash 3.2 (macOS). Deliberately no `set -e`.

_mdb_be_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=runtime-lib.sh
. "$_mdb_be_dir/runtime-lib.sh"
# shellcheck source=env-lib.sh
. "$_mdb_be_dir/env-lib.sh"

MDB_BACKENDS=""
export MDB_BACKEND_REQUIRED_VERBS="up down running exec health volumes"

mdb_backend_register() {
    local name="$1" f
    case " $MDB_BACKENDS " in *" $name "*) ;; *) MDB_BACKENDS="${MDB_BACKENDS:+$MDB_BACKENDS }$name" ;; esac
    for f in $(compgen -A function "_mdb_be_${name}_" 2>/dev/null); do export -f "${f?}"; done
}

_mdb_be_load() {
    local f dir
    local -a _mdb_be_dirs=()
    # Colon-separated, like PATH; set -u safe (verify sources this under set -u).
    [[ -n "${MDB_BACKEND_DIRS:-}" ]] && IFS=: read -r -a _mdb_be_dirs <<<"$MDB_BACKEND_DIRS"
    for dir in "$_mdb_be_dir/backends" ${_mdb_be_dirs[@]+"${_mdb_be_dirs[@]}"}; do
        for f in "$dir"/*.sh; do
            [[ -f "$f" ]] || continue
            # shellcheck source=/dev/null
            . "$f"
        done
    done
}

# MDB_BACKEND from the environment, else from .env, else compose.
mdb_backend_init() {
    [[ -n "${MDB_BACKEND_READY:-}" && "${MDB_BACKEND_READY}" == "${MDB_BACKEND:-}" ]] && return 0
    [[ -n "$MDB_BACKENDS" ]] || _mdb_be_load
    local want="${MDB_BACKEND:-}" root
    if [[ -z "$want" ]]; then
        root="${MDB_RT_ROOT:-$(cd "$_mdb_be_dir/.." && pwd)}"
        want="$(mdb_env_get MDB_BACKEND "$root/.env")"
    fi
    want="${want:-compose}"
    case " $MDB_BACKENDS " in
        *" $want "*) ;;
        *) _mdb_rt_err "MDB_BACKEND=$want is not one of: $MDB_BACKENDS (backends in scripts/backends/)"; return 2 ;;
    esac
    MDB_BACKEND="$want"; MDB_BACKEND_READY="$want"
    export MDB_BACKEND MDB_BACKEND_READY
}

# Dispatch a verb to the selected backend.
_mdb_be_call() {
    local verb="$1"; shift
    mdb_backend_init || return
    if ! declare -F "_mdb_be_${MDB_BACKEND}_${verb}" >/dev/null 2>&1; then
        _mdb_rt_err "the $MDB_BACKEND backend does not support '$verb'"
        return 64
    fi
    "_mdb_be_${MDB_BACKEND}_${verb}" "$@"
}

# A script that only makes sense on some backends says so up front, instead
# of failing half-way through with a Compose error.
mdb_backend_require() {
    mdb_backend_init || return
    local b
    for b in "$@"; do [[ "$MDB_BACKEND" == "$b" ]] && return 0; done
    _mdb_rt_err "$(basename "$0") needs MDB_BACKEND=$(printf '%s' "$*" | sed 's/ /|/g'); it is $MDB_BACKEND"
    return 64
}

mdb_be_up()           { _mdb_be_call up "$@"; }
mdb_be_down()         { _mdb_be_call down "$@"; }
mdb_be_status()       { _mdb_be_call status "$@"; }
mdb_be_running()      { _mdb_be_call running "$@"; }
mdb_be_logs()         { _mdb_be_call logs "$@"; }
mdb_svc_exec()        { _mdb_be_call exec "$@"; }
mdb_svc_exec_tty()    { _mdb_be_call exec_tty "$@"; }
mdb_svc_health()      { _mdb_be_call health "$@"; }
mdb_svc_healthy()     { [[ "$(mdb_svc_health "$@")" == healthy ]]; }
mdb_svc_cp()          { _mdb_be_call cp "$@"; }
mdb_be_volumes()      { _mdb_be_call volumes "$@"; }
mdb_be_volume_rm()    { _mdb_be_call volume_rm "$@"; }
mdb_be_image_load()   { _mdb_be_call image_load "$@"; }
mdb_be_secrets_sync() { _mdb_be_call secrets_sync "$@"; }
mdb_be_info()         { _mdb_be_call info "$@"; }
mdb_be_forward()      { _mdb_be_call forward "$@"; }

# Split "[-u USER] SVC CMD..." for backends: sets _MDB_SVC_USER and _MDB_SVC,
# and leaves the command in "$@" of the caller via _MDB_SVC_SHIFT.
_mdb_svc_args() {
    _MDB_SVC_USER=""; _MDB_SVC_SHIFT=0
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -u) _MDB_SVC_USER="$2"; shift 2; _MDB_SVC_SHIFT=$(( _MDB_SVC_SHIFT + 2 )) ;;
            -T|-i) shift; _MDB_SVC_SHIFT=$(( _MDB_SVC_SHIFT + 1 )) ;;
            --) shift; _MDB_SVC_SHIFT=$(( _MDB_SVC_SHIFT + 1 )) ;;
            *) break ;;
        esac
    done
    _MDB_SVC="${1:-}"
    _MDB_SVC_SHIFT=$(( _MDB_SVC_SHIFT + 1 ))
    [[ -n "$_MDB_SVC" ]] || { _mdb_rt_err "mdb_svc_exec: no service named"; return 2; }
}

_mdb_be_load

export -f mdb_backend_register mdb_backend_init _mdb_be_call mdb_backend_require _mdb_be_load \
    mdb_be_up mdb_be_down mdb_be_status mdb_be_running mdb_be_logs mdb_svc_exec mdb_svc_exec_tty mdb_svc_health \
    mdb_svc_healthy mdb_svc_cp mdb_be_volumes mdb_be_volume_rm mdb_be_image_load \
    mdb_be_secrets_sync mdb_be_info mdb_be_forward _mdb_svc_args 2>/dev/null || true
