#!/usr/bin/env bash
# verify: checks clean up the volumes they create, count only their own project, and wait adaptively
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Guards for the verify suite itself, from defects found in real runs:
#
#   - check 20 created a socket volume per engine and removed it inline while
#     its container still held it, so the rm failed silently and every run
#     leaked two volumes. Every `volume create "$v"` must be tracked instead.
#   - check 15 counted every volume named mdb_*, so under an isolated project it
#     reported the operator's real volumes as having "survived make down".
#   - check 37 waited a flat 180s for pgBackRest's first full backup, which a
#     loaded machine overran while the backup was still visibly running.
#     wait_ready_while keeps waiting while a "still working" probe succeeds.

cd "$MDB_ROOT/scripts/verify" || vfail "no scripts/verify"

for f in [0-9][0-9]-*.sh; do
    [[ "$f" == "$(basename "${BASH_SOURCE[0]}")" ]] && continue   # its own patterns
    while IFS= read -r v; do
        grep -qF "track_volume \"$v\"" "$f" \
            || vfail "$f creates volume $v but never passes it to track_volume, so a failure (or an rm while in use) leaks it"
    done < <(sed -nE 's/.*volume create "(\$[A-Za-z_]+)".*/\1/p' "$f")
    if grep -nE "grep -c '\^mdb_'" "$f" >/dev/null; then
        vfail "$f counts every mdb_ volume on the machine; count the project's own (mdb_project_volumes)"
    fi
done
vinfo "every created volume is tracked; no check counts other projects' volumes"

declare -F wait_ready_while >/dev/null || vfail "lib.sh has no wait_ready_while"
t="$(mktemp -d "${TMPDIR:-/tmp}/mdb-wait.XXXXXX")"; add_cleanup "rm -rf '$t'"
# Ready only after 5s. With a 2s budget and a busy probe it must still pass...
ready_at=$(( $(date +%s) + 5 ))
( wait_ready_while 2 30 "the slow thing" true -- bash -c "(( \$(date +%s) >= $ready_at ))" ) >/dev/null 2>&1 \
    || vfail "wait_ready_while gave up although the busy probe said work was still in progress"
# ...and without one it must give up at the budget, not the ceiling.
start=$SECONDS
if ( wait_ready_while 2 30 "the idle thing" false -- false ) >/dev/null 2>&1; then
    vfail "wait_ready_while passed a condition that never held"
fi
(( SECONDS - start < 10 )) || vfail "wait_ready_while waited for the ceiling although nothing was busy"
vinfo "wait_ready_while extends past its budget only while work is in progress"
