# Upgrade

Moving a database to a new major version, without a window where losing the old
copy is possible.

## The guarantee this rests on

**Volumes are per engine and per major version.** PostgreSQL 17's data lives in
one volume, 18's in another, and an upgrade writes only to the new one.

The consequence is the useful part: **the old volume is never touched**, so a
failed upgrade is a non-event and rollback is changing one line in `.env` back.
You are not racing a backup.

## Upgrade

```bash
make backup-all
make upgrade ENGINE=pg FROM=17 TO=18
```

`make upgrade` covers PostgreSQL, MySQL and MariaDB. MongoDB and Cassandra are
manual, below.

Take the backup anyway. The volume guarantee protects you from the upgrade; it
does not protect you from discovering a week later that something was already
wrong.

`make upgrade` does the dump-and-restore path:

1. starts the old major against its existing volume, read-only work only
2. dumps every database and, for PostgreSQL, the globals
3. starts the new major against a fresh volume
4. restores
5. compares object and row counts, and **fails loudly if they disagree**
6. tells you to set `MDB_PG_VERSION=18` — it does not edit `.env` for you

Step 6 is deliberate. The upgrade proving it worked and you deciding to switch
are two different decisions, and collapsing them means a surprise the next time
anyone runs `make up`.

## Then switch

```
MDB_PG_VERSION=18
MDB_PG_DATA_MOUNT=/var/lib/postgresql
```

PostgreSQL 18 moved its data mount one level up, so crossing from 17 to 18
changes `MDB_PG_DATA_MOUNT` too; `make check-env` refuses a mismatched pair, and
`make upgrade` mounts each side at its own path.

```bash
make up
make psql -c '\l'
```

## Rolling back

```
MDB_PG_VERSION=17
MDB_PG_DATA_MOUNT=/var/lib/postgresql/data
```

```bash
make up
```

That is the whole procedure. The 17 volume was never written to.

## Why dump-and-restore rather than pg_upgrade --link

`pg_upgrade --link` is dramatically faster on large datasets — it hard-links
files instead of copying them — and it is not what `make upgrade` runs.

The reason is that `--link` **mutates the source data directory**. After it runs,
the old cluster is no longer safely startable, which trades away the exact
property that makes this whole procedure calm. On a development machine, and on
most production databases under a few hundred gigabytes, dump-and-restore is
minutes and the guarantee is worth more than the minutes.

If your dataset is large enough that this matters, run it by hand, from a
verified backup, having read the PostgreSQL documentation for your version pair,
and expect to need `--check` first. It is not automated here because automating
it would mean shipping a fast path whose failure mode is losing the source.

## From a version that is no longer maintained

dbpantry maintains the newest supported line of each engine and the one before
it ([Supported versions](../guides/versions.md)). When a line drops off — PostgreSQL 15 and
16, MySQL 8.0, MariaDB 10.11 and 11.4, MongoDB 8.3, Cassandra 4.1 in
0.2.0 — a `.env` still naming it fails `make check-env`, loudly, and nothing
touches its volume.

To upgrade off it, the old image has to exist, so build it once from its upstream
base, unsupported:

```
MDB_MARIADB_VERSION=11.4
MDB_MARIADB_BASE_IMAGE=docker.io/library/mariadb:11.4.13@sha256:1292844148b311e4ed4300022a996d39083f415a963e970cf47cad1b3b18e3a6
```

```bash
scripts/build mariadb          # dbpantry-mariadb:11.4, labelled unsupported
```

If you already built or pulled `dbpantry-mariadb:11.4` before 0.2.0, it is still
local and this step is not needed. Then set the target version, remove the
override, build it, and upgrade as usual:

```bash
scripts/build mariadb          # after MDB_MARIADB_VERSION=11.8, override removed
make upgrade ENGINE=mariadb FROM=11.4 TO=11.8
```

The digest above is the one 0.1.x pinned. Staying on the old version instead is
equally valid — keep both lines in `.env` — but `make check-env` will say on every
start that it is unsupported.

## MySQL and MariaDB

Same command, same guarantee:

