#!/usr/bin/env bash
# verify: a subnet moved off an overlapping pool is written only to the env file of the project being started
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# design.md D-103 moves MDB_NETWORK_SUBNET when the configured pool overlaps a
# network that exists, and writes the move to the env file. Check 27 starts a
# throwaway project (COMPOSE_PROJECT_NAME=mdbverifyNNNN) from the operator's
# checkout, so on 2026-11-25 the move landed in the operator's own .env, for a
# project that was not theirs. The move is still made for that run; it is
# written only when the project being started is the env file's own.

tmp="$(mktemp -d "${TMPDIR:-/tmp}/mdb-subnet.XXXXXX")"
add_cleanup "rm -rf '$tmp'"
mkdir -p "$tmp/root"
printf 'COMPOSE_PROJECT_NAME=mdb\nMDB_NETWORK_SUBNET=172.28.0.0/16\n' > "$tmp/root/.env"

# ensure <extra env...>: run mdb_network_ensure_subnet against a stub runtime
# where one network ("other") holds 172.28.0.0/16 and no project network
# exists; print the subnet the run exported.
ensure() {
    # shellcheck disable=SC2016  # expanded by the inner bash
    env -i PATH="$PATH" HOME="$HOME" TMPDIR="$tmp" MDB_RT_ROOT="$tmp/root" "$@" bash -c '
        . "$1/scripts/runtime-lib.sh"; . "$1/scripts/network-lib.sh"
        mdb_runtime_init() { return 0; }
        mdb_ctr() {
            case "$1 $2" in
                "network ls") echo other ;;
                "network inspect") [[ "${*: -1}" == other ]] && echo 172.28.0.0/16 || return 1 ;;
            esac
        }
        mdb_network_ensure_subnet 2>/dev/null || exit 1
        printf %s "$MDB_NETWORK_SUBNET"
    ' _ "$MDB_ROOT"
}

before="$(cat "$tmp/root/.env")"
got="$(ensure COMPOSE_PROJECT_NAME=mdbverify1)" || vfail "ensure_subnet failed for a throwaway project"
[[ "$got" == 172.16.0.0/16 ]] || vfail "the throwaway project was not moved off the overlapping pool: [$got]"
[[ "$(cat "$tmp/root/.env")" == "$before" ]] \
    || vfail "a throwaway project's subnet was written into another project's env file: $(grep NETWORK "$tmp/root/.env")"
vinfo "a throwaway project's move is exported for its run, the env file is untouched"

got="$(ensure)" || vfail "ensure_subnet failed for the env file's own project"
[[ "$got" == 172.16.0.0/16 ]] || vfail "the env file's own project was not moved: [$got]"
grep -qx 'MDB_NETWORK_SUBNET=172.16.0.0/16' "$tmp/root/.env" \
    || vfail "the env file's own project's move was not written: $(grep NETWORK "$tmp/root/.env")"
vinfo "the env file's own project still has its move written (D-103)"
