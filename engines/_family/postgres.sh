#!/usr/bin/env bash
# shellcheck shell=bash
#
# Family hooks: PostgreSQL (SPEC section 22.2).
#
# Descriptors carry the declarative facts. These are the few operations that
# genuinely need code, and they are per FAMILY rather than per engine so that
# two engines of the same family share one implementation.
#
# The caller provides: engine_exec, secret, compress_ext, and the loaded
# MDB_ENGINE_* descriptor values.

# In the sidecar the engine is a remote host over TCP, which requires a
# password. From the host we exec inside the container and reach it over the
# socket, where peer auth applies and no credential is needed. Setting this
# here rather than in the generic driver keeps engine specifics with the engine.
if [ "${IN_CONTAINER:-0}" = "1" ] && [ -r "/run/secrets/${MDB_ENGINE_ROOT_SECRET:-pg_superuser_password.txt}" ]; then
    PGPASSWORD="$(tr -d '\n' < "/run/secrets/${MDB_ENGINE_ROOT_SECRET:-pg_superuser_password.txt}")"
    export PGPASSWORD
fi

# Readiness. Deliberately NOT hook_list_databases: that answers "are there
# user databases", and a healthy fresh server has none, so using it as a
# readiness probe waits forever on a container that is already up.
hook_ping() {
    engine_exec "$MDB_ENGINE_NAME" pg_isready -U postgres >/dev/null 2>&1
}

# Databases a backup should cover: everything connectable that is not a
# template.
hook_list_databases() {
    engine_exec "$MDB_ENGINE_NAME" psql -U postgres -tAc \
        "SELECT datname FROM pg_database WHERE datallowconn AND NOT datistemplate" \
        | tr -d '\r' | grep -v '^$'
}

hook_dump_database() {
    local db="$1" out="$2"
    if [ -n "$(compress_ext)" ]; then
        engine_exec "$MDB_ENGINE_NAME" pg_dump -U postgres -Fc --no-password "$db" \
            | zstd -q -T0 -"${MDB_BACKUP_ZSTD_LEVEL:-9}" -o "$out" -f
    else
        engine_exec "$MDB_ENGINE_NAME" pg_dump -U postgres -Fc --no-password "$db" > "$out"
    fi
}

# Roles and grants live only here. A per-database dump restores into a server
# nobody can log into, which is why this runs on every backup.
hook_dump_globals() {
    local out="$1"
    if [ -n "$(compress_ext)" ]; then
        engine_exec "$MDB_ENGINE_NAME" pg_dumpall -U postgres --globals-only --no-password \
            | zstd -q -T0 -"${MDB_BACKUP_ZSTD_LEVEL:-9}" -o "$out" -f
    else
        engine_exec "$MDB_ENGINE_NAME" pg_dumpall -U postgres --globals-only --no-password > "$out"
    fi
}

hook_recreate_database() {
    local db="$1"
    engine_exec "$MDB_ENGINE_NAME" psql -U postgres -q -c "DROP DATABASE IF EXISTS \"$db\";" >/dev/null
    engine_exec "$MDB_ENGINE_NAME" psql -U postgres -q -c "CREATE DATABASE \"$db\";" >/dev/null
}

# --no-owner keeps the restore working when roles differ between source and
# target; globals are restored separately.
hook_restore_database() {
    local db="$1"
    engine_exec "$MDB_ENGINE_NAME" pg_restore -U postgres -d "$db" --no-owner --clean --if-exists
}

# Tables present, as distinct from rows. A database with no tables was empty
# at dump time, which is a legitimate state; a database WITH tables but no
# rows means the restore lost the data. Only the second is a failure.
hook_object_count() {
    local db="$1"
    engine_exec "$MDB_ENGINE_NAME" psql -U postgres -d "$db" -tAc \
        "SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')" \
        2>/dev/null | tr -d ' \r'
}

hook_row_count() {
    local db="$1"
    engine_exec "$MDB_ENGINE_NAME" psql -U postgres -d "$db" -tAc \
        "SELECT COALESCE(SUM(n_live_tup), 0) FROM pg_stat_user_tables" 2>/dev/null | tr -d ' \r'
}

# Auth enforcement probe: a connection with a WRONG password must be refused.
# Returns 0 when auth is properly enforced.
hook_auth_enforced() {
    if engine_exec "$MDB_ENGINE_NAME" env PGPASSWORD=mdb-throwaway-wrong-password \
        psql -h 127.0.0.1 -U postgres -tAc "SELECT 1" >/dev/null 2>&1; then
        return 1
    fi
    return 0
}

