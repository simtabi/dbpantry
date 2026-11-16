#!/usr/bin/env bash
# verify: every supported profile combination renders, runs one authenticating pooler per upstream, the prod overlay publishes only poolers and the front door, backup sidecars mount MDB_BACKUP_DIR, and design.md names every service and image
# tags: structure prod pooling
# phase: 6

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# design.md D-72, D-73. Rendering, not booting: `compose config` needs the
# Compose CLI and no daemon, so this runs anywhere the harness does and in
# seconds. It exists because two defects lived in compose files that nothing
# rendered in the combination that mattered:
#
#   - the prod overlay's own pgbouncer: no credential, aimed at `pg`, a second
#     pooler beside pg-pooler, restarting forever wherever the overlay applied;
#   - the overlay itself, which COMPOSE_FILE never named, so prod published the
#     engines' ports and enforced nothing it claimed to.
#
# For every combination below, rendered the way `make up` assembles it (the
# profiles through scripts/profiles, the files through COMPOSE_FILE):
#   1. it renders, and every service has an image or a build;
#   2. at most one pgBouncer per upstream, and every one has its credential
#      file, the bootstrap entrypoint and its tmpfs -- a pooler that would exit
#      on start is refused here, not discovered in production;
#   3. under prod: nothing but the front door and poolers publishes a port
#      (unless MDB_PUBLISH_ENGINES=true), and every engine and pooler has
#      MDB_TLS_ENFORCE=true;
#   4. the HA pooler's environment matches the generated pg-pooler's, so the
#      two hand-kept copies cannot drift.

# The Compose CLI, not a running engine: `config` never contacts the daemon.
mdb_runtime_init >/dev/null 2>&1 || vfail "no container runtime CLI with Compose is installed"
cd "$MDB_ROOT" || vfail "no repository root"
command -v python3 >/dev/null 2>&1 || vfail "python3 is required to read the rendered JSON"

# A scratch copy: gen-compose writes compose.engines.yml and friends into the
# tree it runs from, and each case needs its own MDB_PUBLISH.
s="$(mktemp -d "${TMPDIR:-/tmp}/mdb-render.XXXXXX")"; add_cleanup "rm -rf '$s'"
rsync -a --exclude /.git --exclude /backups --exclude /secrets --exclude /certs \
    --exclude /build --exclude /.env --exclude /.env.prod "$MDB_ROOT/" "$s/" \
    || vfail "could not copy the checkout"
mkdir -p "$s/secrets" "$s/certs"

prod_files="docker-compose.yml:compose.engines.yml:compose.proxy.yml:compose.ha.yml:compose.prod.yml:compose.prod.engines.yml"
base_files="docker-compose.yml:compose.engines.yml:compose.proxy.yml:compose.ha.yml"

