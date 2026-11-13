#!/usr/bin/env bash
# verify: switching PG major starts a new volume and leaves the old one intact
# tags: pg versions
# phase: 2

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC decision 2 exists because pointing a new PG major at an old data
# directory fails hard ("database files are incompatible with server").
# Per-major volumes make the switch safe AND reversible, so this check asserts
# both halves: the new major runs, and the old data still exists.
#
# 17 -> 18 are the two maintained majors (design.md D-57). 18 also moved the
# data mount one level up, to /var/lib/postgresql, so each side mounts its own.

prefix="$(mdb_image_prefix)"
old_img="$prefix/dbpantry-pg:17"
new_img="$prefix/dbpantry-pg:18"

need_image "$old_img"
need_image "$new_img"

vol17="mdb-verify-pg17-$$"
vol18="mdb-verify-pg18-$$"
c17="mdb-verify-sw17-$$"
c18="mdb-verify-sw18-$$"

cleanup() {
    mdb_ctr rm -f -v "$c17" "$c18" >/dev/null 2>&1 || true
    remove_test_volumes "$vol17" "$vol18"
}
add_cleanup cleanup

mdb_ctr volume create "$vol17" >/dev/null
mdb_ctr volume create "$vol18" >/dev/null

# --- start on 17 and write a marker row --------------------------------------
mdb_ctr run -d --name "$c17" -e POSTGRES_PASSWORD="${TW_VERIFY}" \
    -v "$vol17:/var/lib/postgresql/data" "$old_img" >/dev/null \
    || vfail "PG 17 container failed to start"
wait_ready 60 "PG 17 to accept connections" mdb_ctr exec "$c17" pg_isready -U postgres

mdb_ctr exec "$c17" psql -U postgres -q \
    -c "CREATE TABLE marker(v text); INSERT INTO marker VALUES ('pg17-data');" >/dev/null 2>&1 \
    || vfail "could not write the marker row on PG 17"

major17="$(mdb_ctr exec "$c17" psql -U postgres -tAc "SHOW server_version_num" | cut -c1-2)"
vinfo "PG 17 running (server_version_num prefix $major17), marker row written"
mdb_ctr stop -t 30 "$c17" >/dev/null

# --- switch to 18, which must use a DIFFERENT volume -------------------------
mdb_ctr run -d --name "$c18" -e POSTGRES_PASSWORD="${TW_VERIFY}" \
    -v "$vol18:/var/lib/postgresql" "$new_img" >/dev/null \
    || vfail "PG 18 container failed to start on its own volume"
wait_ready 60 "PG 18 to accept connections" mdb_ctr exec "$c18" pg_isready -U postgres

ver18="$(mdb_ctr exec "$c18" psql -U postgres -tAc "SHOW server_version_num")"
[[ "${ver18:0:2}" == "18" ]] || vfail "expected PG 18, got server_version_num=$ver18"
vinfo "PG 18 running on a fresh volume"

# The new major must NOT see the old data (proving volumes are separate).
if mdb_ctr exec "$c18" psql -U postgres -tAc \
    "SELECT 1 FROM information_schema.tables WHERE table_name='marker'" 2>/dev/null | grep -q 1; then
    vfail "PG 18 sees PG 17's data; the volumes are not per-major"
fi
vinfo "PG 18 volume is independent of PG 17"

# --- the reversibility half: old data must still be intact -------------------
mdb_ctr start "$c17" >/dev/null
wait_ready 60 "PG 17 to accept connections again" mdb_ctr exec "$c17" pg_isready -U postgres

val="$(mdb_ctr exec "$c17" psql -U postgres -tAc "SELECT v FROM marker" 2>/dev/null | tr -d ' ')"
[[ "$val" == "pg17-data" ]] || vfail "PG 17 data was lost or altered by the switch (got '$val')"

vinfo "PG 17 volume still intact and readable: rollback is possible"
