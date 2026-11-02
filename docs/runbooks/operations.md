# Operations

Running this on a server rather than a laptop: exposure, tuning, monitoring,
rotation and high availability.

## The prod profile is the entry point

```bash
make init-prod
make up PROFILES=pg,pooler,backup,metrics,prod
```

`prod` is not decoration. `check-env` refuses to start the stack if:

- `MDB_TLS_ENFORCE` is not `true`
- an engine that requires a pooler does not have one
- a UI route is exposed without basic auth

These are refusals, not warnings, because every one of them is a mistake that
looks fine until it is on the internet.

## Exposure

The default binds every engine to `127.0.0.1`. On a server, the question is not
"which port do I open" but "should this port be reachable at all".

**Preferred: do not expose the database.** `MDB_PUBLISH=none` is the default and
binds nothing at all — applications join the `mdb_net` network and use service
names. Nothing to firewall, nothing to collide.

When you need host access, reach it over an SSH tunnel rather than a published
port:

```bash
MDB_PUBLISH=direct     # PostgreSQL lands on MDB_PORT_BASE + 0
ssh -L 5432:127.0.0.1:54000 you@server
```

**If you must expose it**, set `MDB_BIND_ADDR` to a specific private address —
never `0.0.0.0` — enforce TLS, and put a firewall in front. Docker publishes
ports by writing DNAT rules that most firewall front-ends do not show you, so a
`ufw` rule that looks like it blocks the port frequently does not.

```
MDB_PUBLISH=proxy      # one process owns the ports, not six
MDB_BIND_ADDR=10.0.1.5
MDB_TLS_ENFORCE=true
MDB_MTLS=true
```

`proxy` mode is worth preferring here: one front door is one thing to firewall,
one thing to audit, and one place a rule can be wrong. It uses Caddy with the
layer4 module — see [image provenance](../reference/image-provenance.md). That module is
pre-1.0, so if you would rather not have it in the connection path, `direct`
publishes the same ports without it.

`MDB_MTLS=true` requires client certificates. On an exposed database it is
worth the setup.

The UIs go behind Caddy with basic auth on `MDB_CADDY_BIND_ADDR`, and the prod
profile will not start without the auth hash set.

## Tuning

```
MDB_MEM=16
MDB_CPUS=8
```

Engines derive their settings from these — shared buffers, work memory, the
InnoDB buffer pool, WAL sizing. Set the budget, not thirty individual knobs.

Give the *database's* share, not the machine's total. On a 32 GB server also
running your application, `MDB_MEM=16` is honest and `MDB_MEM=32` will get
something OOM-killed.

Per-service hard ceilings:

```
MDB_PG_CPU_LIMIT=4
MDB_PG_MEM_LIMIT=16g
```

Cassandra sets its heap explicitly, because the JVM does not take a hint:

```
MDB_CASSANDRA_HEAP=8G
MDB_CASSANDRA_HEAP_NEW=800M
```

## Connection pooling

On a server this stops being optional for PostgreSQL. See
[Connection pooling](../guides/pooling.md) for why, and for what transaction pooling
breaks. Short version:

```bash
make up PROFILES=pg,pooler
```

Point applications at **6432**.

## Backups

```
MDB_BACKUP_DIR=backups
MDB_BACKUP_SCHEDULE=0300
MDB_BACKUP_COMPRESSION=ZSTD
MDB_BACKUP_ENCRYPT=true
MDB_BACKUP_ENCRYPT_PASSPHRASE_FILE=secrets/backup_passphrase.txt
MDB_BACKUP_RETAIN_DAILY=7
MDB_BACKUP_RETAIN_WEEKLY=4
MDB_BACKUP_RETAIN_MONTHLY=6
MDB_BACKUP_NOTIFY_URL=https://...
```

Each backup run, scheduled or `make backup-all`, prunes dumps
(`<engine>_<db>_<timestamp>.*`) older than `MDB_BACKUP_RETAIN_DAILY` days. Other
files in the directory are left alone. That includes the backup checks in
`make verify`, which run against this same directory. Only the daily window is
enforced today: `MDB_BACKUP_RETAIN_WEEKLY` and `MDB_BACKUP_RETAIN_MONTHLY` are
read by nothing yet.

Two things that are not optional on a server:

