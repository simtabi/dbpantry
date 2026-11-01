#!/usr/bin/env bash
#
# dbpantry-cli entrypoint.
#
# Deliberately thin: this image has no server to initialise and no supervision
# to set up. It prints its bearings on an interactive start and then gets out
# of the way, so `docker run ... dbpantry-cli psql ...` behaves exactly like
# running psql.

set -euo pipefail

if [[ -t 1 ]] && [[ "${1:-bash}" == "bash" ]]; then
    cat <<'BANNER'
dbpantry-cli — clients only, no server here.

  psql / pg_dump / pg_dumpall / pg_restore   PostgreSQL
  mariadb / mariadb-dump (also mysql / mysqldump)
  zstd, rclone, openssl, jq

Engines are reachable by service name on the mdb network, for example:
  psql -h pg -U postgres
  mariadb -h mariadb -uroot -p

BANNER
fi

exec "$@"
