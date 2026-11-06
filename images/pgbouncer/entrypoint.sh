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

TLS_DIR="${TLS_DIR:-/etc/pgbouncer/tls}"

# Client TLS (design.md D-69). bootstrap.sh copies the toolkit certificate into
# TLS_DIR when one is mounted. Under MDB_TLS_ENFORCE -- the prod profile --
# plaintext clients are refused and the hop to PostgreSQL must be TLS too;
# without enforcement TLS is offered and plaintext still accepted, so dev stays
# painless. Enforcement with no certificate is fatal: starting anyway would
# serve plaintext on a stack that promised it would not.
enforce=false
case "${MDB_TLS_ENFORCE:-false}" in
    [Tt][Rr][Uu][Ee]|1|[Yy][Ee][Ss]|[Oo][Nn]) enforce=true ;;
esac
have_tls=false
[ -s "$TLS_DIR/server.crt" ] && [ -s "$TLS_DIR/server.key" ] && have_tls=true
if [ "$enforce" = true ] && [ "$have_tls" = false ]; then
    echo "pgbouncer: MDB_TLS_ENFORCE is true but no certificate is mounted at /certs/pg; run 'make certs'" >&2
    exit 1
fi

# Mutual TLS (design.md D-74). Clients must present a certificate the toolkit
# CA issued, and the pooler presents its own to PostgreSQL and verifies
# PostgreSQL's by name. Everything it needs must be present, or it refuses to
# start: half of mTLS is not mTLS.
mtls=false
case "${MDB_MTLS:-false}" in
    [Tt][Rr][Uu][Ee]|1|[Yy][Ee][Ss]|[Oo][Nn]) mtls=true ;;
esac
if [ "$mtls" = true ]; then
    for f in server.crt server.key ca.crt pooler-client.crt pooler-client.key; do
        if [ ! -s "$TLS_DIR/$f" ]; then
            echo "pgbouncer: MDB_MTLS is true but $f is missing; run 'make certs' (it issues certs/pooler/client.*)" >&2
            exit 1
        fi
    done
fi

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
    # Setting names and values checked against https://www.pgbouncer.org/config.html
    # for the PGDG build this image installs (1.26 at the time of writing; none
    # of these changed after 1.21). client_tls_sslmode defaults to disable,
    # which is why a client asking for sslmode=require could not connect at all.
    if [ "$have_tls" = true ]; then
        if [ "$mtls" = true ]; then
            # pgBouncer documents verify-full as "same as verify-ca": a
            # certificate from client_tls_ca_file is required, and its CN is
            # not matched to the user. The role's password is still checked.
            printf 'client_tls_sslmode = verify-full\n'
            printf 'client_tls_ca_file = %s/ca.crt\n' "$TLS_DIR"
        elif [ "$enforce" = true ]; then
            printf 'client_tls_sslmode = require\n'
        else
            printf 'client_tls_sslmode = prefer\n'
        fi
        printf 'client_tls_cert_file = %s/server.crt\n' "$TLS_DIR"
        printf 'client_tls_key_file = %s/server.key\n' "$TLS_DIR"
        printf 'client_tls_protocols = secure\n'
    fi
    # server_tls_sslmode already defaults to prefer, which falls back to
    # plaintext without saying so. Under enforcement the pooler -> PostgreSQL
    # hop must be TLS or fail; under mTLS it also presents the pooler's own
    # certificate and checks PostgreSQL's against the CA and its host name.
    if [ "$mtls" = true ]; then
        printf 'server_tls_sslmode = verify-full\n'
        printf 'server_tls_ca_file = %s/ca.crt\n' "$TLS_DIR"
        printf 'server_tls_cert_file = %s/pooler-client.crt\n' "$TLS_DIR"
        printf 'server_tls_key_file = %s/pooler-client.key\n' "$TLS_DIR"
    elif [ "$enforce" = true ]; then
        printf 'server_tls_sslmode = require\n'
    fi
    # Logging to stdout is what makes `docker compose logs` useful; a logfile
    # inside a container is a file nobody reads.
    printf 'logfile =\n'
    printf 'pidfile =\n'
} > "$CONF"
chmod 0600 "$CONF"

exec "$@"
