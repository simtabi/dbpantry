# Docker, Docker Desktop, OrbStack, Colima and Rancher Desktop

This is the reference runtime. CI runs on Docker Engine, and the maintainers
develop on OrbStack. Everything that speaks the Docker API is `docker` to
dbpantry ([Container runtimes](runtimes.md)).

## Requirements

- Docker Engine 24+ with the Compose v2 plugin (`docker compose version`), or
  the standalone `docker-compose` v2 binary. Compose v1 (`docker-compose` 1.x,
  Python) is refused.
- About 4 GB of RAM for the engine, or 8 GB with Cassandra.

```bash
scripts/runtime info        # runtime: docker ... compose: docker compose [compose-v2]
make up
```

If Podman is also installed, `auto` still picks Docker whenever Docker's engine
answers. Set `CONTAINER_RUNTIME=docker` to make that explicit.

## OrbStack

This works with no extra configuration. `scripts/runtime info` reports
`provided by orbstack`. OrbStack's optional Kubernetes cluster runs beside the
stack; dbpantry doesn't use it.

Testing note: this is the setup dbpantry is developed on. If `/var/run/docker.sock`
has been repointed (Podman Desktop and `podman machine start` can do this), the
`docker` CLI still reaches OrbStack through its context (`docker context ls`).

## Colima

```bash
colima start --cpu 4 --memory 8 --vm-type vz --mount-type virtiofs
```

Use Colima's default docker runtime. With `colima start --runtime containerd`
you are on nerdctl instead ([nerdctl](runtime-nerdctl.md)). virtiofs is
recommended because the stack bind-mounts `scripts/`, `secrets/` and `certs/`.

Testing note: the Docker API is the same as OrbStack's. Run `make verify-fast`,
then `make verify CHECKS=08,15,22,28`.

## Rancher Desktop

Choose **dockerd (moby)** under Preferences, then Container Engine. With
**containerd** selected, Rancher Desktop provides nerdctl instead
([nerdctl](runtime-nerdctl.md)). You can turn off Kubernetes; dbpantry doesn't
need it.

Testing note: `scripts/runtime info` reports `provided by rancher-desktop`. The
same checks apply as for Colima.

## Docker Desktop

This works with no extra configuration. Give it at least 4 GB of memory under
Settings, then Resources.
