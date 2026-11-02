# Onboarding an existing database

Moving a database you already have — native, on another host, or in another
Docker setup — into the toolkit.

## Before you start

**Nothing here deletes your source.** Every path below reads from the original
and writes into the toolkit, so if the result is wrong you still have what you
started with. Do not turn off the old database until you have queried the new
one.

## From a dump file

```bash
make up PROFILES=pg
make new-project NAME=myapp
make import ENGINE=pg DB=myapp FROM=/path/to/dump.sql
```

A dump from outside dbpantry has no `.sha256`, so `import` restores it as given
(`make restore` would refuse it); if a `.sha256` sits beside it, it is checked.

`import` accepts what the engine's own tools accept: for PostgreSQL a `pg_dump`
custom archive or plain SQL, for the MySQL family a `mysqldump` file, for
MongoDB a `mongodump` directory or archive. Compression is detected from the
content, not the file extension — a file named `.zst` containing gzip is a real
thing that happens and it is handled.

## From a running database on another host

```bash
make import ENGINE=pg DB=myapp --from-host db.old.example --from-user postgres
```

It dumps from the source over the network and restores locally in one pass. The
source is only read.

## From a native install on this machine

The common case: PostgreSQL from Homebrew or apt, and you want to stop running
it natively.

```bash
pg_dump -U postgres -Fc myapp > /tmp/myapp.dump      # your native pg_dump
make new-project NAME=myapp
make import ENGINE=pg DB=myapp FROM=/tmp/myapp.dump
```

Then **check the port**. A native PostgreSQL on 5432 collides with the toolkit's
default, and the failure is confusing rather than obvious: connections succeed,
against the wrong server. Either stop the native service, or move the toolkit:

```
MDB_PG_HOST_PORT=5433
```

## Roles, grants and passwords

Per-database dumps do not carry roles. For PostgreSQL, bring them first:

```bash
pg_dumpall -U postgres --globals-only > /tmp/globals.sql
make psql < /tmp/globals.sql
```

Or skip it: `make new-project` creates an owner role and a read-only companion
with least privilege, which is usually better than importing whatever accumulated
in the old server. If you do import globals, the passwords come with them — run
`make rotate-secrets` afterwards so `secrets/` and the database agree.

## Verify before you cut over

Do not skip this. The dump succeeded is not the same as the data arrived.

```bash
make psql USER_NAME=myapp
```

```sql
SELECT count(*) FROM your_biggest_table;
\dt
```

Compare against the source. `make verify-backups` does exactly this
automatically for backups, and the same instinct applies here: an object count
that matches with a row count of zero means the data was lost in transit, and it
looks like success from every angle except the one that matters.

## Then, and only then

```bash
brew services stop postgresql@16      # or: systemctl disable --now postgresql
```

Keep the dump file for a while.

## Multiple projects at once

```
MDB_PG_DATABASES=app:app_user:__FILE__,analytics:analytics_user:__FILE__
```

Then import into each. Roles cannot reach each other's databases — the default
`PUBLIC` grant is revoked — so onboarding several projects onto one engine does
not merge their access.

---

[← Docs index](../../README.md#documentation)
