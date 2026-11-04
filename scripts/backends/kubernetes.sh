#!/usr/bin/env bash
# shellcheck shell=bash
# Backend: Kubernetes, through the Helm chart in deploy/helm/dbpantry
# (design.md D-64, docs/guides/kubernetes.md).
#
# Settings (environment, then .env):
#   MDB_K8S_CONTEXT        kubeconfig context to deploy to. REQUIRED: dbpantry
#                          never deploys to whatever context happens to be
#                          current.
#   MDB_K8S_NAMESPACE      namespace (default: the project name)
#   MDB_K8S_RELEASE        Helm release (default: the project name)
#   MDB_K8S_STORAGE_CLASS  StorageClass for the volumes (default: the cluster's)
#   MDB_K8S_VALUES         extra values files, colon-separated, applied last
#   MDB_K8S_LOAD_IMAGES    auto | true | false: copy the locally built images
#                          into the cluster (auto: on kind-* contexts only)
#   MDB_K8S_CNPG_READY_TIMEOUT  seconds `make up` waits for a CloudNativePG
#                          Cluster's instances to be ready (default
#                          MDB_WAIT_TIMEOUT, else 600)
#
# Services are the chart's StatefulSets, found by the label
# app.kubernetes.io/component=<service>; volumes are the release's
# PersistentVolumeClaims. kubectl and helm must be on PATH; KUBECONFIG is
# honoured as usual.

_mdb_k8s_var() { _mdb_rt_compose_var "$1"; }

_mdb_k8s_ctx() {
    local c
    c="$(_mdb_k8s_var MDB_K8S_CONTEXT)"
    [[ -n "$c" ]] || { _mdb_rt_err "MDB_BACKEND=kubernetes needs MDB_K8S_CONTEXT (a kubeconfig context; see kubectl config get-contexts)"; return 2; }
    printf '%s' "$c"
}
_mdb_k8s_ns() {
    local n; n="$(_mdb_k8s_var MDB_K8S_NAMESPACE)"
    printf '%s' "${n:-$(mdb_project_name)}"
}
_mdb_k8s_release() {
    local r; r="$(_mdb_k8s_var MDB_K8S_RELEASE)"
    printf '%s' "${r:-$(mdb_project_name)}"
}

# The engines the chart deploys: every implemented one in the descriptors
# (refactor plan F1, design.md D-68), the same list scripts/helm-values renders.
# The descriptors ship with this code, so they are found beside this file, not
# under MDB_RT_ROOT (the operator's checkout, which holds .env and secrets/).
# A subshell, so loading descriptors never leaks MDB_ENGINE_* into the caller.
_mdb_k8s_engines() {
    (
        # shellcheck source=../engine-lib.sh
        . "$(dirname "${BASH_SOURCE[0]}")/../engine-lib.sh" || exit 1
        for e in $(engine_list); do
            ( engine_load "$e" >/dev/null 2>&1 && engine_implemented ) && printf '%s\n' "$e"
        done
    )
}

_mdb_k8s_tools() {
    local t
    for t in kubectl helm; do
        command -v "$t" >/dev/null 2>&1 || { _mdb_rt_err "MDB_BACKEND=kubernetes needs $t on PATH"; return 127; }
    done
}

# kubectl and helm, always pinned to the configured context and namespace.
_mdb_k8s_kubectl() {
    local ctx ns
    ctx="$(_mdb_k8s_ctx)" || return
    ns="$(_mdb_k8s_ns)"
    kubectl --context "$ctx" --namespace "$ns" "$@"
}
_mdb_k8s_helm() {
    local ctx ns
    ctx="$(_mdb_k8s_ctx)" || return
    ns="$(_mdb_k8s_ns)"
    helm --kube-context "$ctx" --namespace "$ns" "$@"
}

_mdb_k8s_sel() {
    local s
    s="app.kubernetes.io/name=dbpantry,app.kubernetes.io/instance=$(_mdb_k8s_release)"
    [[ -z "${1:-}" ]] || s="$s,app.kubernetes.io/component=$1"
    printf '%s' "$s"
}

