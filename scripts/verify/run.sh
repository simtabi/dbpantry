#!/usr/bin/env bash
#
# The dbpantry acceptance harness runner.
#
# Discovers every check in this directory, runs it, and reports. A check is a
# NN-slug.sh script carrying three metadata headers:
#
#   # verify: one-line description of what this proves
#   # tags:   space-separated (fast, pg, mysql, mariadb, tls, backup, ha, ...)
#   # phase:  the KIT.md phase that makes this check pass
#
# Usage:
#   run.sh                    run everything
#   run.sh --structure-only   validate the harness itself, run no checks
#   VERIFY_TAGS=fast run.sh   run only checks carrying a matching tag
#   VERIFY_TAGS=pg,backup     several tags (comma or space separated): a check
#                             runs when it carries ANY of them
#   VERIFY_CHECKS=21,28       run only these checks, by number or full name
#                             (make verify CHECKS=21,28 sets this)
#
# Both filters may be combined; a check must then pass both.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
cd "$ROOT" || exit 1

readonly EXIT_SKIP=3
STRUCTURE_ONLY=0
[[ "${1:-}" == "--structure-only" ]] && STRUCTURE_ONLY=1

if [[ -t 1 ]] && [[ -z "${NO_COLOR:-}" ]]; then
    RED=$'\033[31m'; GRN=$'\033[32m'; YLW=$'\033[33m'; BLU=$'\033[34m'
    DIM=$'\033[2m'; BLD=$'\033[1m'; OFF=$'\033[0m'
else
    RED=''; GRN=''; YLW=''; BLU=''; DIM=''; BLD=''; OFF=''
fi

