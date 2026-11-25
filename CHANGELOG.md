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
- **Every make target now needs Go.** Each one runs the `dbpantry` binary, which `make` builds from `src/`
  the first time a target needs it after a Go file changes. Install the Go named in `src/go.mod` (1.27)
  before `make init`. What each target does is unchanged (docs/reference/cli.md, design.md D-80).
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
- **Certificates:** the next `make up` re-issues certificates that lack the `pg-pooler` SAN or the
  names in `MDB_EXTRA_SANS`, from the same CA; restart `pg-pooler` afterwards. `make certs-renew` does it
  at once.

### Added
- **`make cross`** builds the binary for every release target (Linux, macOS, Windows and FreeBSD, amd64
  and arm64) into `build/<os>/`, with a `build/checksums.txt` that verifies as it is.
- **The dashboard manages webhooks**: the Operations page lists them to an admin, creates one (showing its
  signing secret once) and deletes one, where only the API could before.
- **Add the prod overlay from the dashboard**: an admin button on Configuration runs `config prod-overlay`
  on the instance's own `.env`.
- **The Windows binary runs through WSL 2.** `dbpantry.exe` hands every command that runs the scripts to
  the Linux `dbpantry` inside WSL (a login shell, so `~/.local/bin` is found), and keeps `version`,
  `help` and `completion` native. Without WSL, or without dbpantry in it, it says what to install.
  `dbpantry.exe dashboard` serves inside WSL and opens in a Windows browser at
  `http://127.0.0.1:8484`. `DBPANTRY_NO_WSL=1` keeps a command native.
- **Environment types and cross-env data move (D-100).** Catalog of `localdev`, `dev`, `test`
  (`tst`), `qa`, `uat`, `trn`, `spt`, `stg`, `preprod`, `demo`, `sandbox`, `prod` (`prd`) with
  aliases; registry of named `MDB_HOME`s; `dbpantry env types|ls|init|use|copy|migrate|remove`;
  dashboard `/envs` and actions `env.init` / `env.copy` / `env.migrate` / `env.remove`. Copy and
  migrate export then restore between homes; backup/import/export stay available per home.
- **Signed outbound webhooks and event stream (D-99).** Admin `POST /api/v1/hooks` registers an
  HTTPS endpoint; deliveries carry `X-Dbpantry-Signature` (HMAC-SHA256). `GET /api/v1/events` and
  `GET /api/v1/events/stream` (SSE) expose `job.*` and alert events. Projects call the Bearer API
  in; dbpantry calls out. No inbound webhooks; no WebSocket dependency (SSE only). Dashboard may
  listen on a unix socket (`unix:///path.sock`) for same-host API clients.
- **Job control API and drawer (D-98).** `POST /api/v1/jobs/{id}/control` with
  `abort` / `pause` / `continue` / `restart`; drawer toolbar buttons; verify harness tree
  classification; Compose profile multi-select for Start / Manage profiles.
- **Add-ons grouped by engine with toggles.** Status, Engine and Add-ons pages group catalog rows
  by database engine (then kind) and expose on/off switches that run `addon.enable` /
  `addon.disable`.
- **Primary & replicas + concurrent engines in the dashboard (D-104).** `/ha` explains PostgreSQL
  primary/replicas (`ha` profile), links failover/rebuild, and points at multi-engine
  `MDB_PROFILES` and multi-home Environments. Engines page: “Start several engines…”.
- **Auto-free Compose subnet on pool overlap (D-103).** Before `up` creates the project network,
  if `MDB_NETWORK_SUBNET` overlaps an existing engine pool, dbpantry picks a free
  `172.16–31.0.0/16` (skips the fixture default), writes `.env`, and continues. Opt out with
  `MDB_NETWORK_SUBNET_LOCK=1`.
- **Page splash / preloader (D-102).** Fluent TypeScript loader (`resources/ts/pageloader`, seven
  classes) bound by `data-dbpantry-*`; determinate `%` and stage labels; defaults favour a fast
  DOM/`#content` reveal (optional full `window` load / fonts via attributes).
- **`/status` page.** Health %, services, engines, images/projects, recent jobs and a Live badge
  legend; linked from the header counters, Operate, Help, footer and Overview.
- **`stack.start` dashboard action.** Boots and optionally provisions via `dbpantry start
  --no-dashboard` so the operator already in the UI does not open a second dashboard.
