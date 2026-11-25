#!/usr/bin/env bash
# verify: scripts/build builds every images/<name>/Dockerfile by name, and a full build builds them all
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# The dashboard's Build images offers every images/<name>/Dockerfile
# (design.md D-111), and `make build` promises every image. scripts/build took
# engines, cli, pgbouncer and patroni, but not caddy: `scripts/build caddy`
# failed, and a full build never rebuilt it, so an edited caddy Dockerfile
# reached nobody until the image was removed by hand (2026-11-25).

stubs="$(mktemp -d "${TMPDIR:-/tmp}/mdb-build-stubs.XXXXXX")"
add_cleanup "rm -rf '$stubs'"
log="$stubs/calls.log"
cat > "$stubs/docker" <<'STUB'
#!/usr/bin/env bash
case "$1 ${2:-}" in
    "info "*) exit 0 ;;
    "--version "*) echo "Docker version 99.0.0"; exit 0 ;;
    "compose version") echo "2.40.0"; exit 0 ;;
    "build "*|"buildx build") printf '%s\n' "$*" >> "$STUB_LOG"; exit 0 ;;
    "image inspect"|"images "*) exit 1 ;;
esac
exit 0
STUB
chmod +x "$stubs/docker"

build() {
    : > "$log"
    env -u MDB_IMAGE_PREFIX PATH="$stubs:$PATH" STUB_LOG="$log" CONTAINER_RUNTIME=docker \
        MDB_ENV_FILE="$MDB_ROOT/.env.example" scripts/build "$@" >/dev/null 2>&1
}

names=()
for f in images/*/Dockerfile; do
    [[ -f "$f" ]] || continue
    n="${f#images/}"; names+=("${n%/Dockerfile}")
done
(( ${#names[@]} >= 8 )) || vfail "found only ${#names[@]} images/<name>/Dockerfile to inspect"

bad=0
for n in "${names[@]}"; do
    if ! build "$n"; then
        printf '      scripts/build %s failed\n' "$n" >&2; bad=$(( bad + 1 )); continue
    fi
    if ! grep -q -- "-f $MDB_ROOT/images/$n/Dockerfile" "$log" || ! grep -q -- "-t localhost/dbpantry-$n:" "$log"; then
        printf '      scripts/build %s did not build images/%s/Dockerfile as localhost/dbpantry-%s: %s\n' \
            "$n" "$n" "$n" "$(head -1 "$log")" >&2
        bad=$(( bad + 1 ))
    fi
done
(( bad == 0 )) || vfail "$bad of ${#names[@]} images cannot be built by name"
vinfo "scripts/build builds each of the ${#names[@]} images by name: ${names[*]}"

build || vfail "a full scripts/build failed"
missing=""
for n in "${names[@]}"; do
    grep -q -- "-f $MDB_ROOT/images/$n/Dockerfile" "$log" || missing+=" $n"
done
[[ -z "$missing" ]] || vfail "a full build leaves out:$missing"
vinfo "a full build builds all ${#names[@]}"
