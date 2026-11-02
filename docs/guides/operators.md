# Kubernetes operators (planned)

> **Docs and a values stub only.** Nothing here is implemented. The
> commented `operators:` block at the end of `deploy/helm/dbpantry/values.yaml`
> records the intended shape; no template reads it. PostgreSQL is the
> exception: `MDB_K8S_HA_PROVIDER=cnpg` already runs a CloudNativePG Cluster
> ([Kubernetes](kubernetes.md#high-availability), design.md D-65).

The Kubernetes backend (design.md D-64) runs each engine as a single-replica
StatefulSet. Clustering, failover and engine-native backups are what database
operators are for, so the plan (design.md D-67) is to hand those to an
operator per engine, opt-in, as D-65 did for PostgreSQL, rather than grow
them in the chart.

## The pattern, from CloudNativePG

1. The operator is installed by the cluster's owner, not by dbpantry. The
   chart checks its CRD exists and refuses with a message naming the install
   command when it does not.
2. With a provider set, the chart renders the operator's custom resource
   instead of the StatefulSet, plus Services named like the Compose services
   (`mysql`, plus `mysql-ro` where the engine has read replicas), so
   applications do not change.
3. Passwords come from the same `secrets/` files, as Kubernetes Secrets in the
   shape the operator expects (CloudNativePG needed basic-auth Secrets without
   a trailing newline).
4. The backend's verbs keep working: `exec` finds the primary's pod by the
   operator's labels, `down` uses the operator's own pause or hibernation, and
   volumes stay retained.
5. A check in the style of check 54 renders the resource against the CRD-free
   chart, and the provider is tested on kind and one other cluster before it
   is called supported.

## Candidates

| Engine | Operator | Resource | Notes |
|---|---|---|---|
| MySQL | Oracle MySQL Operator for Kubernetes | `InnoDBCluster` | Group Replication with MySQL Router; Oracle's images, not dbpantry-mysql |
| MySQL | Percona Operator for MySQL | `PerconaServerMySQL` / `PerconaXtraDBCluster` | Percona Server images; built-in backups to S3 |
| MariaDB | mariadb-operator | `MariaDB` | async replication or Galera; `Backup`/`Restore` resources; can run a custom image, so dbpantry-mariadb may work |
| MongoDB | MongoDB Controllers for Kubernetes (successor to the Community Operator) | `MongoDBCommunity` | replica sets; MongoDB's images |
| MongoDB | Percona Operator for MongoDB | `PerconaServerMongoDB` | replica sets and sharding; Percona images; backups via PBM |
| Cassandra | K8ssandra (cass-operator) | `K8ssandraCluster` / `CassandraDatacenter` | racks and repairs (Reaper), backups (Medusa); its own images |

Versions, licences and image policies change; confirm each against the
project's own documentation before implementing, and record the tested
version in the CHANGELOG as D-65 did for CloudNativePG 1.30.1.

## Open questions

- Most operators run their own images. As with CloudNativePG, the dbpantry
  extensions and hardening would not apply; say so per engine.
- `make backup` and `make restore` run the dbpantry scripts inside the pod.
  Whether to keep them, or defer to the operator's backup resources, is per
  engine.
- `MDB_PROFILES` has one `ha` profile; per-engine opt-in probably needs
  `MDB_K8S_<ENGINE>_PROVIDER` variables mirroring `MDB_K8S_HA_PROVIDER`.
