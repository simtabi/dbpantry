#!/usr/bin/env bash
# verify: the pooler pools, authenticates without holding app passwords, and multiplexes
# tags: pooling auth
# phase: 6

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 22.4. A pooler that starts is not a pooler that works, and the
# failure mode is quiet: connections go to it, it cannot authenticate them, and
# the application falls back to the direct port that is still open in dev. So
# this asserts behaviour, in four parts:
#
#   1. an application user can connect THROUGH the pooler;
#   2. the pooler does not hold that user's password -- it resolves it with
#      auth_query against a narrow SECURITY DEFINER function;
#   3. many client connections collapse onto few server connections, which is
#      the entire point;
#   4. a WRONG password is still refused through the pooler, because a pooler
#      that authenticates everyone is worse than no pooler at all.

img="$(image_name pg)"
need_image "$img"

pooler_img="$(grep -oE "^MDB_ENGINE_POOLER_IMAGE='[^']+'" engines/postgres/engine.conf \
              | sed "s/^MDB_ENGINE_POOLER_IMAGE='//; s/'$//")"
[[ -n "$pooler_img" ]] || vfail "engines/postgres/engine.conf declares no pooler image"

# The pooler is an image we BUILD now, so it can be absent or reclaimed like
# any other. Without this the failure surfaced as "the pooler failed to start",
# which reads as a defect in the pooler rather than a missing image.
need_image "$pooler_img"

net="mdb-verify-pool-net-$$"
pg="mdb-verify-pool-pg-$$"
bouncer="mdb-verify-pool-bouncer-$$"
secrets="$(mktemp -d)"

track_container "$pg"
track_container "$bouncer"
track_network "$net"
add_cleanup "rm -rf '$secrets'"

