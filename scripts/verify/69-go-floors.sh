#!/usr/bin/env bash
# verify: Go dependency floors live in one file every image reads, never inline in a Dockerfile, and the floor comparison follows semver
# tags: fast structure supply-chain
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# design.md D-70. CVE-2026-78661 in golang.org/x/net failed the pg, mysql and
# mariadb builds on every pull request at once, because the previous fix --
# x/net@v0.60.0 -- was written inline after each build-go-tool call in four
# Dockerfiles. A floor that has to be repeated per image is one that gets
# missed. So the floors live in images/_shared/gotools/floors.txt and this
# check keeps them there:
#
#   1. floors.txt is well formed, and every floor names the advisory it closes;
#   2. every Dockerfile that installs build-go-tool installs floors.txt too;
#   3. no Dockerfile passes a module@version to build-go-tool;
#   4. the comparison build-go-tool uses never downgrades, including the case
#      sort -V gets wrong (a release against its own pre-release).

cd "$MDB_ROOT" || vfail "no repository root"

floors=images/_shared/gotools/floors.txt
helper=images/_shared/gotools/build.sh
need_file "$floors"
need_file "$helper"

# --- 1. the floors file ------------------------------------------------------
n=0
while read -r module floor reason; do
    case "$module" in ''|'#'*) continue ;; esac
    n=$(( n + 1 ))
    [[ "$module" =~ ^[a-z0-9.-]+\.[a-z]+/[A-Za-z0-9._/-]+$ ]] \
        || vfail "$floors: '$module' is not a module path"
    [[ "$floor" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] \
        || vfail "$floors: $module has floor '$floor', which is not a semantic version"
    [[ "$reason" =~ (CVE-[0-9]{4}-[0-9]+|GHSA-) ]] \
        || vfail "$floors: $module $floor names no advisory; a floor without a reason cannot be retired"
done < "$floors"
(( n >= 1 )) || vfail "$floors lists no floors; the parser read nothing"
vinfo "$floors: $n floor(s), each naming its advisory"

# --- 2 and 3. every Dockerfile ------------------------------------------------
dockerfiles=0; installers=0
for df in images/*/Dockerfile; do
    dockerfiles=$(( dockerfiles + 1 ))
    grep -q '_shared/gotools/build.sh' "$df" || continue
    installers=$(( installers + 1 ))
    grep -q '_shared/gotools/floors.txt' "$df" \
        || vfail "$df installs build-go-tool but not floors.txt, so its tools build without the floors"
    # Join continuation lines so a module argument on its own line is still
    # seen as part of the build-go-tool call it belongs to.
    if sed -e ':a' -e '/\\$/N; s/\\\n//; ta' "$df" | grep -E 'build-go-tool' \
         | grep -qE '[[:space:]][a-z0-9.-]+\.[a-z]+/[^[:space:]]+@v[0-9]'; then
        vfail "$df passes a module@version to build-go-tool; put the floor in $floors instead"
    fi
done
(( dockerfiles >= 8 )) || vfail "inspected $dockerfiles Dockerfile(s), expected at least 8; the glob is wrong"
(( installers >= 5 )) || vfail "only $installers Dockerfile(s) install build-go-tool, expected at least 5"
vinfo "$dockerfiles Dockerfile(s) inspected; $installers install build-go-tool with floors.txt and pass no inline module"

# --- 4. the comparison --------------------------------------------------------
fn="$(sed -n '/^ver_lt()/,/^}/p' "$helper")"
[[ -n "$fn" ]] || vfail "$helper defines no ver_lt"
cases=0
expect() {
    local want="$1" a="$2" b="$3" got
    cases=$(( cases + 1 ))
    if sh -c "$fn"'
ver_lt "$1" "$2"' sh "$a" "$b"; then got=lt; else got=ge; fi
    [[ "$got" == "$want" ]] || vfail "ver_lt $a $b gave $got, expected $want"
}
expect lt v0.57.0 v0.62.0
expect ge v0.62.0 v0.62.0
expect ge v0.63.0 v0.62.0
expect lt v0.9.0  v0.10.0
expect ge v1.84.0 v1.84.0-dev.0.20260825144003-d5a41119e0e3
expect lt v1.84.0-dev.0.20250101000000-aaaaaaaaaaaa v1.84.0-dev.0.20260825144003-d5a41119e0e3
expect lt v1.83.9 v1.84.0-dev.0.20260825144003-d5a41119e0e3
(( cases == 7 )) || vfail "ran $cases comparison case(s), expected 7"
vinfo "ver_lt agrees with semver on $cases cases, including release against pre-release"