- **The dashboard manages the stack.** Every page has actions that run the same `dbpantry` commands as
  the CLI, through `POST /api/v1/jobs` with live output (SSE), CSRF, scoped API tokens and an audit log.
  Auth modes: launch-token URL, password, or `none` on loopback. Projects show active / orphaned /
  unmanaged from the engines; images group by prefix; backups show PITR status and the last verify.
  Configuration lists every `MDB_` key with its origin and can set one. Docs: `docs/guides/dashboard.md`,
  `docs/reference/api.md` (design.md D-86).
- **`config show [--origin]` and `config set KEY VALUE`.** Effective settings with whether each came from
  the environment or the env file; set writes the instance `.env` quoted per check 50 (design.md D-87).
- **`project ls` / `project rotate` / `project drop`.** Live databases and extensions per engine, joined
  with password files; atomic password rotation; typed-confirmation drop of database, roles, secrets and
  client certs.
- **A step tree, progress bars and tables on a terminal** (design.md D-81). `make up` shows each step with a spinner and its time, and groups Compose's work into networks, images and containers. Each image build gets a bar with its percentage and an estimated time left. The run ends with a Connect table for the engines you started. `make down` shows what it removed. `make new-project` shows what it generated, the roles and each extension (with a warning for one that could not be created), a table of the project, and the `.env` block flush left between rules. Off a terminal, or with `--plain`, output is unchanged. `--pretty` forces the view.
- **`make start`: everything in one command.** It boots `PROFILES`, provisions `PROJECTS=name[:engine],…` on every engine in them, then serves the dashboard. Every step is idempotent; `NO_DASHBOARD=1` stops after provisioning.
- **`make dashboard`**: a read-only web dashboard on `http://127.0.0.1:8484/`, built with Tabler 1.7.5's CSS and JavaScript embedded in the binary. Tabler's two-row header and icon menu lead to Overview, Services (state, health, uptime, image), Images, Engines (one page each: addresses, pooler, TLS, extensions, principals), Projects, Backups and Connect. Each project's page has its paste-ready `.env` block, the password blurred until shown, with copy buttons. Pages refresh every five seconds, and the same data, without passwords, is at `/api/status`.
- `scripts/new-project --format json`: the provisioning result as one JSON object. `--show` prints an existing project's block without provisioning or generating anything.
- `dbpantry endpoints` names each engine's admin user (`postgres`, `root`, `cassandra`; MongoDB's
  `MDB_MONGODB_ROOT_USER` overrides it), from a new optional descriptor key `MDB_ENGINE_ADMIN_USER`.
  laranail-test-db's `principal: admin` needs it. A Go test reads the family scripts, so the descriptor
  cannot name a login the toolkit never uses.
- `make new-project NAME=x FIXTURE=1` (`project new --fixture`) provisions in the fixture, with password
  files under `secrets/fixture/`, which `fixture down` removes; `make endpoints FIXTURE=1` (`endpoints
  --fixture`) reports the fixture's ports and those files. A test run that names its own project gets
  its own database, so concurrent runs no longer collide (laranail/settings pilot findings 1 and 4).
-- **The `dbpantry` command tree for every existing feature** (refactor plan phase 2): `init`, `config
  validate|render|prod-overlay`, `up`, `down`, `status`, `logs`, `forward`, `destroy`, `runtime`, `shell`,
  `tools`, `project new`, `backup run|verify`, `restore`, `pitr`, `data import|export`, `upgrade`, `ha`,
  `certs`, `secrets rotate`, `images build|scan`, `verify`, `dev lint|release-notes`, `self-update`, beside
  phase 1's `manifest`, `endpoints` and `fixture`. Global `--dry-run`, `--no-input`, `--no-color`; per-command
  `-o` from one set of formats, with JSON errors carrying a stable code; `--confirm=<name>` answers a typed
  confirmation. The tree is declared one command at a time and pinned by `TestTreeIsPinned` and
  `TestTreeIsDeclared`. Reference: docs/reference/cli.md; ledger: docs/reference/parity.tsv.
- **`dbpantry pooler sync ENGINE`** restarts a pooler that copies credentials when it starts (ProxySQL), so
  users created or rotated afterwards can log in through it. `project new` and `secrets rotate` run it
  (refactor plan §14, N8).
 **Mutual TLS through the pooler** (design.md D-74). Under `MDB_MTLS=true` pgBouncer requires a toolkit-CA client
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

