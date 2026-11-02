#!/usr/bin/env bash
# verify: stack operations go through the backend layer, and every backend implements its contract
# tags: fast structure runtime
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# design.md D-62. Kubernetes and Incus have no container named after a
# service, so a script that says `compose exec pg` works on one backend only.
# Stack operations are service verbs (mdb_svc_exec, mdb_be_up, ...) that each
# backend in scripts/backends/ implements; the verify checks, which drive the
# Compose stack on purpose, are the exception and run on compose only.

cd "$MDB_ROOT" || exit 1

# --- 1. no container-level exec against stack services ------------------------
bad=0
while IFS= read -r f; do
    case "$f" in scripts/backends/*|scripts/verify/*|scripts/runtime-lib.sh|scripts/backend-lib.sh) continue ;; esac
    while IFS= read -r hit; do
        printf '      %s: %s\n' "$f" "$hit" >&2
        bad=$(( bad + 1 ))
    done < <(grep -nE '(mdb_compose|\$\(COMPOSE\))[^#]* exec( |$)' "$f" | grep -vE '^[0-9]+:[[:space:]]*#' || true)
    # mdb_ctr exec is fine into a container the script started itself.
    if grep -qE 'mdb_ctr[^#]* exec ' "$f" && ! grep -qE 'mdb_ctr run ' "$f"; then
        printf '      %s: mdb_ctr exec without starting its own container\n' "$f" >&2
        bad=$(( bad + 1 ))
    fi
done < <(grep -lE 'mdb_(compose|ctr)|\$\(COMPOSE\)' scripts/* Makefile 2>/dev/null)
(( bad == 0 )) || vfail "$bad stack exec(s) bypass the backend layer; use mdb_svc_exec (listed above)"
vinfo "scripts and the Makefile reach stack services only through mdb_svc_exec"

# --- 2. the contract, per backend ---------------------------------------------
out="$(bash -c '
    . scripts/backend-lib.sh
    for b in $MDB_BACKENDS; do
        for v in $MDB_BACKEND_REQUIRED_VERBS; do
            declare -F "_mdb_be_${b}_${v}" >/dev/null || echo "missing _mdb_be_${b}_${v}"
        done
    done
    echo "backends: $MDB_BACKENDS"
' 2>&1)"
printf '%s\n' "$out" | grep -q '^missing' && { printf '%s\n' "$out" >&2; vfail "a backend lacks a required verb"; }
vinfo "$(printf '%s\n' "$out" | grep '^backends:') each define the required verbs"

# --- 3. selection -------------------------------------------------------------
t="$(mktemp -d "${TMPDIR:-/tmp}/mdb-be.XXXXXX")"
add_cleanup "rm -rf '$t'"
printf 'MDB_BACKEND="compose"\n' > "$t/.env"
got="$(MDB_RT_ROOT="$t" bash -c 'set -u; . scripts/backend-lib.sh; unset MDB_BACKEND; mdb_backend_init && echo "$MDB_BACKEND"' 2>&1)"
[[ "$got" == compose ]] || vfail "MDB_BACKEND from .env: expected compose, got [$got]"
got="$(MDB_RT_ROOT="$t" MDB_BACKEND=nosuch bash -c '. scripts/backend-lib.sh; mdb_backend_init' 2>&1 || true)"
[[ "$got" == *"is not one of"* ]] || vfail "an unknown MDB_BACKEND was not refused: [$got]"
vinfo "MDB_BACKEND is read from the environment, then .env; unknown names are refused"

# Extra backend directories (MDB_BACKEND_DIRS, colon-separated) load, and the
# library sources cleanly under set -u with the variable unset or set (verify's
# lib.sh runs under set -u; an unbound MDB_BACKEND_DIRS once broke every check).
mkdir -p "$t/be one" "$t/be2"
printf 'mdb_backend_register fake\n_mdb_be_fake_info() { echo "backend: fake"; }\n' > "$t/be2/fake.sh"
got="$(MDB_RT_ROOT="$t" MDB_BACKEND=fake MDB_BACKEND_DIRS="$t/be one:$t/be2" bash -c 'set -u; . scripts/backend-lib.sh; mdb_be_info' 2>&1)"
[[ "$got" == "backend: fake" ]] || vfail "MDB_BACKEND_DIRS backend did not load: [$got]"
vinfo "MDB_BACKEND_DIRS loads extra backends; the library is set -u clean"

# --- 4. compose backend: verbs become the same Compose commands as before ------
got="$(bash -c '
    . scripts/backend-lib.sh
    mdb_compose() { printf "compose %s\n" "$*"; }
    MDB_BACKEND=compose
    mdb_svc_exec pg psql -c "select 1"
    mdb_svc_exec -u postgres pg pgbackrest info
    mdb_be_down
' 2>&1)"
want='compose exec -T pg psql -c select 1
compose exec -T -u postgres pg pgbackrest info
compose --profile * down --remove-orphans'
[[ "$got" == "$want" ]] || vfail "compose backend verbs changed: got [$got]"
got="$(bash -c '. scripts/backend-lib.sh; MDB_BACKEND=compose; _mdb_be_call nosuchverb' 2>&1 || true)"
[[ "$got" == *"does not support"* ]] || vfail "an unsupported verb did not fail by name: [$got]"
vinfo "compose verbs map to the Compose commands scripts used before; unsupported verbs fail by name"
