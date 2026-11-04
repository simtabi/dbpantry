#!/usr/bin/env bash
# shellcheck shell=bash
# Backend: Incus system containers through incus-compose (design.md D-66).
#
# Built from the incus-compose docs (https://incus-compose.org), pinned against
# stub incus/incus-compose binaries (scripts/verify/58-incus-backend.sh), and run
# on one live host on 2026-11-04 (Incus 7.0.2, incus-compose 1.3.5, the pg and ui
# profiles). See docs/guides/incus.md for what that run found.
#
# incus-compose is a drop-in for `docker compose`: it reads the same files,
# .env and profiles. What it does differently, and what this file maps:
#   - each Compose project is an Incus project of the same name, and a
#     service's instance is <service>-1 in it;
#   - health is written by the ic-healthd sidecar to the instance config key
#     user.healthcheck.status (unknown|stopped|starting|healthy|unhealthy);
#   - `exec -u` takes a user like Compose, the default is the image's user;
#   - volumes are Incus custom storage volumes in the project.

_mdb_incus_project() { mdb_project_name; }

# incus-compose, with the files and profiles of the stack passed explicitly
# (the caller's -f/--profile win), pinned to the project.
_mdb_incus_compose() {
    local -a ic pre=()
    read -r -a ic <<< "${MDB_INCUS_COMPOSE:-incus-compose}"
    command -v "${ic[0]}" >/dev/null 2>&1 \
        || { _mdb_rt_err "MDB_BACKEND=incus needs incus-compose on PATH (docs/guides/incus.md)"; return 127; }
    local a has_f=0 has_p=0 f p
    for a in "$@"; do
        case "$a" in
            -f|--file|--file=*) has_f=1 ;;
            --profile|--profile=*) has_p=1 ;;
        esac
    done
    if (( ! has_f )); then
        for f in $(_mdb_rt_compose_var COMPOSE_FILE | tr ':' ' '); do
            [[ -f "$MDB_RT_ROOT/$f" ]] && pre+=(-f "$MDB_RT_ROOT/$f")
        done
        # Incus-only overrides, last so they win. incus-compose loads its own
        # override only beside a file named compose.yaml, which the stack has not.
        [[ -f "$MDB_RT_ROOT/compose.incus.yml" ]] && pre+=(-f "$MDB_RT_ROOT/compose.incus.yml")
    fi
    if (( ! has_p )); then
        for p in $(_mdb_rt_compose_var COMPOSE_PROFILES | tr ',' ' '); do pre+=(--profile "$p"); done
    fi
    (cd "$MDB_RT_ROOT" && COMPOSE_PROJECT_NAME="$(_mdb_incus_project)" \
        MDB_NETWORK_GATEWAY="$(_mdb_incus_gateway)" "${ic[@]}" ${pre[@]+"${pre[@]}"} "$@")
}

# incus-compose refuses a network whose ipam names a subnet and no gateway
# ("ipam gateway cannot be empty", measured on 1.3.5); Compose picks the first
# host itself. compose.incus.yml names it: MDB_NETWORK_GATEWAY when set, else the
# first host of MDB_NETWORK_SUBNET.
_mdb_incus_gateway() {
    local gw subnet net a b c d
    gw="$(_mdb_rt_compose_var MDB_NETWORK_GATEWAY)"
    [[ -n "$gw" ]] && { printf '%s' "$gw"; return; }
    subnet="$(_mdb_rt_compose_var MDB_NETWORK_SUBNET)"
    net="${subnet:-172.28.0.0/16}"; net="${net%/*}"
    IFS=. read -r a b c d <<< "$net"
    printf '%s.%s.%s.%s' "$a" "$b" "$c" "$(( d + 1 ))"
}

