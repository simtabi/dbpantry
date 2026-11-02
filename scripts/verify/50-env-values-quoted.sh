#!/usr/bin/env bash
# verify: env-file values with spaces or special characters are double-quoted, and every reader unquotes them
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# The convention (.env.example header, CLAUDE.md): TENANTS="a b c", not
# TENANTS=a b c. Compose would cut the bare form at the first space or #, and a
# script sourcing the file would run "b" as a command. The other half is the
# readers: a dozen scripts read values with grep | cut, which handed the quotes
# back as part of the value until they all went through scripts/env-lib.sh.

cd "$MDB_ROOT" || exit 1

# --- 1. the files follow the convention --------------------------------------
# Tracked env files are the contract. A local .env / .env.prod is checked too
# when present, because it is where an operator adds a value with a space.
bad=0
for f in .env.example .env .env.prod; do
    [[ -f "$f" ]] || continue
    while IFS= read -r line; do
        num="${line%%:*}"; kv="${line#*:}"; kv="${kv#\#}"
        key="${kv%%=*}"; raw="${kv#*=}"
        if mdb_env_needs_quotes "$raw"; then
            printf '      %s:%s %s=%s should be %s=%s\n' "$f" "$num" "$key" "$raw" "$key" "$(mdb_env_quote "$raw")" >&2
            bad=$(( bad + 1 ))
        fi
    done < <(grep -nE '^#?[A-Z_][A-Z0-9_]*=' "$f")
done
(( bad == 0 )) || vfail "$bad env value(s) with spaces or special characters are not double-quoted"
vinfo "every env value with spaces or special characters is double-quoted"

# --- 2. the parser ------------------------------------------------------------
t="$(mktemp "${TMPDIR:-/tmp}/mdb-envq.XXXXXX")"
add_cleanup "rm -f '$t'"
cat > "$t" <<'ENV'
PLAIN=pg,ui
TENANTS="landlord panel api socket queue"
SINGLE='a $literal'
ESCAPED="say \"hi\" \\ there"
COMMENTED="a b" # trailing note
EMPTY=
LAST=first
LAST="second value"
ENV
want() {
    local key="$1" exp="$2" got
    got="$(mdb_env_get "$key" "$t")"
    [[ "$got" == "$exp" ]] || vfail "mdb_env_get $key: expected [$exp], got [$got]"
}
want PLAIN    'pg,ui'
want TENANTS  'landlord panel api socket queue'
# shellcheck disable=SC2016  # literal dollar on purpose
want SINGLE   'a $literal'
want ESCAPED  'say "hi" \ there'
want COMMENTED 'a b'
want EMPTY    ''
want LAST     'second value'
want MISSING  ''
vinfo "mdb_env_get unquotes double, single, escaped and commented values; last assignment wins"

# Quote and read back is the identity, for the values generators emit.
# shellcheck disable=SC2016  # literal dollar on purpose
for v in 'pg,ui' 'landlord panel api socket queue' 'p@ss$word"x' 'semi;colon' '127.0.0.1'; do
    printf 'K=%s\n' "$(mdb_env_quote "$v")" > "$t"
    got="$(mdb_env_get K "$t")"
    [[ "$got" == "$v" ]] || vfail "mdb_env_quote/mdb_env_get round trip changed [$v] into [$got]"
done
vinfo "mdb_env_quote output reads back unchanged"

# The safe set is the owner's sim-standards hook's: a comma list is quoted,
# a host:port or path is not.
[[ "$(mdb_env_quote 'pg,ui')" == '"pg,ui"' ]] || vfail "mdb_env_quote left a comma list bare"
[[ "$(mdb_env_quote 'db:5432')" == 'db:5432' ]] || vfail "mdb_env_quote quoted a host:port"
vinfo "comma lists are quoted, host:port and paths stay bare"

# The same file is what Compose and `set -a; . file` see: they must agree.
printf 'TENANTS="landlord panel api socket queue"\nMDB_PROFILES="pg,ui"\n' > "$t"
# shellcheck disable=SC2016  # expanded by the inner bash
sourced="$(env -i bash -c 'set -a; . "$1"; printf %s "$TENANTS"' _ "$t")"
[[ "$sourced" == "$(mdb_env_get TENANTS "$t")" ]] || vfail "the shell and mdb_env_get read TENANTS differently"

# make reads the env file too (project name, profiles).
pmk="$(mktemp "${TMPDIR:-/tmp}/mdb-envq-mk.XXXXXX")"
add_cleanup "rm -f '$pmk'"
# shellcheck disable=SC2016  # make syntax, not shell
printf '_mdb_print_profiles:\n\t@echo $(env_profiles)\n' > "$pmk"
got="$(env -u COMPOSE_PROFILES make -s --no-print-directory -f Makefile -f "$pmk" ENV_FILE="$t" _mdb_print_profiles 2>&1 || true)"
[[ "$got" == "pg,ui" ]] || vfail "make read MDB_PROFILES=\"pg,ui\" as [$got]"
vinfo "the shell, make and mdb_env_get agree on quoted values"

# --- 3. no reader bypasses it -------------------------------------------------
# A new `grep ^KEY= .env | cut -d= -f2-` would quietly return the quotes again.
raw="$(grep -nE 'cut -d= -f2' Makefile scripts/* scripts/verify/*.sh 2>/dev/null \
        | grep -vE '^[^:]+:[0-9]+:[[:space:]]*#' \
        | grep -vE 'openssl x509|MDB_ENGINE_NAME|mdb_env_unquote|^scripts/env-lib\.sh:|^scripts/verify/50-' || true)"
[[ -z "$raw" ]] || { printf '%s\n' "$raw" | sed 's/^/      /' >&2; vfail "an env file is read with cut instead of mdb_env_get"; }
vinfo "every env reader goes through scripts/env-lib.sh"
