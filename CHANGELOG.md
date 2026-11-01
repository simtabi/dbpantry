# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **Each engine now maintains its current and latest version only** (DESIGN.md D-57,
  [Supported versions](docs/versions.md)): the newest supported line and the one before it,
  confirmed from each project's own end-of-life page. The menu goes from 16 versions to 11:
  PostgreSQL 17 · 18, MySQL 8.4 · 9.7, MariaDB 11.8 · 12.3, MongoDB 7.0 · 8.0 · 9.0, Cassandra 5.0
  and FerretDB 2. A `.env` naming a version that left the menu fails `make check-env` and its
  volume is untouched; [Upgrade](docs/UPGRADE.md) covers moving it, or keep it through
  `MDB_<ENGINE>_BASE_IMAGE`, unsupported.
- **The MariaDB default moves from 11.4 to 11.8.** MongoDB keeps 7.0 as its default, the
  one exception to the policy: 8.x and 9.x refuse to start on kernels reporting 6.19 to
  7.0.13 (SERVER-121912), and every Ubuntu 7.0 kernel, GitHub's runners included, reports
  7.0.0 (SERVER-136848).
- MongoDB 7.0 is repinned to 7.0.45, and 8.0 to 8.0.34.

- **The project is renamed dbpantry** (DESIGN.md D-54). The repository moved from
  `simtabi/multidb-server-docker` to `simtabi/dbpantry`; GitHub redirects the old URL. Images
  are now `dbpantry-<engine>`: run `make build` again, and the next `make up`
  regenerates the compose files with the new names. The `MDB_` variables, the `mdb`
  Compose project and `mdb_net` network, and the engine descriptor contract keep their names,
  so existing `.env` files, containers and volumes carry over unchanged.
- The README drops the GHCR badge and the hosted-docs link, which pointed at pages that do not
  exist yet.

### Added

- **`MDB_<ENGINE>_BASE_IMAGE` runs a version that is not maintained**, from its upstream image
  pinned by digest. `check-env` refuses an off-menu version without it and a tag without a
  digest, and warns on every start that the version is unsupported, with no scan guarantee;
  `scripts/build` labels the image `io.simtabi.dbpantry.support=unsupported`.
- MariaDB 12.3 (12.3.3) and MongoDB 9.0 (9.0.4).
- [Supported versions](docs/versions.md): the maintained versions per engine, their upstream
  end-of-life dates and sources, and how to run any other.
- [Upgrade](docs/UPGRADE.md) documents moving off a version that left the menu, and the
  manual MongoDB 7.0 → 8.0 and Cassandra 4.1 → 5.0 migrations, each rehearsed with the old
  volume left intact.

- Check 44 fails on any leftover use of the old names outside the history they belong to.
- Check 45 fails if `images/cassandra/jars.tsv` has a row without a full sha256 or for a
  series that is not built, or if the Cassandra Dockerfile stops running the swap.

### Fixed

- `make upgrade` mounted PostgreSQL 18 at `/var/lib/postgresql/data`, which 18 no longer
  uses, so an upgrade to 18 restored into an anonymous volume. Each side now mounts at its own
  path, and the summary says to set `MDB_PG_DATA_MOUNT`.
- A compose build of a non-default version used the Dockerfile's default base; the generated
  compose file now passes that version's pinned base.
- With `MDB_MONGODB_VERSION` set to a non-default version, compose still ran the default
  version's image against that version's volume. The image now follows the version.
- Check 29 removed the default project's `mdb_pg17_data` volume when it started; it now runs
  under a project of its own.
- `docs/UPGRADE.md` said `make upgrade` handled MongoDB and Cassandra; it handles PostgreSQL,
  MySQL and MariaDB only.
- Check 27 (HA failover) could stop without a message when one Patroni probe failed, such as
  `scripts/ha leader` in the seconds after the leader is killed, because the check runs under
  `set -e` with pipefail. Its polls now retry within their budget, so it fails only when no
  new leader is elected in time.

### Removed

- **Cassandra 4.1** (DESIGN.md D-57). It vendors snakeyaml 1.x, open to CVE-2022-1471, and
  cannot load the 2.x line that fixes it. D-57 records when it comes back.
- PostgreSQL 15 and 16, MySQL 8.0, MariaDB 10.11 and 11.4, MongoDB 8.3 from the
  maintained menu. Each still runs through `MDB_<ENGINE>_BASE_IMAGE`, unsupported.

### Security

- **The last Trivy waiver for Cassandra is gone**: CVE-2022-1471 in snakeyaml 1.33 left with
  Cassandra 4.1, rather than being extended past 2026-11-09. `.trivyignore.yaml` has no
  Cassandra entries.

- **The Cassandra images replace their vulnerable vendored JARs** with fixed releases from
  Maven Central, pinned by sha256 (DESIGN.md D-55): guava 33.7.2-jre, snappy-java 1.1.10.12,
  jackson 2.22.4, lz4-java 1.12.1 and netty 4.1.139.Final on both series, plus logback 1.2.13
  and snakeyaml 1.33 on 4.1. This fixes the guava and snappy-java findings that failed the
  Trivy gate, and removes 21 of the 22 Cassandra waivers. The one left, CVE-2022-1471 in
  snakeyaml on 4.1, is fixed only in snakeyaml 2.x, which Cassandra 4.1 cannot load; it still
  expires on 2026-11-09.
