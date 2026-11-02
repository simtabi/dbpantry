#!/usr/bin/env bash
# verify: s6 init stages run in order and PostgreSQL shuts down cleanly
# tags: pg s6
# phase: 2

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# This check exists because of design.md D-09, which is the most dangerous
# integration hazard found in step 1.
#
# The official postgres image sets STOPSIGNAL SIGINT (fast shutdown). s6 sends
# SIGTERM, which PostgreSQL treats as a SMART shutdown: it waits for every
# client to disconnect voluntarily. It therefore hangs until S6_KILL_GRACETIME
# expires, takes a SIGKILL, and forces crash recovery on the next boot -- while
# the container appears to have stopped normally. The fix is a `down-signal`
# file containing SIGINT in the PG service directory.
#
# Nothing about that failure is visible without this check, which is exactly
# why it exists.

img="$(image_name pg)"
need_image "$img"

name="mdb-verify-s6-$$"
vol="mdb-verify-s6-vol-$$"
track_container "$name"
track_volume "$vol"

mdb_ctr volume create "$vol" >/dev/null

mdb_ctr run -d --name "$name" -e POSTGRES_PASSWORD="${TW_VERIFY}" \
    -v "$vol:/var/lib/postgresql/data" "$img" >/dev/null \
    || vfail "container failed to start"

wait_ready 60 "postgres to accept connections" mdb_ctr exec "$name" pg_isready -U postgres

# --- init stage ordering ------------------------------------------------------
logs="$(mdb_ctr logs "$name" 2>&1)"
stage_order=$(printf '%s' "$logs" | grep -oE 'mdb-(perms|conf|certs|provision|postgres)' | awk '!seen[$0]++' | paste -sd, -)
vinfo "s6 stage order: ${stage_order:-<none observed>}"

[[ -n "$stage_order" ]] || vfail "no mdb s6 init stages observed in the logs"

# Permissions must be fixed before certificates are placed, and certificates
# before the engine starts, or PostgreSQL refuses to start on key permissions.
expected='mdb-perms,mdb-conf,mdb-certs,mdb-provision,mdb-postgres'
[[ "$stage_order" == "$expected" ]] \
    || vfail "s6 init ran in order '$stage_order', expected '$expected'"

# --- clean shutdown (D-09) ----------------------------------------------------
# Hold a client connection open. Under SIGTERM/smart shutdown this connection
# is precisely what makes PostgreSQL refuse to exit.
mdb_ctr exec -d "$name" psql -U postgres -c "SELECT pg_sleep(120)" || true
sleep 2

start=$(date +%s)
mdb_ctr stop -t 30 "$name" >/dev/null 2>&1 || vfail "mdb_ctr stop failed"
elapsed=$(( $(date +%s) - start ))
vinfo "container stopped in ${elapsed}s with a client still connected"

(( elapsed < 25 )) || vfail "shutdown took ${elapsed}s; it is hanging on smart shutdown (see D-09: needs down-signal=SIGINT)"

exit_code="$(mdb_ctr inspect -f '{{.State.ExitCode}}' "$name" 2>/dev/null || echo unknown)"
vinfo "exit code $exit_code"

shutdown_logs="$(mdb_ctr logs "$name" 2>&1 | tail -30)"
printf '%s' "$shutdown_logs" | grep -qi 'shutting down' \
    || vfail "no clean shutdown message; PostgreSQL was likely killed"

# --- the proof: next boot must not perform crash recovery ---------------------
mdb_ctr rm -f -v "$name" >/dev/null 2>&1 || true
name2="mdb-verify-s6-restart-$$"
track_container "$name2"

mdb_ctr run -d --name "$name2" -e POSTGRES_PASSWORD="${TW_VERIFY}" \
    -v "$vol:/var/lib/postgresql/data" "$img" >/dev/null

wait_ready 60 "postgres to accept connections after restart" mdb_ctr exec "$name2" pg_isready -U postgres

if mdb_ctr logs "$name2" 2>&1 | grep -qiE 'database system was not properly shut down|automatic recovery in progress'; then
    mdb_ctr logs "$name2" 2>&1 | grep -iE 'not properly shut down|recovery in progress' >&2
    vfail "the previous shutdown was unclean: next boot ran crash recovery (D-09)"
fi

vinfo "next boot performed no crash recovery: shutdown was genuinely clean"
