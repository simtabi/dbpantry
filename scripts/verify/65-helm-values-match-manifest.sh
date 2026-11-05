#!/usr/bin/env bash
# verify: the Helm chart has a block for every implemented engine and restates every descriptor fact (image, tag, port, paths, password file) exactly as the manifest does
# tags: fast structure engines kubernetes
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# Refactor plan F1 and F3, design.md D-68. deploy/helm/dbpantry/values.yaml
# restates descriptor facts (ports, data paths, image tags, the root
# password file) and nothing compared them, so a descriptor change reached
# Compose and silently not Kubernetes. `dbpantry manifest check-helm` reads
# the chart with a YAML parser (F2: never awk) and compares each fact with
# the manifest; `dbpantry manifest -o helm` prints what the chart must say.

need_dbpantry

values="deploy/helm/dbpantry/values.yaml"
need_file "$MDB_ROOT/$values"

out="$("$DBPANTRY_BIN" --root "$MDB_ROOT" manifest check-helm "$values" 2>&1)" \
    || vfail "$out
       Fix the chart to match engines/, or see the facts with: make manifest OUTPUT=helm"
vinfo "$out"

# The comparison can fail: a chart that drifts on one fact is refused.
work="$(mktemp -d "${TMPDIR:-/tmp}/mdb-verify-helm.XXXXXX")" || vfail "mktemp failed"
add_cleanup "rm -rf '$work'"
sed 's/^    port: 5432$/    port: 5433/' "$values" > "$work/values.yaml"
cmp -s "$values" "$work/values.yaml" && vfail "the self-test edit did not apply; engines.pg.port is no longer 5432 in $values"
if "$DBPANTRY_BIN" --root "$MDB_ROOT" manifest check-helm "$work/values.yaml" >/dev/null 2>&1; then
    vfail "check-helm accepted a chart whose pg port disagrees with the descriptor"
fi
vinfo "self-test: a drifted port is refused"
