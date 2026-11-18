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

# --- the instance home (design.md D-84) --------------------------------------
#
# The KIT is what ships: scripts/, the compose files, engines/, images/,
# config/ templates. The INSTANCE is what `make init` and `make up` create:
# .env, secrets/, certs/, backups/, the generated compose files
# (compose.engines.yml, compose.prod.engines.yml, compose.proxy.yml) and the
# rendered config (config/caddy/Caddyfile, servers.json, layer4.caddy).
#
# MDB_HOME is where the instance lives. It defaults to the kit itself, so a
# checkout behaves exactly as it always has. Set it to another directory (the
# dbpantry binary sets ~/.config/dbpantry) and nothing is written into the kit.
# It is always absolute: Compose interpolates ${MDB_HOME:-.} into bind-mount
# sources, and a relative value would resolve against the project directory
# (the kit), not the caller's directory.
#
# Exported only when the caller set it. A defaulted home is this kit's, and a
# child script in ANOTHER kit -- the verify harness's scratch stacks are
# copies of the checkout -- must default to its own, not inherit the
# operator's .env and secrets/.
#
# The kit is this file's own checkout, never an inherited MDB_RT_ROOT: a
# script run in a copy of the tree (check 71's render copy, a scratch stack)
# inherits the harness's MDB_RT_ROOT, and a home that followed it would write
# the copy's generated files into the operator's checkout.
_mdb_kit_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." 2>/dev/null && pwd)"
#
# This file is sourced more than once in one process (backend-lib.sh sources
# runtime-lib.sh, which sources it, and then sources it again), so a home this
# file defaulted earlier is recognised and defaulted again, never mistaken on
# the second pass for one the caller set and exported.
if [[ -z "${MDB_HOME:-}" || "$MDB_HOME" == "${_mdb_home_defaulted:-}" ]]; then
    MDB_HOME="$_mdb_kit_dir"
    _mdb_home_defaulted="$MDB_HOME"
else
    if [[ "$MDB_HOME" != /* ]]; then
        if [[ -d "$MDB_HOME" ]]; then MDB_HOME="$(cd "$MDB_HOME" && pwd)"; else MDB_HOME="$PWD/${MDB_HOME#./}"; fi
    fi
    MDB_HOME="${MDB_HOME%/}"
    [[ -n "$MDB_HOME" ]] || MDB_HOME=/
    export MDB_HOME
fi

# Does the instance live apart from the kit? Callers that must pass the home
# to Compose explicitly (an --env-file, absolute -f paths) ask this. False in
# a child that never sourced this file (an exported function in `bash -c`),
# where MDB_HOME is unset unless the caller exported one.
mdb_home_separate() {
    [[ -n "${MDB_HOME:-}" && "$MDB_HOME" != "${_mdb_kit_dir:-${MDB_RT_ROOT:-}}" ]]
}

# The instance directory the libraries (runtime-lib.sh, the backends) use:
# the home when it is separate, else MDB_RT_ROOT, as before D-84 (tests point
# the runtime library at a fixture tree with its own .env and secrets/), else
# the home. Scripts that resolve their own ROOT use $MDB_HOME directly.
mdb_instance_dir() {
    if mdb_home_separate; then
        printf '%s\n' "$MDB_HOME"
    else
        printf '%s\n' "${MDB_RT_ROOT:-${MDB_HOME:-.}}"
    fi
}

# The .env a reader uses when it is given none.
mdb_env_default_file() {
    printf '%s/.env\n' "$(mdb_instance_dir)"
}

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

# The value of KEY in FILE (default: mdb_env_default_file): the last
# assignment wins, as in Compose. Prints nothing when it is absent.
mdb_env_get() {
    local key="$1" file="${2:-$(mdb_env_default_file)}" raw
    [[ -f "$file" ]] || return 0
    raw="$(grep -E "^${key}=" "$file" 2>/dev/null | tail -1 || true)"
    [[ -n "$raw" ]] || return 0
    mdb_env_unquote "${raw#*=}"
}

# Where dumps live: MDB_BACKUP_DIR from the environment first (the backup
# sidecar sets /backups), then from the env file, then ./backups. A relative
# value is relative to the checkout, so a command run from a subdirectory
# writes, verifies and prunes the same directory the operator configured.
mdb_backup_dir() {
    local root="$1" file="$2" dir
    dir="${MDB_BACKUP_DIR:-}"
    [[ -n "$dir" ]] || dir="$(mdb_env_get MDB_BACKUP_DIR "$file")"
    [[ -n "$dir" ]] || dir="backups"
    [[ "$dir" == /* ]] || dir="$root/${dir#./}"
    printf '%s' "$dir"
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

export -f mdb_home_separate mdb_instance_dir mdb_env_default_file mdb_backup_dir _mdb_env_unsafe mdb_env_unquote mdb_env_get mdb_env_quote mdb_env_needs_quotes 2>/dev/null || true

# The images dbpantry builds are named <prefix>/dbpantry-<component>:<tag>.
# The prefix is MDB_IMAGE_PREFIX, or localhost when it is unset (design.md
# D-78): a fully qualified local name, so Docker, Podman and nerdctl resolve it
# the same way (check 49), it never falls back to a remote registry, and kind or
# minikube can load it. Set MDB_IMAGE_PREFIX to your own registry (for example
# registry.example.com/team) to build, run and deploy under that name instead.
# Compose files, the Helm chart, the Makefile and the patroni Dockerfile carry
# the same default; check 73 fails if any of them names another.
MDB_IMAGE_PREFIX_DEFAULT=localhost

# The prefix in force, without a trailing slash.
mdb_image_prefix() {
    local p="${MDB_IMAGE_PREFIX:-$MDB_IMAGE_PREFIX_DEFAULT}"
    printf '%s\n' "${p%/}"
}

# The full name of one of our images: mdb_image pg 17 -> localhost/dbpantry-pg:17
mdb_image() {
    printf '%s/dbpantry-%s:%s\n' "$(mdb_image_prefix)" "$1" "$2"
}

# A reference written with the default prefix (an engine descriptor's
# localhost/dbpantry-pgbouncer:dev) under the prefix in force. Any other
# reference, such as an upstream image, is returned unchanged.
mdb_image_ref() {
    case "$1" in
        "$MDB_IMAGE_PREFIX_DEFAULT"/dbpantry-*) printf '%s/%s\n' "$(mdb_image_prefix)" "${1#"$MDB_IMAGE_PREFIX_DEFAULT"/}" ;;
        *) printf '%s\n' "$1" ;;
    esac
}

# Resolve a Compose-style reference the way Compose does: every ${VAR:-default}
# becomes VAR from the environment, or the default when it is unset or empty,
# innermost first. For a script that reads an image: line out of a Compose file
# and runs it directly, so ${MDB_IMAGE_PREFIX:-localhost}/dbpantry-caddy:dev
# becomes localhost/dbpantry-caddy:dev (or the prefix in force).
mdb_compose_ref() {
    # shellcheck disable=SC2016  # a pattern for a literal ${...}, not an expansion
    local ref="$1" re='\$\{([A-Za-z_][A-Za-z0-9_]*):-([^${}]*)\}' name fallback val
    while [[ "$ref" =~ $re ]]; do
        name="${BASH_REMATCH[1]}"; fallback="${BASH_REMATCH[2]}"
        val="${!name:-$fallback}"
        ref="${ref/"${BASH_REMATCH[0]}"/$val}"
    done
    printf '%s\n' "$ref"
}
