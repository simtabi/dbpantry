# nerdctl (containerd)

nerdctl is containerd's Docker-compatible CLI. You get it with Rancher Desktop
(containerd engine), Colima (`--runtime containerd`), Lima, or a plain
containerd host. dbpantry drives it through `nerdctl` and `nerdctl compose`.

Status: **best effort, untested.** The layer resolves it, and check 48 tests
that resolution against a stub, but no maintainer runs the stack on nerdctl
regularly. Reports are welcome.

```bash
CONTAINER_RUNTIME=nerdctl scripts/runtime info
# compose:   nerdctl compose [nerdctl]
```

## What dbpantry does differently on nerdctl

- `nerdctl compose up` has no `--wait`, so `scripts/runtime up` polls container
  health, for up to `MDB_WAIT_TIMEOUT` seconds.
- `COMPOSE_FILE` and `COMPOSE_PROFILES` are passed as explicit `-f` and
  `--profile` flags.
- Rootless nerdctl (containerd-rootless) can't publish ports below 1024, so set
  `MDB_CADDY_HTTP_PORT=8080` and `MDB_CADDY_HTTPS_PORT=8443` (check-env says so).

## Known gaps

- `nerdctl build` needs BuildKit (`buildkitd`) running. The Dockerfiles use
  `FROM --platform=$BUILDPLATFORM` and `COPY --chmod`, which BuildKit supports.
- The Compose features the stack uses (profiles, healthcheck-gated
  `depends_on`, named volumes with explicit names, `ulimits`) are all
  documented as supported by `nerdctl compose`. Older nerdctl releases (< 1.0)
  are not expected to work.
- The `com.docker.compose.project` label, which `check-env` uses for port
  collisions, is set by nerdctl compose too.

## Testing notes

```bash
export CONTAINER_RUNTIME=nerdctl
scripts/runtime info
make verify-fast
make build && make up
make verify CHECKS=08,15,22,28
```
