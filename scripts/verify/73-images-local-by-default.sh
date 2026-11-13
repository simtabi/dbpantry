#!/usr/bin/env bash
# verify: our images are named localhost/ by default, never after a remote registry, and a set MDB_IMAGE_PREFIX or global.imageRegistry names every one
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# design.md D-78. dbpantry publishes no image (D-77), yet until 2026-11-13 every
# image was built, run and deployed as ghcr.io/simtabi/dbpantry-*: the Helm
# chart's default registry, the Compose files, the Kubernetes backend's kind
# list and a dozen scripts each carried that name. A cluster missing an image
# therefore pulled from a registry that holds nothing. The default is now
# localhost, written once in scripts/env-lib.sh (MDB_IMAGE_PREFIX_DEFAULT) and
# repeated only where a file cannot read it: Compose, the chart, the patroni
# Dockerfile and an engine descriptor. This check holds all of them to it.

cd "$MDB_ROOT" || exit 1
scripts/gen-compose >/dev/null 2>&1 || vfail "scripts/gen-compose failed"

def=localhost
remote='registry.example.com/team'

# --- 1. every default is localhost ------------------------------------------------
grep -qx "MDB_IMAGE_PREFIX_DEFAULT=$def" scripts/env-lib.sh \
    || vfail "scripts/env-lib.sh does not set MDB_IMAGE_PREFIX_DEFAULT=$def"
grep -qx "  imageRegistry: $def" deploy/helm/dbpantry/values.yaml \
    || vfail "the chart's global.imageRegistry default is not $def: $(grep -m1 'imageRegistry:' deploy/helm/dbpantry/values.yaml)"
while IFS= read -r line; do
    [[ "$line" == *"ARG IMAGE_PREFIX=$def" ]] || vfail "a Dockerfile defaults IMAGE_PREFIX to something else: $line"
