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
