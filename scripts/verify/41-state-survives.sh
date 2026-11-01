#!/usr/bin/env bash
# verify: settings and recovery data survive rebuilds, restarts and destroy
# tags: fast structure backup
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# What must survive what.
#
# Three different lifetimes get confused with each other, and each confusion
# has a distinct failure:
#
#   * CONFIGURATION is regenerated from .env at every start. It does not need
#     to persist -- it needs to be reproducible, which is stronger. A setting
#     that only exists inside a container is lost on the next rebuild.
#   * DATA lives in named volumes and survives `make down`, a rebuild, and
#     container recreation. Anything in a container's writable layer does not.
#   * RECOVERY DATA -- the pgBackRest repository and the binary logs -- must
#     survive even `make destroy`, because the moment you most want a backup is
#     immediately after destroying something.

# 1. Every path an image declares as a VOLUME must be mapped to a NAMED volume.
#
# An unmapped VOLUME gets an anonymous one, which compose discards on recreate:
# the data appears to persist across restarts and vanishes on the first
# `docker compose up --force-recreate`.
need_file "$MDB_ROOT/compose.engines.yml"
# shellcheck source=../engine-lib.sh
. "$MDB_ROOT/scripts/engine-lib.sh"

unmapped=0
while IFS= read -r engine; do
    [[ -z "$engine" ]] && continue
    engine_load "$engine" || continue
    # No service is generated for an engine with no image yet (gen-compose),
    # so it has no data dir to map; check 47 and check-env cover that state.
    engine_implemented || continue
    grep -q -- "- ${MDB_ENGINE_NAME}_data:${MDB_ENGINE_DATA_DIR}" "$MDB_ROOT/compose.engines.yml" \
        || { printf '      %s: data dir %s is not mapped to a named volume\n' \
                "$MDB_ENGINE_NAME" "$MDB_ENGINE_DATA_DIR" >&2; unmapped=$(( unmapped + 1 )); }
done < <(engine_list)
(( unmapped == 0 )) || vfail "$unmapped engine(s) would write data to an anonymous volume"
vinfo "every engine's data directory is a named volume"

# 2. Recovery data lives in its OWN volume, never inside the data volume.
while IFS= read -r engine; do
    [[ -z "$engine" ]] && continue
    engine_load "$engine" || continue
    [[ "${MDB_ENGINE_PITR_CAPABLE:-false}" == "true" ]] || continue

    repo="${MDB_ENGINE_PITR_REPO_PATH:?}"
    case "$repo" in
        "$MDB_ENGINE_DATA_DIR"/*|"$MDB_ENGINE_DATA_DIR")
            vfail "$MDB_ENGINE_NAME keeps recovery data at $repo, inside its data directory.
       A backup stored inside the thing it backs up goes when that goes." ;;
    esac
    grep -q -- "- ${MDB_ENGINE_NAME}_pitr:${repo}" "$MDB_ROOT/compose.engines.yml" \
        || vfail "$MDB_ENGINE_NAME: $repo is not mapped to a named volume; binlogs would die with the container"
done < <(engine_list)
vinfo "recovery repositories are separate named volumes"

# 3. `make destroy` must not take the recovery repositories with it.
grep -q 'is_recovery_volume' "$MDB_ROOT/scripts/destroy" \
    || vfail "scripts/destroy does not distinguish recovery volumes; --all would delete the backups
       along with the data they exist to recover"
grep -q -- '--include-recovery' "$MDB_ROOT/scripts/destroy" \
    || vfail "scripts/destroy has no explicit opt-in for deleting recovery volumes"
vinfo "destroy keeps recovery repositories unless explicitly told otherwise"

# 4. Configuration is generated at start, so it is reproducible rather than
#    merely persistent. A rebuild must not lose a setting.
for f in images/pg/scripts/20-conf.sh images/_shared/mysql-family/scripts/20-conf.sh; do
    need_file "$MDB_ROOT/$f"
    grep -q 'MDB_CONF_DIR\|conf=' "$MDB_ROOT/$f" \
        || vfail "$f no longer generates configuration at start"
done
vinfo "engine configuration is regenerated from .env on every start"

# 5. Host-side state that must never be silently replaced.
grep -q 'Refusing to overwrite it' "$MDB_ROOT/scripts/init" \
    || vfail "scripts/init no longer refuses to overwrite an existing .env; regenerating it would
       orphan every password already in use"
vinfo "init refuses to overwrite an existing environment file"

# 6. The harness must not leak volumes.
#
# `docker rm -f` without -v leaves a container's ANONYMOUS volumes behind, and
# every throwaway engine container has one because the images declare VOLUME
# for their data directory. Unchecked, that reached 420 volumes and 34GB on
# the development machine -- which is disk pressure, which is what makes Docker
# reclaim IMAGES mid-run. The harness was breaking itself, and the symptom
# appeared as unrelated checks failing to find images they had just built.
leaky="$(grep -rn 'docker rm -f -v "' "$MDB_ROOT/scripts" 2>/dev/null | grep -v -- '-f -v' || true)"
if [[ -n "$leaky" ]]; then
    printf '      %s\n' "$leaky" >&2
    vfail "a container is removed without -v; its anonymous volumes would leak.
       Enough of those become disk pressure, and disk pressure evicts images."
fi
vinfo "containers are removed with their anonymous volumes"

vinfo "configuration reproduces, data persists, recovery data outlives destroy"