# Is word $1 in the comma/space separated list $2?
in_list() {
    local item
    for item in ${2//,/ }; do
        [[ "$item" == "$1" ]] && return 0
    done
    return 1
}

# Does a check, by its name and tags, pass the VERIFY_TAGS and VERIFY_CHECKS
# filters? Unset filters select everything.
selected() {
    local name="$1" tags="$2" num t hit
    if [[ -n "${VERIFY_CHECKS:-}" ]]; then
        num="${name%%-*}"
        in_list "$num" "$VERIFY_CHECKS" || in_list "$name" "$VERIFY_CHECKS" \
            || in_list "$((10#$num))" "$VERIFY_CHECKS" || return 1
    fi
    if [[ -n "${VERIFY_TAGS:-}" ]]; then
        hit=1
        for t in $tags; do
            in_list "$t" "$VERIFY_TAGS" && { hit=0; break; }
        done
        return "$hit"
    fi
    return 0
}

meta() { grep -m1 -E "^# $1:" "$2" 2>/dev/null | sed -E "s/^# $1:[[:space:]]*//" || true; }

# bash 3.2 has no mapfile, and macOS ships bash 3.2 (SPEC section 1 makes
# macOS first-class), so every array is built with a read loop instead.
CHECKS=()
while IFS= read -r c; do
    [ -n "$c" ] && CHECKS+=("$c")
done < <(find "$HERE" -maxdepth 1 -name '[0-9][0-9]-*.sh' | sort)

if (( ${#CHECKS[@]} == 0 )); then
    printf '%sNo checks found in %s%s\n' "$RED" "$HERE" "$OFF" >&2
    exit 1
fi

# -----------------------------------------------------------------------------
# Structural validation. This is what must be green in phase 1: the harness
# itself is well-formed, even while the features it checks do not exist.
# -----------------------------------------------------------------------------
structural_errors=0
for c in "${CHECKS[@]}"; do
    rel="${c#"$ROOT"/}"
    [[ -x "$c" ]] || { printf '%sstructure%s %s is not executable\n' "$RED" "$OFF" "$rel"; structural_errors=$(( structural_errors + 1 )); }
    [[ -n "$(meta verify "$c")" ]] || { printf '%sstructure%s %s has no "# verify:" header\n' "$RED" "$OFF" "$rel"; structural_errors=$(( structural_errors + 1 )); }
    [[ -n "$(meta phase  "$c")" ]] || { printf '%sstructure%s %s has no "# phase:" header\n'  "$RED" "$OFF" "$rel"; structural_errors=$(( structural_errors + 1 )); }
    [[ -n "$(meta tags   "$c")" ]] || { printf '%sstructure%s %s has no "# tags:" header\n'   "$RED" "$OFF" "$rel"; structural_errors=$(( structural_errors + 1 )); }
    bash -n "$c" 2>/dev/null || { printf '%sstructure%s %s is not valid bash\n' "$RED" "$OFF" "$rel"; structural_errors=$(( structural_errors + 1 )); }
done

if (( STRUCTURE_ONLY )); then
    if (( structural_errors )); then
        printf '\n%s✗ harness structure: %d problem(s)%s\n' "$RED" "$structural_errors" "$OFF"
        exit 1
    fi
    printf '%s✓ harness structure: %d checks, all well-formed%s\n' "$GRN" "${#CHECKS[@]}" "$OFF"
    exit 0
fi

if (( structural_errors )); then
    printf '\n%s✗ refusing to run: harness structure has %d problem(s)%s\n' "$RED" "$structural_errors" "$OFF"
    exit 1
fi

# -----------------------------------------------------------------------------
# Execution
# -----------------------------------------------------------------------------
# Counters use $(( x + 1 )), never (( x++ )).
#
# `(( x++ ))` returns the OLD value as its exit status, so the first increment
# from zero exits 1 -- and under `set -e` that aborts the runner. It survives on
# bash 3.2 and does not on the bash 5 CI runs on, which is why the harness ran
# all 42 checks locally and died after the first passing check in CI, printing
# the check's name and nothing else.
pass=0; fail=0; skip=0; ran=0
failed_names=()
skipped_names=()

# An inventory of the images that exist right now, shared with every check.
#
# A full run takes tens of minutes, and Docker reclaims unused images under
# disk pressure. When that happens mid-run, checks fail with "image not built
# yet (run: make build)" -- which sends you diagnosing a build that worked
# perfectly. Recording what was here at the start lets need_image tell the two
# apart, and lets the summary say so once rather than eight times.
MDB_IMAGE_SNAPSHOT="$(mktemp)"
export MDB_IMAGE_SNAPSHOT
trap 'rm -f "$MDB_IMAGE_SNAPSHOT"' EXIT
docker images --format '{{.Repository}}:{{.Tag}}' > "$MDB_IMAGE_SNAPSHOT" 2>/dev/null || true

printf '%s%s dbpantry verify %s%s\n' "$BLD" "$BLU" "$(date '+%H:%M:%S')" "$OFF"
[[ -n "${VERIFY_TAGS:-}" ]] && printf '%sfiltering by tag: %s%s\n' "$DIM" "$VERIFY_TAGS" "$OFF"
[[ -n "${VERIFY_CHECKS:-}" ]] && printf '%sfiltering by check: %s%s\n' "$DIM" "$VERIFY_CHECKS" "$OFF"
printf '\n'

for c in "${CHECKS[@]}"; do
    name="$(basename "$c" .sh)"
    desc="$(meta verify "$c")"
    tags="$(meta tags "$c")"
    phase="$(meta phase "$c")"

    selected "$name" "$tags" || continue
    ran=$(( ran + 1 ))

    printf '%s→%s %-34s %s%s%s\n' "$BLU" "$OFF" "$name" "$DIM" "$desc" "$OFF"

    rc=0
    output="$("$c" 2>&1)" || rc=$?

    case "$rc" in
        0)
            pass=$(( pass + 1 ))
            printf '  %s✓ pass%s\n' "$GRN" "$OFF"
            [[ -n "$output" ]] && printf '%s\n' "$output"
            ;;
        "$EXIT_SKIP")
            skip=$(( skip + 1 )); skipped_names+=("$name")
            printf '  %s· skip%s\n' "$YLW" "$OFF"
            [[ -n "$output" ]] && printf '%s\n' "$output"
            ;;
        *)
            fail=$(( fail + 1 )); failed_names+=("$name (phase $phase)")
            printf '  %s✗ fail%s %s(expected until phase %s)%s\n' "$RED" "$OFF" "$DIM" "$phase" "$OFF"
            [[ -n "$output" ]] && printf '%s\n' "$output"
            ;;
    esac
done

# A filter that matches nothing is a typo, not a green run.
if (( ran == 0 )); then
    printf '%s✗ no check matched VERIFY_TAGS=%s VERIFY_CHECKS=%s%s\n' \
        "$RED" "${VERIFY_TAGS:-}" "${VERIFY_CHECKS:-}" "$OFF" >&2
    exit 1
fi

# -----------------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------------
printf '\n%s%s summary %s\n' "$BLD" "$BLU" "$OFF"
printf '  %s%d passed%s   %s%d failed%s   %s%d skipped%s\n' \
    "$GRN" "$pass" "$OFF" "$RED" "$fail" "$OFF" "$YLW" "$skip" "$OFF"

if (( skip )); then
    printf '\n%sskipped:%s\n' "$YLW" "$OFF"
    for n in "${skipped_names[@]}"; do printf '  · %s\n' "$n"; done
fi

if (( fail )); then
    printf '\n%sfailed:%s\n' "$RED" "$OFF"
    for n in "${failed_names[@]}"; do printf '  ✗ %s\n' "$n"; done
    # Say it once. Eight checks each reporting a missing image reads as eight
    # problems; it is one, and it is not a problem with the code.
    if [[ -s "$MDB_IMAGE_SNAPSHOT" ]]; then
        reclaimed=0
        while IFS= read -r img; do
            [[ -n "$img" ]] || continue
            docker image inspect "$img" >/dev/null 2>&1 || reclaimed=$(( reclaimed + 1 ))
        done < "$MDB_IMAGE_SNAPSHOT"
        if (( reclaimed > 0 )); then
            printf '\n%s%d image(s) that existed when this run started are gone.%s\n' \
                "$YLW" "$reclaimed" "$OFF"
            printf '%sDocker reclaims unused images under disk pressure. Failures above that\n' "$DIM"
            printf 'say "image not built yet" are that, not defects -- free disk space and\n'
            printf 're-run.%s\n' "$OFF"
        fi
    fi

    printf '\n%sA failing check is the point: fix the root cause, never the check.%s\n' "$DIM" "$OFF"
    exit 1
fi

printf '\n%s✓ all checks green%s\n' "$GRN" "$OFF"
