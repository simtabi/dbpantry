# Incus

> **Untested.** `MDB_BACKEND=incus` was built from the incus-compose
> documentation and is checked against stub `incus` and `incus-compose`
> binaries only (verify check 58). It has not been run on a live Incus host.
> Treat it as a starting point and report what breaks.

The `incus` backend (design.md D-66) runs the same Compose files as the
default backend, on Incus system containers, through
[incus-compose](https://incus-compose.org), a drop-in for `docker compose`.
Configuration stays in `.env`; profiles, healthchecks and secrets work as they
do on Compose.

## Requirements

- Incus 7.0.1 LTS or 7.2+ with an https remote and OCI registries added (the
  incus-compose [getting started](https://incus-compose.org) walkthrough).
- `incus-compose` on PATH (Linux: the install script or a release archive;
  macOS: `brew install tallica/tap/incus-compose`).
- On macOS, Incus runs in a VM: `brew install colima incus` then
  `colima start --runtime incus`. Remove it with `colima delete`.

## Use

```bash
# .env
MDB_BACKEND=incus
#MDB_INCUS_POOL=default          # storage pool of the project's volumes
#MDB_INCUS_COMPOSE=incus-compose # the command, if it is not on PATH by that name
```

`make up`, `make down`, `make status`, `make logs`, `make psql` (and the other
shells), `make backup` and `make restore` go through the backend. Each
`COMPOSE_PROJECT_NAME` becomes an Incus project of the same name; a service's
instance is `<service>-1` in it:

```bash
incus list --project mdb
incus config get pg-1 user.healthcheck.status --project mdb
```

## How the verbs map

| Verb | Incus |
|---|---|
| up | `incus-compose up -d`, then wait for every service's `user.healthcheck.status` to be `healthy` (written by the ic-healthd sidecar) |
| down, status, logs | `incus-compose down`, `ps`, `logs` |
| exec `[-u USER]` | `incus-compose exec -T [-u USER] SERVICE ...` |
| cp | `incus-compose cp SERVICE:PATH DEST` |
| running, health | `incus list --project P` (RUNNING `<service>-<n>`) and `user.healthcheck.status` |
| volumes, volume_rm | custom volumes in `incus storage volume list MDB_INCUS_POOL --project P` |
| image_load, secrets_sync | nothing: incus-compose pulls or builds, and `secrets/` is bind-mounted |

## Not known yet (verify these first)

- Whether the Compose hardening keys (`cap_drop`, `security_opt`,
  `ulimits`, `shm_size`) are honoured; incus-compose's compatibility matrix is
  the reference. Incus-specific limits go in `x-incus` blocks.
- Volume names as incus-compose creates them, and so whether `make destroy`
  recognises the recovery (`*_pitr`) volumes.
- Ports below 1024 and 127.0.0.1 binds through proxy devices.
- Scripts that require the Compose backend (`pitr restore`, `upgrade`) refuse
  on Incus, as on Kubernetes.
