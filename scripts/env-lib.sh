#!/usr/bin/env bash
# shellcheck shell=bash
#
# Reading and writing dotenv files (.env, .env.example, .env.prod, rendered
# blocks), one way everywhere.
#
# The convention (CLAUDE.md, .env.example header): a value containing
# whitespace or a character the shell or Compose would interpret is written in
# double quotes -- TENANTS="landlord panel api socket queue". Compose and
# `set -a; . file` understand that already; the many `grep | cut -d= -f2-`
# readers in scripts/ did not, and would have handed the quotes to the caller
# as part of the value. Everything reads through mdb_env_get now.
#
# Sourced, never executed. bash 3.2 compatible.

# Strip one layer of dotenv quoting from a raw value.
#   "a b"        -> a b        (\" \$ \` and \\ unescaped, as the shell does)
#   'a b'        -> a b        (literal)
#   "a b" # note -> a b        (a comment after the closing quote is dropped)
#   plain        -> plain      (unchanged, so existing files read as before)
mdb_env_unquote() {
    local v="$1"
    v="${v#"${v%%[![:space:]]*}"}"   # leading whitespace
    case "$v" in
        \"*\"*)
            v="${v#\"}"; v="${v%\"*}"
            v="${v//\\\"/\"}"; v="${v//\\\$/\$}"; v="${v//\\\`/\`}"; v="${v//\\\\/\\}"
            ;;
        \'*\'*)
            v="${v#\'}"; v="${v%\'*}"
            ;;
        *)
            v="${v%"${v##*[![:space:]]}"}"   # trailing whitespace
            ;;
    esac
    printf '%s\n' "$v"
}

# The value of KEY in FILE (default: the checkout's .env): the last
# assignment wins, as in Compose. Prints nothing when it is absent.
mdb_env_get() {
    local key="$1" file="${2:-${MDB_RT_ROOT:-.}/.env}" raw
    [[ -f "$file" ]] || return 0
    raw="$(grep -E "^${key}=" "$file" 2>/dev/null | tail -1 || true)"
    [[ -n "$raw" ]] || return 0
    mdb_env_unquote "${raw#*=}"
}

# Bare values may use only the characters a dotenv parser can never misread:
# letters, digits and _ . / : @ % + = -. Anything else (whitespace, a comma
# list such as pg,ui, quotes, $, #, ;) is quoted. The same allow-list as the
# owner's global sim-standards pre-commit hook, so the two never disagree.
_mdb_env_unsafe() {
    local re='[^A-Za-z0-9_./:@%+=-]'
    [[ "$1" =~ $re ]]
}

# A value as it should be WRITTEN: double-quoted, with " \ $ ` escaped, when
# it contains anything outside the safe set below; bare otherwise. Anything that
# emits KEY=value lines (new-project's block, renderers) goes through this.
mdb_env_quote() {
    local v="$1"
    if _mdb_env_unsafe "$v"; then
        v="${v//\\/\\\\}"; v="${v//\"/\\\"}"; v="${v//\$/\\\$}"; v="${v//\`/\\\`}"
        printf '"%s"\n' "$v"
    else
        printf '%s\n' "$v"
    fi
}

# Does a raw value (everything after the first =) follow the convention?
# Used by check 50.
mdb_env_needs_quotes() {
    local v="$1"
    case "$v" in \"*\"*|\'*\'*|'') return 1 ;; esac
    _mdb_env_unsafe "$v"
}

export -f _mdb_env_unsafe mdb_env_unquote mdb_env_get mdb_env_quote mdb_env_needs_quotes 2>/dev/null || true
