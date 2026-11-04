# Incus

The `incus` backend (design.md D-66) runs the same Compose files as the
default backend, on Incus system containers, through
[incus-compose](https://incus-compose.org), a drop-in for `docker compose`.
Configuration stays in `.env`; profiles, healthchecks and secrets work as they
do on Compose.

> **Tested once, on one host.** On 2026-11-04 the `pg` and `ui` profiles were
> run end to end on Ubuntu 26.04 (arm64) with Incus 7.0.2 and incus-compose
> 1.3.5: `make init`, `make up` with every service `healthy`, `make status`,
> `make logs`, a query through the backend's exec, `make backup ENGINE=pg`,
> and a row surviving `make down` and `make up`. The other engines, HA and PITR
> have not been run on Incus. Verify check 58 pins the mapping against stub
> binaries.

## Requirements

- **Incus 7.0.2 LTS or 7.5+.** incus-compose refuses older daemons when it
  connects. Ubuntu's own `incus` package is 6.0; use the upstream packages from
  [Zabbly](https://github.com/zabbly/incus) (`lts-7.0`).
- **incus-compose on PATH**: a [release archive](https://github.com/lxc/incus-compose/releases)
  (check it against the release's `checksums.txt`) or its install script.
- **The server listening on the network**, even for a local client:
  `incus config set core.https_address=<host-ip>:8443`. incus-compose copies
  images between Incus projects in pull mode, and `up` fails without it.
- **An https remote as the client's default**, for health checks (the
  ic-healthd sidecar reaches Incus over HTTPS):

  ```bash
  incus remote generate-certificate
  incus config trust add-certificate ~/.config/incus/client.crt
  incus remote add local-https https://<host-ip>:8443 --accept-certificate
  incus remote switch local-https
  ```

- **Podman or Docker, for builds.** incus-compose builds the toolkit's own
  images (`dbpantry-pg`, `dbpantry-caddy`) with one of them. Podman runs
  rootless; run `make` from a normal login session, not under `sg`, which
  changes the primary group and makes Podman's uid mapping fail.
- On macOS, Incus runs in a VM: `brew install colima incus` then
  `colima start --runtime incus`. Remove it with `colima delete`.

## Use

```bash
# .env
MDB_BACKEND=incus
#MDB_INCUS_POOL=default          # storage pool of the project's volumes
#MDB_INCUS_COMPOSE=incus-compose # the command, if it is not on PATH by that name
#MDB_NETWORK_GATEWAY=172.28.0.1  # default: the first host of MDB_NETWORK_SUBNET
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
| exec `[-u USER]` | `incus-compose exec -T [-u UID] SERVICE ...`; a user name is resolved to its uid inside the instance first |
| cp | `incus-compose cp SERVICE:PATH DEST` |
| running, health | `incus list --project P` (RUNNING `<service>-<n>`) and `user.healthcheck.status` |
| volumes, volume_rm | custom volumes in `incus storage volume list MDB_INCUS_POOL --project P` |
| image_load, secrets_sync | nothing: incus-compose pulls or builds, and `secrets/` is bind-mounted |

## Where Incus differs, and what the backend does about it

Measured on the 2026-11-04 run. Each one is pinned by check 58.

| Difference | What the backend does |
|---|---|
| A network that names a subnet and no gateway is refused ("ipam gateway cannot be empty") | `compose.incus.yml`, which only this backend loads, names the gateway: `MDB_NETWORK_GATEWAY`, else the subnet's first host |
| `exec -u` takes a numeric uid only | a name is resolved with `id -u` inside the instance; an unknown name is refused before anything runs |
| A secret's bare `target` lands at `/<name>`, not `/run/secrets/<name>` | secret targets in the Compose files are absolute paths, the same file on both backends |
| Volumes are named `vol-<name>` (`vol-pg-data`, `vol-pg-pitr`) | `make destroy` treats `*-pitr` as a recovery volume, as it does `*_pitr` on Compose |
| Changing a published port on a running stack is not applied to an existing instance | `make down` then `make up` |
| Ports are proxy devices, not the build runtime's | check-env's rootless low-port rule applies on Compose only; collisions are still checked on every backend |

## Not known yet

- Whether the Compose hardening keys (`cap_drop`, `security_opt`, `ulimits`,
  `shm_size`) are all honoured; incus-compose's compatibility matrix is the
  reference. Incus-specific limits go in `x-incus` blocks.
- The engines other than PostgreSQL, the HA topology and PITR on Incus.
- `make status` asks for every profile, so incus-compose warns about the
  images, volumes and instances of profiles that are not running before it
  prints the table. The table is right; the warnings are noise.
- Scripts that require the Compose backend (`pitr restore`, `upgrade`) refuse
  on Incus, as on Kubernetes (`upgrade` checked: exit 64).

---

[← Docs index](../../README.md#documentation)
