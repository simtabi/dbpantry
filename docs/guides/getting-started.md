# Getting started

From a fresh clone to an application talking to a database, in about a minute.

Assumes [installation](installation.md) is done and `make verify-structure`
passes.

## 1. Start what you need

```bash
make up PROFILES=pg,ui
```

Profiles are opt-in, one per engine, so a laptop is never running six databases
to serve one project. Available: `pg`, `mysql`, `mariadb`, `mongodb`,
`cassandra`, `ferretdb`, plus `ui`, `pooler`, `backup`, `metrics` and `prod`.

```bash
make status
```

## 2. Provision a project

```bash
make new-project NAME=myapp
```

That one command creates the database, an owner role with least privilege, a
read-only companion role, the configured extensions, and prints a paste-ready
connection block:

```
DB_CONNECTION=pgsql
DB_HOST=pg
DB_PORT=5432
DB_DATABASE=myapp
DB_USERNAME=myapp_user
DB_PASSWORD=$(cat secrets/pg_myapp_user_password.txt)
```

The real block prints the password itself. It is generated, written to
`secrets/`, and never committed — the secret scan fails the build on a literal
credential in any tracked file, including this page, which is why the line above
is shown as a file read.

`DB_HOST` is the service name because your application container joins the
toolkit network. From the host it is `127.0.0.1`.

For another engine — all six work the same way:

```bash
make new-project NAME=myapp ENGINE=mysql
make new-project NAME=myapp ENGINE=mongodb
make new-project NAME=myapp ENGINE=cassandra
```

The owner role and the read-only role get **different** passwords, both written
to `secrets/`. They are separate principals, and sharing one credential would
mean a leak of the read-only role is a leak of the writable one.

## 3. Connect

A shell, without needing a client installed on your machine:

```bash
make psql                    # as the superuser
make psql USER_NAME=myapp    # as the project role
make mysql
make mariadb
```

These connect over the unix socket rather than TCP — faster, and it removes a
network surface entirely.

### Nothing is published by default

`MDB_PUBLISH=none` is the default, so no host port is bound at all. That is the
one setting that cannot conflict with software you already run, and it is how
applications are meant to connect anyway — join the network and use the service
name, exactly as the block above prints:

```yaml
networks:
  default:
    external: true
    name: mdb_net
```

**For a GUI client that cannot join the compose network**, publish:

```
MDB_PUBLISH=direct     # each engine gets its own host port
MDB_PORT_BASE=54000    # PostgreSQL 54000, MySQL 54001, MariaDB 54002, ...
```

or put everything behind one front door:

```
MDB_PUBLISH=proxy      # Caddy owns the ports and forwards to the engines
```

Either way a port is `MDB_PORT_BASE + offset`, so if 54000 is taken you move
one number and every engine follows. Ports bind to loopback only, which is
deliberate: an engine reachable from the network the moment it starts is how
development databases end up on the internet.

The browser UIs are on <https://adminer.db.localhost> under the `ui` profile — Adminer
for the SQL engines, plus per-engine consoles.

## 4. Isolation is real, and worth confirming

Roles cannot reach each other's databases. That is not a convention here, it is
enforced and tested — `myapp` connecting to `otherapp` is refused. If you have
two projects, try it; the harness does.

## 5. Back up before you need to

```bash
make backup ENGINE=pg DB=myapp     # one database
make backup-all                    # every database on every running engine
make verify-backups                # restore them into throwaway containers
```

`verify-backups` is the one that matters. It restores the latest dumps into
disposable containers and asserts the row counts came back, because a backup
nobody has restored is a hypothesis. Run it before you rely on the backups, not
after you need them.

## Where to go next

| You want to | Read |
|---|---|
| Understand every setting | [Configuration](configuration.md) |
| Put this on a VPS | [Operations](../runbooks/operations.md) |
| Handle a thousand connections | [Connection pooling](pooling.md) |
| Restore under pressure | [Restore](../runbooks/restore.md) |
| Move an existing database in | [Onboarding](../runbooks/onboarding.md) |
| Change PostgreSQL major version | [Upgrade](../runbooks/upgrade.md) |
| Add a database engine | [Adding an engine](adding-an-engine.md) |

---

[← Docs index](../../README.md#documentation)
