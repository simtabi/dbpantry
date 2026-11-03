#!/usr/bin/env bash
# verify: the Kubernetes backend keeps the backend contract, .env renders to Helm values, and the chart lints
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# MDB_BACKEND=kubernetes (design.md D-64) is tested here without a cluster:
#
#   1. scripts/helm-values turns .env into values: the profiles pick the
#      engines, every value is a double-quoted string, the chart's own password
#      files and memory variable are not duplicated;
#   2. the backend, against stub kubectl and helm: nothing runs without
#      MDB_K8S_CONTEXT, every call is pinned to that context and namespace, a
#      terminating pod is never "running", exec -u drops privileges in the pod,
#      and secrets_sync never ships the CA key;
#   3. the chart lints and renders every engine with the hardening the Compose
#      services have (when helm is installed).
# Real clusters are tested by hand: docs/guides/kubernetes.md.

cd "$MDB_ROOT" || exit 1

tmp="$(mktemp -d "${TMPDIR:-/tmp}/mdb-k8s.XXXXXX")"
add_cleanup "rm -rf '$tmp'"

# --- 1. helm-values -------------------------------------------------------------
cat > "$tmp/env" <<'ENV'
MDB_PROFILES="pg,mongodb,ui"
MDB_PG_VERSION=16
MDB_PG_MEM_LIMIT=1g
MDB_PG_CPU_LIMIT=1
MDB_PG_SHM=512m
MDB_TZ="America/New York"
MDB_PG_INIT_EXTENSIONS="vector,pg_stat_statements"
MDB_PG_DATABASES="app:app_user:__FILE__"
MDB_K8S_STORAGE_CLASS=fast-ssd
ENV
out="$(env -i PATH="/usr/bin:/bin" MDB_HELM_ENV_FILE="$tmp/env" MDB_PG_SLOW_MS='say "hi"' \
    bash scripts/helm-values 2>"$tmp/err")" || vfail "helm-values failed: $(cat "$tmp/err")"
section() { printf '%s\n' "$out" | awk -v e="  $1:" '$0 == e { f = 1; next } f && /^  [a-z]/ { exit } f'; }
pg="$(section pg)"; mysql="$(section mysql)"; mongodb="$(section mongodb)"
[[ "$pg" == *"enabled: true"* && "$mongodb" == *"enabled: true"* && "$mysql" == *"enabled: false"* ]] \
    || vfail "MDB_PROFILES did not pick pg and mongodb only"
grep -q 'not deployed on Kubernetes yet: ui' "$tmp/err" || vfail "the ui profile was dropped silently"
printf '%s\n' "$pg" | grep -qx '    tag: "16"' || vfail "MDB_PG_VERSION is not the tag"
printf '%s\n' "$pg" | grep -qx '    memory: "1g"' || vfail "MDB_PG_MEM_LIMIT is not memory"
printf '%s\n' "$pg" | grep -qx '    shm: "512m"' || vfail "MDB_PG_SHM is not shm"
printf '%s\n' "$pg" | grep -qx '      MDB_TZ: "America/New York"' || vfail "a value with a space is not quoted: $(printf '%s\n' "$pg" | grep MDB_TZ)"
printf '%s\n' "$pg" | grep -qx '      MDB_PG_SLOW_MS: "say \\"hi\\""' || vfail "the environment does not win over .env, or a quote is not escaped"
printf '%s\n' "$pg" | grep -q 'MDB_PG_MEM_LIMIT' && vfail "MDB_PG_MEM_LIMIT is duplicated in env (the chart sets it from memory)"
printf '%s\n' "$out" | grep -q '/run/secrets/' && vfail "a /run/secrets path is in env (the chart mounts those)"
printf '%s\n' "$out" | grep -E '^ +[A-Za-z_]+: ' | grep -vE ': ("([^"\\]|\\.)*"|true|false)$' \
    | grep -v '^    enabled' && vfail "values above are not double-quoted strings"
