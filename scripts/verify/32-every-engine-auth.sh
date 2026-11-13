#!/usr/bin/env bash
# verify: no engine, of any paradigm, accepts unauthenticated connections
# tags: security auth engines
# phase: 6

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=../engine-lib.sh
source "$MDB_ROOT/scripts/engine-lib.sh"

cd "$MDB_ROOT" || exit 1

# SPEC section 22.3. This is the check that makes "every engine authenticates"
# a property of the toolkit rather than a claim about three of them.
#
# It matters most for the engines added last, because both of them ship
# insecure and correcting that is the main reason to use this toolkit rather
# than the upstream images directly:
#
#   MongoDB    runs with NO authentication unless root credentials are supplied
#   Cassandra  ships AllowAllAuthenticator AND AllowAllAuthorizer
#
# A new engine inherits this check by existing. That is the point: an engine
# with no coverage is an engine nobody can trust, and remembering to add a
# check per engine is exactly the discipline that fails quietly.

need_runtime

checked=0
skipped=0

for engine in $(engine_list); do
    engine_load "$engine" || vfail "could not load the descriptor for $engine"

    # Not every engine has a hooks implementation yet; say so rather than
    # passing silently, which would read as coverage that does not exist.
    if ! engine_load_hooks 2>/dev/null; then
        vinfo "$engine: SKIPPED, no family hooks for '${MDB_ENGINE_FAMILY}'"
        skipped=$(( skipped + 1 ))
        continue
    fi

    if ! declare -f hook_auth_enforced >/dev/null 2>&1; then
        vfail "$engine: family '${MDB_ENGINE_FAMILY}' declares no hook_auth_enforced; auth cannot be proven"
    fi

    ver="$(engine_version "$engine")"
    img="$(mdb_image "$engine" "$ver")"

    # A referenced engine is not published by us, so it runs the pinned
    # upstream image (SPEC section 22.5).
    if [[ "${MDB_ENGINE_PUBLISH:-derive}" == "reference" ]]; then
        img="$(awk -v e="$engine" -v v="$ver" '$1==e && $2==v {print $3; exit}' images/bases.tsv)"
    fi

    # A missing image used to SKIP, and this check still exited 0 having
    # verified almost nothing. That is how a scoping bug that disabled the
    # probe for four of six engines went unnoticed: the run reported "pass"
    # with five engines skipped. Every other check in this harness treats a
    # missing image as a failure (need_image), and so does this one now.
    #
    # Three cases, distinguished because they mean different things:
    if ! image_exists "$img"; then
        if [[ "${MDB_ENGINE_PUBLISH:-derive}" == "reference" ]]; then
            # We do not build it, so fetch it. The reference is digest-pinned,
            # which makes this deterministic rather than a moving target.
            vinfo "$engine: pulling the referenced image"
            mdb_ctr pull -q "$img" >/dev/null 2>&1 \
                || vfail "$engine: could not pull $img; auth cannot be proven"
        elif [[ -f "$MDB_ROOT/images/$engine/Dockerfile" ]]; then
            vfail "$engine: image not built yet ($img); run: make build"
        else
            # No Dockerfile at all: the engine is declared but not implemented.
            # That is a real gap, but it is not this check's to report, and
            # failing here would block the harness on unfinished work.
            vinfo "$engine: SKIPPED, declared but not implemented (no images/$engine/Dockerfile)"
            skipped=$(( skipped + 1 ))
            continue
        fi
    fi

    name="mdb-verify-auth-${engine}-$$"
    track_container "$name"

    # Deliberately NOT named pw. The family helpers declare `local pw` for
    # their own use, and bash locals are dynamically scoped, so a secret()
    # that returned "$pw" would read the helper's empty local rather than this
    # value -- every probe would then run with an empty password and the
    # engine would look like it never became ready. The hooks were also fixed;
    # this side is named so neither depends on the other.
    auth_pw="${TW_VERIFY}"

    # Not every engine takes its root password from an environment variable.
    # Cassandra reads it from a mounted secret file, so an env-only container
    # keeps the default credential and every probe with the throwaway password
    # fails -- which looks like "never became ready" rather than the setup
    # error it is. The descriptor already names the file, so mount one.
    # A fresh, tracked volume for the data directory. Without one, Docker
    # creates an ANONYMOUS volume (the engine images declare VOLUME), and any
    # data surviving from a previous run makes the entrypoint skip first-run
    # initialisation -- so the root user keeps its OLD password and every probe
    # fails with a storedKey mismatch that looks like a broken engine.
    vol="mdb-verify-auth-${engine}-vol-$$"
    remove_test_volumes "$vol"
    mdb_ctr volume create "$vol" >/dev/null
    track_volume "$vol"

    secrets_dir="$(mktemp -d)"
    add_cleanup "rm -rf '$secrets_dir'"
    printf '%s' "$auth_pw" > "$secrets_dir/${MDB_ENGINE_ROOT_SECRET}"
    chmod 0644 "$secrets_dir/${MDB_ENGINE_ROOT_SECRET}"
    # Every engine's root password reaches it by a different variable name,
    # which the descriptor already declares. Passing them all is harmless: an
    # engine ignores the ones that are not its own.
    mdb_ctr run -d --name "$name" \
        -e POSTGRES_PASSWORD="$auth_pw" \
        -e MYSQL_ROOT_PASSWORD="$auth_pw" \
        -e MARIADB_ROOT_PASSWORD="$auth_pw" \
        -e MONGO_INITDB_ROOT_USERNAME=root \
        -e MONGO_INITDB_ROOT_PASSWORD="$auth_pw" \
        -e MAX_HEAP_SIZE=1G -e HEAP_NEWSIZE=200M \
        -v "$secrets_dir:/run/secrets:ro" \
        -v "$vol:${MDB_ENGINE_DATA_DIR}" \
        "$img" >/dev/null 2>&1 \
        || vfail "$engine: container failed to start from $img"

    # The hooks are written against this contract; here every call targets the
    # throwaway container rather than a compose service.
    # shellcheck disable=SC2034  # read by the family hooks
    IN_CONTAINER=0
    engine_exec() { shift; mdb_ctr exec -i "$name" "$@"; }
    secret() { printf '%s' "$auth_pw"; }
    compress_ext() { printf ''; }

    # Cassandra is genuinely slow to become queryable: a JVM to warm and a
    # gossip round to complete. The budget reflects that rather than assuming
    # every engine behaves like PostgreSQL.
    # An if, not `engine_is_heavy && budget=300`: that form returns non-zero
    # for every engine that is NOT heavy, which aborts the whole check under
    # set -e at the first light engine.
    budget=90
    if engine_is_heavy; then budget=300; fi

    ready=0
    for (( i = 0; i < budget; i++ )); do
        if hook_ping; then ready=1; break; fi
        sleep 1
    done

    if (( ! ready )); then
        mdb_ctr logs "$name" 2>&1 | tail -8 >&2
        vfail "$engine: never became ready within ${budget}s"
    fi

    # THE assertion.
    if hook_auth_enforced; then
        vinfo "$(printf '%-10s %-12s auth enforced' "$engine" "$MDB_ENGINE_PARADIGM")"
    else
        vfail "$engine ACCEPTS UNAUTHENTICATED CONNECTIONS. SPEC section 22.3 forbids it."
    fi

    mdb_ctr rm -f -v "$name" >/dev/null 2>&1 || true
    checked=$(( checked + 1 ))
done

(( checked > 0 )) || vfail "no engines were checked; this proves nothing"

# Skips are only ever unimplemented engines now, so name them. A count alone
# reads as noise; a name is something someone can act on.
if (( skipped > 0 )); then
    vinfo "note: $skipped engine(s) are declared but not yet implemented"
fi
vinfo "$checked engine(s) enforce authentication${skipped:+, $skipped skipped}"