printf 'mdb-throwaway-bouncer\n' > "$secrets/pgbouncer_password.txt"
chmod 0600 "$secrets"/*

mdb_ctr network create "$net" >/dev/null || vfail "could not create the test network"

# An application user, provisioned the normal way. Its password is known to
# THIS check and to PostgreSQL -- and, the point of the exercise, never to the
# pooler.
mdb_ctr run -d --name "$pg" --network "$net" --network-alias pg \
    -e POSTGRES_PASSWORD=mdb-throwaway-super \
    -e MDB_PG_DATABASES="poolapp:poolapp:mdb-throwaway-app" \
    -v "$secrets:/run/secrets:ro" \
    "$img" >/dev/null || vfail "PostgreSQL failed to start"

wait_ready 120 "postgres to accept connections" \
    mdb_ctr exec -u postgres "$pg" pg_isready -U postgres

# The convergence stage runs after the server is up, so give it a moment to
# land rather than racing it.
wait_ready 60 "the pooler auth function to be converged" \
    mdb_ctr exec -u postgres "$pg" psql -qtAX -d postgres \
        -c "SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
            WHERE n.nspname='pgbouncer' AND p.proname='get_auth'"

vinfo "pgbouncer.get_auth() converged"

# The function must NOT be able to leak the superuser's verifier.
super_leak="$(mdb_ctr exec -u postgres "$pg" psql -qtAX -d postgres \
    -c "SELECT count(*) FROM pgbouncer.get_auth('postgres')" 2>/dev/null | tr -d '[:space:]')"
[[ "$super_leak" == "0" ]] \
    || vfail "pgbouncer.get_auth() returned a superuser verifier; it must exclude superusers"
vinfo "auth function refuses to return superuser verifiers"

# shellcheck disable=SC2016  # $1 is pgbouncer's placeholder, not ours
mdb_ctr run -d --name "$bouncer" --network "$net" \
    -e DB_PASSWORD_FILE=/run/secrets/pgbouncer_password.txt \
    -e DB_HOST=pg -e DB_PORT=5432 -e DB_USER=pgbouncer \
    -e LISTEN_PORT=6432 \
    -e AUTH_TYPE=scram-sha-256 -e AUTH_USER=pgbouncer -e AUTH_DBNAME=postgres \
    -e 'AUTH_QUERY=SELECT username, password FROM pgbouncer.get_auth($1)' \
    -e POOL_MODE=transaction -e DEFAULT_POOL_SIZE=2 -e MAX_CLIENT_CONN=100 \
    -e ADMIN_USERS=pgbouncer -e STATS_USERS=pgbouncer \
    -v "$secrets:/run/secrets:ro" \
    --tmpfs /etc/pgbouncer:exec,mode=0700,uid=100,gid=100 \
    "$pooler_img" >/dev/null || vfail "the pooler failed to start"

# The pooler exiting 0 is the failure this check was written after: the shim
# generates the config and, with no CMD to exec, succeeds at doing nothing.
sleep 3
if [[ "$(mdb_ctr inspect -f '{{.State.Running}}' "$bouncer" 2>/dev/null)" != "true" ]]; then
    mdb_ctr logs "$bouncer" 2>&1 | tail -15 >&2
    vfail "the pooler is not running (exit $(mdb_ctr inspect -f '{{.State.ExitCode}}' "$bouncer" 2>/dev/null))"
fi

# psql runs from the PostgreSQL image, on the same network -- no client needed
# on the host. --entrypoint psql is load-bearing: the image's entrypoint is s6,
# so without it every result comes back wrapped in supervision-tree logging and
# no comparison against the query output can ever match.
via_pooler() {
    mdb_ctr run --rm --network "$net" -e PGPASSWORD="$1" --entrypoint psql "$img" \
        -h "$bouncer" -p 6432 -U "$2" -d "$3" -qtAX -c "$4" 2>&1
}

wait_ready 60 "the pooler to accept connections" \
    mdb_ctr run --rm --network "$net" -e PGPASSWORD=mdb-throwaway-app \
        --entrypoint psql "$img" \
        -h "$bouncer" -p 6432 -U poolapp -d poolapp -qtAX -c 'SELECT 1'

# 1. An application user connects through the pooler.
got="$(via_pooler mdb-throwaway-app poolapp poolapp 'SELECT 42')"
[[ "$got" == "42" ]] || vfail "query through the pooler returned '$got', expected 42"
vinfo "application user authenticated through the pooler with auth_query"

# 2. The pooler holds no application password. Its userlist may contain only
#    its own credential; finding the app user's password there would mean the
#    auth_query path is not actually in use.
if mdb_ctr exec "$bouncer" sh -c 'cat /etc/pgbouncer/userlist.txt 2>/dev/null' \
     | grep -q 'mdb-throwaway-app'; then
    vfail "the pooler stored an application password; auth_query must resolve them instead"
fi
vinfo "no application password is stored in the pooler"

# 3. Multiplexing: more clients than server connections. DEFAULT_POOL_SIZE=2,
#    so eight concurrent clients must not open eight backends.
for _ in 1 2 3 4 5 6 7 8; do
    mdb_ctr run --rm -d --network "$net" -e PGPASSWORD=mdb-throwaway-app \
        --entrypoint psql "$img" \
        -h "$bouncer" -p 6432 -U poolapp -d poolapp -qtAX \
        -c 'SELECT pg_sleep(3)' >/dev/null 2>&1 || true
done
sleep 2
backends="$(mdb_ctr exec -u postgres "$pg" psql -qtAX -d postgres \
    -c "SELECT count(*) FROM pg_stat_activity WHERE usename='poolapp'" 2>/dev/null \
    | tr -d '[:space:]')"
backends="${backends:-0}"
if (( backends > 4 )); then
    vfail "pool_size is 2 but PostgreSQL has $backends poolapp backends; the pooler is not multiplexing"
fi
vinfo "8 clients collapsed onto $backends server connection(s) (pool_size 2)"

# 4. A wrong password is still refused. Asserted on exit status rather than on
#    the text of the error: matching strings would pass for any failure at all,
#    including "could not connect", which proves nothing about authentication.
if via_pooler mdb-throwaway-wrong-password poolapp poolapp 'SELECT 1' >/dev/null 2>&1; then
    vfail "the pooler accepted a wrong password"
fi
vinfo "wrong password refused through the pooler"

vinfo "pooler pools, authenticates by query, and stores no application credential"
