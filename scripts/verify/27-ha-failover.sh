#!/usr/bin/env bash
# verify: killing the Patroni leader elects a new one and the write port follows
# tags: ha
# phase: 7

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_docker
cd "$MDB_ROOT" || exit 1

# SPEC section 21.5. This is the only check that proves the HA stack does the
# one thing it exists to do. Note section 21.2's warning: single-host compose
# HA is a REHEARSAL topology -- it demonstrates election, it is not production
# HA, and the docs must not pretend otherwise.

need_file "$MDB_ROOT/docker-compose.yml"

# A psql that does not depend on which other services happen to be running.
#
# This used to run `docker compose exec -T pgbouncer psql ...`, from a time when
# a hand-written service by that name was assumed. The pooler is generated from
# the descriptors now and is called pg-pooler, and the pgBouncer image has no
# psql in it anyway. Using the engine image with --entrypoint psql bypasses its
# s6 entrypoint, which would otherwise wrap every result in supervision logging.
psql_via_haproxy() {
    local port="$1" sql="$2" net pw
    # The exact compose network, not a substring search. Matching anything
    # containing "mdb" picked up a leftover throwaway network from another
    # check (mdb-verify-pool-net-NNNN), where no service resolves -- so every
    # probe failed with "could not translate host name", and the check reported
    # that the write port was not routing when it had never been reached.
    net="${COMPOSE_PROJECT_NAME:-mdb}_net"
    # The superuser password, not an anonymous connection. pg_hba is
    # scram-sha-256 for every host rule -- check 30 fails the build if it ever
    # is not -- so a passwordless attempt is refused, and the check then reads
    # as "the write port does not route to a primary" when the routing was
    # fine all along.
    pw="$(tr -d '\n' < "$MDB_ROOT/secrets/pg_superuser_password.txt" 2>/dev/null)"
    docker run --rm --network "$net" \
        -e PGPASSWORD="$pw" \
        --entrypoint psql "$(image_name pg)" \
        "host=haproxy port=${port} user=postgres dbname=postgres sslmode=require" \
        -tAc "$sql" 2>/dev/null | tr -d ' \r' || true
}

budget="$(env_get MDB_HA_FAILOVER_BUDGET 30)"

# PROFILES=ha, not pg,ha.
#
# HA REPLACES the standalone engine rather than joining it: HAProxy owns the
# PostgreSQL port and routes it to whichever Patroni node is leader. Asking for
# both binds 5432 twice, which check-env now refuses outright. Nothing below
# asserts anything about the standalone pg service, so it was never needed --
# it was in the profile list from before the HA services existed.
# Start from empty state. Patroni's cluster-wide settings (ttl, loop_wait) are
# written into etcd by whoever bootstraps FIRST and are never re-read from the
# config file afterwards, so a leftover etcd volume silently pins the timings
# from an earlier run -- a failover budget can then fail for a value nobody can
# find in the repository. Inheriting a half-broken cluster from a previous run
# would also make this check's result depend on the one before it.
make down >/dev/null 2>&1 || true
#
# The NODE data volumes go too, not just etcd's. Clearing one and not the other
# is worse than clearing neither: etcd loses all history while the nodes keep a
# data directory from a previous timeline, so the replicas try to follow a
# leader whose past they no longer share. They then fall back to archive
# recovery, sit at "waiting for WAL to become available", and report tens of
# megabytes of lag that never closes -- which reads as a replication bug rather
# than as leftovers from the previous run.
_pgv="$(env_get MDB_PG_VERSION 17)"
docker volume rm -f \
    "${COMPOSE_PROJECT_NAME:-mdb}_etcd1_data" \
    "${COMPOSE_PROJECT_NAME:-mdb}_etcd2_data" \
    "${COMPOSE_PROJECT_NAME:-mdb}_etcd3_data" \
    "${COMPOSE_PROJECT_NAME:-mdb}_patroni1_pg${_pgv}_data" \
    "${COMPOSE_PROJECT_NAME:-mdb}_patroni2_pg${_pgv}_data" \
    "${COMPOSE_PROJECT_NAME:-mdb}_patroni3_pg${_pgv}_data" >/dev/null 2>&1 || true

