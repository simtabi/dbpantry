#!/usr/bin/env bash
# verify: backup, drop, restore, and assert row counts on all three engines (scratch stack)
# tags: backup
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 11: "A backup that has never been restored is a hope, not a
# backup." This check is the difference between the two.
#
# It also guards the dump-correctness requirements that defaults get wrong:
# mysqldump omits routines and events unless asked, and a per-database pg_dump
# omits roles and grants, which is why globals are dumped alongside.

need_file "$MDB_ROOT/scripts/backup"
need_file "$MDB_ROOT/scripts/restore"

# It runs in scratch_stack: its own checkout copy, project, secrets, volumes and
# backups directory. It used to run on the operator's project, seed and drop a
# database there, write its dumps into the checkout's backups/ and bring the
# operator's stack down at exit. Scratch cleanup removes the project, its
# volumes and its dumps with it, so nothing here needs undoing by hand.
scratch_stack "pg,mysql,mariadb"
scratch_up || vfail "make up failed in the scratch stack"
wait_for 180 "scratch pg, mysql and mariadb healthy" scratch_healthy pg mysql mariadb
dumps="$SCRATCH_DIR/backups"
[[ "$dumps" != "$MDB_ROOT/"* ]] || vfail "refusing: the scratch dumps directory is inside the checkout"

pg_ok() { scratch_exec pg psql -U postgres -tAc 'SELECT 1' >/dev/null 2>&1; }
my_ok() { scratch_exec "$1" env MYSQL_PWD="$(cat "$SCRATCH_DIR/secrets/${1}_root_password.txt")" "$1" -uroot --protocol=socket -N -B -e 'SELECT 1' >/dev/null 2>&1; }
wait_ready 120 "scratch pg ready" pg_ok
wait_ready 120 "scratch mysql ready" my_ok mysql
wait_ready 120 "scratch mariadb ready" my_ok mariadb

# --- PostgreSQL ---------------------------------------------------------------
# Start from a known-empty database. A previous run's restore leaves the
# tables behind, and the seeding below would then collide with them -- failing
# for a reason that has nothing to do with backups.
scratch_exec pg psql -U postgres -q -c "DROP DATABASE IF EXISTS roundtrip;" >/dev/null 2>&1 || true
scratch_exec pg psql -U postgres -q -c "CREATE DATABASE roundtrip;" >/dev/null 2>&1 || true
scratch_exec pg psql -U postgres -d roundtrip -q -c "
    CREATE TABLE items(id serial primary key, name text);
    INSERT INTO items(name) SELECT 'row-' || g FROM generate_series(1,500) g;
    CREATE FUNCTION bump(int) RETURNS int AS 'SELECT \$1 + 1' LANGUAGE sql;" >/dev/null 2>&1 \
    || vfail "could not seed the PG roundtrip database"

scratch_run make backup ENGINE=pg DB=roundtrip >/dev/null 2>&1 || vfail "make backup ENGINE=pg failed"

# Globals must be dumped alongside, or a restore lands in a server with no roles.
# Fresh only: a globals file left by an earlier run must not satisfy this.
find "$dumps" -name '*globals*' -type f -newermt '-10 minutes' | grep -q . \
    || vfail "no pg_dumpall --globals-only output; a per-DB dump alone loses roles and grants"
vinfo "pg dump written with globals alongside"

scratch_exec pg psql -U postgres -q -c "DROP DATABASE roundtrip;" >/dev/null 2>&1 || true

# ! -name '*.sha256': the checksum file sorts after the dump it describes,
# so a bare tail -1 selects the checksum and "restores" a text file.
latest="$(find "$dumps" -name 'pg_roundtrip_*' -type f ! -name '*.sha256' | sort | tail -1)"
[[ -n "$latest" ]] || vfail "no PG dump file found to restore"

scratch_run make restore ENGINE=pg DB=roundtrip FILE="$latest" CONFIRM=yes >/dev/null 2>&1 \
    || vfail "make restore ENGINE=pg failed"

count="$(scratch_exec pg psql -U postgres -d roundtrip -tAc "SELECT count(*) FROM items" 2>/dev/null | tr -d ' \r')"
[[ "$count" == "500" ]] || vfail "PG restored $count rows, expected 500"

# A function proves the dump captured more than table data.
scratch_exec pg psql -U postgres -d roundtrip -tAc "SELECT bump(1)" >/dev/null 2>&1 \
    || vfail "PG restore lost the stored function"
vinfo "pg: 500 rows and the stored function restored"

# --- MySQL family -------------------------------------------------------------
roundtrip_mysql_family() {
    local engine="$1" client="$2" pw
    # The scratch stack's generated root password, not a fixed throwaway:
    # those are only used by checks that start their own containers.
    pw="$(cat "$SCRATCH_DIR/secrets/${engine}_root_password.txt")"
    scratch_exec "$engine" "$client" -uroot -p"$pw" -e "
        DROP DATABASE IF EXISTS roundtrip;
        CREATE DATABASE roundtrip;
        USE roundtrip;
        CREATE TABLE items(id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(64));
        INSERT INTO items(name) VALUES ('a'),('b'),('c');
        # Single-statement body on purpose: a BEGIN...END block contains a
        # semicolon, and the mysql client splits -e input on semicolons with no
        # way to change the delimiter, so the statement would be truncated.
        CREATE PROCEDURE bump() SELECT 1;
        CREATE EVENT ev ON SCHEDULE EVERY 1 DAY DO SELECT 1;" >/dev/null 2>&1 \
        || vfail "could not seed the $engine roundtrip database"

    scratch_run make backup ENGINE="$engine" DB=roundtrip >/dev/null 2>&1 \
        || vfail "make backup ENGINE=$engine failed"

    scratch_exec "$engine" "$client" -uroot -p"$pw" \
        -e "DROP DATABASE roundtrip;" >/dev/null 2>&1 || true

    local latest
    latest="$(find "$dumps" -name "${engine}_roundtrip_*" -type f ! -name '*.sha256' | sort | tail -1)"
    [[ -n "$latest" ]] || vfail "no $engine dump file found to restore"

    scratch_run make restore ENGINE="$engine" DB=roundtrip FILE="$latest" CONFIRM=yes >/dev/null 2>&1 \
        || vfail "make restore ENGINE=$engine failed"

    local rows routines events
    rows="$(scratch_exec "$engine" "$client" -uroot -p"$pw" -N -B \
        -e "SELECT count(*) FROM roundtrip.items" 2>/dev/null | tr -d ' \r')"
    [[ "$rows" == "3" ]] || vfail "$engine restored $rows rows, expected 3"

    # The flags SPEC section 11 insists on: --routines and --events. Their
    # absence is invisible until the day you need them.
    routines="$(scratch_exec "$engine" "$client" -uroot -p"$pw" -N -B \
        -e "SELECT count(*) FROM information_schema.routines WHERE routine_schema='roundtrip'" 2>/dev/null | tr -d ' \r')"
    [[ "$routines" == "1" ]] || vfail "$engine restore lost stored routines (--routines missing?)"

    events="$(scratch_exec "$engine" "$client" -uroot -p"$pw" -N -B \
        -e "SELECT count(*) FROM information_schema.events WHERE event_schema='roundtrip'" 2>/dev/null | tr -d ' \r')"
    [[ "$events" == "1" ]] || vfail "$engine restore lost events (--events missing?)"

    vinfo "$engine: 3 rows, 1 routine, 1 event restored"
}

roundtrip_mysql_family mysql mysql
roundtrip_mysql_family mariadb mariadb
