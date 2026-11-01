#!/usr/bin/env bash
# verify: repository layout matches SPEC.md section 15
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

for d in images/pg images/mysql images/mariadb images/cli \
         overrides/pg overrides/mysql overrides/mariadb \
         config/caddy config/haproxy config/proxysql \
         scripts scripts/verify rls docs .github/workflows; do
    need_dir "$MDB_ROOT/$d"
done

for f in Makefile .env.example .gitignore .gitattributes .editorconfig \
         README.md CONTRIBUTING.md SECURITY.md CODE_OF_CONDUCT.md CHANGELOG.md LICENSE \
         CLAUDE.md DESIGN.md docs/reference/SPEC.md docs/reference/KIT.md; do
    need_file "$MDB_ROOT/$f"
done

# SPEC section 15 requires named volumes for data, never bind mounts: WSL2
# performance and file permissions both depend on it.
if [[ -f docker-compose.yml ]]; then
    if grep -nE '^\s*-\s*\./.*:/var/lib/(postgresql|mysql)' docker-compose.yml; then
        vfail "data is bind-mounted; SPEC section 15 requires named volumes"
    fi
fi

# The org convention forbids a second README acting as a docs index.
if [[ -f docs/README.md ]]; then
    vfail "docs/README.md exists; the docs index is README.md's Documentation section"
fi

# Engine services live in the GENERATED compose.engines.yml, which reaches a
# bare `docker compose` through COMPOSE_FILE in .env. Passing any -f REPLACES
# that variable rather than adding to it, so an invocation that lists overlays
# and forgets the generated file has no engines at all.
#
# The failure is not obvious from the message compose prints -- it reports
# `service "pg" has neither an image nor a build context`, which reads like a
# broken overlay rather than a missing file. `make test-profile` shipped this
# way and the harness saw only "make test-profile failed".
#
# Matched on an actual invocation -- `docker compose` or the Makefile's
# $(COMPOSE) -- rather than on the filename alone, which also appears in
# ordinary `[[ -f docker-compose.yml ]]` guards. This file is skipped because
# it necessarily contains the pattern it searches for.
offenders=""
while IFS= read -r hit; do
    [[ -z "$hit" ]] && continue
    case "$hit" in
        *"01-repo-structure.sh"*) continue ;;
        *compose.engines.yml*) continue ;;
    esac
    # The literals are matched, not expanded -- COMPOSE is the Makefile's
    # variable, not this script's.
    # shellcheck disable=SC2016
    case "$hit" in
        *'docker compose'*|*'$(COMPOSE)'*|*'${COMPOSE}'*) ;;
        *) continue ;;
    esac
    offenders+="        ${hit}"$'\n'
done < <(grep -rn -- '-f docker-compose.yml' "$MDB_ROOT/Makefile" "$MDB_ROOT/scripts" 2>/dev/null || true)

if [[ -n "$offenders" ]]; then
    printf '%s' "$offenders" >&2
    vfail "a compose invocation passes -f without compose.engines.yml; engine services would be undefined"
fi
vinfo "every explicit compose invocation includes the generated engines file"

# -----------------------------------------------------------------------------
# Every engine's overrides/ directory must be TRACKED, not merely present
# -----------------------------------------------------------------------------
# The generated compose file bind-mounts overrides/<engine>/ for each engine, and
# git does not track directories -- only files. overrides/mongodb/ had no
# .gitkeep where the other five did, so it existed on every machine that had
# ever run the generator and on none that had only cloned. A fresh checkout got
# an invalid compose file, `make up` failed, and the harness reported ten
# failures whose messages pointed at pgAdmin returning 502 and images not being
# built -- none of them at the missing directory.
#
# So this asserts what git knows, not what the filesystem has. Checking for the
# directory would pass here for exactly the reason the bug survived: it is
# sitting right there, untracked.
# engine_list, not `ls engines/` -- that directory also holds _family (the
# shared hook scripts) and alias entries like postgres, neither of which is an
# engine with an overrides/ mount.
# shellcheck source=../engine-lib.sh
. "$MDB_ROOT/scripts/engine-lib.sh"

missing_keep=""
while IFS= read -r engine; do
    [[ -z "$engine" ]] && continue
    engine_load "$engine" || continue
    git -C "$MDB_ROOT" ls-files --error-unmatch "overrides/$MDB_ENGINE_NAME/.gitkeep" >/dev/null 2>&1 \
        || missing_keep+="$MDB_ENGINE_NAME "
done < <(engine_list)

if [[ -n "$missing_keep" ]]; then
    vfail "overrides/ is untracked for: ${missing_keep% }
       git stores no empty directories, so a fresh clone will not have them and
       the generated compose file mounts a path that does not exist. Add
       overrides/<engine>/.gitkeep for each."
fi
vinfo "every engine's overrides/ directory is tracked"

vinfo "layout matches SPEC section 15"
