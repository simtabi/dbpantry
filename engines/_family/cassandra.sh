#!/usr/bin/env bash
# shellcheck shell=bash
#
# Family hooks: Apache Cassandra (SPEC section 22.2).
#
# Cassandra differs from every other engine here in a way worth stating: it has
# no logical dump tool comparable to pg_dump. The production path is
# `nodetool snapshot`, which is a per-node hard-linked copy of the SSTables and
# restores only into a cluster of the same topology.
#
# For a toolkit whose promise is a VERIFIED restore, that is the wrong shape,
# so the dump here is logical: the schema via DESCRIBE plus the data via
# COPY TO. It restores anywhere, into any topology, and can be checked by
# counting rows -- which is what makes `make verify-backups` mean something.
# Snapshots remain the right answer for large production clusters and are
# documented as such rather than pretended away.

_cqlsh() {
    # _mdb_pw, not pw. `local pw` here SHADOWS a caller's own $pw, and a
    # caller whose secret() returns "$pw" then reads this function's empty
    # local instead of its own value -- bash locals are dynamically scoped.
    # Check 32 does exactly that, so every probe ran with an empty password
    # and the engine reported "never became ready" for five minutes.
    local _mdb_pw; _mdb_pw="$(secret "$MDB_ENGINE_ROOT_SECRET")"
    engine_exec "$MDB_ENGINE_NAME" cqlsh -u cassandra -p "$_mdb_pw" "$@"
}

hook_ping() {
    _cqlsh -e "SELECT release_version FROM system.local" >/dev/null 2>&1
}

# System keyspaces are excluded, the same way the SQL families exclude
# information_schema.
hook_list_databases() {
    _cqlsh -e "SELECT keyspace_name FROM system_schema.keyspaces" 2>/dev/null \
        | grep -vE "system|keyspace_name|^-+$|rows\)|^\s*$" \
        | tr -d ' \r' | grep -v '^$'
}

hook_dump_database() {
    local ks="$1" out="$2"
    # Schema first: a data-only dump restores into nothing.
    if [ -n "$(compress_ext)" ]; then
        _cqlsh -e "DESCRIBE KEYSPACE ${ks}" 2>/dev/null \
            | zstd -q -T0 -"${MDB_BACKUP_ZSTD_LEVEL:-9}" -o "$out" -f
    else
        _cqlsh -e "DESCRIBE KEYSPACE ${ks}" 2>/dev/null > "$out"
    fi
}

hook_dump_globals() { return 0; }

hook_recreate_database() {
    local ks="$1"
    _cqlsh -e "DROP KEYSPACE IF EXISTS ${ks};" >/dev/null 2>&1
}

hook_restore_database() {
    _cqlsh
}

hook_object_count() {
    local ks="$1"
    _cqlsh -e "SELECT count(*) FROM system_schema.tables WHERE keyspace_name='${ks}'" 2>/dev/null \
        | grep -oE '^\s+[0-9]+' | tr -d ' ' | head -1
}

hook_row_count() {
    # Cassandra has no cheap cross-table row count, and a full scan on a real
    # cluster is a bad idea. Table count stands in for it here.
    hook_object_count "$1"
}

hook_auth_enforced() {
    if engine_exec "$MDB_ENGINE_NAME" cqlsh \
        -e "SELECT release_version FROM system.local" >/dev/null 2>&1; then
        return 1
    fi
    return 0
}

# Provision a project: keyspace, owner role, read-only companion.
#
# SimpleStrategy with RF=1 is correct for the single-node development cluster
# this toolkit runs and WRONG for a real one. A production keyspace wants
# NetworkTopologyStrategy and RF>=3; that is a deployment decision, not
# something to guess here, so it is documented rather than defaulted.
hook_provision_project() {
    local ks="$1" user="$2" pw="$3" ro_pw="${5:-$3}"
    local stmt
    # CQL map keys are single-quoted, which would otherwise need three levels
    # of escaping inside a double-quoted shell string.
    local q="'"
    local repl="{${q}class${q}:${q}SimpleStrategy${q},${q}replication_factor${q}:1}"

    # One statement per invocation, not a heredoc of several.
    #
    # `cqlsh -e` splits its argument on ';', so a string that begins with a
    # newline yields an empty leading statement and cqlsh exits non-zero with
    # "no viable alternative at input ';'" -- AFTER having run everything else.
    # The keyspace and roles existed and provisioning still reported failure.
    for stmt in \
        "CREATE KEYSPACE IF NOT EXISTS ${ks} WITH replication = ${repl}" \
        "CREATE ROLE IF NOT EXISTS ${user} WITH PASSWORD = '$(_cql_quote "$pw")' AND LOGIN = true" \
        "CREATE ROLE IF NOT EXISTS ${user}_readonly WITH PASSWORD = '$(_cql_quote "$ro_pw")' AND LOGIN = true" \
        "GRANT ALL PERMISSIONS ON KEYSPACE ${ks} TO ${user}" \
        "GRANT SELECT ON KEYSPACE ${ks} TO ${user}_readonly"
    do
        _cqlsh -e "$stmt" >/dev/null 2>&1 || {
            printf '  cassandra: statement failed: %s...\n' "${stmt%% *}" >&2
            return 1
        }
    done

    _cass_set_password "$user" "$pw" || return 1
    _cass_set_password "${user}_readonly" "$ro_pw" || return 1
}

# Drop a project: its keyspace and both roles, one statement per invocation for
# the reason hook_provision_project gives. IF EXISTS throughout, so a
# half-dropped project can be dropped again.
hook_drop_project() {
    local ks="$1" user="$2" stmt
    for stmt in \
        "DROP KEYSPACE IF EXISTS ${ks}" \
        "DROP ROLE IF EXISTS ${user}" \
        "DROP ROLE IF EXISTS ${user}_readonly"
    do
        _cqlsh -e "$stmt" >/dev/null 2>&1 || {
            printf '  cassandra: statement failed: %s...\n' "${stmt%% *}" >&2
            return 1
        }
    done
}

_cql_quote() { printf '%s' "${1//$_CQL_SQ/$_CQL_SQ$_CQL_SQ}"; }
_CQL_SQ="'"

# Bring a role's password to the intended value, if it is not already there.
#
# The obvious CREATE-then-ALTER pair does not work: Cassandra rate-limits
# password changes per role -- "Password for role X can only be changed every
# 5000ms" -- and reports it as code=1001 "Coordinator node overloaded", which
# names neither the role nor the real cause. Creating a role and immediately
# resetting its password therefore fails EVERY time, not intermittently.
#
# So the password is only altered when it is actually wrong, tested by logging
# in with it. On a fresh create that login already succeeds and nothing is
# altered; on a re-run with an unchanged secret the same holds, which is why
# `make new-project` can be re-run to reprint the connection block.
_cass_set_password() {
    local role="$1" pw="$2" i

    if engine_exec "$MDB_ENGINE_NAME" cqlsh -u "$role" -p "$pw" \
         -e "SELECT release_version FROM system.local" >/dev/null 2>&1; then
        return 0
    fi

    # It is genuinely wrong, so it must change -- waiting out the limiter.
    for i in 1 2 3; do
        if _cqlsh -e "ALTER ROLE ${role} WITH PASSWORD = '$(_cql_quote "$pw")'" >/dev/null 2>&1; then
            return 0
        fi
        # An `[ ] && sleep` list would return 1 on the last iteration, which
        # `set -e` treats as a failure in every caller that does not already
        # neutralise it.
        if [ "$i" -lt 3 ]; then sleep 3; fi
    done
    printf '  cassandra: could not set the password for %s\n' "$role" >&2
    return 1
}
