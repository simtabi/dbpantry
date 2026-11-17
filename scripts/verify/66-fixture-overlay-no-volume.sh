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

# The fixture runs beside the stack, so its network needs its own subnet:
# Docker refuses a pool that overlaps the stack's mdb_net, and until
# 2026-11-17 `fixture up` failed for exactly that whenever the stack was up.
# Render with the environment the fixture's own plan sets, and check the
# network's subnet, that it differs from the stack's, and that every pooler's
# fixed address lies inside it.
read -r -a words <<< "${up_line%% scripts/runtime up*}"
fxenv=()
for w in "${words[@]}"; do
    [[ "$w" == *=* && "$w" != COMPOSE_PROFILES=* ]] && fxenv+=("$w")
done
(( ${#fxenv[@]} >= 3 )) || vfail "the fixture plan sets ${#fxenv[@]} variables, expected its project, subnet and ports: $up_line"
fx="$(env "${fxenv[@]}" COMPOSE_PROFILES="$profiles" bash -c '. scripts/runtime-lib.sh && mdb_compose "$@" config --format json' _ "${files[@]}" 2>&1)" \
    || vfail "compose config failed with the fixture's environment: $(printf '%s\n' "$fx" | tail -5)"
stack="$(printf '%s\n' "$without")"
check="$(python3 - "$fx" "$stack" <<'PY'
import ipaddress, json, sys
fx, st = json.loads(sys.argv[1]), json.loads(sys.argv[2])
net = lambda d: ipaddress.ip_network(d["networks"]["mdb"]["ipam"]["config"][0]["subnet"])
a, b = net(fx), net(st)
if a.overlaps(b):
    print(f"FAIL the fixture network {a} overlaps the stack's {b}"); sys.exit()
n = 0
for name, svc in fx["services"].items():
    ip = (svc.get("networks") or {}).get("mdb", {}) or {}
    ip = ip.get("ipv4_address") if isinstance(ip, dict) else None
    if ip:
        n += 1
        if ipaddress.ip_address(ip) not in a:
            print(f"FAIL {name} has {ip}, outside the fixture network {a}"); sys.exit()
print(f"OK {a} {b} {n}")
PY
)"
[[ "$check" == OK* ]] || vfail "${check#FAIL }"
read -r _ fxnet stnet npool <<< "$check"
(( npool >= 1 )) || vfail "no pooler with a fixed address in the fixture render, so the address check inspected nothing"
vinfo "fixture network $fxnet beside the stack's $stnet; $npool pooler address(es) inside it"
