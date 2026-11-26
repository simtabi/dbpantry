#!/usr/bin/env bash
# verify: a secret file named in .env resolves against the instance home on the host and /run/secrets in a container
# tags: fast structure backup
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# 2026-11-24 audit, K1. scripts/backup read the off-site credentials from
# $ROOT/$key_file: the kit, not the instance home (design.md D-84), and never
# /run/secrets, where the backup sidecar has them. So with S3 configured a
# scheduled backup died after its first dump, and with a separate home the
# credentials were looked for in the extracted kit. Secret files named in
# .env (MDB_*_FILE=secrets/...) go through mdb_secret_path.

home="$(mktemp -d "${TMPDIR:-/tmp}/mdb-verify-home.XXXXXX")" || vfail "mktemp failed"
add_cleanup "rm -rf '$home'"
# shellcheck disable=SC2016  # expanded by the inner bash
resolve() { env -u MDB_IN_CONTAINER "$@" bash -c '. scripts/env-lib.sh && mdb_secret_path "$1"' _ "${RESOLVE_ARG:?}"; }

got="$(RESOLVE_ARG=secrets/s3_key_id.txt resolve MDB_HOME="$home")"
[[ "$got" == "$home/secrets/s3_key_id.txt" ]] || vfail "on the host, secrets/s3_key_id.txt resolved to '$got', not the home's"
got="$(RESOLVE_ARG=secrets/s3_key_id.txt resolve MDB_HOME="$home" MDB_IN_CONTAINER=1)"
[[ "$got" == /run/secrets/s3_key_id.txt ]] || vfail "in a container, secrets/s3_key_id.txt resolved to '$got', not /run/secrets"
got="$(RESOLVE_ARG=/etc/creds/key resolve MDB_HOME="$home")"
[[ "$got" == /etc/creds/key ]] || vfail "an absolute path was rewritten to '$got'"
vinfo "secret files resolve to the home on the host, /run/secrets in a container, absolute paths as given"

# No script reads a secret file named in .env relative to the kit again.
hits="$(grep -nE '\$\{?ROOT\}?/\$\{?(key_file|secret_file|[a-z_]*_file)\}?' scripts/backup scripts/restore scripts/pitr scripts/transfer 2>/dev/null || true)"
[[ -z "$hits" ]] || vfail "a script reads a secret file relative to the kit: $hits"
vinfo "no script reads a secret file relative to the kit"