### Changed
- **The release ships the `dbpantry` binary.** `release.yml` now publishes through `simtabi/.github`'s
  shared `go-release.yml`: the binary for Linux, macOS (plus a universal build), Windows and FreeBSD on
  amd64 and arm64, each with the kit inside (`make kit` runs first), the source archive, checksums,
  SBOMs, provenance and the `src/vX.Y.Z` module tag. Pre-release tags (`vX.Y.Z-rc.N`) build a GitHub
  pre-release. See docs/guides/release.md and design.md D-110.
- **Configuration › This instance is three tiles** (env file, profiles, network), each with its
  icon, main value, the real values behind it (path and keys set in the file; engines on and
  extras; subnet, project name, backend and publish mode) and its buttons on one aligned footer.
- **Slide-in panel heads use Tabler components.** The job, status and add-on panels share an
  `offcanvas-header` with a `subheader` kicker and `offcanvas-title`; the job's facts are a
  `datagrid`, its status the same `badge bg-*-lt` + status dot as everywhere else, its result a
  Tabler `alert` (danger, warning, success) and Steps / Raw log a `nav-segmented`.
- **The job output console is always dark**, in the light theme too, like the Logs view: it
  scopes Tabler's dark theme (`data-bs-theme="dark"`) to the console.
- **Status guide (D-108).** Help has a “What the statuses mean” section, and
  [docs/guides/statuses.md](docs/guides/statuses.md) the same tables: engine (running, stopped,
  leftover, disabled), service health, project, job and Live badge, each with what to do next.
  Stack › By engine, Engines and the Status page link it. Every badge's tooltip and the Live
  legend now read one table (`StateGuide`), and a test regenerates and checks the doc.
  `dbpantry endpoints` prints PROFILE `enabled`/`disabled` instead of `true`/`false`, and
  `status` / `endpoints` help point to the guide.
- **One button size and radius, per-engine password rotation, quieter engine page (D-107).**
  Every `.btn` and `.btn-sm` is the same 2rem control with the same corner radius as the header
  chips and the search trigger; only `.btn-lg` is bigger, and the drawers no longer square their
  buttons. Icon avatars are circles. The engine page shows Start once (the header), with a one-line
  status strip instead of a second title, status and Start. The Principals card rotates this
  engine's passwords or every engine's: `secrets rotate --engine pg|mysql|mariadb` and the
  `secrets.rotate` action's optional `engine`. The header search shows Ctrl K off macOS, opens at
  once (actions join when loaded, and a failed load is retried), focuses on open, answers `/`, and
  drops the tooltip that repeated its label.
- **Job drawer and slide-in panels redesigned (D-106).** The drawer now renders structured run
  snapshots the CLI prints with `--events` instead of parsing pretty text: status/duration/profile
  facts, one bar per step, a failure-first banner with Retry and a link to the failing service's log,
  Steps/Raw views, search (`/`), an Issues filter, timestamps, Follow, foldable steps with drawn
  tree connectors, and Connect/Next tails. Status and Add-ons drawers share the same header. SSE
  `line` events carry the time each line was written. CLI: step icons, folded finished steps,
  durations in one right-aligned column, unfinished work shown as `not started`/`stopped` rather
  than failed, and failures headlined by the script's own message plus `dbpantry logs <service>`.
- **Engine page layout rebalanced (D-101).** “Where to connect” is compact label/value rows
  beside Projects (`row-deck`); Add-ons open in a slide-in panel with on/total badge, not a
  tall inline card. Live refresh skips while any offcanvas or modal is open.
- **The `dbpantry` binary runs with no checkout.** It carries the kit (scripts, compose files, engines,
  image sources) and, run where there is no checkout, extracts it to `~/.config/dbpantry/kit/<version>`,
  verified by checksum on every run, with the instance in `~/.config/dbpantry`. `--home` or
  `DBPANTRY_HOME` choose another home. `make kit` generates the embedded copy (design.md D-85).
- **`check-env` no longer refuses a fresh init on a machine running PostgreSQL** when the stack publishes
  no ports: it used to fall back to every port in the env file, HAProxy's 5432 included, though HA was off.