# Provision a project: database, owner role, read-only companion, extensions.
#
# Idempotent throughout, because `make new-project` on an existing name must be
# safe -- people re-run it to reprint the connection block.
hook_provision_project() {
    local db="$1" user="$2" pw="$3" extensions="${4:-}"
    local esc_pw="${pw//\'/\'\'}"

    engine_exec "$MDB_ENGINE_NAME" psql -U postgres -q -v ON_ERROR_STOP=1 <<SQL || return 1
DO \$mdb\$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${user}') THEN
        ALTER ROLE ${user} WITH LOGIN PASSWORD '${esc_pw}';
    ELSE
        CREATE ROLE ${user} LOGIN PASSWORD '${esc_pw}';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${user}_readonly') THEN
        CREATE ROLE ${user}_readonly NOLOGIN;
    END IF;
END \$mdb\$;
SQL

    # CREATE DATABASE cannot run inside a transaction block, so it is separate
    # from the DO block above rather than folded into it.
    if ! engine_exec "$MDB_ENGINE_NAME" psql -U postgres -tAc \
            "SELECT 1 FROM pg_database WHERE datname='${db}'" 2>/dev/null | grep -q 1; then
        engine_exec "$MDB_ENGINE_NAME" psql -U postgres -q \
            -c "CREATE DATABASE ${db} OWNER ${user};" || return 1
    fi

    # Isolation: revoke the implicit PUBLIC grant so another project's role
    # cannot connect here (SPEC section 18, "cross-access denied").
    engine_exec "$MDB_ENGINE_NAME" psql -U postgres -q -v ON_ERROR_STOP=1 <<SQL || return 1
REVOKE ALL ON DATABASE ${db} FROM PUBLIC;
GRANT CONNECT, TEMPORARY ON DATABASE ${db} TO ${user};
GRANT CONNECT ON DATABASE ${db} TO ${user}_readonly;
SQL

    # Split the list first and loop with the default IFS: engine_exec reaches
    # mdb_compose, which splits "docker compose" into words, so running it while
    # IFS=',' made every CREATE EXTENSION run a command that does not exist and
    # warn about extensions the image ships (found 2026-11-17, check 76).
    local ext err
    local -a exts=()
    if [ -n "$extensions" ]; then
        IFS=',' read -r -a exts <<< "$extensions"
        for ext in ${exts[@]+"${exts[@]}"}; do
            ext="${ext// /}"
            [ -z "$ext" ] && continue
            if ! err="$(engine_exec "$MDB_ENGINE_NAME" psql -U postgres -d "$db" -q \
                -c "CREATE EXTENSION IF NOT EXISTS \"$ext\" CASCADE;" 2>&1 >/dev/null)"; then
                printf '  WARNING: extension %s could not be created: %s\n' "$ext" "${err:-no error output}" >&2
            fi
        done
    fi

    engine_exec "$MDB_ENGINE_NAME" psql -U postgres -d "$db" -q <<SQL >/dev/null 2>&1 || true
GRANT ALL ON SCHEMA public TO ${user};
GRANT USAGE ON SCHEMA public TO ${user}_readonly;
ALTER DEFAULT PRIVILEGES FOR ROLE ${user} IN SCHEMA public
    GRANT SELECT ON TABLES TO ${user}_readonly;
SQL
}

# The extensions each database has, one "db<TAB>ext,ext" line per database, in
# one exec rather than one per database. plpgsql is in every database and is
# left out.
hook_list_extensions() {
    local sql="SELECT string_agg(extname, ',' ORDER BY extname) FROM pg_extension WHERE extname <> 'plpgsql'"
    [ $# -gt 0 ] || return 0
    # shellcheck disable=SC2016 # expanded by the container's shell
    engine_exec "$MDB_ENGINE_NAME" sh -c \
        'q="$1"; shift; for d; do printf "%s\t%s\n" "$d" "$(psql -U postgres -d "$d" -tAc "$q")"; done' \
        sh "$sql" "$@" | tr -d '\r'
}

# Drop a project: its database, after ending the sessions on it, then both
# roles. Every step tolerates a part that is already gone, so a half-dropped
# project can be dropped again.
hook_drop_project() {
    local db="$1" user="$2" role
    engine_exec "$MDB_ENGINE_NAME" psql -U postgres -q -v ON_ERROR_STOP=1 \
        -c "SET client_min_messages = warning" \
        -c "DROP DATABASE IF EXISTS ${db} WITH (FORCE);" || return 1
    for role in "${user}_readonly" "${user}"; do
        engine_exec "$MDB_ENGINE_NAME" psql -U postgres -q -v ON_ERROR_STOP=1 <<SQL || return 1
DO \$mdb\$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${role}') THEN
        EXECUTE 'DROP OWNED BY ${role}';
        EXECUTE 'DROP ROLE ${role}';
    END IF;
END \$mdb\$;
SQL
    done
}
