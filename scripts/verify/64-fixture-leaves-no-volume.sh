#!/usr/bin/env bash
# verify: dbpantry fixture up boots healthy engines on tmpfs and fixture down leaves no volume behind
# tags: fixture test-profile pg mongodb
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime
need_dbpantry

# G3 and G4 of the refactor plan, design.md D-68: mesobia's conformance suite
# boots dbpantry engines as fixtures, many times a day. A fixture that left a
# volume per run would fill a CI runner's disk and, worse, carry one run's
# data into the next. The test overlay puts each engine's data on tmpfs and
# drops its named volumes, so up then down must leave the project with none.
#
# It runs in a scratch checkout (its own project, ports and secrets), so the
# operator's stack and volumes are never involved. pg is the engine every
# runner builds; mongodb is a referenced image, so it needs no build and
# covers the G4 overlay.
need_image "$(image_name pg)"

scratch_stack "pg,mongodb"

project_volumes() {
    mdb_ctr volume ls -q --filter "label=com.docker.compose.project=$SCRATCH_PROJECT" 2>/dev/null | awk 'NF' | sort
}

before="$(project_volumes)"
[[ -z "$before" ]] || vfail "the scratch project already has volumes: $before"

out="$(scratch_run "$DBPANTRY_BIN" fixture up --profiles pg,mongodb 2>&1)" \
    || vfail "dbpantry fixture up failed:
$(printf '%s\n' "$out" | tail -20 | sed 's/^/       /')"
add_cleanup "scratch_run '$DBPANTRY_BIN' fixture down >/dev/null 2>&1 || true"

# fixture up returns only once Compose reports every service healthy, or
# running where the image has no healthcheck: the upstream mongo image ships
# none, so it reports "none", never "healthy", and is accepted as running.
fixture_ready() {
    local pg mongo
    pg="$(scratch_run bash -c '. scripts/backend-lib.sh && mdb_svc_health pg')"
    mongo="$(scratch_run bash -c '. scripts/backend-lib.sh && mdb_svc_health mongodb')"
    [[ "$pg" == healthy && ( "$mongo" == healthy || "$mongo" == none ) ]]
}
wait_for 90 "the fixture's pg to report healthy and mongodb to run" fixture_ready

during="$(project_volumes)"
[[ -z "$during" ]] || vfail "the fixture created volumes while running (data must be on tmpfs): $(printf '%s\n' "$during" | tr '\n' ' ')"
vinfo "pg and mongodb healthy, with no volume"

out="$(scratch_run "$DBPANTRY_BIN" fixture down 2>&1)" || vfail "dbpantry fixture down failed: $(printf '%s\n' "$out" | tail -5)"
after="$(project_volumes)"
[[ -z "$after" ]] || vfail "volumes left after fixture down: $(printf '%s\n' "$after" | tr '\n' ' ')"
vinfo "fixture down left no volume"
