#!/usr/bin/env bash
# verify: MySQL and MariaDB boot standalone, configured, and run concurrently
# tags: mysql mariadb standalone
# phase: 3

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 18: "Both MySQL and MariaDB run concurrently". They use the same
# internal port, so this also proves the port-mapping contract in section 7.
#
# DESIGN.md D-17: these two images do NOT share a base OS (MySQL is Oracle
# Linux 9, MariaDB is Ubuntu 24.04), so each is checked on its own terms.

check_engine() {
    local engine="$1" pw_env="$2" client="$3" img name
    img="$(image_name "$engine")"
    need_image "$img"

    name="mdb-verify-$engine-$$"
    track_container "$name"

    mdb_ctr run -d --name "$name" -e "$pw_env=mdb-throwaway-verify" "$img" >/dev/null \
        || vfail "$engine failed to start with only $pw_env set"

    wait_ready 90 "$engine to accept connections" \
        mdb_ctr exec "$name" "$client" -uroot -pmdb-throwaway-verify -e "SELECT 1"

    # Baked defaults from SPEC section 6 must actually be in effect.
    local charset collation sql_mode
    charset="$(mdb_ctr exec "$name" "$client" -uroot -pmdb-throwaway-verify -N -B \
        -e "SELECT @@character_set_server" 2>/dev/null | tr -d ' \r')"
    [[ "$charset" == "utf8mb4" ]] || vfail "$engine character_set_server is '$charset', expected utf8mb4"

    collation="$(mdb_ctr exec "$name" "$client" -uroot -pmdb-throwaway-verify -N -B \
        -e "SELECT @@collation_server" 2>/dev/null | tr -d ' \r')"
    [[ "$collation" == utf8mb4_unicode_ci* ]] \
        || vfail "$engine collation_server is '$collation', expected utf8mb4_unicode_ci"

    sql_mode="$(mdb_ctr exec "$name" "$client" -uroot -pmdb-throwaway-verify -N -B \
        -e "SELECT @@sql_mode" 2>/dev/null | tr -d ' \r')"
    [[ -n "$sql_mode" ]] || vfail "$engine has an empty sql_mode; SPEC section 6 requires it explicit"

    vinfo "$engine: utf8mb4 / $collation, sql_mode set"

    # The baked tool suite (SPEC 6.1).
    local tool
    for tool in zstd rclone; do
        mdb_ctr exec "$name" sh -c "command -v $tool" >/dev/null 2>&1 \
            || vfail "$engine image is missing $tool"
    done

    # Returned through a global rather than stdout: vinfo writes to stdout too,
    # so command substitution would capture the whole running commentary along
    # with the container name.
    CHECKED_CONTAINER="$name"
}

CHECKED_CONTAINER=""
check_engine mysql MYSQL_ROOT_PASSWORD mysql
mysql_c="$CHECKED_CONTAINER"
check_engine mariadb MARIADB_ROOT_PASSWORD mariadb
mariadb_c="$CHECKED_CONTAINER"

# Both must be alive at the same time.
mdb_ctr exec "$mysql_c" mysql -uroot -pmdb-throwaway-verify -e "SELECT 1" >/dev/null 2>&1 \
    || vfail "MySQL stopped responding once MariaDB was up"
mdb_ctr exec "$mariadb_c" mariadb -uroot -pmdb-throwaway-verify -e "SELECT 1" >/dev/null 2>&1 \
    || vfail "MariaDB stopped responding once MySQL was up"

vinfo "MySQL and MariaDB run concurrently"
