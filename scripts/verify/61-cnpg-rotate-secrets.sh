#!/usr/bin/env bash
# verify: rotate-secrets republishes secrets to the backend, so a running CloudNativePG cluster takes the new superuser password (live half needs a kind cluster)
# tags: security kubernetes
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# A CloudNativePG operator keeps the postgres password equal to its basic-auth
# Secret (<release>-pg-superuser). rotate-secrets changed the password in the
# database and in secrets/ but never republished the Secret, so the operator
# was free to put the OLD password back: the file and the database then
# disagree, which is the lock-out rotate-secrets is written to prevent.
#
#   1. Always: against a stub backend, rotate-secrets publishes secrets/ right
#      after the superuser file changes (with the new password in it) and once
#      more at the end, and a failed publish fails the rotation loudly.
#   2. Only with MDB_VERIFY_KIND_CONTEXT=kind-<name>, a kind cluster with the
#      CloudNativePG operator installed (docs/guides/kubernetes.md): a scratch
#      checkout deploys a CNPG cluster into its own mdbscratch* namespace,
#      `make up` returns only once it is ready, rotate-secrets runs, and the
#      Secret and a login agree on the new password while the old one is
#      refused. The namespace is deleted afterwards.

tmp="$(mktemp -d "${TMPDIR:-/tmp}/mdb-cnpgrot.XXXXXX")"
add_cleanup "rm -rf '$tmp'"

# --- 1. publish after rotation (stub backend) ----------------------------------
root="$tmp/root"
mkdir -p "$root/secrets" "$tmp/be"
cp -R "$MDB_ROOT/scripts" "$root/"
old_pw="$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')"
printf '%s\n' "$old_pw" > "$root/secrets/pg_superuser_password.txt"
cat > "$tmp/be/fake.sh" <<'BE'
# shellcheck shell=bash
_mdb_be_fake_up()      { :; }
_mdb_be_fake_down()    { :; }
_mdb_be_fake_running() { printf 'pg\n'; }
_mdb_be_fake_exec()    { cat >/dev/null; }
_mdb_be_fake_health()  { printf 'healthy\n'; }
_mdb_be_fake_volumes() { :; }
_mdb_be_fake_secrets_sync() {
    printf 'sync %s\n' "$(tr -d '\n' < "$MDB_RT_ROOT/secrets/pg_superuser_password.txt")" >> "$FAKE_LOG"
    [[ -z "${FAKE_SYNC_FAIL:-}" ]]
}
mdb_backend_register fake
BE
log="$tmp/sync.log"
rotate() {
    : > "$log"
    ( cd "$root" && env -i PATH="$PATH" HOME="$HOME" TMPDIR="${TMPDIR:-/tmp}" FAKE_LOG="$log" \
        MDB_BACKEND=fake MDB_BACKEND_DIRS="$tmp/be" MDB_RT_ROOT="$root" "$@" scripts/rotate-secrets --yes ) 2>&1
}
rc=0; out="$(rotate)" || rc=$?
(( rc == 0 )) || vfail "rotate-secrets failed against the stub backend: $(tail -3 <<<"$out")"
new_pw="$(tr -d '\n' < "$root/secrets/pg_superuser_password.txt")"
[[ -n "$new_pw" && "$new_pw" != "$old_pw" ]] || vfail "the stubbed rotation did not change the superuser file"
[[ "$(grep -c . "$log")" -ge 2 ]] || vfail "rotate-secrets published secrets $(grep -c . "$log") time(s); expected after the superuser and at the end"
[[ "$(head -1 "$log")" == "sync $new_pw" ]] || vfail "the first publish did not carry the new superuser password"
grep -qxF "sync $old_pw" "$log" && vfail "rotate-secrets published the OLD superuser password"
rc=0; out="$(rotate FAKE_SYNC_FAIL=1)" || rc=$?
(( rc != 0 )) || vfail "rotate-secrets exited 0 although publishing the new secrets failed"
[[ "$out" == *"publishing"*"failed"* ]] || vfail "a failed publish is not reported: $(tail -2 <<<"$out")"
vinfo "rotate-secrets publishes secrets/ with the new superuser password, and fails loudly when it cannot"

