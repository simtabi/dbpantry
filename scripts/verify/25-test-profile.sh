#!/usr/bin/env bash
# verify: the test profile runs on tmpfs with durability deliberately off
# tags: test-profile
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime
cd "$MDB_ROOT" || exit 1

# SPEC section 7: the test profile trades durability for speed, and "data loss
# on stop is the point". This check asserts the trade was actually made -- a
# test profile that quietly kept fsync=on would be slow for no reason, and one
# that persisted data would leak state between test runs.

need_file "$MDB_ROOT/docker-compose.yml"

make test-profile >/dev/null 2>&1 || vfail "make test-profile failed"
add_cleanup 'make down'

# shellcheck disable=SC2016  # evaluated by the subshell, not here
wait_for 90 "the test-profile pg to report healthy" bash -c \
    'mdb_service_healthy pg'

for setting in fsync synchronous_commit full_page_writes; do
    val="$(mdb_compose exec -T pg psql -U postgres -tAc "SHOW $setting" 2>/dev/null | tr -d ' \r')"
    case "$val" in
        off) vinfo "$setting=off" ;;
        *) vfail "$setting is '$val' in the test profile, expected off" ;;
    esac
done

# PGDATA must actually be on tmpfs, not merely configured for speed.
fstype="$(mdb_compose exec -T pg sh -c \
    "stat -f -c %T \$(psql -U postgres -tAc 'SHOW data_directory' | tr -d ' ')" 2>/dev/null | tr -d ' \r')"
[[ "$fstype" == "tmpfs" ]] || vfail "PGDATA filesystem is '$fstype', expected tmpfs"
vinfo "PGDATA is on tmpfs"

# Data loss on stop is the documented contract; prove it holds.
mdb_compose exec -T pg psql -U postgres -q \
    -c "CREATE TABLE ephemeral(v text); INSERT INTO ephemeral VALUES ('gone');" >/dev/null 2>&1

make down >/dev/null 2>&1
make test-profile >/dev/null 2>&1 || vfail "make test-profile failed on the second boot"
# shellcheck disable=SC2016  # evaluated by the subshell, not here
wait_for 90 "the test-profile pg to report healthy again" bash -c \
    'mdb_service_healthy pg'

if mdb_compose exec -T pg psql -U postgres -tAc \
    "SELECT 1 FROM information_schema.tables WHERE table_name='ephemeral'" 2>/dev/null | grep -q 1; then
    vfail "test-profile data survived a restart; it must be ephemeral"
fi
vinfo "data correctly discarded on stop"