```bash
make upgrade ENGINE=mysql FROM=8.4 TO=9.7
```

8.4 → 9.7 is LTS to LTS, the upgrade path Oracle supports. An 8.0 volume goes to
8.4 first; MySQL does not support skipping an LTS.

Two version-specific traps, both of which bite after the upgrade rather than
during it:

**MySQL 8.4 removed `mysql_native_password`.** Clients too old to speak
`caching_sha2_password` stop connecting. `MDB_MYSQL_NATIVE_PASSWORD_COMPAT=true`
re-enables it as a transition, not a destination.

**MariaDB and MySQL have diverged** far enough that they are not interchangeable
targets. Upgrade MariaDB to MariaDB. Moving between them is a migration, and the
tool for it is `make export` / `make import`.

## MongoDB

Not automated: `make upgrade` refuses `ENGINE=mongodb`. The same guarantee is
kept by hand — copy the old major's volume, and only ever start the new major on
the copy. Rehearsed for 7.0.39 → 8.0.34 on 2026-11-01: 150 of 150 documents on
8.0, and the 7.0 volume still started and read afterwards.

**MongoDB does not support skipping major versions.** 6.0 → 8.0 must go through
7.0, and the source must be at its own feature compatibility version (FCV) first;
`db.adminCommand({getParameter: 1, featureCompatibilityVersion: 1})` shows it.

```bash
make backup-all
scripts/runtime compose stop mongodb
docker volume create mdb_mongodb8.0_data
docker run --rm -v mdb_mongodb7.0_data:/from:ro -v mdb_mongodb8.0_data:/to \
    alpine sh -c 'cp -a /from/. /to/'
```

Then set `MDB_MONGODB_VERSION=8.0`, `make up`,
check your data, and only then raise the FCV — after that the copy cannot go back
to 7.0, though the original volume still can:

```js
db.adminCommand({ setFeatureCompatibilityVersion: "8.0", confirm: true })
```

MongoDB 8.0 refuses to start on Linux kernels that report 6.19 to 7.0.13
(SERVER-121912), which includes every Ubuntu 7.0 kernel (SERVER-136848). Check
the kernel your containers see with `docker run --rm alpine uname -r` before you
migrate; if it is affected, stay on 7.0.

## Cassandra 4.1 → 5.0

Cassandra 4.1 is no longer maintained here (design.md D-57). Not automated
either; on a single node the volume copy keeps the old data untouched. Rehearsed
for 4.1.12 → 5.0.9 on 2026-11-01: 200 of 200 rows on 5.0, `upgradesstables`
clean, and the 4.1 volume unchanged.

```bash
make backup-all
scripts/runtime compose exec cassandra nodetool drain     # flush memtables, stop writes
scripts/runtime compose stop cassandra
docker volume create mdb_cassandra5.0_data
docker run --rm -v mdb_cassandra4.1_data:/from:ro -v mdb_cassandra5.0_data:/to \
    alpine sh -c 'cp -a /from/. /to/'
```

Set `MDB_CASSANDRA_VERSION=5.0`, remove `MDB_CASSANDRA_BASE_IMAGE`, `make up`,
then rewrite the SSTables in the new format:

```bash
scripts/runtime compose exec cassandra nodetool upgradesstables
```

5.0 starts with `storage_compatibility_mode: CASSANDRA_4`, which keeps the files
readable by 4.1. Leave it there until you are sure; moving it through
`UPGRADING` to `NONE` is what unlocks 5.0-only features and closes the door back.
On a real cluster, follow the Cassandra upgrade documentation — a rolling upgrade
is not a thing this toolkit can do for you.

## FerretDB

FerretDB stores its data in PostgreSQL through the DocumentDB extension, so its
upgrades follow FerretDB's release notes, not this page.

## Cleaning up the old volume

Only once the new version has been running long enough that you would have
noticed a problem. There is no hurry, and the disk is cheaper than the regret.

```bash
docker volume ls | grep mdb
docker volume rm mdb_pg17_data
```

---

[← Docs index](../../README.md#documentation)
