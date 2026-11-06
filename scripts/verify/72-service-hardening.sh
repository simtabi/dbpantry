#!/usr/bin/env bash
# verify: every service drops privileges, has a health signal, is limited under prod, and takes secrets as files; exceptions carry a reason and must stay true
# tags: structure security prod
# phase: 6

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# design.md D-75. Measured 2026-11-06 over the broadest render (every engine,
# every pooler, ui, backup, metrics, ha, prod): etcd and HAProxy kept every
# capability, eighteen of twenty-five services had no limit under prod, five
# had no health signal, and pg-exporter connected with sslmode=disable to a
# server that is hostssl-only under prod. Nothing failed, because nothing
# looked. This looks, without a daemon:
#
#   1. no-new-privileges and cap_drop ALL on every service;
#   2. a health signal: a Compose healthcheck, or a HEALTHCHECK in the image's
#      own Dockerfile when the image is one this repository builds;
#   3. a resource limit on every service under the prod overlay;
#   4. secrets as files: an environment key that names a credential holds a
#      /run/secrets path or ends in _FILE.
#
# The exceptions below are each a ceiling, not a licence: an exception that is
# no longer needed fails the check, so it cannot quietly outlive its reason.

mdb_runtime_init >/dev/null 2>&1 || vfail "no container runtime CLI with Compose is installed"
cd "$MDB_ROOT" || vfail "no repository root"
command -v python3 >/dev/null 2>&1 || vfail "python3 is required to read the rendered JSON"

s="$(mktemp -d "${TMPDIR:-/tmp}/mdb-harden.XXXXXX")"; add_cleanup "rm -rf '$s'"
rsync -a --exclude /.git --exclude /backups --exclude /secrets --exclude /certs \
    --exclude /build --exclude /.env --exclude /.env.prod "$MDB_ROOT/" "$s/" \
    || vfail "could not copy the checkout"
cp "$MDB_ROOT/.env.example" "$s/.env"
( cd "$s" && MDB_ENV_FILE="$s/.env" scripts/gen-compose >/dev/null ) || vfail "gen-compose failed"

profiles=(pg mysql mariadb mongodb cassandra pg-pooler mysql-pooler mariadb-pooler ui backup metrics ha ha-pooler prod)
files=(docker-compose.yml compose.engines.yml compose.proxy.yml compose.ha.yml compose.prod.yml compose.prod.engines.yml)
args=()
for f in "${files[@]}"; do args+=(-f "$s/$f"); done
for p in "${profiles[@]}"; do args+=(--profile "$p"); done
( cd "$s" && unset COMPOSE_FILE COMPOSE_PROFILES && export COMPOSE_PROJECT_NAME=mdbharden \
    && mdb_compose --project-directory "$s" --env-file "$s/.env" "${args[@]}" config --format json \
    > "$s/all.json" 2> "$s/all.err" ) \
    || { sed 's/^/      /' "$s/all.err" >&2; vfail "the full render failed"; }

# Images this repository builds and gives a HEALTHCHECK of their own.
built_health=""
for df in images/*/Dockerfile; do
    grep -q '^HEALTHCHECK' "$df" && built_health+="$(basename "$(dirname "$df")") "
done

out="$(python3 - "$s/all.json" "$built_health" <<'PY'
import json, re, sys
svc = json.load(open(sys.argv[1]))["services"]
built = set(sys.argv[2].split())

# rule -> {service: reason}. Each is a measured gap with its reason; the check
# fails if one stops being a gap.
EXEMPT = {
    "privileges": {
        "pgadmin": "the image binds port 80 as an unprivileged user through a file "
                   "capability on its python, which no-new-privileges disables; moving "
                   "it to an unprivileged port needs the Caddy routes changed with it",
    },
    "health": {
        "adminer":        "upstream image; no client in it that a probe could use, unverified",
        "phpmyadmin":     "upstream image; a probe is unverified without a daemon to try it",
        "pgadmin":        "upstream image; a probe is unverified without a daemon to try it",
        "caddy":          "prod disables the admin API and redirects :80 to TLS, so a probe needs design",
        "mysql-pooler":   "ProxySQL upstream image; a probe through its admin port is unverified",
        "mariadb-pooler": "ProxySQL upstream image; a probe through its admin port is unverified",
    },
    "secrets": {
        "etcd1:ETCD_INITIAL_CLUSTER_TOKEN": "a cluster identifier, not a credential",
        "etcd2:ETCD_INITIAL_CLUSTER_TOKEN": "a cluster identifier, not a credential",
        "etcd3:ETCD_INITIAL_CLUSTER_TOKEN": "a cluster identifier, not a credential",
        "mysql:MDB_MYSQL_NATIVE_PASSWORD_COMPAT": "a boolean switch, not a credential",
    },
}
bad, used = [], set()

def gap(rule, key, msg):
    if key in EXEMPT[rule]:
        used.add((rule, key))
    else:
        bad.append(msg)

for name, d in sorted(svc.items()):
    priv_ok = "no-new-privileges:true" in (d.get("security_opt") or []) and "ALL" in (d.get("cap_drop") or [])
    if not priv_ok:
        gap("privileges", name, f"{name}: missing no-new-privileges or cap_drop ALL")
    hc = d.get("healthcheck") or {}
    image = str(d.get("image", ""))
    m = re.match(r"ghcr\.io/simtabi/dbpantry-([a-z0-9-]+):", image)
    health_ok = (hc and not hc.get("disable")) or (m and m.group(1) in built)
    if not health_ok:
        gap("health", name, f"{name}: no healthcheck in Compose or in its image")
    lim = ((d.get("deploy") or {}).get("resources") or {}).get("limits") or {}
    if not (lim.get("memory") and lim.get("cpus")):
        bad.append(f"{name}: no cpu and memory limit under prod")
    for k, v in (d.get("environment") or {}).items():
        if re.search(r"PASS|SECRET|TOKEN|PRIVATE_KEY", k) and v and not k.endswith("_FILE") \
                and not str(v).startswith("/run/secrets/"):
            gap("secrets", f"{name}:{k}", f"{name}: {k} holds a value, not a /run/secrets path")

for rule, entries in EXEMPT.items():
    for key, reason in entries.items():
        if (rule, key) not in used:
            bad.append(f"stale exception: {rule} for {key} no longer applies ({reason}); remove it")
print(f"#{len(svc)} {sum(len(v) for v in EXEMPT.values())}")
print("\n".join(bad))
PY
)" || vfail "could not judge the render"

head="$(head -1 <<< "$out")"; rest="$(tail -n +2 <<< "$out")"
read -r services exceptions <<< "${head#\#}"
(( services >= 25 )) || vfail "the full render has $services service(s), expected at least 25; the profiles or files are wrong"
[[ -z "$rest" ]] || { printf '      %s\n' "$rest" >&2; vfail "service hardening has gaps (see above)"; }
vinfo "$services services: privileges dropped, a health signal, limits under prod, secrets as files ($exceptions recorded exception(s), each still needed)"
