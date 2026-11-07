#!/usr/bin/env bash
#
# Stage 5: cluster-level convergence, applied on EVERY start.
#
# Stage 4 emits into /docker-entrypoint-initdb.d, which the upstream entrypoint
# runs exactly once, on first init. That is right for project provisioning --
# re-running CREATE DATABASE against a live cluster would be wrong -- but it is
# wrong for cluster-level objects that a later change may need to introduce.
#
# The concrete case is the pooler. Enabling the `pooler` profile on a checkout
# whose volume was created months earlier must work; requiring `make destroy`
# to gain a connection pooler would be an absurd trade. So the objects the
# pooler needs are converged here, idempotently, after the server is accepting
# connections.
#
# This runs as a oneshot AFTER mdb-postgres, so it must wait for readiness
# itself rather than assuming it.

MDB_STAGE=mdb-converge
export MDB_STAGE
# The absolute path is correct inside the image; this tells shellcheck where
# to find the same file in the repository.
# shellcheck source=mdb-lib.sh
source /usr/local/lib/mdb/mdb-lib.sh

secret=/run/secrets/pgbouncer_password.txt

# This stage converges more than one thing, so nothing here may exit early on
# behalf of the others. An absent pgbouncer secret used to `exit 0` for the
# whole script, which silently skipped PITR setup entirely -- the stanza was
# never created and archiving never started, on a container that reported
# converging successfully.
converge_pgbouncer=0
[[ -r "$secret" ]] && converge_pgbouncer=1

# Skip the readiness wait only when EVERY concern this script owns is absent.
# The first version of this gate skipped on "no /run/secrets", which read as
# "not a composed engine" -- and broke check 37, whose bare container mounts no
# secrets and legitimately expects PITR convergence from MDB_PG_PITR=true
# alone. The concerns decide, not the mounts: with no pooler secret and PITR
# off, there is nothing to wait FOR, and waiting anyway stalls the CMD in a
# client container (`docker run <image> psql ...`, check 12) by up to 300s
# against a server that is never going to exist.
if (( ! converge_pgbouncer )) && ! is_true "${MDB_PG_PITR:-false}"; then
    stage "no pooler secret and PITR off; nothing to converge"
    exit 0
fi

# Bounded wait. An unbounded one turns "PostgreSQL failed to start" into a
# container that hangs with no explanation, which is strictly worse.
# 300 seconds, not 60 -- a fresh volume's full initialisation can exceed the
# short budget on a loaded machine -- and timing out is a FAILURE, not a
# skip-with-success. The mysql-family twin of this script exited 0 here and
# left the pooler looping on Access denied against an engine whose log said
# the convergence service started successfully.
deadline=$(( SECONDS + 300 ))
until gosu postgres pg_isready -q -h /var/run/postgresql 2>/dev/null; do
    if (( SECONDS >= deadline )); then
        stage "PostgreSQL not ready after 300s; convergence FAILED"
        # Marker + exit 0, not exit 1 -- see the mysql-family twin. This image
        # runs `docker run <image> psql ...` as a pure client (check 12 does),
        # where no server exists for this probe to find; a failing oneshot
        # halts s6 before that psql ever executes. The healthcheck refuses on
        # the marker instead, which only a real server container has.
        mkdir -p /run/mdb && : > /run/mdb/converge-failed
        exit 0
    fi
    sleep 1
done

if (( converge_pgbouncer )); then
# The pooler's role and its narrow auth function. Moved verbatim to
# converge-pooler.sh so the Patroni image -- which has no converge stage --
# runs exactly the same SQL on whichever node leads (design.md D-72).
/usr/local/lib/mdb/converge-pooler.sh "$secret" -- \
    gosu postgres psql -h /var/run/postgresql

stage "converged pooler auth objects"
else
    stage "no pgbouncer secret mounted; skipping the pooler objects"
fi

# -----------------------------------------------------------------------------
# PITR: create the stanza and take the first backup
# -----------------------------------------------------------------------------
# stanza-create needs a RUNNING server, which is exactly why this is here and
# the archive settings are in stage 2: archive_mode cannot be reloaded, and a
# stanza cannot be created before there is something to query.
#
# Both are idempotent, so a restart re-checks rather than re-does.
if is_true "${MDB_PG_PITR:-false}"; then
    stanza="${MDB_PGBACKREST_STANZA:-mdb}"

    if gosu postgres pgbackrest --stanza="$stanza" stanza-create 2>&1 | grep -qi 'error'; then
        stage "WARNING: stanza-create reported an error; PITR is NOT active"
    else
        stage "pgBackRest stanza '$stanza' ready"
    fi

    # A stanza with no full backup can archive WAL and restore nothing: recovery
    # replays forward FROM a base backup, so without one the segments are
    # inert. Taking it here means PITR is usable from first boot rather than
    # from whenever someone remembers to run a backup.
    if ! gosu postgres pgbackrest --stanza="$stanza" info 2>/dev/null | grep -q 'full backup'; then
        stage "taking the initial full backup (PITR needs a base to replay from)"
        gosu postgres pgbackrest --stanza="$stanza" --type=full backup >/dev/null 2>&1 \
            || stage "WARNING: the initial full backup failed; PITR is NOT usable yet"
    fi

    if gosu postgres pgbackrest --stanza="$stanza" check >/dev/null 2>&1; then
        stage "pgBackRest check passed: archiving and the repository both work"
    else
        stage "WARNING: pgBackRest check failed; archiving may not be working"
    fi
fi
