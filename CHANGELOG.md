# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

The first release published by CI: the GitHub release, with the source tarball
and its provenance, once every engine image and the cli image has built and passed
the Trivy gate on amd64 and arm64. Container images are no longer published. It
also renames the project to dbpantry and fixes the findings that failed the Trivy
gate on every image, replacing Cassandra's vulnerable vendored JARs rather than
waiving them.

### Upgrade

- **Images are now named `localhost/dbpantry-*`.** Images you built before keep their old
  `ghcr.io/simtabi/dbpantry-*` names, which nothing runs any more. Run `make build` once; the next
  `make up` recreates the containers on the new names. Volumes and data are untouched. To build,
  run and deploy under your own registry instead, export `MDB_IMAGE_PREFIX` (for example
  `registry.example.com/team`) before `make build` and `make up` (design.md D-78).
- **Prod: PostgreSQL's own port stops being published.** With the prod overlay applied (see Changed), a prod
  stack under `MDB_PUBLISH=direct` publishes the pooler, `pg-pooler` on `MDB_PORT_BASE + 100` (54100 by
  default), and no engine port: 54000 and the other engines' ports go away. Point applications at 54100 with
  `sslmode=verify-full` and `sslrootcert` set to a copy of `certs/ca.crt` (docs/guides/pooling.md). An env
  file made before this needs `make prod-overlay ENV_FILE=...` once; `check-env` refuses the prod boot until
  then. `MDB_PUBLISH_ENGINES=true` keeps the engine ports, deprecated for prod.
- **Certificates:** run `make certs-renew` to add the `pg-pooler` SAN and IP SANs for addresses in
  `MDB_EXTRA_SANS`, then restart `pg-pooler`.

### Changed
- **Container images are no longer published.** A release publishes the GitHub release only, with
  the notes and the source tarball (owner, 2026-11-13). The `ghcr.io/simtabi/dbpantry-*` images are
  frozen: no new tag is pushed and nothing is deleted from ghcr.io. Build the images with
  `make build` (design.md D-77).
- **Images are built, run and deployed as `localhost/dbpantry-<component>:<tag>`,** and nothing
  falls back to a remote registry. One default, `MDB_IMAGE_PREFIX=localhost` in
  `scripts/env-lib.sh`, replaces every hard-coded registry: Compose, the generated engine and pooler
  services, the patroni build, the Makefile, `scripts/{build,upgrade,verify-backups,transfer,scan}`,
  the verify checks and the CI scan steps. The Helm chart's `global.imageRegistry` defaults to
  `localhost`, `make up` on Kubernetes passes it the prefix the images were built under, and kind
  loading follows it; `imagePullPolicy` stays `IfNotPresent`. Setting `MDB_IMAGE_PREFIX` (or
  `global.imageRegistry` when installing the chart by hand) prefixes every image. The Kubernetes
  guide shows `kind load docker-image`, `minikube image load` and pushing to your own registry.
  Check 73 fails if any default names a remote registry or a set prefix stops reaching every image,
  and check 49 now resolves every `${VAR:-default}` in an image reference. Checks 42 and 72 recognise
  our images as `localhost/dbpantry-*`, and `make init-prod` resolves the caddy image the way Compose
  does (`mdb_compose_ref`) before running it (design.md D-78).
- **A release runs on a pushed `vX.Y.Z` tag, or on request for a tag already on GitHub:**
  `gh workflow run release.yml --repo simtabi/dbpantry -f tag=vX.Y.Z`. This replaces the dispatch
  gated on `-f confirm=release-go` (D-76). Every job checks out the tag, and the first step refuses
  anything but a version tag that exists on GitHub (docs/guides/release.md, design.md D-77).
- **`make dbpantry` writes `build/<os>/dbpantry`** (`build/macos/dbpantry`, `build/linux/dbpantry`),
  the org's build layout: one folder per OS and nothing else in `build/`. It wrote `build/dbpantry`
  before; delete that file if you built one. Check 67 fails on anything else in `build/`, and the
  binary finds its checkout from either location.
