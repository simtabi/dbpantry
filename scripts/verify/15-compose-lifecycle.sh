#!/usr/bin/env bash
# verify: make up boots the default profile healthy and make down keeps data
# tags: compose lifecycle
# phase: 2

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 18: "clone, make init, make up -> PG + Adminer running in two
# commands". SPEC section 15: "make down never touches data" -- the check that
# matters most here, because a down that eats a volume is discovered exactly
# once, in anger.

need_file "$MDB_ROOT/docker-compose.yml"

cd "$MDB_ROOT" || exit 1

# The error is printed, not swallowed. "docker-compose.yml is not valid" with
# the reason discarded sent me looking at docker-compose.yml when the actual
# message named compose.engines.yml, a GENERATED file that `make init` did not
# write -- so a freshly initialised checkout failed here and said nothing about
# which file or which step was missing.
if ! compose_err="$(mdb_compose config 2>&1 >/dev/null)"; then
    printf '%s\n' "$compose_err" | sed 's/^/      /' >&2
    vfail "compose configuration is not valid (the Compose implementation's own error is above)"
fi
vinfo "compose file is valid"

# shellcheck disable=SC2119  # no profile override: this is the DEFAULT boot
mdb_up || vfail "make up failed"

# shellcheck disable=SC2016  # evaluated by the subshell, not here
wait_for 90 "pg service to report healthy" bash -c \
    'mdb_service_healthy pg'
vinfo "pg reports healthy"

mdb_service_running adminer \
    || vfail "adminer is not running under the default pg,ui profile"
vinfo "adminer is up"

# Write a marker, take the stack down, bring it back, and prove data survived.
# The volume deliberately outlives this check, so the marker must be reset
# rather than appended: otherwise a second run finds two rows, a third finds
# three, and the assertion below fails for a reason that has nothing to do with
# whether data survived.
mdb_compose exec -T pg psql -U postgres -q \
    -c "CREATE TABLE IF NOT EXISTS lifecycle_marker(v text);
        DELETE FROM lifecycle_marker;
        INSERT INTO lifecycle_marker VALUES ('survived');" \
    >/dev/null 2>&1 || vfail "could not write the marker row"

make down >/dev/null 2>&1 || vfail "make down failed"

vols="$(mdb_ctr volume ls --format '{{.Name}}' | grep -c '^mdb_' || true)"
(( vols > 0 )) || vfail "make down removed data volumes; SPEC section 15 forbids it"
vinfo "$vols data volume(s) survived make down"

# shellcheck disable=SC2119  # default boot again, deliberately
mdb_up || vfail "make up failed on the second boot"
# shellcheck disable=SC2016  # evaluated by the subshell, not here
wait_for 90 "pg to report healthy again" bash -c \
    'mdb_service_healthy pg'

val="$(mdb_compose exec -T pg psql -U postgres -tAc "SELECT v FROM lifecycle_marker" 2>/dev/null | tr -d ' \r')"
[[ "$val" == "survived" ]] || vfail "data did not survive down/up (got '$val')"
vinfo "data survived a full down/up cycle"

make down >/dev/null 2>&1 || true
