#!/usr/bin/env bash
# verify: every image takes its distribution's updates through one helper, downloads only through mdb-fetch with a recorded digest, and CI gates image findings where they can have been caused
# tags: fast structure supply-chain
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# design.md D-71. Three failures in one pull request, one class each:
#
#   - apt (CVE-2026-109518), then x/net: fixable findings in a pinned base,
#     patched one package at a time with --only-upgrade lines, so each new
#     advisory failed every pull request -- including ones touching no image.
#   - a verify run lost to "HTTP/2 stream 1 was not closed cleanly" on the PGDG
#     key fetch, which had no retry, and whose result nothing verified.
#   - downloads (s6-overlay, pg_graphql) never checked against any digest.
#
# This check holds the fixes in place without building anything:
#   1. every image's final stage installs and runs mdb-os-upgrade, and no
#      --only-upgrade line remains;
#   2. no Dockerfile downloads with curl or wget directly; mdb-fetch does it;
#   3. every mdb-fetch either has a recorded digest for the versions the
#      Dockerfiles pin, a key fingerprint, or --verified-by with the check
#      it names present in the same RUN;
#   4. mdb-fetch itself refuses a wrong or unrecorded digest (offline, file://);
#   5. CI has one weekly schedule, and ci-scan-gate gates a pull request only
#      when it changes an image input (tested on a scratch repository).

cd "$MDB_ROOT" || vfail "no repository root"
sup=images/_shared/supply
sums="$sup/checksums.txt"
for f in "$sup/os-upgrade.sh" "$sup/fetch.sh" "$sums" scripts/ci-scan-gate .github/workflows/ci.yml; do
    need_file "$f"
done

# Joined RUN instructions: continuation lines folded, comment lines dropped.
folded() { grep -vE '^[[:space:]]*#' "$1" | sed -e ':a' -e '/\\$/N; s/\\\n//; ta'; }

# --- 1 and 2 ----------------------------------------------------------------
images=0; upgraded=0
for df in images/*/Dockerfile; do
    images=$(( images + 1 ))
    img="$(basename "$(dirname "$df")")"
    final="$(awk '/^FROM[[:space:]]/{buf=""} {buf=buf $0 "\n"} END{printf "%s", buf}' "$df")"
    if grep -qE '^FROM[[:space:]]+\$\{IMAGE_PREFIX\}/dbpantry-' <<< "$final"; then
        # patroni: built FROM dbpantry-pg in the same run, so it inherits pg's
        # upgraded layer rather than repeating it.
        vinfo "$img: final stage is FROM a dbpantry image; inherits its upgrade"
    else
        grep -q '_shared/supply/os-upgrade.sh' <<< "$final" \
            || vfail "$df: the final stage does not install mdb-os-upgrade"
        grep -qE '^RUN .*mdb-os-upgrade' <<< "$final" \
            || vfail "$df: the final stage never runs mdb-os-upgrade"
        upgraded=$(( upgraded + 1 ))
    fi
    if grep -vE '^[[:space:]]*#' "$df" | grep -q -- '--only-upgrade'; then
        vfail "$df upgrades packages by name; mdb-os-upgrade takes the whole distribution's updates"
    fi
    if folded "$df" | grep -qE '(^|[;&|[:space:]])(curl|wget)[[:space:]]+-'; then
        vfail "$df downloads with curl or wget directly; use mdb-fetch (retried and verified)"
    fi
done
(( images >= 8 )) || vfail "inspected $images Dockerfile(s), expected at least 8"
(( upgraded >= 7 )) || vfail "only $upgraded image(s) run mdb-os-upgrade, expected at least 7"
vinfo "$images Dockerfile(s): $upgraded run mdb-os-upgrade, none upgrade by name or download outside mdb-fetch"

# --- 3 ------------------------------------------------------------------------
fetches=0
for df in images/*/Dockerfile; do
    while IFS= read -r run; do
        fetches=$(( fetches + 1 ))
        if [[ "$run" == *"--verified-by"* ]]; then
            # The named check must RUN, so the --verified-by text itself does
            # not count as finding it.
            sed -E "s/--verified-by[[:space:]]+('[^']*'|\"[^\"]*\"|[^[:space:]]+)//" <<< "$run" \
                | grep -qE 'rpm --checksig|gpg --verify|sha256sum -c' \
                || vfail "$df: mdb-fetch --verified-by with no verification command in the same RUN"
        fi
    done < <(folded "$df" | grep -E 'mdb-fetch[[:space:]]')
done
(( fetches >= 7 )) || vfail "found $fetches RUN(s) calling mdb-fetch, expected at least 7"