done < <(grep -H '^ARG IMAGE_PREFIX=' images/*/Dockerfile)

# Compose: every image of ours is ${MDB_IMAGE_PREFIX:-localhost}/dbpantry-*, in
# the tracked files and the ones scripts/gen-compose just wrote.
ours=0
while IFS=: read -r file _ ref; do
    ours=$(( ours + 1 ))
    ref="${ref#"${ref%%[![:space:]]*}"}"
    # shellcheck disable=SC2016  # a literal Compose ${...}, not an expansion
    [[ "$ref" == 'image: ${MDB_IMAGE_PREFIX:-'"$def"'}/dbpantry-'* ]] \
        || vfail "$file: $ref does not take its prefix from \${MDB_IMAGE_PREFIX:-$def}"
done < <(grep -nE '^[[:space:]]*image:.*/dbpantry-' docker-compose.yml compose.*.yml)
# docker-compose.yml (4), compose.ha.yml (2) and the generated engines (5) on
# 2026-11-13; fewer means this is not reading the Compose files.
(( ours >= 8 )) || vfail "found $ours dbpantry image line(s) in the Compose files, want at least 8"
others="$(grep -hoE 'MDB_IMAGE_PREFIX:-[^}]*' docker-compose.yml compose.*.yml | sort -u)"
[[ "$others" == "MDB_IMAGE_PREFIX:-$def" ]] || vfail "a Compose default prefix is not $def: $others"

# Engine descriptors write our images with the default prefix.
while IFS= read -r line; do
    ref="$(printf '%s' "$line" | sed -E "s/.*_IMAGE='//; s/'$//")"
    [[ "$ref" == "$def/"* ]] || vfail "${line%%:*}: $ref is not named $def/"
done < <(grep -HoE "_IMAGE='[^']*/dbpantry-[^']*'" engines/*/engine.conf)
vinfo "every default prefix is $def: env-lib, the chart, Compose ($ours image lines), the Dockerfiles and the descriptors"

# --- 2. nothing names a remote registry for our images ------------------------------
# A dotted host (or host:port) before dbpantry-<x>, in anything but prose. The
# Markdown docs may show registry.example.com as the user's own registry.
scanned=0
bad=0
while IFS= read -r -d '' f; do
    case "$f" in *.md|scripts/verify/73-images-local-by-default.sh) continue ;; esac
    scanned=$(( scanned + 1 ))
    if grep -nE '([A-Za-z0-9-]+\.)+[A-Za-z]{2,}(:[0-9]+)?/([A-Za-z0-9._-]+/)*dbpantry-[a-z]' "$f" \
        | grep -v 'ghcr.io/simtabi' >/dev/null; then
        printf '      %s names a remote registry for a dbpantry image\n' "$f" >&2
        bad=$(( bad + 1 ))
    fi
done < <(git ls-files -z)
(( scanned >= 300 )) || vfail "scanned $scanned tracked file(s), want at least 300: git ls-files is not listing the repository"
(( bad == 0 )) || vfail "$bad file(s) name a remote registry for our images (listed above)"

# ghcr.io/simtabi may only describe the frozen images, or the history that
# published them. Each file is held to the number of lines measured on
# 2026-11-13, so a new mention fails here and an old one that goes stale
# fails too.
frozen_lines() {
    case "$1" in
        .github/workflows/release.yml) echo 1 ;;          # the header: images on ghcr.io/simtabi are frozen
        CHANGELOG.md) echo 3 ;;                           # history, and the D-77/D-78 entries
        README.md) echo 1 ;;                              # the images are frozen
        design.md) echo 4 ;;                              # D-02, D-23 and D-77 history, D-78
        docs/guides/kubernetes.md) echo 1 ;;              # nothing falls back to the frozen images
        docs/guides/licensing.md) echo 1 ;;               # no registry attestation; the images are frozen
        docs/guides/release.md) echo 2 ;;                 # the frozen tag scheme
        docs/reference/spec.md) echo 4 ;;                 # the original SPEC, kept as written
        src/internal/release/workflow_test.go) echo 4 ;;  # samples of the image pushes the release guard refuses
        *) echo 0 ;;
    esac
}
listed=0
while IFS= read -r -d '' f; do
    [[ "$f" == scripts/verify/73-images-local-by-default.sh ]] && continue
    n="$(grep -c 'ghcr.io/simtabi' "$f" || true)"
    want="$(frozen_lines "$f")"
    (( want > 0 )) && listed=$(( listed + 1 ))
    (( n == want )) && continue
    (( want == 0 )) && vfail "$f mentions ghcr.io/simtabi $n time(s); our images are localhost/ (D-78)"
    vfail "$f mentions ghcr.io/simtabi $n time(s), measured $want: update the entry or the file"
done < <(git ls-files -z)
(( listed == 9 )) || vfail "found $listed of the 9 files allowed to describe the frozen images: an entry names a missing file"
vinfo "$scanned tracked file(s): no remote registry for our images; ghcr.io/simtabi only where the frozen images are described"

# --- 3. a set prefix names every image -----------------------------------------------
lib_out() { (. scripts/env-lib.sh && "$@"); }
[[ "$(MDB_IMAGE_PREFIX='' lib_out mdb_image pg 17)" == "$def/dbpantry-pg:17" ]] \
    || vfail "mdb_image with no prefix set is not $def/dbpantry-pg:17"
[[ "$(MDB_IMAGE_PREFIX="$remote/" lib_out mdb_image pg 17)" == "$remote/dbpantry-pg:17" ]] \
    || vfail "mdb_image does not apply MDB_IMAGE_PREFIX"
[[ "$(MDB_IMAGE_PREFIX="$remote" lib_out mdb_image_ref "$def/dbpantry-pgbouncer:dev")" == "$remote/dbpantry-pgbouncer:dev" ]] \
    || vfail "mdb_image_ref does not move the descriptor's pooler image to MDB_IMAGE_PREFIX"
[[ "$(MDB_IMAGE_PREFIX="$remote" lib_out mdb_image_ref docker.io/library/mongo:7.0)" == docker.io/library/mongo:7.0 ]] \
    || vfail "mdb_image_ref rewrote an upstream image"

# The Makefile's own use (make shell).
grep -qE "[[:space:]]$def/dbpantry-cli:dev" < <(MDB_IMAGE_PREFIX='' make -s -n shell 2>/dev/null) \
    || vfail "make shell does not run $def/dbpantry-cli:dev by default"
grep -qE "[[:space:]]$remote/dbpantry-cli:dev" < <(MDB_IMAGE_PREFIX="$remote" make -s -n shell 2>/dev/null) \
    || vfail "make shell ignores MDB_IMAGE_PREFIX"

# Kubernetes: helm-values hands the prefix to the chart, and the backend loads
# exactly the names the chart will run.
k8s_images() {
    local values="$1"
    (
        mdb_backend_register() { :; }
        . scripts/env-lib.sh
        . scripts/backends/kubernetes.sh
        _mdb_k8s_local_images "$values"
    )
}
tmp="$(mktemp -d "${TMPDIR:-/tmp}/mdb-images.XXXXXX")"
add_cleanup "rm -rf '$tmp'"
for prefix in "$def" "$remote"; do
    MDB_IMAGE_PREFIX="$( [[ "$prefix" == "$def" ]] || printf '%s' "$prefix")" MDB_PROFILES=pg,mysql,ha \
        scripts/helm-values -o "$tmp/values.yaml" 2>/dev/null || vfail "scripts/helm-values failed"
    grep -qx "  imageRegistry: \"$prefix\"" "$tmp/values.yaml" \
        || vfail "helm-values does not render global.imageRegistry \"$prefix\""
    n=0
    while IFS= read -r img; do
        n=$(( n + 1 ))
        [[ "$img" == "$prefix/dbpantry-"* ]] || vfail "the Kubernetes backend would load $img, not $prefix/dbpantry-*"
    done < <(k8s_images "$tmp/values.yaml")
    (( n >= 3 )) || vfail "the Kubernetes backend listed $n image(s) for pg, mysql and ha, want at least 3"

    if command -v helm >/dev/null 2>&1; then
        set_args=()
        [[ "$prefix" == "$def" ]] || set_args=(--set "global.imageRegistry=$prefix")
        helm template r deploy/helm/dbpantry --set engines.pg.enabled=true --set engines.mysql.enabled=true \
            --set engines.pg.ha.enabled=true ${set_args[@]+"${set_args[@]}"} >"$tmp/out.yaml" 2>"$tmp/err" \
            || vfail "helm template: $(cat "$tmp/err")"
        imgs="$(grep -E '^[[:space:]]+image: ' "$tmp/out.yaml" | grep '/dbpantry-' || true)"
        [[ "$(printf '%s\n' "$imgs" | grep -c .)" -ge 2 ]] || vfail "helm template rendered no dbpantry image"
        printf '%s\n' "$imgs" | grep -vE "image: \"?$prefix/dbpantry-" && vfail "helm template ran an image outside $prefix/ (above)"
        grep -q 'ghcr.io' "$tmp/out.yaml" && vfail "helm template rendered a ghcr.io image"
        grep -qE 'imagePullPolicy: IfNotPresent' "$tmp/out.yaml" || vfail "the chart no longer uses imagePullPolicy: IfNotPresent"
    fi
done
if command -v helm >/dev/null 2>&1; then
    vinfo "unset, every image is $def/dbpantry-*; with $remote set, every image (Make, helm-values, kind loading, the rendered chart) carries it"
else
    vinfo "unset, every image is $def/dbpantry-*; with $remote set, Make, helm-values and kind loading carry it (helm is not installed, so the chart was not rendered)"
fi
