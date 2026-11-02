#!/usr/bin/env bash
# verify: every image that has a Dockerfile builds, and all four eventually exist
# tags: build
# phase: 5

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# Ordering matters: this check runs before every check that needs an image, and
# building is its deliberate side effect. So it builds everything present FIRST
# and only then asserts completeness -- otherwise a missing phase-3 Dockerfile
# would abort before the phase-2 PG image was ever built, and every downstream
# PG check would fail for the wrong reason.

built=0
missing=()

for engine in pg mysql mariadb cli; do
    dockerfile="$MDB_ROOT/images/$engine/Dockerfile"
    if [[ ! -f "$dockerfile" ]]; then
        missing+=("images/$engine/Dockerfile")
        continue
    fi

    img="$(image_name "$engine")"
    log="/tmp/mdb-build-$engine.log"
    vinfo "building $img"

    if ! build_image "$engine" "$img" >"$log" 2>&1; then
        printf '      build failed; last 30 lines of %s:\n' "$log" >&2
        tail -30 "$log" >&2
        vfail "images/$engine failed to build"
    fi
    built=$(( built + 1 ))
done

vinfo "$built image(s) built on $(uname -m)"

if (( ${#missing[@]} )); then
    printf '      not written yet: %s\n' "${missing[*]}" >&2
    vfail "${#missing[@]} image(s) still missing a Dockerfile"
fi