# incus, pinned to the project. --project goes before a `--`, which ends incus's
# own flags: after it the flag reaches the command run in the instance instead
# (`exec pg-1 -- id -u postgres --project mdb` asked id for three users).
_mdb_incus() {
    command -v incus >/dev/null 2>&1 \
        || { _mdb_rt_err "MDB_BACKEND=incus needs the incus client on PATH (docs/guides/incus.md)"; return 127; }
    local -a own=()
    while [[ $# -gt 0 && "$1" != -- ]]; do own+=("$1"); shift; done
    incus "${own[@]}" --project "$(_mdb_incus_project)" "$@"
}

_mdb_incus_instance() { printf '%s-1\n' "$1"; }

_mdb_be_incus_down()   { _mdb_incus_compose --profile '*' "$@" down; }
_mdb_be_incus_status() { _mdb_incus_compose --profile '*' "$@" ps; }
_mdb_be_incus_logs()   { _mdb_incus_compose --profile '*' logs "$@"; }

# Running services: RUNNING instances named <service>-<n>, minus the healthd
# sidecar and one-off helpers incus-compose may add.
_mdb_be_incus_running() {
    _mdb_incus list -f csv -c ns 2>/dev/null \
        | awk -F, '$2=="RUNNING" {print $1}' \
        | sed -E 's/-[0-9]+$//' | grep -vE '^(ic-healthd|healthd)' | sort -u || true
}

_mdb_be_incus_health() {
    local inst st state
    inst="$(_mdb_incus_instance "$1")"
    state="$(_mdb_incus list -f csv -c s "^${inst}\$" 2>/dev/null | head -1)"
    if [[ -z "$state" ]]; then printf 'absent\n'; return 0; fi
    st="$(_mdb_incus config get "$inst" user.healthcheck.status 2>/dev/null || true)"
    case "$state/$st" in
        RUNNING/healthy) printf 'healthy\n' ;;
        RUNNING/unhealthy) printf 'unhealthy\n' ;;
        RUNNING/|RUNNING/unknown)
            # No verdict: no healthcheck declared (none), or healthd has not
            # reported yet. A watched instance carries user.healthcheck.enabled.
            if [[ "$(_mdb_incus config get "$inst" user.healthcheck.enabled 2>/dev/null)" == true ]]; then
                printf 'starting\n'
            else
                printf 'none\n'
            fi ;;
        RUNNING/*) printf 'starting\n' ;;
        *) printf 'unhealthy\n' ;;
    esac
}

# up -d, then wait like `compose up --wait`: every started service healthy (or
# running without a healthcheck), until MDB_WAIT_TIMEOUT.
_mdb_be_incus_up() {
    _mdb_incus_compose "$@" up -d || return
    local budget="${MDB_WAIT_TIMEOUT:-300}" i svcs s pending
    for (( i = 0; i < budget; i += 2 )); do
        svcs="$(_mdb_be_incus_running)"
        pending=0
        for s in $svcs; do
            case "$(_mdb_be_incus_health "$s")" in
                healthy|none) ;;
                unhealthy) _mdb_rt_err "service $s is unhealthy"; return 1 ;;
                *) pending=$(( pending + 1 )) ;;
            esac
        done
        (( pending == 0 )) && [[ -n "$svcs" ]] && return 0
        sleep 2
    done
    _mdb_rt_err "timed out after ${budget}s waiting for services to become healthy"
    return 1
}

_mdb_incus_exec() {
    local tty="$1"; shift
    _mdb_svc_args "$@" || return; shift "$_MDB_SVC_SHIFT"
    local -a flags=()
    [[ "$tty" == 1 && -t 0 && -t 1 ]] || flags=(-T)
    if [[ -n "$_MDB_SVC_USER" ]]; then
        local uid
        uid="$(_mdb_incus_uid "$_MDB_SVC" "$_MDB_SVC_USER")" || return
        flags+=(-u "$uid")
    fi
    _mdb_incus_compose exec ${flags[@]+"${flags[@]}"} "$_MDB_SVC" "$@"
}

# incus-compose exec -u takes a numeric uid only ("invalid argument "postgres"
# for "--user" flag", measured on 1.3.5), where Compose takes a name. A name is
# resolved inside the service's instance, so it is the image's own uid.
_mdb_incus_uid() {
    local svc="$1" user="$2" uid
    [[ "$user" =~ ^[0-9]+$ ]] && { printf '%s' "$user"; return; }
    uid="$(_mdb_incus exec "$(_mdb_incus_instance "$svc")" -- id -u "$user" 2>/dev/null)"
    [[ "$uid" =~ ^[0-9]+$ ]] || { _mdb_rt_err "no user '$user' in service $svc"; return 1; }
    printf '%s' "$uid"
}
_mdb_be_incus_exec()     { _mdb_incus_exec 0 "$@"; }
_mdb_be_incus_exec_tty() { _mdb_incus_exec 1 "$@"; }

_mdb_be_incus_cp() { _mdb_incus_compose cp "$1" "$2"; }

# Custom storage volumes of the project, in MDB_INCUS_POOL (default: default).
_mdb_be_incus_volumes() {
    _mdb_incus storage volume list "${MDB_INCUS_POOL:-default}" -f csv -c tn 2>/dev/null \
        | awk -F, '$1=="custom" {print $2}' | sort || true
}
_mdb_be_incus_volume_rm() { _mdb_incus storage volume delete "${MDB_INCUS_POOL:-default}" "$1"; }

_mdb_be_incus_image_load()   { :; }   # incus-compose pulls, or builds `build:` itself
_mdb_be_incus_secrets_sync() { :; }   # secrets/ is a bind-mounted file, as on Compose

_mdb_be_incus_info() {
    printf 'backend:   incus (MDB_BACKEND=%s) -- UNTESTED, see docs/guides/incus.md\n' "$MDB_BACKEND"
    printf 'project:   %s\n' "$(_mdb_incus_project)"
    printf 'pool:      %s\n' "${MDB_INCUS_POOL:-default}"
    if command -v incus >/dev/null 2>&1; then
        printf 'incus:     %s\n' "$(incus version 2>/dev/null | head -1)"
    else
        printf 'incus:     NOT on PATH\n'
    fi
    local -a ic; read -r -a ic <<< "${MDB_INCUS_COMPOSE:-incus-compose}"
    if command -v "${ic[0]}" >/dev/null 2>&1; then
        printf 'incus-compose: %s\n' "$("${ic[@]}" version 2>/dev/null | head -1)"
    else
        printf 'incus-compose: NOT on PATH\n'
    fi
}

mdb_backend_register incus
