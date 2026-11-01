#!/usr/bin/env bash
# verify: make backup-all then make verify-backups completes the round trip
# tags: backup
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_docker
cd "$MDB_ROOT" || exit 1

# SPEC section 18: "make backup-all produces per-database dumps plus PG
# globals, and make verify-backups restores the latest set green."
#
# SPEC section 11 also requires ONE dump helper shared by the scheduled sidecar
# and the make targets, so flags can never drift between the nightly path and
# the manual path. That shared-helper property is asserted here because a drift
# is otherwise only discovered during a real restore.

need_file "$MDB_ROOT/scripts/backup"

cleanup_new_backups
add_cleanup 'make down'
mdb_up PROFILES=pg,mysql,mariadb,backup || vfail "make up failed"

# shellcheck disable=SC2016  # evaluated by the subshell, not here
wait_for 120 "all engines healthy" bash -c \
    '[[ $(docker compose ps --format "{{.Health}}" | grep -c healthy) -ge 3 ]]'

make backup-all >/dev/null 2>&1 || vfail "make backup-all failed"

for engine in pg mysql mariadb; do
    find backups -name "${engine}_*" -type f -newermt '-10 minutes' | grep -q . \
        || vfail "make backup-all produced no fresh dump for $engine"
done
vinfo "fresh dumps present for all three engines"

find backups -name '*globals*' -type f -newermt '-10 minutes' | grep -q . \
    || vfail "make backup-all did not produce PG globals"
vinfo "pg globals dumped"

# Checksums, per SPEC section 11.
find backups \( -name '*.sha256' -o -name '*.md5' \) -type f -newermt '-10 minutes' | grep -q . \
    || vfail "no checksum files alongside the dumps"
vinfo "checksums written"

# The single-helper requirement: the sidecar and the make target must invoke
# the same script, so the dump flags cannot diverge.
grep -q 'scripts/backup' docker-compose.yml \
    || vfail "the backup sidecar does not invoke scripts/backup; flags will drift from the make path"
vinfo "sidecar and make target share scripts/backup"

make verify-backups >/dev/null 2>&1 || vfail "make verify-backups failed"
vinfo "verify-backups restored the latest set and asserted row counts"

# A FAILED dump must leave nothing behind.
#
# This check exists because the helper once did the opposite: `set -e` does not
# abort inside a function invoked from a `||` context, so a dump that died with
# "connection refused" still fell through to writing a checksummed file. The
# result looked exactly like a backup, which is worse than having none, because
# it would be trusted right up until a restore was needed. The harness did not
# catch it -- so it is a permanent check now.
before="$(find backups -maxdepth 1 -type f | wc -l | tr -d ' ')"
if scripts/backup --engine pg --db mdb_no_such_database_exists >/dev/null 2>&1; then
    vfail "backup reported success for a database that does not exist"
fi
after="$(find backups -maxdepth 1 -type f | wc -l | tr -d ' ')"

[[ "$before" == "$after" ]] \
    || vfail "a failed dump left $(( after - before )) file(s) behind; a broken backup must never look like a good one"
vinfo "a failed dump leaves no artifact ($before files before and after)"
