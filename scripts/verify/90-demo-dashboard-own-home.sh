#!/usr/bin/env bash
# verify: the detached demo dashboard runs on its own home's stack, never the caller's project or profiles
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# `make demo-dashboard` exports the checkout's COMPOSE_PROJECT_NAME and
# COMPOSE_PROFILES, and scripts/demo-dashboard handed its whole environment to
# the dashboard, where they outrank the demo home's .env. On 2026-11-25 the demo
# in /tmp/mdb-live showed project mdb's network and profiles: a Start pressed
# there would have booted the operator's own project on their data volumes.

command -v python3 >/dev/null 2>&1 || vfail "python3 is needed (scripts/demo-dashboard uses it)"
command -v curl >/dev/null 2>&1 || vfail "curl is needed (scripts/demo-dashboard uses it)"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/mdb-demo.XXXXXX")"
mkdir -p "$tmp/home"
printf 'COMPOSE_PROJECT_NAME=mdbdemoverify\nMDB_PROFILES="pg"\n' > "$tmp/home/.env"
port=$(( 47000 + RANDOM % 900 ))

# A stand-in dbpantry: `runtime info` succeeds; `dashboard` records the
# variables that pick a stack, then answers 200 on --listen.
cat > "$tmp/dbpantry" <<'STUB'
#!/usr/bin/env python3
import os, sys, http.server
if "runtime" in sys.argv:
    sys.exit(0)
listen = sys.argv[sys.argv.index("--listen") + 1]
with open(os.environ["STUB_ENV"], "w") as f:
    for k in ("COMPOSE_PROJECT_NAME", "COMPOSE_PROFILES", "MDB_ENV_FILE", "ENV_FILE", "PROFILES", "MDB_HOME"):
        f.write("%s=%s\n" % (k, os.environ.get(k, "")))
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b"ok")
    def log_message(self, *a): pass
host, port = listen.rsplit(":", 1)
http.server.HTTPServer((host, int(port)), H).serve_forever()
STUB
chmod +x "$tmp/dbpantry"

demo() {
    env COMPOSE_PROJECT_NAME=mdb COMPOSE_PROFILES=pg,mysql,ui MDB_ENV_FILE="$MDB_ROOT/.env" PROFILES=pg,mysql \
        STUB_ENV="$tmp/env.txt" MDB_BIN="$tmp/dbpantry" MDB_HOME="$tmp/home" LISTEN="127.0.0.1:$port" \
        PIDFILE="$tmp/pid" LOGFILE="$tmp/log" scripts/demo-dashboard "$@"
}
add_cleanup "env LISTEN='127.0.0.1:$port' PIDFILE='$tmp/pid' scripts/demo-dashboard stop; rm -rf '$tmp'"

demo start >/dev/null 2>&1 || vfail "scripts/demo-dashboard start failed: $(tail -5 "$tmp/log" 2>/dev/null)"
for _ in 1 2 3 4 5 6 7 8; do [[ -s "$tmp/env.txt" ]] && break; sleep 0.25; done
[[ -s "$tmp/env.txt" ]] || vfail "the stand-in dashboard never started"

grep -qx "MDB_HOME=$tmp/home" "$tmp/env.txt" || vfail "the demo did not run on its own home: $(tr '\n' ' ' < "$tmp/env.txt")"
leaked=""
for k in COMPOSE_PROJECT_NAME COMPOSE_PROFILES MDB_ENV_FILE ENV_FILE PROFILES; do
    grep -qx "$k=" "$tmp/env.txt" || leaked+=" $(grep "^$k=" "$tmp/env.txt")"
done
[[ -z "$leaked" ]] || vfail "the caller's stack reached the demo dashboard:$leaked"
vinfo "the demo dashboard runs on its own home; the caller's project, profiles and env file stay out"
