# Apple container (macOS 26)

Apple's [`container`](https://github.com/apple/container) tool runs each Linux
container in its own lightweight VM on Apple silicon. dbpantry drives it
through [socktainer](https://socktainer.github.io), a third-party daemon that
serves the Docker API on top of it. To dbpantry it is the `docker` runtime with
a different provider, so `docker compose` and every script work unchanged
(design.md D-63).

Status: **supported, tested** on macOS 27.2 (arm64) with container 1.6.1 and
socktainer 1.6.0. Checks 08, 21, 22, 28 and 41 pass on it (PG, MySQL and
MariaDB). MongoDB, Cassandra, the HA profile and the UIs have not been run on it.
socktainer describes its Docker API coverage as partial and aimed at local
development. Use it for that, not for production.

## Requirements

- macOS 26 on Apple silicon.
- `container` 1.6 and socktainer. socktainer 1.6.0 warns that it was built
  against container 1.6.0 when it finds 1.6.1 and carries on; everything above
  was tested with that pair.

```bash
brew install socktainer          # pulls in the container formula (1.6.x)
container system start --enable-kernel-install
```

If you installed `container` from Apple's .pkg (into /usr/local/bin), check
which one is first on your PATH: socktainer needs the 1.6 API server. Both can
be installed; only one API server runs at a time.

## Starting socktainer

```bash
brew services start socktainer   # at login, or once in a terminal:
HOME=/opt/homebrew/var/run/socktainer /opt/homebrew/opt/socktainer/bin/socktainer --no-docker-context
```

Then point the Docker CLI at it, per shell:

```bash
export DOCKER_HOST=unix:///opt/homebrew/var/run/socktainer/.socktainer/container.sock
scripts/runtime info             # engine: reachable, provided by apple-container (socktainer)
```

socktainer can also register a `socktainer` docker context
(`docker context use socktainer`); `--no-docker-context` leaves your contexts
alone. It also starts a DNS server on port 2054 on every interface, used for
service names inside Compose networks.

## Memory: add compose.apple.yml

Every container gets a VM whose memory is fixed at creation, 1 GiB unless
Compose asks for more. Each engine tunes its caches from
`MDB_<ENGINE>_MEM_LIMIT` (PG's default 4g gives `shared_buffers = 1GB`), so in
a 1 GiB VM PG would be killed under load. `compose.apple.yml` gives each engine
a VM of exactly its limit. Add it to the end of `COMPOSE_FILE` in `.env`:

```dotenv
COMPOSE_FILE=docker-compose.yml:compose.engines.yml:compose.proxy.yml:compose.ha.yml:compose.apple.yml
```

dbpantry refuses to run on socktainer without it. The VMs reserve that memory
while they run: PG 4g, MySQL 2g and MariaDB 2g is 8 GB of host RAM. Lower the
`MDB_*_MEM_LIMIT` values on a smaller Mac.

## Images

socktainer has its own image store; images built on OrbStack or Docker Desktop
are not in it. Either build there (`make build` passes `--load` on socktainer,
because its BuildKit builder otherwise keeps the result in the build cache), or
copy an image across:

```bash
docker save -o /tmp/pg17.tar localhost/dbpantry-pg:17          # on the other engine
DOCKER_HOST=unix:///opt/homebrew/var/run/socktainer/.socktainer/container.sock \
    docker load -i /tmp/pg17.tar
```

Registry pulls work as usual.

## What differs

| Concern | Apple container via socktainer |
|---|---|
| Memory | fixed per-container VM; set through compose.apple.yml |
| CPUs | whole vCPUs per VM (default 4); fractions are floored |
| Named volumes | ext4 images; socktainer removes `lost+found` for Postgres. They default to `nosync` (guest fsync not flushed on demand), so a host crash can lose the last writes |
| `exec` | socktainer documents exec without an interactive mode; SQL piped into `scripts/runtime exec pg psql` was tested, interactive `make psql` was not |
| Restart policies | enforced by the socktainer process, not resumed after it restarts |
| Private registries | depend on `container` registry login |

## Stopping and removing

```bash
make down
brew services stop socktainer    # or stop the foreground process
container system stop
```

`brew uninstall socktainer container` removes both; Apple's data stays in
`~/Library/Application Support/com.apple.container` until you delete it.
