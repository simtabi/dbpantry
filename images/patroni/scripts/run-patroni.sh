#!/usr/bin/env bash
#
# Generate this node's Patroni configuration and hand over to Patroni.
#
# Patroni reads most of its settings from PATRONI_* environment variables, but
# the parts that matter here -- the bootstrap block, pg_hba, and the TLS and
# authentication settings the standalone image already enforces -- have no
# environment equivalent. So the config is written here, from the same values
# the rest of the toolkit uses, rather than duplicated into compose.

MDB_STAGE=mdb-patroni
export MDB_STAGE
# The absolute path is correct inside the image; this tells shellcheck where
# to find the same file in the repository.
# shellcheck source=../../pg/scripts/mdb-lib.sh
source /usr/local/lib/mdb/mdb-lib.sh

name="${PATRONI_NAME:?PATRONI_NAME is required}"
scope="${PATRONI_SCOPE:-mdb-pg}"

# Where the cluster state lives (design.md D-65). etcd3 is the Compose
# topology. kubernetes keeps the leader lock in ConfigMaps of the pod's own
# namespace and labels each pod with its role, which is what the chart's pg
# (primary) and pg-ro (replica) Services select on -- no etcd, no HAProxy.
dcs="${MDB_PATRONI_DCS:-etcd3}"
# Members reach each other by this address: the compose hostname, or the pod
# IP on Kubernetes, where a pod's name is not resolvable on its own.
connect_host="${MDB_PATRONI_CONNECT_ADDRESS:-$name}"

case "$dcs" in
    etcd3)
        etcd_hosts="${PATRONI_ETCD3_HOSTS:?PATRONI_ETCD3_HOSTS is required}"
        # Accept the comma-separated form compose passes and emit YAML list
        # items. Quotes are stripped so both "a:1,b:2" and "'a:1','b:2'" work;
        # getting that wrong is silent, because a quoted host is still a valid
        # YAML string.
        etcd_hosts_yaml="$(printf '%s' "$etcd_hosts" \
            | tr ',' '\n' \
            | sed "s/^[[:space:]]*['\"]*//; s/['\"]*[[:space:]]*$//" \
            | sed '/^$/d; s/^/    - /')"
        # A YAML LIST. PATRONI_ETCD3_HOSTS is a comma-separated scalar when
        # Patroni reads it from the environment, and writing that form straight
        # into the file is a parse error at the first comma -- Patroni exits
        # before it ever contacts etcd, and the container just looks unhealthy.
        dcs_yaml="etcd3:
  hosts:
${etcd_hosts_yaml}"
        # The old product name stays: a new namespace orphans an existing
        # cluster (D-54).
        global_ns="namespace: /multidb-server/"
        ;;
    kubernetes)
        k8s_ns="${MDB_PATRONI_K8S_NAMESPACE:?MDB_PATRONI_K8S_NAMESPACE is required for the kubernetes DCS}"
        # Flow-style YAML map of the labels that select this cluster's pods,
        # e.g. {app.kubernetes.io/instance: mdb, app.kubernetes.io/component: pg}.
        k8s_labels="${MDB_PATRONI_K8S_LABELS:?MDB_PATRONI_K8S_LABELS is required for the kubernetes DCS}"
        # ConfigMaps, not Endpoints: Endpoints mode needs a selector-less
        # Service Patroni owns, and the chart's Services select on the role
        # label instead. Role values are spelled out because Patroni 4 changed
        # its defaults, and the Services depend on them.
        dcs_yaml="kubernetes:
  namespace: ${k8s_ns}
  labels: ${k8s_labels}
  scope_label: cluster-name
  role_label: role
  leader_label_value: primary
  follower_label_value: replica
  standby_leader_label_value: primary
  use_endpoints: false
  pod_ip: ${connect_host}
  ports:
    - name: db
      port: 5432"
        # No global namespace: Patroni copies it into the DCS config, where it
        # replaces kubernetes.namespace and every API call goes to a namespace
        # called /multidb-server/.
        global_ns=""
        ;;
    *) die "MDB_PATRONI_DCS must be etcd3 or kubernetes, not $dcs" ;;