printf '%s\n' "$out" | grep -qx '  storageClass: "fast-ssd"' || vfail "MDB_K8S_STORAGE_CLASS is not global.storageClass"
vinfo "helm-values: profiles pick engines, values stay quoted strings, the environment wins over .env"

# --- 2. the backend against stub kubectl, helm and kind -------------------------
stubs="$tmp/bin"; mkdir -p "$stubs" "$tmp/root/secrets" "$tmp/root/certs/pg" "$tmp/root/scripts"
for t in kubectl helm kind; do
    cat > "$stubs/$t" <<STUB
#!/usr/bin/env bash
printf '$t %s\n' "\$*" >> "\$STUB_LOG"
case "\$*" in
    *"get pods"*) printf '%b' "\${STUB_PODS:-}" ;;
    *"get clusters.postgresql.cnpg.io "*jsonpath*) printf '%s' "\${STUB_CNPG:-}" ;;
    *"get clusters.postgresql.cnpg.io "*) [[ -z "\${STUB_NO_CNPG:-}" ]] || exit 1 ;;
    *"get pvc "*) [[ -z "\${STUB_NO_PVC:-}" ]] || exit 1 ;;
    *"-pg-superuser "*)
        for a in "\$@"; do
            [[ "\$a" == --from-file=password=* ]] && { cat "\${a#--from-file=password=}" > "\$STUB_LOG.superpw"; printf '%s\\n' "\${a#--from-file=password=}" > "\$STUB_LOG.superpath"; }
            [[ "\$a" == --from-file=username=* ]] && cat "\${a#--from-file=username=}" > "\$STUB_LOG.superuser"
        done
        printf 'kind: Secret\n' ;;
    *"create -f -"*) cat >> "\$STUB_LOG.stdin" ;;
    *--dry-run=client*) printf 'kind: Secret\n' ;;
    *" apply "*) cat >/dev/null ;;
esac
exit 0
STUB
    chmod +x "$stubs/$t"
done
log="$tmp/calls.log"
echo pw > "$tmp/root/secrets/pg_superuser_password.txt"
for f in ca.crt ca.key; do echo x > "$tmp/root/certs/$f"; done
for f in server.crt server.key; do echo x > "$tmp/root/certs/pg/$f"; done

# kube <MDB_K8S_CONTEXT> <bash snippet> [VAR=value...]
kube() {
    local ctx="$1" snippet="$2"; shift 2
    printf 'COMPOSE_PROJECT_NAME=proj\nMDB_K8S_CONTEXT=%s\n' "$ctx" > "$tmp/root/.env"
    : > "$log"
    # shellcheck disable=SC2016  # expanded by the inner bash
    env -i PATH="$stubs:/usr/bin:/bin" HOME="$HOME" TMPDIR="${TMPDIR:-/tmp}" STUB_LOG="$log" MDB_RT_ROOT="$tmp/root" MDB_BACKEND=kubernetes "$@" \
        bash -c '. "$1"; mdb_backend_init || exit; '"$snippet" _ "$MDB_ROOT/scripts/backend-lib.sh" 2>&1 || true
}

for v in $MDB_BACKEND_REQUIRED_VERBS exec_tty status logs cp volume_rm image_load secrets_sync info forward; do
    got="$(kube ctx "declare -F _mdb_be_kubernetes_$v >/dev/null && echo yes")"
    [[ "$got" == yes ]] || vfail "the kubernetes backend has no '$v' verb"
done

got="$(kube "" 'mdb_be_running; mdb_svc_exec pg true; echo rc=$?')"
[[ "$got" == *"needs MDB_K8S_CONTEXT"* ]] || vfail "no MDB_K8S_CONTEXT was not refused: [$got]"
grep -q '^kubectl' "$log" && vfail "kubectl ran without MDB_K8S_CONTEXT: $(head -1 "$log")"

pods='pg proj-pg-0 Running True\nmysql proj-mysql-0 Running Terminating\nmariadb proj-mariadb-0 Pending False\n'
got="$(kube ctx 'mdb_be_running' STUB_PODS="$pods")"
[[ "$got" == pg ]] || vfail "running should be only pg (mysql is terminating, mariadb pending): [$got]"
while IFS= read -r call; do
    [[ "$call" == "kubectl --context ctx --namespace proj "* ]] || vfail "kubectl not pinned to the context and namespace: $call"
