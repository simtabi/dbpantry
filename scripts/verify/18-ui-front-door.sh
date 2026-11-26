#!/usr/bin/env bash
# verify: Caddy serves every UI over HTTPS and phpMyAdmin sees both engines
# tags: ui caddy
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 18: "Caddy serves every UI at its *.db.localhost hostname over
# HTTPS" and "Both MySQL and MariaDB run concurrently with phpMyAdmin seeing
# both via one UI".

need_file "$MDB_ROOT/config/caddy/Caddyfile.tmpl"
need_file "$MDB_ROOT/docker-compose.yml"

# Caddy files the certificates it issues under the issuer's CA id and serves
# a cached, still-valid one before issuing anew. An install upgraded to the
# home's web CA (D-112) under the default id "local" kept serving the
# certificates Caddy's own CA had issued into caddy_data -- measured on the
# demo, 2026-11-25 -- so the web CA has an id of its own and every UI asks
# for it. The scratch stack below starts with an empty volume and cannot see
# this, hence a check on the template.
tmpl="$MDB_ROOT/config/caddy/Caddyfile.tmpl"
grep -qE '^[[:space:]]*ca[[:space:]]+dbpantry[[:space:]]*\{' "$tmpl" \
    || vfail "Caddyfile.tmpl does not define the web CA under its own id (pki { ca dbpantry })"
grep -qE '^[[:space:]]*ca[[:space:]]+local([[:space:]]|\{|$)' "$tmpl" \
    && vfail "Caddyfile.tmpl still defines the web CA as 'local', whose cached certificates pre-date it"
if ! grep -qE 'issuer[[:space:]]+internal[[:space:]]*\{' "$tmpl" \
   || ! grep -qE '^[[:space:]]*ca[[:space:]]+dbpantry[[:space:]]*$' "$tmpl"; then
    vfail "the UI sites do not ask for certificates from the dbpantry CA (tls { issuer internal { ca dbpantry } })"
fi
vinfo "the UIs are issued from the web CA's own id, never a cached 'local' certificate"

# In a stack of its own (scratch_stack), whose cleanup is registered before
# the boot, so a FAILED boot is cleaned up too. It used to boot the operator's
# project and `make down` it at exit, stopping every service there, including
# ones this check never started.
scratch_stack "pg,mysql,mariadb,ui"
domain="$(mdb_env_get MDB_UI_DOMAIN "$SCRATCH_DIR/.env")"
domain="${domain:-db.localhost}"
scratch_up || vfail "make up with the ui profile failed"

wait_for 90 "caddy to start" scratch_call mdb_service_running caddy

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
    scratch_exec caddy \
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
            scratch_call mdb_compose logs --tail 20 "$ui" 2>&1 | sed 's/^/      /' >&2 || true
            printf '      ----- caddy log -----\n' >&2
            scratch_call mdb_compose logs --tail 8 caddy 2>&1 | sed 's/^/      /' >&2 || true
            vfail "https://${ui}.${domain} returned '${code:-no response}' after 120s (logs above)" ;;
    esac
done

# phpMyAdmin must be wired to both MySQL-family servers via PMA_HOSTS.
hosts="$(scratch_exec phpmyadmin printenv PMA_HOSTS 2>/dev/null | tr -d ' \r' || true)"
[[ "$hosts" == *mysql* && "$hosts" == *mariadb* ]] \
    || vfail "PMA_HOSTS is '$hosts'; it must list both mysql and mariadb"
vinfo "phpMyAdmin PMA_HOSTS=$hosts"

# Adminer and pgAdmin must open ready to log in, not asking for a hostname.
server="$(scratch_exec adminer printenv ADMINER_DEFAULT_SERVER 2>/dev/null | tr -d ' \r' || true)"
[[ -n "$server" ]] || vfail "ADMINER_DEFAULT_SERVER is unset; SPEC section 7 requires UIs pre-wired"
vinfo "Adminer default server: $server"

scratch_exec pgadmin test -f /pgadmin4/servers.json 2>/dev/null \
    || vfail "pgAdmin has no baked servers.json; SPEC section 7 requires it pre-provisioned"
vinfo "pgAdmin servers.json present"

# --- What a browser sees, from the host (2026-11-25) ---------------------------
# "Answers 200" above passed while, in a browser, every UI's certificate was
# untrusted (Caddy's own CA lived in its volume), phpMyAdmin printed "Failed to
# read configuration file", pgAdmin printed "Failed to load preferences", and
# http:// redirected to port 443 instead of the published one. Each is
# asserted here through the published ports, as a browser reaches them.
https_port="$(mdb_env_get MDB_CADDY_HTTPS_PORT "$SCRATCH_HOME/.env")"
http_port="$(mdb_env_get MDB_CADDY_HTTP_PORT "$SCRATCH_HOME/.env")"
[[ -n "$https_port" && -n "$http_port" ]] || vfail "the scratch stack names no Caddy ports"
command -v curl >/dev/null 2>&1 || vfail "curl is required"
jar="$(mktemp)"
add_cleanup "rm -f '$jar'"
browse() {
    local host="$1"; shift
    curl -s --max-time 20 --resolve "$host:$https_port:127.0.0.1" --resolve "$host:$http_port:127.0.0.1" \
        -c "$jar" -b "$jar" "$@"
}

