#!/usr/bin/env bash
# verify: PITR recovers to a point in time, keeping earlier writes and dropping later ones
# tags: backup pitr
# phase: 5

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 18, criterion 7c: "PITR active for PG".
#
# The assertion that matters is NOT "pgBackRest is installed" or "a backup
# exists" -- both are true of a setup that cannot actually recover. It is that
# recovery to a chosen instant keeps what was written before it and discards
# what was written after.
#
# So: write row A, record the time, write row B, recover to the recorded time,
# and assert A survived and B did not. A check that only asserted "the database
# came back" would pass on a plain restore of the base backup, which is the
# thing PITR is supposed to improve on.

img="$(image_name pg)"
need_image "$img"

name="mdb-verify-pitr-$$"
vol="mdb-verify-pitr-repo-$$"
track_container "$name"

mdb_ctr volume rm -f "$vol" >/dev/null 2>&1 || true
mdb_ctr volume create "$vol" >/dev/null
track_volume "$vol"

mdb_ctr run -d --name "$name" \
    -e POSTGRES_PASSWORD="${TW_PITR}" \
    -e MDB_PG_PITR=true \
    -e MDB_PG_ARCHIVE_TIMEOUT=5 \
    -v "$vol:/var/lib/pgbackrest" \
    "$img" >/dev/null || vfail "container failed to start"

wait_ready 180 "postgres to accept connections" \
    mdb_ctr exec -u postgres "$name" pg_isready -U postgres

psql_pg() { mdb_ctr exec -i -u postgres "$name" psql -qtAX -d postgres "$@" 2>/dev/null; }

# archive_mode is set before start, so it must already be on rather than
# converged afterwards.
mode="$(psql_pg -c 'SHOW archive_mode' | tr -d ' \r')"
[[ "$mode" == "on" ]] || vfail "archive_mode is '$mode'; PITR needs it on, set before start"
vinfo "archive_mode is on"

# 180s is ample on an idle machine; under a full suite the backup has run past
# it while still visibly working. Keep waiting, up to 15 minutes, for as long as
# a pgbackrest process (or the converge script that starts it) is alive.
# shellcheck disable=SC2016  # expanded inside the container
wait_ready_while 180 900 "pgBackRest to finish its initial full backup" \
    mdb_ctr exec "$name" sh -c \
        'for c in /proc/[0-9]*/cmdline; do tr "\0" " " < "$c" 2>/dev/null; echo; done | grep -q "[p]gbackrest\|[5]0-converge"' \
    -- mdb_ctr exec -u postgres "$name" sh -c \
    'pgbackrest --stanza=mdb info 2>/dev/null | grep -q "full backup"'
vinfo "initial full backup present: recovery has a base to replay from"

# Row A, then the target instant, then row B.
psql_pg -c "CREATE TABLE pitr_demo (id int primary key, label text);" >/dev/null
psql_pg -c "INSERT INTO pitr_demo VALUES (1, 'before');" >/dev/null

# The recovery target. Taken from the SERVER's clock, not the host's: recovery
# compares against transaction commit timestamps, and a host/container clock
# skew of even a second would put the target on the wrong side of a write.
target="$(psql_pg -c "SELECT now()" | tr -d '\r')"
[[ -n "$target" ]] || vfail "could not read the server clock"

sleep 2
psql_pg -c "INSERT INTO pitr_demo VALUES (2, 'after');" >/dev/null

before_count="$(psql_pg -c 'SELECT count(*) FROM pitr_demo' | tr -d ' \r')"
[[ "$before_count" == "2" ]] || vfail "expected 2 rows before recovery, found $before_count"
vinfo "wrote 'before', recorded the target, wrote 'after'"

# Force the current segment out. Without this the writes may still be sitting
# in an unarchived partial segment, so recovery would stop short of them and
# the check would fail for a reason that has nothing to do with PITR.
psql_pg -c "SELECT pg_switch_wal()" >/dev/null
sleep 5
mdb_ctr exec -u postgres "$name" pgbackrest --stanza=mdb check >/dev/null 2>&1 \
    || vfail "pgBackRest check failed: archiving is not working"
vinfo "WAL archiving verified by pgbackrest check"

# Recover. The server must be stopped: restore overwrites the data directory.
mdb_ctr exec -u postgres "$name" pg_ctl -D /var/lib/postgresql/data -m fast -w stop >/dev/null 2>&1 \
    || vfail "could not stop PostgreSQL for recovery"

mdb_ctr exec -u postgres "$name" pgbackrest --stanza=mdb --delta \
    --type=time --target="$target" --target-action=promote restore >/dev/null 2>&1 \
    || vfail "pgbackrest restore failed"

mdb_ctr exec -u postgres "$name" pg_ctl -D /var/lib/postgresql/data -w start >/dev/null 2>&1 \
    || vfail "PostgreSQL did not start after the restore"

wait_ready 120 "recovery to finish and the server to accept connections" \
    mdb_ctr exec -u postgres "$name" pg_isready -U postgres

# THE assertion.
kept="$(psql_pg -c "SELECT count(*) FROM pitr_demo WHERE label='before'" | tr -d ' \r')"
dropped="$(psql_pg -c "SELECT count(*) FROM pitr_demo WHERE label='after'" | tr -d ' \r')"

[[ "$kept" == "1" ]] \
    || vfail "the row written BEFORE the target is missing after recovery (found $kept); recovery went too far back"
[[ "$dropped" == "0" ]] \
    || vfail "the row written AFTER the target survived recovery (found $dropped); the target was not honoured"

vinfo "recovered to the target: the earlier write survived, the later one did not"
vinfo "PITR is active and recovers to a point in time"
