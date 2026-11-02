#!/usr/bin/env bash
#
# Stage 5: cluster-level convergence, applied on EVERY start.
#
# Stage 4 emits into /docker-entrypoint-initdb.d, which the upstream entrypoint
# runs exactly once. That is right for project provisioning and wrong for
# anything a later change may need to introduce -- enabling the pooler on a
# volume created months ago must not require destroying the data.
#
# What it converges is the pooler's own account. ProxySQL cannot use root:
# root is restricted to the local socket, which is correct hardening and
# deliberately not relaxed here. It gets a narrow account instead, the same way
# pgBouncer does on the PostgreSQL side (design.md D-35):
#
#   * SELECT on mysql.user, to read password VERIFIERS -- never cleartext;
#   * REPLICATION CLIENT, for ProxySQL's monitor module.
#
# Nothing else. A compromised pooler account cannot read application data.

MDB_STAGE=mdb-converge
export MDB_STAGE
# shellcheck source=mdb-lib.sh
source /usr/local/lib/mdb/mdb-lib.sh

secret=/run/secrets/proxysql_admin_password.txt

if [[ ! -r "$secret" ]]; then
    stage "no proxysql secret mounted; nothing to converge"
    exit 0
fi

pw="$(tr -d '\n' < "$secret")"

# The root secret's filename follows the engine, the same way every other
# per-engine path here does.
root_secret="/run/secrets/${MDB_ENGINE}_root_password.txt"
[[ -r "$root_secret" ]] || { stage "no root secret at $root_secret; skipping"; exit 0; }
root_pw="$(tr -d '\n' < "$root_secret")"
esc_pw="${pw//\'/\'\'}"

# Bounded wait. An unbounded one turns "the engine failed to start" into a
# container that hangs with no explanation.
#
# 300 seconds, not 90: a FRESH volume means the official entrypoint runs the
# full initialisation -- temporary server, initdb.d, shutdown, real server --
# and on a two-core CI runner that alone can exceed 90s. And the timeout is a
# FAILURE, not a skip-with-success: this stage is what creates the proxysql
# user, so "skipping convergence" and exiting 0 left the pooler looping on
# ERROR 1045 Access denied against a healthy-looking engine, with the one line
# explaining why buried in a log claiming the service started successfully.
deadline=$(( SECONDS + 300 ))
until "$MDB_CLIENT" --protocol=socket -uroot \
        -p"$root_pw" -e "SELECT 1" >/dev/null 2>&1; do
    if (( SECONDS >= deadline )); then
        stage "engine not ready after 300s; convergence FAILED -- the proxysql
       user does not exist and a pooler pointed here cannot authenticate"
        # A MARKER plus exit 0, not exit 1. This image also runs as a CLIENT --
        # `docker run <image> mysql ...` is how the harness and make shell use
        # it -- and there this oneshot can never succeed, because no server is
        # supposed to exist. Failing the oneshot halts s6 before the command
        # ever runs (check 12 caught exactly that for pg). The marker turns the
        # failure into an UNHEALTHY server instead: the healthcheck refuses
        # while it exists, and a client container has no healthcheck to fail.
        mkdir -p /run/mdb && : > /run/mdb/converge-failed
        exit 0
    fi
    sleep 2
done

"$MDB_CLIENT" --protocol=socket -uroot \
    -p"$root_pw" <<SQL
CREATE USER IF NOT EXISTS 'proxysql'@'%' IDENTIFIED BY '${esc_pw}';
ALTER USER 'proxysql'@'%' IDENTIFIED BY '${esc_pw}';
GRANT USAGE, REPLICATION CLIENT ON *.* TO 'proxysql'@'%';
GRANT SELECT ON mysql.user TO 'proxysql'@'%';
FLUSH PRIVILEGES;
SQL

stage "converged the pooler account"
