# Supported versions

Which version of each engine dbpantry maintains, why those, and how to run any
other one when you need to.

## The policy

**Each engine maintains its current and its latest version: the newest supported
line, and the one before it.** A maintained version has a digest-pinned base in
[`images/bases.tsv`](../../images/bases.tsv), is built for amd64 and arm64, passes
the Trivy gate with no waiver, and runs the acceptance checks in CI.

Where upstream runs a long-term track beside a short-lived one, the two lines are
the two newest **long-term** releases. A release supported only until the next one
ships — MySQL Innovation, MariaDB rolling, MongoDB minor releases — would drop off
this table every quarter, so it is not tracked.

The reasoning is in [DESIGN.md D-57](../../DESIGN.md).

## Maintained versions

Bold is the default.

| Engine | Maintained | Upstream support ends | Not maintained here, still upstream-supported | Source |
|---|---|---|---|---|
| PostgreSQL | **17** · 18 | 17: 2029-11-08 · 18: 2030-11-14 | 15 (2027-11-11), 16 (2028-11-09) | [Versioning policy](https://www.postgresql.org/support/versioning/) |
| MySQL | **8.4** · 9.7 (LTS) | 8.4: Premier to 2029-04 · 9.7: Premier to 2031-04 | Innovation releases (26.x) | [Release model](https://dev.mysql.com/doc/refman/9.7/en/mysql-releases.html), [EOL notice](https://www.mysql.com/support/eol-notice.html) |
| MariaDB | **11.8** · 12.3 (LTS) | 11.8: 2028-06-04 · 12.3: 2029-06-12 | 10.11 (2028-02-16), 11.4 (2029-05-29), rolling 13.x | [Maintenance policy](https://mariadb.org/about/) |
| MongoDB | **7.0** · 8.0 · 9.0 | 7.0: 2027-08-31 · 8.0: 2029-10-31 · 9.0: 2031-10-31 | minor releases (8.3) | [Lifecycle schedules](https://www.mongodb.com/legal/support-policy/lifecycles) |
| Cassandra | **5.0** | until 7.0 ships | 4.1 — dropped on purpose, see below | [Downloads](https://cassandra.apache.org/_/download.html) |
| FerretDB | **2** | latest 2.x | — | [Releases](https://github.com/FerretDB/FerretDB/releases) |

Dates as published on 2026-11-01; the source is authoritative when they differ.

Three engines differ from two lines, each for a reason:

- **MongoDB** keeps 7.0 as its default beside 8.0 and 9.0. 8.x and 9.x refuse
  to start on Linux kernels that report 6.19 to 7.0.13 (SERVER-121912), and every
  Ubuntu 7.0 kernel reports 7.0.0 ([SERVER-136848](https://jira.mongodb.org/browse/SERVER-136848)),
  including GitHub's runners. 7.0 is unaffected. On a kernel reporting 7.0.14 or
  later, such as OrbStack's, choose 8.0 or 9.0. 7.0 leaves when MongoDB fixes
  the check, or on 2027-08-31.

- **Cassandra 4.1** is still maintained upstream, but it vendors snakeyaml 1.x,
  open to CVE-2022-1471, and cannot load the 2.x line that fixes it. Shipping it
  meant a permanent Trivy waiver, so it was dropped instead. It comes back when a
  4.1 release moves to snakeyaml 2.x, and none is planned: the move to 2.4
  ([CASSANDRA-20928](https://issues.apache.org/jira/browse/CASSANDRA-20928)) is
  in 6.0 only. Cassandra regains a second line when 6.0 reaches GA
  ([D-57](../../DESIGN.md)); it is alpha today.
- **FerretDB** publishes one supported line, 2.x.

One oddity: MariaDB 11.4 was the last LTS with five years of community binaries,
so it is supported upstream until 2029-05-29 — longer than 11.8, which has the
three-year term every later LTS has. The policy still tracks the two newest LTS
lines; 11.4 remains a sound unsupported choice through its override.

## Running another version

Any version off the menu still runs if you name the upstream image it comes from,
pinned by digest:

```
MDB_MARIADB_VERSION=11.4
MDB_MARIADB_BASE_IMAGE=mariadb:11.4.13@sha256:1292844148b311e4ed4300022a996d39083f415a963e970cf47cad1b3b18e3a6
```

That version is **unsupported, with no scan guarantee**: dbpantry does not build,
scan or verify it. It is your choice, and the toolkit says so every time:

- `make check-env` prints a warning on every start, and refuses the version
  outright if the override is missing — a typo cannot select an unmaintained
  engine.
- The override must be `name:tag@sha256:<digest>`; a bare tag is refused, so an
  unsupported version is still a reproducible one.
- `scripts/build` warns, and labels the image
  `io.simtabi.dbpantry.support=unsupported`.
- Cassandra keeps upstream's vendored JARs for a series `jars.tsv` has no rows
  for, and the build says that too.

| Engine | Override | Example image |
|---|---|---|
| PostgreSQL | `MDB_PG_BASE_IMAGE` | `pgvector/pgvector:0.8.6-pg16@sha256:…` |
| MySQL | `MDB_MYSQL_BASE_IMAGE` | `mysql:8.0.46@sha256:…` |
| MariaDB | `MDB_MARIADB_BASE_IMAGE` | `mariadb:11.4.13@sha256:…` |
| MongoDB | `MDB_MONGODB_BASE_IMAGE` | `mongo:8.3.11@sha256:…` — run directly, MongoDB is referenced, not rebuilt |
| Cassandra | `MDB_CASSANDRA_BASE_IMAGE` | `cassandra:4.1.12@sha256:…` |
| FerretDB | `MDB_FERRETDB_BASE_IMAGE` | `ghcr.io/ferretdb/ferretdb:2.1.0@sha256:…` |

Resolve a digest with `docker buildx imagetools inspect <image>:<tag>`.

## When a version drops off

When a new line ships, the oldest maintained one leaves the menu in the next
release, and the CHANGELOG says so. Your data is never touched: volumes are per
engine and per major, so a `.env` still naming the old version fails
`make check-env` and leaves its volume exactly as it was. Then either:

- **upgrade** — [Upgrade](../runbooks/UPGRADE.md) covers each engine, including building the
  old image once from its override so `make upgrade` can read it; or
- **stay** — keep the version and add its `MDB_<ENGINE>_BASE_IMAGE`, unsupported.

## Changing the policy for one engine

The menu is `MDB_ENGINE_VERSIONS` in `engines/<engine>/engine.conf`, and every
version on it needs a row in `images/bases.tsv`; check 31 fails otherwise. CI's
build matrix is derived from the two, so adding or removing a version is those two
edits and nothing else.

---

[← Docs index](../../README.md#documentation)
