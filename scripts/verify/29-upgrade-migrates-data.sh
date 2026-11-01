#!/usr/bin/env bash
# verify: make upgrade migrates data to the new major and leaves rollback intact
# tags: versions upgrade
# phase: 5

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_docker
cd "$MDB_ROOT" || exit 1

# SPEC section 18: "make upgrade migrates data". Check 14 proves the per-major
# VOLUMES stay separate; this proves the migration between them actually moves
# the rows, and — the half that matters when an upgrade goes wrong at 2am —
# that the old major is still sitting there, complete, to roll back to.

prefix="${MDB_IMAGE_PREFIX:-ghcr.io/simtabi}"
old_img="$prefix/dbpantry-pg:16"
new_img="$prefix/dbpantry-pg:17"
need_image "$old_img"
need_image "$new_img"

project="${COMPOSE_PROJECT_NAME:-mdb}"
vol16="${project}_pg16_data"
vol17="${project}_pg17_data"

# This check owns these volumes for its duration, so it starts from nothing and
# cleans up after itself rather than inheriting whatever a previous run left.
docker volume rm -f "$vol16" "$vol17" >/dev/null 2>&1 || true
track_volume "$vol16"
track_volume "$vol17"
add_cleanup "rm -rf '$MDB_ROOT/backups/upgrade'"

seed="mdb-verify-upg-seed-$$"
track_container "$seed"

docker volume create "$vol16" >/dev/null
docker run -d --name "$seed" -e POSTGRES_PASSWORD=mdb-throwaway-verify \
    -v "$vol16:/var/lib/postgresql/data" "$old_img" >/dev/null \
    || vfail "could not start PG 16 to seed"
wait_ready 90 "PG 16 to accept connections" docker exec "$seed" pg_isready -U postgres

docker exec "$seed" psql -U postgres -q -c "CREATE DATABASE legacy;" >/dev/null 2>&1 \
    || vfail "could not create the seed database"
docker exec "$seed" psql -U postgres -d legacy -q -c "
    CREATE TABLE t(v text);
    INSERT INTO t SELECT 'row-' || g FROM generate_series(1,250) g;" >/dev/null 2>&1 \
    || vfail "could not seed rows"
vinfo "seeded PG 16 with 250 rows"
docker rm -f -v "$seed" >/dev/null 2>&1 || true

CONFIRM=yes make upgrade ENGINE=pg FROM=16 TO=17 >/tmp/mdb-upgrade.log 2>&1 \
    || { tail -15 /tmp/mdb-upgrade.log >&2; vfail "make upgrade failed"; }
vinfo "upgrade completed"

# --- the data arrived on the new major --------------------------------------
check17="mdb-verify-upg-17-$$"
track_container "$check17"
docker run -d --name "$check17" -e POSTGRES_PASSWORD=mdb-throwaway-verify \
    -v "$vol17:/var/lib/postgresql/data" "$new_img" >/dev/null
wait_ready 90 "PG 17 to accept connections" docker exec "$check17" pg_isready -U postgres

ver="$(docker exec "$check17" psql -U postgres -tAc "SHOW server_version_num" | tr -d ' \r')"
[[ "${ver:0:2}" == "17" ]] || vfail "expected PG 17, got server_version_num=$ver"

rows="$(docker exec "$check17" psql -U postgres -d legacy -tAc "SELECT count(*) FROM t" 2>/dev/null | tr -d ' \r')"
[[ "$rows" == "250" ]] || vfail "PG 17 has $rows rows after upgrade, expected 250"
vinfo "PG 17 has all 250 rows"
docker rm -f -v "$check17" >/dev/null 2>&1 || true

# --- rollback is still possible ---------------------------------------------
# The upgrade is only safe if the source is untouched. This is the assertion
# that makes "just set the version back" true rather than hopeful.
check16="mdb-verify-upg-16-$$"
track_container "$check16"
docker run -d --name "$check16" -e POSTGRES_PASSWORD=mdb-throwaway-verify \
    -v "$vol16:/var/lib/postgresql/data" "$old_img" >/dev/null
wait_ready 90 "PG 16 to accept connections again" docker exec "$check16" pg_isready -U postgres

rows16="$(docker exec "$check16" psql -U postgres -d legacy -tAc "SELECT count(*) FROM t" 2>/dev/null | tr -d ' \r')"
[[ "$rows16" == "250" ]] \
    || vfail "PG 16 has $rows16 rows after the upgrade, expected 250; rollback would lose data"
vinfo "PG 16 still complete: rollback is possible"