# render NAME PROFILES PUBLISH PUBLISH_ENGINES FILES -> $s/out/NAME.json
render() {
    local name="$1" profiles="$2" publish="$3" pe="$4" files="$5" expanded p
    local -a args=()
    cp "$MDB_ROOT/.env.example" "$s/.env"
    {
        printf '\nMDB_PUBLISH=%s\n' "$publish"
        printf 'MDB_PUBLISH_ENGINES=%s\n' "$pe"
        printf 'COMPOSE_FILE=%s\n' "$files"
    } >> "$s/.env"
    ( cd "$s" && MDB_ENV_FILE="$s/.env" scripts/gen-compose >/dev/null ) \
        || vfail "$name: gen-compose failed"
    # Files first, in COMPOSE_FILE's order -- later files override earlier
    # ones, so the order is the meaning -- then the profiles.
    local f
    for f in ${files//:/ }; do args+=(-f "$s/$f"); done
    expanded="$("$s/scripts/profiles" "$profiles")"
    for p in ${expanded//,/ }; do args+=(--profile "$p"); done
    mkdir -p "$s/out"
    if ! ( cd "$s" && unset COMPOSE_FILE COMPOSE_PROFILES && export COMPOSE_PROJECT_NAME=mdbrender \
            && mdb_compose --project-directory "$s" --env-file "$s/.env" "${args[@]}" \
            config --format json > "$s/out/$name.json" 2> "$s/out/$name.err" ); then
        sed 's/^/      /' "$s/out/$name.err" >&2
        vfail "$name ($profiles, publish=$publish): compose config failed"
    fi
}

# judge NAME PROD(0|1) PUBLISH_ENGINES -> prints one line per violation
judge() {
    python3 - "$s/out/$1.json" "$2" "$3" <<'PY'
import json, sys
path, prod, pe = sys.argv[1], sys.argv[2] == "1", sys.argv[3] == "true"
svc = json.load(open(path)).get("services", {})
bad = []
by_host = {}
for name, d in svc.items():
    if not d.get("image") and not d.get("build"):
        bad.append(f"{name}: no image and no build")
    if "dbpantry-pgbouncer" in str(d.get("image", "")):
        env = d.get("environment") or {}
        by_host.setdefault(env.get("DB_HOST"), []).append(name)
        if not env.get("DB_PASSWORD_FILE"):
            bad.append(f"{name}: a pgBouncer with no DB_PASSWORD_FILE exits on start")
        if "mdb-pgbouncer-bootstrap" not in " ".join(d.get("entrypoint") or []):
            bad.append(f"{name}: a pgBouncer not started through its bootstrap")
        if not d.get("tmpfs"):
            bad.append(f"{name}: a pgBouncer with no tmpfs for its generated userlist")
for host, names in by_host.items():
    if len(names) > 1:
        bad.append(f"{len(names)} pgBouncers for upstream {host}: {', '.join(sorted(names))}")
if prod:
    engines = {n for n, d in svc.items() if "MDB_TLS_ENFORCE" in (d.get("environment") or {})}
    for name, d in svc.items():
        pooler = name.endswith("-pooler") or name == "pgbouncer"
        if d.get("ports") and name != "caddy" and not pooler and not pe:
            bad.append(f"{name}: publishes {len(d['ports'])} port(s) under prod")
    for name in engines:
        v = str((svc[name].get("environment") or {}).get("MDB_TLS_ENFORCE"))
        if v.lower() != "true":
            bad.append(f"{name}: MDB_TLS_ENFORCE={v} under prod")
print("\n".join(bad))
print(f"#services={len(svc)} poolers={sum(len(v) for v in by_host.values())}", file=sys.stderr)
PY
}

cases=0
case_ok() {  # NAME PROFILES PUBLISH PUBLISH_ENGINES FILES PROD
    local out
    render "$1" "$2" "$3" "$4" "$5"
    out="$(judge "$1" "$6" "$4" 2>/dev/null)"
    [[ -z "$out" ]] || { printf '      %s\n' "$out" >&2; vfail "$1 ($2, publish=$3): see above"; }
    cases=$(( cases + 1 ))
}

case_ok pg            pg                                     none   false "$base_files" 0
case_ok pg-pooler     pg,pooler                              direct false "$base_files" 0
case_ok pg-ui         pg,pooler,ui                           none   false "$base_files" 0
case_ok mysql-family  mysql,mariadb,pooler                   direct false "$base_files" 0
case_ok all-engines   pg,mysql,mariadb,mongodb,cassandra,pooler direct false "$base_files" 0
case_ok ha            ha                                     none   false "$base_files" 0
case_ok ha-pooler     ha,pooler                              none   false "$base_files" 0
case_ok pg-and-ha     pg,ha,pooler                           none   false "$base_files" 0
case_ok prod-direct   pg,pooler,backup,prod                  direct false "$prod_files" 1
case_ok prod-none     pg,pooler,backup,prod                  none   false "$prod_files" 1
case_ok prod-proxy    pg,pooler,backup,prod                  proxy  false "$prod_files" 1
case_ok prod-all      pg,mysql,mariadb,mongodb,cassandra,pooler,prod direct false "$prod_files" 1
case_ok prod-ha       ha,pooler,backup,prod                  direct false "$prod_files" 1
case_ok prod-optout   pg,pooler,prod                         direct true  "$prod_files" 1
(( cases == 14 )) || vfail "rendered $cases combination(s), expected 14"
vinfo "$cases profile combinations render with one authenticating pooler per upstream"

# The prod rules must actually bite, not just pass. prod-direct: only the
# pooler's port; prod-optout: the engine's port is back.
published() {
    python3 - "$s/out/$1.json" "$2" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))["services"].get(sys.argv[2], {})
print(" ".join(str(p.get("published")) for p in d.get("ports") or []))
PY
}
[[ -z "$(published prod-direct pg)" ]] || vfail "prod-direct publishes pg on $(published prod-direct pg)"
[[ -n "$(published prod-direct pg-pooler)" ]] || vfail "prod-direct does not publish pg-pooler"
[[ -z "$(published prod-all mongodb)$(published prod-all cassandra)" ]] \
    || vfail "prod publishes MongoDB or Cassandra; the generated overlay must cover every engine"
