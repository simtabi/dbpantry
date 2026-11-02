#!/bin/sh
#
# Generate pgbouncer.ini from the environment, then exec pgBouncer.
#
# This reproduces the contract the previous third-party image provided, so
# replacing that image changed nothing about how the pooler is configured:
# engines/postgres/compose-pooler.yml still sets DB_HOST, AUTH_QUERY,
# POOL_MODE and the rest, and this turns them into a config file.
#
# The file is written to /etc/pgbouncer, which compose mounts as tmpfs, so the
# generated userlist never reaches disk.

set -eu

CONF=/etc/pgbouncer/pgbouncer.ini
AUTH_FILE="${AUTH_FILE:-/etc/pgbouncer/userlist.txt}"

: > "$AUTH_FILE"
chmod 0600 "$AUTH_FILE"

# pgBouncer authenticates itself to PostgreSQL with this, and resolves every
# other user through auth_query -- so this is the only credential it holds.
# See docs/guides/pooling.md and design.md D-35.
if [ -n "${DB_USER:-}" ] && [ -n "${DB_PASSWORD:-}" ]; then
    printf '"%s" "%s"\n' "$DB_USER" "$DB_PASSWORD" >> "$AUTH_FILE"
fi

{
    printf '[databases]\n'
    # A wildcard entry with auth_user: any database, every user resolved by
    # query rather than listed here.
    printf '* = host=%s port=%s auth_user=%s\n' \
        "${DB_HOST:?DB_HOST is required}" "${DB_PORT:-5432}" "${AUTH_USER:-pgbouncer}"

    printf '\n[pgbouncer]\n'
    printf 'listen_addr = %s\n' "${LISTEN_ADDR:-0.0.0.0}"
    printf 'listen_port = %s\n' "${LISTEN_PORT:-6432}"
    printf 'auth_file = %s\n' "$AUTH_FILE"
    printf 'auth_type = %s\n' "${AUTH_TYPE:-scram-sha-256}"
    [ -n "${AUTH_USER:-}" ]    && printf 'auth_user = %s\n' "$AUTH_USER"
    [ -n "${AUTH_QUERY:-}" ]   && printf 'auth_query = %s\n' "$AUTH_QUERY"
    [ -n "${AUTH_DBNAME:-}" ]  && printf 'auth_dbname = %s\n' "$AUTH_DBNAME"
    printf 'pool_mode = %s\n' "${POOL_MODE:-transaction}"
    printf 'max_client_conn = %s\n' "${MAX_CLIENT_CONN:-1000}"
    printf 'default_pool_size = %s\n' "${DEFAULT_POOL_SIZE:-25}"
    [ -n "${MIN_POOL_SIZE:-}" ]     && printf 'min_pool_size = %s\n' "$MIN_POOL_SIZE"
    [ -n "${RESERVE_POOL_SIZE:-}" ] && printf 'reserve_pool_size = %s\n' "$RESERVE_POOL_SIZE"
    [ -n "${MAX_DB_CONNECTIONS:-}" ] && printf 'max_db_connections = %s\n' "$MAX_DB_CONNECTIONS"
    [ -n "${MAX_PREPARED_STATEMENTS:-}" ] \
        && printf 'max_prepared_statements = %s\n' "$MAX_PREPARED_STATEMENTS"
    printf 'ignore_startup_parameters = %s\n' "${IGNORE_STARTUP_PARAMETERS:-extra_float_digits}"
    printf 'admin_users = %s\n' "${ADMIN_USERS:-pgbouncer}"
    [ -n "${STATS_USERS:-}" ]       && printf 'stats_users = %s\n' "$STATS_USERS"
    [ -n "${LOG_POOLER_ERRORS:-}" ] && printf 'log_pooler_errors = %s\n' "$LOG_POOLER_ERRORS"
    # Logging to stdout is what makes `docker compose logs` useful; a logfile
    # inside a container is a file nobody reads.
    printf 'logfile =\n'
    printf 'pidfile =\n'
} > "$CONF"
chmod 0600 "$CONF"

exec "$@"