- **The instance can live outside the checkout.** `MDB_HOME`, set in the environment, names the
  directory that holds `.env`, `secrets/`, `certs/`, `backups/`, the generated compose files and the
  rendered config; with it set, nothing is written into the checkout. It defaults to the checkout,
  so nothing changes unless you set it. Check 77 boots a stack with a separate home and fails if the
  checkout changes (docs/guides/configuration.md, design.md D-84).
- **`fixture up` runs beside the stack instead of replacing it.** It is now its own Compose project
  (`<project>-fixture`) on `MDB_FIXTURE_PORT_BASE` (default 55000), so a test suite's tmpfs engines
  no longer restart every application's database, and `fixture down` stops only the fixture. No
  consumer read the fixture's old ports (searched simtabi, laranail and sangehq, 11,677 files). Check 64
  now looks for the fixture's containers and volumes in its own project, and fails if it finds no fixture
  container or one in the stack's project.
-- **The make targets run the `dbpantry` binary** (refactor plan phase 2, design.md D-80). Every target
  except `help`, `dbpantry` and the `go-*` targets now runs a `dbpantry` command, which runs the script the
  target used to run, with the same arguments, and exits with that script's status. Check 74 holds each
  target to the command `docs/reference/parity.tsv` names. `make test-profile` is `dbpantry up --profile test`,
  `make shell` is `dbpantry tools`, and the engine shells pass their client to `dbpantry shell SERVICE --`.
- **`dbpantry` usage errors exit 64, not 2**, following the exit-code table dbpantry shares with mesobia.
  A typed confirmation refused under `--no-input` exits 7. An unknown subcommand is now a usage error
  rather than the parent's help with exit 0.
- **`scripts/exposure-notice` lists every engine the stack can run** from the descriptors, not `pg` and
  `mysql` only (refactor plan §14.2).
 **Container images are no longer published.** A release publishes the GitHub release only, with
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

### Removed
- **Cassandra 4.1** (design.md D-57). It vendors snakeyaml 1.x, open to CVE-2022-1471, and
  cannot load the 2.x line that fixes it. D-57 records when it comes back.
- PostgreSQL 15 and 16, MySQL 8.0, MariaDB 10.11 and 11.4, MongoDB 8.3 from the
  maintained menu. Each still runs through `MDB_<ENGINE>_BASE_IMAGE`, unsupported.

### Fixed
- **One failed convergence left MySQL, MariaDB and PostgreSQL unhealthy for good (D-109).**
  The converge-failed marker survived restarts, and nothing removed it, so the healthcheck kept refusing
  after a later start converged. Each convergence stage now clears it first; check 80 guards it. Rebuild
  the images (`make build`) to pick it up.
- **Aborting a job ends what it started.** Dashboard jobs and log streams run in a process group of their
  own, killed as a whole; an aborted build no longer kept the job, and the one-change lock, until docker
  finished. An aborted job can no longer be paused.
- **A finished job sends one completion webhook**, whether no page or several watch it.
- **A runtime outage reads as unavailable (503)**, never as "no such service".
- **Off-site backups find their credentials** in the instance home, or `/run/secrets` in the backup
  sidecar, which died after its first dump with S3 configured.
- **A separate home's `MDB_BACKEND` is honoured**; it silently ran Compose.
- **`up` builds every missing image.** The pgBouncer pooler had no `build:` key, so `up` with the
  pooler or HA profile pulled an image no registry holds and failed; it now builds it, as the engines
  do. The verify harness also builds an image it needs and lacks, saying which and how, where it only
  named `make build` before.
- **`make scan` scans every image `make build` builds**, under the same tag: pgbouncer, patroni and caddy
  had never been scanned, and `scan pg pgbouncer` dropped pgbouncer without a word. An unknown image name
  now fails. The dashboard's Scan images offers the same list.
- **`make build` builds caddy too**, and `dbpantry images build` takes any image by name: the engines,
  `cli`, `caddy`, `pgbouncer` and `patroni`. The dashboard's Build images offers the same list; it
  offered engines only, including MongoDB, which has no image to build.
- **`up` re-issues a server certificate whose SANs are out of date**, from the same CA, and reloads the
  running engines live; it was a manual `make certs-renew` after an upgrade.
- **A verify run no longer rewrites your `.env`.** When the network pool overlapped, the subnet move
  (D-103) was written to the env file even for a throwaway project started from the checkout, so check
  27 changed the operator's `MDB_NETWORK_SUBNET`. The move is now written only for the env file's own
  project; any other project uses it for that run.
