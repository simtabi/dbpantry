#!/usr/bin/env bash
# verify: no engine accepts trust or empty-password auth, including on localhost
# tags: security auth
# phase: 5

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 9: "no trust/empty-password anywhere, including localhost."
#
# This check exists because the toolkit was violating it. The official
# PostgreSQL image ships an initdb-generated pg_hba.conf that trusts the local
# socket AND loopback TCP:
#
#     local  all  all                   trust
#     host   all  all  127.0.0.1/32     trust
#     host   all  all  ::1/128          trust
#
# The consequence is not theoretical. Any process inside the container -- a
# compromised extension, a sidecar sharing the network namespace, an untrusted
# procedural language -- connects as the superuser with no credential at all.
# It also makes password rotation impossible to verify, because a connection
# over loopback succeeds whatever the password is, which is exactly how this
# was found.
#
# SPEC section 10.1 names the intended local method: peer auth, "reserved for
# make-driven maintenance as the engine user".

img="$(image_name pg)"
need_image "$img"

name="mdb-verify-notrust-$$"
track_container "$name"

mdb_ctr run -d --name "$name" -e POSTGRES_PASSWORD=mdb-throwaway-verify "$img" >/dev/null \
    || vfail "container failed to start"
wait_ready 90 "postgres to accept connections" \
    mdb_ctr exec -u postgres "$name" pg_isready -U postgres

hba="$(mdb_ctr exec "$name" cat /var/lib/postgresql/data/pg_hba.conf 2>/dev/null)"
[[ -n "$hba" ]] || vfail "could not read pg_hba.conf"

# Any non-comment rule ending in `trust` is a finding, wherever it appears.
offenders="$(printf '%s\n' "$hba" | grep -vE '^\s*#|^\s*$' | awk '$NF == "trust"' || true)"
if [[ -n "$offenders" ]]; then
    printf '      %s\n' "$offenders" >&2
    vfail "pg_hba.conf still contains trust rules; SPEC section 9 forbids trust anywhere, including localhost"
fi
vinfo "pg_hba.conf contains no trust rules"

# ...and the same for empty-password auth.
if printf '%s\n' "$hba" | grep -vE '^\s*#|^\s*$' | awk '$NF == "password"' | grep -q .; then
    vfail "pg_hba.conf uses cleartext 'password' auth; scram-sha-256 is required"
fi
vinfo "no cleartext password auth"

# The behavioural assertion, not just the file contents: a loopback TCP
# connection with a WRONG password must be refused.
if mdb_ctr exec -u postgres -e PGPASSWORD=mdb-throwaway-wrong-password "$name" \
    psql -h 127.0.0.1 -U postgres -tAc "SELECT 1" >/dev/null 2>&1; then
    vfail "a loopback TCP connection succeeded with the wrong password; auth is not being enforced"
fi
vinfo "loopback TCP refuses a wrong password"

# ...while the right one still works, so the rule is enforcing rather than
# simply breaking connectivity.
mdb_ctr exec -u postgres -e PGPASSWORD=mdb-throwaway-verify "$name" \
    psql -h 127.0.0.1 -U postgres -tAc "SELECT 1" >/dev/null 2>&1 \
    || vfail "loopback TCP refused the CORRECT password; auth is broken, not hardened"
vinfo "loopback TCP accepts the correct password"

# Peer auth on the socket: the engine user connects, others do not.
mdb_ctr exec -u postgres "$name" psql -U postgres -tAc "SELECT 1" >/dev/null 2>&1 \
    || vfail "the postgres OS user cannot connect over the socket; peer auth is misconfigured"
vinfo "socket peer auth works for the engine user"
