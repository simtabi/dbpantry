#!/usr/bin/env bash
# verify: build/ holds only targets.txt, nfpm.yaml, checksums.txt, archives/ and one flat folder per OS
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# The org's build layout (decided 2026-11-04, design.md D-68): every compiled
# file lives in build/<os>/ with a self-describing name, the OS folder is the
# OS token (macos, never darwin), and nothing else sits in build/: no dist/,
# no per-target subfolders, no tool intermediates. `make dbpantry` writes
# build/<os>/dbpantry. A layout nobody checks drifts one stray file at a time.

# The folders build/ may hold: GOOS values, with darwin spelled macos.
OS_DIRS="macos linux windows freebsd openbsd netbsd dragonfly solaris illumos aix android ios plan9 js wasip1"

# layout_problems DIR: print one line per entry that breaks the layout.
layout_problems() {
    local dir="$1" entry name sub
    [[ -d "$dir" ]] || return 0
    while IFS= read -r entry; do
        name="${entry##*/}"
        case "$name" in
            targets.txt|nfpm.yaml|checksums.txt)
                [[ -f "$entry" ]] || printf '%s: expected a file\n' "$name"
                continue ;;
            archives)
                [[ -d "$entry" ]] || printf '%s: expected a directory\n' "$name"
                continue ;;
        esac
        if [[ -d "$entry" && " $OS_DIRS " == *" $name "* ]]; then
            while IFS= read -r sub; do
                printf '%s/%s: an OS folder holds files only, not subfolders\n' "$name" "${sub##*/}"
            done < <(find "$entry" -mindepth 1 -maxdepth 1 -type d | sort)
            continue
        fi
        case "$name" in
            darwin) printf '%s: the OS folder is macos, not darwin\n' "$name" ;;
            dist)   printf '%s: no build/dist/; binaries go in build/<os>/\n' "$name" ;;
            *)      printf '%s: not part of the build layout\n' "$name" ;;
        esac
    done < <(find "$dir" -mindepth 1 -maxdepth 1 | sort)
}

# Self-test first, so the scan below is known to see a breach when there is one.
work="$(mktemp -d "${TMPDIR:-/tmp}/mdb-verify-build.XXXXXX")" || vfail "mktemp failed"
add_cleanup "rm -rf '$work'"
mkdir -p "$work/good/linux" "$work/good/macos" "$work/good/archives"
: > "$work/good/targets.txt"; : > "$work/good/checksums.txt"
: > "$work/good/linux/dbpantry_linux_amd64"; : > "$work/good/macos/dbpantry"
[[ -z "$(layout_problems "$work/good")" ]] \
    || vfail "self-test: a conforming layout was refused: $(layout_problems "$work/good")"
for bad in stray-file dist darwin nested; do
    rm -rf "$work/bad"; cp -R "$work/good" "$work/bad"
    case "$bad" in
        stray-file) : > "$work/bad/dbpantry" ;;
        dist)       mkdir -p "$work/bad/dist" ;;
        darwin)     mkdir -p "$work/bad/darwin" ;;
        nested)     mkdir -p "$work/bad/linux/dbpantry_linux_amd64.d" ;;
    esac
    [[ -n "$(layout_problems "$work/bad")" ]] || vfail "self-test: a layout with a $bad defect was accepted"
done
vinfo "self-test: a conforming build/ passes; a stray file, dist/, darwin/ and a nested folder are each refused"

if [[ ! -d build ]]; then
    vinfo "no build/ directory: nothing built here yet"
    exit 0
fi
n="$(find build -mindepth 1 | wc -l | tr -d ' ')"
problems="$(layout_problems build)"
[[ -z "$problems" ]] || vfail "build/ breaks the layout (one folder per OS, nothing else):
$(printf '%s\n' "$problems" | sed 's/^/       build\//')
       Move binaries into build/<os>/ (make dbpantry writes build/<os>/dbpantry)."
vinfo "build/: $n entries, all in the layout"
