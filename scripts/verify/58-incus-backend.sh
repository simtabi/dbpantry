#!/usr/bin/env bash
# verify: the Incus backend keeps the backend contract and maps each verb onto incus-compose and incus
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# MDB_BACKEND=incus (design.md D-66) is UNTESTED on a live Incus host. This
# check pins its mapping against stub incus and incus-compose binaries: every
# required verb exists, every incus call is pinned to the project, exec -u is
# passed to incus-compose, health reads ic-healthd's user.healthcheck.status,
# and nothing runs without incus-compose on PATH.

cd "$MDB_ROOT" || exit 1

tmp="$(mktemp -d "${TMPDIR:-/tmp}/mdb-incus.XXXXXX")"
add_cleanup "rm -rf '$tmp'"
stubs="$tmp/bin"; mkdir -p "$stubs" "$tmp/root"
for t in incus incus-compose; do
    cat > "$stubs/$t" <<STUB
#!/usr/bin/env bash
printf '$t %s\n' "\$*" >> "\$STUB_LOG"
case "\$*" in
    "list -f csv -c ns "*) printf 'pg-1,RUNNING\nmysql-1,STOPPED\nic-healthd-1,RUNNING\n' ;;
    "list -f csv -c s ^pg-1\$ "*) echo RUNNING ;;
    "list -f csv -c s ^mysql-1\$ "*) echo STOPPED ;;
    "config get pg-1 user.healthcheck.status "*) echo "\${STUB_PG_HEALTH-healthy}" ;;
    "config get pg-1 user.healthcheck.enabled "*) echo true ;;
    "storage volume list "*) printf 'custom,proj_pg_data\nimage,abc\ncustom,proj_pg_pitr\n' ;;
esac
exit 0
STUB
    chmod +x "$stubs/$t"
done
log="$tmp/calls.log"
printf 'COMPOSE_PROJECT_NAME=proj\nCOMPOSE_FILE=compose.yml\nCOMPOSE_PROFILES=pg\n' > "$tmp/root/.env"
: > "$tmp/root/compose.yml"

# inc <PATH> <bash snippet> [VAR=value...]
inc() {
    local path="$1" snippet="$2"; shift 2
    : > "$log"
    # shellcheck disable=SC2016  # expanded by the inner bash
    env -i PATH="$path" HOME="$HOME" STUB_LOG="$log" MDB_RT_ROOT="$tmp/root" MDB_BACKEND=incus "$@" \
        bash -c '. "$1"; mdb_backend_init || exit; '"$snippet" _ "$MDB_ROOT/scripts/backend-lib.sh" 2>&1 || true
}
P="$stubs:/usr/bin:/bin"

for v in $MDB_BACKEND_REQUIRED_VERBS exec_tty status logs cp volume_rm image_load secrets_sync info; do
    got="$(inc "$P" "declare -F _mdb_be_incus_$v >/dev/null && echo yes")"
    [[ "$got" == yes ]] || vfail "the incus backend has no '$v' verb"
done

# shellcheck disable=SC2016  # expanded by the inner bash
got="$(inc /usr/bin:/bin 'mdb_svc_exec pg true; echo rc=$?')"
[[ "$got" == *"needs incus-compose"* && "$got" == *rc=127* ]] || vfail "no incus-compose was not refused: [$got]"

got="$(inc "$P" 'mdb_be_running')"
[[ "$got" == pg ]] || vfail "running is not the RUNNING services without the healthd sidecar: [$got]"
grep '^incus ' "$log" | grep -qv -- '--project proj$' && vfail "an incus call is not pinned to the project: $(grep '^incus ' "$log" | grep -v -- '--project proj$' | head -1)"

[[ "$(inc "$P" 'mdb_svc_health pg')" == healthy ]] || vfail "health does not read user.healthcheck.status"
[[ "$(inc "$P" 'mdb_svc_health pg' STUB_PG_HEALTH=starting)" == starting ]] || vfail "a starting instance is not starting"
[[ "$(inc "$P" 'mdb_svc_health pg' STUB_PG_HEALTH=)" == starting ]] || vfail "a watched instance with no verdict yet is not starting"
[[ "$(inc "$P" 'mdb_svc_health mysql')" == unhealthy ]] || vfail "a stopped instance is not unhealthy"
[[ "$(inc "$P" 'mdb_svc_health mongodb')" == absent ]] || vfail "a missing instance is not absent"

inc "$P" 'mdb_svc_exec -u postgres pg psql -c "select 1"' >/dev/null
grep -qx "incus-compose -f $tmp/root/compose.yml --profile pg exec -T -u postgres pg psql -c select 1" "$log" \
    || vfail "exec -u is not incus-compose exec -T -u, with the stack's files and profiles: $(cat "$log")"
inc "$P" 'mdb_svc_exec pg true' >/dev/null
grep -q 'exec -T pg true$' "$log" || vfail "exec without -u is not incus-compose exec -T: $(cat "$log")"

inc "$P" 'mdb_svc_cp pg:/tmp/x /tmp/y' >/dev/null
grep -q 'cp pg:/tmp/x /tmp/y$' "$log" || vfail "cp is not incus-compose cp"

got="$(inc "$P" 'mdb_be_volumes')"
[[ "$got" == $'proj_pg_data\nproj_pg_pitr' ]] || vfail "volumes are not the project's custom volumes: [$got]"
grep -q '^incus storage volume list default -f csv -c tn --project proj$' "$log" || vfail "volumes do not default to pool 'default': $(cat "$log")"
inc "$P" 'mdb_be_volume_rm proj_pg_data' MDB_INCUS_POOL=fast >/dev/null
grep -q '^incus storage volume delete fast proj_pg_data --project proj$' "$log" || vfail "volume_rm does not delete from MDB_INCUS_POOL"

inc "$P" 'mdb_be_down' >/dev/null
grep -q "^incus-compose -f $tmp/root/compose.yml --profile \* down$" "$log" || vfail "down is not incus-compose down over every profile: $(cat "$log")"

# shellcheck disable=SC2016  # expanded by the inner bash
got="$(inc "$P" 'mdb_be_up; echo rc=$?' MDB_WAIT_TIMEOUT=4)"
[[ "$got" == *rc=0* ]] || vfail "up did not return once the running services were healthy: [$got]"
grep -q 'up -d$' "$log" || vfail "up is not incus-compose up -d"
# shellcheck disable=SC2016  # expanded by the inner bash
got="$(inc "$P" 'mdb_be_up; echo rc=$?' MDB_WAIT_TIMEOUT=4 STUB_PG_HEALTH=unhealthy)"
[[ "$got" == *"pg is unhealthy"* && "$got" != *rc=0* ]] || vfail "up did not fail on an unhealthy service: [$got]"

vinfo "incus backend: verbs map onto incus-compose and incus, pinned to the project (stubs only; untested on a live host)"