**Get them off the machine.** A backup on the machine that failed is not a
backup. Set an S3-compatible destination and every dump is copied there after
it is taken:

```
MDB_S3_BUCKET=my-backups
MDB_S3_HOST=s3.us-east-1.amazonaws.com
MDB_S3_REGION=us-east-1
MDB_S3_PROVIDER=AWS
MDB_S3_KEY_ID_FILE=secrets/s3_key_id.txt
MDB_S3_KEY_SECRET_FILE=secrets/s3_key_secret.txt
```

Credentials go in `secrets/`, never in `.env`. A failed push **fails the
backup** rather than being logged: a run that reports success having left
everything on the machine is the exact shape of a backup strategy discovered to
be missing at the worst moment.

PostgreSQL additionally sends its WAL archive and base backups off-site through
pgBackRest's own S3 repository — see below — which is what makes recovery to a
point in time survive losing the host.

**Scheduled verification.** `make verify-backups` restores the latest set into
throwaway containers and asserts the row counts. Put it on a schedule and alert
on failure via `MDB_BACKUP_NOTIFY_URL`. An unverified backup is a hypothesis,
and the moment you need it is the worst possible time to test it.

## Monitoring

```bash
make up PROFILES=pg,metrics
```

Prometheus exporters per engine, on the metrics network. Scrape them with your
existing Prometheus; nothing here assumes it owns your monitoring stack.

Worth alerting on, in rough order of how often each one is what actually went
wrong:

| Signal | Why |
|---|---|
| disk free on the data volume | the most common cause of a database that stops |
| connection count vs `max_connections` | you are about to refuse connections |
| replication lag | a replica that is silently far behind is not a replica |
| backup job success **and** verification success | the second one is the one that matters |
| oldest transaction age | long-running transactions block vacuum and grow the disk |

## Rotating secrets and certificates

```bash
make rotate-secrets     # every database password, applied to running engines
make certs-renew        # server certificates, reloaded without downtime
```

`rotate-secrets` changes the password in the engine and in `secrets/` together,
then verifies the old one no longer works. That verification exists because it
once did not: loopback connections were being trusted by `pg_hba.conf`, so
rotation "succeeded" while the old password kept working. Check 30 now fails the
build on any trust rule anywhere.

Applications reading passwords from `secrets/` at startup need a restart after
rotation.

## Point-in-time recovery

A dump gives you the moment it ran. WAL archiving lets you stop recovery at any
instant the archive covers — including the second before a `DELETE` with no
`WHERE` clause.

```
MDB_PG_PITR=true
MDB_PGBACKREST_REPO_TYPE=s3
MDB_PGBACKREST_S3_BUCKET=my-pgbackrest
MDB_PGBACKREST_S3_ENDPOINT=s3.us-east-1.amazonaws.com
```

**Not a live toggle.** `archive_mode` cannot be reloaded, so enabling PITR takes
effect at the next restart. It is off by default because archiving on a
development machine fills a disk with segments nobody will replay; the prod
profile requires it, and also requires the repository to be `s3` — a local
repository dies with the machine it was protecting.

```bash
make pitr-info                                  # repository and recoverable window
make pitr-backup TYPE=full                      # incr by default
make pitr-restore TO='2026-08-09 12:00:00'      # or TO=latest
```

The repository is encrypted before it leaves the machine and lives in its own
volume rather than inside the data volume — a backup stored inside the thing it
backs up goes when that goes, and `make destroy` would take it.

Restoring rewrites the data directory, so `pitr-restore` requires a typed
confirmation. If you are not certain of the target, restore into a copy first;
[Restore](restore.md) covers that.

### The MySQL family

MariaDB has point-in-time recovery through its binary log:

```
MDB_MARIADB_PITR=true
```

The log is written to its own volume, never inside the data directory, in ROW
format with full row images — `STATEMENT` format replays non-deterministic
statements differently than they ran, so recovery would silently diverge from
the database it is meant to reproduce.

**Recovery targets a coordinate, not a time.**

```bash
make pitr-info ENGINE=mariadb                          # current coordinate
make pitr-restore ENGINE=mariadb TO=binlog.000002:873  # after restoring a dump
```