# "<component> <pod> <phase> <True|False|Terminating>" for every pod of the
# release. A pod being deleted still reports Running and Ready, so it is
# marked Terminating: nothing may treat it as up (make destroy checks this).
_MDB_K8S_PODS_TPL='{{range .items}}{{index .metadata.labels "app.kubernetes.io/component"}} {{.metadata.name}} {{.status.phase}} {{if .metadata.deletionTimestamp}}Terminating{{else}}{{range .status.conditions}}{{if eq .type "Ready"}}{{.status}}{{end}}{{end}}{{end}} {{or (index .metadata.labels "role") (index .metadata.labels "cnpg.io/instanceRole") "-"}}{{"\n"}}{{end}}'
_mdb_k8s_pods() {
    _mdb_k8s_ctx >/dev/null || return
    _mdb_k8s_kubectl get pods -l "$(_mdb_k8s_sel "${1:-}")" -o go-template="$_MDB_K8S_PODS_TPL" 2>/dev/null
}

# The pod serving SVC: the Ready primary of an HA cluster (Patroni and
# CloudNativePG both label it role=primary), else the first Ready pod, else
# the first one. Backups and restores must reach the primary.
_mdb_k8s_pod() {
    local svc="$1" pods pod
    pods="$(_mdb_k8s_pods "$svc")" || return
    pod="$(printf '%s\n' "$pods" | awk '$4=="True" && $5=="primary" {print $2; exit}')"
    [[ -n "$pod" ]] || pod="$(printf '%s\n' "$pods" | awk '$4=="True" {print $2; exit}')"
    [[ -n "$pod" ]] || pod="$(printf '%s\n' "$pods" | awk 'NF {print $2; exit}')"
    [[ -n "$pod" ]] || { _mdb_rt_err "service $svc is not running in $(_mdb_k8s_ns)/$(_mdb_k8s_release)"; return 1; }
    printf '%s' "$pod"
}

# Run "$@" as USER inside the pod: kubectl exec has no -u, so drop privileges
# with whatever the image has (s6-setuidgid in the dbpantry images, gosu in
# the upstream ones, setpriv from util-linux otherwise), or run as is when the
# container already runs as USER (CloudNativePG's runs as postgres).
# shellcheck disable=SC2016  # expanded by the pod's sh, not here
_MDB_K8S_ASUSER='u="$1"; shift
if [ "$(id -un 2>/dev/null)" = "$u" ]; then exec "$@"
elif command -v s6-setuidgid >/dev/null 2>&1; then exec s6-setuidgid "$u" "$@"
elif command -v gosu >/dev/null 2>&1; then exec gosu "$u" "$@"
elif command -v setpriv >/dev/null 2>&1; then exec setpriv --reuid="$u" --regid="$u" --init-groups "$@"
else echo "no way to switch to user $u in this image" >&2; exit 126; fi'

_mdb_k8s_exec() {
    local tty="$1"; shift
    _mdb_svc_args "$@" || return; shift "$_MDB_SVC_SHIFT"
    local pod flags=(-i)
    pod="$(_mdb_k8s_pod "$_MDB_SVC")" || return
    [[ "$tty" == 1 ]] && [[ -t 0 && -t 1 ]] && flags=(-it)
    if [[ -n "$_MDB_SVC_USER" ]]; then
        _mdb_k8s_kubectl exec "${flags[@]}" "$pod" -- sh -c "$_MDB_K8S_ASUSER" sh "$_MDB_SVC_USER" "$@"
    else
        _mdb_k8s_kubectl exec "${flags[@]}" "$pod" -- "$@"
    fi
}

_mdb_be_kubernetes_exec()     { _mdb_k8s_exec 0 "$@"; }
_mdb_be_kubernetes_exec_tty() { _mdb_k8s_exec 1 "$@"; }

_mdb_be_kubernetes_running() {
    local pods
    pods="$(_mdb_k8s_pods)" || return
    # Engines only: a scheduled backup's pod (component backup-<engine>) is
    # not a service of the stack.
    printf '%s\n' "$pods" | awk '$3=="Running" && $4=="True" && $1 !~ /^backup-/ {print $1}' | sort -u
}

