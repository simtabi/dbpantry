#!/usr/bin/env bash
# verify: the pooler serves client TLS: prod refuses plaintext and requires TLS upstream, dev still connects
# tags: pooling tls security prod
# phase: 6

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# design.md D-69. An application on another host reaches PostgreSQL through
# pgBouncer, published on MDB_BIND_ADDR, and refuses any sslmode weaker than
# require to a non-loopback host. pgBouncer's client_tls_sslmode defaults to
# disable, so before this every such client failed to connect -- while the
# pooler itself looked healthy, and check 33, which connects in plaintext on a
# private network, stayed green. So this asserts behaviour on the wire:
#
#   1. under enforcement a plaintext client is REFUSED by the pooler;
#   2. under enforcement an sslmode=require client connects, and one using
#      verify-full against certs/ca.crt does too -- the pooler serves the
#      toolkit certificate, not something a client would have to skip-verify;
#   3. under enforcement the pooler -> PostgreSQL hop is TLS, as the server
#      itself reports for the backend running the query;
#   4. under enforcement with no certificate mounted the pooler refuses to
#      start, rather than quietly serving plaintext;
#   5. without enforcement (dev) a plaintext client still connects, and a TLS
#      one is offered TLS.
#
# Each attempt is counted; the check fails if it made fewer than it expects,
# so a block that stops running cannot pass by having asserted nothing.

need_dir "$MDB_ROOT/certs"
for f in ca.crt pg/server.crt pg/server.key; do
    need_file "$MDB_ROOT/certs/$f"
done

img="$(image_name pg)"
need_image "$img"

pooler_img="$(grep -oE "^MDB_ENGINE_POOLER_IMAGE='[^']+'" engines/postgres/engine.conf \
              | sed "s/^MDB_ENGINE_POOLER_IMAGE='//; s/'$//")"
[[ -n "$pooler_img" ]] || vfail "engines/postgres/engine.conf declares no pooler image"
need_image "$pooler_img"

# verify-full checks the name the client dialled against the certificate, so
# the certificate must name the pooler's service. Certificates issued before
# scripts/certs added pg-pooler do not, and `make certs-renew` re-issues them.
if ! mdb_ctr run --rm --entrypoint openssl -v "$MDB_ROOT/certs:/certs:ro" "$img" \
        x509 -in /certs/pg/server.crt -noout -text 2>/dev/null | grep -qF 'DNS:pg-pooler'; then
    vfail "certs/pg/server.crt has no DNS:pg-pooler SAN; run 'make certs-renew' to re-issue it"
fi

net="mdb-verify-pooltls-net-$$"
pg="mdb-verify-pooltls-pg-$$"
prod="mdb-verify-pooltls-prod-$$"
dev="mdb-verify-pooltls-dev-$$"
nocert="mdb-verify-pooltls-nocert-$$"
secrets="$(mktemp -d)"

track_container "$pg"
track_container "$prod"
track_container "$dev"
track_container "$nocert"
track_network "$net"
add_cleanup "rm -rf '$secrets'"