[[ -n "$(published prod-optout pg)" ]] || vfail "MDB_PUBLISH_ENGINES=true did not restore pg's port under prod"
vinfo "prod publishes pg-pooler and not pg; MongoDB and Cassandra too; the opt-out restores pg"

# The HA pooler mirrors pg-pooler (rendered together in pg-and-ha).
drift="$(python3 - "$s/out/pg-and-ha.json" <<'PY'
import json, sys
svc = json.load(open(sys.argv[1]))["services"]
a = dict(svc["pg-pooler"].get("environment") or {})
b = dict(svc["pgbouncer"].get("environment") or {})
for k in ("DB_HOST",):
    a.pop(k, None); b.pop(k, None)
diff = sorted(set(a) ^ set(b)) + sorted(k for k in set(a) & set(b) if str(a[k]) != str(b[k]))
print(" ".join(diff))
PY
)"
[[ -z "$drift" ]] || vfail "the HA pooler's environment has drifted from pg-pooler's: $drift"
vinfo "the HA pooler's environment matches pg-pooler's apart from its upstream"

# A Compose network address must not reach Kubernetes: helm-values reads the
# engines' environment from compose.engines.yml, and the pooler address there
# (design.md D-74) means nothing in a cluster.
leak="$(cd "$s" && COMPOSE_PROFILES=pg,pg-pooler MDB_ENV_FILE="$s/.env" scripts/helm-values 2>/dev/null | grep -c '_POOLER_ADDR' || true)"
[[ "$leak" == 0 ]] || vfail "helm-values carries $leak Compose pooler address(es) into the chart"
vinfo "no Compose pooler address reaches the Helm values"