esac
data_dir="${PGDATA:-/var/lib/postgresql/data}"
conf=/etc/patroni/patroni.yml

read_secret() {
    local f="/run/secrets/$1"
    [[ -r "$f" ]] || die "secret not readable: $f"
    tr -d '\n' < "$f"
}

super_pw="$(read_secret "${MDB_PG_SUPERUSER_SECRET:-pg_superuser_password.txt}")"

# Replication gets a credential of its own rather than reusing the superuser's.
# A replica only needs REPLICATION, and handing every standby the superuser
# password makes each one a full compromise of the primary.
repl_pw="$(read_secret "${MDB_REPLICATION_SECRET:-pg_replication_password.txt}")"

# Patroni's own REST API is what HAProxy trusts to decide where writes go, so
# its write endpoints are authenticated. Reads (/primary, /replica) stay open:
# HAProxy polls them constantly and they disclose only role, not data.
rest_pw="$(read_secret "${MDB_PATRONI_REST_SECRET:-patroni_rest_password.txt}")"

# --- stage the server certificate where postgres owns it --------------------
# PostgreSQL refuses to start on a private key it does not own:
#   FATAL: private key file "/certs/pg/server.key" must be owned by the
#          database user or root
# certs/ is generated on the HOST by make init, so the key carries the invoking
# user's uid. On a machine where that happens to be root, or where the Docker
# implementation flattens ownership -- as Docker Desktop and OrbStack do on
# macOS -- pointing postgres straight at the bind mount works, and it looks
# fine forever. On Linux, and so on every CI runner, the uid is the runner's,
# matches neither postgres nor root, and the LEADER fails to bootstrap: initdb
# succeeds, the postmaster dies on the key, Patroni renames pgdata to
# pgdata.failed and gives up. The replicas then wait for a leader that will
# never arrive, and compose reports whichever node it noticed -- a different
# one each run, which reads as a flaky node rather than a permission.
#
# The non-HA image already solved this in images/pg/scripts/30-certs.sh; this
# is the same install(1), which copies and sets owner and mode in one step. The
# mount stays read-only and the copy is what postgres reads.
pg_certs=/etc/patroni/certs
install -d -o postgres -g postgres -m 0700 "$pg_certs"
install -o postgres -g postgres -m 0600 /certs/pg/server.key "$pg_certs/server.key"
install -o postgres -g postgres -m 0644 /certs/pg/server.crt "$pg_certs/server.crt"
install -o postgres -g postgres -m 0644 /certs/ca.crt        "$pg_certs/ca.crt"

stage "writing $conf for node $name in scope $scope"

# YAML, generated rather than templated with sed: the values include generated
# passwords, and a substitution into a checked-in template is how a credential
# ends up in a file someone later commits.
cat > "$conf" <<YAML
scope: ${scope}
name: ${name}
${global_ns}

restapi:
  listen: 0.0.0.0:8008
  connect_address: ${connect_host}:8008
  authentication:
    username: patroni
    password: '${rest_pw}'

${dcs_yaml}

