#!/usr/bin/env bash
# verify: every engine family can provision a project through its hook
# tags: provisioning engines
# phase: 6

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# SPEC section 1: "a new project connects in under a minute", for every engine
# rather than for the three that happened to have a case in a switch statement.
#
# new-project used to branch on the engine and supported pg, mysql and mariadb.
# The other three silently fell through to "unknown engine" -- an engine could
# be fully supported everywhere else and have no way to create a project on it,
# and nothing said so.
#
# This asserts the contract, not the implementation: every family defines
# hook_provision_project, every descriptor carries what the printed connection
# block needs, and the hook is idempotent.

# shellcheck source=../engine-lib.sh
. "$MDB_ROOT/scripts/engine-lib.sh"

missing=0
checked=0

while IFS= read -r engine; do
    [[ -z "$engine" ]] && continue
    engine_load "$engine" || vfail "could not load the descriptor for '$engine'"

    # A hook file is per FAMILY, so several engines share one. Loading them in
    # a subshell keeps a previously-defined hook from standing in for a family
    # that does not actually define one.
    if ! (
        engine_load_hooks >/dev/null 2>&1 || exit 1
        declare -F hook_provision_project >/dev/null 2>&1 || exit 1
    ); then
        printf '      %s (%s): no hook_provision_project\n' \
            "$MDB_ENGINE_NAME" "$MDB_ENGINE_FAMILY" >&2
        missing=$(( missing + 1 ))
        continue
    fi

    # The printed connection block is part of the promise, so the descriptor
    # must carry what it needs. Without these new-project prints an empty
    # DB_CONNECTION or DB_PORT, which looks like a copy-paste error at the far
    # end rather than a missing field here.
    [[ -n "${MDB_ENGINE_DRIVER:-}" ]] \
        || vfail "engines/$engine/engine.conf declares no MDB_ENGINE_DRIVER"
    [[ -n "${MDB_ENGINE_PORT:-}" ]] \
        || vfail "engines/$engine/engine.conf declares no MDB_ENGINE_PORT"

    checked=$(( checked + 1 ))
    vinfo "$(printf '%-10s family=%-10s driver=%-10s port=%s' \
        "$MDB_ENGINE_NAME" "$MDB_ENGINE_FAMILY" \
        "$MDB_ENGINE_DRIVER" "$MDB_ENGINE_PORT")"
done < <(engine_list)

(( missing == 0 )) \
    || vfail "$missing engine(s) cannot provision a project; new-project would reject them"

(( checked > 0 )) || vfail "no engines were checked"

# new-project must not have grown a per-engine branch back. The whole point of
# the hooks is that this file contains no engine names.
if grep -qE '^\s*(pg|mysql|mariadb|mongodb|cassandra|ferretdb)\)' "$MDB_ROOT/scripts/new-project"; then
    vfail "scripts/new-project branches on an engine name again; that belongs in a family hook"
fi
vinfo "new-project contains no per-engine branch"

vinfo "$checked engine(s) declare a provisioning hook"

# -----------------------------------------------------------------------------
# Behavioural half
# -----------------------------------------------------------------------------
# The structural half above passed while Cassandra's hook was broken: cqlsh -e
# splits on ';', so a statement string beginning with a newline produced an
# empty leading statement, and cqlsh exited non-zero AFTER running everything
# else. The keyspace and roles existed and provisioning reported failure.
#
# So the hook is actually invoked, against every engine that happens to be
# running. Engines that are not up are named rather than silently passed over --
# a check that quietly covers nothing reads exactly like one that covers
# everything.
if ! mdb_runtime_ready; then
    vinfo "no container runtime reachable; skipped the behavioural half"
    exit 0
fi

running=""
while IFS= read -r svc; do
    [[ -n "$svc" ]] && running+="$svc "
done < <( { cd "$MDB_ROOT" || exit 0; mdb_running_services; } || true)

provisioned=0
skipped=""

while IFS= read -r engine; do
    [[ -z "$engine" ]] && continue
    engine_load "$engine" || continue
    name="$MDB_ENGINE_NAME"

    case " $running " in
        *" $name "*) ;;
        *) skipped+="$name "; continue ;;
    esac

    proj="vfy$$"
    add_cleanup "rm -f '$MDB_ROOT'/secrets/${name}_${proj}_user*_password.txt"

    # </dev/null is load-bearing. new-project reaches the engine through
    # `docker compose exec -T`, which READS STDIN -- and inside this loop
    # stdin is the engine list being iterated. Without it the first engine
    # swallows the rest, the loop ends after one pass, and the check reports
    # success having exercised a single engine.
    if ! (cd "$MDB_ROOT" && ./scripts/new-project --name "$proj" --engine "$name" \
            </dev/null >/dev/null 2>&1); then
        vfail "new-project failed against a running $name"
    fi

    # Idempotent: people re-run this to reprint the connection block.
    if ! (cd "$MDB_ROOT" && ./scripts/new-project --name "$proj" --engine "$name" \
            </dev/null >/dev/null 2>&1); then
        vfail "new-project is not idempotent on $name; a second run failed"
    fi

    vinfo "$name: provisioned '$proj' and re-ran cleanly"
    provisioned=$(( provisioned + 1 ))
done < <(engine_list)

[[ -n "$skipped" ]] && vinfo "not running, so not exercised: ${skipped% }"

if (( provisioned == 0 )); then
    vinfo "no engines were running; only the structural half ran"
else
    vinfo "$provisioned engine(s) provisioned a project for real"
fi
