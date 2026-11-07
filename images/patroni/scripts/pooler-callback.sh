#!/usr/bin/env bash
#
# Patroni callback: converge the pooler's auth objects on the node that leads
# (design.md D-72).
#
# The standalone image creates the pgbouncer role and pgbouncer.get_auth() in
# its converge stage. The Patroni image deletes that stage, because it assumes
# the upstream entrypoint's first-run contract -- so an HA cluster had neither,
# and no pooler could authenticate anyone through it. Patroni calls this as
#   pooler-callback.sh <action> <role> <scope>
# on every start and role change; on the primary it runs the same SQL as the
# standalone image (converge-pooler.sh), idempotently. Replicas receive the
# objects by replication, so they do nothing here.
#
# Runs as postgres, over the local socket (peer, mapped in pg_hba). Bounded:
# Patroni does not wait for callbacks, but a callback that never ends is a
# process that never ends.

set -uo pipefail

action="${1:-}" role="${2:-}"
secret=/etc/patroni/pgbouncer_password

log() { printf '[pooler-callback] %s\n' "$*" >&2; }

case "$action" in on_start|on_role_change) ;; *) exit 0 ;; esac
case "$role" in primary|master) ;; *) exit 0 ;; esac
[[ -r "$secret" ]] || { log "no pooler secret staged; nothing to converge"; exit 0; }

psql=(psql -h /var/run/postgresql -U postgres)
for _ in $(seq 1 90); do
    state="$("${psql[@]}" -d postgres -qtAX -c 'SELECT pg_is_in_recovery()' 2>/dev/null || true)"
    if [[ "$state" == "f" ]]; then
        if /usr/local/lib/mdb/converge-pooler.sh "$secret" -- "${psql[@]}"; then
            log "converged pooler auth objects ($action as $role)"
            exit 0
        fi
        log "convergence failed; retrying"
    fi
    sleep 2
done
log "gave up after 180s: the pooler cannot authenticate through this cluster until it converges"
exit 1
