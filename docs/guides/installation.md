# Installation

What you need before `make up`, and what to do when the usual things go wrong.

## Requirements

| | Minimum | Notes |
|---|---|---|
| A container runtime | Docker 24 / Podman 4.7 / nerdctl 1.x | Plus a Compose v2-compatible implementation. See [Container runtimes](runtimes.md) |
| RAM for the runtime's VM | 4 GB | 8 GB if you run Cassandra, which alone wants ~2 GB |
| Disk | 20 GB | Images are large; PostgreSQL alone with extensions is ~1 GB per major |
| `make` | any | Preinstalled on macOS and Linux; on Windows see [windows.md](../runbooks/windows.md) |
| `bash` | 3.2 | macOS ships 3.2 from 2007 and every script here supports it |

Check the first one:

```bash
scripts/runtime info
```

This shows which runtime and Compose dbpantry will use. Docker, OrbStack,
Colima, Rancher Desktop, Podman and nerdctl all work, and `CONTAINER_RUNTIME`
chooses between them ([Container runtimes](runtimes.md)). If it reports that
the engine is unreachable, stop here. Nothing in this toolkit works without a
running engine, and it will tell you so rather than pretending otherwise.

## Platforms

Everything runs on **amd64 and arm64**, including Apple Silicon, and CI proves
both. Two things are not identical across architectures:

- **PostgreSQL extensions**: a small number are amd64-only. `make build` says
  which were skipped rather than failing, and `design.md` lists them.
- **Emulation is not supported.** Running an amd64 image under QEMU on Apple
  Silicon appears to work and then corrupts data under load. If an engine has
  no arm64 image, the toolkit refuses rather than emulating.

## Install

```bash
git clone https://github.com/simtabi/dbpantry.git
cd dbpantry
make init
```

`make init` is the only step that is not idempotent-by-design, because it
generates your secrets. It:

1. copies `.env.example` to `.env` if you have no `.env` (yours is never overwritten)
2. generates a random password per engine into `secrets/`
3. generates a local CA and server certificates into `certs/`
4. generates `compose.engines.yml` from the engine descriptors

`secrets/` and `certs/` are gitignored, and check 03 fails the build if anything
in them is ever tracked.

## First run

```bash
make up
```

That starts PostgreSQL and Adminer. Nothing else starts unless you ask:

```bash
make up PROFILES=pg,mysql,mongodb,ui
```

Profiles are the engine names — `pg`, `mysql`, `mariadb`, `mongodb`,
`cassandra`, `ferretdb` — plus `ui`, `pooler`, `backup`, `metrics` and `prod`.
See [Configuration](configuration.md).

## Verify the install

```bash
make verify
```

This is the same harness CI runs. It is slow the first time because it builds
images; afterwards most checks are seconds. If it passes, your install is
correct — not "probably correct".

## Uninstall

```bash
make down          # stop, keep data
make destroy       # stop and delete volumes — asks first, and means it
```

`make destroy` deletes every data volume. It requires typing the project name to
confirm, because "I have run this in the wrong terminal" is a real way to lose a
day.

## When it goes wrong

**`make: command not found` on Windows.** Use WSL2. See [windows.md](../runbooks/windows.md).

**Port already in use.** `make up` refuses before starting anything and names
the port and the setting:

```
check-env port(s) already in use on this machine:
       8080 (MDB_ADMINER_HOST_PORT)
```

Something native is usually on 5432, 3306 or 8080 — a Homebrew PostgreSQL,
another project's Adminer. Either stop it, or move ours:

```bash
MDB_PG_HOST_PORT=5433 make up
```

Find what holds a port with `lsof -nP -iTCP:8080 -sTCP:LISTEN` on macOS, or
`ss -ltnp` on Linux. Ports your own stack already publishes are not flagged —
`make up` on a running stack rebinds them.

**A container starts and immediately exits.** Read the logs before anything
else; every engine here logs why.

```bash
scripts/runtime compose logs pg --tail 50
```

**MongoDB 8.0 or 9.0 crash-loops on start.** That is a known upstream
incompatibility with Linux kernels 6.19 to 7.0.13 (SERVER-121912). MongoDB
exempts 7.0.14 and later, but by the version `uname -r` reports, and Ubuntu's
7.0 kernels all report 7.0.0 (SERVER-136848). Check with
`scripts/runtime ctr run --rm docker.io/library/alpine uname -r`. On an affected kernel, use the default, 7.0.

**Cassandra never becomes healthy.** It is almost always memory. Cassandra needs
about 2 GB to itself; give the runtime 8 GB or raise `MDB_CASSANDRA_HEAP`.

**`make build` fails on a PostgreSQL extension.** Check whether it is one of the
amd64-only extensions listed in `design.md`. If it is not, that is a bug worth
reporting.

---

[← Docs index](../../README.md#documentation)
