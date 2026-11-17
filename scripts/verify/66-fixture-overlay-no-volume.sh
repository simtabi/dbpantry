#!/usr/bin/env bash
# verify: with the files dbpantry fixture up uses, no implemented engine or pooler mounts a volume, so a fixture is disposable by construction
# tags: fast structure fixture engines
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# The static half of check 64, which needs a running engine. Compose resolves
# the project without a daemon (`config`), so this proves on any machine
# that the test overlay (compose.test.yml) covers every engine the fixture can
# boot: an engine added without an overlay entry keeps its named volume, and
# every fixture run would leave one behind (design.md D-68, G4).

need_dbpantry
mdb_runtime_init >/dev/null 2>&1 \
    || vfail "no container CLI with Compose found; \`compose config\` needs one (no daemon)"
scripts/gen-compose >/dev/null 2>&1 || vfail "scripts/gen-compose failed"

# Every implemented engine and its pooler: everything a fixture may boot.
profiles=""
n=0
while IFS=$'\t' read -r name impl _build pooler; do
    [[ "$impl" == true ]] || continue
    profiles="${profiles:+$profiles,}$name"
    [[ "$pooler" == - ]] || profiles="$profiles,$pooler"
    n=$(( n + 1 ))
done < <("$DBPANTRY_BIN" --root "$MDB_ROOT" manifest -o engines)
(( n >= 5 )) || vfail "only $n implemented engines in the manifest"

# The fixture's own command line names these files; read them from it rather
# than restating them, so the two cannot drift.
plan="$("$DBPANTRY_BIN" --root "$MDB_ROOT" fixture up --profiles "$profiles" --dry-run)" \
    || vfail "dbpantry fixture up --dry-run refused profiles $profiles"
up_line="$(printf '%s\n' "$plan" | grep ' scripts/runtime up ')" || vfail "the fixture plan has no runtime up step:
$plan"
files=()
read -r -a words <<<"${up_line#* scripts/runtime up }"
for (( i = 0; i < ${#words[@]}; i++ )); do
    [[ "${words[$i]}" == -f ]] && files+=(-f "${words[$((i + 1))]}")
done
(( ${#files[@]} >= 6 )) || vfail "the fixture names ${#files[@]} compose arguments, expected -f for three files: $up_line"
printf '%s\n' "${files[@]}" | grep -qx compose.test.yml || vfail "the fixture does not use compose.test.yml: $up_line"

render() { COMPOSE_PROFILES="$profiles" mdb_compose "$@" config --format json 2>&1; }

with="$(render "${files[@]}")" || vfail "compose config failed with the fixture's files: $(printf '%s\n' "$with" | tail -5)"
services="$(printf '%s\n' "$with" | grep -cE '^    "[a-z0-9-]+": \{$' || true)"
(( services >= n )) || vfail "the rendered fixture has $services services, fewer than the $n engines selected"

# Positive control: without the overlay the same render shows volumes, so the
# match below can see one when it is there.
without="$(render -f docker-compose.yml -f compose.engines.yml)" || vfail "compose config failed without the overlay"
printf '%s\n' "$without" | grep -q '"type": "volume"' \
    || vfail "self-test: no volume mount found even without the overlay; the match is broken"

if printf '%s\n' "$with" | grep -q '"type": "volume"'; then
    vfail "a fixture service still mounts a volume; give it a tmpfs and volumes: !override in compose.test.yml:
$(printf '%s\n' "$with" | grep -B12 '"type": "volume"' | grep -E '"(source|target)"' | sed 's/^ */       /' | head -10)"
fi
vinfo "profiles $profiles: $services services, no volume mount (and the same render without the overlay has them)"
