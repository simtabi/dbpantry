# Restore

The runbook for when something is already wrong. Read the first section, then
do exactly what it says.

## Stop and read this first

**Do not restore over a live database while you are guessing.** Two minutes of
diagnosis is cheaper than restoring the wrong dump onto data that was fine.

```bash
make status
docker compose logs <engine> --tail 100
```

Establish which of these you have, because the answer changes everything:

| Symptom | This is |
|---|---|
| Engine will not start | an availability problem — restore is probably not the fix |
| Engine is up, data is wrong or missing | a data problem — restore is the fix |
| One table is wrong, everything else is fine | a data problem — restore into a *copy*, not over the original |

## Restore one database

```bash
make restore ENGINE=pg DB=myapp FILE=backups/pg/myapp-2026-08-07.dump
```

It shows you what it is about to do — engine, database, file, timestamp — and
requires a typed confirmation. The confirmation is not friction for its own
sake: `restore` drops and recreates the target database, and that is not
recoverable.

Without `FILE=`, it lists what is available and asks.

## Restore into a copy instead

Almost always the right move when only part of the data is wrong. Restore
beside the original, compare, then move what you need:

```bash
make new-project NAME=myapp_recovery
make restore ENGINE=pg DB=myapp_recovery FILE=backups/pg/myapp-2026-08-07.dump
make psql USER_NAME=myapp_recovery
```

Nothing in production is touched, and if the dump turns out to be wrong too you
have lost nothing.

## Verify the backups instead of trusting them

```bash
make verify-backups
```

Restores the latest dump for every database into throwaway containers and
asserts the object and row counts came back. Run it on a schedule, not in an
emergency — this is how you learn a backup is bad while it still does not matter.

It distinguishes two failures on purpose: a restored database with **tables but
no rows** lost data in transit and is a real failure; one with **no tables at
all** came from an empty source and proves nothing but is not broken.

## When the engine will not start

Restore is not the first move. In order:

```bash
docker compose logs <engine> --tail 100     # it will say why
docker compose ps -a                        # exit code
df -h                                       # disk full is a common cause
```

Common causes, most likely first:

**Disk full.** Free space, then start. Nothing is corrupted.

**Version mismatch.** `MDB_PG_VERSION` was changed and PostgreSQL is pointed at
a data directory from another major. Set the variable back to the version that
wrote the data and it starts. Volumes are per major precisely so this is
recoverable rather than destructive — see [Upgrade](UPGRADE.md) to migrate.

**Unclean shutdown.** Databases recover from this themselves on start. Let it
finish; do not interrupt recovery and do not restore over it.

**Actual corruption.** Rare, and the logs say so explicitly. Now restore.

## Restore everything

After losing a machine:

```bash
make init                     # regenerates certs; keeps existing secrets
make up PROFILES=pg,mysql
for db in ...; do make restore ENGINE=pg DB=$db FILE=backups/pg/$db-<date>.dump; done
```

For PostgreSQL, restore the globals first if you have them
(`backups/pg/globals-<date>.sql`) — they carry roles and grants, which every
per-database dump assumes already exist.

> If `secrets/` was lost with the machine, restored databases keep the passwords
> they had when dumped, which no longer match anything in a fresh `secrets/`.
> Run `make rotate-secrets` after restoring to bring them back into agreement.

## Where backups are

```
backups/<engine>/<database>-<timestamp>.<ext>[.zst]
backups/pg/globals-<timestamp>.sql
```

Retention, schedule, compression and encryption are in `.env` under Backup.

The toolkit writes backups to `MDB_BACKUP_DIR` and does not ship them
anywhere — copying them off the machine is yours to arrange. If you have not
done that yet, do it before you need this page again: a backup on the machine
that failed is not a backup.

---

[← Docs index](../../README.md#documentation)
