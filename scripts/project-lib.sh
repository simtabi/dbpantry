# shellcheck shell=bash
#
# What the project scripts share (scripts/new-project, scripts/project): the
# name rules, resolving an engine by its name, the environment the family
# hooks are written against, and where a project's password files live.
#
# The caller sets ROOT, defines log and die, and has sourced backend-lib.sh.

# shellcheck source=engine-lib.sh
. "$ROOT/scripts/engine-lib.sh"

# Names every family keeps for itself. A project named after one would share
# the server's own database, and dropping it would drop that.
PROJECT_RESERVED_NAMES=" postgres template0 template1 mysql sys information_schema performance_schema admin local config "

# Identifiers go into SQL and CQL, so they are constrained rather than escaped.
# A name that needs quoting is a name that will cause trouble in a connection
# string later anyway.
project_check_name() {
    local name="$1"
    [[ -n "$name" ]] || die "specify --name <project>"
    [[ "$name" =~ ^[a-z][a-z0-9_]{0,62}$ ]] \
        || die "invalid name '$name': use lowercase letters, digits and underscores, starting with a letter"
    [[ "$PROJECT_RESERVED_NAMES" != *" $name "* && "$name" != system && "$name" != system_* ]] \
        || die "'$name' is a name the engines keep for themselves; choose another"
}

# project_load_engine ENGINE loads the descriptor and hooks of ENGINE and sets
# ENGINE to its MDB_ENGINE_NAME. Descriptors are keyed by directory name and
# engines are addressed by MDB_ENGINE_NAME; `pg` is the descriptor in
# engines/postgres/, so a lookup by directory alone would reject the toolkit's
# own default.
project_load_engine() {
    local want="$1" candidate resolved=""
    for candidate in $(engine_list); do
        engine_load "$candidate" || continue
        if [[ "$MDB_ENGINE_NAME" == "$want" || "$candidate" == "$want" ]]; then
            resolved="$candidate"
            break
        fi
    done
    [[ -n "$resolved" ]] || die "unknown engine: $want (have: $(engine_list | tr '\n' ' '))"
    engine_load "$resolved" || die "could not load the descriptor for $resolved"
    ENGINE="$MDB_ENGINE_NAME"
    engine_load_hooks || die "no hooks for engine family ${MDB_ENGINE_FAMILY:-?}"
}

# project_require_hook HOOK fails unless the loaded family defines HOOK.
project_require_hook() {
    command -v "$1" >/dev/null 2>&1 || die "the ${MDB_ENGINE_FAMILY} family defines no $1"
}

# -----------------------------------------------------------------------------
# The hook contract's environment
# -----------------------------------------------------------------------------
# Same shape as scripts/backup: hooks call engine_exec and secret rather than
# knowing whether they are inside a container or on the host.
IN_CONTAINER="${MDB_IN_CONTAINER:-0}"

engine_exec() {
    local engine="$1"; shift
    if [[ "$IN_CONTAINER" == "1" ]]; then
        "$@"
    else
        ( cd "$ROOT" && mdb_svc_exec "$engine" "$@" )
    fi
}

secret() {
    local file="$1"
    if [[ "$IN_CONTAINER" == "1" ]]; then
        [[ -r "/run/secrets/$file" ]] || die "secret not readable: /run/secrets/$file"
        tr -d '\n' < "/run/secrets/$file"
    else
        [[ -r "$MDB_HOME/secrets/$file" ]] || die "secret not readable: secrets/$file"
        tr -d '\n' < "$MDB_HOME/secrets/$file"
    fi
}

# Only the dump path compresses; the project scripts make no artefact. Defined
# because the family files are shared and may reference it at source time.
compress_ext() { printf ''; }

# -----------------------------------------------------------------------------
# Password files
# -----------------------------------------------------------------------------
# A fixture's projects (MDB_FIXTURE=1) keep their password files in
# secrets/fixture/, which `fixture down` removes with the fixture's tmpfs data.
# The stack's projects keep them in secrets/. Both are under the instance home
# (design.md D-84).
#
# project_secret_files NAME sets USER_NAME, SECRET_FILE and RO_SECRET_FILE for
# NAME on the loaded ENGINE.
# shellcheck disable=SC2034 # set for the caller
project_secret_files() {
    local dir="$MDB_HOME/secrets"
    [[ "${MDB_FIXTURE:-0}" == "1" ]] && dir="$MDB_HOME/secrets/fixture"
    USER_NAME="${1}_user"
    SECRET_FILE="${dir}/${ENGINE}_${USER_NAME}_password.txt"
    RO_SECRET_FILE="${dir}/${ENGINE}_${USER_NAME}_readonly_password.txt"
}

# project_rel PATH prints PATH relative to the instance home, as secrets/<file>,
# so output reads the same whichever home is in use.
project_rel() { printf '%s' "${1#"$MDB_HOME"/}"; }

# json_str VALUE prints VALUE as a JSON string. Every value the project scripts
# print is a constrained identifier, a path, or a .env block, so escaping
# backslash, quote, newline and tab is the whole of JSON's rule that applies.
json_str() {
    local v="$1"
    v="${v//\\/\\\\}"; v="${v//\"/\\\"}"; v="${v//$'\n'/\\n}"; v="${v//$'\t'/\\t}"
    printf '"%s"' "$v"
}
