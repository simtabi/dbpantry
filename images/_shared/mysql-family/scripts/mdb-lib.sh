#!/usr/bin/env bash
# shellcheck shell=bash
#
# Shared helpers for the dbpantry MySQL-family s6 init stages.
#
# One set of scripts serves both engines. design.md D-17 warned that MySQL
# (Oracle Linux 9) and MariaDB (Ubuntu 24.04) do not share a base OS, and that
# remains true for package managers and client binary names -- but the thing
# that actually mattered turned out to be common: BOTH read /etc/mysql/conf.d,
# and MariaDB includes it last, so a file dropped there wins on either engine.
# The engine-specific parts are supplied by MDB_ENGINE, baked at build time.

set -euo pipefail

MDB_ENGINE="${MDB_ENGINE:-mysql}"

MDB_CONF_DIR=/etc/mysql/conf.d
MDB_CERT_DIR=/run/mdb/certs
MDB_INITDB_DIR=/docker-entrypoint-initdb.d
MDB_DATA_DIR="${MDB_DATA_DIR:-/var/lib/mysql}"

case "$MDB_ENGINE" in
    mysql)
        MDB_DAEMON=mysqld
        MDB_CLIENT=mysql
        MDB_ADMIN=mysqladmin
        MDB_CONF_SECTION=mysqld
        MDB_ROOT_PW_ENV=MYSQL_ROOT_PASSWORD
        ;;
    mariadb)
        MDB_DAEMON=mariadbd
        MDB_CLIENT=mariadb
        MDB_ADMIN=mariadb-admin
        MDB_CONF_SECTION=mariadbd
        MDB_ROOT_PW_ENV=MARIADB_ROOT_PASSWORD
        ;;
    *)
        printf 'unknown MDB_ENGINE: %s\n' "$MDB_ENGINE" >&2; exit 1 ;;
esac

export MDB_ENGINE MDB_CONF_DIR MDB_CERT_DIR MDB_INITDB_DIR MDB_DATA_DIR \
       MDB_DAEMON MDB_CLIENT MDB_ADMIN MDB_CONF_SECTION MDB_ROOT_PW_ENV

stage() { printf '[%s] %s\n' "$MDB_STAGE" "$*"; }
die() { printf '[%s] FATAL: %s\n' "${MDB_STAGE:-mdb}" "$*" >&2; exit 1; }

# Has the data directory already been initialised?
datadir_initialised() { [[ -d "$MDB_DATA_DIR/mysql" ]]; }

is_true() {
    case "${1:-}" in
        true|TRUE|True|1|yes|YES|on|ON) return 0 ;;
        *) return 1 ;;
    esac
}

# Per-engine env lookup: MDB_MYSQL_FOO or MDB_MARIADB_FOO.
engine_env() {
    local suffix="$1" default="${2:-}" name value
    case "$MDB_ENGINE" in
        mysql)   name="MDB_MYSQL_${suffix}" ;;
        mariadb) name="MDB_MARIADB_${suffix}" ;;
    esac
    value="${!name:-}"
    printf '%s' "${value:-$default}"
}