done < "$log"
grep -q 'app.kubernetes.io/instance=proj' "$log" || vfail "pods are not selected by the release label"

got="$(kube ctx 'mdb_svc_health pg' STUB_PODS='pg proj-pg-0 Running True\n')"
[[ "$got" == healthy ]] || vfail "a Ready pod is not healthy: [$got]"
got="$(kube ctx 'mdb_svc_health pg' STUB_PODS='pg proj-pg-0 Running Terminating\n')"
[[ "$got" == absent ]] || vfail "a terminating pod is not absent: [$got]"
got="$(kube ctx 'mdb_svc_health pg')"
[[ "$got" == absent ]] || vfail "no pod is not absent: [$got]"

kube ctx 'mdb_svc_exec -u postgres pg id' STUB_PODS='pg proj-pg-0 Running True\n' >/dev/null
# The switch script spans lines in the log; its call starts and ends like this.
if ! grep -q -- ' exec -i proj-pg-0 -- sh -c ' "$log" || ! grep -q -- ' sh postgres id$' "$log"; then
    vfail "exec -u did not drop to the user in the pod: $(cat "$log")"
fi
# HA (D-65): commands reach the primary, wherever it is in the list, and a
# backup job's pod is not a service of the stack.
pods='pg proj-pg-0 Running True replica\npg proj-pg-1 Running True primary\nbackup-pg proj-backup-pg-1 Running True -\n'
kube ctx 'mdb_svc_exec pg true' STUB_PODS="$pods" >/dev/null
grep -q -- ' exec -i proj-pg-1 -- true$' "$log" || vfail "exec did not pick the primary: $(grep ' exec ' "$log")"
got="$(kube ctx 'mdb_be_running' STUB_PODS="$pods")"
[[ "$got" == pg ]] || vfail "a backup job's pod is listed as running: [$got]"
vinfo "every kubectl call is pinned to MDB_K8S_CONTEXT; terminating pods are not running; exec -u switches user; exec reaches the HA primary"

kube ctx 'mdb_be_secrets_sync' >/dev/null
grep -q 'create secret generic dbpantry-secrets .*pg_superuser_password.txt' "$log" || vfail "secrets/ was not synced"
grep -q 'create secret generic dbpantry-certs-pg ' "$log" || vfail "certs/pg was not synced"
grep -q 'ca.key' "$log" && vfail "the CA private key was sent to the cluster"
# CloudNativePG uses the basic-auth password byte for byte: the file's trailing
# newline became part of it, and every client sending the file's contents was
# refused ("password authentication failed for user postgres").
[[ "$(od -c < "$log.superpw" 2>/dev/null | head -1)" == "$(printf pw | od -c | head -1)" ]] \
    || vfail "the CloudNativePG superuser Secret password is not the file without its newline: $(od -c < "$log.superpw" 2>/dev/null | head -1)"