# --- the backup sidecars mount MDB_BACKUP_DIR ----------------------------------
#
# scripts/backup, restore and verify-backups resolve MDB_BACKUP_DIR (the
# environment, then .env, then backups/, relative to the checkout). The backup
# sidecars mounted ./backups whatever it said, so a configured directory got
# the manual dumps and never the scheduled ones, and retention pruned one tree
# while the other grew. Each case renders the backup profile and reads where
# every sidecar's /backups comes from.
# backup_source NAME ENV_LINE ENV_VALUE -> one "service source" line per sidecar
backup_source() {
    local name="$1" line="$2" val="$3"
    local -a args=() envset=()
    grep -v '^MDB_BACKUP_DIR=' "$MDB_ROOT/.env.example" > "$s/.env"
    [[ -n "$line" ]] && printf '\n%s\n' "$line" >> "$s/.env"
    printf 'COMPOSE_FILE=%s\n' "$base_files" >> "$s/.env"
    ( cd "$s" && MDB_ENV_FILE="$s/.env" scripts/gen-compose >/dev/null ) || vfail "$name: gen-compose failed"
    local f
    for f in ${base_files//:/ }; do args+=(-f "$s/$f"); done
    [[ -z "$val" ]] || envset=(MDB_BACKUP_DIR="$val")
    # shellcheck disable=SC2016  # expanded by the inner bash
    ( cd "$s" && env -u COMPOSE_FILE -u COMPOSE_PROFILES -u MDB_BACKUP_DIR COMPOSE_PROJECT_NAME=mdbrender ${envset[@]+"${envset[@]}"} bash -c '. scripts/runtime-lib.sh && mdb_compose "$@"' mdb_compose \
            --project-directory "$s" --env-file "$s/.env" "${args[@]}" \
            --profile pg --profile mysql --profile mariadb --profile backup \
            config --format json > "$s/out/backup-$name.json" 2> "$s/out/backup-$name.err" ) \
        || { sed 's/^/      /' "$s/out/backup-$name.err" >&2; vfail "backup-$name: compose config failed"; }
    python3 - "$s/out/backup-$name.json" <<'PY'
import json, sys
svc = json.load(open(sys.argv[1]))["services"]
for name in sorted(n for n in svc if n.startswith("backup-")):
    src = [v.get("source") for v in svc[name].get("volumes") or [] if v.get("target") == "/backups"]
    print(name, " ".join(str(x) for x in src) or "none")
PY
}

bcases=0
backup_case() {  # NAME ENV_LINE ENV_VALUE EXPECTED
    local out want n
    out="$(backup_source "$1" "$2" "$3")"
    n="$(printf '%s\n' "$out" | grep -c '^backup-' || true)"
    (( n == 3 )) || vfail "backup-$1: expected 3 backup sidecars, rendered $n: $out"
    want="$(printf 'backup-%s %s\n' mariadb "$4" mysql "$4" pg "$4")"
    [[ "$out" == "$want" ]] || vfail "backup-$1: the sidecars mount
$out
       but scripts/backup writes to $4"
    bcases=$(( bcases + 1 ))
}
backup_case unset    ''                                ''            "$s/backups"
backup_case example  'MDB_BACKUP_DIR=backups'          ''            "$s/backups"
backup_case relative 'MDB_BACKUP_DIR=dumps/nightly'    ''            "$s/dumps/nightly"
backup_case dotted   'MDB_BACKUP_DIR=./dumps'          ''            "$s/dumps"
backup_case absolute "MDB_BACKUP_DIR=$s/abs-dumps"     ''            "$s/abs-dumps"
backup_case environ  'MDB_BACKUP_DIR=dumps/nightly'    "$s/env-dumps" "$s/env-dumps"
(( bcases == 6 )) || vfail "rendered $bcases backup case(s), expected 6"
# The same answer scripts/backup gives, for the relative case.
# shellcheck disable=SC2016  # expanded by the inner bash
got="$(cd "$s" && env -u MDB_BACKUP_DIR bash -c '. scripts/env-lib.sh; mdb_backup_dir "$PWD" "$PWD/.env"')"
[[ "$got" == "$s/dumps/nightly" ]] || vfail "scripts resolve MDB_BACKUP_DIR to $got, the sidecars to $s/dumps/nightly"
vinfo "$bcases MDB_BACKUP_DIR settings: every backup sidecar mounts the directory scripts/backup writes to"

# --- design.md's service map is the rendered stack ------------------------------
#
# design.md section 2 lists every service and its image. It drifted from the
# compose files for months (etcd, HAProxy and Caddy on the step 1 plan, one
# db-backup service where three sidecars run, no MongoDB, Cassandra, ProxySQL
# or Patroni rows) and nothing noticed. Every service of the broadest render
# must have a row whose first `code` in the image cell is its image (`<ver>`
# stands for a version variable, `<engine>` for an engine name), and
# every row whose profile is not "—" (not a Compose service) must be rendered.
render everything pg,mysql,mariadb,mongodb,cassandra,pooler,ui,backup,metrics,ha none false "$base_files"
map_drift="$(python3 - "$s/out/everything.json" "$MDB_ROOT/design.md" <<'PY'
import json, re, sys
svc = json.load(open(sys.argv[1]))["services"]
text = open(sys.argv[2]).read()
sec = text.split("\n## 2.", 1)[1].split("\n## ", 1)[0]
rows = {}
for line in sec.splitlines():
    cells = [c.strip() for c in line.strip().strip("|").split("|")]
    if len(cells) < 3 or not cells[0].startswith("`"):
        continue
    names = re.findall(r"`([^`]+)`", cells[0])
    first = re.search(r"`([^`]+)`", cells[1])
    image = first.group(1) if first else cells[1]
    for n in names:
        rows[n] = (image, cells[2])
bad = []
for name, d in svc.items():
    if name not in rows:
        bad.append(f"{name}: rendered, but design.md section 2 has no row for it")
        continue
    got = str(d.get("image", "")).split("@")[0]
    want = rows[name][0]
    pat = "^" + re.escape(want).replace(re.escape("<ver>"), "[^:/@]+").replace(re.escape("<engine>"), "[a-z]+") + "$"
    if not re.match(pat, got):
        bad.append(f"{name}: design.md says {want}, the compose files run {got}")
for name, (image, prof) in rows.items():
    if not prof.startswith("—") and name not in svc:
        bad.append(f"{name}: in design.md section 2 (profile {prof}), but no compose file defines it")
print("\n".join(bad))
PY
)"
[[ -z "$map_drift" ]] || { printf '      %s\n' "$map_drift" >&2; vfail "design.md section 2 disagrees with the compose files (listed above)"; }
nsvc="$(python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1]))["services"]))' "$s/out/everything.json")"
(( nsvc >= 25 )) || vfail "the broadest render has $nsvc services; expected at least 25 (measured 2026-11-16)"
vinfo "design.md section 2 names all $nsvc rendered services with the images they run"
