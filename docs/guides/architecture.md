# Architecture

How dbpantry is put together, and why adding a database engine is a descriptor
rather than a refactor.

## Three layers

**Layer 1 — engine images.** One derived image per engine, each standalone-complete:
it boots into a fully configured server from a bare `docker run` with nothing but
a password, because everything it needs is baked in. s6-overlay is PID 1, running
ordered init stages and then the engine itself.

**Layer 2 — the descriptor.** Each engine declares what it is in one file. The
generic machinery reads that file; nothing else knows the engine exists.

**Layer 3 — compose.** A thin orchestrator that selects which engines run and
wires them together. It contains no engine knowledge beyond what the descriptors
generate.

## Why a descriptor

The first three engines were hardcoded. PostgreSQL, MySQL and MariaDB each had
their own compose service, their own branch in the backup script, their own
provisioning path, and their own checks. That works at three and collapses at
six: every new engine means editing compose, backup, restore, new-project,
check-env, and a dozen checks, and every one of those edits is a place to forget
one engine.

So engines are **declared**. A descriptor states what the generic layer needs:

```sh
# engines/postgres/engine.conf
MDB_ENGINE_NAME=pg
MDB_ENGINE_FAMILY=postgres
MDB_ENGINE_PARADIGM=relational
MDB_ENGINE_VERSIONS="17 18"
MDB_ENGINE_DEFAULT_VERSION=17
MDB_ENGINE_PORT=5432
MDB_ENGINE_DATA_DIR=/var/lib/postgresql/data
MDB_ENGINE_SOCKET_DIR=/var/run/postgresql
MDB_ENGINE_USER=postgres

MDB_ENGINE_PING='pg_isready -U postgres'
MDB_ENGINE_CLIENT=psql
MDB_ENGINE_DUMP='pg_dump -Fc'
MDB_ENGINE_DUMP_GLOBALS='pg_dumpall --globals-only'
MDB_ENGINE_RESTORE='pg_restore --no-owner'

MDB_ENGINE_POOLING=external
MDB_ENGINE_POOLER_IMAGE=ghcr.io/simtabi/dbpantry-pgbouncer:dev
MDB_ENGINE_POOLING_REQUIRED_IN_PROD=true

MDB_ENGINE_LICENSE=PostgreSQL
MDB_ENGINE_OSI_APPROVED=true
```

Shell key-value, not YAML, deliberately: every consumer here is bash, and a
format that needs `yq` installed to read is a dependency the toolkit does not
otherwise have.

Anything a descriptor can express must not appear as an `if engine == ...`
branch anywhere else. That rule is what keeps the abstraction real.

## What is shared and what is not

Shared, because it is genuinely identical across paradigms:

- image build and digest pinning (`images/bases.tsv`)
- s6 supervision and the ordered init stages
- TLS material, placement, and rotation
- secrets via the `_FILE` convention
- backup scheduling, compression, checksums, retention, and verified restore
- health checks, container hardening, the acceptance harness

Per-engine, because it genuinely differs:

- the wire protocol and client
- provisioning statements
- dump and restore commands and their flags
- the pooling story

The init stages are the clearest illustration. Every engine runs the same five,
in the same order, for the same reasons:

```
mdb-perms  →  mdb-conf  →  mdb-certs  →  mdb-provision  →  mdb-engine
```

Permissions before certificates, because engines refuse to start on a
group-readable key. Certificates before provisioning, because provisioning
connects. Provisioning before the engine, because the upstream entrypoint owns
first-run init and our work has to be staged for it to consume.

## Paradigms

| Paradigm | Engines | A "database" is | Provisioned with |
|---|---|---|---|
| Relational | PostgreSQL, MySQL, MariaDB | database + owner role | SQL |
| Document | MongoDB | database + user with roles | JS via mongosh |
| Wide-column | Cassandra | keyspace + role | CQL |

The triplet contract (`db:user:password`) holds across all three, because
"somewhere to put data, someone who owns it, and a credential" is a concept
every one of these engines has, whatever it calls the pieces.

## Connection pooling is a capability

Pooling is not universal, and a uniform abstraction over it would be a lie:

| Engine | Mode | Reason |
|---|---|---|
| PostgreSQL | pgBouncer, **required in prod** | One OS process per connection at roughly 10 MB. ~500 connections cost ~5 GB before any query runs. |
| MySQL / MariaDB | ProxySQL, optional | Thread-per-connection is far cheaper; pooling is a scaling aid, not survival. ProxySQL also brings routing and query rules. |
| MongoDB | Driver-side | Drivers pool natively per the CMAP spec. A proxy in front breaks topology discovery and retryable writes. |
| Cassandra | Driver-side | The driver keeps connections per node under a load-balancing policy and multiplexes thousands of requests per connection. A proxy defeats token-aware routing. |

So the descriptor says `external` or `driver`, and where it says `driver` the
answer is documentation — reuse one client per process — not infrastructure.

## Why one engine per container

Servers are processes with lifecycles: one healthcheck, one restart policy, one
exit code. Combining them means one engine's crash becomes everyone's restart,
logs interleave into an unparsable stream, and per-engine memory limits stop
meaning anything. Tags explode too — split is a sum, combined is a product, and
any engine's CVE forces rebuilding all of them.

The single exception is `dbpantry-cli`, because tools are not daemons.

## Why per-major data volumes

Pointing a new PostgreSQL major at an old data directory fails hard: *"database
files are incompatible with server"*. So the major is part of the volume name,
versions coexist, and `make upgrade` can dump from one into the other while the
source volume is never touched. That last property is what makes rollback a
one-line change rather than a restore.

## Why s6 wraps the official entrypoint instead of replacing it

The upstream entrypoints own first-run initialisation, the `POSTGRES_*` /
`MYSQL_*` / `MONGO_*` environment semantics, the `_FILE` convention, and
`/docker-entrypoint-initdb.d`. Reimplementing that would mean re-earning years of
edge cases. So s6 invokes them as the supervised service and everything the
toolkit adds is staged beforehand for them to consume.

One consequence is not obvious and cost real debugging time: s6 stops services
with SIGTERM, which PostgreSQL reads as a *smart* shutdown — wait for every
client to disconnect. It hangs, gets SIGKILLed, and the next boot runs crash
recovery, all while the container appears to have stopped cleanly. The engine's
s6 service therefore carries a `down-signal` of SIGINT. See `DESIGN.md` D-09.

## Adding an engine

See [Adding an engine](adding-an-engine.md). The short version: write a
descriptor, write a Dockerfile, write the provisioning hook. If you find
yourself editing compose, backup, restore, or a check, the abstraction has a
gap — report it, because that is a bug in this design rather than in your engine.

---

[← Docs index](../../README.md#documentation)