# No credential reaches argv or the source: both keys come from files in a
# private temporary directory that is gone once the Secret is applied.
grep -q -- '--from-literal' "$log" && vfail "a Secret key was passed with --from-literal (on argv): $(grep -m1 -- '--from-literal' "$log")"
[[ "$(cat "$log.superuser" 2>/dev/null)" == postgres ]] || vfail "the CloudNativePG superuser Secret username is not postgres"
superdir="$(dirname "$(cat "$log.superpath" 2>/dev/null)")"
[[ -n "$superdir" && "$superdir" != . && ! -e "$superdir" ]] || vfail "the superuser Secret's temporary directory was left behind: $superdir"
grep -nE -- '--from-literal=(username|password)=' scripts/backends/*.sh && vfail "a backend passes a credential key with --from-literal"
# make down hibernates a CloudNativePG Cluster with kubectl annotate, which then
# owns that annotation; helm 4 applies server-side, so the next make up failed
# with "conflict with kubectl-annotate ... cnpg.io/hibernation". up wakes it
# first by removing the annotation (which also drops kubectl's ownership).
kube ctx '_mdb_k8s_cnpg_wake' >/dev/null
grep -q -- ' annotate clusters.postgresql.cnpg.io proj-pg cnpg.io/hibernation-' "$log" \
    || vfail "make up does not wake a hibernated CloudNativePG Cluster before helm: $(cat "$log")"
# helm --wait cannot see a CloudNativePG Cluster (a custom resource), so make up
# returned while the operator was still creating instances. up now waits for
# readyInstances to reach spec.instances, bounded, and fails with the phase.
grep -qE '^ *_mdb_k8s_cnpg_wait_ready$' scripts/backends/kubernetes.sh \
    || vfail "make up on Kubernetes does not wait for a CloudNativePG Cluster after helm"
got="$(kube ctx '_mdb_k8s_cnpg_wait_ready; echo rc=$?' STUB_NO_CNPG=1)"
[[ "$got" == "rc=0" ]] || vfail "with no CloudNativePG Cluster, up should not wait: [$got]"
got="$(kube ctx '_mdb_k8s_cnpg_wait_ready; echo rc=$?' STUB_CNPG='2 2 Cluster in healthy state')"
[[ "$got" == *"ready (2/2 instances)"*"rc=0" ]] || vfail "a ready CloudNativePG Cluster was not accepted: [$got]"
start=$SECONDS
got="$(kube ctx '_mdb_k8s_cnpg_wait_ready; echo rc=$?' STUB_CNPG='1 2 Creating a new replica' \
    MDB_K8S_CNPG_READY_TIMEOUT=2 _MDB_K8S_POLL_SECONDS=1)"
[[ "$got" == *"not ready after 2s: 1/2 instances ready, phase 'Creating a new replica'"* && "$got" == *"rc=1" ]] \
    || vfail "an unready CloudNativePG Cluster did not fail clearly at the timeout: [$got]"
(( SECONDS - start < 15 )) || vfail "the CloudNativePG wait overran MDB_K8S_CNPG_READY_TIMEOUT=2"
got="$(kube ctx '_mdb_k8s_cnpg_wait_ready; echo rc=$?' STUB_CNPG='0 2 Setting up primary' MDB_K8S_CNPG_READY_TIMEOUT=soon)"
[[ "$got" == *"must be a number of seconds"*"rc=2" ]] || vfail "a non-numeric MDB_K8S_CNPG_READY_TIMEOUT was accepted: [$got]"
vinfo "make up waits for a CloudNativePG Cluster's instances, bounded by MDB_K8S_CNPG_READY_TIMEOUT, and fails with its phase"
kube kind-dev 'mdb_be_image_load img:1' >/dev/null
grep -qx 'kind load docker-image img:1 --name dev' "$log" || vfail "a kind context did not kind-load the image"
kube orbstack 'mdb_be_image_load img:1' >/dev/null
grep -q '^kind' "$log" && vfail "OrbStack (shared image store) ran kind"
vinfo "secrets and certs sync without the CA key; images load into kind only"

# Backup claims come from the backend, not the chart: on WaitForFirstConsumer
# storage an unmounted claim stays Pending and `helm --wait` times out on it.
printf 'global:\n  storageClass: "fast"\nengines:\n  pg:\n    enabled: true\n  mysql:\n    enabled: false\n  mariadb:\n    enabled: true\nbackup:\n  enabled: true\n  storage: "5Gi"\n' > "$tmp/values.yaml"
: > "$log.stdin"
kube ctx "_mdb_k8s_backup_claims '$tmp/values.yaml'" STUB_NO_PVC=1 >/dev/null
grep -qx '  name: proj-backups-pg' "$log.stdin" || vfail "no backup claim was created for pg: $(cat "$log.stdin")"
grep -qx '  name: proj-backups-mariadb' "$log.stdin" || vfail "no backup claim was created for mariadb"
grep -q 'proj-backups-mysql' "$log.stdin" && vfail "a backup claim was created for a disabled engine"
grep -qx '      storage: 5Gi' "$log.stdin" || vfail "the backup claim ignored backup.storage"
grep -qx '  storageClassName: fast' "$log.stdin" || vfail "the backup claim ignored global.storageClass"
: > "$log.stdin"
kube ctx "_mdb_k8s_backup_claims '$tmp/values.yaml'" >/dev/null
[[ -s "$log.stdin" ]] && vfail "an existing backup claim was recreated"
# shellcheck disable=SC2016  # a literal line of the backend
grep -qF '_mdb_k8s_backup_claims "$values"' scripts/backends/kubernetes.sh || vfail "make up does not create the backup claims"
vinfo "backup claims are created by the backend for enabled engines, never recreated"

# Check 05 scans config files, not scripts, so the settings the backend and
# scripts/helm-values read from .env are checked here: every one must be
# documented in .env.example.
for key in MDB_BACKEND $(grep -ohE '(^|[^_A-Z])MDB_K8S_[A-Z_]+' scripts/backends/kubernetes.sh scripts/helm-values | grep -oE 'MDB_K8S_[A-Z_]+' | sort -u); do
    grep -qE "^#? *$key=" .env.example || vfail "$key is read by the Kubernetes backend or helm-values but not documented in .env.example"
done
vinfo "MDB_BACKEND and every MDB_K8S_ setting are documented in .env.example"

# make destroy keeps recovery volumes unless --include-recovery. On Kubernetes
# the scheduled dumps live in a PVC (<release>-backups-<engine>), and a destroy
# that took them with the data would repeat the incident the scratch-backup
# rules exist for: the data and every dump of it gone in one command.
eval "$(sed -n '/^is_recovery_volume()/,/^}/p' scripts/destroy)"
for v in proj-backups-pg proj-backups-mysql pitr-proj-pg-0 proj_pg_pitr; do
    is_recovery_volume "$v" || vfail "make destroy --all would delete $v (a recovery volume)"
done
for v in data-proj-pg-0 proj_pg_data proj-pg-1; do
    is_recovery_volume "$v" && vfail "$v is a data volume but destroy treats it as recovery"
done
vinfo "destroy --all keeps the backup and pitr volumes"

# --- 3. the chart -----------------------------------------------------------------
command -v helm >/dev/null 2>&1 || vskip "helm is not installed; the chart was not linted"
chart=deploy/helm/dbpantry
all=(--set engines.pg.enabled=true --set engines.mysql.enabled=true --set engines.mariadb.enabled=true
     --set engines.mongodb.enabled=true --set engines.cassandra.enabled=true)
helm lint "$chart" "${all[@]}" >"$tmp/lint" 2>&1 || vfail "helm lint: $(tail -5 "$tmp/lint")"
helm template t "$chart" "${all[@]}" >"$tmp/all.yaml" 2>"$tmp/err" || vfail "helm template: $(cat "$tmp/err")"
n="$(grep -c '^kind: StatefulSet' "$tmp/all.yaml")"
[[ "$n" == 5 ]] || vfail "expected 5 StatefulSets, rendered $n"
for want in 'allowPrivilegeEscalation: false' 'type: RuntimeDefault' 'whenDeleted: Retain' 'drop: \[ALL\]'; do
    c="$(grep -c -- "$want" "$tmp/all.yaml")"
    [[ "$c" == 5 ]] || vfail "'$want' is on $c of 5 engines"
done
for svc in pg mysql mariadb mongodb cassandra; do
    grep -qx "  name: $svc" "$tmp/all.yaml" || vfail "no Service named $svc (apps use the Compose service names)"
done
c="$(grep -c '^        name: pitr$' "$tmp/all.yaml")"  # the volumeClaimTemplates entries
[[ "$c" == 3 ]] || vfail "pitr volumes on $c engines, expected 3 (pg, mysql, mariadb)"
grep -q 'automountServiceAccountToken: true' "$tmp/all.yaml" && vfail "the engines mount a Kubernetes API token"
vinfo "the chart lints; every engine renders with no privilege escalation, RuntimeDefault seccomp and retained volumes"
