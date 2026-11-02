#!/usr/bin/env bash
# verify: check-env refuses sentinel passwords, missing required vars and unchosen versions
# tags: fast security
# phase: 2

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# SPEC decision 6 and section 9: "make check-env refuses to start with sentinel
# passwords". A check-env that can be satisfied by a placeholder is not a gate.

need_file "$MDB_ROOT/scripts/check-env"
[[ -x "$MDB_ROOT/scripts/check-env" ]] || vfail "scripts/check-env is not executable"

tmp="$(mktemp -d)"
add_cleanup "rm -rf '$tmp'"

# A sentinel value must be rejected.
cp "$MDB_ROOT/.env.example" "$tmp/.env"
printf 'MDB_PG_SUPERUSER_PASSWORD=CHANGE_ME\n' >> "$tmp/.env"

if MDB_ENV_FILE="$tmp/.env" "$MDB_ROOT/scripts/check-env" >/dev/null 2>&1; then
    vfail "check-env accepted a CHANGE_ME sentinel password"
fi
vinfo "sentinel password rejected"

# A plaintext password (rather than a _FILE reference) must be rejected.
cp "$MDB_ROOT/.env.example" "$tmp/.env2"
printf 'MDB_PG_SUPERUSER_PASSWORD=mdb-throwaway-plaintext\n' >> "$tmp/.env2"

if MDB_ENV_FILE="$tmp/.env2" "$MDB_ROOT/scripts/check-env" >/dev/null 2>&1; then
    vfail "check-env accepted a plaintext password; the _FILE convention is mandatory"
fi
vinfo "plaintext password rejected"

# The unmodified template must pass.
cp "$MDB_ROOT/.env.example" "$tmp/.env3"
MDB_ENV_FILE="$tmp/.env3" "$MDB_ROOT/scripts/check-env" >/dev/null 2>&1 \
    || vfail "check-env rejected the unmodified .env.example template"
vinfo "clean template accepted"

# --- versions: maintained, or unsupported by explicit choice (design.md D-57) --
# An off-menu version needs MDB_<ENGINE>_BASE_IMAGE pinned by digest, and is
# then accepted with a warning. Each half of that is a promise in
# docs/guides/versions.md, so each is asserted.
digest="sha256:$(printf '0%.0s' $(seq 1 64))"

cp "$MDB_ROOT/.env.example" "$tmp/.env4"
printf 'MDB_MARIADB_VERSION=10.6\n' >> "$tmp/.env4"
if MDB_ENV_FILE="$tmp/.env4" "$MDB_ROOT/scripts/check-env" >/dev/null 2>&1; then
    vfail "check-env accepted an off-menu version with no MDB_MARIADB_BASE_IMAGE"
fi
vinfo "off-menu version without an override rejected"

cp "$tmp/.env4" "$tmp/.env5"
printf 'MDB_MARIADB_BASE_IMAGE=docker.io/library/mariadb:10.6\n' >> "$tmp/.env5"
if MDB_ENV_FILE="$tmp/.env5" "$MDB_ROOT/scripts/check-env" >/dev/null 2>&1; then
    vfail "check-env accepted MDB_MARIADB_BASE_IMAGE without a digest"
fi
vinfo "override without a digest rejected"

cp "$tmp/.env4" "$tmp/.env6"
printf 'MDB_MARIADB_BASE_IMAGE=docker.io/library/mariadb:10.6.25@%s\n' "$digest" >> "$tmp/.env6"
out="$(MDB_ENV_FILE="$tmp/.env6" "$MDB_ROOT/scripts/check-env" 2>&1)" \
    || vfail "check-env rejected an off-menu version with a digest-pinned override"
grep -q 'UNSUPPORTED' <<<"$out" \
    || vfail "check-env accepted an off-menu version without saying it is unsupported"
vinfo "off-menu version with a digest-pinned override accepted, flagged unsupported"

out="$(MDB_ENV_FILE="$tmp/.env3" "$MDB_ROOT/scripts/check-env" 2>&1)"
if grep -q 'UNSUPPORTED' <<<"$out"; then
    vfail "the unmodified template is flagged unsupported; its defaults must be maintained versions"
fi
vinfo "template defaults are maintained versions"
