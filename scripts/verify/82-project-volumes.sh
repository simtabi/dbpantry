#!/usr/bin/env bash
# verify: a project's volumes are its own, never another project's whose name starts the same way
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# 2026-11-24 audit, K4. mdb_project_volumes matched ^${project}_ as a regex,
# so project mdb also took mdb_dev's volumes, and
# `destroy --all` on mdb would list them for deletion. A project name may
# hold an underscore (env init --project), so the prefix alone cannot tell
# them apart: the Compose project label does.
#
# The container CLI is stubbed: volume ls prints the names below, volume
# inspect the label each was created with. No runtime is touched, and the
# names carry the test prefix (check 60).
# shellcheck disable=SC2016  # expanded by the inner bash
got="$(bash -c '
    . scripts/runtime-lib.sh
    mdb_runtime_init() { return 0; }
    mdb_ctr() {
        case "$1 $2" in
            "volume ls")      printf "%s\n" mdbverify_pg_data mdbverify_pg_pitr mdbverify_dev_pg_data mdbverify_dev_mysql_data mdbverifyx_pg_data old_mdbverify_pg_data mdbverify_legacy_data ;;
            "volume inspect") case "${*: -1}" in
                                  mdbverify_dev_*) echo mdbverify_dev ;;
                                  mdbverify_legacy_data) echo "" ;;
                                  *) echo mdbverify ;;
                              esac ;;
        esac
    }
    mdb_project_volumes mdbverify
' 2>&1)"
want="$(printf '%s\n' mdbverify_legacy_data mdbverify_pg_data mdbverify_pg_pitr)"
[[ "$got" == "$want" ]] || vfail "project mdbverify's volumes:
$got
       want:
$want"
vinfo "a project lists its own volumes, not those of a project it prefixes (mdbverify_dev), a longer name or an unrelated prefix"

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
