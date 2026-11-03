#!/usr/bin/env bash
# verify: make backup-all then make verify-backups completes the round trip (scratch stack)
# tags: backup
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime
cd "$MDB_ROOT" || exit 1   # for the static docker-compose.yml assertion below

# SPEC section 18: "make backup-all produces per-database dumps plus PG
# globals, and make verify-backups restores the latest set green."
#
# SPEC section 11 also requires ONE dump helper shared by the scheduled sidecar
# and the make targets, so flags can never drift between the nightly path and
# the manual path. That shared-helper property is asserted here because a drift
# is otherwise only discovered during a real restore.

need_file "$MDB_ROOT/scripts/backup"

# The check seeds its own database on every engine. It used to rely on user
# databases left behind by check 21; once that check cleaned up after itself,
# MySQL and MariaDB had nothing but system schemas, backup-all correctly dumped
# nothing, and only stale dumps from earlier runs had kept this check green.
#
# It runs in scratch_stack: its own checkout copy, project, secrets, volumes and
# backups directory. It used to run on the operator's project with the backup
# profile, so the sidecars dumped into, and pruned, the checkout's real backups/
# while this check planted files there to test retention, and it brought the
# operator's stack down at exit. Retention in exactly that directory once
# deleted about 175 real dumps. Scratch cleanup removes everything it made.
scratch_stack "pg,mysql,mariadb,backup"
scratch_up || vfail "make up failed in the scratch stack"
wait_for 180 "scratch pg, mysql and mariadb healthy" scratch_healthy pg mysql mariadb
dumps="$SCRATCH_DIR/backups"
[[ "$dumps" != "$MDB_ROOT/"* ]] || vfail "refusing: the scratch dumps directory is inside the checkout"
my_root() { local e="$1"; shift; scratch_exec "$e" env MYSQL_PWD="$(cat "$SCRATCH_DIR/secrets/${e}_root_password.txt")" "$e" -uroot --protocol=socket "$@"; }
pg_ok() { scratch_exec pg psql -U postgres -tAc 'SELECT 1' >/dev/null 2>&1; }
my_ok() { my_root "$1" -N -B -e 'SELECT 1' >/dev/null 2>&1; }
wait_ready 120 "scratch pg ready" pg_ok
wait_ready 120 "scratch mysql ready" my_ok mysql
wait_ready 120 "scratch mariadb ready" my_ok mariadb

if ! scratch_exec pg psql -U postgres -q -c "CREATE DATABASE backupall;" >/dev/null 2>&1 \
    || ! scratch_exec pg psql -U postgres -d backupall -q -c "
        CREATE TABLE items(id int primary key); INSERT INTO items VALUES (1);" >/dev/null 2>&1; then
    vfail "could not seed the PG backupall database"
fi
for e in mysql mariadb; do
    my_root "$e" -e "
        CREATE DATABASE IF NOT EXISTS backupall;
        CREATE TABLE IF NOT EXISTS backupall.items(id INT PRIMARY KEY);
        INSERT IGNORE INTO backupall.items VALUES (1);" >/dev/null 2>&1 \
        || vfail "could not seed the $e backupall database"
done
vinfo "seeded a backupall database on pg, mysql and mariadb"

# Retention prunes old dumps, and only dumps. It once removed EVERY file in
# backups/ older than the retention window -- the operator's notes, an .env
# copy kept there, Finder's .DS_Store -- because it matched on age alone.
# Planted in the scratch tree's dumps directory, never the checkout's.
keep="$dumps/operator-notes-$$.txt"
expired="$dumps/pg_verifyprune$$_20200101T000000Z.dump.zst"
printf 'not a dump\n' > "$keep"
: > "$expired"; : > "$expired.sha256"
touch -t 202001010000 "$keep" "$expired" "$expired.sha256"

scratch_run make backup-all >/dev/null 2>&1 || vfail "make backup-all failed"

[[ -f "$keep" ]] || vfail "retention deleted $keep, which is not a backup artefact"
[[ ! -f "$expired" ]] || vfail "retention left an expired dump behind ($expired)"
vinfo "retention pruned an expired dump and left a non-dump file alone"

# Every user database, not just the first: from the host each dump runs through
# `docker compose exec`, which reads stdin, and once swallowed the rest of the
# database list -- PG dumped `postgres` and silently skipped everything after it.
for engine in pg mysql mariadb; do
    find "$dumps" -name "${engine}_backupall_*" -type f -newermt '-10 minutes' | grep -q . \
        || vfail "make backup-all produced no fresh dump of backupall for $engine"
done
vinfo "fresh dumps of every seeded database on all three engines"

find "$dumps" -name '*globals*' -type f -newermt '-10 minutes' | grep -q . \
    || vfail "make backup-all did not produce PG globals"
vinfo "pg globals dumped"

# Checksums, per SPEC section 11.
find "$dumps" \( -name '*.sha256' -o -name '*.md5' \) -type f -newermt '-10 minutes' | grep -q . \
    || vfail "no checksum files alongside the dumps"
vinfo "checksums written"

# The single-helper requirement: the sidecar and the make target must invoke
# the same script, so the dump flags cannot diverge.
grep -q 'scripts/backup' docker-compose.yml \
    || vfail "the backup sidecar does not invoke scripts/backup; flags will drift from the make path"
vinfo "sidecar and make target share scripts/backup"

scratch_run make verify-backups >/dev/null 2>&1 || vfail "make verify-backups failed"
vinfo "verify-backups restored the latest set and asserted row counts"

# A FAILED dump must leave nothing behind.
#
# This check exists because the helper once did the opposite: `set -e` does not
# abort inside a function invoked from a `||` context, so a dump that died with
# "connection refused" still fell through to writing a checksummed file. The
# result looked exactly like a backup, which is worse than having none, because
# it would be trusted right up until a restore was needed. The harness did not
# catch it -- so it is a permanent check now.
before="$(find "$dumps" -maxdepth 1 -type f | wc -l | tr -d ' ')"
if scratch_run scripts/backup --engine pg --db mdb_no_such_database_exists >/dev/null 2>&1; then
    vfail "backup reported success for a database that does not exist"
fi
after="$(find "$dumps" -maxdepth 1 -type f | wc -l | tr -d ' ')"

[[ "$before" == "$after" ]] \
    || vfail "a failed dump left $(( after - before )) file(s) behind; a broken backup must never look like a good one"
vinfo "a failed dump leaves no artifact ($before files before and after)"