# --- 2. a running CloudNativePG cluster on kind ---------------------------------
ctx="${MDB_VERIFY_KIND_CONTEXT:-}"
[[ -n "$ctx" ]] || vskip "the live CloudNativePG rotation needs a kind cluster with the operator: set MDB_VERIFY_KIND_CONTEXT=kind-<name> (stub half passed)"
[[ "$ctx" == kind-* ]] || vfail "MDB_VERIFY_KIND_CONTEXT must be a kind-* context (this check deploys and deletes a namespace): $ctx"
for t in kubectl helm; do
    command -v "$t" >/dev/null 2>&1 || vfail "$t is required for the live half"
done
kubectl --context "$ctx" get crd clusters.postgresql.cnpg.io >/dev/null 2>&1 \
    || vfail "the CloudNativePG operator is not installed in $ctx (see docs/guides/kubernetes.md)"

scratch_stack "pg"
require_test_name "$SCRATCH_PROJECT" namespace
# Registered after scratch_stack, so it runs after the scratch `make down`.
add_cleanup "kubectl --context '$ctx' delete namespace '$SCRATCH_PROJECT' --ignore-not-found --wait=false"
{
    printf 'MDB_BACKEND=kubernetes\nMDB_K8S_CONTEXT=%s\nMDB_PROFILES="pg,ha"\n' "$ctx"
    printf 'MDB_K8S_HA_PROVIDER=cnpg\nMDB_K8S_HA_REPLICAS=2\nMDB_K8S_CNPG_READY_TIMEOUT=600\n'
} >> "$SCRATCH_DIR/.env"
scratch_up || vfail "make up (kubernetes, cnpg) failed in the scratch checkout"
cluster="$SCRATCH_PROJECT-pg"
k() { kubectl --context "$ctx" --namespace "$SCRATCH_PROJECT" "$@"; }
st="$(k get clusters.postgresql.cnpg.io "$cluster" -o jsonpath='{.status.readyInstances}/{.spec.instances}')"
[[ "${st%/*}" == "${st#*/}" && "${st%/*}" -ge 1 ]] || vfail "make up returned before the CloudNativePG cluster was ready ($st)"
vinfo "make up returned with the CloudNativePG cluster ready ($st)"

old_pw="$(tr -d '\n' < "$SCRATCH_DIR/secrets/pg_superuser_password.txt")"
scratch_run scripts/rotate-secrets --yes >/dev/null 2>&1 || vfail "rotate-secrets failed on the CloudNativePG cluster"
new_pw="$(tr -d '\n' < "$SCRATCH_DIR/secrets/pg_superuser_password.txt")"
[[ "$new_pw" != "$old_pw" ]] || vfail "rotate-secrets did not change the superuser file"
secret_pw="$(k get secret "$cluster-superuser" -o jsonpath='{.data.password}' | base64 -d 2>/dev/null)"
[[ "$secret_pw" == "$new_pw" ]] || vfail "the $cluster-superuser Secret does not hold the rotated password"

login() { scratch_exec pg env PGPASSWORD="$1" psql -h 127.0.0.1 -U postgres -d postgres -tAc 'SELECT 1' 2>/dev/null | tr -d ' \r' | grep -qx 1; }
# The operator reconciles on its own schedule; give it time, then insist it
# stays on the new password (it must not put the old one back).
wait_ready 180 "the new superuser password to work on the CloudNativePG primary" login "$new_pw"
sleep 30
login "$new_pw" || vfail "the new superuser password stopped working: the operator reverted it"
login "$old_pw" && vfail "the old superuser password still works after rotation"
vinfo "the CloudNativePG Secret, the file and the database agree on the new password; the old one is refused"