# 1. Every UI's certificate chains to the web CA that scripts/certs generated in
# the home -- the one `dbpantry certs trust` installs -- and that CA can sign
# for localhost names only.
web_root="$SCRATCH_HOME/certs/web/root.crt"
[[ -s "$web_root" ]] || vfail "scripts/certs wrote no web CA at certs/web/root.crt"
# -text, not -ext: LibreSSL (macOS's /usr/bin/openssl) has no -ext.
openssl x509 -in "$web_root" -noout -text 2>/dev/null | grep -A3 'Name Constraints' | grep -q 'DNS:\.localhost' \
    || vfail "the web CA is not name-constrained to .localhost"
for ui in adminer pgadmin pma; do
    browse "$ui.$domain" --cacert "$web_root" -o /dev/null "https://$ui.$domain:$https_port/" \
        || vfail "https://$ui.$domain:$https_port/ does not verify against the home's web CA"
done
vinfo "every UI's certificate verifies against certs/web/root.crt (name-constrained to .localhost)"

# 2. http:// redirects to the published HTTPS port, not to 443.
loc="$(browse "adminer.$domain" -o /dev/null -w '%{redirect_url}' "http://adminer.$domain:$http_port/")"
[[ "$loc" == "https://adminer.$domain:$https_port/" ]] \
    || vfail "http://adminer.$domain:$http_port/ redirects to '$loc', not to the published HTTPS port $https_port"
vinfo "http:// redirects to https on port $https_port"

# 3. phpMyAdmin reads its configuration (its entrypoint writes
# config.secret.inc.php and Apache drops to www-data: capabilities).
page="$(browse "pma.$domain" -k "https://pma.$domain:$https_port/")"
grep -q 'Failed to read configuration file' <<< "$page" \
    && vfail "phpMyAdmin cannot read its configuration file (config.secret.inc.php)"
grep -q 'pma_username' <<< "$page" || vfail "phpMyAdmin shows no login form"
vinfo "phpMyAdmin reads its configuration and shows its login form"

# 4. pgAdmin's API refuses a call without a same-origin Referer ("The referrer
# header is missing"), so the front door's Referrer-Policy must let one through.
h="pgadmin.$domain"; u="https://$h:$https_port"
policy="$(browse "$h" -k -o /dev/null -D - "$u/browser/" | tr -d '\r' \
    | awk -F': ' 'tolower($1) == "referrer-policy" {print $2}')"
[[ "$policy" != no-referrer ]] \
    || vfail "pgAdmin is served with Referrer-Policy no-referrer, so a browser's API calls fail (Failed to load preferences)"
tok="$(browse "$h" -k "$u/browser/js/utils.js" | grep -oE "csrf_token'\] = '[^']+'" | sed -E "s/.*= '//; s/'\$//")"
[[ -n "$tok" ]] || vfail "pgAdmin served no CSRF token"
code="$(browse "$h" -k -o /dev/null -w '%{http_code}' -H "X-pgA-CSRFToken: $tok" -H "Referer: $u/browser/" "$u/preferences/")"
[[ "$code" == 200 ]] || vfail "pgAdmin's preferences API answered $code to a browser's request"
vinfo "pgAdmin is served with Referrer-Policy '${policy:-default}' and its preferences API answers"

# 5. A changed front-door configuration reaches Caddy on the next `up`. Caddy
# reads its Caddyfile and the web CA once, at start, with its admin API off,
# and Compose recreates a container only when its definition changes -- not
# when a bind-mounted file does -- so a new MDB_UI_DOMAIN, a renewed web CA or
# an upgraded template was rendered and never served (the demo, 2026-11-25).
newdomain="ui$RANDOM.localhost"
printf '\n# check 18: a new UI domain\nMDB_UI_DOMAIN=%s\n' "$newdomain" >> "$SCRATCH_HOME/.env"
scratch_up || vfail "make up after changing MDB_UI_DOMAIN failed"
ok=""
for _ in $(seq 1 30); do
    if browse "adminer.$newdomain" --cacert "$web_root" -o /dev/null "https://adminer.$newdomain:$https_port/"; then ok=1; break; fi
    sleep 2
done
[[ -n "$ok" ]] || vfail "after MDB_UI_DOMAIN=$newdomain and make up, Caddy does not serve https://adminer.$newdomain (it kept its old configuration)"
vinfo "a changed MDB_UI_DOMAIN is served after make up (Caddy restarted with its new configuration)"
