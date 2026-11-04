# Adding an engine

How to add a database to dbpantry. If you find yourself editing compose, the
backup script, or a check, stop — that is a bug in this design, not in your
engine, and it is worth reporting.

## The shape of the job

Three files, sometimes two:

| File | Always? | What it is |
|---|---|---|
| `engines/<name>/engine.conf` | Yes | The descriptor: everything declarative about the engine |
| `engines/_family/<family>.sh` | Only for a new family | The handful of operations that genuinely need code |
| `images/<name>/Dockerfile` | Only when publishing | Absent for engines we reference rather than build |

An engine joining an existing family costs **one file** to declare. FerretDB
speaks the MongoDB wire protocol, so it declared `MDB_ENGINE_FAMILY=mongodb` and
inherited `mongosh`, `mongodump` and `mongorestore` without a line of new code.
Declaring is not running, though: a derived engine also needs its
`images/<name>/Dockerfile`, and until it has one `gen-compose` emits no service
for it and `check-env` refuses its profile. FerretDB is in that state today.

## 1. Write the descriptor

`engines/redis/engine.conf`, say. Shell key-value, not YAML — every consumer
here is bash, and a format needing `yq` would add a dependency the toolkit does
not otherwise have.

```sh
MDB_ENGINE_NAME=redis
MDB_ENGINE_TITLE="Redis"
MDB_ENGINE_FAMILY=redis
MDB_ENGINE_PARADIGM=key-value
# One of: relational, document, wide-column, key-value, graph, search, vector,
# time-series. An engine serving more than one names the rest here.
#MDB_ENGINE_PARADIGM_ALSO="search"

MDB_ENGINE_VERSIONS="7.2 7.4"
MDB_ENGINE_DEFAULT_VERSION=7.4

MDB_ENGINE_PORT=6379
MDB_ENGINE_HOST_PORT_DEFAULT=6379
MDB_ENGINE_DATA_DIR=/data
MDB_ENGINE_USER=redis
MDB_ENGINE_ROOT_SECRET=redis_root_password.txt
MDB_ENGINE_ROOT_PW_ENV=REDIS_PASSWORD_FILE

MDB_ENGINE_PING='redis-cli ping'
MDB_ENGINE_CLIENT=redis-cli
MDB_ENGINE_DUMP='redis-cli --rdb'
MDB_ENGINE_RESTORE='redis-cli'
MDB_ENGINE_BACKUP_EXT=rdb

MDB_ENGINE_AUTH_METHOD=requirepass
MDB_ENGINE_TLS_ALWAYS=false

MDB_ENGINE_POOLING=driver
MDB_ENGINE_POOLING_RATIONALE='clients multiplex over one connection'

MDB_ENGINE_LICENSE=RSALv2
MDB_ENGINE_OSI_APPROVED=false
MDB_ENGINE_LICENSE_NOTE='Source-available since 7.4. See docs/guides/licensing.md.'
MDB_ENGINE_PUBLISH=reference

MDB_ENGINE_OVERRIDES_DIR=/mdb/overrides
```

Then pin every version's base in `images/bases.tsv`:

```
redis	7.2	redis:7.2.x@sha256:...
redis	7.4	redis:7.4.x@sha256:...
```

Run `make verify-structure` and then check 31, which will tell you exactly what
is missing. It enforces that the default version is on the menu, that every
version has a digest-pinned base, that pooling is declared honestly, and that a
non-OSI licence carries both a note and `PUBLISH=reference`.

## 2. Write the family hooks, if the family is new

`engines/_family/redis.sh`. Six functions, written against a contract the
caller provides (`engine_exec`, `secret`, `compress_ext`):

```sh
hook_ping()               # is the server ANSWERING? not "does it have data"
hook_list_databases()     # names, one per line
hook_dump_database()      # $1 = name, $2 = output path
hook_dump_globals()       # cluster-wide state, or `return 0`
hook_recreate_database()  # drop and create empty
hook_restore_database()   # reads the dump on stdin
hook_object_count()       # tables/collections — distinguishes empty from lost
hook_row_count()          # rows/documents
hook_auth_enforced()      # 0 when a bad or absent credential is REFUSED
hook_provision_project()  # $1 db  $2 user  $3 password
                          # $4 extensions (csv, may be empty)
                          # $5 read-only password
```

Two of these have non-obvious contracts, both learned the hard way:

**`hook_ping` must not be `hook_list_databases`.** They answer different
questions. A healthy, freshly initialised server has no user databases, so
using the listing as a readiness probe waits forever on something that is
already up.

**`hook_object_count` exists to separate two failures.** A restored database
with tables but no rows means the data was lost in transit — a real failure. A
restored database with no tables at all means the source was empty, so the
restore proves nothing but is not broken. Reporting the second as a failure
teaches people to ignore the check.

**`hook_provision_project` must be idempotent.** People re-run `make new-project`
on an existing name to reprint the connection block, so creating must be
`IF NOT EXISTS` and the password must be reset rather than skipped — otherwise
the block printed does not match the credential in the database.

It also receives two passwords. The read-only role is a separate principal and
gets its own, so a leak of the read-only credential is not a leak of the
writable one.

Compression belongs to the **generic layer**. Do not compress inside a hook: an
artefact named `.zst` that contains gzip decompresses into garbage, and the
resulting error surfaces far from its cause.

## 3. Write the Dockerfile, if we publish it

Only for `PUBLISH=derive`. Build context is `images/`, so paths are relative to
that. Follow an existing image: s6-overlay as PID 1, ordered init stages, and
the upstream entrypoint **invoked, never reimplemented** — it owns first-run
initialisation, the `_FILE` convention and `docker-entrypoint-initdb.d`, and
re-earning those edge cases is a bad trade.

**Enable authentication at build time.** Several engines ship without it, and
correcting that is a large part of why this toolkit exists:

- MongoDB runs with no authentication unless root credentials are supplied
- Cassandra ships `AllowAllAuthenticator` **and** `AllowAllAuthorizer`

If your engine has a default superuser with a documented password, rotate it
during provisioning. Authentication that is on with a publicly known credential
is barely better than authentication that is off.

## 4. Verify

```bash
make verify-structure                       # harness is well-formed
scripts/verify/31-engine-descriptors.sh     # your descriptor is complete
scripts/verify/32-every-engine-auth.sh      # your engine refuses anonymous access
make build <name> && make up PROFILES=<name>
```

Your engine inherits check 32 by existing. That is deliberate: remembering to
add a check per engine is exactly the discipline that fails quietly, and an
engine with no coverage is one nobody can trust.

## What you should NOT have to touch

`docker-compose.yml`, `scripts/backup`, `scripts/restore`, `scripts/build`,
`scripts/check-env`, or any existing check. All of them read descriptors.

If your engine needs a service-level setting with no generic equivalent —
PostgreSQL's `shm_size`, Cassandra's JVM heap — there are two escape hatches
next to the descriptor:

| File | Merged into |
|---|---|
| `engines/<name>/compose-env.yml` | the service's `environment:` block |
| `engines/<name>/compose-extra.yml` | the service itself |

Reach for those before editing the generator. If neither fits, the generator
has a genuine gap and that is worth an issue.

---

[← Docs index](../../README.md#documentation)
