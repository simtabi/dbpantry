#!/usr/bin/env bash
# verify: make new-project provisions an isolated project and prints usable creds
# tags: provisioning security
# phase: 5

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_docker
cd "$MDB_ROOT" || exit 1

# SPEC section 18: "Two projects per engine, isolated roles, cross-access
# denied." Check 13 proves that for the env-triplet path used at first init.
# This proves it for `make new-project`, which is a DIFFERENT code path and the
# one humans actually use day to day -- the guarantee has to hold on both or it
# does not hold.

need_file "$MDB_ROOT/scripts/new-project"

a="mdbchk_alpha"
b="mdbchk_beta"

add_cleanup "docker compose exec -T pg psql -U postgres -q -c 'DROP DATABASE IF EXISTS $a;' >/dev/null 2>&1"
add_cleanup "docker compose exec -T pg psql -U postgres -q -c 'DROP DATABASE IF EXISTS $b;' >/dev/null 2>&1"
add_cleanup "docker compose exec -T pg psql -U postgres -q -c 'DROP ROLE IF EXISTS ${a}_user;' >/dev/null 2>&1"
add_cleanup "docker compose exec -T pg psql -U postgres -q -c 'DROP ROLE IF EXISTS ${a}_user_readonly;' >/dev/null 2>&1"
add_cleanup "docker compose exec -T pg psql -U postgres -q -c 'DROP ROLE IF EXISTS ${b}_user;' >/dev/null 2>&1"
add_cleanup "docker compose exec -T pg psql -U postgres -q -c 'DROP ROLE IF EXISTS ${b}_user_readonly;' >/dev/null 2>&1"
# new-project writes a readonly companion secret beside each app secret; both
# go, or every run leaks two more files into secrets/.
add_cleanup "rm -f secrets/pg_${a}_user_password.txt secrets/pg_${a}_user_readonly_password.txt secrets/pg_${b}_user_password.txt secrets/pg_${b}_user_readonly_password.txt"
add_cleanup "make down"

mdb_up PROFILES=pg || vfail "make up failed"
# shellcheck disable=SC2016  # evaluated by the subshell, not here
wait_for 120 "pg to report healthy" bash -c \
    'docker compose ps --format "{{.Service}} {{.Health}}" | grep -q "^pg healthy$"'

out="$(make new-project NAME="$a" 2>&1)" || { printf '%s\n' "$out" >&2; vfail "make new-project failed"; }
make new-project NAME="$b" >/dev/null 2>&1 || vfail "make new-project failed for the second project"
vinfo "provisioned two projects"

# The printed block has to be usable, not merely printed. A paste-ready block
# that omits the database or the user is worse than no block at all.
for field in DB_CONNECTION DB_HOST DB_PORT DB_DATABASE DB_USERNAME DB_PASSWORD; do
    printf '%s' "$out" | grep -q "^${field}=" \
        || vfail "the printed Laravel block is missing $field"
done
printf '%s' "$out" | grep -q "^DB_DATABASE=${a}$" \
    || vfail "the printed block names the wrong database"
vinfo "printed a complete, paste-ready Laravel block"

pw="$(cat "secrets/pg_${a}_user_password.txt")"

# The credentials must actually work.
docker compose exec -T -e PGPASSWORD="$pw" pg \
    psql -U "${a}_user" -d "$a" -tAc "SELECT 1" >/dev/null 2>&1 \
    || vfail "the printed credentials cannot connect to their own database"
vinfo "credentials connect to their own database"

# ...and must not reach the other project.
if docker compose exec -T -e PGPASSWORD="$pw" pg \
    psql -U "${a}_user" -d "$b" -tAc "SELECT 1" >/dev/null 2>&1; then
    vfail "${a}_user reached database $b; new-project does not isolate"
fi
vinfo "denied on the other project's database"

# No app role may hold superuser (SPEC section 9).
super="$(docker compose exec -T pg psql -U postgres -tAc \
    "SELECT rolsuper FROM pg_roles WHERE rolname='${a}_user'" 2>/dev/null | tr -d ' \r')"
[[ "$super" == "f" ]] || vfail "${a}_user holds superuser"
vinfo "app role holds no superuser"

# The readonly companion exists (SPEC section 8).
ro="$(docker compose exec -T pg psql -U postgres -tAc \
    "SELECT 1 FROM pg_roles WHERE rolname='${a}_user_readonly'" 2>/dev/null | tr -d ' \r')"
[[ "$ro" == "1" ]] || vfail "no readonly companion role was created"
vinfo "readonly companion role created"