- **Incus reads a separate home's `.env`.** With `MDB_HOME` apart from the kit, incus-compose read the
  kit's, so the stack booted as project `mdb` on the default subnet with `secrets/` and `certs/` from the kit.
- **`project new` names the stack's own network** (`<project>_net`), not `mdb_net`, when the project
  name is set only in `.env`.
- **`env copy` and `env migrate` reach each home's own stack.** The caller's `COMPOSE_PROJECT_NAME`,
  `MDB_ENV_FILE` and profiles no longer outrank each home's `.env`, which pointed both steps at one stack
  while the copy reported success.
- **A project's volumes are its own**: project `mdb` no longer lists `mdb_dev`'s, which `destroy --all`
  would have offered for deletion.
- **`data import --from` and `export --to` take paths relative to where you run them**, and an import
  from a live server lands in the configured dump directory, not the kit's `backups/`.
- **`--listen` accepts a unix socket path**, sessions are capped in mode `none`, a value such as `-1`
  is no longer read as a flag by `config set`, and `/operations` lists API tokens to admins only.
- **`make go-lint` and `go test -race` pass again**: 64 lint findings and a data race in a test had
  reached `main` while CI's jobs were not running.
- **Job drawer kept spinning after fail and painted a dead 0% bar.** Failed steps abandon
  running children; the drawer settles leftover spinners, strips double ✔ from tree rows,
  drives the header from ASCII bars without dumping ░ rows, and tightens line spacing.
- **Pretty CLI tree looked padded and dotted (D-105).** Removed the left gutter and title blank,
  dropped the continuing `│` column under nested steps, and tightened blanks between the tree,
  Connect table and Next hints so `up` / `down` / `project new` read flush and natural. Done
  leaves no longer echo Compose’s `Healthy`/`Built` text; Overview flags leftover engines.
  Job drawer follow-up: stop painting `·` marks on tree/plain rows, collapse that empty mark
  column in CSS, and print the title once on Stop. Interactive follow-up: off-TTY `--pretty`
  streams nested compose tree lines mid-flight, prints each finished step as ✔, buckets elapsed
  every 3s, and the drawer shows header + per-row progress bars with %/ETA when known.
- **Splash could cover the page forever.** A synchronous `<head>` boot captured a null splash
  root, so hide cleared `data-dbpantry-loading` but left the overlay visible. Script is `defer`,
  root is re-queried, maxMs race is cleared, and `<noscript>` reveals the page.
- **Add-ons / Databases Live wiped wizard state.** Those pages keep Live off (`Refresh: false`)
  so a partial refresh cannot drop step/selection or `?engine=`.
- **Overview “Needs attention” showed a “Soon” stub** next to enabled-but-stopped engines, and
  could claim “Nothing needs attention” while Publish=`none` was listed. Labels are View / Start /
  Review / Optional; empty state only when no rows apply.
- **Engines cards were unclickable when off or leftover.** Tabler's `card-inactive` sets
  `pointer-events: none`, which killed every link and the ⋮ menu on non-running engines. Cards
  now use a visual dim class only; title is a stretched link; menus stay above it.
- **`dbpantry doctor` and Operate → System.** Host, binary and container-runtime facts on the CLI
  and `/system` (design.md D-93).
- **Declarative add-ons.** Catalog of poolers, PostgreSQL extensions, embed toggles and UIs the
  stack already ships; `dbpantry addon ls|enable|disable` (enable runs `CREATE EXTENSION` on project
  DBs; disable never `DROP`s), multi-step `/addons` wizard (engine → catalog → review → job), Status
  card (supported + enabled engines only), and a per-engine strip with `?engine=` (design.md D-96).
- **Databases & users wizard.** Data → Databases & users: shared-hosting provision steps, privileges
  matrix, rename-via-recreate, password reveal on the project page (design.md D-97).
- **Job drawer dumped spinner frames as white text.** Each Live progress tick became a new
  `pre` line (no colours, no %/ETA), and Stack › By engine stayed stale after activate/deactivate
  because it was outside `data-sync`. Off-TTY Live now skips spinner-/sub-second-only updates; the
  drawer is a structured viewer with step coalesce, status colours, and a progress bar with
  percentage and ETA; engine nav syncs on every partial refresh. Containers still up after profiles
  drop them show `up · off` (azure) instead of a green dot beside a plain `off` badge
  (design.md D-95).
