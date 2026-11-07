#!/bin/sh
#
# Hand pgBouncer its own credential, then start it.
#
# The image has no _FILE convention and its own entrypoint warns that
# `docker inspect` exposes environment variables. So the password is read from
# the mounted secret into the process environment of PID 1 at exec time: never
# in an image layer, never in the compose file, never in `docker inspect`.
#
# This is the ONLY credential pgBouncer holds. Application users are resolved
# with auth_query against a narrow SECURITY DEFINER function -- see
# docs/guides/pooling.md.

set -eu

[ -r "${DB_PASSWORD_FILE:?DB_PASSWORD_FILE is required}" ] \
    || { echo "pgbouncer: cannot read $DB_PASSWORD_FILE" >&2; exit 1; }

DB_PASSWORD="$(tr -d '\n' < "$DB_PASSWORD_FILE")"
export DB_PASSWORD

# TLS material for client connections (design.md D-69). The pooler serves the
# certificate `make certs` issues for PostgreSQL -- the same lookup as the pg
# image's mdb-certs stage, /certs/pg first, then /certs -- so a client that
# trusts certs/ca.crt trusts the pooler too. It is copied, not referenced in
# place, for the same reason the password is read here: certs/pg/server.key is
# host-owned 0600, readable by this root process and not by postgres. The copy
# lands on the /etc/pgbouncer tmpfs, so the key never reaches the container's
# writable layer. entrypoint.sh decides what to do with it; finding none is
# fatal only under MDB_TLS_ENFORCE.
#
# Every file is written as root with its mode already set, and the tree is
# handed to postgres once, last. Under Compose the pooler keeps only
# DAC_OVERRIDE, SETUID, SETGID and CHOWN. `install -o postgres -m ...` changes
# the owner and THEN the mode, and changing the mode of a file root no longer
# owns needs FOWNER: under Compose it failed with "cannot change permissions",
# and passed wherever Docker's default capabilities applied. Check 72 refuses
# `install -o` here.
TLS_DIR="${TLS_DIR:-/etc/pgbouncer/tls}"
export TLS_DIR
staged=0
for candidate in /certs/pg /certs; do
    if [ -r "$candidate/server.crt" ] && [ -r "$candidate/server.key" ]; then
        install -d -m 0700 "$TLS_DIR"
        install -m 0600 "$candidate/server.key" "$TLS_DIR/server.key"
        install -m 0644 "$candidate/server.crt" "$TLS_DIR/server.crt"
        if [ -r /certs/ca.crt ]; then
            install -m 0644 /certs/ca.crt "$TLS_DIR/ca.crt"
        fi
        echo "pgbouncer: serving TLS with the certificate from $candidate" >&2
        staged=1
        break
    fi
done

# The pooler's own CLIENT certificate, presented to PostgreSQL under mTLS
# (design.md D-74). Issued by `make certs` as certs/pooler/client.{crt,key}.
if [ -r /certs/pooler/client.crt ] && [ -r /certs/pooler/client.key ]; then
    install -d -m 0700 "$TLS_DIR"
    install -m 0600 /certs/pooler/client.key "$TLS_DIR/pooler-client.key"
    install -m 0644 /certs/pooler/client.crt "$TLS_DIR/pooler-client.crt"
    staged=1
fi

# Ownership last: a chown needs CHOWN and nothing else.
if [ "$staged" -eq 1 ]; then
    chown -R postgres:postgres "$TLS_DIR"
fi

# The read above happens as ROOT, and the drop to postgres happens here, at
# exec -- the same read-then-drop the engine entrypoints use, and the reason
# they never hit this: secrets/ is created on the host by make init as the
# invoking user, mode 0700, files 0600. A container process that starts as
# postgres cannot even traverse that directory on Linux, so this script died
# on its very first line with "cannot read" and the check reported "the
# pooler is not running (exit 1)". On macOS the Docker implementation
# flattens ownership, which is why it only ever failed on CI. The image
# previously baked USER postgres, which made the unprivileged read the only
# option; the Dockerfile now leaves the user root and this line is the drop.
exec setpriv --reuid postgres --regid postgres --init-groups \
    /entrypoint.sh /usr/sbin/pgbouncer /etc/pgbouncer/pgbouncer.ini
