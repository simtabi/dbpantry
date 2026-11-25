#!/usr/bin/env bash
# verify: every service that runs one of our own images can build it, so `up` builds whatever is missing
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Compose builds a missing image on `up` only for a service with a build: key;
# without one it pulls, and our images exist in no registry. The pgBouncer
# pooler (pg-pooler, generated, and the HA pgbouncer) had none, so `up` with the
# pooler or HA profile pulled localhost/dbpantry-pgbouncer:dev, failed, and
# aborted the whole start, interrupting the patroni build beside it. That is
# why the full `make verify` of 2026-11-25 failed checks 27, 33 and 68 after a
# `make build` that did not reach the derived images.

cd "$MDB_ROOT" || exit 1
scripts/gen-compose >/dev/null 2>&1 || vfail "scripts/gen-compose failed"

files=(docker-compose.yml compose.*.yml)
(( ${#files[@]} >= 4 )) || vfail "found only ${#files[@]} compose files to inspect"

# A service needs no build: of its own when another service builds the same
# image: Compose then tries to pull it, fails, builds it, and `up` succeeds
# (measured with Compose 5.5.0, 2026-11-25). The backup sidecars run on their
# engine's image that way.
#
# One line per service running an image named dbpantry-*: FILE SERVICE BUILD,
# where BUILD is yes when the service, or the x- block it merges with <<: *name,
# has a build: key, or when another service that has one builds the same image.
scan() {
    awk '
        function flush() {
            if (blk != "") { img[FILENAME SUBSEP blk] = image; bld[FILENAME SUBSEP blk] = build; inh[FILENAME SUBSEP blk] = from; svc[FILENAME SUBSEP blk] = isvc }
            image = ""; build = 0; from = ""
        }
        FNR == 1 { flush(); blk = ""; top = "" }
        /^[A-Za-z0-9_.-]+:/ {
            flush(); top = $1; sub(/:.*/, "", top); blk = ""
            if (top ~ /^x-/) { blk = top; isvc = 0; if (match($0, /&[A-Za-z0-9_-]+/)) anchor[FILENAME SUBSEP substr($0, RSTART + 1, RLENGTH - 1)] = top }
            next
        }
        top == "services" && /^  [A-Za-z0-9_.-]+:/ { flush(); blk = $1; sub(/:.*/, "", blk); isvc = 1; next }
        blk != "" && /^[[:space:]]+build:/ { build = 1 }
        blk != "" && /^[[:space:]]+image:/ { image = $0; sub(/^[[:space:]]*image:[[:space:]]*/, "", image) }
        # Only the merge at the block level counts: a nested one (patroni_env) merges an
        # environment map, not the service.
        blk != "" && from == "" && ((isvc && /^    <<: \*/) || (!isvc && /^  <<: \*/)) { match($0, /\*[A-Za-z0-9_-]+/); from = substr($0, RSTART + 1, RLENGTH - 1) }
        END {
            flush()
            # Resolve merges first, then collect every image some service builds.
            for (k in svc) {
                if (!svc[k]) continue
                split(k, p, SUBSEP); f = p[1]
                if (inh[k] != "" && ((f SUBSEP inh[k]) in anchor)) {
                    a = f SUBSEP anchor[f SUBSEP inh[k]]
                    if (img[k] == "") img[k] = img[a]
                    if (!bld[k]) bld[k] = bld[a]
                }
                if (bld[k] && img[k] != "") built[img[k]] = 1
            }
            for (k in svc) {
                if (!svc[k]) continue
                split(k, p, SUBSEP); f = p[1]; s = p[2]
                i = img[k]; b = bld[k] || (i in built)
                if (i ~ /dbpantry-/) print f, s, (b ? "yes" : "no")
            }
        }
    ' "${files[@]}"
}

n=0 bad=0
while read -r file service build; do
    n=$(( n + 1 ))
    if [[ "$build" != yes ]]; then
        printf '      %s: service %s runs a dbpantry image and has no build: key\n' "$file" "$service" >&2
        bad=$(( bad + 1 ))
    fi
done < <(scan)

# Measured 2026-11-25 against the default .env: 13 such services (four
# engines, three backup sidecars, caddy, pg-pooler, three patroni nodes and
# the HA pgbouncer).
(( n >= 12 )) || vfail "found only $n services running a dbpantry image; the scan inspected too little"
(( bad == 0 )) || vfail "$bad of $n services cannot build their image, so up pulls it and fails"
vinfo "all $n services running a dbpantry image can build it on up"
