#!/usr/bin/env bash
# verify: up re-issues a server certificate whose SANs no longer cover the stack, from the same CA, and leaves current ones alone
# tags: certs
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime
cd "$MDB_ROOT" || exit 1

# A certificate issued before a SAN was added (pg-pooler, design.md D-69) kept
# working for every client but the new one, and the only cure was a manual
# `make certs-renew` that the CHANGELOG's upgrade notes asked for. Check 68
# failed on exactly that in the full run of 2026-11-25. `dbpantry up` now runs
# `scripts/certs --heal`, which hands a stale set to certs-renew (D-111).

home="$(mktemp -d "${TMPDIR:-/tmp}/mdb-certs-heal.XXXXXX")"
add_cleanup "rm -rf '$home'"
# A project of its own, so certs-renew finds no running engine to reload.
printf 'COMPOSE_PROJECT_NAME=mdbverifycerts%s\n' "$$" > "$home/.env"

certs() { env -u COMPOSE_PROJECT_NAME -u MDB_ENV_FILE MDB_HOME="$home" MDB_ENV_FILE="$home/.env" scripts/certs "$@"; }
fp() { openssl x509 -in "$1" -noout -fingerprint -sha256 2>/dev/null; }
sans() { openssl x509 -in "$1" -noout -text 2>/dev/null | awk '/Subject Alternative Name/{getline; print}'; }

certs >/dev/null 2>&1 || vfail "scripts/certs could not issue a fresh set"
for e in pg mysql mariadb; do [[ -s "$home/certs/$e/server.crt" ]] || vfail "no $e certificate issued"; done
ca="$(fp "$home/certs/ca.crt")"

# The pg certificate as it was issued before D-69: no pg-pooler name.
openssl req -newkey rsa:2048 -nodes -subj "/O=Simtabi LLC/OU=dbpantry/CN=pg" \
    -keyout "$home/certs/pg/server.key" -out "$home/certs/pg/server.csr" 2>/dev/null
openssl x509 -req -in "$home/certs/pg/server.csr" -CA "$home/certs/ca.crt" -CAkey "$home/certs/ca.key" \
    -CAcreateserial -days 30 -out "$home/certs/pg/server.crt" \
    -extfile <(printf 'subjectAltName=DNS:pg,DNS:localhost,DNS:postgres,DNS:pgbouncer,DNS:haproxy,IP:127.0.0.1\n') 2>/dev/null
[[ "$(sans "$home/certs/pg/server.crt")" != *pg-pooler* ]] || vfail "could not make a stale pg certificate"
mysql_before="$(fp "$home/certs/mysql/server.crt")"

out="$(certs --heal 2>&1)" || vfail "certs --heal failed: $(tail -5 <<< "$out")"
grep -q 'pg: lacks DNS:pg-pooler' <<< "$out" || vfail "--heal did not name what the pg certificate lacks: $(head -5 <<< "$out")"
[[ "$(sans "$home/certs/pg/server.crt")" == *DNS:pg-pooler* ]] || vfail "--heal left the pg certificate without DNS:pg-pooler"
[[ "$(fp "$home/certs/ca.crt")" == "$ca" ]] || vfail "--heal replaced the CA; clients that trust it would stop connecting"
vinfo "a stale certificate was re-issued from the same CA: $(grep -c . <<< "$out") lines of output"

pg_after="$(fp "$home/certs/pg/server.crt")"
mysql_after="$(fp "$home/certs/mysql/server.crt")"
[[ "$mysql_after" != "$mysql_before" ]] || vinfo "note: certs-renew left the current mysql certificate as it was"
out="$(certs --heal 2>&1)" || vfail "a second --heal failed: $out"
[[ -z "$out" ]] || vfail "--heal is not silent when every certificate is current: $out"
[[ "$(fp "$home/certs/pg/server.crt")" == "$pg_after" && "$(fp "$home/certs/mysql/server.crt")" == "$mysql_after" ]] \
    || vfail "--heal re-issued certificates that were current"
vinfo "with every certificate current, --heal is silent and changes nothing"
