#!/usr/bin/env bash
# verify: Caddy serves every UI over HTTPS and phpMyAdmin sees both engines
# tags: ui caddy
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime
cd "$MDB_ROOT" || exit 1

# SPEC section 18: "Caddy serves every UI at its *.db.localhost hostname over
# HTTPS" and "Both MySQL and MariaDB run concurrently with phpMyAdmin seeing
# both via one UI".

need_file "$MDB_ROOT/config/caddy/Caddyfile.tmpl"
need_file "$MDB_ROOT/docker-compose.yml"

domain="$(env_get MDB_UI_DOMAIN db.localhost)"

# Registered BEFORE the boot, not after. A cleanup registered on the line
# following `mdb_up` never runs when the boot itself fails -- and a FAILED
# boot is exactly when containers are left behind. Check 27's half-started
# HA stack kept haproxy on 5432, so check 28 then failed its port pre-flight
# and reported a port collision that had nothing to do with its subject.
add_cleanup 'make down'
mdb_up PROFILES=pg,mysql,mariadb,ui || vfail "make up with the ui profile failed"

# shellcheck disable=SC2016  # evaluated by the subshell, not here
wait_for 90 "caddy to start" bash -c \
    'mdb_service_running caddy'

# Each UI must answer over HTTPS through Caddy. Caddy's internal CA is not in
# any trust store, so --no-check-certificate is correct here; what is asserted
# is that TLS is served and the app responds, not that the local CA chains to a
# public root.
#
# "Container running" is NOT readiness: pgAdmin's container reports running
# roughly 25 seconds before gunicorn starts listening, and Caddy answers 502 in
# the meantime. So each route is polled until it answers, with a budget -- a UI
# that never comes up still fails.
ui_status() {
    mdb_compose exec -T caddy \
        wget -qO /dev/null --no-check-certificate --server-response \
        "https://${1}.${domain}/" 2>&1 \
        | awk '$1 ~ /^HTTP\// {print $2}' | tail -1
}

for ui in adminer pgadmin pma; do
    code=""
    for _ in $(seq 1 60); do
        code="$(ui_status "$ui" || true)"
        case "$code" in
            200|301|302) break ;;
        esac
        sleep 2
    done
    case "$code" in
        200|301|302) vinfo "https://${ui}.${domain} -> $code" ;;
        *)
            # The status code alone convicts nobody: 502 means only that Caddy
            # could not reach the UI, and the UI's own log says why it is not
            # answering -- for pgAdmin it was a crash loop on a secret file it
            # could not read, which no amount of staring at Caddy would surface.
            printf '      ----- %s log -----\n' "$ui" >&2
            mdb_compose logs --tail 20 "$ui" 2>&1 | sed 's/^/      /' >&2 || true
            printf '      ----- caddy log -----\n' >&2
            mdb_compose logs --tail 8 caddy 2>&1 | sed 's/^/      /' >&2 || true
            vfail "https://${ui}.${domain} returned '${code:-no response}' after 120s (logs above)" ;;
    esac
done

# phpMyAdmin must be wired to both MySQL-family servers via PMA_HOSTS.
hosts="$(mdb_compose exec -T phpmyadmin printenv PMA_HOSTS 2>/dev/null | tr -d ' \r' || true)"
[[ "$hosts" == *mysql* && "$hosts" == *mariadb* ]] \
    || vfail "PMA_HOSTS is '$hosts'; it must list both mysql and mariadb"
vinfo "phpMyAdmin PMA_HOSTS=$hosts"

# Adminer and pgAdmin must open ready to log in, not asking for a hostname.
server="$(mdb_compose exec -T adminer printenv ADMINER_DEFAULT_SERVER 2>/dev/null | tr -d ' \r' || true)"
[[ -n "$server" ]] || vfail "ADMINER_DEFAULT_SERVER is unset; SPEC section 7 requires UIs pre-wired"
vinfo "Adminer default server: $server"

mdb_compose exec -T pgadmin test -f /pgadmin4/servers.json 2>/dev/null \
    || vfail "pgAdmin has no baked servers.json; SPEC section 7 requires it pre-provisioned"
vinfo "pgAdmin servers.json present"
