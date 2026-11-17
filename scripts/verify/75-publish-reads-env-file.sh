#!/usr/bin/env bash
# verify: MDB_PUBLISH and MDB_PORT_BASE set in the environment but not in the env file are refused by gen-compose, not ignored
# tags: structure publish
# phase: 6

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# gen-compose reads MDB_PUBLISH and MDB_PORT_BASE from the env file only. Until
# 2026-11-17 a different value in the environment was dropped silently, so
# `MDB_PUBLISH=direct make up` booted a stack with no published port. This
# renders a scratch copy four ways and needs no container runtime:
#   1. the env file alone renders;
#   2. the environment agreeing with the file renders;
#   3. MDB_PUBLISH differing from the file is refused, naming the file;
#   4. MDB_PORT_BASE differing from the file is refused.
cd "$MDB_ROOT" || vfail "no repository root"
tmpbase="${TMPDIR:-/tmp}"
s="$(mktemp -d "${tmpbase%/}/mdb-publish.XXXXXX")"; add_cleanup "rm -rf '$s'"
rsync -a --exclude /.git --exclude /backups --exclude /secrets --exclude /certs \
    --exclude /build --exclude /.env --exclude /.env.prod "$MDB_ROOT/" "$s/" \
    || vfail "could not copy the checkout"
mkdir -p "$s/secrets" "$s/certs"
cp "$MDB_ROOT/.env.example" "$s/.env"
printf '\nMDB_PUBLISH=none\nMDB_PORT_BASE=54000\n' >> "$s/.env"

gen() { ( cd "$s" && env -u MDB_PUBLISH -u MDB_PORT_BASE MDB_ENV_FILE="$s/.env" "$@" scripts/gen-compose ) >/dev/null 2>"$s/err"; }

gen || vfail "the env file alone did not render: $(cat "$s/err")"
gen env MDB_PUBLISH=none MDB_PORT_BASE=54000 || vfail "an environment that agrees with the file was refused: $(cat "$s/err")"
if gen env MDB_PUBLISH=direct; then vfail "MDB_PUBLISH=direct in the environment was ignored, not refused"; fi
grep -q "$s/.env" "$s/err" || vfail "the refusal does not name the env file: $(cat "$s/err")"
if gen env MDB_PORT_BASE=55000; then vfail "MDB_PORT_BASE=55000 in the environment was ignored, not refused"; fi
vinfo "4 renders: file alone and agreeing environment accepted; differing MDB_PUBLISH and MDB_PORT_BASE refused"