# Registered BEFORE the boot, not after. A cleanup registered on the line
# following `mdb_up` never runs when the boot itself fails -- and a FAILED
# boot is exactly when containers are left behind. Check 27's half-started
# HA stack kept haproxy on 5432, so check 28 then failed its port pre-flight
# and reported a port collision that had nothing to do with its subject.
add_cleanup 'make down'
if ! mdb_up PROFILES=ha; then
    # compose says only "container mdb-patroniN is unhealthy", which is the one
    # fact that does not narrow anything down -- and N moves between runs, which
    # reads as a flaky node. Patroni says exactly why in its own log, so print
    # it: locally the same symptom was "password authentication failed for user
    # postgres", a replica that had cloned a data directory predating the
    # current secrets, and nothing in compose's output hinted at credentials.
    for n in 1 2 3; do
        c="${COMPOSE_PROJECT_NAME:-mdb}-patroni${n}"
        printf '      ----- %s -----\n' "$c" >&2
        # The API tracebacks are noise: they are connection resets from the
        # healthcheck probing a node that is not answering yet, one per probe.
        # 40 lines, and only stack FRAMES filtered. The first version cut to 12
        # and dropped everything that looked like Python, which left exactly
        # "PatroniFatalException: Failed to bootstrap cluster" -- the assertion
        # that bootstrap failed, with the initdb or post-bootstrap error that
        # says WHY scrolled off above it.
        docker logs "$c" 2>&1 \
            | grep -viE '^ +File "|^\s+\^+\s*$|ConnectionResetError' \
            | tail -40 | sed 's/^/      /' >&2 || true
    done
    vfail "make up PROFILES=ha failed (each node's own log is above)"
fi

# Parsed through `scripts/ha`'s machine-readable verbs, not by grepping the
# human table. The table is for people and changes shape: `ha status` also
# prints the election history, whose header contains the words "New Leader", so
# counting lines matching "leader" never yields 1 however healthy the cluster
# is. This check was written before the implementation existed and assumed an
# output that never appeared.
# shellcheck disable=SC2016  # evaluated by the subshell, not here
wait_for 240 "the Patroni cluster to converge on one leader" bash -c \
    '[[ -n $(scripts/ha leader 2>/dev/null) ]]'

leader="$(scripts/ha leader 2>/dev/null || true)"
[[ -n "$leader" ]] || vfail "could not identify the cluster leader"

# Streaming, not merely present: a replica that exists but is not replicating
# is the exact failure this topology is supposed to make visible.
# Every poll below ends in `|| true`. lib.sh runs this under `set -e` and
# pipefail, so a probe that fails once -- patronictl while the cluster has no
# leader, curl while a node restarts -- ended the whole check on that line,
# with no vfail message and no retry. On 2026-11-01 that is how check 27
# failed in CI: "killed leader patroni1", then nothing. A failed probe is an
# unanswered poll, and the loop's own budget decides when that is a failure.
streaming=0
for (( i = 0; i < 180; i++ )); do
    # State is "streaming", not "running". A replica reports "running" while it
    # is still catching up or has fallen back to archive recovery; "streaming"
    # is the one that means it is connected to the primary and following it.
    # Accepting "running" would pass on exactly the degraded cluster this is
    # meant to catch.
    streaming="$(scripts/ha roles 2>/dev/null \
        | awk -F'\t' '$2 == "Replica" && $3 == "streaming"' | wc -l | tr -d ' ' || true)"
    streaming="${streaming:-0}"
    (( streaming >= 2 )) && break
    sleep 1
done

replicas="$streaming"
(( replicas >= 2 )) || vfail "expected 2 streaming replicas, found $replicas"
vinfo "cluster converged: leader=$leader, replicas=$replicas"

