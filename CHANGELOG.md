# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

The first release published by CI: every engine image and the cli image on GHCR,
for amd64 and arm64, each with an SBOM and build provenance. It also renames the
project to dbpantry and fixes the findings that failed the Trivy gate on every
image, replacing Cassandra's vulnerable vendored JARs rather than waiving them.

### Changed
- Markdown files use lowercase slug names (design.md D-61): `design.md`, `docs/runbooks/*.md`,
  `docs/reference/spec.md`, `kit.md` and others. Git and GitHub conventional names stay upper case:
  `README.md` (also `rls/README.md` and `scripts/runtimes/README.md`), `CHANGELOG.md`, `CONTRIBUTING.md`,
  `SECURITY.md`, `CODE_OF_CONDUCT.md`, `LICENSE`, `CLAUDE.md` and `.github/PULL_REQUEST_TEMPLATE.md`.

- **Each engine now maintains its current and latest version only** (design.md D-57,
  [Supported versions](docs/guides/versions.md)): the newest supported line and the one before it,
  confirmed from each project's own end-of-life page. The menu goes from 16 versions to 11:
  PostgreSQL 17 · 18, MySQL 8.4 · 9.7, MariaDB 11.8 · 12.3, MongoDB 7.0 · 8.0 · 9.0, Cassandra 5.0
  and FerretDB 2. A `.env` naming a version that left the menu fails `make check-env` and its
  volume is untouched; [Upgrade](docs/runbooks/upgrade.md) covers moving it, or keep it through
  `MDB_<ENGINE>_BASE_IMAGE`, unsupported.
- **The MariaDB default moves from 11.4 to 11.8.** MongoDB keeps 7.0 as its default, the
  one exception to the policy: 8.x and 9.x refuse to start on kernels reporting 6.19 to
  7.0.13 (SERVER-121912), and every Ubuntu 7.0 kernel, GitHub's runners included, reports
  7.0.0 (SERVER-136848).
- MongoDB 7.0 is repinned to 7.0.45, and 8.0 to 8.0.34.
- **The project is renamed dbpantry** (design.md D-54). The repository moved from
  `simtabi/multidb-server-docker` to `simtabi/dbpantry`; GitHub redirects the old URL. Images
  are now `dbpantry-<engine>`: run `make build` again, and the next `make up`
  regenerates the compose files with the new names. The `MDB_` variables, the `mdb`
  Compose project and `mdb_net` network, and the engine descriptor contract keep their names,
  so existing `.env` files, containers and volumes carry over unchanged.
- The README drops the GHCR badge and the hosted-docs link, which pointed at pages that do not
  exist yet.
- **Files are grouped by how they are used** (design.md D-59, [the plan](docs/reorganization-plan.md)).
  `caddy/`, `haproxy/` and `proxysql/` move under `config/`; `docs/` splits into `guides/`,
  `runbooks/` and `reference/`, and `IMAGE-PROVENANCE.md` becomes
  `docs/reference/image-provenance.md`. Every link and path is updated. A checkout with
  rendered files in the old `caddy/` regenerates them on the next `make up`.
- `make verify` takes `CHECKS=21,28` and `TAGS=pg,backup`; `VERIFY_TAGS` accepts several tags,
  and a filter that matches nothing fails rather than reporting green.
- design.md: the second D-51 (vulnerability waivers) is renumbered D-58, and D-23 and
  D-52 to D-57, which sat after the end of the document, are back in the decision log.

### Added
- **Apple container (macOS 26) through socktainer** (design.md D-63,
  [Apple container](docs/guides/runtime-apple-container.md)): detected as a Docker provider;
  `compose.apple.yml` sizes each engine's VM and is required there; builds pass `--load`.
- **A deployment backend layer** (design.md D-62, [scripts/backends](scripts/backends/README.md)):
  `MDB_BACKEND=compose` (the default, and the only backend so far) selects how the stack is deployed,
  separately from `CONTAINER_RUNTIME`. Scripts reach databases through service-level verbs
  (`mdb_svc_exec`, `mdb_be_running`, ...) instead of `docker compose exec`, and `scripts/runtime` gains
  `backend`, `up`, `down`, `status`, `logs` and `exec`. Behaviour on Compose is unchanged (check 52).