- **Connect (and project) code snippets were invisible.** Tabler styles every `<pre>` with a light
  colour for dark code blocks; `.mdb-env` sits on the light surface, so Join-the-network, TLS CA path
  and `.env` blocks looked empty and Copy seemed broken. `.mdb-env` now sets `--tblr-body-color`.
- **Projects nav badge disagreed with its dropdown.** The badge counted every password-file project
  (including orphaned) while the menu hid orphans. The menu now lists them with an `orphaned` badge,
  and Connect is a top-level nav link (not only under Help).
- **Connect page and instance menu.** Publish modes are three cards with In use / Available, Switch
  via `config.set`, and Start to apply; engine address Copy sits in Quick access. The Instance menu
  is short links (Configuration, Connect, About, Status JSON); profiles, network and env editing
  live on Configuration's instance summary.
- **Dashboard confirms and feedback.** With `confirm: all` (default), operate actions open a soft
  confirm modal first; destructive ones always need the CLI's typed value. Job output uses an
  improved slide-in drawer (follow toggle, clearer progress); toasts report copy, job start and
  finish. Running jobs show the action's title instead of its machine name.
- **Thin PATH finds Docker.** `dbpantry` appends well-known container CLI directories
  (`/usr/local/bin`, Homebrew, OrbStack, …) when it runs scripts, so a dashboard started with
  `PATH=/usr/bin:/bin` still lists services. Check 79 guards it (design.md D-88).
- **Grouped dashboard nav.** Overview · Stack (services, images, engines) · Data (projects,
  backups) · Operate · Connect · Help, with section headers inside each menu instead of a flat
  nine-item bar and broken split carets.
- **Dashboard jobs no longer dump BuildKit.** Jobs run with `--pretty`, so the drawer shows the
  same step tree, progress bar, percentage and ETA as a terminal `up` (design.md D-89). Closing the
  drawer or the confirm modal is blocked while a command is running or starting.
- **Tooltips and popovers on actions and nav.** Every action button names the `dbpantry` command it
  runs; Operations rows use popovers; Tabler tooltips bind on load and after each refresh.
- **Connect and Engines actions.** Engine cards keep clickable details links plus a ⋮ menu
  (restart, rebuild/update, disconnect, destroy volume, verify). Connect's next-steps card runs
  declared jobs (`project.new`, `stack.start`, `up`/`down`, `verify`); interactive shells stay
  Copy-only. Connect is a main-nav item (no duplicate header CTA); Live pauses/resumes refresh; Back
  and job restore across reload (design.md D-90).
- **Dropdowns no longer clip; tooltips no longer stack.** In-page ⋮ / More menus use Popper
  `strategy: fixed` so `.table-responsive` / cards cannot hide them. Tooltips dispose before each
  partial refresh, skip `.dropdown-menu` items, and hide when a menu or action opens. Live refresh
  skips while a menu is open (no more random autoclose). Engine ⋮ uses Operate / Stop submenus.
- **Stack nav dots reflect running state**, not only `MDB_PROFILES`. Engines nest under By engine.
  **Status as JSON** opens a slide-in panel. Config group rows use a theme-aware section style
  (no white `table-light` bars in dark mode). Configuration hosts Switch profiles / Configure
  subnet / Rename project (`config.set`). Operate adds Clients: connection details, project `.env`,
  provision, verify, and status JSON.
- **Dashboard i18n** (en/es/fr/de/pt/ja) for chrome; **Help** and **About** pages with version,
  licences and engine licence table; page-header **Back/Forward** history. Projects and unmanaged
  databases use ⋮ action menus (adopt unmanaged via `project.new`). Help links prefer in-app
  `/help`, `/about` and `/api/v1/actions` over unpublished GitHub doc paths.
- **Header status chrome and Connect feedback** (design.md D-92). Notifications, language (`EN`)
  and Instance menus follow Tabler patterns; header service/engine/image counters live in a
  stats dropdown chip beside Live. Live badge colours Live / Paused / Retrying / Disconnected.
  Overview “Where to connect” is stacked list rows (no horizontal scroll). Connect explains
  `MDB_PUBLISH=none` with an alert, equal-height publish cards, and matching Join/TLS cards.
- **Overview CRM layout.** Welcome lead, eight icon-left KPI cards, stack funnel and needs-attention
  list, services and connect side by side, projects table with recent jobs — denser Tabler CRM home.
