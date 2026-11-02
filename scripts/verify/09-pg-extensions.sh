#!/usr/bin/env bash
# verify: every extension named in SPEC section 5 actually creates
# tags: pg extensions
# phase: 2

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# DESIGN.md section 4 verified each of these is obtainable on arm64. This check
# proves they are actually installed and creatable, not merely available.

img="$(image_name pg)"
need_image "$img"

name="mdb-verify-pg-ext-$$"
track_container "$name"

# pgaudit, pg_cron, and pg_net are background-worker or hook extensions: they
# refuse to be created at all unless they are in shared_preload_libraries
# ("pgaudit must be loaded via shared_preload_libraries"). Preloading them here
# is correct usage, not an accommodation -- but it is also why the shipped
# default in .env.example lists only pg_stat_statements: preloading costs
# startup time and memory, so the others are opt-in.
mdb_ctr run -d --name "$name" -e POSTGRES_PASSWORD=mdb-throwaway-verify \
    -e MDB_PG_SHARED_PRELOAD=pg_stat_statements,pg_cron,pgaudit,pg_net \
    --shm-size=256m "$img" >/dev/null || vfail "container failed to start"

wait_ready 60 "postgres to accept connections" mdb_ctr exec "$name" pg_isready -U postgres

# pg_cron only creates in its configured database; postgis brings companions.
extensions=(
    vector postgis postgis_topology postgis_raster
    pg_cron pgaudit pg_stat_statements pg_trgm "uuid-ossp" pgcrypto citext hstore
    pg_repack pg_partman pgtap http pgjwt pgsodium pg_net pg_graphql
)

failed=()
for ext in "${extensions[@]}"; do
    if ! mdb_ctr exec "$name" psql -U postgres -q \
        -c "CREATE EXTENSION IF NOT EXISTS \"$ext\" CASCADE" >/dev/null 2>&1; then
        failed+=("$ext")
    fi
done

if (( ${#failed[@]} )); then
    printf '      failed to create: %s\n' "${failed[*]}" >&2
    vfail "${#failed[@]} of ${#extensions[@]} extensions could not be created"
fi

vinfo "all ${#extensions[@]} extensions create cleanly"

# plpython3u is untrusted and must stay off unless explicitly enabled.
if mdb_ctr exec "$name" psql -U postgres -tAc \
    "SELECT 1 FROM pg_language WHERE lanname='plpython3u'" 2>/dev/null | grep -q 1; then
    vfail "plpython3u is enabled by default; SPEC section 5 requires it off (MDB_PG_PLPYTHON)"
fi
vinfo "plpython3u correctly absent by default"
