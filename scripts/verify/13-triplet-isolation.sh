#!/usr/bin/env bash
# verify: triplet-provisioned projects are isolated; cross-access is denied
# tags: pg provisioning security
# phase: 2

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 18: "Two projects per engine, isolated roles, cross-access
# denied." Provisioning that creates the databases but leaves project A able to
# read project B is worse than no provisioning, because it looks correct.

img="$(image_name pg)"
need_image "$img"

name="mdb-verify-triplet-$$"
track_container "$name"

mdb_ctr run -d --name "$name" \
    -e POSTGRES_PASSWORD=mdb-throwaway-verify \
    -e MDB_PG_DATABASES="alpha:alpha_user:mdb-throwaway-alpha,beta:beta_user:mdb-throwaway-beta" \
    "$img" >/dev/null || vfail "container failed to start with triplet provisioning"

wait_ready 60 "postgres to accept connections" mdb_ctr exec "$name" pg_isready -U postgres

# Both databases and both roles must exist.
for db in alpha beta; do
    mdb_ctr exec "$name" psql -U postgres -tAc \
        "SELECT 1 FROM pg_database WHERE datname='$db'" 2>/dev/null | grep -q 1 \
        || vfail "database '$db' was not provisioned"
done
for role in alpha_user beta_user; do
    mdb_ctr exec "$name" psql -U postgres -tAc \
        "SELECT 1 FROM pg_roles WHERE rolname='$role'" 2>/dev/null | grep -q 1 \
        || vfail "role '$role' was not provisioned"
done
vinfo "both databases and both roles provisioned"

# Owner must be able to work in its own database.
mdb_ctr exec -e PGPASSWORD=mdb-throwaway-alpha "$name" \
    psql -U alpha_user -d alpha -tAc "CREATE TABLE t(i int); DROP TABLE t;" >/dev/null 2>&1 \
    || vfail "alpha_user cannot create a table in its own database"
vinfo "alpha_user owns its own database"

# The isolation assertion: alpha_user must NOT reach beta.
if mdb_ctr exec -e PGPASSWORD=mdb-throwaway-alpha "$name" \
    psql -U alpha_user -d beta -tAc "SELECT 1" >/dev/null 2>&1; then
    vfail "alpha_user connected to database beta; projects are not isolated"
fi
vinfo "alpha_user denied on database beta"

# No app role may hold superuser.
supers="$(mdb_ctr exec "$name" psql -U postgres -tAc \
    "SELECT rolname FROM pg_roles WHERE rolsuper AND rolname LIKE '%_user'" 2>/dev/null || true)"
[[ -z "$supers" ]] || vfail "app roles hold superuser: $supers"
vinfo "no app role holds superuser"
