#!/usr/bin/env bash
# shellcheck shell=bash
#
# Family hooks: MySQL and MariaDB (SPEC section 22.2).
#
# One implementation for both engines. Everything that differs between them --
# the client binary, the dump tool, the root secret -- comes from the
# descriptor, which is exactly what the descriptor is for.

# In the sidecar the engine is a remote host; from the host we are already
# exec'ing inside its container and there is no host to name.
_mysql_host_args() {
    if [ "${IN_CONTAINER:-0}" = "1" ]; then printf '%s\n%s\n' '-h' "$MDB_ENGINE_NAME"; fi
}

_mysql_args() {
    # _mdb_pw, not pw: `local pw` would shadow a caller's own $pw, and a
    # caller whose secret() returns "$pw" would read this empty local instead.
    local _mdb_pw; _mdb_pw="$(secret "$MDB_ENGINE_ROOT_SECRET")"
    local hargs=()
    while IFS= read -r a; do [ -n "$a" ] && hargs+=("$a"); done < <(_mysql_host_args)
    printf '%s\n' ${hargs[@]+"${hargs[@]}"} "-uroot" "-p$_mdb_pw"
}

_mysql_run() {
    local args=()
    while IFS= read -r a; do [ -n "$a" ] && args+=("$a"); done < <(_mysql_args)
    engine_exec "$MDB_ENGINE_NAME" "$MDB_ENGINE_CLIENT" ${args[@]+"${args[@]}"} "$@"
}

# Readiness. Deliberately NOT hook_list_databases: that answers "are there
# user databases", and a healthy fresh server has none, so using it as a
# readiness probe waits forever on a container that is already up.
hook_ping() {
    _mysql_run -N -B -e "SELECT 1" >/dev/null 2>&1
}

hook_list_databases() {
    _mysql_run -N -B -e \
        "SELECT schema_name FROM information_schema.schemata
         WHERE schema_name NOT IN ('information_schema','performance_schema','mysql','sys')" \
        2>/dev/null | tr -d '\r' | grep -v '^$'
}

# --routines and --events are the two the defaults silently omit, so a
# "complete" dump without them is quietly incomplete until you need a procedure.
hook_dump_database() {
    local db="$1" out="$2" dumper args=() pitr_args=()
    dumper="$(printf '%s' "$MDB_ENGINE_DUMP" | awk '{print $1}')"
    while IFS= read -r a; do [ -n "$a" ] && args+=("$a"); done < <(_mysql_args)

    # Record the binary-log coordinates the dump was taken at.
    #
    # Without them, point-in-time recovery has no safe place to start replaying
    # from: replaying the whole archive would re-apply transactions the dump
    # already contains, and guessing a later position would skip some. The flag
    # writes them as a COMMENT, so the dump still restores normally.
    #
    # --source-data replaced --master-data in MySQL 8.0.26 and MariaDB 10.5;
    # the descriptor says which to use rather than this branching on version.
    if [ "${MDB_MYSQL_PITR:-${MDB_MARIADB_PITR:-false}}" = "true" ]; then
        pitr_args=("--${MDB_ENGINE_DUMP_POSITION_FLAG:-source-data}=2")
    fi

    if [ -n "$(compress_ext)" ]; then
        engine_exec "$MDB_ENGINE_NAME" "$dumper" ${args[@]+"${args[@]}"} \
            --single-transaction --routines --triggers --events --quick \
            ${pitr_args[@]+"${pitr_args[@]}"} "$db" \
            | zstd -q -T0 -"${MDB_BACKUP_ZSTD_LEVEL:-9}" -o "$out" -f
    else
        engine_exec "$MDB_ENGINE_NAME" "$dumper" ${args[@]+"${args[@]}"} \
            --single-transaction --routines --triggers --events --quick \
            ${pitr_args[@]+"${pitr_args[@]}"} "$db" > "$out"
    fi
}

# MySQL has no equivalent of pg_dumpall --globals-only: grants live in the
# mysql schema, which is dumped as an ordinary database when included.
hook_dump_globals() { return 0; }

hook_recreate_database() {
    local db="$1"
    _mysql_run -e "DROP DATABASE IF EXISTS \`$db\`;
                   CREATE DATABASE \`$db\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" 2>/dev/null
}

hook_restore_database() {
    local db="$1"
    local args=()
    while IFS= read -r a; do [ -n "$a" ] && args+=("$a"); done < <(_mysql_args)
    engine_exec "$MDB_ENGINE_NAME" "$MDB_ENGINE_CLIENT" ${args[@]+"${args[@]}"} "$db"
}

# Tables present, as distinct from rows. A database with no tables was empty
# at dump time, which is a legitimate state; a database WITH tables but no
# rows means the restore lost the data. Only the second is a failure.
hook_object_count() {
    local db="$1"
    _mysql_run -N -B -e \
        "SELECT count(*) FROM information_schema.tables WHERE table_schema = '$db'" \
        2>/dev/null | tr -d ' \r'
}

# An exact count. information_schema.tables.table_rows is an InnoDB estimate,
# and MySQL 8 caches it for a day: a freshly restored table reports 0 rows, so
# verify-backups failed a perfectly good MySQL restore as "tables but no rows".
hook_row_count() {
    local db="$1" q="" t tables
    tables="$(_mysql_run -N -B -e \
        "SELECT table_name FROM information_schema.tables
         WHERE table_schema = '$db' AND table_type = 'BASE TABLE'" 2>/dev/null | tr -d '\r')" || true
    while IFS= read -r t; do
        [ -n "$t" ] || continue
        q="${q:+$q UNION ALL }SELECT COUNT(*) AS n FROM \`$db\`.\`$t\`"
    done <<EOF_TABLES
$tables
EOF_TABLES
    [ -n "$q" ] || { printf '0\n'; return 0; }
    _mysql_run -N -B -e "SELECT COALESCE(SUM(n), 0) FROM ($q) AS c" 2>/dev/null | tr -d ' \r'
}

hook_auth_enforced() {
    local hargs=()
    while IFS= read -r a; do [ -n "$a" ] && hargs+=("$a"); done < <(_mysql_host_args)
    if engine_exec "$MDB_ENGINE_NAME" "$MDB_ENGINE_CLIENT" ${hargs[@]+"${hargs[@]}"} \
        -h 127.0.0.1 -uroot -pmdb-throwaway-wrong-password -e "SELECT 1" >/dev/null 2>&1; then
        return 1
    fi
    return 0
}

# Provision a project: database, owner user, read-only companion.
#
# The readonly user gets its OWN password rather than sharing the owner's. They
# are separate principals, and giving both the same credential means a leak of
# the read-only one is a leak of the writable one.
hook_provision_project() {
    local db="$1" user="$2" pw="$3" ro_pw="${5:-$3}"
    local esc_pw="${pw//\'/\'\'}" esc_ro="${ro_pw//\'/\'\'}"

    _mysql_run <<SQL 2>/dev/null || return 1
CREATE DATABASE IF NOT EXISTS \`${db}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS '${user}'@'%' IDENTIFIED BY '${esc_pw}';
ALTER USER '${user}'@'%' IDENTIFIED BY '${esc_pw}';
GRANT ALL PRIVILEGES ON \`${db}\`.* TO '${user}'@'%';
CREATE USER IF NOT EXISTS '${user}_readonly'@'%' IDENTIFIED BY '${esc_ro}';
GRANT SELECT ON \`${db}\`.* TO '${user}_readonly'@'%';
FLUSH PRIVILEGES;
SQL
}
