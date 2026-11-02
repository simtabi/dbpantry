#!/usr/bin/env bash
# verify: the prod profile publishes nothing but Caddy and demands pooling
# tags: prod security
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime
cd "$MDB_ROOT" || exit 1

# SPEC section 18: "make init-prod renders a prod env that passes check-env
# with TLS enforced, nothing published except Caddy".
# SPEC section 21.1: pooling is mandatory under prod, and check-env must fail a
# prod boot without a reachable pooler rather than starting unpooled.

need_file "$MDB_ROOT/compose.prod.yml"

tmp="$(mktemp -d)"
add_cleanup "rm -rf '$tmp'"
add_cleanup 'make down'

make init-prod ENV_FILE="$tmp/.env.prod" >/dev/null 2>&1 \
    || vfail "make init-prod failed"

grep -q '^MDB_TLS_ENFORCE=true' "$tmp/.env.prod" \
    || vfail "init-prod did not enforce TLS"
vinfo "init-prod renders TLS enforced"

# Validated the way init-prod validates it. Rendering and booting are different
# questions: rendering produces a file, often on a machine that is not the one
# that will run it, so a port held here or an S3 key not yet created is not a
# defect in the file. Booting is when both must be real.
MDB_ENV_FILE="$tmp/.env.prod" MDB_CHECK_RENDERING=1 scripts/check-env >/dev/null 2>&1 \
    || vfail "the rendered prod env does not pass check-env"
vinfo "rendered prod env passes check-env"

# ...and the other half: BOOTING that same file without the operator's
# object-store credentials must be refused. Deferring the requirement is only
# acceptable because something later enforces it -- otherwise a prod stack
# starts with PITR configured, archiving to a repository it cannot reach, and
# the only symptom is pg_wal growing while everything reports healthy.
# MDB_CHECK_RENDERING=0 asks the boot question explicitly: the filename would
# otherwise default a non-.env file to rendering, which is right for init-prod
# and wrong for this assertion.
if MDB_ENV_FILE="$tmp/.env.prod" MDB_CHECK_RENDERING=0 scripts/check-env >/dev/null 2>&1; then
    vfail "check-env allowed a prod BOOT with no pgBackRest S3 credentials;
       the deferred requirement is never enforced"
fi
vinfo "a prod boot without object-store credentials is refused"

# Pooling is not optional under prod (section 21.1).
sed -i.bak 's/^MDB_PGBOUNCER_POOL_MODE=.*/MDB_PGBOUNCER_POOL_MODE=/' "$tmp/.env.prod"
if MDB_ENV_FILE="$tmp/.env.prod" scripts/check-env >/dev/null 2>&1; then
    vfail "check-env allowed a prod boot with no pooler; SPEC 21.1 makes pooling mandatory"
fi
vinfo "check-env refuses an unpooled prod boot"
mv "$tmp/.env.prod.bak" "$tmp/.env.prod"

# UI basic auth is mandatory under prod.
sed -i.bak 's/^MDB_UI_BASIC_AUTH_HASH=.*/MDB_UI_BASIC_AUTH_HASH=/' "$tmp/.env.prod"
if MDB_ENV_FILE="$tmp/.env.prod" scripts/check-env >/dev/null 2>&1; then
    vfail "check-env allowed a prod boot with no UI basic auth"
fi
vinfo "check-env refuses prod without UI basic auth"

# Nothing but Caddy may be published.
published="$(mdb_compose -f docker-compose.yml -f compose.engines.yml -f compose.prod.yml --profile prod config \
    | awk '/^  [a-z-]+:/{svc=$1} /published:/{print svc, $2}' | grep -v '^caddy:' || true)"
[[ -z "$published" ]] || {
    printf '      %s\n' "$published" >&2
    vfail "prod publishes ports on services other than caddy"
}
vinfo "prod publishes nothing except Caddy"
