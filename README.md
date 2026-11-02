# simtabi/dbpantry

[![Tests](https://github.com/simtabi/dbpantry/actions/workflows/ci.yml/badge.svg)](https://github.com/simtabi/dbpantry/actions/workflows/ci.yml)
[![Static analysis](https://github.com/simtabi/dbpantry/actions/workflows/lint.yml/badge.svg)](https://github.com/simtabi/dbpantry/actions/workflows/lint.yml)
[![License MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

> One local database toolkit for SQL and NoSQL: PostgreSQL, MySQL, MariaDB, MongoDB, Cassandra and FerretDB, each version-switchable, secure by default, with browser UIs — installed once and shared by many projects.

Runs on macOS, Windows and Linux across amd64 and arm64; dev-first, prod-capable.
Works with any OCI runtime that has Compose: Docker, OrbStack, Colima, Rancher
Desktop, Podman (rootless included) and nerdctl. It detects which one you have,
and `CONTAINER_RUNTIME=podman` forces a choice. See [Container runtimes](docs/guides/runtimes.md).

## Install

```bash
git clone https://github.com/simtabi/dbpantry.git
cd dbpantry
make init
make up
```

PostgreSQL and Adminer are running, with **no host port published** — your
application joins the `mdb_net` network and connects to `pg:5432`. Set
`MDB_PUBLISH=direct` (or `proxy`) when a GUI client needs a host port; see
[Configuration](docs/guides/configuration.md).

Add engines with profiles:

```bash
make up PROFILES=pg,mysql,mongodb,ui
```

## Quick start guide and usage

### Getting started

1. Confirm the harness is sound: `make verify-structure`.
2. Start only the engines a project needs (profiles are opt-in, one per engine):
   `make up PROFILES=pg,ui`.
3. Check what is running: `make status`.

### Usage

Provision a project: a database, a least-privilege owner role, a read-only
companion role, the configured extensions, and a paste-ready connection block:

```bash
make new-project NAME=myapp
make new-project NAME=myapp ENGINE=mysql
```

Connect, and back up:

```bash
make psql USER_NAME=myapp    # as the project role
make backup ENGINE=pg DB=myapp     # one database
make verify-backups                # restore them into throwaway containers
```

The full walkthrough is in [Getting started](docs/guides/getting-started.md); everything
else is in the [documentation index](#documentation).

## Engines

Every engine is **authenticated by default**, which several of them are not out
of the box — correcting that is a large part of why this exists.

| Engine | Paradigm | Versions | Pooling | Licence |
|---|---|---|---|---|
| PostgreSQL | relational | **17** · 18 | pgBouncer (required in prod) | PostgreSQL |
| MySQL | relational | **8.4** · 9.7 (both LTS) | ProxySQL (optional) | GPL-2.0 |
| MariaDB | relational | **11.8** · 12.3 (both LTS) | ProxySQL (optional) | GPL-2.0 |
| MongoDB | document | **7.0** · 8.0 · 9.0 | driver-side | SSPL — [referenced, not published](docs/guides/licensing.md) |
| Cassandra | wide-column | **5.0** | driver-side | Apache-2.0 |
| FerretDB | document | **2** | driver-side | Apache-2.0 — declared, image not built yet ([why](docs/guides/adding-an-engine.md)) |

Bold is the default. Each engine maintains the newest supported line and the one
before it; those are the versions built, scanned and verified here. An older
version still runs, unsupported and with no scan guarantee, by naming its upstream
image in `MDB_<ENGINE>_BASE_IMAGE` — see [Supported versions](docs/guides/versions.md).

Adding another is a descriptor file — see [Adding an engine](docs/guides/adding-an-engine.md).

## <a name="documentation"></a>Documentation

The documentation lives in [`docs/`](docs/), indexed below. Engine images are not
published yet: the release workflow publishes them from the next tag, and until
then `make build` builds them locally (see [Release](docs/guides/release.md)).

### Guides — `docs/guides/`

Read once, to learn how the toolkit works and how to set it up.

- [Installation](docs/guides/installation.md) — prerequisites and first run
- [Container runtimes](docs/guides/runtimes.md) — Docker, OrbStack, Colima, Rancher Desktop, Podman, nerdctl ([Docker](docs/guides/runtime-docker.md) · [Podman](docs/guides/runtime-podman.md) · [nerdctl](docs/guides/runtime-nerdctl.md))
- [Getting started](docs/guides/getting-started.md) — a new project connected in under a minute
- [Configuration](docs/guides/configuration.md) — every `MDB_` variable
- [Supported versions](docs/guides/versions.md) — which versions are maintained, and running any other
- [Architecture](docs/guides/architecture.md) — how engines are declared rather than hardcoded
- [Adding an engine](docs/guides/adding-an-engine.md) — the extensibility contract
- [Connection pooling](docs/guides/pooling.md) — why the answer differs per engine
- [Licensing](docs/guides/licensing.md) — what you are running, and under what terms
- [Release](docs/guides/release.md) — how versions ship

### Runbooks — `docs/runbooks/`

Followed step by step, often under pressure.

- [Operations](docs/runbooks/OPERATIONS.md) — VPS, exposure, tuning, HA
- [Restore](docs/runbooks/RESTORE.md) — the 2am runbook
- [Upgrade](docs/runbooks/UPGRADE.md) — guided major-version migration
- [Onboarding](docs/runbooks/ONBOARDING.md) — migrate existing native databases in
- [Windows](docs/runbooks/WINDOWS.md) — WSL2 and git-bash notes

### Reference — `docs/reference/`

Looked up to check the contract, the evidence, or a fact.

- [Specification](docs/reference/SPEC.md) — the build contract
- [Acceptance](docs/reference/acceptance.md) — every criterion mapped to the check that proves it
- [Image provenance](docs/reference/image-provenance.md) — every image, its publisher, and why
- [Design](DESIGN.md) — pinned versions, capacity table, decision log
- [Row-level security kit](rls/README.md) — database-enforced multi-tenancy
- [Repository layout](docs/REORGANIZATION_PLAN.md) — where each file lives, and why

## Name

dbpantry was called multidb-server (repository `simtabi/multidb-server-docker`), and
before that db-toolkit-docker. The rename changed the product, repository and image names
(`dbpantry-<engine>`) only. These keep their names on purpose, because existing checkouts,
volumes and integrations depend on them:

- every `MDB_` environment variable;
- the `mdb` Compose project and its `mdb_net` network, and so every container and volume name;
- the engine descriptor contract (`engines/*/engine.conf` and its `MDB_ENGINE_*` keys);
- two strings written into existing data: the PostgreSQL `# multidb-server include` marker and
  the Patroni `/multidb-server/` namespace.

The decision is [D-54](DESIGN.md#7-decision-log). Check 44 fails on any other use of the old
names.

## Community

Issues and discussions: <https://github.com/simtabi/dbpantry/issues>.

## Contributing & security

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).

## License

MIT for this repository. Published images inherit the licence of the database
they contain — see [Licensing](docs/guides/licensing.md). See [LICENSE](LICENSE).