bootstrap:
  # DCS values are written to etcd once, by whichever node bootstraps, and are
  # the cluster-wide settings from then on. Editing them here later changes
  # nothing; use \`patronictl edit-config\`.
  dcs:
    # Patroni requires ttl >= loop_wait + 2 * retry_timeout. The defaults
    # (30/10/10) mean the leader lock does not expire for 30 seconds, so no
    # election can possibly complete faster than that -- a 30s failover budget
    # is unreachable by construction, not by bad luck. These values keep the
    # constraint satisfied (20 >= 5 + 10) and bring failover under ~25s.
    #
    # Lower is not automatically better: a short ttl makes a brief network
    # stall look like a dead leader, and demoting a healthy primary costs more
    # than a few extra seconds of failover would have.
    ttl: 20
    loop_wait: 5
    retry_timeout: 5
    # How far behind a replica may be and still be promoted, in bytes. 0 would
    # forbid any data loss and also forbid failover whenever no replica is
    # exactly current -- availability traded for durability, which is a choice
    # to make deliberately rather than inherit.
    maximum_lag_on_failover: 1048576
    synchronous_mode: ${MDB_PG_SYNC_MODE:-false}
    postgresql:
      use_pg_rewind: true
      use_slots: true
      parameters:
        wal_level: replica
        hot_standby: 'on'
        max_wal_senders: 10
        max_replication_slots: 10
        wal_keep_size: 128MB
        ssl: 'on'
        ssl_cert_file: /etc/patroni/certs/server.crt
        ssl_key_file: /etc/patroni/certs/server.key
        ssl_ca_file: /etc/patroni/certs/ca.crt

  initdb:
    - encoding: UTF8
    - data-checksums
    # NOT --auth=trust. initdb defaults to trust for local connections, which
    # check 30 fails the build over: any process in the container would then be
    # superuser with no credential.
    - auth-host: scram-sha-256
    - auth-local: peer

postgresql:
  listen: 0.0.0.0:5432
  connect_address: ${connect_host}:5432
  data_dir: ${data_dir}
  bin_dir: /usr/lib/postgresql/${PG_MAJOR:-17}/bin
  pgpass: /tmp/pgpass0
  authentication:
    superuser:
      username: postgres
      password: '${super_pw}'
    replication:
      username: replicator
      password: '${repl_pw}'
  parameters:
    shared_buffers: ${MDB_PG_SHARED_BUFFERS:-256MB}
  # pg_hba, which Patroni writes every time it starts PostgreSQL. Same shape
  # the standalone image generates, plus the replication rules the cluster
  # needs. Here and not under bootstrap:, where it is written once: the
  # image's start-up hardening (30-certs.sh) regenerates pg_hba.conf on every
  # container start without them, so a member that restarts -- the old
  # primary after a failover, or any pod Kubernetes reschedules -- and later
  # leads would refuse every replica.
  pg_hba:
    - local   all             postgres                    peer map=mdb
    - local   all             all                         scram-sha-256
    - hostssl all             all         127.0.0.1/32    scram-sha-256
    - hostssl all             all         ::1/128         scram-sha-256
    - hostssl all             all         0.0.0.0/0       scram-sha-256
    - hostssl replication     replicator  0.0.0.0/0       scram-sha-256
    # Pod networks may be IPv6 (or dual-stack) on Kubernetes.
    - hostssl all             all         ::/0            scram-sha-256
    - hostssl replication     replicator  ::/0            scram-sha-256
  # kubectl exec and docker exec run as root: map root (and postgres) to the
  # postgres role on the socket, as the standalone image does, so host-side
  # make backup, restore and psql work against a member. Peer still checks a
  # real OS identity; this names exactly which ones.
  pg_ident:
    - mdb        root              postgres
    - mdb        postgres          postgres

tags:
  nofailover: false
  noloadbalance: false
  clonefrom: false
  nosync: false
YAML

chown postgres:postgres "$conf"
chmod 0600 "$conf"

# The data directory must belong to postgres and be 0700, or PostgreSQL refuses
# to start with an error that names permissions rather than ownership.
mkdir -p "$data_dir"
chown -R postgres:postgres "$data_dir"
chmod 0700 "$data_dir"

# HOME must be postgres's, not root's.
#
# s6-setuidgid changes the uid but not the environment, so HOME stayed /root.
# libpq looks for an optional client certificate at $HOME/.postgresql/ and, as
# postgres, gets EACCES on /root -- which it treats as a failure to start TLS
# and RETRIES THE CONNECTION UNENCRYPTED. The leader's pg_hba then refuses it,
# and the only error you see is "no pg_hba.conf entry ... no encryption", which
# points at the rule rather than at the permission problem that caused it.
export HOME=/var/lib/postgresql
export PGSSLMODE="${PGSSLMODE:-require}"

stage "starting Patroni"
exec s6-setuidgid postgres env HOME="$HOME" PGSSLMODE="$PGSSLMODE" \
    /usr/local/bin/patroni "$conf"
