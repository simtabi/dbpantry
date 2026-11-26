#!/usr/bin/env bash
# verify: every file the binary keeps in the instance home is gitignored, so a checkout used as the home cannot commit one
# tags: fast structure security
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# A checkout is its own instance home by default (design.md D-84). The
# dashboard writes api-tokens.json (token hashes), webhooks.json (each
# webhook's HMAC signing secret), dashboard.json and audit/ there, and none
# was ignored: `git add -A` would have committed them (found 2026-11-25, when
# a dashboard run left webhooks.json and audit/ untracked in the checkout).
# The names are read from the Go source, every filepath.Join(home, "NAME"),
# so a new file is covered the day it is added.

names=()
while IFS= read -r n; do names+=("$n"); done < <(
    find src/internal -name '*.go' ! -name '*_test.go' -print0 \
    | xargs -0 grep -hoE 'Join\([a-zA-Z.]*(home|Home)[a-zA-Z.()]*, "[^"/]+"' \
    | sed -E 's/.*, "([^"]+)"$/\1/' | sort -u)
(( ${#names[@]} >= 6 )) || vfail "found only ${#names[@]} file names joined to the home in src/internal"

# Names that are not instance state in a checkout: the home's own parents
# (.config, .local, .orbstack under the user's home) and the kit, which is
# extracted only into a home that is not a checkout.
bad=0 n=0
for name in "${names[@]}"; do
    case "$name" in .config|.local|.orbstack|kit) continue ;; esac
    n=$(( n + 1 ))
    probe="$name"
    [[ -d "$name" || "$name" != *.* ]] && probe="$name/x"
    if ! git check-ignore -q "$probe"; then
        printf '      %s is written into the instance home and is not gitignored\n' "$name" >&2
        bad=$(( bad + 1 ))
    fi
done
(( n >= 6 )) || vfail "only $n instance files left to inspect"
(( bad == 0 )) || vfail "$bad of $n instance files could be committed from a checkout used as its home"
vinfo "all $n instance files the binary writes into the home are gitignored"
