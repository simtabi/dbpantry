#!/usr/bin/env bash
# verify: scripts/scan scans every image scripts/build builds, under the same tag, and an unknown name fails
# tags: fast structure security
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# scripts/scan took engines and cli only. `scripts/scan pg pgbouncer` scanned pg
# and dropped pgbouncer without a word, a full scan never reached pgbouncer,
# patroni or caddy, and `scripts/scan pgbouncer` died with "no images are
# built", which was not why. So our own derived images had never been scanned
# (2026-11-25). Scan and build each compute an image's tag; this check holds
# them to the same one.

stubs="$(mktemp -d "${TMPDIR:-/tmp}/mdb-scan-stubs.XXXXXX")"
add_cleanup "rm -rf '$stubs'"
log="$stubs/calls.log"
cat > "$stubs/docker" <<'STUB'
#!/usr/bin/env bash
case "$1 ${2:-}" in
    "info "*) exit 0 ;;
    "--version "*) echo "Docker version 99.0.0"; exit 0 ;;
    "compose version") echo "2.40.0"; exit 0 ;;
    "image inspect") exit 0 ;;
    "save -o") printf 'save %s\n' "${4:-}" >> "$STUB_LOG"; exit 0 ;;
    "build "*|"buildx build") printf 'build %s\n' "$*" >> "$STUB_LOG"; exit 0 ;;
    "run "*) exit 0 ;;
esac
exit 0
STUB
chmod +x "$stubs/docker"

run() {
    : > "$log"
    env -u MDB_IMAGE_PREFIX PATH="$stubs:$PATH" STUB_LOG="$log" CONTAINER_RUNTIME=docker HOME="$stubs" \
        MDB_ENV_FILE="$MDB_ROOT/.env.example" "$@" >/dev/null 2>&1
}

names=()
for f in images/*/Dockerfile; do
    [[ -f "$f" ]] || continue
    n="${f#images/}"; names+=("${n%/Dockerfile}")
done
(( ${#names[@]} >= 8 )) || vfail "found only ${#names[@]} images/<name>/Dockerfile to inspect"

bad=0
for n in "${names[@]}"; do
    run scripts/build "$n" || { printf '      scripts/build %s failed\n' "$n" >&2; bad=$(( bad + 1 )); continue; }
    built="$(grep -oE -- "-t [^ ]+" "$log" | head -1 | cut -c4-)"
    if ! run scripts/scan "$n"; then
        printf '      scripts/scan %s failed\n' "$n" >&2; bad=$(( bad + 1 )); continue
    fi
    scanned="$(awk '/^save /{print $2; exit}' "$log")"
    if [[ -z "$built" || "$scanned" != "$built" ]]; then
        printf '      %s: built as %s, scanned as %s\n' "$n" "${built:-nothing}" "${scanned:-nothing}" >&2
        bad=$(( bad + 1 ))
    fi
done
(( bad == 0 )) || vfail "$bad of ${#names[@]} images are not scanned under the tag they are built with"
vinfo "scripts/scan scans each of the ${#names[@]} images under its build tag"

run scripts/scan || vfail "a full scripts/scan failed"
missing=""
for n in "${names[@]}"; do grep -q "^save localhost/dbpantry-$n:" "$log" || missing+=" $n"; done
[[ -z "$missing" ]] || vfail "a full scan leaves out:$missing"
vinfo "a full scan covers all ${#names[@]}"

if run scripts/scan pg nope-not-an-image; then
    vfail "scripts/scan accepted a name that is not an image"
fi
vinfo "an unknown image name fails the scan instead of being skipped"
