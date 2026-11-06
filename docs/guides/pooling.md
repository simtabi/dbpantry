# Connection pooling

Pooling is not one thing. What you should do differs by engine, and a toolkit
that shipped a uniform "pooler" for all of them would be selling you three
quarters of a lie.

## The short version

| Engine | What to use | Required? | Shipped |
|---|---|---|---|
| PostgreSQL | pgBouncer | **Yes, in production** | Yes — `PROFILES=pg,pooler` |
| MySQL / MariaDB | ProxySQL | Optional | Yes — `PROFILES=mysql,pooler` |
| MongoDB | The driver | Never use a proxy | n/a |
| Cassandra | The driver | Never use a proxy | n/a |
| FerretDB | The driver | Never use a proxy | n/a |

## PostgreSQL: pooling is survival

PostgreSQL forks **one operating system process per connection**, and each costs
roughly 10 MB before it does any work. The arithmetic is unforgiving:

| Direct connections | RAM spent before a single query |
|---|---|
| 100 | ~1 GB |
| 300 | ~3 GB |
| 500 | ~5 GB |

That is memory gone to *existing*, not to serving. Past a few hundred
connections the machine also spends real time context-switching between
processes that are mostly idle.

So pgBouncer is **mandatory** under the prod profile, and `check-env` refuses to
start a prod stack without it. Your application opens a thousand connections to
pgBouncer; pgBouncer keeps twenty-five to PostgreSQL.

```bash
make up PROFILES=pg,pooler
```

Then connect to **6432** instead of 5432 — that is the only application change.

```
MDB_PGBOUNCER_POOL_MODE=transaction
MDB_PGBOUNCER_DEFAULT_POOL_SIZE=25
MDB_PGBOUNCER_MAX_CLIENT_CONN=1000
```

### The pooler holds no application passwords

A pooler has to authenticate clients, and the obvious way — give it a list of
users and passwords — makes it a second place every credential lives. This one
does not do that. It has a login role of its own, and resolves everyone else's
verifier on demand:

```sql
SELECT username, password FROM pgbouncer.get_auth($1)
```

`pgbouncer.get_auth` is a `SECURITY DEFINER` function that returns exactly one
row for the name it is given and **excludes superusers**, so compromising the
pooler yields one credential rather than all of them. Its `search_path` is
pinned to `pg_catalog`, because a `SECURITY DEFINER` function that resolves
unqualified names through the caller's path is a privilege-escalation bug.

The function is created by a convergence stage that runs on **every** start, not
only at first init, so you can enable the pooler on a volume created months ago
without destroying data to get it.

Check 33 asserts all of this behaviourally: that a user authenticates through
the pooler, that the pooler's userlist does not contain that user's password,
that eight clients collapse onto a pool of two, and that a wrong password is
still refused.

**Transaction pooling has rules.** A server connection is returned to the pool
at the end of every transaction, so anything that lives on a *session* breaks:

- session-level `SET` (use `SET LOCAL`, inside the transaction)
- `LISTEN` / `NOTIFY`
- advisory locks held across transactions
- prepared statements outside a transaction
- cursors declared `WITHOUT HOLD`

This matters for the RLS kit: `SET LOCAL app.tenant_id` is correct precisely
because it is discarded at commit, so a pooled connection cannot carry one
tenant's identity into the next request. See [the RLS kit](../../rls/README.md).

If you need session state, use `session` pool mode and accept a much lower
multiplexing ratio.

### The pooler speaks TLS

pgBouncer serves the same certificate `make certs` issues for PostgreSQL
(`certs/pg/server.crt`, signed by `certs/ca.crt`), so a client that trusts the
toolkit CA trusts the pooler too. What it accepts follows `MDB_TLS_ENFORCE`:

| | Client → pooler | Pooler → PostgreSQL |
|---|---|---|
| `MDB_TLS_ENFORCE=true` (prod profile) | TLS required; plaintext refused | TLS required |
| `MDB_TLS_ENFORCE=false` (dev) | TLS when the client asks, plaintext otherwise | TLS when PostgreSQL offers it |

Under enforcement the pooler refuses to start if no certificate is mounted,
rather than serving plaintext. With no certificate and no enforcement it serves
plaintext only. Check 68 asserts each row on the wire. Design rationale:
design.md D-69.

The certificate is copied when the pooler starts, so after `make certs-renew`
restart `pg-pooler` to serve the new one.

### Connecting from another host

An application on a different server reaches PostgreSQL **through the pooler**,
published on a private address. On the database server, in `.env.prod`:

```
MDB_PUBLISH=direct
MDB_BIND_ADDR=10.0.1.5         # the database server's private address
MDB_EXTRA_SANS="10.0.1.5"      # so verify-full by address matches the certificate
```

```bash
make certs-renew               # re-issue certs/pg with the new SANs
make up PROFILES=pg,pooler,backup,prod
```

