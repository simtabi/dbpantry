#!/usr/bin/env bash
# verify: with MDB_HOME apart from the kit, the stack boots, provisions and backs up, and nothing is written into the kit
# tags: stack pg structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime
need_dbpantry

# design.md D-84. The kit (what ships: scripts/, compose files, engines/,
# images/, config/ templates) and the instance (.env, secrets/, certs/,
# backups/, the generated compose files and rendered config) were one
# directory, so the dbpantry binary could not run from an extracted, read-only
# kit with its settings in ~/.config/dbpantry. MDB_HOME splits them.
#
# This runs a scratch kit with its instance in a separate directory, through
# the whole path a user takes -- init, check-env, render, generate, up,
# new-project, backup, down -- and then compares the kit with a manifest taken
# before any of it ran. A script still writing $ROOT/.env, $ROOT/secrets or a
# generated file next to the compose files shows up as a changed manifest,
# named by path.
need_image "$(image_name pg)"

SCRATCH_SEPARATE_HOME=1 scratch_stack "pg"
[[ "$SCRATCH_HOME" != "$SCRATCH_DIR" && -s "$SCRATCH_HOME/.verify-kit-manifest" ]] \
    || vfail "scratch_stack did not give the scratch stack a separate home"

# 0. A script in a copy of the tree defaults its home to that copy, even when
# its parent sourced the operator's libraries (more than once, as
# backend-lib.sh does) and points MDB_RT_ROOT at the operator's checkout. A
# second sourcing used to export the defaulted home as if the caller had set
# it, and check 71's render copy then wrote its generated compose files into
# the operator's checkout (2026-11-18, before this branch merged).
# shellcheck disable=SC2016  # expanded by the inner shells
got="$(cd "$SCRATCH_DIR" && env -u MDB_HOME MDB_RT_ROOT="$MDB_ROOT" bash -c \
    '. "$1/scripts/backend-lib.sh"; . "$1/scripts/env-lib.sh"; bash -c ". scripts/env-lib.sh; printf %s \"\$MDB_HOME\""' _ "$MDB_ROOT")"
[[ "$got" == "$(cd "$SCRATCH_DIR" && pwd)" ]] \
    || vfail "a copy of the tree, run from a process that sourced the operator's libraries, defaults its home to $got, not to itself"
vinfo "a copy of the tree defaults its home to itself, whatever its parent sourced"

# 0b. A generated compose file is the home's, never the kit's: a checkout
# that is also its own instance holds compose.engines.yml for itself, and a
# separate home that has not generated one yet read it, so check-env tested
# the other instance's ports (2026-11-21, a scratch home refused for 54000).
probe="$(mktemp -d)"
add_cleanup "rm -rf '$probe'"
mkdir -p "$probe/kit" "$probe/home"
: > "$probe/kit/compose.engines.yml"
: > "$probe/kit/docker-compose.yml"
# shellcheck disable=SC2016  # expanded by the inner shell
got="$(MDB_HOME="$probe/home" MDB_RT_ROOT="$probe/kit" bash -c \
    '. "$1/scripts/runtime-lib.sh"; _mdb_compose_file compose.engines.yml; _mdb_compose_file docker-compose.yml' _ "$MDB_ROOT")"
[[ "$got" == "$probe/home/compose.engines.yml"$'\n'"$probe/kit/docker-compose.yml" ]] \
    || vfail "a home with no generated compose.engines.yml resolves it to: ${got//$'\n'/, } (want the home's, absent, and the kit's docker-compose.yml)"
vinfo "a generated compose file resolves to the home even before it is generated; shipped ones to the kit"

# 1. init and check-env wrote the instance into the home, and only there.
for f in .env secrets/pg_superuser_password.txt certs/ca.crt; do
    [[ -s "$SCRATCH_HOME/$f" ]] || vfail "init did not write $f into the home"
    [[ ! -e "$SCRATCH_DIR/$f" ]] || vfail "init wrote $f into the kit"
done
vinfo "init wrote .env, secrets/ and certs/ into the home"

# 2. Boot through the binary, as a user with no checkout would: not make,
# whose binary rule could rebuild into the kit's build/.
out="$(scratch_run "$DBPANTRY_BIN" up --plain 2>&1)" \
    || vfail "dbpantry up failed with a separate home:
$(printf '%s\n' "$out" | tail -25 | sed 's/^/       /')"
add_cleanup "scratch_run '$DBPANTRY_BIN' down --plain >/dev/null 2>&1 || true"
wait_for 120 "pg to report healthy" scratch_healthy pg
[[ "$(scratch_exec pg psql -U postgres -tAc 'SELECT 1' 2>/dev/null | tr -d ' \r')" == 1 ]] \
    || vfail "pg is healthy but does not answer a query"
for f in compose.engines.yml compose.proxy.yml config/caddy/Caddyfile config/caddy/layer4.caddy; do
    [[ -s "$SCRATCH_HOME/$f" ]] || vfail "up did not generate $f in the home"
done
vinfo "booted from the home: pg healthy, generated files in the home"

# 3. The engine read its secret from the home: the superuser password there
# is the one the server accepts over TCP (the socket would trust the role).
pw="$(tr -d '\n' < "$SCRATCH_HOME/secrets/pg_superuser_password.txt")"
scratch_exec pg env PGPASSWORD="$pw" psql -h 127.0.0.1 -U postgres -tAc 'SELECT 1' >/dev/null 2>&1 \
    || vfail "pg does not accept the superuser password from the home's secrets/"
vinfo "pg uses the password in the home's secrets/"

# 4. A project and a backup land in the home.
proj="homecheck$$"
out="$(scratch_run "$DBPANTRY_BIN" project new --name "$proj" --engine pg --plain 2>&1)" \
    || vfail "dbpantry project new failed: $(printf '%s\n' "$out" | tail -10)"
[[ -s "$SCRATCH_HOME/secrets/pg_${proj}_user_password.txt" ]] \
    || vfail "new-project did not write the project's password into the home"
# The network an application joins is the home's project's, named in its .env
# only: new-project printed mdb_net (live on Incus, 2026-11-24).
grep -q "name: ${SCRATCH_PROJECT}_net\$" <<< "$out" \
    || vfail "new-project names a network other than ${SCRATCH_PROJECT}_net: $(grep 'name: ' <<< "$out" | head -1)"
out="$(scratch_run "$DBPANTRY_BIN" backup run --engine pg --db "$proj" --plain 2>&1)" \
    || vfail "dbpantry backup run failed: $(printf '%s\n' "$out" | tail -10)"
dumps="$(find "$SCRATCH_HOME/backups" -maxdepth 1 -type f -name "pg_${proj}_*" ! -name '*.sha256' | grep -c . || true)"
(( dumps >= 1 )) || vfail "the backup wrote no dump into the home's dump directory"
vinfo "project secrets and the dump are in the home"

scratch_run "$DBPANTRY_BIN" down --plain >/dev/null 2>&1 || vfail "dbpantry down failed"

# 5. The kit is exactly as it was: no file added, removed or changed.
after="$(scratch_kit_manifest)"
n="$(grep -c . "$SCRATCH_HOME/.verify-kit-manifest")"
(( n > 100 )) || vfail "the kit manifest lists only $n entries; it inspected nothing"
if ! diff_out="$(diff "$SCRATCH_HOME/.verify-kit-manifest" <(printf '%s\n' "$after"))"; then
    printf '%s\n' "$diff_out" | head -30 | sed 's/^/      /' >&2
    vfail "the kit changed while the instance lived in a separate home (listed above)"
fi
vinfo "the kit is unchanged: $n paths compared"
