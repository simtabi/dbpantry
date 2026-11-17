#!/usr/bin/env bash
# verify: new-project's PostgreSQL extension loop runs each CREATE EXTENSION with the default IFS, so the compose command still splits into words
# tags: structure pg extensions
# phase: 2

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Until 2026-11-17 hook_provision_project set IFS=',' around its extension
# loop and called engine_exec inside it. engine_exec reaches mdb_compose, which
# splits MDB_COMPOSE_CMD ("docker compose") with `read -a`; under IFS=',' that
# is one word, a command that does not exist, so every requested extension
# "could not be created" although the image ships it. Check 09 needs a running
# engine; this one needs none. It sources the hook with a stub engine_exec that
# splits a two-word command exactly as mdb_compose does and fails unless it gets
# two words, then counts the CREATE EXTENSION calls that got through.
cd "$MDB_ROOT" || vfail "no repository root"
calls="$(mktemp "${TMPDIR:-/tmp}/mdb-ifs.XXXXXX")"; add_cleanup "rm -f '$calls'"

out="$(
    MDB_ENGINE_NAME=pg
    engine_exec() {
        local -a c
        read -r -a c <<< "docker compose"
        [[ "${#c[@]}" == 2 ]] || return 127
        cat >/dev/null 2>&1 || true
        case "$*" in *"CREATE EXTENSION"*) printf '%s\n' "$*" >> "$calls" ;; esac
        return 0
    }
    # shellcheck source=../../engines/_family/postgres.sh
    . "$MDB_ROOT/engines/_family/postgres.sh"
    hook_provision_project shop shop_user secret "vector, pg_stat_statements" </dev/null 2>&1
)" || vfail "hook_provision_project failed: $out"

grep -q 'could not be created' <<<"$out" && vfail "an extension was reported as not created: $out"
n="$(grep -c 'CREATE EXTENSION' "$calls" || true)"
[[ "$n" == 2 ]] || vfail "expected 2 CREATE EXTENSION calls for \"vector, pg_stat_statements\", got $n"
vinfo "2 of 2 extensions reached engine_exec with a two-word compose command"