- **The Go toolchain moves to 1.27.3**, the latest stable release, for gosu, rclone
  and postgres_exporter. This fixes CVE-2026-111943 in `crypto/x509`, which failed the Trivy
  gate on every engine image.
- **gosu, rclone and postgres_exporter are rebuilt from their release tags on a
  patched Go toolchain**, with gRPC, `x/net` and `x/crypto` raised past their
  fixes (DESIGN.md D-51). This removes 26 Trivy waivers that were due to expire
  on 2026-11-09 and fail every build. The toolchain is pinned in
  `images/bases.tsv`, and check 02 fails if a Dockerfile's `GO_IMAGE` default
  drifts from it.
- **The Go toolchain moves to 1.27.2 and `x/net` to v0.60.0** for gosu, rclone
  and postgres_exporter, fixing CVE-2026-78667, CVE-2026-78669 and
  CVE-2026-97031, which failed the Trivy gate on every engine image.
- **The cassandra 4.1 image upgrades its python3.11 packages** to
  3.11.2-6+deb12u9, the same named-package pattern used for perl.
- **lz4-java 1.10.1, vendored by Cassandra, is waived for CVE-2026-106451**
  until 2026-11-12: neither 4.1.12 nor 5.0.9 has a release carrying the fix.

## [0.1.0] - 2026-08-10

First public release.

Six database engines behind one toolkit — PostgreSQL, MySQL, MariaDB, MongoDB,
Cassandra and FerretDB — each version-switchable, authenticated by default, with
verified backups, point-in-time recovery, connection pooling and a rehearsal HA
topology. 42 acceptance checks; every claim below is one of them.

### Added

- **Six engines from one descriptor model.** An engine is a declarative
  `engine.conf` plus family hooks; adding one to an existing family costs a
  single file. Nothing branches on an engine name.
- **Authenticated by default**, which several engines are not: MongoDB ships
  with no authentication and Cassandra with `AllowAllAuthenticator` *and*
  `AllowAllAuthorizer`. Check 32 asserts every engine refuses anonymous access.
- **Point-in-time recovery** for PostgreSQL (pgBackRest) and the MySQL family
  (binary logs), proven by recovering to a moment *between* two writes.
- **Connection pooling that stores no application password** — pgBouncer
  resolves credentials with `auth_query`; ProxySQL mirrors password verifiers,
  never cleartext.
- **Verified backups**: `make verify-backups` restores the latest set into
  throwaway containers and asserts the row counts, because an unrestored backup
  is a hypothesis.
- **High availability** — Patroni, a three-node etcd quorum and HAProxy, with a
  killed leader replaced in ~20s and the write port following it.
- **Row-level security kit**, proven to isolate tenants including from the table
  owner.
- **Exposure is a decision**: `MDB_PUBLISH=none` by default, so no host port is
  bound and nothing can collide; `direct` and `proxy` publish from a single
  configurable base.
- **Supply chain**: every image official, upstream, or justified in
  `IMAGE-PROVENANCE.md`; every reference digest-pinned; trivy in CI with
  waivers that must carry a reason and a revisit condition.


- Repository scaffold per `docs/SPEC.md` section 15.
- The complete `make verify` acceptance harness, written before the features it
  checks so every subsequent phase has something to heal against.
- `DESIGN.md`: pinned versions with digests, the arm64 extension matrix, the
  capacity truth table, and the decision log.
- `docs/SPEC.md` section 21 (scale, HA, sync) as an approved amendment.
- `scripts/check-env`, enforcing sentinel rejection, the `_FILE` convention,
  supported version menus, port-collision detection, and the prod guards
  (mandatory pooling, UI basic auth, 3-node etcd quorum).
- CI workflows for static analysis and the amd64/arm64 verify matrix.

- `multidb-server-pg`: PostgreSQL 15–18 with the full extension suite (vector,
  PostGIS, pg_cron, pgaudit, pg_repack, pg_partman, pgtap, http, hypopg,
  pg_graphql, pg_net, pgsodium, pgjwt), supervised by s6-overlay v3.
- Ordered s6 init: permissions, configuration, certificates, provisioning, then
  the engine — wrapping the official entrypoint rather than replacing it.
- Multi-project triplet provisioning with cross-project access denied.
- `make init`, `certs`, `build`, `up`, `down`, `status`, `logs`, `psql`.
- `docker-compose.yml` with the `pg` and `ui` profiles and per-major volumes.
- `multidb-server-mysql` (8.0/8.4/9.7) and `multidb-server-mariadb` (10.11/11.4/11.8),
  both s6-supervised with baked utf8mb4 / `utf8mb4_unicode_ci` / explicit
  `sql_mode`, triplet provisioning, and the same tool suite.
- phpMyAdmin serving both MySQL-family servers from one instance via
  `PMA_HOSTS`, and `make mysql` / `make mariadb` client shells.

### Fixed

- All shell is bash 3.2 compatible, so the toolkit runs on stock macOS
  (see `DESIGN.md` D-24).
- PostgreSQL now shuts down cleanly under s6 via a per-service `down-signal`,
  so the next boot performs no crash recovery (D-09, D-27).
- `pg_graphql`'s packaged symlinks are flattened, which also protects against
  the PG 18 volume mount shadowing them (D-26).

[Unreleased]: https://github.com/simtabi/dbpantry/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/simtabi/dbpantry/releases/tag/v0.1.0
