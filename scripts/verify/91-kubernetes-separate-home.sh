#!/usr/bin/env bash
# verify: with MDB_HOME apart from the kit, the Kubernetes backend deploys, provisions and backs up from the home, and the kit is unchanged (needs a local cluster)
# tags: kubernetes stack pg
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Check 77 runs the separate-home path (design.md D-84) on Compose. This runs
# it on the Kubernetes backend (D-64), which reads the home differently: it
# renders the home's .env to Helm values, publishes the home's secrets/ and
# certs/ as Secrets, and takes the chart and overrides/ from the kit. Until
# this check, no cluster had run it with a separate home
# (docs/reference/follow-ups.md).
#
# Only with MDB_VERIFY_K8S_CONTEXT naming a local cluster (kind-*, orbstack,
# docker-desktop, rancher-desktop, minikube): it deploys into its own
# mdbscratch* namespace and deletes that namespace afterwards. Helm options
# the cluster needs (HELM_KUBEAPISERVER for OrbStack, docs/guides/kubernetes.md)
# come from the environment, as for any make target.

ctx="${MDB_VERIFY_K8S_CONTEXT:-}"
[[ -n "$ctx" ]] || vskip "needs a local Kubernetes cluster: set MDB_VERIFY_K8S_CONTEXT (kind-<name>, orbstack, docker-desktop, rancher-desktop or minikube)"
case "$ctx" in
    kind-*|orbstack|docker-desktop|rancher-desktop|minikube) ;;
    *) vfail "MDB_VERIFY_K8S_CONTEXT must name a local cluster (this check deploys and deletes a namespace): $ctx" ;;
esac
for t in kubectl helm; do
    command -v "$t" >/dev/null 2>&1 || vfail "$t is required"
done
kubectl --context "$ctx" get --raw /version >/dev/null 2>&1 || vfail "the cluster of context $ctx does not answer"
need_runtime
need_dbpantry
need_image "$(image_name pg)"

SCRATCH_SEPARATE_HOME=1 scratch_stack "pg"
[[ "$SCRATCH_HOME" != "$SCRATCH_DIR" && -s "$SCRATCH_HOME/.verify-kit-manifest" ]] \
    || vfail "scratch_stack did not give the scratch stack a separate home"
require_test_name "$SCRATCH_PROJECT" namespace
# Registered after scratch_stack, so it runs after the scratch `make down`.
add_cleanup "kubectl --context '$ctx' delete namespace '$SCRATCH_PROJECT' --ignore-not-found --wait=false >/dev/null 2>&1 || true"
printf '\n# check 91: the Kubernetes backend\nMDB_BACKEND=kubernetes\nMDB_K8S_CONTEXT=%s\n' "$ctx" >> "$SCRATCH_HOME/.env"
k() { kubectl --context "$ctx" --namespace "$SCRATCH_PROJECT" "$@"; }

# 1. Deploy through the binary, from the home.
out="$(scratch_run "$DBPANTRY_BIN" up --plain 2>&1)" \
    || vfail "dbpantry up failed on Kubernetes with a separate home:
$(printf '%s\n' "$out" | tail -25 | sed 's/^/       /')"
wait_for 300 "pg to report healthy" scratch_healthy pg
[[ "$(scratch_exec pg psql -U postgres -tAc 'SELECT 1' 2>/dev/null | tr -d ' \r')" == 1 ]] \
    || vfail "pg is healthy but does not answer a query"
vinfo "deployed release $SCRATCH_PROJECT to $ctx from the home: pg healthy"

# 2. The cluster holds the home's secrets, and pg takes the home's password
# over TCP (the socket would trust the role). The password goes in on stdin.
pw="$(tr -d '\n' < "$SCRATCH_HOME/secrets/pg_superuser_password.txt")"
[[ -n "$pw" ]] || vfail "the home has no superuser password"
published="$(k get secret dbpantry-secrets -o jsonpath='{.data.pg_superuser_password\.txt}' | base64 -d 2>/dev/null | tr -d '\n')"
[[ "$published" == "$pw" ]] || vfail "the dbpantry-secrets Secret does not hold the home's superuser password"
# shellcheck disable=SC2016  # expanded in the pod
got="$(printf '%s\n' "$pw" | scratch_exec pg sh -c 'read -r PGPASSWORD; export PGPASSWORD; psql -h 127.0.0.1 -U postgres -tAc "SELECT 1"' 2>/dev/null | tr -d ' \r')"
[[ "$got" == 1 ]] || vfail "pg does not accept the superuser password from the home's secrets/"
vinfo "the Secret and pg both hold the home's superuser password"

# 3. A project and a backup land in the home.
proj="homecheck$$"
out="$(scratch_run "$DBPANTRY_BIN" project new --name "$proj" --engine pg --plain 2>&1)" \
    || vfail "dbpantry project new failed: $(printf '%s\n' "$out" | tail -10)"
[[ -s "$SCRATCH_HOME/secrets/pg_${proj}_user_password.txt" ]] \
    || vfail "new-project did not write the project's password into the home"
out="$(scratch_run "$DBPANTRY_BIN" backup run --engine pg --db "$proj" --plain 2>&1)" \
    || vfail "dbpantry backup run failed: $(printf '%s\n' "$out" | tail -10)"
dumps="$(find "$SCRATCH_HOME/backups" -maxdepth 1 -type f -name "pg_${proj}_*" ! -name '*.sha256' | grep -c . || true)"
(( dumps >= 1 )) || vfail "the backup wrote no dump into the home's dump directory"
vinfo "project secrets and the dump are in the home"

out="$(scratch_run "$DBPANTRY_BIN" down --plain 2>&1)" || vfail "dbpantry down failed: $(printf '%s\n' "$out" | tail -10)"

# 4. The kit is exactly as it was: no file added, removed or changed.
after="$(scratch_kit_manifest)"
n="$(grep -c . "$SCRATCH_HOME/.verify-kit-manifest")"
(( n > 100 )) || vfail "the kit manifest lists only $n entries; it inspected nothing"
if ! diff_out="$(diff "$SCRATCH_HOME/.verify-kit-manifest" <(printf '%s\n' "$after"))"; then
    printf '%s\n' "$diff_out" | head -30 | sed 's/^/      /' >&2
    vfail "the kit changed while the instance lived in a separate home (listed above)"
fi
vinfo "the kit is unchanged: $n paths compared"