With `PROFILES=...,prod` only the pooler is published: `pg-pooler` on
`MDB_PORT_BASE + 100`, **54100** with the default base (override with
`MDB_PG_POOLER_HOST_PORT`), bound to `MDB_BIND_ADDR`. The application connects
with:

| Setting | Value |
|---|---|
| host | `10.0.1.5` (the `MDB_BIND_ADDR`, or a name listed in `MDB_EXTRA_SANS`) |
| port | `54100` |
| sslmode | `require` to start; `verify-full` once the CA is on the client |
| sslrootcert | the client's copy of `certs/ca.crt` (for `verify-full`) |
| user, password, database | the project's own, from `make new-project` |

For Laravel that is:

```
DB_CONNECTION=pgsql
DB_HOST=10.0.1.5
DB_PORT=54100
DB_SSLMODE=verify-full
DB_SSLROOTCERT=/etc/ssl/dbpantry/ca.crt
```

Laravel's stock `config/database.php` hard-codes `'sslmode' => 'prefer'` on the
`pgsql` connection, so point it at the environment:
`'sslmode' => env('DB_SSLMODE', 'prefer')` and
`'sslrootcert' => env('DB_SSLROOTCERT')`. Its PostgreSQL connector passes both
to libpq.
Copy `certs/ca.crt` (the certificate, never `ca.key`) to the application server.
`require` encrypts but does not check who answered; `verify-full` also checks
that the certificate was issued by your CA for the name you dialled, which is
why the address or name must be in `MDB_EXTRA_SANS`.

Remember transaction pooling: the application must not rely on session state
(see the rules above). Firewalling the published port is covered in
[Operations: Exposure](../runbooks/operations.md#exposure).

## MySQL and MariaDB: pooling helps, but is not survival

The MySQL family uses **one thread per connection**, not a process. A thread is
far cheaper, so a few hundred connections is unremarkable and pooling is a
scaling aid rather than a rescue.

ProxySQL is offered because it brings more than pooling — query routing,
read/write splitting, query rules — and those are the reasons to adopt it. If
all you want is fewer connections, raise `max_connections` first and measure.

It is wired, and the interesting part is how it authenticates.

ProxySQL has **no equivalent of `auth_query`**: it cannot look a credential up
on demand, it has to hold one. That is why this pooler was left unwired at
first — holding every application password would have given up the exact
property the PostgreSQL side goes to some trouble to preserve.

What it holds is a **verifier**, not a password. On start it copies each user's
`caching_sha2_password` hash straight out of `mysql.user.authentication_string`,
so ProxySQL knows no more than the server already does, and a compromised
pooler yields what a read of that table would — not a set of usable passwords.

```
proxysql: mirrored 7 user verifier(s); no cleartext password was read
```

Two consequences worth knowing:

**It mirrors on start.** A user created after the pooler booted is not known to
it until it restarts. `make new-project` followed by
`scripts/runtime compose restart mysql-pooler` is the sequence.

**ProxySQL gets its own narrow account**, not root — root stays restricted to
the local socket. The account has exactly two privileges: `SELECT` on
`mysql.user` to read verifiers, and `REPLICATION CLIENT` for the monitor. Its
admin interface on 6032 is never published, because that interface can read
every verifier it holds.

Check 35 asserts all of it: a user connects through the pooler, what is stored
begins with the `$A$` verifier prefix and does not contain the password, the
pooler's account holds no `SUPER`, and a wrong password is still refused.

## MongoDB, Cassandra, FerretDB: the driver already does it

**Do not put a connection proxy in front of these.** Their drivers pool
natively, and a proxy actively breaks things:

**MongoDB** drivers implement pooling as a specified part of the driver
(Connection Monitoring and Pooling). The driver also monitors topology, selects
servers by latency and read preference, and retries writes on failover. A proxy
in the middle hides the topology, so all of that stops working.

**Cassandra** drivers hold connections to each node under a load-balancing
policy and multiplex thousands of concurrent requests per connection. The
driver routes each query to a replica that owns the data — token awareness —
which is where much of Cassandra's performance comes from. A proxy defeats it
by making every request appear to come from one place.

For these engines the fix is **one line of application code**, not
infrastructure:

```js
// Correct: one client for the process lifetime. The driver pools behind it.
const client = new MongoClient(uri);

// Wrong: a new client per request. This defeats the pool entirely and is the
// single most common cause of "MongoDB is slow".
async function handler() {
    const client = new MongoClient(uri);   // don't
}
```

The same rule applies to Cassandra's `Session` and FerretDB, which is reached by
MongoDB drivers.

## How this is enforced

Every engine's descriptor declares `MDB_ENGINE_POOLING=external` or `=driver`,
and check 31 requires a `driver` declaration to state *why*. An engine cannot
quietly acquire a pooler nobody thought about, and it cannot quietly go without
one either.

---

[← Docs index](../../README.md#documentation)
