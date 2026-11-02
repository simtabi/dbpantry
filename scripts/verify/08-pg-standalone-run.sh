#!/usr/bin/env bash
# verify: the PG image is standalone-complete with only a password env
# tags: pg standalone
# phase: 2

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 18: "docker run ghcr.io/simtabi/dbpantry-pg with only a
# password env yields a fully configured server, no compose required."
# This is the check that keeps layer 2 of the architecture honest.

img="$(image_name pg)"
need_image "$img"

name="mdb-verify-pg-standalone-$$"
track_container "$name"

mdb_ctr run -d --name "$name" -e POSTGRES_PASSWORD=mdb-throwaway-verify "$img" >/dev/null \
    || vfail "mdb_ctr run failed with only POSTGRES_PASSWORD set"

wait_ready 60 "postgres to accept connections" \
    mdb_ctr exec "$name" pg_isready -U postgres

# A fully configured server, not merely a running one.
tz="$(mdb_ctr exec "$name" psql -U postgres -tAc "SHOW timezone" 2>/dev/null || true)"
[[ -n "$tz" ]] || vfail "could not query the running server"
vinfo "server up, timezone=$tz"

enc="$(mdb_ctr exec "$name" psql -U postgres -tAc "SHOW server_encoding" 2>/dev/null || true)"
[[ "$enc" == "UTF8" ]] || vfail "server_encoding is '$enc', expected UTF8"

# The baked tool suite must be present (SPEC section 6.1).
for tool in psql pg_dump pg_dumpall pg_restore zstd rclone; do
    mdb_ctr exec "$name" sh -c "command -v $tool" >/dev/null 2>&1 \
        || vfail "$tool is missing from the image; SPEC 6.1 requires the full tool suite baked in"
done
vinfo "baked tool suite present"

# s6 must be PID 1 (SPEC section 6.1).
pid1="$(mdb_ctr exec "$name" ps -o comm= -p 1 2>/dev/null | tr -d ' ')"
[[ "$pid1" == *s6* || "$pid1" == *init* ]] \
    || vfail "PID 1 is '$pid1', expected the s6-overlay init"
vinfo "PID 1 is $pid1"
