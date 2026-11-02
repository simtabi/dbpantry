# Podman

dbpantry supports Podman 4.7+ (the first with `podman compose`; older releases work through `podman-compose` directly), rootless or rootful, on Linux or through `podman
machine` on macOS and Windows. The tested setup is a rootful `podman machine`
on macOS with docker-compose v2 as the Compose provider.

```bash
CONTAINER_RUNTIME=podman scripts/runtime info
# runtime:   podman (CONTAINER_RUNTIME=podman)
# compose:   podman compose [compose-v2]
# engine:    reachable, provided by podman-machine
# rootless:  no
```

To make Podman the default, put `CONTAINER_RUNTIME=podman` in `.env`. You only
need this when Docker is also installed and running, because `auto` prefers
Docker.

## Setup

macOS:

```bash
brew install podman docker-compose       # docker-compose is the provider podman compose runs
podman machine init --cpus 4 --memory 8192 --disk-size 60
podman machine start
```

The machine needs more than its 2 GB default. PostgreSQL with extensions, plus
MySQL and MariaDB, wants about 4 GB, and Cassandra adds about 2 GB.

Linux: install `podman` and either `docker-compose` (v2) or `podman-compose`.
Run `systemctl --user enable --now podman.socket` if you want Docker-API tools
to work too.

### Rootless or rootful machine

`podman machine` lists two connections, `podman-machine-default` (rootless) and
`podman-machine-default-root` (rootful), but **`podman compose` ignores the
connection**. It always gives its provider the machine's forwarded API socket,
and that socket is rootful or rootless according to the machine itself
(`podman machine inspect --format '{{.Rootful}}'`). If the CLI used the other
connection, Compose would create containers in one store and dbpantry would look
for them in the other. The runtime layer refuses that combination with an
explanation. Pick one of these:

```bash
# use the machine as it is (rootful by default on recent Podman)
unset CONTAINER_CONNECTION

# or make the machine rootless (restarts it; reversible with --rootful=true)
podman machine stop && podman machine set --rootful=false && podman machine start

# or use podman-compose, which runs the podman CLI and so follows the connection
CONTAINER_CONNECTION=podman-machine-default MDB_COMPOSE=podman-compose make up
```

On native Linux there is no machine and no split: rootless is just your user.

## Things that are different

### Ports below 1024 (rootless)

A rootless runtime can't bind host ports below 1024. This applies on Linux
(`net.ipv4.ip_unprivileged_port_start=1024`) and through gvproxy on a rootless
`podman machine`. Caddy's defaults are 80 and 443, so `make check-env` refuses
them on a rootless runtime and tells you what to set:

```bash
MDB_CADDY_HTTP_PORT=8080
MDB_CADDY_HTTPS_PORT=8443
```

### Image names

Podman doesn't assume Docker Hub. With `short-name-mode=enforcing` and no
`unqualified-search-registries` (the `podman machine` default), `mysql:8.4`
fails to resolve. Every reference in dbpantry is fully qualified
(`docker.io/library/mysql:8.4.11@sha256:…`), and check 49 keeps it that way. If
you set an `MDB_<ENGINE>_BASE_IMAGE` override, qualify it the same way.

### Healthchecks

`podman build` defaults to the OCI image format, which has no HEALTHCHECK.
`scripts/build` passes `--format docker` on Podman so the engines' healthchecks
survive. Healthchecks run from systemd timers, which every `podman machine` and
systemd host has. On a host without systemd, health stays `starting`. In that
case raise `MDB_WAIT_TIMEOUT`, or run without health-gated waits.

Podman also ignores a HEALTHCHECK in an image that arrives in OCI format by
another route. This happens with `docker save | podman load` from a Docker that
uses the containerd image store (OrbStack, or Docker Desktop with containerd).
That container then has no healthcheck at all: `compose up --wait` reports it
ready as soon as it is running, and checks waiting for `healthy` time out. Build
under Podman with `make build`, or pull the published images, instead of moving
images across. Check this with
`podman image inspect --format '{{json .HealthCheck}}' <image>`, which should
not print `null`.

### SELinux

`podman machine` on macOS mounts host folders over virtiofs, so SELinux labels
don't apply. On an SELinux-enforcing Linux host (Fedora, RHEL), the bind mounts
(`./secrets`, `./certs`, `./scripts`, `./backups`, `./config/...`) are denied
until they are labelled. dbpantry doesn't add `:z` itself, because relabelling
the checkout would also relabel your editor's files. Instead, do one of the
following once:

```bash
chcon -R -t container_file_t secrets certs scripts backups config overrides engines
# or, for this user only, in ~/.config/containers/containers.conf:
#   [containers]
#   label = false
```

### podman-compose

When `podman compose` runs podman-compose (no docker-compose installed), or you
set `MDB_COMPOSE=podman-compose`:

- `--wait` doesn't exist, so `make up` (through `scripts/runtime up`) polls
  every container's health itself, for up to `MDB_WAIT_TIMEOUT` seconds.
- `COMPOSE_FILE` and `COMPOSE_PROFILES` from `.env` are passed as explicit `-f`
  and `--profile` flags, because podman-compose doesn't reliably read them.
- Service state is read with `ps -q` and `inspect` (Compose labels and
  `.State.Health`), not with `ps --format` templates, which differ between
  implementations.

This combination is best effort: it isn't part of the tested matrix.

## Testing notes

```bash
export CONTAINER_RUNTIME=podman            # and no CONTAINER_CONNECTION, see above
scripts/runtime info
make verify-fast
make build                    # builds into Podman's own store; Docker's images are not shared
make up
make verify CHECKS=08,15,22,28
```

Podman keeps its own image store, separate from Docker's, so the engine images
have to be built (or pulled from GHCR) again under Podman.

If `docker-compose` fails pulls with `unauthorized: incorrect username or
password` for docker.io images, it is sending stale Docker Hub credentials from
`~/.docker/config.json` (Podman and docker-compose both read it). Log in again,
or point one command at an empty config: `DOCKER_CONFIG="$(mktemp -d)" make up`.
