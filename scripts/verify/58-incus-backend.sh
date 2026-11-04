#!/usr/bin/env bash
# verify: the Incus backend keeps the backend contract and maps each verb onto incus-compose and incus
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# MDB_BACKEND=incus (design.md D-66) was run on a live host on 2026-11-04 (Incus
# 7.0.2, incus-compose 1.3.5; docs/guides/incus.md). This
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
[[ "$t" == incus-compose ]] && printf 'gateway %s\n' "\${MDB_NETWORK_GATEWAY:-}" >> "\$STUB_LOG"
case "\$*" in
    "exec pg-1 -- id -u postgres "*) echo 999 ;;
    "exec pg-1 -- id -u nobody-here "*) exit 1 ;;
    "list -f csv -c ns "*) printf 'pg-1,RUNNING\nmysql-1,STOPPED\nic-healthd-1,RUNNING\n' ;;
    "list -f csv -c s ^pg-1\$ "*) echo RUNNING ;;
    "list -f csv -c s ^mysql-1\$ "*) echo STOPPED ;;
    "config get pg-1 user.healthcheck.status "*) echo "\${STUB_PG_HEALTH-healthy}" ;;
    "config get pg-1 user.healthcheck.enabled "*) echo true ;;
    "storage volume list "*) printf 'custom,vol-pg-data\nimage,abc\ncustom,vol-pg-pitr\n' ;;
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

# incus-compose exec -u takes a numeric uid only (measured on 1.3.5, 2026-11-04),
# so a name is resolved inside the service's instance first.
inc "$P" 'mdb_svc_exec -u postgres pg psql -c "select 1"' >/dev/null
grep -qx "incus exec pg-1 -- id -u postgres --project proj" "$log" \
    || vfail "exec -u NAME does not resolve the name inside the instance: $(cat "$log")"
grep -qx "incus-compose -f $tmp/root/compose.yml --profile pg exec -T -u 999 pg psql -c select 1" "$log" \
    || vfail "exec -u is not incus-compose exec -T -u UID, with the stack's files and profiles: $(cat "$log")"
inc "$P" 'mdb_svc_exec -u 70 pg true' >/dev/null
grep -q 'exec -T -u 70 pg true$' "$log" || vfail "a numeric -u is not passed through as it is: $(cat "$log")"
grep -q '^incus exec ' "$log" && vfail "a numeric -u was resolved inside the instance"
# shellcheck disable=SC2016  # expanded by the inner bash
got="$(inc "$P" 'mdb_svc_exec -u nobody-here pg true; echo rc=$?')"
[[ "$got" == *"no user 'nobody-here' in service pg"* && "$got" != *rc=0* ]] \
    || vfail "an unknown user was not refused: [$got]"
grep -q '^incus-compose ' "$log" && vfail "exec ran although the user could not be resolved"

# The network needs a gateway on Incus: compose.incus.yml names it, last.
cp "$MDB_ROOT/compose.incus.yml" "$tmp/root/compose.incus.yml"
inc "$P" 'mdb_svc_exec pg true' >/dev/null
grep -qx "incus-compose -f $tmp/root/compose.yml -f $tmp/root/compose.incus.yml --profile pg exec -T pg true" "$log" \
    || vfail "compose.incus.yml is not passed after the stack's files: $(cat "$log")"
grep -qx 'gateway 172.28.0.1' "$log" || vfail "the default gateway is not the default subnet's first host: $(cat "$log")"
inc "$P" 'mdb_svc_exec pg true' MDB_NETWORK_SUBNET=10.20.0.0/16 >/dev/null
grep -qx 'gateway 10.20.0.1' "$log" || vfail "the gateway does not follow MDB_NETWORK_SUBNET: $(cat "$log")"
inc "$P" 'mdb_svc_exec pg true' MDB_NETWORK_SUBNET=10.20.0.0/16 MDB_NETWORK_GATEWAY=10.20.0.254 >/dev/null
grep -qx 'gateway 10.20.0.254' "$log" || vfail "MDB_NETWORK_GATEWAY does not win: $(cat "$log")"
# shellcheck disable=SC2016  # a literal ${...} in compose.incus.yml
grep -q 'gateway: ${MDB_NETWORK_GATEWAY' "$MDB_ROOT/compose.incus.yml" \
    || vfail "compose.incus.yml does not read MDB_NETWORK_GATEWAY"
