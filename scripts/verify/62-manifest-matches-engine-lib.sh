#!/usr/bin/env bash
# verify: the dbpantry manifest says exactly what engine-lib.sh loads, for every key of every engine, and generates the same CI matrix
# tags: fast structure engines
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# One space between words, none at the ends.
words() { printf '%s' "$1" | tr -s '[:space:]' ' ' | sed 's/^ //; s/ $//'; }

cd "$MDB_ROOT" || exit 1

# design.md D-68: engines/*/engine.conf stays the authored source, the bash
# scripts read it by sourcing it, and the Go binary reads it with its own
# parser of the same strict subset. Two readers of one file drift unless
# something compares them, so this compares them: every key and value bash
# sees after engine_load, against `dbpantry manifest -o tsv`.

need_dbpantry

work="$(mktemp -d "${TMPDIR:-/tmp}/mdb-verify-manifest.XXXXXX")" || vfail "mktemp failed"
add_cleanup "rm -rf '$work'"

engines="$(engine_list)"
[[ -n "$engines" ]] || vfail "engine_list found no descriptors"

# What bash sees. engine_load clears every MDB_ENGINE_* first, so what is set
# afterwards is exactly the descriptor, plus the two paths it adds itself.
for e in $engines; do
    engine_load "$e" || vfail "engine_load $e failed"
    for k in $(compgen -A variable MDB_ENGINE_ | sort); do
        case "$k" in
            MDB_ENGINE_CONF|MDB_ENGINE_DIR|MDB_ENGINE_REQUIRED_KEYS) continue ;;
        esac
        printf '%s\t%s\t%s\n' "$e" "$k" "${!k}"
    done
done | LC_ALL=C sort > "$work/bash.tsv"

"$DBPANTRY_BIN" --root "$MDB_ROOT" manifest -o tsv | LC_ALL=C sort > "$work/go.tsv" \
    || vfail "dbpantry manifest failed: $("$DBPANTRY_BIN" --root "$MDB_ROOT" manifest -o tsv 2>&1 >/dev/null | head -5)"

# Assert the size of what was compared, so an empty pair cannot pass.
rows="$(wc -l < "$work/bash.tsv" | tr -d ' ')"
n_engines="$(printf '%s\n' "$engines" | wc -l | tr -d ' ')"
(( rows >= n_engines * 19 )) \
    || vfail "bash produced only $rows descriptor rows for $n_engines engines; fewer than the 19 required keys each"

if ! diff -u "$work/bash.tsv" "$work/go.tsv" > "$work/diff"; then
    vfail "the manifest disagrees with engine-lib.sh (- bash, + dbpantry):
$(sed 's/^/       /' "$work/diff" | head -40)"
fi
vinfo "$rows descriptor values across $n_engines engines: bash and the manifest agree on every one"

# The engine list and what is implemented, buildable and pooled.
go_names="$("$DBPANTRY_BIN" --root "$MDB_ROOT" manifest -o engines | cut -f1)"
[[ "$go_names" == "$engines" ]] \
    || vfail "engine lists differ: engine_list says '$(words "$engines")', the manifest '$(words "$go_names")'"
while IFS=$'\t' read -r name impl build pooler; do
    engine_load "$name" >/dev/null 2>&1 || vfail "engine_load $name failed"
    want_impl=false; engine_implemented && want_impl=true
    want_build=false
    if [[ "${MDB_ENGINE_PUBLISH:-derive}" == derive ]] && engine_implemented; then want_build=true; fi
    [[ "$impl" == "$want_impl" ]] || vfail "$name: manifest implemented=$impl, engine_implemented says $want_impl"
    [[ "$build" == "$want_build" ]] || vfail "$name: manifest buildable=$build, ci-matrix's rule says $want_build"
    want_pooler=-
    if [[ "${MDB_ENGINE_POOLING:-}" == external && -n "${MDB_ENGINE_POOLER_IMAGE:-}" && -f "$MDB_ENGINE_DIR/compose-pooler.yml" ]]; then
        want_pooler="$name-pooler"
    fi
    [[ "$pooler" == "$want_pooler" ]] || vfail "$name: manifest pooler=$pooler, gen-compose would generate '$want_pooler'"
done < <("$DBPANTRY_BIN" --root "$MDB_ROOT" manifest -o engines)
vinfo "implemented, buildable and pooler agree with engine_implemented, ci-matrix and gen-compose"

# The CI matrix is generated from the manifest (F12); scripts/ci-matrix stays
# as the oracle and the fallback, so the two must be byte-identical.
for mode in versions engines; do
    want="$(scripts/ci-matrix "$mode")" || vfail "scripts/ci-matrix $mode failed"
    got="$("$DBPANTRY_BIN" --root "$MDB_ROOT" manifest --matrix "$mode")" || vfail "dbpantry manifest --matrix $mode failed"
    [[ "$got" == "$want" ]] || vfail "the $mode matrix differs:
       ci-matrix: $want
       dbpantry:  $got"
done
vinfo "the CI matrix from the manifest equals scripts/ci-matrix, in both modes"

# The paradigm vocabulary (N11) is written twice: the manifest's list and
# check 31's case pattern. They must be the same eight words.
vocab="$(words "$("$DBPANTRY_BIN" --root "$MDB_ROOT" manifest | awk '/"paradigms": \[/{on=1; next} on && /\]/{exit} on {gsub(/[",[:space:]]/, ""); print}')")"
check31="$(words "$(grep -oE '^[[:space:]]+relational\|[a-z|-]+\)' scripts/verify/31-engine-descriptors.sh | tr -d ' )' | tr '|' ' ')")"
[[ "$vocab" == "$check31" ]] \
    || vfail "paradigm vocabulary differs: manifest '$vocab', check 31 '$check31'"
n_words="$(printf '%s\n' "$vocab" | wc -w | tr -d ' ')"
[[ "$n_words" == 8 ]] || vfail "the vocabulary has $n_words words, not 8"
vinfo "paradigm vocabulary: $vocab"
