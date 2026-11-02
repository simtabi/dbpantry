#!/usr/bin/env bash
# verify: a restore that fails exits non-zero and says FAILED (scratch stack)
# tags: backup
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# Check 21 proves a good dump restores. This proves the other half: a dump that
# cannot restore must not be reported as restored. scripts/restore ended its
# pipe in `|| true` and logged "complete" whatever happened, so a corrupt dump
# left an empty database behind a success message.
#
# It runs in scratch_stack: its own checkout copy, project, secrets and
# volumes, so it never drops or recreates a database the operator owns.

scratch_stack "pg,mysql"
scratch_up || vfail "make up failed in the scratch stack"
wait_for 180 "scratch pg and mysql healthy" scratch_healthy pg mysql

pw="$(cat "$SCRATCH_DIR/secrets/mysql_root_password.txt")"
my() { scratch_exec mysql env MYSQL_PWD="$pw" mysql -uroot --protocol=socket -N -B "$@" 2>/dev/null | tr -d '\r'; }

# Healthy is not ready: the first-run temporary server answers and then goes
# away (see wait_ready). A restore that lands in that window fails to
# recreate the database, which is a different failure from the one tested.
my_ok() { [[ "$(my -e 'SELECT 1')" == 1 ]]; }
pg_ok() { scratch_exec pg psql -U postgres -tAc 'SELECT 1' >/dev/null 2>&1; }
wait_ready 120 "scratch mysql ready" my_ok
wait_ready 120 "scratch pg ready" pg_ok

checksum() {
    if command -v sha256sum >/dev/null 2>&1; then
        ( cd "$(dirname "$1")" && sha256sum "$(basename "$1")" > "$(basename "$1").sha256" )
    else
        ( cd "$(dirname "$1")" && shasum -a 256 "$(basename "$1")" > "$(basename "$1").sha256" )
    fi
}

expect_failed_restore() {
    local what="$1"; shift
    local out rc=0
    out="$(CONFIRM=yes scratch_run scripts/restore "$@" 2>&1)" || rc=$?
    (( rc != 0 )) || vfail "$what: scripts/restore exited 0 ($(tail -1 <<<"$out"))"
    if grep -q 'restore of .* complete' <<<"$out"; then
        vfail "$what: a failed restore still reported 'complete'"
    fi
    grep -q 'FAILED' <<<"$out" || vfail "$what: the failure message does not say FAILED: $(tail -6 <<<"$out")"
    vinfo "$what: exit $rc, reported FAILED"
}

tmp="$SCRATCH_DIR/backups"

# 1. PostgreSQL: a corrupt archive whose checksum matches, so only the
#    restore itself can catch it.
printf 'not a pg_dump archive\n' > "$tmp/pg_restorefail_corrupt.dump"
checksum "$tmp/pg_restorefail_corrupt.dump"
expect_failed_restore "pg corrupt archive" --engine pg --db restorefail --file "$tmp/pg_restorefail_corrupt.dump"

# 2. MySQL: a dump that fails partway.
printf 'CREATE TABLE t (id INT);\nINSERT INTO t VALUES (1);\nTHIS IS NOT SQL;\nINSERT INTO t VALUES (2);\n' \
    > "$tmp/mysql_restorefail_partial.sql"
checksum "$tmp/mysql_restorefail_partial.sql"
expect_failed_restore "mysql partial dump" --engine mysql --db restorefail --file "$tmp/mysql_restorefail_partial.sql"
rows="$(my -e "SELECT count(*) FROM restorefail.t")"
[[ "$rows" == "1" ]] || vfail "mysql partial dump: expected the 1 row before the error, found '${rows:-none}'"

# 3. A good dump still restores and says complete.
printf 'CREATE TABLE ok (id INT);\nINSERT INTO ok VALUES (1),(2);\n' > "$tmp/mysql_restorefail_good.sql"
checksum "$tmp/mysql_restorefail_good.sql"
out="$(CONFIRM=yes scratch_run scripts/restore --engine mysql --db restorefail --file "$tmp/mysql_restorefail_good.sql" 2>&1)" \
    || vfail "a valid dump failed to restore: $(tail -3 <<<"$out")"
grep -q 'restore of restorefail complete' <<<"$out" || vfail "a valid restore did not report complete"
[[ "$(my -e "SELECT count(*) FROM restorefail.ok")" == "2" ]] || vfail "the valid restore is missing its rows"
vinfo "a valid mysql dump still restores"

# 4. A real pg_dump still restores: pg_restore --clean --if-exists into the
#    recreated database must not trip the stricter exit status.
scratch_exec pg psql -U postgres -q -c "CREATE DATABASE rtsrc;" >/dev/null 2>&1 || vfail "could not create rtsrc"
scratch_exec pg psql -U postgres -d rtsrc -q -c "CREATE TABLE t (id int); INSERT INTO t VALUES (1),(2),(3);" \
    >/dev/null 2>&1 || vfail "could not seed rtsrc"
scratch_exec pg pg_dump -U postgres -Fc rtsrc | zstd -q -o "$tmp/pg_rtsrc_good.dump.zst" \
    || vfail "could not dump rtsrc"
checksum "$tmp/pg_rtsrc_good.dump.zst"
out="$(CONFIRM=yes scratch_run scripts/restore --engine pg --db rtdst --file "$tmp/pg_rtsrc_good.dump.zst" 2>&1)" \
    || vfail "a valid pg dump failed to restore: $(tail -5 <<<"$out")"
n="$(scratch_exec pg psql -U postgres -d rtdst -tAc "SELECT count(*) FROM t" 2>/dev/null | tr -d ' \r')"
[[ "$n" == "3" ]] || vfail "the valid pg restore has '${n:-no}' rows, expected 3"
vinfo "a valid pg dump still restores"

vinfo "failed restores exit non-zero and say FAILED; good ones still complete"
