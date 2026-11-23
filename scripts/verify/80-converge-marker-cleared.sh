#!/usr/bin/env bash
# verify: every convergence stage clears a stale converge-failed marker before it can write a new one, so a restart after a failed convergence comes back healthy
# tags: fast structure pg mysql mariadb
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Escaped bug (design.md D-109): a convergence stage that times out writes
# /run/mdb/converge-failed, and each engine's HEALTHCHECK refuses while the
# file exists. Nothing removed it, and /run lives in the container's own
# filesystem, so it survives `docker restart`: after one failed convergence
# (a full disk, 2026-11-18) mdb-fixture-mysql-1 stayed unhealthy with mysqld
# answering pings and the next convergence logging success. This check needs
# no engine: every script that writes the marker must remove it on a line
# above the first write.
cd "$MDB_ROOT" || vfail "no repository root"

marker=/run/mdb/converge-failed
mapfile -t writers < <(grep -rlF -- "> $marker" images | sort)
(( ${#writers[@]} >= 2 )) || vfail "expected at least 2 scripts writing $marker (mysql-family, pg), found ${#writers[@]}: ${writers[*]}"

# Every healthcheck that reads the marker needs a writer in the same family,
# or the marker test guards nothing.
mapfile -t readers < <(grep -rlF -- "test ! -f $marker" images | sort)
(( ${#readers[@]} >= 3 )) || vfail "expected at least 3 healthchecks reading $marker (mysql, mariadb, pg), found ${#readers[@]}"

bad=()
for f in "${writers[@]}"; do
    first_write="$(grep -nF -- "> $marker" "$f" | head -1 | cut -d: -f1)"
    first_clear="$(grep -nE -- "rm -f [\"']?$marker" "$f" | head -1 | cut -d: -f1 || true)"
    if [[ -z "$first_clear" || "$first_clear" -ge "$first_write" ]]; then
        bad+=("$f (writes at line $first_write, clears at line ${first_clear:-never})")
    fi
done
(( ${#bad[@]} == 0 )) || vfail "a convergence stage writes $marker without first clearing a stale one:
$(printf '       %s\n' "${bad[@]}")"
vinfo "${#writers[@]} of ${#writers[@]} marker writers clear it first; ${#readers[@]} healthchecks read it"