- **The prod overlay is applied** (design.md D-73). Nothing put `compose.prod.yml` in `COMPOSE_FILE`, so a prod
  `make up` applied none of it. `make init-prod` now writes `COMPOSE_FILE` with the overlay and a generated
  `compose.prod.engines.yml` that covers every engine and pooler; `check-env` refuses a prod boot without them
  and names `make prod-overlay`, and detects `prod` from `PROFILES=` as well as `MDB_PROFILES`.
  `MDB_PUBLISH_ENGINES=true` is the deprecated opt-out.
- **pgBouncer serves client TLS** (design.md D-69). It reuses the certificate `make certs` issues for
  PostgreSQL, so a client trusting `certs/ca.crt` can use `sslmode=verify-full` against `pg-pooler`.
  Under `MDB_TLS_ENFORCE=true` (the prod profile) it sets `client_tls_sslmode = require`, refusing
  plaintext clients, and `server_tls_sslmode = require`, so the hop to PostgreSQL can no longer fall
  back to plaintext; it refuses to start with no certificate mounted. Otherwise it sets
  `client_tls_sslmode = prefer` and plaintext still connects. Before this the pooler accepted
  plaintext only, so an application connecting from another host with `sslmode=require` could not
  connect. Check 68 proves both modes; no new `MDB_` variable.
- `scripts/certs` adds `DNS:pg-pooler` to the PostgreSQL certificate, and an `MDB_EXTRA_SANS` entry
  that is an IP address now also gets an `IP:` SAN (it was written as `DNS:` only). Run
  `make certs-renew` to re-issue existing certificates; restart `pg-pooler` afterwards, since it
  copies the certificate at start (`certs-renew` says so when it is running).
- docs/runbooks/operations.md covers an application on another host (direct publish of the pooler
  on a private `MDB_BIND_ADDR`) and a firewall that works with Docker's DNAT, matching
  `DOCKER-USER` on `--ctorigdstport`; docs/guides/pooling.md gives the client settings.
- The CI build matrix comes from `dbpantry manifest --matrix versions`; `scripts/ci-matrix` stays
  and check 62 holds the two byte-identical.
- `scripts/helm-values` and the Kubernetes backend read the engine list from the descriptors
  (every implemented engine) instead of listing five engines each. The list is the same five.
- The `.env.example` note on published ports said BASE plus the engine's own port; it is BASE
  plus the descriptor's offset, which is what `gen-compose` has always done.
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
- **Mutual TLS through the pooler** (design.md D-74). Under `MDB_MTLS=true` pgBouncer requires a toolkit-CA client
  certificate (`client_tls_sslmode = verify-full`), presents its own (`certs/pooler/client.crt`) to PostgreSQL and
  verifies PostgreSQL's by name, and PostgreSQL admits the pooler from its fixed address with `clientcert=verify-ca`
  while every other client keeps `verify-full`. `scripts/certs --client NAME` issues role certificates,
  `make new-project` issues one per role under mTLS, and `make certs-renew` re-issues all of them. Check 68 covers
  it. A fresh volume under `MDB_MTLS` now gets the client-certificate rules from its first boot.
- **The `dbpantry` binary, phase 1: the engine manifest** (design.md D-68,
  [Engine manifest](docs/reference/manifest.md)). A Go module in `src/`, built from source with
  `make dbpantry`; nothing releases it yet. `dbpantry manifest` publishes every descriptor as
  versioned JSON (`schema_version` 1), read with a strict parser of the descriptor subset that
  refuses anything bash would interpret. `dbpantry endpoints` reports each engine's network,
  published and pooled endpoints (superusers never through a pooler), its boot budget, and its
  principals as `/run/secrets` paths, never values. `dbpantry fixture up|down` boots disposable
  engines on tmpfs for a test suite. Make targets: `dbpantry`, `manifest`, `endpoints`,
  `fixture-up`, `fixture-down`, `go-ci`, `go-lint`. The descriptors stay the source and every
  script works without the binary.
- **The paradigm vocabulary is mesobia's eight values**: relational, document, wide-column,
  key-value, graph, search, vector and time-series (check 31 accepted four), plus an optional
  `MDB_ENGINE_PARADIGM_ALSO` for an engine that serves more than one.
- **The test overlay covers MongoDB and Cassandra** (`compose.test.yml`): their data is on tmpfs
  with no named volume. FerretDB joins it with its image.
