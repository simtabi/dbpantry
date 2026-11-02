#!/usr/bin/env bash
# verify: Kubernetes HA (Patroni by default, CloudNativePG opt-in) and backup CronJobs render as designed, and prune only their own volume
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# The ha and backup profiles on Kubernetes (design.md D-65), without a cluster:
#
#   1. scripts/helm-values: the ha profile turns on engines.pg.ha with Patroni
#      unless MDB_K8S_HA_PROVIDER=cnpg; MDB_BACKUP_SCHEDULE (HHMM) becomes a
#      daily cron line; a bad provider or schedule is refused;
#   2. run-patroni.sh writes a kubernetes DCS block whose role labels are the
#      ones the Services select on, and still writes etcd3 for Compose;
#   3. the chart: Patroni replaces the standalone StatefulSet, gets a
#      namespace-scoped Role (never a ClusterRole), "pg" selects the primary
#      and "pg-ro" the replicas; CloudNativePG refuses without its operator
#      and is kept on uninstall; each backup CronJob mounts its own volume, so
#      retention never reaches another engine's dumps or the checkout's backups/.
# Real clusters are tested by hand: docs/guides/kubernetes.md.

cd "$MDB_ROOT" || exit 1

tmp="$(mktemp -d "${TMPDIR:-/tmp}/mdb-k8s-ha.XXXXXX")"
add_cleanup "rm -rf '$tmp'"

# --- 1. helm-values --------------------------------------------------------------
printf 'MDB_PROFILES="pg,ha,backup"\nMDB_BACKUP_SCHEDULE=0215\nMDB_TZ=UTC\n' > "$tmp/env"
hv() { env -i PATH="$PATH" MDB_HELM_ENV_FILE="$tmp/env" "$@" bash scripts/helm-values 2>"$tmp/err"; }
out="$(hv)" || vfail "helm-values failed: $(cat "$tmp/err")"
printf '%s\n' "$out" | grep -A2 '^    ha:$' | grep -qx '      enabled: true' || vfail "the ha profile did not enable engines.pg.ha"
printf '%s\n' "$out" | grep -qx '      provider: "patroni"' || vfail "Patroni is not the default HA provider"
printf '%s\n' "$out" | grep -qx '  schedule: "15 2 \* \* \*"' || vfail "MDB_BACKUP_SCHEDULE=0215 is not a daily cron line: $(printf '%s\n' "$out" | grep schedule)"
grep -q 'not deployed on Kubernetes yet' "$tmp/err" && vfail "ha or backup reported as not deployed: $(cat "$tmp/err")"
out="$(hv MDB_K8S_HA_PROVIDER=cnpg)" || vfail "helm-values failed for cnpg"
printf '%s\n' "$out" | grep -qx '      provider: "cnpg"' || vfail "MDB_K8S_HA_PROVIDER=cnpg was not passed"
hv MDB_K8S_HA_PROVIDER=stolon >/dev/null && vfail "an unknown HA provider was accepted"
hv MDB_BACKUP_SCHEDULE=3am >/dev/null && vfail "a schedule that is not HHMM was accepted"
vinfo "the ha profile picks Patroni unless cnpg is asked for; MDB_BACKUP_SCHEDULE becomes cron"

# --- 2. run-patroni.sh -------------------------------------------------------------
# Run the config writer with its root-only side effects stubbed out, and read
# the file it generates.
rp="$tmp/run-patroni.sh"
# shellcheck disable=SC2016  # sed patterns that match the script's literal text
sed -e "s#^conf=/etc/patroni/patroni.yml#conf=$tmp/patroni.yml#" \
    -e "s#^source /usr/local/lib/mdb/mdb-lib.sh#stage() { :; }; die() { echo \"\$*\" >\&2; exit 1; }#" \
    -e "s#\"/run/secrets/\$1\"#\"$tmp/secrets/\$1\"#" \
    -e 's#^install #: install #' -e 's#^chown #: chown #' -e 's#^chmod #: chmod #' -e 's#^mkdir -p "\$data_dir"#:#' \
    -e 's#^exec s6-setuidgid.*#exit 0#' -e 's#^    /usr/local/bin/patroni "\$conf"##' \
    images/patroni/scripts/run-patroni.sh > "$rp"
mkdir -p "$tmp/secrets"
for f in pg_superuser_password.txt pg_replication_password.txt patroni_rest_password.txt; do echo pw > "$tmp/secrets/$f"; done
env -i PATH="$PATH" PATRONI_NAME=rel-pg-1 MDB_PATRONI_DCS=kubernetes MDB_PATRONI_CONNECT_ADDRESS=10.1.2.3 \
    MDB_PATRONI_K8S_NAMESPACE=ns MDB_PATRONI_K8S_LABELS='{app.kubernetes.io/instance: rel}' \
    bash "$rp" 2>"$tmp/err" || vfail "run-patroni.sh (kubernetes) failed: $(cat "$tmp/err")"
for want in '^kubernetes:$' '^  namespace: ns$' '^  role_label: role$' '^  leader_label_value: primary$' \
            '^  follower_label_value: replica$' '^  connect_address: 10.1.2.3:8008$' '^  connect_address: 10.1.2.3:5432$'; do
    grep -qE "$want" "$tmp/patroni.yml" || vfail "patroni.yml (kubernetes) lacks $want"
done
grep -q '^etcd3:' "$tmp/patroni.yml" && vfail "the kubernetes DCS config still names etcd3"
# Patroni hands its global namespace to the DCS, where it overrides
# kubernetes.namespace: the members then query /api/v1/namespaces//multidb-server//
# and never elect a leader.
grep -q '^namespace:' "$tmp/patroni.yml" && vfail "the kubernetes DCS config sets a global namespace, which overrides the pod namespace"
env -i PATH="$PATH" PATRONI_NAME=patroni1 PATRONI_ETCD3_HOSTS="'etcd1:2379','etcd2:2379'" \
    bash "$rp" 2>"$tmp/err" || vfail "run-patroni.sh (etcd3) failed: $(cat "$tmp/err")"
