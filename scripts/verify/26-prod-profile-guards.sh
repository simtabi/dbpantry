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

# design.md D-73: init-prod applies the overlay through COMPOSE_FILE.
grep -q '^COMPOSE_FILE=.*compose\.prod\.yml:compose\.prod\.engines\.yml' "$tmp/.env.prod" \
    || vfail "init-prod did not put the prod overlay in COMPOSE_FILE"
vinfo "init-prod writes the prod overlay into COMPOSE_FILE"

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
mv "$tmp/.env.prod.bak" "$tmp/.env.prod"

# A prod env whose COMPOSE_FILE lacks the overlay -- every env file made before
# design.md D-73 -- is refused, and the refusal names the fix.
sed -i.bak 's/^COMPOSE_FILE=.*/COMPOSE_FILE=docker-compose.yml:compose.engines.yml:compose.proxy.yml:compose.ha.yml/' "$tmp/.env.prod"
if out="$(env -u COMPOSE_FILE MDB_ENV_FILE="$tmp/.env.prod" MDB_CHECK_RENDERING=1 scripts/check-env 2>&1)"; then
    vfail "check-env allowed a prod env whose COMPOSE_FILE has no prod overlay"
fi
grep -q 'make prod-overlay' <<< "$out" || vfail "check-env refused the missing overlay without naming make prod-overlay"
scripts/prod-overlay "$tmp/.env.prod" >/dev/null || vfail "scripts/prod-overlay failed"
env -u COMPOSE_FILE MDB_ENV_FILE="$tmp/.env.prod" MDB_CHECK_RENDERING=1 scripts/check-env >/dev/null 2>&1 \
    || vfail "check-env still refuses after scripts/prod-overlay"
vinfo "a prod env without the overlay is refused, and make prod-overlay fixes it"

# `make up PROFILES=pg,pooler,prod` on a DEV env file must get the prod guards.
# They used to read MDB_PROFILES from the file alone and skip every one.
cp .env.example "$tmp/.env.dev"
if env -u COMPOSE_FILE COMPOSE_PROFILES=pg,pg-pooler,prod MDB_ENV_FILE="$tmp/.env.dev" \
        scripts/check-env >/dev/null 2>&1; then
    vfail "check-env let PROFILES=...,prod boot a dev env file; the prod guards were skipped"
fi
vinfo "prod named only in PROFILES= still applies the prod guards"

# Nothing but Caddy may be published.
# The files init-prod writes into COMPOSE_FILE (design.md D-73), in its order:
# compose.ha.yml because the overlay's pgbouncer hardens the HA pooler defined
# there (D-72), and the generated half that covers every engine.
published="$(mdb_compose -f docker-compose.yml -f compose.engines.yml -f compose.ha.yml -f compose.prod.yml -f compose.prod.engines.yml --profile prod config \
    | awk '/^  [a-z-]+:/{svc=$1} /published:/{print svc, $2}' | grep -v '^caddy:' || true)"
[[ -z "$published" ]] || {
    printf '      %s\n' "$published" >&2
    vfail "prod publishes ports on services other than caddy"
}
vinfo "prod publishes nothing except Caddy"