- Checks 62 (the manifest equals what `engine-lib.sh` loads, key by key, and generates the same CI
  matrix), 63 (no engine list written out in the files phase 1 derives), 64 (a fixture leaves no
  volume, on a running stack), 65 (the Helm chart restates every descriptor fact exactly) and 66
  (no fixture service mounts a volume, from the Compose config).
- **Kubernetes** (design.md D-64, [Kubernetes](docs/guides/kubernetes.md)): `MDB_BACKEND=kubernetes`
  deploys PG, MySQL, MariaDB, MongoDB and Cassandra with the Helm chart in `deploy/helm/dbpantry`,
  configured from `.env` (`scripts/helm-values`) and deployed only to `MDB_K8S_CONTEXT`. `make up`,
  `down`, `psql`, `backup`, `restore` and `destroy` work through `kubectl exec`; volumes outlive
  `make down`; `scripts/runtime forward` port-forwards the engines. Tested on OrbStack and kind.
- **PostgreSQL HA and scheduled backups on Kubernetes** (design.md D-65, [Kubernetes](docs/guides/kubernetes.md#high-availability)):
  the `ha` profile runs Patroni with the Kubernetes API as its DCS (no etcd or HAProxy), or a
  CloudNativePG Cluster with `MDB_K8S_HA_PROVIDER=cnpg`. Either way `pg` follows the primary and `pg-ro`
  the replicas. The `backup` profile adds one CronJob per engine (pg, mysql, mariadb) that writes to
  its own claim and prunes only there. Tested on OrbStack and kind, CloudNativePG 1.30.1 included.
- **Kubernetes operators for MySQL, MariaDB, MongoDB and Cassandra: plan only** (design.md D-67,
  [Operators](docs/guides/operators.md)): the pattern and candidate operators, with a commented
  `operators:` stub in the chart's values.yaml. Nothing reads it yet.
- **Incus through incus-compose, untested** (design.md D-66, [Incus](docs/guides/incus.md)): `MDB_BACKEND=incus`
  runs the same Compose files on Incus system containers with incus-compose. Built from the incus-compose
  documentation and checked against stub binaries only (check 58); it has not been run on a live Incus host.
- **Apple container (macOS 26) through socktainer** (design.md D-63,
  [Apple container](docs/guides/runtime-apple-container.md)): detected as a Docker provider;
  `compose.apple.yml` sizes each engine's VM and is required there; builds pass `--load`.
- **A deployment backend layer** (design.md D-62, [scripts/backends](scripts/backends/README.md)):
  `MDB_BACKEND=compose` (the default) selects how the stack is deployed,
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
- **The prod overlay's `pgbouncer` service is now the HA pooler** (design.md D-72). It had no credential and
  pointed at `pg`, so wherever the overlay applied it restarted forever beside `pg-pooler`. It is defined in
  `compose.ha.yml` in front of HAProxy's write port (`make up PROFILES=ha,pooler`), and the overlay entry only
  hardens it. Patroni nodes converge the pooler's role and `pgbouncer.get_auth()` when they become primary,
  through the same script the standalone image now uses, so an HA stack can pool at all. New:
  `MDB_HA_POOLER_HOST_PORT`, `MDB_PGBOUNCER_CPU_LIMIT`, `MDB_PGBOUNCER_MEM_LIMIT`. The pgBouncer image now
  reports healthy only while listening.
- **`MDB_BACKEND=incus` failed on its first live host.** Run on 2026-11-04 on Ubuntu 26.04
  (arm64) with Incus 7.0.2 and incus-compose 1.3.5, the `pg` and `ui` profiles now come up with
  every service healthy, and `status`, `logs`, exec, `backup ENGINE=pg` and data across `down` and
  `up` work. Six defects the stub-only check 58 could not see, each now pinned by it:
  - `up` refused the network ("ipam gateway cannot be empty"). A new `compose.incus.yml`, which
    only the Incus backend loads, names the gateway: `MDB_NETWORK_GATEWAY`, else the subnet's
    first host. Compose is unchanged.
  - `exec -u postgres` failed: incus-compose takes a numeric uid only. The backend resolves a
    name inside the instance and refuses an unknown one before running anything.
  - pgAdmin exited at boot: incus-compose wrote its secret at `/pgadmin_password.txt`. The
    secret's target is now the absolute `/run/secrets/pgadmin_password.txt`, the same file on
    Compose.
  - `make destroy` on Incus would have deleted the PITR repository with the data: its volume is
    `vol-pg-pitr`, which the recovery rule did not match. `*-pitr` is now a recovery volume.
  - check-env refused ports below 1024 because the build runtime (rootless Podman) was rootless,
    although Incus, not Podman, publishes them. The rule now applies on Compose only.
  - `docs/guides/incus.md` asked for Incus 7.0.1 or 7.2+; incus-compose needs 7.0.2 or 7.5+. The
    guide now lists the measured requirements and setup, and the differences above.
- **CloudNativePG: `make up` returned before the cluster was ready.** `helm --wait` cannot see a
  custom resource, so `make up` now waits for `.status.readyInstances` to reach `.spec.instances`,
  bounded by `MDB_K8S_CNPG_READY_TIMEOUT` (default `MDB_WAIT_TIMEOUT`, else 600 seconds), and fails
  with the Cluster's phase. `make rotate-secrets` republishes `secrets/` to the backend after the
  superuser changes, so the operator, which keeps the password equal to its Secret, no longer has
  the old one to put back (check 61; its live half needs a kind cluster).
- **Verify checks could reach the operator's data.** Check 27 removed the operator project's
  Patroni and etcd volumes, check 29 cleaned the checkout's `backups/upgrade`, and checks 21 and 22
  seeded and dropped databases in the operator's project and wrote, planted and pruned files in
  `backups/`. The harness now refuses any volume or project without a test prefix, gives every check
  a private `MDB_BACKUP_DIR`, runs checks 21 and 22 in a scratch stack and check 27 in its own
  project, and check 60 fails a check that goes around this. `scripts/upgrade` honours
  `MDB_UPGRADE_DIR` from the environment (default unchanged).
- **The metrics exporters crash-looped on Linux hosts.** `pg-exporter` and `mysqld-exporter` run
  as `nobody`, which cannot open a 0600 password file inside the 0700 `secrets/` directory, so
  both exited at start and `restart: unless-stopped` hid it. Docker Desktop and OrbStack ignore
  those permissions, so it only showed on Linux (CI). They now run as root with every capability
  dropped except `DAC_READ_SEARCH` (read, not write), and check 24 replays each exporter's user
  and capabilities against real Linux permissions on any host.
- **Check 48 used the host's real runtimes.** Its stub PATH still included `/usr/bin`, so on a
  host with docker there (every GitHub Ubuntu runner) a "podman only" case resolved to docker.
- **`make restore` no longer restores a dump it cannot verify.** A dump with no `.sha256` beside
  it only logged a warning and was restored over the live database. It is now refused before
  anything is touched; `MDB_RESTORE_ALLOW_UNVERIFIED=yes` restores a hand-made dump you trust.
  A checksum mismatch is still always refused.
- **`make upgrade` reported success after a failed restore.** Both restore steps ended in
  `|| true`, so a client that could not connect, or MySQL stopping at its first error, still led
  to "Upgrade staged successfully" over a partial copy. A failure now prints the client's last
  lines, keeps the full log under `backups/upgrade/`, and exits non-zero; the old volume is
  untouched either way.
- **`scripts/backup` and `scripts/verify-backups` ignored `MDB_BACKUP_DIR` in `.env`.** Only the
  environment was read, so a directory configured in `.env` was not the one written to,
  verified or pruned. The environment still wins (the sidecars set `/backups`); a relative value
  is relative to the checkout. The sidecars still bind `./backups`, so a different host
  directory for them remains a Compose override.
- **Verify-suite hygiene:** check 20 leaked one socket volume per MySQL-family engine on every
  run (it removed the volume while its container still held it); check 15 counted every `mdb_*`
  volume on the machine, so an isolated project could pass on the operator's real volumes; and
  check 37 failed under load while pgBackRest's first full backup was still running. Check 60
  now guards all three.
- **`make rotate-secrets` never rotated a MySQL or MariaDB project user.** Root logged in with
  the user's freshly generated password instead of its own, so every user failed with a warning,
  kept its old password, and the run still exited 0. Only `'root'@'localhost'` was changed, so
  `'root'@'%'` kept accepting the old password over TCP, and read-only companions were never
  rotated. Root now authenticates with its current password, every host row of a user is altered,
  the new passwords go in on stdin rather than the command line, read-only users are included,
  and any user left on its old password makes the run exit non-zero (check 57, in a scratch
  checkout).
- **`scripts/restore` reported success when the restore failed.** Its pipe ended in `|| true`
  and it logged "complete" either way, so a corrupt or truncated dump left an empty or partial
  database behind a success message. It now prints the client's last lines and exits non-zero,
  saying the database was already recreated (check 56, which runs in a scratch checkout).
- **`make init-prod` claimed the backups were encrypted; they never were.** It set
  `MDB_BACKUP_ENCRYPT=true` and printed "backups encrypted", but nothing reads that variable:
  dumps in `backups/` and their S3 copies are plaintext, and only the pgBackRest repository is
  encrypted. `init --prod` now says so, the variable is documented as reserved, and `check-env`
  warns on `true` (check 06). Real dump encryption is future work.
- Patroni HA (Compose too): `pg_hba` was under `bootstrap:`, so after any member restart the image's
  hardening rewrote it without replication rules and replicas were refused. Members also lacked the
  root to postgres `pg_ident` map, so host-side `make backup`, restore and psql failed with peer auth.
- `make destroy --all` keeps the Kubernetes backups claim (`<release>-backups-*`) as a recovery volume.
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
- **Every service drops privileges, has a health signal, and is limited under prod** (design.md D-75). etcd and
  HAProxy drop all capabilities; MongoDB, Cassandra, poolers, sidecars, etcd, HAProxy, Patroni and the UIs get
  prod limits (`MDB_<ENGINE>_CPU_LIMIT`/`_MEM_LIMIT`, `MDB_POOLER_*`, `MDB_AUX_*`); the exporters get
  healthchecks. pg-exporter now connects with `sslmode=require` and ProxySQL reaches its server over TLS, so
  neither is refused by a TLS-only server under prod. Check 72 holds this, and lists what is not done yet.
- **Images take their distribution's security updates whole** (design.md D-71): `mdb-os-upgrade` runs in every
  image and upgrades from the distribution's own repositories only, failing the build if the engine version
  moves. It replaces the per-package `--only-upgrade` lines (pcre2, expat, perl, libssl3, apt). Downloads go
  through `mdb-fetch`, which retries with backoff and verifies a recorded SHA-256 (s6-overlay, pg_graphql),
  a key fingerprint (PGDG) or a named signature check (the MySQL client RPM). Check 70 guards both.
- **CI gates image findings where they can have been caused** (design.md D-71): the Trivy scan fails the weekly
  scheduled build of main and pull requests that change an image input; other pull requests report findings
  without failing. A failed scheduled build opens or updates one tracking issue.
- **Go dependency floors are one file** (design.md D-70): `images/_shared/gotools/floors.txt`, read by every image
  that rebuilds a Go tool. `golang.org/x/net` is raised to v0.62.0 for CVE-2026-78661, which failed the pg,
  mysql and mariadb builds because the previous floor was written inline in four Dockerfiles. Check 69 keeps
  floors out of Dockerfiles and tests the version comparison.

- **CVE-2026-109518 in `apt` is fixed in the MariaDB images too.** It started failing the Trivy gate
  on MariaDB 11.8 and 12.3 on both architectures after the pg and Cassandra fix landed. `apt` is
  upgraded by name (2.8.3 to 2.8.4ubuntu0.1 on Ubuntu 24.04), and brings `libapt-pkg` along.
- **CVE-2026-109518 in `apt` is fixed in the pg and Cassandra images.** `apt` and `libapt-pkg` are
  upgraded by name, as pcre2, expat and perl already are. The pg images go from 2.6.1 to 2.6.2 on
  bookworm, and Cassandra from 3.0.3 to 3.0.4 on trixie. pgvector has not rebuilt 0.8.6 since
  2026-08-13, so there was no newer digest to repin to.
- **The CloudNativePG superuser Secret is built from files, not `--from-literal`.** Both keys come
  from a umask 077 temporary directory removed on exit, so no credential reaches argv or the
  source, and the pattern secret scanners reported is gone (check 54).
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