- **dbpantry runs on any OCI runtime with Compose** (design.md D-60,
  [Container runtimes](docs/guides/runtimes.md)): Docker, OrbStack, Colima, Rancher Desktop,
  Podman (rootless included) and nerdctl.
  - `CONTAINER_RUNTIME=auto|docker|podman|nerdctl` chooses the runtime and `MDB_COMPOSE`
    overrides the Compose command.
  - `make runtime` / `scripts/runtime info` shows what was detected.
  - There are guides for [Docker and the Docker-API products](docs/guides/runtime-docker.md),
    [Podman](docs/guides/runtime-podman.md) and [nerdctl](docs/guides/runtime-nerdctl.md).
  - Runtimes are pluggable adapters in `scripts/runtimes/` (interface in `scripts/runtimes/README.md`); a new backend is a new file. Kubernetes, LXC/Incus and Apple's `container` tool are planned as follow-up adapters.
  - The Podman adapter refuses a rootless connection on a rootful podman machine, because `podman compose` would use the other container store.
- Checks 48 (runtime detection, make's project name, no direct `docker` calls), 49
  (fully-qualified image references) and 50 (quoted env values, and readers that unquote them).
- `scripts/env-lib.sh`: one dotenv reader and writer. A value with whitespace or a special
  character is written double-quoted (`TENANTS="landlord panel api socket queue"`), and every
  script, and make, reads it back without the quotes.

- **`MDB_<ENGINE>_BASE_IMAGE` runs a version that is not maintained**, from its upstream image
  pinned by digest. `check-env` refuses an off-menu version without it and a tag without a
  digest, and warns on every start that the version is unsupported, with no scan guarantee;
  `scripts/build` labels the image `io.simtabi.dbpantry.support=unsupported`.
- MariaDB 12.3 (12.3.3) and MongoDB 9.0 (9.0.4).
- [Supported versions](docs/guides/versions.md): the maintained versions per engine, their upstream
  end-of-life dates and sources, and how to run any other.
- [Upgrade](docs/runbooks/upgrade.md) documents moving off a version that left the menu, and the
  manual MongoDB 7.0 → 8.0 and Cassandra 4.1 → 5.0 migrations, each rehearsed with the old
  volume left intact.
- **Releases are built and published by CI.** A `vX.Y.Z` tag on `main` runs
  `release.yml`: every image is built natively on amd64 and arm64, scanned, and
  proven by the full harness on both before anything is pushed to
  `ghcr.io/simtabi/dbpantry-<engine>`, with an SBOM and build provenance attached.
  Engines are tagged `<major>-<release>`, which never moves, and `<major>`; see
  [Release](docs/guides/release.md).
- `scripts/release-notes` prints a version's CHANGELOG section, the body of its
  GitHub release; `scripts/release-notes Unreleased` previews the next one.
- Check 44 fails on any leftover use of the old names outside the history they belong to.
- Check 45 fails if `images/cassandra/jars.tsv` has a row without a full sha256 or for a
  series that is not built, or if the Cassandra Dockerfile stops running the swap.
- Check 46 fails on a relative Markdown link that points at nothing.
- Check 47 fails on a compose bind-mount source, build context or dockerfile that does not
  exist and is not a known generated file.

### Fixed
- The verify harness leaked one network per run of checks 12 and 33: it removed the network before
  the container still attached to it. Networks are now removed last (`track_network`), and check 01
  rejects the old pattern.
- **`make` ignored `COMPOSE_PROJECT_NAME` in `.env`.** The Makefile exported a bare `mdb`
  default, which beats `.env` in Compose, so a second checkout drove the first checkout's
  containers and data volumes. The name is now read from the env file. `scripts/destroy`,
  `upgrade` and `check-env` resolve it the same way when run directly.
- Image references are fully qualified (`docker.io/library/mysql:…`), so Podman resolves them
  without a search registry.
- Images built with Podman keep their HEALTHCHECK (`--format docker`).
- `make scan` no longer mounts the Docker socket into Trivy. It scans a saved image tarball.
- `check-env` refuses Caddy's ports 80/443 on a rootless runtime and names the fix.
- Check 41's scan for unguarded `rm -f` could never match. It now does.

- `make upgrade` mounted PostgreSQL 18 at `/var/lib/postgresql/data`, which 18 no longer
  uses, so an upgrade to 18 restored into an anonymous volume. Each side now mounts at its own
  path, and the summary says to set `MDB_PG_DATA_MOUNT`.
- A compose build of a non-default version used the Dockerfile's default base; the generated
  compose file now passes that version's pinned base.
- With `MDB_MONGODB_VERSION` set to a non-default version, compose still ran the default
  version's image against that version's volume. The image now follows the version.
- Check 29 removed the default project's `mdb_pg17_data` volume when it started; it now runs
  under a project of its own.
- `docs/upgrade.md` said `make upgrade` handled MongoDB and Cassandra; it handles PostgreSQL,
  MySQL and MariaDB only.
- Check 27 (HA failover) could stop without a message when one Patroni probe failed, such as
  `scripts/ha leader` in the seconds after the leader is killed, because the check runs under
  `set -e` with pipefail. Its polls now retry within their budget, so it fails only when no
  new leader is elected in time.
- `make up PROFILES=ferretdb` failed inside compose on a missing `images/ferretdb/Dockerfile`.
  FerretDB is declared but not built yet: `gen-compose` now emits no service for an engine
  without its image, and `check-env` refuses the profile by name.
- Checks 21, 22 and 28 left their dumps in `backups/`, their `roundtrip` databases, and the
  readonly secrets of the projects they created. They now remove what they made and nothing
  else, and 21 and 22 require fresh globals and checksums rather than any on disk.
- Comments claimed the workflows pin actions by commit; they pin by tag. CONTRIBUTING named
  Renovate; the repository uses Dependabot. `docs/reference/acceptance.md` said the
  repository had no remote.
- **`make backup-all` from the host backed up only the first database of each engine.** Each
  dump is a `docker compose exec`, which read the rest of the database list from stdin. The
  list is now read before dumping, and an engine with only system schemas says so.
- **Backup retention deleted every file in `backups/` older than the retention window**, not
  just dumps: notes, an `.env` copy, `.DS_Store`. It now prunes only
  `<engine>_<db>_<timestamp>` artefacts.
- `make verify-backups` failed good MySQL restores as "tables but no rows": the row count read
  `information_schema.tables.table_rows`, an estimate MySQL 8 caches. It now counts exactly.
- Check 22 relied on databases check 21 left behind; it seeds its own, and asserts every seeded
  database is dumped and that retention spares non-dump files.

### Removed

- **Cassandra 4.1** (design.md D-57). It vendors snakeyaml 1.x, open to CVE-2022-1471, and
  cannot load the 2.x line that fixes it. D-57 records when it comes back.
- PostgreSQL 15 and 16, MySQL 8.0, MariaDB 10.11 and 11.4, MongoDB 8.3 from the
  maintained menu. Each still runs through `MDB_<ENGINE>_BASE_IMAGE`, unsupported.

### Security

- **The last Trivy waiver for Cassandra is gone**: CVE-2022-1471 in snakeyaml 1.33 left with
  Cassandra 4.1, rather than being extended past 2026-11-09. `.trivyignore.yaml` has no
  Cassandra entries.
- **The Cassandra images replace their vulnerable vendored JARs** with fixed releases from
  Maven Central, pinned by sha256 (design.md D-55): guava 33.7.2-jre, snappy-java 1.1.10.12,
  jackson 2.22.4, lz4-java 1.12.1 and netty 4.1.139.Final on both series, plus logback 1.2.13
  and snakeyaml 1.33 on 4.1. This fixes the guava and snappy-java findings that failed the
  Trivy gate, and removes 21 of the 22 Cassandra waivers. The one left, CVE-2022-1471 in
  snakeyaml on 4.1, is fixed only in snakeyaml 2.x, which Cassandra 4.1 cannot load; it left
  with 4.1 (above).
- **The Go toolchain moves to 1.27.3**, the latest stable release, with `x/net` at
  v0.60.0, for gosu, rclone and postgres_exporter. This fixes CVE-2026-111943 in
  `crypto/x509`, and CVE-2026-78667, CVE-2026-78669 and CVE-2026-97031, which
  failed the Trivy gate on every engine image.
- **gosu, rclone and postgres_exporter are rebuilt from their release tags on a
  patched Go toolchain**, with gRPC, `x/net` and `x/crypto` raised past their
  fixes (design.md D-51). This removes 26 Trivy waivers that were due to expire
  on 2026-11-09 and fail every build. The toolchain is pinned in
  `images/bases.tsv`, and check 02 fails if a Dockerfile's `GO_IMAGE` default
  drifts from it.
- **The cassandra 4.1 image upgrades its python3.11 packages** to
  3.11.2-6+deb12u9, the same named-package pattern used for perl.

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


- Repository scaffold per `docs/spec.md` section 15.
- The complete `make verify` acceptance harness, written before the features it
  checks so every subsequent phase has something to heal against.
- `design.md`: pinned versions with digests, the arm64 extension matrix, the
  capacity truth table, and the decision log.
- `docs/spec.md` section 21 (scale, HA, sync) as an approved amendment.
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
  (see `design.md` D-24).
- PostgreSQL now shuts down cleanly under s6 via a per-service `down-signal`,
  so the next boot performs no crash recovery (D-09, D-27).
- `pg_graphql`'s packaged symlinks are flattened, which also protects against
  the PG 18 volume mount shadowing them (D-26).

[Unreleased]: https://github.com/simtabi/dbpantry/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/simtabi/dbpantry/releases/tag/v0.1.0