- **Dashboard demo URL.** Launch line always prints a browser-openable `http://127.0.0.1:…/` (rewrites
  `0.0.0.0`/`::`); docs stress Host must be localhost. `make demo-dashboard` runs a detached
  AUTH=none process via `scripts/demo-dashboard` (survives SIGHUP; `demo-dashboard-stop` /
  `scripts/demo-dashboard status`). Status drawer uses `/api/v1/status`.
- **Dashboard cold-cache stampede.** Concurrent first page loads no longer each start a full stack
  reading; `cached` single-flights the in-flight snapshot (design.md D-83).
- **Services health / image overflow.** Health uses compact badges (`no check` for none) instead of
  Tabler's fixed-height `.status` pill; image digests truncate via `.mdb-cell-ref` (ellipsis + title).
- **Page header chrome.** Back/forward history chips sit in the top navbar beside Live; page titles
  use Tabler breadcrumbs (under the title; every crumb is a real page) and coloured page icons.
- **Service log viewer.** `/services/{svc}/logs` formats lines into prefix / level / message with
  colours, filter, level chips, wrap and a follow-hold “new lines” pill.
- The dashboard, tested end to end against a live stack:
  - Every page took 1 to 3 seconds, the five-second refresh included. Pages are now served at once from the latest reading of the stack, renewed in the background.
  - The footer's "read at" time never moved after the first load; it now follows each refresh.
  - Engines in the profiles showed their addresses even while the stack was down. Each engine and project now says running, stopped or not enabled.
  - A missing page or engine answered with plain text; it now gets a dashboard error page.
  - The fixture's containers appeared nowhere; the Services page now lists them.
  - Project blocks showed `/path/to/dbpantry` for the CA; they now show the real path.
  - A blocked clipboard failed silently; the copy button now says so.
- `make up`'s summary said "1 networks"; a single item is now singular.
- **Check 64 failed beside a running fixture**: its scratch stack's fixture claimed the default
  fixture subnet (172.29.0.0/16) and ports (55000+), which a live fixture on the host already held
  ("Pool overlaps with other one on this address space"). `scratch_stack` now gives the scratch
  fixture a random 10.150-199 /24 and a free `MDB_FIXTURE_PORT_BASE`, as it already did for the stack.
  The docs say a second checkout sets its own fixture range and ports. Found by the CLI UI/UX session.

