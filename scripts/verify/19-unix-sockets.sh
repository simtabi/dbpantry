#!/usr/bin/env bash
# verify: a sidecar mounting the sockets volume connects with no TCP
# tags: sockets
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 18: "a sidecar container mounting the sockets volume connects
# with no TCP". Section 10.1 is explicit that container-to-container sockets
# work everywhere, so this check runs on every platform -- it is the
# HOST-to-container case that is Linux-only, and that is a docs matter.

img="$(image_name pg)"
need_image "$img"

sockvol="mdb-verify-sock-$$"
name="mdb-verify-sockpg-$$"
track_container "$name"
track_volume "$sockvol"

mdb_ctr volume create "$sockvol" >/dev/null

mdb_ctr run -d --name "$name" \
    -e POSTGRES_PASSWORD="${TW_VERIFY}" \
    -e MDB_SOCKETS=true \
    -v "$sockvol:/var/run/postgresql" \
    "$img" >/dev/null || vfail "container failed to start with MDB_SOCKETS=true"

wait_ready 60 "postgres to accept connections" mdb_ctr exec "$name" pg_isready -U postgres

# The socket file must exist in the shared volume.
mdb_ctr exec "$name" test -S /var/run/postgresql/.s.PGSQL.5432 \
    || vfail "no socket at /var/run/postgresql/.s.PGSQL.5432"
vinfo "socket present in the shared volume"

# A separate container, with NO network at all, must be able to connect. Using
# --network none is what makes "no TCP" an assertion rather than a claim.
out="$(mdb_ctr run --rm --network none \
    -v "$sockvol:/var/run/postgresql" \
    -e PGPASSWORD="${TW_VERIFY}" \
    "$img" \
    psql -h /var/run/postgresql -U postgres -tAc "SELECT 'socket-ok'" 2>&1 || true)"

printf '%s' "$out" | grep -q 'socket-ok' \
    || { printf '      %s\n' "$out" >&2; vfail "sidecar could not connect over the shared socket"; }

vinfo "sidecar connected over the socket with --network none: no TCP involved"
