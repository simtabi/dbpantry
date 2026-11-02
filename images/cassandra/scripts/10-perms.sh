#!/usr/bin/env bash
MDB_STAGE=mdb-perms
export MDB_STAGE
# shellcheck source=mdb-lib.sh
source /usr/local/lib/mdb/mdb-lib.sh

stage "fixing ownership and permissions"
install -d -o cassandra -g cassandra -m 0700 "$MDB_DATA_DIR"
install -d -m 0755 /run/mdb
# /mdb/overrides is an input: a read-only mount (the Compose bind mount, a
# Kubernetes ConfigMap) or the image's own empty directory. Never chmod it; on
# a read-only mount that fails and stops the container.
[ -d /mdb/overrides ] || install -d -m 0755 /mdb/overrides
