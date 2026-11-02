#!/usr/bin/env bash
# verify: the prod profile refuses plaintext transport but keeps sockets working
# tags: tls security prod
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 18: "prod-profile MySQL-family connections without TLS are
# refused by the server". Section 9.1 adds the nuance that matters: unix
# sockets COUNT as a secure transport, so enforcing TLS must not break them.
# A check that only asserted the refusal would let us ship a prod profile that
# silently breaks every socket client.

check_family() {
    local engine="$1" pw_env="$2" client="$3" img name sockvol
    img="$(image_name "$engine")"
    need_image "$img"

    name="mdb-verify-tlsenf-$engine-$$"
    sockvol="mdb-verify-tlsenf-sock-$engine-$$"
    track_container "$name"
    mdb_ctr volume create "$sockvol" >/dev/null
    # Removed by the EXIT trap after the container. An inline rm here ran
    # while the container still held the volume, failed silently, and leaked
    # one volume per engine per run.
    track_volume "$sockvol"

    mdb_ctr run -d --name "$name" \
        -e "$pw_env=${TW_VERIFY}" \
        -e MDB_TLS_ENFORCE=true \
        -e MDB_SOCKETS=true \
        -v "$MDB_ROOT/certs:/certs:ro" \
        -v "$sockvol:/var/run/mysqld" \
        "$img" >/dev/null || vfail "$engine failed to start with MDB_TLS_ENFORCE=true"

    wait_ready 90 "$engine to accept connections" \
        mdb_ctr exec "$name" "$client" -uroot -p"${TW_VERIFY}" --protocol=socket -e "SELECT 1"

    # require_secure_transport must actually be ON.
    local rst
    rst="$(mdb_ctr exec "$name" "$client" -uroot -p"${TW_VERIFY}" --protocol=socket -N -B \
        -e "SELECT @@require_secure_transport" 2>/dev/null | tr -d ' \r')"
    [[ "$rst" == "1" || "$rst" == "ON" ]] \
        || vfail "$engine require_secure_transport is '$rst', expected ON"
    vinfo "$engine require_secure_transport=ON"

    # A plaintext TCP connection must be REFUSED BY THE SERVER.
    if mdb_ctr run --rm --network "container:$name" -e MYSQL_PWD="${TW_VERIFY}" "$img" \
        "$client" -h 127.0.0.1 -uroot --ssl-mode=DISABLED -e "SELECT 1" >/dev/null 2>&1; then
        vfail "$engine accepted a plaintext TCP connection under TLS enforcement"
    fi
    vinfo "$engine refused plaintext TCP"

    # ...but the socket must still work (SPEC section 9.1).
    mdb_ctr exec "$name" "$client" -uroot -p"${TW_VERIFY}" --protocol=socket -e "SELECT 1" >/dev/null 2>&1 \
        || vfail "$engine broke socket connections under TLS enforcement; sockets are a secure transport"
    vinfo "$engine socket connections still work"

}

check_family mysql MYSQL_ROOT_PASSWORD mysql
check_family mariadb MARIADB_ROOT_PASSWORD mariadb

# PostgreSQL: pg_hba must be hostssl-only under enforcement.
img="$(image_name pg)"
need_image "$img"
name="mdb-verify-tlsenf-pg-$$"
track_container "$name"

mdb_ctr run -d --name "$name" -e POSTGRES_PASSWORD="${TW_VERIFY}" -e MDB_TLS_ENFORCE=true \
    -v "$MDB_ROOT/certs:/certs:ro" "$img" >/dev/null \
    || vfail "PG failed to start with TLS enforcement"
wait_ready 60 "postgres to accept connections" mdb_ctr exec "$name" pg_isready -U postgres

if mdb_ctr exec "$name" sh -c "grep -E '^host[[:space:]]' /var/lib/postgresql/data/pg_hba.conf | grep -v replication" 2>/dev/null | grep -q .; then
    vfail "pg_hba.conf still has plain 'host' rules; TLS enforcement requires hostssl only"
fi
vinfo "pg_hba.conf is hostssl-only"
