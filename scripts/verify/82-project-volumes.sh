#!/usr/bin/env bash
# verify: a project's volumes are its own, never another project's whose name starts the same way
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# 2026-11-24 audit, K4. mdb_project_volumes matched ^${project}_ as a regex,
# so project mdb also took mdb_dev's volumes (mdb_dev_pg_data), and
# `destroy --all` on mdb would list them for deletion. A project name may
# hold an underscore (env init --project), so the prefix alone cannot tell
# them apart: the Compose project label does.
#
# The container CLI is stubbed: volume ls prints the names below, volume
# inspect the label each was created with. No runtime is touched.
# shellcheck disable=SC2016  # expanded by the inner bash
got="$(bash -c '
    . scripts/runtime-lib.sh
    mdb_runtime_init() { return 0; }
    mdb_ctr() {
        case "$1 $2" in
            "volume ls")      printf "%s\n" mdb_pg_data mdb_pg_pitr mdb_dev_pg_data mdb_dev_mysql_data mdbx_pg_data old_mdb_pg_data mdb_legacy_data ;;
            "volume inspect") case "${*: -1}" in
                                  mdb_dev_*) echo mdb_dev ;;
                                  mdb_legacy_data) echo "" ;;
                                  *) echo mdb ;;
                              esac ;;
        esac
    }
    mdb_project_volumes mdb
' 2>&1)"
want="$(printf '%s\n' mdb_legacy_data mdb_pg_data mdb_pg_pitr)"
[[ "$got" == "$want" ]] || vfail "project mdb's volumes:
$got
       want:
$want"
vinfo "project mdb lists its own volumes, not mdb_dev's, mdbx's or an unrelated prefix"

# A name is matched as text: a project name is never a regex.
# shellcheck disable=SC2016  # expanded by the inner bash
got="$(bash -c '
    . scripts/runtime-lib.sh
    mdb_runtime_init() { return 0; }
    mdb_ctr() { case "$1 $2" in "volume ls") printf "%s\n" a.b_x axb_x ;; "volume inspect") echo "a.b" ;; esac; }
    mdb_project_volumes a.b
' 2>&1)"
[[ "$got" == a.b_x ]] || vfail "project a.b matched as a regex: [$got]"
vinfo "a project name is matched as text, not a pattern"