rm -f "$tmp/root/compose.incus.yml"
inc "$P" 'mdb_svc_exec pg true' >/dev/null
grep -q 'exec -T pg true$' "$log" || vfail "exec without -u is not incus-compose exec -T: $(cat "$log")"

inc "$P" 'mdb_svc_cp pg:/tmp/x /tmp/y' >/dev/null
grep -q 'cp pg:/tmp/x /tmp/y$' "$log" || vfail "cp is not incus-compose cp"

# Volume names as incus-compose 1.3.5 creates them on a live host: vol-<name>.
got="$(inc "$P" 'mdb_be_volumes')"
[[ "$got" == $'vol-pg-data\nvol-pg-pitr' ]] || vfail "volumes are not the project's custom volumes: [$got]"
grep -q '^incus storage volume list default -f csv -c tn --project proj$' "$log" || vfail "volumes do not default to pool 'default': $(cat "$log")"
inc "$P" 'mdb_be_volume_rm vol-pg-data' MDB_INCUS_POOL=fast >/dev/null
grep -q '^incus storage volume delete fast vol-pg-data --project proj$' "$log" || vfail "volume_rm does not delete from MDB_INCUS_POOL"

# destroy keeps recovery volumes out of --all; on Incus that one is vol-pg-pitr.
recovery="$(sed -n '/^is_recovery_volume()/,/^}/p' scripts/destroy)"
[[ -n "$recovery" ]] || vfail "scripts/destroy has no is_recovery_volume"
for v in vol-pg-pitr mdbverify_pg_pitr; do
    bash -c "$recovery"$'\n''is_recovery_volume "$1"' _ "$v" || vfail "destroy does not keep $v as a recovery volume"
done
bash -c "$recovery"$'\n''is_recovery_volume "$1"' _ vol-pg-data && vfail "destroy treats vol-pg-data as a recovery volume"

inc "$P" 'mdb_be_down' >/dev/null
grep -q "^incus-compose -f $tmp/root/compose.yml --profile \* down$" "$log" || vfail "down is not incus-compose down over every profile: $(cat "$log")"

# shellcheck disable=SC2016  # expanded by the inner bash
got="$(inc "$P" 'mdb_be_up; echo rc=$?' MDB_WAIT_TIMEOUT=4)"
[[ "$got" == *rc=0* ]] || vfail "up did not return once the running services were healthy: [$got]"
grep -q 'up -d$' "$log" || vfail "up is not incus-compose up -d"
# shellcheck disable=SC2016  # expanded by the inner bash
got="$(inc "$P" 'mdb_be_up; echo rc=$?' MDB_WAIT_TIMEOUT=4 STUB_PG_HEALTH=unhealthy)"
[[ "$got" == *"pg is unhealthy"* && "$got" != *rc=0* ]] || vfail "up did not fail on an unhealthy service: [$got]"

# A secret's target is absolute: Compose reads a bare name as /run/secrets/<name>,
# incus-compose 1.3.5 as /<name>, so pgAdmin found no password and exited.
n=0
while IFS= read -r line; do
    n=$(( n + 1 ))
    [[ "$line" =~ target:\ */ ]] || vfail "a secret target is not absolute: $line"
done < <(awk '/^    secrets:/{s=1; next} s && /^    [a-z]/{s=0} s && /target:/{print FILENAME": "$0}' \
         docker-compose.yml compose.*.yml)
(( n >= 1 )) || vfail "found no service secret target to inspect (expected pgAdmin's)"

# The rootless low-port rule applies where the runtime publishes ports, Compose
# only: on Incus a proxy device publishes them and Podman only builds images.
rule="$(grep -n 'mdb_runtime_rootless; then' scripts/check-env)"
[[ -n "$rule" ]] || vfail "check-env has no rootless port rule to inspect"
sed -n "$(( ${rule%%:*} - 1 )),${rule%%:*}p" scripts/check-env | grep -q 'MDB_BACKEND:-compose}" == compose' \
    || vfail "check-env applies the rootless port rule on every backend"

vinfo "incus backend: verbs map onto incus-compose and incus, pinned to the project (stubs; run live on Incus 7.0.2 + incus-compose 1.3.5, 2026-11-04)"
