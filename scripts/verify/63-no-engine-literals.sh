#!/usr/bin/env bash
# verify: no engine list is written out in the files phase 1 generates from the manifest, and the Go module names no engine
# tags: fast structure engines
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# Master prompt R6 and design.md D-68: engines are declared in engines/, so a
# list of engine names anywhere else is a second list, and a new engine
# silently misses it. Kubernetes shipped that way: scripts/helm-values and
# the backend each listed five engines by hand (refactor plan F1). Phase 1
# derives those lists from the descriptors; this keeps them derived.
#
# Scope is what phase 1 owns, not the whole tree. Other scripts still branch
# on engine names (refactor plan §14.2) and move in later phases, which widen
# this list as they land.
#   - shell and workflow files: no line of code lists two or more engine
#     names in a row ("pg mysql mariadb", "pg,mysql").
#   - the Go module (non-test files): no string literal is an engine name.
SHELL_FILES="
scripts/ci-matrix
scripts/helm-values
scripts/backends/kubernetes.sh
scripts/profiles
scripts/gen-compose
scripts/engine-lib.sh
.github/workflows/ci.yml
.github/workflows/lint.yml
"

# file|fixed text on the line|why it may stay. Each entry must still match a
# line, so a stale one fails instead of quietly covering something new.
# Measured 2026-11-04 (design.md D-68): this is the only list left in scope.
EXEMPT='scripts/backends/kubernetes.sh|split("pg mysql mariadb"|the backup claims follow the Compose backup sidecars (backup-pg, backup-mysql, backup-mariadb in docker-compose.yml), which no descriptor declares yet; the data context (refactor plan phase 6) moves them'

names="$(engine_list)"
[[ -n "$names" ]] || vfail "engine_list found no descriptors"
alt="$(printf '%s\n' "$names" | paste -sd'|' -)"
list_re="(^|[^A-Za-z0-9_-])($alt)([ ,]+($alt))+([^A-Za-z0-9_-]|\$)"

problems=""
inspected=0
for f in $SHELL_FILES; do
    [[ -f "$f" ]] || vfail "$f is in this check's scope but does not exist; update the list"
    inspected=$(( inspected + 1 ))
    while IFS= read -r hit; do
        [[ -n "$hit" ]] || continue
        lineno="${hit%%:*}"; text="${hit#*:}"
        # Comments explain; only code is a list.
        [[ "$text" =~ ^[[:space:]]*# ]] && continue
        exempt=0
        while IFS='|' read -r ef etext _why; do
            [[ "$ef" == "$f" && "$text" == *"$etext"* ]] && exempt=1
        done <<<"$EXEMPT"
        (( exempt )) && continue
        problems="${problems}
       $f:$lineno: ${text#"${text%%[![:space:]]*}"}"
    done < <(grep -nE "$list_re" "$f" || true)
done

# Every exemption still matches, or it is stale.
while IFS='|' read -r ef etext _why; do
    [[ -n "$ef" ]] || continue
    grep -qF "$etext" "$ef" 2>/dev/null \
        || vfail "stale exemption: '$etext' no longer appears in $ef; remove it from this check"
done <<<"$EXEMPT"

gofiles=0
while IFS= read -r g; do
    gofiles=$(( gofiles + 1 ))
    while IFS= read -r hit; do
        [[ -n "$hit" ]] || continue
        problems="${problems}
       $g:${hit%%:*}: ${hit#*:}"
    done < <(grep -nE "\"($alt)\"" "$g" || true)
done < <(find src -name '*.go' ! -name '*_test.go' | sort)

# Assert the size of what was inspected, so a scope that stops matching fails.
(( inspected >= 8 )) || vfail "inspected only $inspected shell and workflow files"
(( gofiles >= 10 )) || vfail "inspected only $gofiles Go files under src/; the module is larger than that"

[[ -z "$problems" ]] || vfail "engine names written out instead of read from engines/ (R6):$problems
       Derive the list from the descriptors (engine_list and engine_implemented in
       scripts/engine-lib.sh, or the manifest in Go)."

vinfo "$inspected shell and workflow files and $gofiles Go files: no engine list outside engines/ ($(printf '%s\n' "$names" | wc -l | tr -d ' ') engines checked)"