That is not a stylistic preference. `mariadb-binlog` given several files and
`--stop-datetime` exits after the **first** file and silently skips the rest
([MDEV-35528](https://jira.mariadb.org/browse/MDEV-35528)), so a time-bounded
replay recovers almost nothing while reporting success. A position is exact,
has no timezone interpretation, and `--stop-position` applies to the last file
named — earlier files replay in full, the final one stops at a byte offset.

The procedure is: restore the base dump, then replay. Dumps record the
coordinate they were taken at, so the replay knows where to start.

**MySQL configures binary logging but does not claim PITR.** The log is written
and can be shipped off-site; what is missing is a tool to read it back. The
official `mysql:8.4` image is `mysql-community-server-minimal` on Oracle Linux
9 with no `mysqlbinlog`, the only one in its repos conflicts with the server
package, and MySQL Shell's binlog utilities arrived after the version it ships.
A log nothing can replay is an audit trail rather than a recovery path, so
`engines/mysql/engine.conf` says so rather than implying otherwise. Use MariaDB
if you need PITR from that family.

## High availability

```bash
make up PROFILES=ha
```

That is three Patroni nodes, a three-member etcd quorum, and HAProxy in front.

**`ha` replaces the `pg` profile rather than joining it.** HAProxy owns the
PostgreSQL port and routes it to whichever node is currently leader, so asking
for both binds 5432 twice — `check-env` refuses that combination outright.

```
MDB_HA_CLUSTER_NAME=mdb-pg
MDB_HA_ETCD_HOSTS=etcd1:2379,etcd2:2379,etcd3:2379
MDB_HAPROXY_WRITE_PORT=5432
MDB_HAPROXY_READ_PORT=5433
```

**etcd needs at least three nodes.** A two-node etcd has *lower* availability
than a single node, because it cannot form a majority after losing one. If you
are not going to run three, do not enable HA — run one well-backed-up node and
be honest about the recovery time.

HAProxy routes by Patroni's REST health check: the write port follows the
primary, the read port the replicas.

```bash
make ha-status              # topology, roles and replication lag
make ha-failover            # controlled switchover, typed confirmation
make ha-reinit NODE=patroni2   # rebuild one replica from the leader
```

`ha-failover` performs a **switchover** on a healthy cluster, not a failover:
it hands the role over cleanly instead of simulating a crash. Using a real
failover on a healthy cluster is how people lose the transactions in flight.

For scripting, `scripts/ha leader` prints the leader's name and
`scripts/ha roles` prints member/role/state, tab separated. Use those rather
than parsing `ha-status`, which is formatted for people and includes the
election history.

### What failover actually costs

A killed leader is replaced in about **20 seconds**, which the harness asserts
on every run. That number is not arbitrary and cannot be tuned to zero:

| Setting | Value | Why |
|---|---|---|
| `ttl` | 20s | The leader lock's lifetime. No election can start before it expires, so **a failover budget at or below the ttl is unreachable by construction.** |
| `loop_wait` | 5s | How often each node re-evaluates. |
| `retry_timeout` | 5s | Patroni requires `ttl >= loop_wait + 2 × retry_timeout`. |

Lower is not automatically better. A short `ttl` makes a brief network stall
look like a dead leader, and demoting a healthy primary costs more than a few
extra seconds of failover would have.

### What will not be promoted

Patroni refuses to promote a replica lagging more than
`maximum_lag_on_failover` (1 MB here), and that is correct — promoting a node
that is behind silently discards the writes it never received. The consequence
worth knowing: **if every replica is behind, there is no election at all.** The
cluster stays leaderless and every node logs "I am not the healthiest node".

That is Patroni working, not failing. Check replication lag with
`make ha-status` before assuming a failover mechanism is broken.

`MDB_PG_SYNC_MODE=on` gives zero data loss on failover, at the cost of every
commit waiting for a replica. That is a real latency cost, and the right answer
depends on your data. Choose it deliberately.

MongoDB and Cassandra have their own replication models and are not covered by
this; a single-node Cassandra with RF=1 is a development setup, not a cluster.

## Upgrades and updates

```bash
make self-update      # the toolkit itself; never touches project data
```

Database version upgrades are [their own runbook](upgrade.md).

## When something breaks

[Restore](restore.md) is the runbook, and its first section is about not
restoring until you know which problem you have.

---

[← Docs index](../../readme.md#documentation)
