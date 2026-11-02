#!/usr/bin/env bash
# verify: the MySQL family recovers to a point in time from its binary log
# tags: backup pitr
# phase: 5

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 18, criterion 7c, for the MySQL family.
#
# PostgreSQL got PITR through WAL archiving; MySQL and MariaDB had none, so
# their recovery granularity was the last dump. The binary log is the
# equivalent mechanism and the assertion is held to the same standard as
# check 37: recovery to a chosen instant must keep what was written before it
# and drop what came after. "The server restarted" and "a binlog exists" both
# pass on a setup that cannot actually recover.

# Both engines, unless one is named. MySQL's image ships no mysqlbinlog, so the
# Dockerfile extracts that single file from the matching client RPM; without it
# the binary log would be an audit trail rather than a recovery path. Running
# both here is what proves that extraction actually produced a working tool.
# BOTH engines by default. They diverge in three places that each silently
# break recovery -- the coordinate statement's name, the replay tool's name,
# and whether the image ships that tool at all -- so checking one proves
# nothing about the other. MDB_PITR_ENGINE narrows it for debugging.
for engine in ${MDB_PITR_ENGINE:-mysql mariadb}; do
    img="$(image_name "$engine")"
    need_image "$img"

    # Per ENGINE, not just per run: the loop would otherwise reuse a name
    # whose container is still being removed from the previous iteration.
    name="mdb-verify-mypitr-${engine}-$$"
    vol="mdb-verify-mypitr-binlog-${engine}-$$"
    track_container "$name"

    mdb_ctr volume rm -f "$vol" >/dev/null 2>&1 || true
    mdb_ctr volume create "$vol" >/dev/null
    track_volume "$vol"

    upper="$(printf '%s' "$engine" | tr '[:lower:]' '[:upper:]')"
    pw=mdb-throwaway-mypitr

    mdb_ctr run -d --name "$name" \
        -e "${upper}_ROOT_PASSWORD=$pw" \
        -e "MDB_${upper}_PITR=true" \
        -v "$vol:/var/lib/mdb-binlog" \
        "$img" >/dev/null || vfail "container failed to start"

    client="$( [[ "$engine" == mariadb ]] && printf 'mariadb' || printf 'mysql' )"
    binlog_tool="$( [[ "$engine" == mariadb ]] && printf 'mariadb-binlog' || printf 'mysqlbinlog' )"
    run_sql() { mdb_ctr exec -i "$name" "$client" --protocol=socket -uroot -p"$pw" -N -B -e "$1" 2>/dev/null; }

    if ! wait_ready 180 "the engine to accept connections" \
            mdb_ctr exec "$name" bash -c "$client --protocol=socket -uroot -p'$pw' -e 'SELECT 1'"; then
        # The engine's own log says why; a bare timeout does not.
        mdb_ctr logs "$name" 2>&1 | tail -12 >&2
        vfail "$engine never accepted connections"
    fi

    # Binary logging must be on, and in a REPLAYABLE format. STATEMENT format
    # replays non-deterministic statements differently than they ran, so recovery
    # would silently diverge from the database it is meant to reproduce.
    log_bin="$(run_sql "SELECT @@log_bin" | tr -d ' \r')"
    [[ "$log_bin" == "1" ]] || vfail "log_bin is '$log_bin'; PITR needs binary logging on, set before start"
    fmt="$(run_sql "SELECT @@binlog_format" | tr -d ' \r')"
    [[ "$fmt" == "ROW" ]] || vfail "binlog_format is '$fmt'; PITR needs ROW to replay deterministically"
    vinfo "binary logging on, ROW format"

    # On its own volume, not inside the data directory. A recovery log stored
    # inside the thing it recovers goes when that goes.
    basename_val="$(run_sql "SELECT @@log_bin_basename" | tr -d ' \r')"
    case "$basename_val" in
        /var/lib/mdb-binlog/*) ;;
        *) vfail "binary logs are at '$basename_val'; they must live outside the data directory" ;;
    esac
    vinfo "binary logs are on their own volume ($basename_val)"

    run_sql "CREATE DATABASE pitrdemo; CREATE TABLE pitrdemo.t (id INT PRIMARY KEY, label VARCHAR(20));" >/dev/null
    run_sql "INSERT INTO pitrdemo.t VALUES (1,'before');" >/dev/null

    # The recovery target is a binary-log COORDINATE, not a timestamp.
    #
    # mariadb-binlog with --stop-datetime across multiple files exits after the
    # FIRST file and silently skips the rest (MDEV-35528), so a time-bounded replay
    # recovers almost nothing while reporting success. Positions have no such bug,
    # no second-granularity ambiguity, and no timezone interpretation --
    # --stop-position applies to the last file named, which is exactly the
    # semantics point-in-time recovery needs.
    # The coordinate statement was renamed in both directions: MySQL 8.4 REMOVED
    # SHOW MASTER STATUS in favour of SHOW BINARY LOG STATUS, while MariaDB added
    # SHOW BINLOG STATUS and kept the old name as an alias. Trying in order costs
    # one failed query and avoids branching on a version string.
    binlog_status() {
        local out
        for stmt in "SHOW BINARY LOG STATUS" "SHOW BINLOG STATUS" "SHOW MASTER STATUS"; do
            out="$(run_sql "$stmt" 2>/dev/null || true)"
            if [ -n "$out" ]; then printf '%s' "$out"; return 0; fi
    done
    return 1
}

coord="$(binlog_status)"
target_file="$(printf '%s' "$coord" | awk '{print $1}')"
target_pos="$(printf '%s' "$coord" | awk '{print $2}')"
[[ -n "$target_file" && -n "$target_pos" ]] || vfail "could not read the binary-log coordinate"
vinfo "recovery target: ${target_file}:${target_pos}"

run_sql "INSERT INTO pitrdemo.t VALUES (2,'after');" >/dev/null

total="$(run_sql "SELECT COUNT(*) FROM pitrdemo.t" | tr -d ' \r')"
[[ "$total" == "2" ]] || vfail "expected 2 rows before recovery, found $total"
vinfo "wrote 'before', took the coordinate, wrote 'after'"

# Close the current log so every event is on disk.
run_sql "FLUSH BINARY LOGS" >/dev/null
sleep 2

# Simulate the loss. Dropping the database stands in for restoring a base dump
# taken before both writes, which is what the documented procedure does.
run_sql "DROP DATABASE pitrdemo" >/dev/null
gone="$(run_sql "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='pitrdemo'" | tr -d ' \r')"
[[ "$gone" == "0" ]] || vfail "could not drop the database to simulate data loss"

# Files up to AND INCLUDING the target's file; --stop-position bounds the last.
# shellcheck disable=SC2016  # expanded by the container shell
mdb_ctr exec "$name" bash -c '
    set -e
    set -o pipefail
    dir=/var/lib/mdb-binlog
    target_file="$1"; target_pos="$2"; tool="$3"; client="$4"; pw="$5"
    logs=""
    for f in $(ls -1 "$dir" | grep -E "^binlog\.[0-9]+$" | sort); do
        logs="$logs $dir/$f"
        [ "$f" = "$target_file" ] && break
    done
    [ -n "$logs" ] || { echo "no binary logs found" >&2; exit 1; }
    # shellcheck disable=SC2086
    $tool --stop-position="$target_pos" $logs \
      | "$client" --protocol=socket -uroot -p"$pw"
' _ "$target_file" "$target_pos" "$binlog_tool" "$client" "$pw" \
    > /tmp/mdb-replay-$$.log 2>&1 \
    || { sed -n "1,8p" /tmp/mdb-replay-$$.log >&2; rm -f /tmp/mdb-replay-$$.log; vfail "binlog replay failed"; }
rm -f /tmp/mdb-replay-$$.log

# THE assertion.
kept="$(run_sql "SELECT COUNT(*) FROM pitrdemo.t WHERE label='before'" | tr -d ' \r')"
dropped="$(run_sql "SELECT COUNT(*) FROM pitrdemo.t WHERE label='after'" | tr -d ' \r')"

[[ "$kept" == "1" ]] \
    || vfail "the row written BEFORE the target is missing after recovery (found '$kept')"
[[ "$dropped" == "0" ]] \
    || vfail "the row written AFTER the target survived recovery (found '$dropped'); the target was not honoured"

vinfo "recovered to the target: the earlier write survived, the later one did not"
vinfo "$engine PITR is active and recovers to a point in time"

    # Each engine gets a clean slate; the next iteration must not inherit this
    # one's container, volume or cleanup entries.
    mdb_ctr rm -f -v "$name" >/dev/null 2>&1 || true
    mdb_ctr volume rm -f "$vol" >/dev/null 2>&1 || true
done
