#!/usr/bin/env bash
# verify: Apple container through socktainer is detected, refused without compose.apple.yml, and builds load
# tags: fast structure runtime
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Apple's container tool runs dbpantry through socktainer, a Docker API on top
# of it (design.md D-63). Three things have to stay true, tested here against a
# stub docker CLI so the result does not depend on this machine:
#
#   1. a socktainer endpoint is recognised, from DOCKER_HOST or from the server
#      component name, and nothing else is mistaken for it;
#   2. without compose.apple.yml in COMPOSE_FILE every container would get a
#      1 GiB VM while PG tunes shared_buffers for 4g, so the runtime refuses;
#   3. builds pass --load, or the image stays in BuildKit's cache and `make up`
#      cannot find it.
# A fourth part checks the overlay sizes every engine from its MEM_LIMIT.

cd "$MDB_ROOT" || exit 1

stubs="$(mktemp -d "${TMPDIR:-/tmp}/mdb-apple-stubs.XXXXXX")"
add_cleanup "rm -rf '$stubs'"
root="$stubs/root"; mkdir -p "$root"
# The stub answers like a socktainer daemon when STUB_SERVER=socktainer, and
# logs every call so the build flags can be read back.
cat > "$stubs/docker" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$STUB_LOG"
case "$1 ${2:-}" in
    "info "*)          exit 0 ;;
    "--version "*)     echo "Docker version 29.0.0"; exit 0 ;;
    "compose version") echo "Docker Compose version v2.40.0"; exit 0 ;;
    "context inspect") echo "${STUB_CONTEXT_HOST:-unix:///var/run/docker.sock}"; exit 0 ;;
    "version "*)       [ "${STUB_SERVER:-}" = socktainer ] && echo "socktainer Engine " || echo "Engine "; exit 0 ;;
esac
exit 0
STUB
chmod +x "$stubs/docker"
log="$stubs/calls.log"

# run <COMPOSE_FILE value> <bash snippet> [VAR=value...]: a clean environment
# with only the stub, bash and coreutils on PATH.
run() {
    local cf="$1" snippet="$2"; shift 2
    printf 'COMPOSE_FILE=%s\n' "$cf" > "$root/.env"
    : > "$log"
    # shellcheck disable=SC2016  # expanded by the inner bash
    env -i PATH="$stubs:/usr/bin:/bin" HOME="$HOME" STUB_LOG="$log" MDB_RT_ROOT="$root" "$@" \
        bash -c '. "$1"; '"$snippet" _ "$MDB_ROOT/scripts/runtime-lib.sh" 2>&1 || true
}
base="docker-compose.yml:compose.engines.yml"
sock="unix:///opt/homebrew/var/run/socktainer/.socktainer/container.sock"

# --- 1. detection -------------------------------------------------------------
got="$(run "$base:compose.apple.yml" 'mdb_runtime_provider' DOCKER_HOST="$sock")"
[[ "$got" == "apple-container (socktainer)" ]] || vfail "DOCKER_HOST at socktainer: provider [$got]"
got="$(run "$base:compose.apple.yml" 'mdb_runtime_provider' STUB_CONTEXT_HOST="unix:///Users/x/.socktainer/container.sock")"
[[ "$got" == "apple-container (socktainer)" ]] || vfail "a socktainer docker context: provider [$got]"
got="$(run "$base:compose.apple.yml" 'mdb_runtime_provider' DOCKER_HOST="unix:///tmp/elsewhere.sock" STUB_SERVER=socktainer)"
[[ "$got" == "apple-container (socktainer)" ]] || vfail "a socktainer server on another socket: provider [$got]"
got="$(run "$base" 'mdb_runtime_provider')"
[[ "$got" != *socktainer* ]] || vfail "a plain Docker engine was taken for socktainer: [$got]"
vinfo "socktainer is recognised by its socket or its server name, and nothing else is"

# --- 2. the memory overlay is required ------------------------------------------
got="$(run "$base" 'mdb_runtime_init && echo READY' DOCKER_HOST="$sock")"
[[ "$got" == *"compose.apple.yml"* && "$got" != *READY* ]] \
    || vfail "socktainer without compose.apple.yml was not refused: [$got]"
got="$(run "$base:compose.apple.yml" 'mdb_runtime_init && echo READY' DOCKER_HOST="$sock")"
[[ "$got" == *READY* ]] || vfail "socktainer with compose.apple.yml was refused: [$got]"
got="$(run "$base" 'mdb_runtime_init && echo READY')"
[[ "$got" == *READY* ]] || vfail "plain Docker now requires compose.apple.yml: [$got]"
vinfo "socktainer without compose.apple.yml is refused, with it or on plain Docker it runs"

# --- 3. builds load into the image store --------------------------------------
run "$base:compose.apple.yml" 'mdb_build -t x:dev .' DOCKER_HOST="$sock" >/dev/null
grep -qx 'build --load -t x:dev .' "$log" || vfail "socktainer build did not pass --load: $(grep build "$log")"
run "$base" 'mdb_build -t x:dev .' >/dev/null
grep -qx 'build -t x:dev .' "$log" || vfail "a plain Docker build changed: $(grep build "$log")"
vinfo "builds on socktainer pass --load; plain Docker builds are unchanged"

# --- 4. the overlay sizes every engine from its MEM_LIMIT ---------------------
for e in pg mysql mariadb mongodb cassandra; do
    v="MDB_$(printf '%s' "$e" | tr '[:lower:]' '[:upper:]')_MEM_LIMIT"
    awk -v s="  $e:" -v v="\${$v:-" '
        $0 == s { inside = 1; next }
        inside && /^  [a-z]/ { inside = 0 }
        inside && index($0, "memory: " v) { found = 1 }
        END { exit !found }' compose.apple.yml \
        || vfail "compose.apple.yml does not size $e from $v"
done
while read -r e; do
    grep -qE "^  $e:\$" compose.engines.yml || vfail "compose.apple.yml names $e, which compose.engines.yml does not define"
done < <(awk '/^  [a-z][a-z0-9-]*:$/ { sub(":", ""); print $1 }' compose.apple.yml)
vinfo "compose.apple.yml gives pg, mysql, mariadb, mongodb and cassandra the memory they are tuned for"
