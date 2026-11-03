#!/usr/bin/env bash
# verify: make upgrade migrates data to the new major and leaves rollback intact
# tags: versions upgrade
# phase: 5

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime
cd "$MDB_ROOT" || exit 1

# SPEC section 18: "make upgrade migrates data". Check 14 proves the per-major
# VOLUMES stay separate; this proves the migration between them actually moves
# the rows, and — the half that matters when an upgrade goes wrong at 2am —
# that the old major is still sitting there, complete, to roll back to.
#
# 17 -> 18 are the two maintained majors (design.md D-57), and the pair crosses
# PG 18's move of the data mount to /var/lib/postgresql, which `make upgrade`
# has to get right on each side.

prefix="${MDB_IMAGE_PREFIX:-ghcr.io/simtabi}"
old_img="$prefix/dbpantry-pg:17"
new_img="$prefix/dbpantry-pg:18"
need_image "$old_img"
need_image "$new_img"

# A project name of its own, so the volumes this check creates and removes can
# never be the operator's real <project>_pg17_data. require_test_name and the
# guarded helpers in lib.sh refuse any volume without the test prefix, so a
# mistake here fails the check instead of reaching real data.
project="mdbverify$$"
require_test_name "$project" project
vol17="${project}_pg17_data"
vol18="${project}_pg18_data"

# This check owns these volumes for its duration, so it starts from nothing and
# cleans up after itself rather than inheriting whatever a previous run left.
remove_test_volumes "$vol17" "$vol18"
track_volume "$vol17"
track_volume "$vol18"
# make upgrade writes its dumps to MDB_UPGRADE_DIR, which lib.sh points inside
# the check's private backup directory (removed at exit). This check used to
# rm -rf the checkout's backups/upgrade, where an operator's own upgrade dumps
# live.
[[ "$MDB_UPGRADE_DIR" != "$MDB_CHECKOUT_BACKUPS"* ]] || vfail "MDB_UPGRADE_DIR is inside the checkout's dump directory"
upglog="$MDB_VERIFY_BACKUP_DIR/upgrade.log"

seed="mdb-verify-upg-seed-$$"
track_container "$seed"

mdb_ctr volume create "$vol17" >/dev/null
mdb_ctr run -d --name "$seed" -e POSTGRES_PASSWORD="${TW_VERIFY}" \
    -v "$vol17:/var/lib/postgresql/data" "$old_img" >/dev/null \
    || vfail "could not start PG 17 to seed"
wait_ready 90 "PG 17 to accept connections" mdb_ctr exec "$seed" pg_isready -U postgres

mdb_ctr exec "$seed" psql -U postgres -q -c "CREATE DATABASE legacy;" >/dev/null 2>&1 \
    || vfail "could not create the seed database"
mdb_ctr exec "$seed" psql -U postgres -d legacy -q -c "
    CREATE TABLE t(v text);
    INSERT INTO t SELECT 'row-' || g FROM generate_series(1,250) g;" >/dev/null 2>&1 \
    || vfail "could not seed rows"
vinfo "seeded PG 17 with 250 rows"
mdb_ctr rm -f -v "$seed" >/dev/null 2>&1 || true

COMPOSE_PROJECT_NAME="$project" CONFIRM=yes make upgrade ENGINE=pg FROM=17 TO=18 >"$upglog" 2>&1 \
    || { tail -15 "$upglog" >&2; vfail "make upgrade failed"; }
[[ -n "$(find "$MDB_UPGRADE_DIR" -type f 2>/dev/null)" ]] \
    || vfail "make upgrade wrote no dump to MDB_UPGRADE_DIR ($MDB_UPGRADE_DIR); it may be writing to the checkout's dump directory"
vinfo "upgrade completed"

# --- the data arrived on the new major --------------------------------------
check18="mdb-verify-upg-18-$$"
track_container "$check18"
mdb_ctr run -d --name "$check18" -e POSTGRES_PASSWORD="${TW_VERIFY}" \
    -v "$vol18:/var/lib/postgresql" "$new_img" >/dev/null
wait_ready 90 "PG 18 to accept connections" mdb_ctr exec "$check18" pg_isready -U postgres

ver="$(mdb_ctr exec "$check18" psql -U postgres -tAc "SHOW server_version_num" | tr -d ' \r')"
[[ "${ver:0:2}" == "18" ]] || vfail "expected PG 18, got server_version_num=$ver"

rows="$(mdb_ctr exec "$check18" psql -U postgres -d legacy -tAc "SELECT count(*) FROM t" 2>/dev/null | tr -d ' \r')"
[[ "$rows" == "250" ]] || vfail "PG 18 has $rows rows after upgrade, expected 250"
vinfo "PG 18 has all 250 rows"
mdb_ctr rm -f -v "$check18" >/dev/null 2>&1 || true

# --- rollback is still possible ---------------------------------------------
# The upgrade is only safe if the source is untouched. This is the assertion
# that makes "just set the version back" true rather than hopeful.
check17="mdb-verify-upg-17-$$"
track_container "$check17"
mdb_ctr run -d --name "$check17" -e POSTGRES_PASSWORD="${TW_VERIFY}" \
    -v "$vol17:/var/lib/postgresql/data" "$old_img" >/dev/null
wait_ready 90 "PG 17 to accept connections again" mdb_ctr exec "$check17" pg_isready -U postgres

rows17="$(mdb_ctr exec "$check17" psql -U postgres -d legacy -tAc "SELECT count(*) FROM t" 2>/dev/null | tr -d ' \r')"
[[ "$rows17" == "250" ]] \
    || vfail "PG 17 has $rows17 rows after the upgrade, expected 250; rollback would lose data"
vinfo "PG 17 still complete: rollback is possible"