# etcd must have a real quorum. Section 21.2 is explicit that a two-node etcd
# is worse than one node, so a 2-member cluster is a failure, not a warning.
members="$(docker compose exec -T etcd1 etcdctl member list 2>/dev/null | grep -c . || echo 0)"
(( members >= 3 )) || vfail "etcd has $members member(s); section 21.2 requires a quorum of 3 minimum"
vinfo "etcd quorum: $members members"

# Caught up, not merely "streaming".
#
# Patroni refuses to promote a replica lagging more than
# maximum_lag_on_failover, and rightly so -- promoting a node that is 80MB
# behind silently discards those writes. A replica reports state "running"
# from the moment it starts streaming, long before it has caught up, so
# killing the leader at that point produces a cluster where NO node is
# eligible and no election ever happens. That is Patroni behaving correctly
# and the test being unfair, which is the harder failure to read: the logs
# say "i am not the healthiest node" on every node at once.
max_lag=1048576
caught_up=0
for (( i = 0; i < 180; i++ )); do
    worst="$(docker compose exec -T patroni1 sh -c 'curl -s http://127.0.0.1:8008/cluster' 2>/dev/null \
        | tr ',' '\n' | grep -oE '"lag": *[0-9]+' | grep -oE '[0-9]+' \
        | sort -n | tail -1 || true)"
    worst="${worst:-999999999}"
    if (( worst < max_lag )); then caught_up=1; break; fi
    sleep 1
done
(( caught_up )) || vfail "replicas never caught up to within ${max_lag} bytes; no node would be eligible for election"
vinfo "replicas caught up (worst lag ${worst} bytes, limit ${max_lag})"

# Write through the HAProxy write port, then kill the leader.
docker compose exec -T haproxy sh -c 'true' >/dev/null 2>&1 || vfail "haproxy is not running"

docker compose kill "$leader" >/dev/null 2>&1 || vfail "could not kill the leader"
vinfo "killed leader $leader"

start=$(date +%s)
elected=""
for (( i = 0; i < budget; i++ )); do
    elected="$(scripts/ha leader 2>/dev/null || true)"
    [[ -n "$elected" && "$elected" != "$leader" ]] && break
    sleep 1
done
elapsed=$(( $(date +%s) - start ))

[[ -n "$elected" && "$elected" != "$leader" ]] \
    || vfail "no new leader was elected within ${budget}s"
vinfo "new leader $elected elected in ${elapsed}s (budget ${budget}s)"

# The write port must actually follow the new leader, not just report it.
#
# `docker compose exec haproxy true` used to stand in for this. It only proved
# the container was alive, which it always is -- HAProxy stays up whether or not
# any backend is usable, so that probe could not fail for the reason it existed
# to catch. HAProxy needs a few health-check intervals to notice the new
# primary, so the real query is retried instead.
# An explicit loop rather than wait_for: wait_for runs its command in a
# subshell, which cannot see psql_via_haproxy defined above.
writable=""
for (( i = 0; i < budget; i++ )); do
    writable="$(psql_via_haproxy "${MDB_HAPROXY_WRITE_PORT:-5432}" \
        "SELECT NOT pg_is_in_recovery()")"
    [[ "$writable" == "t" ]] && break
    sleep 2
done
[[ "$writable" == "t" ]] || vfail "the write port does not route to a writable primary after failover"
vinfo "write port follows the new leader"

# The read port must serve replicas only.
# Retried until it is CORRECT, not until it merely answers.
#
# Breaking on any non-empty reply accepted "f" -- the read port still routing
# to the node that had just been promoted, because HAProxy had not yet re-run
# its /replica check (inter 3s, rise 2). That is a transient state on the way
# to the right one, and treating it as the final answer failed a working
# cluster.
in_recovery=""
for (( i = 0; i < budget; i++ )); do
    in_recovery="$(psql_via_haproxy "${MDB_HAPROXY_READ_PORT:-5433}" \
        "SELECT pg_is_in_recovery()")"
    [[ "$in_recovery" == "t" ]] && break
    sleep 2
done
[[ "$in_recovery" == "t" ]] || vfail "the read port served a primary; it must serve replicas only"
vinfo "read port serves replicas only"
