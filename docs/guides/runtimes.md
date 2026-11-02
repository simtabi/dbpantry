# Container runtimes

dbpantry runs on any OCI container runtime that has a Compose implementation.
Every script, make target and verify check reaches the runtime through one
layer, `scripts/runtime-lib.sh` (design.md D-60). No script calls `docker`
directly, and check 48 enforces that.

```bash
make runtime              # or: scripts/runtime info
```

```
runtime:   docker (CONTAINER_RUNTIME=auto)
cli:       docker
compose:   docker compose [compose-v2]
engine:    reachable, provided by orbstack
rootless:  no
```

## Choosing a runtime

| Variable | Values | Default |
|---|---|---|
| `CONTAINER_RUNTIME` | `auto`, or an adapter name: `docker`, `podman`, `nerdctl` | `auto` |
| `MDB_COMPOSE` | a full Compose command, for example `podman-compose` | resolved from the runtime |
| `MDB_RUNTIME_ADAPTER_DIRS` | extra adapter directories, colon-separated | none |
| `MDB_WAIT_TIMEOUT` | seconds `make up` waits for health when the Compose implementation has no `--wait` | `300` |

You can set these in your shell or in `.env`, or pass them on a single command
(`CONTAINER_RUNTIME=podman make up`).

`auto` picks the first runtime that is installed and whose engine answers, in
adapter order: docker, podman, nerdctl. If none answers, it reports the first one
that is installed, and you get a clear error instead of a hang. A `docker`
binary that is really the podman-docker shim counts as Podman.

The Compose command is resolved like this:

| Runtime | Compose used | Notes |
|---|---|---|
| docker | `docker compose`, then `docker-compose` v2 | Compose v1 is refused |
| podman | `podman compose`, then `podman-compose` | `podman compose` runs whichever provider is installed. That is docker-compose v2 if present, otherwise podman-compose. dbpantry checks which one it got. |
| nerdctl | `nerdctl compose` | |

## Support matrix

| Runtime / product | How dbpantry sees it | Status | Guide |
|---|---|---|---|
| Docker Engine (Linux) | docker | Supported, tested in CI | [Docker](runtime-docker.md) |
| Docker Desktop | docker | Supported | [Docker](runtime-docker.md) |
| OrbStack | docker | Supported, tested by the maintainers | [Docker](runtime-docker.md#orbstack) |
| Colima (docker runtime) | docker | Supported, not regularly tested | [Docker](runtime-docker.md#colima) |
| Rancher Desktop (moby) | docker | Supported, not regularly tested | [Docker](runtime-docker.md#rancher-desktop) |
| Podman 4.7+ (rootless or rootful), with docker-compose v2 | podman | Supported, tested on a rootful podman machine (macOS) | [Podman](runtime-podman.md) |
| Podman with podman-compose | podman | Best effort | [Podman](runtime-podman.md#podman-compose) |
| nerdctl / containerd (incl. Rancher Desktop containerd, Colima containerd) | nerdctl | Best effort, untested | [nerdctl](runtime-nerdctl.md) |
| Apple container (macOS 26) through socktainer | docker | Supported for local development, tested on macOS 27.2 (PG, MySQL, MariaDB); needs `compose.apple.yml` | [Apple container](runtime-apple-container.md) |

OrbStack, Colima, Docker Desktop and Rancher Desktop (moby) all expose the
Docker API. To dbpantry they are just `docker`, so they share Docker's code
paths. `scripts/runtime info` names the product only so you can see which one
you are using.

## Adapters: how runtimes plug in

Each runtime is an adapter file in `scripts/runtimes/` (`10-docker.sh`,
`20-podman.sh`, `30-nerdctl.sh`). The core in `scripts/runtime-lib.sh` names no
runtime. It loads the adapters in filename order, which is also the `auto`
priority, and calls their hooks: `present`, `reachable` and `compose` are
required; `ctr`, `alias`, `validate`, `build`, `rootless` and `provider` are
optional. To add a backend you add a file, and neither the core nor any caller
changes. Check 48 plugs in a fake adapter to prove it. The interface is in
[scripts/runtimes/README.md](../../scripts/runtimes/README.md).

## Backends: how the stack is deployed

The runtime above is the container CLI. A second setting, `MDB_BACKEND` in
`.env`, chooses how the stack is deployed (design.md D-62). `compose`, the
default, runs the Compose files on whichever runtime you picked. Scripts never
call `compose exec` themselves; they ask the backend to run a command in a
service, so the same `make backup`, `make psql` or `make new-project` works on
any backend that implements the verbs in
[scripts/backends/README.md](../../scripts/backends/README.md).
`scripts/runtime backend` prints the one in use.

Backends so far:

| `MDB_BACKEND` | Deploys with | Guide |
|---|---|---|
| `compose` (default) | the Compose files, on the runtime above | this page |
| `kubernetes` | the Helm chart in `deploy/helm/dbpantry`, to `MDB_K8S_CONTEXT` | [Kubernetes](kubernetes.md) |

## Planned

- **LXC / LXD / Incus.**

Apple's `container` tool (macOS 26) needed no backend of its own: socktainer
serves the Docker API on top of it, so it is the docker runtime with a memory
overlay ([Apple container](runtime-apple-container.md)).

## What differs between runtimes

These are the differences the layer handles, and the ones it can't.

| Concern | Docker | Podman | nerdctl |
|---|---|---|---|
| Short image names | expanded to docker.io | rejected without a search registry | expanded to docker.io |
| HEALTHCHECK in built images | kept | dropped by the default OCI format, so builds use `--format docker` | kept |
| `up --wait` | yes | yes with docker-compose v2, polled with podman-compose | polled |
| Host ports below 1024, rootless | n/a (rootful) | refused, so use 8080/8443 | refused when rootless |
| SELinux bind mounts | n/a on macOS | see [Podman](runtime-podman.md#selinux) | n/a |
| Trivy scan | `image save` + `--input` (no socket mount) | same | same |

Every image reference in dbpantry is fully qualified (`docker.io/library/mysql`,
not `mysql`), so every runtime resolves it to the same image. Check 49
enforces that.

## Checking a runtime

```bash
CONTAINER_RUNTIME=podman scripts/runtime info
CONTAINER_RUNTIME=podman make verify-fast
CONTAINER_RUNTIME=podman make verify CHECKS=08,15,22,28
```

Check 48 tests the detection rules against stub CLIs, so it passes on any
machine. The behavioural checks run against the runtime you select.