printf '%s\n' "${TW_BOUNCER}" > "$secrets/pgbouncer_password.txt"
chmod 0600 "$secrets"/*

attempts=0

mdb_ctr network create "$net" >/dev/null || vfail "could not create the test network"

# PostgreSQL as the prod profile runs it: toolkit certificates, hostssl only.
mdb_ctr run -d --name "$pg" --network "$net" --network-alias pg \
    -e POSTGRES_PASSWORD="${TW_SUPER}" \
    -e MDB_TLS_ENFORCE=true \
    -e MDB_PG_DATABASES="poolapp:poolapp:${TW_APP}" \
    -v "$secrets:/run/secrets:ro" \
    -v "$MDB_ROOT/certs:/certs:ro" \
    "$img" >/dev/null || vfail "PostgreSQL failed to start"

wait_ready 120 "postgres to accept connections" \
    mdb_ctr exec -u postgres "$pg" pg_isready -U postgres
wait_ready 60 "the pooler auth function to be converged" \
    mdb_ctr exec -u postgres "$pg" psql -qtAX -d postgres \
        -c "SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
            WHERE n.nspname='pgbouncer' AND p.proname='get_auth'"

# start_pooler NAME ENFORCE MOUNT_CERTS [ALIAS] -- the same environment compose
# gives pg-pooler (engines/postgres/compose-pooler.yml), minus the pool sizing.
start_pooler() {
    local name="$1" enforce="$2" certs="$3" alias="${4:-}"
    # The +-expansion keeps an empty array legal under `set -u` on bash 3.2.
    local -a extra=()
    [[ "$certs" == yes ]] && extra+=(-v "$MDB_ROOT/certs:/certs:ro")
    [[ -n "$alias" ]] && extra+=(--network-alias "$alias")
    # shellcheck disable=SC2016  # $1 is pgbouncer's placeholder, not ours
    mdb_ctr run -d --name "$name" --network "$net" ${extra[@]+"${extra[@]}"} \
        -e DB_PASSWORD_FILE=/run/secrets/pgbouncer_password.txt \
        -e DB_HOST=pg -e DB_PORT=5432 -e DB_USER=pgbouncer \
        -e LISTEN_PORT=6432 -e MDB_TLS_ENFORCE="$enforce" \
        -e AUTH_TYPE=scram-sha-256 -e AUTH_USER=pgbouncer -e AUTH_DBNAME=postgres \
        -e 'AUTH_QUERY=SELECT username, password FROM pgbouncer.get_auth($1)' \
        -e POOL_MODE=transaction -e DEFAULT_POOL_SIZE=2 -e MAX_CLIENT_CONN=100 \
        -e ADMIN_USERS=pgbouncer -e STATS_USERS=pgbouncer \
        -v "$secrets:/run/secrets:ro" \
        --tmpfs /etc/pgbouncer:exec,mode=0700,uid=100,gid=100 \
        "$pooler_img" >/dev/null
}

# psql from the PostgreSQL image, on the test network. --entrypoint psql keeps
# s6's logging out of the output (see check 33). The CA is mounted so
# verify-full has something to verify against. The result lands in $out rather
# than on stdout: a $(...) caller would run this in a subshell and the attempt
# counter would never move.
out=""
psql_to() {
    local host="$1" sslmode="$2" sql="$3"
    attempts=$(( attempts + 1 ))
    out="$(mdb_ctr run --rm --network "$net" -e PGPASSWORD="${TW_APP}" \
        -v "$MDB_ROOT/certs/ca.crt:/ca.crt:ro" --entrypoint psql "$img" \
        "host=$host port=6432 user=poolapp dbname=poolapp sslmode=$sslmode sslrootcert=/ca.crt" \
        -qtAX -c "$sql" 2>&1)"
}

running() { [[ "$(mdb_ctr inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" == "true" ]]; }

# --- prod: MDB_TLS_ENFORCE=true -----------------------------------------------

start_pooler "$prod" true yes pg-pooler || vfail "the enforcing pooler failed to start"
sleep 3
if ! running "$prod"; then
    mdb_ctr logs "$prod" 2>&1 | tail -15 >&2
    vfail "the enforcing pooler is not running"
fi

wait_ready 60 "the enforcing pooler to accept TLS connections" \
    mdb_ctr run --rm --network "$net" -e PGPASSWORD="${TW_APP}" --entrypoint psql "$img" \
        "host=$prod port=6432 user=poolapp dbname=poolapp sslmode=require" -qtAX -c 'SELECT 1'

conf="$(mdb_ctr exec "$prod" cat /etc/pgbouncer/pgbouncer.ini 2>/dev/null)" \
    || vfail "could not read the enforcing pooler's generated config"
grep -qx 'client_tls_sslmode = require' <<< "$conf" \
    || vfail "the enforcing pooler's config does not set client_tls_sslmode = require"
grep -qx 'server_tls_sslmode = require' <<< "$conf" \
    || vfail "the enforcing pooler's config does not set server_tls_sslmode = require"
vinfo "generated config: client_tls_sslmode = require, server_tls_sslmode = require"

# 1. Plaintext is refused. Exit status, not error text: any failure text would
#    match a string, including one that proves nothing about TLS. The require
#    attempt below with the same credentials is what pins the cause on TLS.
if psql_to "$prod" disable 'SELECT 1'; then
    vfail "the enforcing pooler accepted a plaintext client"
fi
vinfo "plaintext client refused under enforcement"

# 2. sslmode=require connects, and so does verify-full by the service name.
psql_to "$prod" require 'SELECT 42' || true; got="$out"
[[ "$got" == "42" ]] || vfail "sslmode=require through the pooler returned '$got', expected 42"
vinfo "sslmode=require client connected through the pooler"

psql_to pg-pooler verify-full 'SELECT 43' || true; got="$out"
[[ "$got" == "43" ]] || vfail "sslmode=verify-full against certs/ca.crt returned '$got', expected 43"
vinfo "sslmode=verify-full against the toolkit CA connected through the pooler"

# 3. The query runs on a server backend the pooler opened; PostgreSQL reports
#    whether THAT connection is encrypted.
psql_to "$prod" require 'SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()' || true; got="$out"
[[ "$got" == "t" ]] || vfail "the pooler -> PostgreSQL connection reports ssl='$got', expected t"
vinfo "pooler -> PostgreSQL hop is TLS"

# 4. Enforcement without a certificate must not start.
start_pooler "$nocert" true no || true
sleep 3
if running "$nocert"; then
    vfail "the pooler started under MDB_TLS_ENFORCE with no certificate; it would serve plaintext"
fi
attempts=$(( attempts + 1 ))
vinfo "enforcement without a certificate refuses to start"

# --- dev: MDB_TLS_ENFORCE=false -----------------------------------------------

start_pooler "$dev" false yes || vfail "the dev pooler failed to start"
sleep 3
running "$dev" || { mdb_ctr logs "$dev" 2>&1 | tail -15 >&2; vfail "the dev pooler is not running"; }
wait_ready 60 "the dev pooler to accept connections" \
    mdb_ctr run --rm --network "$net" -e PGPASSWORD="${TW_APP}" --entrypoint psql "$img" \
        "host=$dev port=6432 user=poolapp dbname=poolapp sslmode=disable" -qtAX -c 'SELECT 1'

# 5. Plaintext still works in dev, and TLS is offered to a client that asks.
psql_to "$dev" disable 'SELECT 44' || true; got="$out"
[[ "$got" == "44" ]] || vfail "plaintext through the dev pooler returned '$got', expected 44"
psql_to "$dev" require 'SELECT 45' || true; got="$out"
[[ "$got" == "45" ]] || vfail "sslmode=require through the dev pooler returned '$got', expected 45"
vinfo "dev pooler accepts plaintext and offers TLS"

# Six client connections plus one refused start. Fewer means a block above
# stopped running without failing.
(( attempts == 7 )) || vfail "made $attempts attempts, expected 7; part of this check did not run"
vinfo "pooler TLS behaves as enforced in prod and optional in dev ($attempts attempts)"
