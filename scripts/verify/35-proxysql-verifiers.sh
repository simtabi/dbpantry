#!/usr/bin/env bash
# verify: ProxySQL mirrors password verifiers, never cleartext, and routes queries
# tags: pooling auth
# phase: 6

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime
cd "$MDB_ROOT" || exit 1

# SPEC section 22.4, for the MySQL family.
#
# ProxySQL has no equivalent of pgBouncer's auth_query: it cannot look a
# credential up on demand, it has to hold one. That was the reason this pooler
# was left unwired for a while, and the reason it is wired now is that what it
# holds can be the *verifier* -- the same value the server itself keeps in
# mysql.user.authentication_string -- rather than a cleartext password.
#
# So the assertion is not "the pooler works". It is:
#
#   1. an application user connects THROUGH the pooler;
#   2. what the pooler stores is a verifier, not the password;
#   3. the pooler itself holds no superuser credential;
#   4. a wrong password is still refused.
#
# Without (2) this integration is a security regression over not having it.

engine=mysql
img="$(image_name "$engine")"
need_image "$img"

secret_dir="$MDB_ROOT/secrets"
need_file "$secret_dir/proxysql_admin_password.txt"

proj="px$$"
add_cleanup "rm -f '$secret_dir'/${engine}_${proj}_user*_password.txt"
add_cleanup "make down >/dev/null 2>&1 || true"

mdb_up "PROFILES=${engine},pooler" \
    || vfail "make up PROFILES=${engine},pooler failed"

wait_ready 180 "the engine to accept connections" \
    mdb_compose exec -T "$engine" sh -c 'test -S /var/run/mysqld/mysqld.sock'

./scripts/new-project --name "$proj" --engine "$engine" </dev/null >/dev/null 2>&1 \
    || vfail "could not provision a project to test with"

# The pooler mirrors on start, so it has to see a user created after it booted.
mdb_compose restart "${engine}-pooler" >/dev/null 2>&1
# In a SUBSHELL, because wait_ready exits through vfail on timeout and would
# take the whole check with it before the log dump below ever ran. The subshell
# contains the exit; its FAIL line still prints, and the dump follows it.
if ! ( wait_ready 120 "the pooler to finish mirroring" bash -c \
    "mdb_compose logs ${engine}-pooler 2>/dev/null | grep -q 'mirrored'" ); then
    # The timeout names a wait, not a cause. The pooler's own log distinguishes
    # every distinct failure this can be: still waiting for the upstream (the
    # engine's convergence has not created the proxysql user yet), auth
    # rejected (wrong verifier), TLS refused (CA problems), or ProxySQL itself
    # exiting. Locally the full sequence finishes in seconds, so whatever this
    # is exists only on the runner -- which is exactly why it must be printed
    # rather than reasoned about.
    printf '      ----- %s-pooler log -----\n' "$engine" >&2
    mdb_compose logs --tail 30 "${engine}-pooler" 2>&1 | sed 's/^/      /' >&2 || true
    vfail "the pooler never reported mirroring (its log is above)"
fi

app_pw="$(tr -d '\n' < "$secret_dir/${engine}_${proj}_user_password.txt")"
admin_pw="$(tr -d '\n' < "$secret_dir/proxysql_admin_password.txt")"

proxy_admin() {
    mdb_compose exec -T "${engine}-pooler" \
        mysql -h 127.0.0.1 -P 6032 -uadmin -p"$admin_pw" --protocol=TCP -N -B -e "$1" 2>/dev/null
}

# 1. Through the pooler, as the application user.
got="$(mdb_ctr run --rm --network "$(mdb_project_name)_net" \
    -e MYSQL_PWD="$app_pw" --entrypoint mysql "$img" \
    -h "${engine}-pooler" -P 6033 -u "${proj}_user" --protocol=TCP -N -B \
    -e "SELECT 42" "$proj" 2>/dev/null | tr -d ' \r')"
[[ "$got" == "42" ]] || vfail "query through the pooler returned '$got', expected 42"
vinfo "application user authenticated through ProxySQL"

# 2. A verifier, not the password. caching_sha2_password verifiers begin '$A$'.
prefix="$(proxy_admin "SELECT SUBSTR(password,1,3) FROM mysql_users WHERE username='${proj}_user'" | tr -d ' \r')"
# Matched literally: caching_sha2_password verifiers begin with a $A$ prefix.
# shellcheck disable=SC2016
[[ "$prefix" == '$A$' ]] \
    || vfail "ProxySQL stores '${prefix}...' for ${proj}_user; expected a caching_sha2_password verifier"

leaked="$(proxy_admin "SELECT COUNT(*) FROM mysql_users WHERE password LIKE '%${app_pw}%'" | tr -d ' \r')"
[[ "$leaked" == "0" ]] \
    || vfail "ProxySQL stores the cleartext application password; it must hold only verifiers"
vinfo "stored credential is a verifier; no cleartext application password present"

# 3. The pooler's own account is not root, and not a superuser.
super="$(mdb_compose exec -T "$engine" sh -c \
    "mysql --protocol=socket -uroot -p\"\$(cat /run/secrets/${engine}_root_password.txt)\" -N -B \
     -e \"SELECT COUNT(*) FROM mysql.user WHERE user='proxysql' AND Super_priv='Y'\"" 2>/dev/null | tr -d ' \r')"
[[ "$super" == "0" ]] \
    || vfail "the proxysql account holds SUPER; it needs only SELECT on mysql.user and REPLICATION CLIENT"
vinfo "pooler account holds no superuser privilege"

# 4. A wrong password is still refused.
if mdb_ctr run --rm --network "$(mdb_project_name)_net" \
    -e MYSQL_PWD="${TW_WRONG_PASSWORD}" --entrypoint mysql "$img" \
    -h "${engine}-pooler" -P 6033 -u "${proj}_user" --protocol=TCP -N -B \
    -e "SELECT 1" "$proj" >/dev/null 2>&1; then
    vfail "ProxySQL accepted a wrong password"
fi
vinfo "wrong password refused through ProxySQL"

vinfo "ProxySQL routes queries while holding only verifiers"
