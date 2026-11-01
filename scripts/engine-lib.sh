#!/usr/bin/env bash
# shellcheck shell=bash
#
# The engine descriptor loader (SPEC section 22.1).
#
# Everything generic in the toolkit reads engines through this file. The rule
# that keeps the abstraction real: if something can be expressed in a
# descriptor, it must NOT appear as an `if engine == ...` branch anywhere else.
#
# Usage:
#   source scripts/engine-lib.sh
#   engine_load pg              # populates MDB_ENGINE_* for that engine
#   for e in $(engine_list); do ... done

# Deliberately no `set -e` here: this is sourced by scripts that set their own
# options, and overriding a caller's shell settings from a library is rude.

MDB_ENGINES_DIR="${MDB_ENGINES_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../engines" 2>/dev/null && pwd)}"

# Keys every descriptor must declare. A missing one is a descriptor bug, and
# check 31 fails on it rather than letting the gap surface as a confusing
# runtime error three layers away.
# shellcheck disable=SC2034  # consumed by scripts that source this library
MDB_ENGINE_REQUIRED_KEYS="
MDB_ENGINE_NAME
MDB_ENGINE_TITLE
MDB_ENGINE_FAMILY
MDB_ENGINE_PARADIGM
MDB_ENGINE_VERSIONS
MDB_ENGINE_DEFAULT_VERSION
MDB_ENGINE_PORT
MDB_ENGINE_DATA_DIR
MDB_ENGINE_USER
MDB_ENGINE_ROOT_SECRET
MDB_ENGINE_PING
MDB_ENGINE_CLIENT
MDB_ENGINE_DUMP
MDB_ENGINE_RESTORE
MDB_ENGINE_BACKUP_EXT
MDB_ENGINE_AUTH_METHOD
MDB_ENGINE_POOLING
MDB_ENGINE_LICENSE
MDB_ENGINE_OSI_APPROVED
"

# Every engine directory that carries a descriptor, in a stable order.
engine_list() {
    local d
    for d in "$MDB_ENGINES_DIR"/*/engine.conf; do
        [ -e "$d" ] || continue
        # The engine's NAME, not its directory: the directory is "postgres"
        # while the engine everything else refers to is "pg".
        grep -m1 '^MDB_ENGINE_NAME=' "$d" | cut -d= -f2- | tr -d '"'
    done | sort
}

# Map an engine name back to its descriptor path.
engine_conf_path() {
    local want="$1" d name
    for d in "$MDB_ENGINES_DIR"/*/engine.conf; do
        [ -e "$d" ] || continue
        name="$(grep -m1 '^MDB_ENGINE_NAME=' "$d" | cut -d= -f2- | tr -d '"')"
        if [ "$name" = "$want" ]; then printf '%s\n' "$d"; return 0; fi
    done
    return 1
}

# Load one engine's descriptor into the environment.
#
# Previously loaded MDB_ENGINE_* values are cleared first, so loading engine B
# after engine A cannot leave A's optional keys visible and silently wrong.
engine_load() {
    local want="$1" conf key
    conf="$(engine_conf_path "$want")" || {
        printf 'engine-lib: no descriptor for engine "%s" in %s\n' "$want" "$MDB_ENGINES_DIR" >&2
        return 1
    }

    for key in $(set | grep -oE '^MDB_ENGINE_[A-Z0-9_]+' | sort -u); do
        case "$key" in
            MDB_ENGINE_REQUIRED_KEYS|MDB_ENGINES_DIR) continue ;;
        esac
        unset "$key" 2>/dev/null || true
    done

    # shellcheck disable=SC1090  # path resolved at runtime from the engine name
    . "$conf"
    MDB_ENGINE_CONF="$conf"
    MDB_ENGINE_DIR="$(dirname "$conf")"
    export MDB_ENGINE_CONF MDB_ENGINE_DIR
}

# Load the family hooks for the currently loaded engine.
#
# Hooks are per FAMILY, not per engine: MySQL and MariaDB share one
# implementation because everything that differs between them -- client binary,
# dump tool, root secret -- comes from the descriptor.
#
# The caller must already provide engine_exec, secret and compress_ext; the
# hooks are written against that contract.
engine_load_hooks() {
    local family="${MDB_ENGINE_FAMILY:-}"
    [ -n "$family" ] || { printf 'engine-lib: no engine loaded\n' >&2; return 1; }

    local hooks="$MDB_ENGINES_DIR/_family/${family}.sh"
    [ -f "$hooks" ] || {
        printf 'engine-lib: no hooks for family "%s" (expected %s)\n' "$family" "$hooks" >&2
        return 1
    }
    # shellcheck disable=SC1090  # path resolved at runtime from the family
    . "$hooks"
}

# Does this engine declare a capability?
engine_has_external_pooler() { [ "${MDB_ENGINE_POOLING:-}" = "external" ]; }
engine_is_heavy()            { [ "${MDB_ENGINE_HEAVY:-false}" = "true" ]; }
engine_tls_always()          { [ "${MDB_ENGINE_TLS_ALWAYS:-false}" = "true" ]; }

# Is a version on this engine's supported menu?
#
# The menu holds MAINTAINED versions only: the newest supported line and the one
# before it (DESIGN.md D-57, docs/versions.md). A version off the menu is not
# refused outright. It runs from MDB_<ENGINE>_BASE_IMAGE, unsupported, and
# check-env says so; see engine_base_override.
engine_supports_version() {
    local want="$1" v
    for v in ${MDB_ENGINE_VERSIONS:-}; do
        [ "$v" = "$want" ] && return 0
    done
    return 1
}

# The digest-pinned upstream base for a maintained version, from
# images/bases.tsv. Prints nothing for a version that is not maintained.
engine_pinned_base() {
    awk -v e="$1" -v v="$2" '$1==e && $2==v {print $3; exit}' \
        "$MDB_ENGINES_DIR/../images/bases.tsv" 2>/dev/null || true
}

# The name of an engine's base-image override variable: MDB_<ENGINE>_BASE_IMAGE.
engine_base_override_var() {
    printf 'MDB_%s_BASE_IMAGE' "$(printf '%s' "$1" | tr '[:lower:]-' '[:upper:]_')"
}

# Is a base-image override acceptable? It must be pinned by digest, the same
# rule images/bases.tsv follows, so an unsupported version is still a
# reproducible one: name[:tag]@sha256:<64 hex>.
engine_valid_base_ref() {
    printf '%s' "$1" | grep -qE '^[a-z0-9][a-z0-9._/:-]*@sha256:[0-9a-f]{64}$'
}

# Is a version string safe to put in an image tag and a volume name?
engine_valid_version() {
    printf '%s' "$1" | grep -qE '^[0-9]+(\.[0-9]+)?$'
}

# Engines whose descriptor says they need an external pooler, for compose
# generation and for the prod guard in check-env.
engine_list_with_pooler() {
    local e
    for e in $(engine_list); do
        ( engine_load "$e" >/dev/null 2>&1 && engine_has_external_pooler && printf '%s\n' "$e" )
    done
}
