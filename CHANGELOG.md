# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **The project is renamed dbpantry** (DESIGN.md D-54). The repository moved from
  `simtabi/multidb-server-docker` to `simtabi/dbpantry`; GitHub redirects the old URL. Images
  are now `dbpantry-<engine>`: run `make build` again, and the next `make up`
  regenerates the compose files with the new names. The `MDB_` variables, the `mdb`
  Compose project and `mdb_net` network, and the engine descriptor contract keep their names,
  so existing `.env` files, containers and volumes carry over unchanged.
- The README drops the GHCR badge and the hosted-docs link, which pointed at pages that do not
  exist yet.

### Added

- **Releases are built and published by CI.** A `vX.Y.Z` tag on `main` runs
  `release.yml`: every image is built natively on amd64 and arm64, scanned, and
  proven by the full harness on both before anything is pushed to
  `ghcr.io/simtabi/dbpantry-<engine>`, with an SBOM and build provenance attached.
  Engines are tagged `<major>-<release>`, which never moves, and `<major>`; see
  [Release](docs/release.md).
- `scripts/release-notes` prints a version's CHANGELOG section, the body of its
  GitHub release; `scripts/release-notes Unreleased` previews the next one.
- Check 44 fails on any leftover use of the old names outside the history they belong to.
- Check 45 fails if `images/cassandra/jars.tsv` has a row without a full sha256 or for a
  series that is not built, or if the Cassandra Dockerfile stops running the swap.

### Security

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
