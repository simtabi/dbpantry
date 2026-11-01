#!/usr/bin/env bash
# verify: switching PG major starts a new volume and leaves the old one intact
# tags: pg versions
# phase: 2

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_docker

# SPEC decision 2 exists because pointing a new PG major at an old data
# directory fails hard ("database files are incompatible with server").
# Per-major volumes make the switch safe AND reversible, so this check asserts
# both halves: the new major runs, and the old data still exists.

prefix="${MDB_IMAGE_PREFIX:-ghcr.io/simtabi}"
old_img="$prefix/dbpantry-pg:16"
new_img="$prefix/dbpantry-pg:17"

need_image "$old_img"
need_image "$new_img"

vol16="mdb-verify-pg16-$$"
vol17="mdb-verify-pg17-$$"
c16="mdb-verify-sw16-$$"
c17="mdb-verify-sw17-$$"

cleanup() {
    docker rm -f -v "$c16" "$c17" >/dev/null 2>&1 || true
    docker volume rm -f "$vol16" "$vol17" >/dev/null 2>&1 || true
}
add_cleanup cleanup

docker volume create "$vol16" >/dev/null
docker volume create "$vol17" >/dev/null

# --- start on 16 and write a marker row --------------------------------------
docker run -d --name "$c16" -e POSTGRES_PASSWORD=mdb-throwaway-verify \
    -v "$vol16:/var/lib/postgresql/data" "$old_img" >/dev/null \
    || vfail "PG 16 container failed to start"
wait_ready 60 "PG 16 to accept connections" docker exec "$c16" pg_isready -U postgres

docker exec "$c16" psql -U postgres -q \
    -c "CREATE TABLE marker(v text); INSERT INTO marker VALUES ('pg16-data');" >/dev/null 2>&1 \
    || vfail "could not write the marker row on PG 16"

major16="$(docker exec "$c16" psql -U postgres -tAc "SHOW server_version_num" | cut -c1-2)"
vinfo "PG 16 running (server_version_num prefix $major16), marker row written"
docker stop -t 30 "$c16" >/dev/null

# --- switch to 17, which must use a DIFFERENT volume -------------------------
docker run -d --name "$c17" -e POSTGRES_PASSWORD=mdb-throwaway-verify \
    -v "$vol17:/var/lib/postgresql/data" "$new_img" >/dev/null \
    || vfail "PG 17 container failed to start on its own volume"
wait_ready 60 "PG 17 to accept connections" docker exec "$c17" pg_isready -U postgres

ver17="$(docker exec "$c17" psql -U postgres -tAc "SHOW server_version_num")"
[[ "${ver17:0:2}" == "17" ]] || vfail "expected PG 17, got server_version_num=$ver17"
vinfo "PG 17 running on a fresh volume"

# The new major must NOT see the old data (proving volumes are separate).
if docker exec "$c17" psql -U postgres -tAc \
    "SELECT 1 FROM information_schema.tables WHERE table_name='marker'" 2>/dev/null | grep -q 1; then
    vfail "PG 17 sees PG 16's data; the volumes are not per-major"
fi
vinfo "PG 17 volume is independent of PG 16"

# --- the reversibility half: old data must still be intact -------------------
docker start "$c16" >/dev/null
wait_ready 60 "PG 16 to accept connections again" docker exec "$c16" pg_isready -U postgres

val="$(docker exec "$c16" psql -U postgres -tAc "SELECT v FROM marker" 2>/dev/null | tr -d ' ')"
[[ "$val" == "pg16-data" ]] || vfail "PG 16 data was lost or altered by the switch (got '$val')"

vinfo "PG 16 volume still intact and readable: rollback is possible"
