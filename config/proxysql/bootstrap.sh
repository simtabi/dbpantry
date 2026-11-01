#!/bin/sh
#
# Start ProxySQL and configure it from the SERVER's own password verifiers.
#
# The point of this file is what it does NOT do: it never sees, stores or is
# given an application's cleartext password. It copies each user's verifier --
# the same value MySQL itself keeps in mysql.user.authentication_string -- so
# compromising the pooler yields exactly what compromising a read of that table
# would, and no more. That is the closest the MySQL family gets to pgBouncer's
# auth_query, which fetches a SCRAM verifier for the same reason.
#
# Verifiers are copied as HEX and re-assembled with UNHEX() because they are
# binary and contain characters that would need escaping. ProxySQL's own
# documentation recommends exactly this.
#
# Configuration goes through the admin interface rather than a config file,
# because UNHEX() is a SQL function and a .cnf has no way to express it.
#
# DB_HOST, DB_PORT and DB_PASSWORD_FILE are the generator's existing pooler
# contract, shared with pgBouncer. Inventing MDB_-prefixed names here would
# have made container-internal plumbing look like toolkit configuration a user
# is meant to set.

set -eu

upstream="${DB_HOST:?DB_HOST is required}"
upstream_port="${DB_PORT:-3306}"
# The POOLER's account, not root. root is restricted to the local socket --
# correct hardening, and deliberately not relaxed for this. The engine's
# convergence stage creates a `proxysql` user with exactly two privileges:
# SELECT on mysql.user (to read verifiers) and REPLICATION CLIENT (to monitor).
pooler_user=proxysql
pooler_pw_file="${ADMIN_PASSWORD_FILE:-/run/secrets/proxysql_admin_password.txt}"
admin_pw_file="${ADMIN_PASSWORD_FILE:-/run/secrets/proxysql_admin_password.txt}"

[ -r "$pooler_pw_file" ] || { echo "proxysql: cannot read $pooler_pw_file" >&2; exit 1; }
pooler_pw="$(tr -d '\n' < "$pooler_pw_file")"
admin_pw="$(tr -d '\n' < "$admin_pw_file" 2>/dev/null || echo "")"
[ -n "$admin_pw" ] || { echo "proxysql: cannot read $admin_pw_file" >&2; exit 1; }

# --ssl-ca is required, not optional. The client in this image is MariaDB's,
# which attempts TLS by default and then rejects the toolkit's own CA with
# "self-signed certificate in certificate chain" -- a connection error that says
# nothing about certificates being the thing to configure. The CA is already
# mounted at /certs for exactly this.
UPSTREAM_ARGS="--protocol=TCP --ssl-ca=${MDB_CA_FILE:-/certs/ca.crt}"

mysql_up() {
    # shellcheck disable=SC2086  # deliberately word-split into arguments
    mysql -h "$upstream" -P "$upstream_port" -u"$pooler_user" -p"$pooler_pw" \
        $UPSTREAM_ARGS -N -B -e "$1" 2>/dev/null
}

# ProxySQL starts with its built-in admin credential and --initial resets to it
# on every start, so bootstrap configuration necessarily runs as that default.
# It is reachable only on 127.0.0.1 inside this container -- port 6032 is never
# published, precisely because that interface can read every verifier ProxySQL
# holds -- and the last bootstrap step replaces it with the generated secret.
admin() {
    mysql -h 127.0.0.1 -P 6032 -uadmin -padmin \
        --protocol=TCP -N -B -e "$1" 2>/dev/null
}

echo "proxysql: waiting for ${upstream}:${upstream_port}"
i=0
until mysql_up "SELECT 1" >/dev/null; do
    i=$(( i + 1 ))
    if [ "$i" -gt 120 ]; then
        echo "proxysql: upstream never became reachable; last error was:" >&2
        # shellcheck disable=SC2086
        mysql -h "$upstream" -P "$upstream_port" -u"$pooler_user" -p"$pooler_pw" \
            $UPSTREAM_ARGS -e "SELECT 1" >&2 2>&1 || true
        exit 1
    fi
    sleep 2
done

# The monitor account is the same narrow user: it already holds REPLICATION
# CLIENT, and a second account would be a second credential to rotate.
mon_pw="$pooler_pw"

echo "proxysql: starting"
proxysql --initial -f --idle-threads -D /var/lib/proxysql &
proxysql_pid=$!

echo "proxysql: waiting for the admin interface"
i=0
until admin "SELECT 1" >/dev/null; do
    i=$(( i + 1 ))
    [ "$i" -gt 60 ] && { echo "proxysql: admin interface never came up" >&2; exit 1; }
    # If ProxySQL died, say so rather than looping until the timeout.
    kill -0 "$proxysql_pid" 2>/dev/null || { echo "proxysql: exited during startup" >&2; exit 1; }
    sleep 1
done

admin "DELETE FROM mysql_servers;
       INSERT INTO mysql_servers (hostgroup_id, hostname, port, max_connections)
       VALUES (0, '${upstream}', ${upstream_port}, ${MAX_BACKEND_CONN:-100});
       LOAD MYSQL SERVERS TO RUNTIME; SAVE MYSQL SERVERS TO DISK;"

admin "UPDATE global_variables SET variable_value='proxysql'
         WHERE variable_name='mysql-monitor_username';
       UPDATE global_variables SET variable_value='${mon_pw}'
         WHERE variable_name='mysql-monitor_password';
       UPDATE global_variables SET variable_value='caching_sha2_password'
         WHERE variable_name='mysql-default_authentication_plugin';
       LOAD MYSQL VARIABLES TO RUNTIME; SAVE MYSQL VARIABLES TO DISK;"

# Mirror every real user's verifier. System accounts are excluded: they are not
# application users, and one of them is root.
users="$(mysql_up "
    SELECT CONCAT(user, '\t', HEX(authentication_string))
      FROM mysql.user
     WHERE plugin = 'caching_sha2_password'
       AND authentication_string <> ''
       AND user NOT IN ('root','mysql.sys','mysql.session','mysql.infoschema',
                        'proxysql_monitor','healthcheck')")"

count=0
if [ -n "$users" ]; then
    echo "$users" | while IFS="$(printf '\t')" read -r u hexpw; do
        if [ -z "$u" ] || [ -z "$hexpw" ]; then continue; fi
        admin "INSERT OR REPLACE INTO mysql_users
                 (username, password, default_hostgroup, active, transaction_persistent)
               VALUES ('${u}', UNHEX('${hexpw}'), 0, 1, 1);"
    done
    count="$(printf '%s\n' "$users" | grep -c . || echo 0)"
fi
admin "LOAD MYSQL USERS TO RUNTIME; SAVE MYSQL USERS TO DISK;"

# Last, so every step above ran against a known credential: replace the default
# admin password with the generated one.
admin "UPDATE global_variables SET variable_value='admin:${admin_pw}'
         WHERE variable_name='admin-admin_credentials';
       LOAD ADMIN VARIABLES TO RUNTIME; SAVE ADMIN VARIABLES TO DISK;"

echo "proxysql: mirrored ${count} user verifier(s); no cleartext password was read"

wait "$proxysql_pid"
