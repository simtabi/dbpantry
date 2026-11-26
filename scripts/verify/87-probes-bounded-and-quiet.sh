#!/usr/bin/env bash
# verify: every psql probe in the harness has a connect timeout, and no operator secret is put on a command line
# tags: fast structure security
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# The full make verify of 2026-11-25 hung for 40 minutes in check 27: after the
# leader was killed, a psql probe through HAProxy connected to a backend that
# would never answer, and libpq waits for ever without connect_timeout. The
# same probe passed the operator's PostgreSQL superuser password through -e
# with its value written out, which any process can read in the process list.
# A conninfo names connect_timeout; a secret from secrets/ reaches a container
# as `-e NAME` with the value in the environment, never on the command line.

files=(scripts/verify/*.sh)
(( ${#files[@]} >= 80 )) || vfail "found only ${#files[@]} checks to inspect"

# psql conninfo strings: "... host=... port=..." handed to psql, wherever
# connect_timeout sits in them.
n=0 bad=0
while IFS= read -r hit; do
    n=$(( n + 1 ))
    if [[ "$hit" != *connect_timeout=* ]]; then
        printf '      %s\n' "${hit:0:160}" >&2
        bad=$(( bad + 1 ))
    fi
done < <(grep -nE '"[^"]*host=[^"]*port=' "${files[@]}" | grep -v '^scripts/verify/87-')
(( n >= 6 )) || vfail "found only $n psql conninfo strings; the scan inspected too little"
(( bad == 0 )) || vfail "$bad of $n psql conninfo strings have no connect_timeout, so a probe can hang the run"
vinfo "all $n psql conninfo strings carry a connect_timeout"

# A variable filled from the operator's secrets/ must not reach a command line.
# shellcheck disable=SC2016  # a literal $( and $MDB_ROOT in the pattern
secret_read='([a-z_]+)="\$\(tr -d .\\n. < "\$MDB_ROOT/secrets/'
leaks=0 checked=0
for f in "${files[@]}"; do
    [[ "$f" == scripts/verify/87-* ]] && continue
    while IFS= read -r var; do
        [[ -n "$var" ]] || continue
        checked=$(( checked + 1 ))
        if grep -nE -- "-e [A-Z_]+=\"?\\\$\\{?${var}\\b" "$f" >&2; then
            printf '      %s: %s, read from secrets/, is passed on a command line\n' "$f" "$var" >&2
            leaks=$(( leaks + 1 ))
        fi
    done < <(grep -oE "$secret_read" "$f" | sed -E 's/=.*//')
done
(( checked >= 1 )) || vfail "found no variable read from the operator's secrets/ to inspect"
(( leaks == 0 )) || vfail "$leaks operator secret(s) reach a command line, visible in the process list"
vinfo "no operator secret ($checked inspected) is put on a command line"