if ! grep -qx '    - etcd2:2379' "$tmp/patroni.yml" || ! grep -qx '  connect_address: patroni1:8008' "$tmp/patroni.yml"; then
    vfail "the Compose (etcd3) config changed: $(sed -n '/^etcd3/,/^$/p' "$tmp/patroni.yml")"
fi
grep -qx 'namespace: /multidb-server/' "$tmp/patroni.yml" || vfail "the Compose (etcd3) config lost its D-54 namespace"
vinfo "run-patroni.sh writes the kubernetes DCS with the role labels the Services use, and etcd3 as before"

# --- 3. the chart ------------------------------------------------------------------
command -v helm >/dev/null 2>&1 || vskip "helm is not installed; the chart was not rendered"
chart=deploy/helm/dbpantry
ha=(--set engines.pg.enabled=true --set engines.pg.ha.enabled=true)
helm template r "$chart" "${ha[@]}" >"$tmp/patroni.yaml" 2>"$tmp/err" || vfail "helm template (patroni): $(cat "$tmp/err")"
n="$(grep -c '^kind: StatefulSet' "$tmp/patroni.yaml")"
[[ "$n" == 1 ]] || vfail "HA should replace the standalone StatefulSet; rendered $n"
grep -q 'image: ghcr.io/simtabi/dbpantry-patroni:17' "$tmp/patroni.yaml" || vfail "the HA StatefulSet does not run dbpantry-patroni"
grep -q '^kind: ClusterRole' "$tmp/patroni.yaml" && vfail "Patroni was given cluster-wide rights"
grep -q '^kind: Role$' "$tmp/patroni.yaml" || vfail "Patroni has no Role for its DCS"
awk '/^kind: Service$/{s=1} s&&/^  name: pg$/{n=1} n&&/role: primary/{print "ok"; exit}' "$tmp/patroni.yaml" | grep -q ok \
    || vfail "Service pg does not select role=primary"
awk '/^kind: Service$/{s=1} s&&/^  name: pg-ro$/{n=1} n&&/role: replica/{print "ok"; exit}' "$tmp/patroni.yaml" | grep -q ok \
    || vfail "Service pg-ro does not select role=replica"
grep -q 'value: kubernetes' "$tmp/patroni.yaml" || vfail "the members are not told to use the kubernetes DCS"
grep -q 'allowPrivilegeEscalation: false' "$tmp/patroni.yaml" || vfail "the HA members lost the hardening"
# The Secret sits at /run/secrets, the parent of Patroni's hard-coded token
# path; without a directory for it in the projection the kubelet cannot mount
# the token and every member fails with "read-only file system".
grep -q 'path: kubernetes.io/serviceaccount/' "$tmp/patroni.yaml" \
    || vfail "the secrets volume has no mountpoint for the service account token"
# The bundle is bigger than the 256 KiB last-applied annotation.
grep -q 'apply --server-side.*-f -' scripts/backends/kubernetes.sh \
    || vfail "the backup bundle is applied client-side, which fails past 256 KiB"

helm template r "$chart" "${ha[@]}" --set engines.pg.ha.provider=cnpg >/dev/null 2>"$tmp/err" \
    && vfail "CloudNativePG rendered without its operator's API"
grep -q 'CloudNativePG operator' "$tmp/err" || vfail "the cnpg refusal does not say why: $(cat "$tmp/err")"
helm template r "$chart" "${ha[@]}" --set engines.pg.ha.provider=cnpg --api-versions postgresql.cnpg.io/v1/Cluster \
    >"$tmp/cnpg.yaml" 2>"$tmp/err" || vfail "helm template (cnpg): $(cat "$tmp/err")"
grep -q '^kind: Cluster$' "$tmp/cnpg.yaml" || vfail "no CloudNativePG Cluster rendered"
grep -q '^kind: StatefulSet' "$tmp/cnpg.yaml" && vfail "cnpg also rendered a StatefulSet"
grep -q 'helm.sh/resource-policy: keep' "$tmp/cnpg.yaml" || vfail "the Cluster (which owns the volumes) is deleted on uninstall"
grep -q 'cnpg.io/instanceRole: primary' "$tmp/cnpg.yaml" || vfail "Service pg does not follow the CloudNativePG primary"

helm template r "$chart" --set engines.pg.enabled=true --set engines.mysql.enabled=true --set engines.mongodb.enabled=true \
    --set backup.enabled=true >"$tmp/backup.yaml" 2>"$tmp/err" || vfail "helm template (backup): $(cat "$tmp/err")"
n="$(grep -c '^kind: CronJob' "$tmp/backup.yaml")"
[[ "$n" == 2 ]] || vfail "expected CronJobs for pg and mysql only (Compose has no MongoDB sidecar), rendered $n"
for e in pg mysql; do
    grep -q "claimName: r-backups-$e\$" "$tmp/backup.yaml" || vfail "the $e backup job does not use its own volume"
done
grep -q 'concurrencyPolicy: Forbid' "$tmp/backup.yaml" || vfail "backup jobs may overlap"
grep -q 'scripts/backup --engine pg' "$tmp/backup.yaml" || vfail "the backup job does not run scripts/backup"
grep -q 'hostPath' "$tmp/backup.yaml" && vfail "a backup job mounts a host path"
vinfo "Patroni is namespace-scoped with pg/pg-ro on the role labels; cnpg needs its operator; each backup job prunes only its own volume"
