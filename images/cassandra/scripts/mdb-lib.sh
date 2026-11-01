#!/usr/bin/env bash
# shellcheck shell=bash
# Shared helpers for the dbpantry-cassandra init stages.
set -euo pipefail

MDB_CONF=/etc/cassandra/cassandra.yaml
MDB_DATA_DIR=/var/lib/cassandra
export MDB_CONF MDB_DATA_DIR

stage() { printf '[%s] %s\n' "$MDB_STAGE" "$*"; }
die() { printf '[%s] FATAL: %s\n' "${MDB_STAGE:-mdb}" "$*" >&2; exit 1; }

is_true() {
    case "${1:-}" in true|TRUE|True|1|yes|YES|on|ON) return 0 ;; *) return 1 ;; esac
}