- `make` now rebuilds the binary when a file it embeds changes (the dashboard's templates and assets), not only when Go code does; a guard test lists every embedded file.
- **`fixture up` failed whenever the stack was running** ("Pool overlaps with other one on this address
  space"): the fixture's network asked for the stack's subnet. It now uses `MDB_FIXTURE_NETWORK_SUBNET`
  (default `172.29.0.0/16`), refused when it overlaps `MDB_NETWORK_SUBNET`, and gen-compose writes each
  pooler's fixed address as `${MDB_FIXTURE_<ENGINE>_POOLER_IP:-<stack address>}`, so the stack's render is
  unchanged. Check 66 now renders the fixture's network and pooler addresses. Found by the
  laranail/settings pilot.
- `dbpantry endpoints --project NAME` named the project's roles `NAME` and `NAME_readonly`, with
  password files `<engine>_NAME_password.txt`, none of which `make new-project` creates: it creates
  `NAME_user` and `NAME_user_readonly` and writes `<engine>_NAME_user[_readonly]_password.txt`. The
  principals now use new-project's names, and a Go test reads `scripts/new-project` so they cannot
  drift again.
- `make new-project` warned that `vector` and `pg_stat_statements` could not be created although the
  image ships both: the extension loop ran with `IFS=','`, so `mdb_compose` read `docker compose` as one
  command word. The loop now splits the list first and runs with the default IFS, the warning carries
  psql's error, and `mdb_compose`/`mdb_ctr` split their command strings on whitespace whatever the
  caller's IFS; check 76 proves it without a running engine.
- Getting started said "about a minute" with no mention of the first image build (about 8 minutes for
  PostgreSQL and MySQL).
- `MDB_PUBLISH` and `MDB_PORT_BASE` set on the command line (`MDB_PUBLISH=direct make up`) were
  ignored without a word, because `gen-compose` reads them from `.env` only, so the stack booted with
  no published port. `gen-compose` now refuses a value that differs from the env file and names the
  file to edit; check 75 proves it.
- **Checks 67 and 70 pass on a developer's Mac.** Check 67 refused the `.DS_Store` Finder writes into
  `build/`, which is gitignored and no build step touches. Check 70's scratch repository read the operator's global
  git config, where git 2.5x runs hooks declared as `hook.<name>.command` whatever `core.hooksPath` says, so a
  global commit hook could refuse its commits; it now reads no global or system config.
- **The Cassandra image build survives Maven Central rate limits.** `replace-jars.sh` downloaded with `--retry 3`,
  about 7 seconds of backoff, and HTTP 429 outlasted it in CI. It now retries as `mdb-fetch` does (`--retry 6
  --retry-all-errors`, with connect and total timeouts), and check 45 fails when the two policies differ.
- **`design.md` matches the compose files.** Sections 2 and 3 still showed the step 1 plan (etcd `v3.6.6`,
  HAProxy `3.2-alpine`, Caddy `2.10.2`, an `nfrastack/db-backup` service) and had no rows for MongoDB,
  Cassandra, the poolers or Patroni. They now list what the compose files run, `MDB_BACKUP_DIR` is in the
  variable reference, and check 71 fails when the service map and the rendered stack disagree (design.md D-79).
- **The backup sidecars honour `MDB_BACKUP_DIR`.** `backup-pg`, `backup-mysql` and `backup-mariadb` bound
  `./backups` whatever `MDB_BACKUP_DIR` said, while `make backup`, `restore` and `verify-backups` used it, so
  scheduled and manual dumps went to different trees and each was pruned on its own. The sidecars now bind
  `${MDB_BACKUP_DIR:-./backups}` (long form, so the `.env.example` spelling `backups` is a path, not a named
  volume); the default is unchanged. Check 71 renders six settings against what `scripts/backup` resolves,
  and check 47 now reads long-form bind sources.
- **Verify checks stop only what they started.** Checks 15, 18, 24, 25, 28 and 35 booted the operator's
  Compose project and ran `make down` on it, stopping every service there, including ones the check never
  started; check 26 ran `make down` without starting anything. Each now runs in a stack of its own
  (`scratch_stack`, with a new `scratch_call` helper for runtime functions), and 26 no longer stops a stack.
  Check 60 fails any check that runs a whole-stack down outside its own stack (through make, Compose, the
  runtime or the `dbpantry` binary); check 27's pre-boot
  `make down` (its stack needs the ports the operator's holds) is its one exemption.
- **`make logs` works on Incus.** It passed Compose's `--tail=100`, which incus-compose (1.3.5) refuses
  because its `logs` takes only `-f` and service names. The Incus backend now drops Compose-only log options
  with a note and shows the whole log. It also sends follow as `--follow`: a bare `-f` was read as `--file`,
  which left out the stack's compose files (check 58).
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

### Security
- **Job control keeps each action's gates** (codebase audit, 2026-11-24). An operate-scope API token could
  restart a finished admin job through `POST /api/v1/jobs/{id}/control`, re-running `secrets rotate`,
  `project drop`, `restore` or a volume delete with its stored typed confirmation. Abort, pause, continue
  and restart now need the job's action's scope; a restart needs the typed confirmation again
  (`"confirm"`) and is refused for a volume delete while `allow_destroy` is off.
- **Read scope no longer sees passwords.** A project's page needs `operate`, and a job's output and event
  stream need its action's scope, so a read token cannot read the `.env` block `project new` prints.
- **A secret set through `config set` is masked** in the job list, the Jobs page, the audit log and
  webhooks when the key is one the Configuration page masks; the runner also blanks it from the job's
  output lines.
- **Sign-in returns only to a same-site path**: `/\host` and its encoded form, which browsers read as
  `//host`, now go to `/`.
- **The Go toolchain moves to 1.27.4** for GO-2026-6859 in `net/http`, which the dashboard's HTTP server made reachable from the binary (`govulncheck` reported 17 call paths; it reports none on 1.27.4).
- **The images' Go build stage moves to 1.27.4**, the release `src/go.mod` uses, so gosu, rclone and postgres_exporter are built with the `net/http` fix for GO-2026-6859 too. Check 02 now fails when `images/bases.tsv`'s Go image and `src/go.mod`'s toolchain name different releases (design.md D-82).
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
