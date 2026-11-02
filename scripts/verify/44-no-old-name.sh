#!/usr/bin/env bash
# verify: no tracked file uses an old product name outside its history
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# The project was db-toolkit-docker, then multidb-server, and is now dbpantry
# (design.md D-54). A rename is finished only when nothing new says the old
# name, and a grep is the only reviewer that reads every file every time.
#
# Two kinds of old-name text are correct and stay:
#
# - History. These files record what was true when they were written, and
#   rewriting them would falsify the record.
# - Strings already written into users' data. The PostgreSQL include marker and
#   the Patroni namespace are matched against existing volumes; changing either
#   breaks every installation made before the rename.
#
# Each exemption below is one of those, and nothing else belongs here. The
# MDB_ variables and the mdb Compose project are not old names: they were kept
# on purpose and never matched this pattern.

cd "$MDB_ROOT" || exit 1

pattern='multidb[-_]server|db-toolkit|DBTK_'

history_files="changelog.md design.md docs/reference/spec.md docs/reference/kit.md docs/archive/KICKOFF-PROMPT.txt
               scripts/verify/44-no-old-name.sh"

# Lines that must keep the old name, matched as fixed strings.
kept_lines='# multidb-server include
/multidb-server/
was called multidb-server
before that db-toolkit-docker'

command -v git >/dev/null 2>&1 || vfail "git is required to list tracked files"
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || vfail "not a git checkout; this check reads tracked files"

offenders=""
scanned=0

while IFS= read -r f; do
    [[ -f "$f" ]] || continue
    skip=0
    for h in $history_files; do
        [[ "$f" == "$h" ]] && { skip=1; break; }
    done
    (( skip )) && continue
    scanned=$(( scanned + 1 ))

    while IFS= read -r hit; do
        [[ -n "$hit" ]] || continue
        allowed=0
        while IFS= read -r k; do
            [[ -n "$k" ]] || continue
            case "$hit" in *"$k"*) allowed=1; break ;; esac
        done <<< "$kept_lines"
        (( allowed )) || offenders+="        $f:$hit"$'\n'
    done < <(grep -n -I -E "$pattern" "$f" 2>/dev/null || true)
done < <(git ls-files)

(( scanned > 0 )) || vfail "no tracked files found; this check would pass vacuously"

if [[ -n "$offenders" ]]; then
    printf '%s' "$offenders" >&2
    vfail "an old product name is still in use. Say dbpantry (images: dbpantry-<engine>),
       or, if the line is history or a string already written into user data,
       add it to this check with the reason (design.md D-54)."
fi

vinfo "$scanned tracked file(s): no old product name outside history"
