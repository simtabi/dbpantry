#!/usr/bin/env bash
# verify: the RLS kit isolates tenants, including from the table owner
# tags: security rls
# phase: 5

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 18: "PG RLS demo passes".
#
# Check 01 asserts rls/ exists. A directory is not a passing demo, and this is
# the one acceptance criterion where the difference matters most: an RLS setup
# that silently does nothing looks exactly like one that works, right up until
# a tenant reads another tenant's data.
#
# Four assertions, in the order the mistakes are usually made:
#
#   1. a tenant sees only its own rows;
#   2. an UNSET tenant sees nothing -- the policy fails closed, not open;
#   3. a tenant cannot INSERT rows labelled with another tenant's id, which
#      USING alone would permit (writable but invisible, worse than refused);
#   4. the TABLE OWNER is not exempt, which is what FORCE ROW LEVEL SECURITY
#      buys and the single most common way this silently does nothing.

img="$(image_name pg)"
need_image "$img"
need_file "$MDB_ROOT/rls/rls-template.sql"

name="mdb-verify-rls-$$"
track_container "$name"

mdb_ctr run -d --name "$name" -e POSTGRES_PASSWORD=mdb-throwaway-rls "$img" >/dev/null \
    || vfail "container failed to start"
wait_ready 120 "postgres to accept connections" \
    mdb_ctr exec -u postgres "$name" pg_isready -U postgres

# Applied as a NON-superuser. A superuser bypasses RLS entirely, so running the
# demo as postgres would prove nothing at all -- every query would succeed and
# the check would pass on a policy that does not work.
mdb_ctr exec -i -u postgres "$name" psql -qtAX -v ON_ERROR_STOP=1 -d postgres >/dev/null 2>&1 <<'SQL' \
    || vfail "could not create the tenant-app role"
CREATE ROLE rlsapp LOGIN PASSWORD 'mdb-throwaway-rlsapp';
CREATE DATABASE rlsdemo OWNER rlsapp;
SQL

# PGPASSWORD is required even over the unix socket: pg_hba reserves peer auth
# for the postgres superuser and demands scram-sha-256 for everyone else, which
# check 30 exists to keep that way.
app_pw=mdb-throwaway-rlsapp

# As the OWNER of the table, which is the case FORCE exists for.
mdb_ctr exec -i -u postgres -e PGPASSWORD="$app_pw" "$name" psql -qtAX -v ON_ERROR_STOP=1 \
    -d rlsdemo -U rlsapp -h /var/run/postgresql < "$MDB_ROOT/rls/rls-template.sql" >/dev/null 2>&1 \
    || vfail "rls/rls-template.sql failed to apply"
vinfo "rls-template.sql applied as the table owner"

app_sql() {
    mdb_ctr exec -i -u postgres -e PGPASSWORD="$app_pw" "$name" psql -qtAX -d rlsdemo -U rlsapp \
        -h /var/run/postgresql 2>/dev/null | tr -d ' \r'
}

t1=11111111-1111-1111-1111-111111111111
t2=22222222-2222-2222-2222-222222222222

app_sql >/dev/null <<SQL
BEGIN;
SET LOCAL app.tenant_id = '${t1}';
INSERT INTO documents (tenant_id, title) VALUES ('${t1}', 'one'), ('${t1}', 'two');
COMMIT;
BEGIN;
SET LOCAL app.tenant_id = '${t2}';
INSERT INTO documents (tenant_id, title) VALUES ('${t2}', 'three');
COMMIT;
SQL

# 1. Each tenant sees only its own rows.
c1="$(printf "BEGIN; SET LOCAL app.tenant_id = '%s'; SELECT count(*) FROM documents; COMMIT;" "$t1" | app_sql)"
c2="$(printf "BEGIN; SET LOCAL app.tenant_id = '%s'; SELECT count(*) FROM documents; COMMIT;" "$t2" | app_sql)"
[[ "$c1" == "2" ]] || vfail "tenant 1 sees '$c1' rows, expected 2"
[[ "$c2" == "1" ]] || vfail "tenant 2 sees '$c2' rows, expected 1"
vinfo "each tenant sees only its own rows (2 and 1)"

# 2. Unset tenant sees nothing. The policy must fail CLOSED.
c0="$(printf 'SELECT count(*) FROM documents;' | app_sql)"
[[ "$c0" == "0" ]] || vfail "a session with no tenant set sees '$c0' rows; RLS must fail closed"
vinfo "unset tenant sees no rows: the policy fails closed"

# 3. A tenant cannot write rows labelled with another tenant's id. WITH CHECK.
cross="$(printf "BEGIN; SET LOCAL app.tenant_id = '%s';
    INSERT INTO documents (tenant_id, title) VALUES ('%s', 'smuggled'); COMMIT;" "$t1" "$t2" \
    | mdb_ctr exec -i -u postgres -e PGPASSWORD="$app_pw" "$name" psql -qtAX -d rlsdemo \
        -U rlsapp -h /var/run/postgresql 2>&1)"
case "$cross" in
    *"row-level security"*|*"violates"*) ;;
    *) vfail "tenant 1 inserted a row labelled for tenant 2; WITH CHECK is not enforced" ;;
esac
vinfo "cross-tenant INSERT refused by WITH CHECK"

# 4. The owner is not exempt. Without FORCE, rlsapp -- which owns the table --
#    would see all three rows here and the isolation above would be an illusion.
owner_total="$(printf 'SELECT count(*) FROM documents;' | app_sql)"
[[ "$owner_total" == "0" ]] \
    || vfail "the table owner sees $owner_total rows with no tenant set; FORCE ROW LEVEL SECURITY is missing"
vinfo "table owner is not exempt: FORCE ROW LEVEL SECURITY is in effect"

vinfo "RLS demo passes: tenants isolated, fails closed, owner not exempt"
