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
TLS_DIR="${TLS_DIR:-/etc/pgbouncer/tls}"
export TLS_DIR
for candidate in /certs/pg /certs; do
    if [ -r "$candidate/server.crt" ] && [ -r "$candidate/server.key" ]; then
        install -d -o postgres -g postgres -m 0700 "$TLS_DIR"
        install -o postgres -g postgres -m 0600 "$candidate/server.key" "$TLS_DIR/server.key"
        install -o postgres -g postgres -m 0644 "$candidate/server.crt" "$TLS_DIR/server.crt"
        if [ -r /certs/ca.crt ]; then
            install -o postgres -g postgres -m 0644 /certs/ca.crt "$TLS_DIR/ca.crt"
        fi
        echo "pgbouncer: serving TLS with the certificate from $candidate" >&2
        break
    fi
done

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