_mdb_be_kubernetes_health() {
    local line phase ready
    line="$(_mdb_k8s_pods "$1")" || return
    line="$(printf '%s\n' "$line" | awk 'NF' | head -1)"
    if [[ -z "$line" ]]; then printf 'absent\n'; return 0; fi
    read -r _ _ phase ready <<<"$line"
    case "$phase/${ready:-}" in
        Running/True) printf 'healthy\n' ;;
        */Terminating) printf 'absent\n' ;;
        Running/*|Pending/*) printf 'starting\n' ;;
        *) printf 'unhealthy\n' ;;
    esac
}

_mdb_be_kubernetes_cp() {
    local src="$1" dest="$2" pod
    pod="$(_mdb_k8s_pod "${src%%:*}")" || return
    [[ -d "$dest" ]] && dest="$dest/$(basename "${src#*:}")"
    _mdb_k8s_kubectl exec -i "$pod" -- cat "${src#*:}" >"$dest"
}

_mdb_be_kubernetes_volumes() {
    _mdb_k8s_ctx >/dev/null || return
    _mdb_k8s_kubectl get pvc -l "$(_mdb_k8s_sel)" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | awk 'NF' | sort
}

_mdb_be_kubernetes_volume_rm() {
    local pvc="$1" scope
    case "$pvc" in
        data-"$(_mdb_k8s_release)"-pg-*)
            # Patroni keeps the cluster's identity in ConfigMaps it creates
            # itself (<scope>-config holds the system id). Left behind, a new
            # cluster on fresh volumes waits forever for a leader that already
            # "initialised", so they go with the data.
            scope="$(_mdb_k8s_var MDB_HA_CLUSTER_NAME)"
            _mdb_k8s_kubectl delete configmap -l "app.kubernetes.io/instance=$(_mdb_k8s_release),cluster-name=${scope:-mdb-pg}" \
                --ignore-not-found >/dev/null 2>&1 || true ;;
        "$(_mdb_k8s_release)"-pg-[0-9]*)
            # A CloudNativePG instance volume belongs to its Cluster, which
            # `make down` hibernates rather than deletes; destroying the data
            # deletes the Cluster first, or the operator recreates the volume.
            _mdb_k8s_kubectl delete clusters.postgresql.cnpg.io "$(_mdb_k8s_release)-pg" \
                --ignore-not-found --wait=true >/dev/null 2>&1 || true ;;
    esac
    _mdb_k8s_kubectl delete pvc "$pvc" --ignore-not-found --wait=true
}

# The CloudNativePG superuser as a kubernetes.io/basic-auth Secret, built from
# two files in a private temporary directory (umask 077, removed on exit).
# Every key is a --from-file, so neither the role name nor the password is
# ever on a command line, and no line of source pairs a literal with a
# credential key (secret scanners report that shape as a leaked credential).
_mdb_k8s_superuser_secret() {
    local pwfile="$1"
    (
        umask 077
        d="$(mktemp -d "${TMPDIR:-/tmp}/dbpantry-su.XXXXXX")" || exit 1
        trap 'rm -rf "$d"' EXIT
        printf '%s' postgres > "$d/username"
        tr -d '\n' < "$pwfile" > "$d/password" || exit 1
        _mdb_k8s_kubectl create secret generic "$(_mdb_k8s_release)-pg-superuser" --type=kubernetes.io/basic-auth \
            --from-file=username="$d/username" --from-file=password="$d/password" \
            --dry-run=client -o yaml | _mdb_k8s_kubectl apply -f - >/dev/null
    )
}

# Publish secrets/*.txt as the chart's Secret, certs/ as one TLS Secret per
# engine and overrides/<engine>/ as ConfigMaps. kubectl apply, so a rotation
# is the same command. The CA key never leaves the checkout.
_mdb_be_kubernetes_secrets_sync() {
    local root="$MDB_RT_ROOT" ns f args=() eng d
    ns="$(_mdb_k8s_ns)"
    _mdb_k8s_ctx >/dev/null || return
    if ! _mdb_k8s_kubectl get namespace "$ns" >/dev/null 2>&1; then
        _mdb_k8s_kubectl create namespace "$ns" >/dev/null || return
    fi
    for f in "$root"/secrets/*.txt; do
        [[ -f "$f" ]] && args+=(--from-file="$f")
    done
    (( ${#args[@]} )) || { _mdb_rt_err "secrets/ has no *.txt files; run make init"; return 1; }
    _mdb_k8s_kubectl create secret generic dbpantry-secrets "${args[@]}" --dry-run=client -o yaml \
        | _mdb_k8s_kubectl apply -f - >/dev/null || return
    # CloudNativePG takes the superuser password as a basic-auth Secret, and
    # uses it byte for byte: without the file's trailing newline (as every
    # dbpantry client reads it), or nothing can log in.
    if [[ -f "$root/secrets/pg_superuser_password.txt" ]]; then
        _mdb_k8s_superuser_secret "$root/secrets/pg_superuser_password.txt" || return
    fi
    _mdb_k8s_backup_bundle || return
    for eng in $(_mdb_k8s_engines); do
        d="$root/certs/$eng"
        if [[ -f "$d/server.crt" && -f "$d/server.key" && -f "$root/certs/ca.crt" ]]; then
            _mdb_k8s_kubectl create secret generic "dbpantry-certs-$eng" \
                --from-file=server.crt="$d/server.crt" --from-file=server.key="$d/server.key" \
                --from-file=ca.crt="$root/certs/ca.crt" --dry-run=client -o yaml \
                | _mdb_k8s_kubectl apply -f - >/dev/null || return
        fi
        d="$root/overrides/$eng"
        args=()
        for f in "$d"/*; do
            [[ -f "$f" ]] && args+=(--from-file="$f")
        done
        if (( ${#args[@]} )); then
            _mdb_k8s_kubectl create configmap "dbpantry-overrides-$eng" "${args[@]}" --dry-run=client -o yaml \
                | _mdb_k8s_kubectl apply -f - >/dev/null || return
        fi
    done
}

# What the scheduled backup jobs run: scripts/backup with the libraries it
# sources, engines/, and .env, as the Compose sidecars bind-mount them. A tar
# in a Secret (it carries .env), because a ConfigMap volume cannot hold
# directories.
_mdb_k8s_backup_bundle() {
    local root="$MDB_RT_ROOT" tmp f files=() rc
    [[ -f "$root/scripts/backup" ]] || return 0
    for f in scripts/backup scripts/backend-lib.sh scripts/runtime-lib.sh scripts/env-lib.sh \
             scripts/engine-lib.sh scripts/backends scripts/runtimes engines .env; do
        [[ -e "$root/$f" ]] && files+=("$f")
    done
    tmp="$(mktemp "${TMPDIR:-/tmp}/dbpantry-bundle.XXXXXX")" || return
    ( cd "$root" && COPYFILE_DISABLE=1 tar -cf "$tmp" --no-xattrs --exclude '*.md' "${files[@]}" ) || { rm -f "$tmp"; return 1; }
    # Server-side apply: a client-side apply copies the whole object into the
    # last-applied annotation, and the bundle is past that 256 KiB limit.
    _mdb_k8s_kubectl create secret generic dbpantry-backup-bundle --from-file=bundle.tar="$tmp" \
        --dry-run=client -o yaml \
        | _mdb_k8s_kubectl apply --server-side --force-conflicts --field-manager=dbpantry -f - >/dev/null
    rc=$?
    rm -f "$tmp"
    return "$rc"
}

# Make a locally built image visible to the cluster. kind has its own image
# store; OrbStack and Docker Desktop share the Docker one; a real cluster
# pulls from the registry, so it is up to you to push there.
_mdb_be_kubernetes_image_load() {
    local ref="$1" ctx mode
    ctx="$(_mdb_k8s_ctx)" || return
    mode="$(_mdb_k8s_var MDB_K8S_LOAD_IMAGES)"
    case "${mode:-auto}/$ctx" in
        false/*|auto/orbstack|auto/docker-desktop) return 0 ;;
        auto/kind-*|true/kind-*)
            command -v kind >/dev/null 2>&1 || { _mdb_rt_err "context $ctx is kind, but kind is not on PATH"; return 127; }
            kind load docker-image "$ref" --name "${ctx#kind-}" ;;
        true/*) _mdb_rt_err "MDB_K8S_LOAD_IMAGES=true only knows kind contexts; push $ref to your registry instead"; return 2 ;;
        *) return 0 ;;
    esac
}

# The images the release will run that are built here (MongoDB's is upstream).
_mdb_k8s_local_images() {
    local values="$1"
    awk '
        /^  [a-z]+:$/ { eng = $1; sub(/:/, "", eng); en = 0; ha = 0; tag = "" }
        /^    enabled: true$/ { en = 1 }
        /^      enabled: true$/ { ha = 1 }
        /^    tag: / { tag = $2; gsub(/"/, "", tag) }
        /^    env:$/ && en && tag != "" { print "ghcr.io/simtabi/dbpantry-" eng ":" tag }
        /^      provider: "?patroni"?$/ && en && ha && tag != "" { print "ghcr.io/simtabi/dbpantry-patroni:" tag }
    ' "$values"
}

# One claim per scheduled backup job, created here rather than by the chart:
# on WaitForFirstConsumer storage an unmounted claim is Pending until the
# first job runs, and `helm --wait` would time out on it every time. An
# existing claim is left alone (a claim cannot shrink, and recreating one
# loses the dumps). Same names and labels as the chart's, so `make destroy`
# finds them.
_mdb_k8s_backup_claims() {
    local values="$1" line eng size sc name
    line="$(awk '
        /^global:$/ { sec = "g" } /^engines:$/ { sec = "e" } /^backup:$/ { sec = "b" }
        sec == "g" && /^  storageClass: / { sc = $2; gsub(/"/, "", sc) }
        sec == "e" && /^  [a-z]+:$/ { eng = $1; sub(/:/, "", eng) }
        sec == "e" && /^    enabled: true$/ { on[eng] = 1 }
        sec == "b" && /^  enabled: true$/ { b = 1 }
        sec == "b" && /^  storage: / { size = $2; gsub(/"/, "", size) }
        END {
            if (!b) exit
            printf "%s %s", (size == "" ? "20Gi" : size), (sc == "" ? "-" : sc)
            n = split("pg mysql mariadb", want, " ")
            for (i = 1; i <= n; i++) if (on[want[i]]) printf " %s", want[i]
            printf "\n"
        }' "$values")"
    [[ -n "$line" ]] || return 0
    read -r size sc line <<<"$line"
    for eng in $line; do
        name="$(_mdb_k8s_release)-backups-$eng"
        _mdb_k8s_kubectl get pvc "$name" >/dev/null 2>&1 && continue
        {
            printf 'apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n'
            printf '  name: %s\n  labels:\n' "$name"
            printf '    app.kubernetes.io/name: dbpantry\n'
            printf '    app.kubernetes.io/instance: %s\n' "$(_mdb_k8s_release)"
            printf '    app.kubernetes.io/component: backup-%s\n' "$eng"
            printf 'spec:\n  accessModes: [ReadWriteOnce]\n'
            [[ "$sc" == - ]] || printf '  storageClassName: %s\n' "$sc"
            printf '  resources:\n    requests:\n      storage: %s\n' "$size"
        } | _mdb_k8s_kubectl create -f - >/dev/null || return
    done
}

# Wake a CloudNativePG Cluster that `make down` hibernated. Removing the
# annotation (rather than leaving it to the chart's "off") matters: down set it
# with kubectl annotate, which then owns the field, and helm 4 applies
# server-side, so the install conflicted with "kubectl-annotate" over
# cnpg.io/hibernation. Removing it drops that ownership and wakes the Cluster;
# the chart then sets "off" as its own.
_mdb_k8s_cnpg_wake() {
    local cluster; cluster="$(_mdb_k8s_release)-pg"
    _mdb_k8s_kubectl get clusters.postgresql.cnpg.io "$cluster" >/dev/null 2>&1 || return 0
    _mdb_k8s_kubectl annotate clusters.postgresql.cnpg.io "$cluster" cnpg.io/hibernation- >/dev/null
}

_mdb_be_kubernetes_up() {
    _mdb_k8s_tools || return
    local ctx release ns values extra img chart="$MDB_RT_ROOT/deploy/helm/dbpantry" vf=() v rc
    ctx="$(_mdb_k8s_ctx)" || return
    release="$(_mdb_k8s_release)"; ns="$(_mdb_k8s_ns)"
    values="$(mktemp "${TMPDIR:-/tmp}/dbpantry-values.XXXXXX")" || return
    "$MDB_RT_ROOT/scripts/helm-values" -o "$values" || { rm -f "$values"; return 1; }
    _mdb_be_kubernetes_secrets_sync || { rm -f "$values"; return 1; }
    _mdb_k8s_backup_claims "$values" || { rm -f "$values"; return 1; }
    _mdb_k8s_cnpg_wake || { rm -f "$values"; return 1; }
    while IFS= read -r img; do
        [[ -n "$img" ]] || continue
        _mdb_be_kubernetes_image_load "$img" || { rm -f "$values"; return 1; }
    done < <(_mdb_k8s_local_images "$values")
    extra="$(_mdb_k8s_var MDB_K8S_VALUES)"
    if [[ -n "$extra" ]]; then
        IFS=: read -r -a vf <<<"$extra"
    fi
    local args=(-f "$values")
    for v in ${vf[@]+"${vf[@]}"}; do
        [[ -n "$v" ]] && args+=(-f "$v")
    done
    printf 'dbpantry: deploying release %s to %s/%s\n' "$release" "$ctx" "$ns"
    _mdb_k8s_helm upgrade --install "$release" "$chart" "${args[@]}" \
        --wait --timeout "${MDB_WAIT_TIMEOUT:-300}s" "$@"
    rc=$?
    rm -f "$values"
    (( rc == 0 )) || return "$rc"
    _mdb_k8s_cnpg_wait_ready
}

# helm --wait waits for the workloads it knows (StatefulSets, Deployments), not
# for a custom resource, so with engines.pg.ha.provider=cnpg `make up` used to
# return while the operator was still creating the instances. Wait until every
# instance the Cluster asks for is ready, bounded by MDB_K8S_CNPG_READY_TIMEOUT
# seconds (default MDB_WAIT_TIMEOUT, else 600), and fail with the Cluster's own
# phase when it does not get there. .status.readyInstances against
# .spec.instances works on every CloudNativePG release, unlike the Ready
# condition, which older ones do not set. No Cluster: nothing to wait for.
_mdb_k8s_cnpg_wait_ready() {
    local cluster timeout poll="${_MDB_K8S_POLL_SECONDS:-5}" start state ready want phase
    cluster="$(_mdb_k8s_release)-pg"
    _mdb_k8s_kubectl get clusters.postgresql.cnpg.io "$cluster" >/dev/null 2>&1 || return 0
    timeout="$(_mdb_k8s_var MDB_K8S_CNPG_READY_TIMEOUT)"
    timeout="${timeout:-${MDB_WAIT_TIMEOUT:-600}}"
    [[ "$timeout" =~ ^[0-9]+$ ]] || { _mdb_rt_err "MDB_K8S_CNPG_READY_TIMEOUT must be a number of seconds, not '$timeout'"; return 2; }
    printf 'dbpantry: waiting up to %ss for CloudNativePG cluster %s\n' "$timeout" "$cluster"
    start=$SECONDS
    while :; do
        state="$(_mdb_k8s_kubectl get clusters.postgresql.cnpg.io "$cluster" \
            -o jsonpath='{.status.readyInstances} {.spec.instances} {.status.phase}' 2>/dev/null || true)"
        read -r ready want phase <<<"$state"
        if [[ "$ready" =~ ^[0-9]+$ && "$want" =~ ^[0-9]+$ ]] && (( want > 0 && ready >= want )); then
            printf 'dbpantry: CloudNativePG cluster %s ready (%s/%s instances)\n' "$cluster" "$ready" "$want"
            return 0
        fi
        if (( SECONDS - start >= timeout )); then
            _mdb_rt_err "CloudNativePG cluster $cluster is not ready after ${timeout}s: ${ready:-0}/${want:-?} instances ready, phase '${phase:-unknown}'. Inspect it with: kubectl --context $(_mdb_k8s_ctx) -n $(_mdb_k8s_ns) describe clusters.postgresql.cnpg.io $cluster (raise MDB_K8S_CNPG_READY_TIMEOUT for a slow cluster)"
            return 1
        fi
        sleep "$poll"
    done
}

# PersistentVolumeClaims survive `helm uninstall`; only make destroy deletes them.
_mdb_be_kubernetes_down() {
    _mdb_k8s_tools || return
    local release
    release="$(_mdb_k8s_release)"
    if _mdb_k8s_helm status "$release" >/dev/null 2>&1; then
        # A CloudNativePG Cluster owns its volumes, so deleting it would
        # delete the data. The chart keeps it on uninstall, and down
        # hibernates it instead: the pods stop, the volumes stay, and the next
        # `make up` (hibernation "off" in the chart) starts it again.
        if _mdb_k8s_kubectl get clusters.postgresql.cnpg.io "$release-pg" >/dev/null 2>&1; then
            _mdb_k8s_kubectl annotate clusters.postgresql.cnpg.io "$release-pg" cnpg.io/hibernation=on --overwrite >/dev/null || return
        fi
        _mdb_k8s_helm uninstall "$release" --wait "$@" || return
        # helm returns once the objects are deleted, not once the pods have
        # stopped; a database is down only when its pod is gone.
        _mdb_k8s_kubectl wait --for=delete pod -l "$(_mdb_k8s_sel)" --timeout="${MDB_WAIT_TIMEOUT:-300}s" >/dev/null 2>&1 || true
    else
        printf 'dbpantry: release %s is not installed in %s\n' "$release" "$(_mdb_k8s_ns)"
    fi
}

_mdb_be_kubernetes_status() {
    _mdb_k8s_kubectl get statefulsets,pods,services,pvc -l "$(_mdb_k8s_sel)" "$@"
}

_mdb_be_kubernetes_logs() {
    _mdb_k8s_kubectl logs -l "$(_mdb_k8s_sel)" --prefix --all-containers --max-log-requests=20 "$@"
}

# kubectl port-forward every running engine to its MDB_<ENGINE>_HOST_PORT (or
# its standard port) on 127.0.0.1, until interrupted.
_mdb_be_kubernetes_forward() {
    local svc port hp ENG pids=() pid
    while IFS= read -r svc; do
        [[ -n "$svc" ]] || continue
        port="$(_mdb_k8s_kubectl get service "$svc" -o jsonpath='{.spec.ports[0].port}' 2>/dev/null)"
        [[ -n "$port" ]] || port="$(_mdb_k8s_kubectl get service "$(_mdb_k8s_release)-$svc" -o jsonpath='{.spec.ports[0].port}' 2>/dev/null)"
        [[ -n "$port" ]] || continue
        ENG="$(printf '%s' "$svc" | tr '[:lower:]-' '[:upper:]_')"
        hp="$(_mdb_k8s_var "MDB_${ENG}_HOST_PORT")"
        printf 'dbpantry: %s on 127.0.0.1:%s\n' "$svc" "${hp:-$port}"
        _mdb_k8s_kubectl port-forward --address 127.0.0.1 "pod/$(_mdb_k8s_pod "$svc")" "${hp:-$port}:$port" >/dev/null &
        pids+=("$!")
    done < <(_mdb_be_kubernetes_running)
    (( ${#pids[@]} )) || { _mdb_rt_err "nothing is running to forward"; return 1; }
    trap 'kill "${pids[@]}" 2>/dev/null' INT TERM EXIT
    for pid in "${pids[@]}"; do wait "$pid"; done
}

_mdb_be_kubernetes_info() {
    local ctx
    ctx="$(_mdb_k8s_ctx)" || return
    printf 'backend:   kubernetes (MDB_BACKEND=%s)\n' "$MDB_BACKEND"
    printf 'context:   %s\n' "$ctx"
    printf 'namespace: %s\n' "$(_mdb_k8s_ns)"
    printf 'release:   %s\n' "$(_mdb_k8s_release)"
    if command -v kubectl >/dev/null 2>&1; then
        printf 'kubectl:   %s\n' "$(kubectl version --client 2>/dev/null | head -1)"
    else
        printf 'kubectl:   NOT on PATH\n'
    fi
    if command -v helm >/dev/null 2>&1; then
        printf 'helm:      %s\n' "$(helm version --short 2>/dev/null)"
    else
        printf 'helm:      NOT on PATH\n'
    fi
}

mdb_backend_register kubernetes