# The digests must exist for the versions the Dockerfiles pin, so a version
# bump without its digest fails here in seconds instead of in a 40-minute build.
arg_values() { grep -hoE "^ARG $1=[^[:space:]]+" images/*/Dockerfile | sed "s/^[^=]*=//" | sort -u; }
digests=0
need_sum() {
    grep -qE "^$1[[:space:]]+[0-9a-f]{64}$" "$sums" \
        || vfail "$sums has no sha256 for $1"
    digests=$(( digests + 1 ))
}
for v in $(arg_values S6_OVERLAY_VERSION); do
    for a in noarch x86_64 aarch64; do
        need_sum "https://github.com/just-containers/s6-overlay/releases/download/v${v}/s6-overlay-${a}.tar.xz"
    done
done
for v in $(arg_values PG_GRAPHQL_VERSION); do
    while IFS= read -r pg; do
        for a in amd64 arm64; do
            need_sum "https://github.com/supabase/pg_graphql/releases/download/v${v}/pg_graphql-v${v}-pg${pg}-${a}-linux-gnu.deb"
        done
    done < <(awk -F'\t' '$1=="pg"{print $2}' images/bases.tsv)
done
(( digests >= 7 )) || vfail "checked $digests digest(s), expected at least 7"
vinfo "$fetches mdb-fetch call(s); $digests pinned download(s) have a recorded sha256"

# --- 4: mdb-fetch refuses what it should, offline -----------------------------
t="$(mktemp -d "${TMPDIR:-/tmp}/mdb-fetch.XXXXXX")"; add_cleanup "rm -rf '$t'"
printf 'payload\n' > "$t/file.bin"
sum="$(sha256sum "$t/file.bin" 2>/dev/null || shasum -a 256 "$t/file.bin")"
sum="${sum%% *}"
url="file://$t/file.bin"
printf '%s %s\n' "$url" "$sum" > "$t/good.txt"
printf '%s %s\n' "$url" "$(printf '%064d' 0)" > "$t/bad.txt"
: > "$t/none.txt"
fetch() { FETCH_SUMS="$1" sh "$sup/fetch.sh" "${@:2}" >/dev/null 2>&1; }
cases=0
fetch "$t/good.txt" "$url" "$t/o1" || vfail "mdb-fetch refused a file matching its recorded digest"; cases=$(( cases + 1 ))
if fetch "$t/bad.txt" "$url" "$t/o2"; then vfail "mdb-fetch accepted a file whose digest does not match"; fi; cases=$(( cases + 1 ))
[[ ! -e "$t/o2" ]] || vfail "mdb-fetch left a rejected download on disk"
if fetch "$t/none.txt" "$url" "$t/o3"; then vfail "mdb-fetch accepted a file with no recorded digest"; fi; cases=$(( cases + 1 ))
fetch "$t/none.txt" --verified-by 'rpm --checksig' "$url" "$t/o4" \
    || vfail "mdb-fetch refused --verified-by"; cases=$(( cases + 1 ))
(( cases == 4 )) || vfail "ran $cases mdb-fetch case(s), expected 4"
vinfo "mdb-fetch: accepts a matching digest, refuses a wrong or missing one, honours --verified-by"

# --- 5: CI gates image findings where they can have been caused --------------
crons="$(grep -cE "^[[:space:]]+- cron:" .github/workflows/ci.yml || true)"
[[ "$crons" == 1 ]] || vfail "ci.yml has $crons cron schedule(s), expected exactly one weekly"
grep -qE "cron: '[0-9]+ [0-9]+ \* \* [0-6]'" .github/workflows/ci.yml \
    || vfail "ci.yml's schedule is not weekly"
grep -q 'scripts/ci-scan-gate' .github/workflows/ci.yml || vfail "ci.yml does not decide the scan gate with scripts/ci-scan-gate"
# shellcheck disable=SC2016  # the literal text of the workflow step
grep -q -- '--exit-code "$code"' .github/workflows/ci.yml || vfail "ci.yml's trivy step does not take its exit code from the gate"

r="$t/repo"; mkdir -p "$r/images/x" "$r/docs" "$r/scripts"
cp scripts/ci-scan-gate "$r/scripts/"
gate() {
    ( cd "$r" && GITHUB_OUTPUT="" EVENT="$1" BASE_SHA="${2:-}" scripts/ci-scan-gate 2>/dev/null ) \
        | sed -n 's/^gate=//p'
}
(
    cd "$r" && git init -q && git config core.hooksPath /dev/null && git config user.email v@example.invalid && git config user.name verify \
    && printf a > docs/a.md && printf a > images/x/Dockerfile && git add -A && git commit -qm base
) || vfail "could not create the scratch repository"
base="$(git -C "$r" rev-parse HEAD)"
printf b > "$r/docs/a.md"; git -C "$r" commit -qam docs
[[ "$(gate pull_request "$base")" == false ]] || vfail "a docs-only pull request gates the scan"
base2="$(git -C "$r" rev-parse HEAD)"
printf b > "$r/images/x/Dockerfile"; git -C "$r" commit -qam image
[[ "$(gate pull_request "$base2")" == true ]] || vfail "a pull request changing images/ does not gate the scan"
[[ "$(gate schedule)" == true ]] || vfail "the scheduled build does not gate the scan"
vinfo "one weekly schedule; the scan gates on schedule and on pull requests that change image inputs, not on others"
