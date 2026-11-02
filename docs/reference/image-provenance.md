# Image provenance

Every image this toolkit runs, where it comes from, and why.

The rule: **use the upstream project's own image, and prefer a Docker Official
Image where one exists.** A third-party rebuild is a party that can be
compromised independently of the project it packages, and one whose update
cadence you do not control.

Check 42 enforces this. An image outside the allowed namespaces fails the build
unless it carries a justification here.

## Docker Official Images

Maintained by Docker in partnership with upstream, on the library namespace.

| Image | Used for |
|---|---|
| `postgres` (via `pgvector/pgvector`) | PostgreSQL — see below |
| `mysql` | MySQL engine base |
| `mariadb` | MariaDB engine base |
| `mongo` | MongoDB (referenced, not rebuilt — see [licensing](../guides/licensing.md)) |
| `cassandra` | Cassandra engine base; vulnerable vendored JARs replaced from Maven Central (see below) |
| `debian` | the `cli` image base |
| `golang` | build stage only: rebuilds gosu, rclone and postgres_exporter from their upstream tags ([D-51](../../design.md)); never shipped |
| `caddy` | UI front door |
| `haproxy` | HA routing |
| `adminer` | SQL UI |
| `phpmyadmin` | MySQL-family UI |

## Upstream project images

Not on the library namespace, but published by the project itself.

| Image | Publisher | Why not an official image |
|---|---|---|
| `pgvector/pgvector` | the pgvector project | No official Postgres image ships pgvector. This is Postgres official plus the extension, by the extension's own authors. |
| `dpage/pgadmin4` | the pgAdmin team | pgAdmin's own image; there is no library pgAdmin. |
| `proxysql/proxysql` | the ProxySQL project | ProxySQL's own image. |
| `ghcr.io/ferretdb/ferretdb` | the FerretDB project | FerretDB's own image. |
| `ghcr.io/ferretdb/postgres-documentdb` | the FerretDB project | The PostgreSQL build FerretDB requires. |
| `quay.io/coreos/etcd` | the etcd project | etcd publishes to quay, not Docker Hub. |
| `quay.io/prometheuscommunity/postgres-exporter` | Prometheus Community | The exporter's own publisher. |
| `prom/mysqld-exporter` | Prometheus | The exporter's own publisher. |
| `aquasec/trivy` | Aqua Security | The scanner's own image. Pinned by version; the `trivy-action` GitHub Action is deliberately NOT used — 75 of its 76 tags were force-pushed in a 2026 supply-chain attack. |

## Justified exceptions

**None.** pgBouncer was the single third-party image here; it is now built from
a Docker Official base plus the PGDG package by `images/pgbouncer/Dockerfile`,
so nothing in this toolkit depends on a community rebuild.

The vendor's own `pgbouncer/pgbouncer` is not used because it is amd64-only and
stuck at 1.15.0 against a current 1.25.x — it would break the arm64 half of the
matrix and ship a years-old pooler. Building from PGDG gets the current version
on both architectures from the PostgreSQL project itself.

## Libraries replaced inside an image

One image changes what its base ships. Cassandra vendors its Java dependencies
in `lib/`, and where those are below their security fixes with no newer
Cassandra release to repin to, the image replaces them
([D-55](../../design.md)).

| Image | Replaced from | Pinned in | Checked by |
|---|---|---|---|
| `dbpantry-cassandra` | Maven Central, each library's own published release | [`images/cassandra/jars.tsv`](../../images/cassandra/jars.tsv), by sha256 | `sha256sum` at build, check 45 |

A JAR is only replaced when the fixed release is still binary-compatible with
the Cassandra series that loads it. snakeyaml 2.x is not, for 4.1, which is why
4.1 is no longer maintained here ([D-57](../../design.md)) rather than shipped under a
waiver. `.trivyignore.yaml` has no Cassandra entries.

## Pinning

Every reference is pinned by **digest**, not tag, so a moved tag cannot change
what runs. Engine bases live in [`images/bases.tsv`](../../images/bases.tsv); check 02
fails the build on any unpinned reference or floating tag. The JARs replaced
inside the Cassandra image are pinned by sha256 in `images/cassandra/jars.tsv`.

`images/bases.tsv` holds maintained versions only ([Supported versions](../guides/versions.md)).
An older version runs from `MDB_<ENGINE>_BASE_IMAGE`, which must also be
digest-pinned — `make check-env` refuses a tag — but its provenance is yours to
judge: this project does not build, scan or verify it.

---

[← Docs index](../../README.md#documentation)
