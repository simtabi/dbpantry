# Engine manifest

The `dbpantry` binary reads every `engines/*/engine.conf` and publishes it as one versioned JSON manifest, with the endpoints and test fixtures built from it.

This is phase 1 of the Go port (design.md D-68). The descriptors stay the authored source: the
binary reads them, it never replaces them, and every script keeps working without it. Deleting the
binary rolls phase 1 back.

## Build it

The binary is built from source. No release publishes it yet, and nothing in this repository
downloads one.

```bash
make dbpantry                # builds build/<os>/dbpantry, e.g. build/macos/dbpantry (needs Go 1.27)
build/macos/dbpantry version
```

The Go module lives in `src/` (`github.com/simtabi/dbpantry/src`). Run Go against it as
`go -C src …`, or through the Makefile: `make go-ci` (format check, vet, race tests), `make go-lint`.

The binary finds the checkout from `--root`, then `DBPANTRY_DIR`, then the nearest directory above
the current one that holds `engines/` and `scripts/engine-lib.sh`, then the nearest such
directory above the binary (so `build/macos/dbpantry` finds its own checkout). `MDB_ENGINES_DIR` moves where descriptors are read from, as it does for
`scripts/engine-lib.sh`.

## Commands

| Command | Make target | Prints |
|---|---|---|
| `dbpantry manifest` | `make manifest` | the manifest as JSON |
| `dbpantry manifest -o tsv` | `make manifest OUTPUT=tsv` | every descriptor key as `engine<TAB>key<TAB>value` |
| `dbpantry manifest -o engines` | `make manifest OUTPUT=engines` | one line per engine: name, implemented, buildable, pooler service |
| `dbpantry manifest -o helm` | `make manifest OUTPUT=helm` | the facts each chart engine block must restate |
| `dbpantry manifest --matrix versions\|engines` | `make manifest MATRIX=versions` | the CI build matrix, byte-identical to `scripts/ci-matrix` |
| `dbpantry manifest check-helm [VALUES]` | — | fails when the chart restates a fact differently |
| `dbpantry endpoints [-o json]` | `make endpoints` | where to connect to each engine |
| `dbpantry fixture up --profiles pg,mysql` | `make fixture-up PROFILES=pg,mysql` | boots disposable engines on tmpfs |
| `dbpantry fixture down` | `make fixture-down` | stops them |

Exit codes: 0 success, 1 failure, 2 a usage error (an unknown flag, output or profile).

## The manifest

`schema_version` is 1. Adding a field keeps the version. Removing or renaming a field, or changing
what one means, increments it, and a consumer must refuse a version newer than it knows.

| Field | From | Notes |
|---|---|---|
| `paradigms` | the vocabulary | relational, document, wide-column, key-value, graph, search, vector, time-series (shared with mesobia) |
| `engines[].name`, `title`, `family` | `MDB_ENGINE_NAME`, `_TITLE`, `_FAMILY` | `name` is the Compose service (`pg`, not `postgres`) |
| `paradigm`, `paradigms` | `_PARADIGM`, `_PARADIGM_ALSO` | `paradigms` is the primary one first, then any others the engine also serves |
| `versions`, `default_version`, `bases` | `_VERSIONS`, `_DEFAULT_VERSION`, `images/bases.tsv` | `bases` maps each maintained version to its digest-pinned upstream image |
| `port`, `port_offset`, `host_port_default` | `_PORT`, `_PORT_OFFSET`, `_HOST_PORT_DEFAULT` | `port` is the port on the `mdb` network |
| `root_secret`, `root_secret_file` | `_ROOT_SECRET` | the file name under `secrets/` and its path in a container, `/run/secrets/…`; never a value |
| `principals` | `_ROOT_SECRET` | `[{role: admin, password_file}]`; see [Principals](#principals) |
| `auth_method`, `auth_off_by_default_upstream`, `tls_always` | `_AUTH_METHOD`, `_AUTH_OFF_BY_DEFAULT_UPSTREAM`, `_TLS_ALWAYS` | |
| `pooling`, `pooler` | `_POOLING`, `_POOLER_*` | `pooler` exists only when `gen-compose` generates one: external pooling, an image, and a `compose-pooler.yml` |
| `pooler.superuser` | — | always `false`: pgBouncer's auth query excludes superusers and ProxySQL mirrors every user except `root` |
| `implemented`, `buildable` | `_PUBLISH`, `images/<engine>/Dockerfile` | `implemented` is `engine_implemented`; `buildable` is what `scripts/ci-matrix` builds |
| `heavy`, `min_memory_mb`, `boot_budget_seconds` | `_HEAVY`, `_MIN_MEMORY_MB` | the boot budget is 90 seconds, 300 for a heavy engine: the budgets check 32 proves on every run |
| `license`, `osi_approved`, `license_note`, `publish` | `_LICENSE`, `_OSI_APPROVED`, `_LICENSE_NOTE`, `_PUBLISH` | |
| `descriptor` | every key | the descriptor verbatim; the keys stay a public interface |

`engine.conf` remains the contract mesobia reads directly until it moves to the manifest (refactor
plan N21). Descriptor keys are add-only.

### The descriptor subset

The binary reads a strict subset of shell: blank lines, comment lines and `MDB_ENGINE_KEY=value`
lines, where the value is empty, bare (letters, digits and `_ . / : @ % + = , -` only),
`'single-quoted'`, or `"double-quoted"` with no `$`, `` ` ``, `\` or `"` inside. Nothing may
follow a closing quote, and a key may be assigned once. A descriptor outside the subset is refused
with its file and line, rather than read differently from the way bash reads it. Check 62 compares
every value bash sees after `engine_load` with the manifest.

### `MDB_ENGINE_PARADIGM_ALSO`

Optional, space-separated: the further paradigms an engine serves, from the same vocabulary, never
repeating the primary one. ClickHouse, when it lands, is `relational` with
`MDB_ENGINE_PARADIGM_ALSO=time-series` (mesobia ADR 0047). Check 31 validates both keys.

## Endpoints

`dbpantry endpoints -o json` reports, for every implemented engine:

| Field | Meaning |
|---|---|
| `direct.network` | `host:port` on the `mdb` network (`pg:5432`); an application that joins it uses this |
| `direct.published` | the host port, or `null` when `MDB_PUBLISH=none`. `via` is `direct` (the engine's own port) or `proxy` (Caddy) |
| `pooled` | the pooler, when the engine has one: `network`, `published` (direct mode only), `superuser: false` |
| `enabled` | whether the active profiles start the service |
| `boot_budget_seconds` | how long to wait for it to become ready |
| `principals` | admin, plus `app` and `read-only` with `--project NAME` |

Published ports are the ones `scripts/gen-compose` writes: `MDB_PORT_BASE` plus the engine's
offset, a pooler at plus 100, unless `MDB_<ENGINE>_HOST_PORT` or `MDB_<ENGINE>_POOLER_HOST_PORT`
sets one. `MDB_PUBLISH` and `MDB_PORT_BASE` come from the env file only, as `gen-compose` reads
them; the host-port overrides and `MDB_BIND_ADDR` follow Compose, the environment over the file.
Profiles come from `--profiles`, then `PROFILES`, then `MDB_PROFILES`, then `pg,ui`, with `pooler`
expanded as `scripts/profiles` expands it.

Superusers cannot authenticate through a pooler, so admin work uses `direct`.

### Principals

Principals are paths, never values. `admin` is the engine's root secret,
`/run/secrets/<root_secret>`. `--project NAME` adds the two roles `make new-project NAME=<name>`
creates: `app` (user `<name>`, `/run/secrets/<engine>_<name>_password.txt`) and `read-only` (user
`<name>_readonly`, `/run/secrets/<engine>_<name>_readonly_password.txt`). The files exist once the
project has been provisioned.

## Fixtures

`dbpantry fixture up --profiles pg,mysql,mongodb` runs what `make test-profile` runs, with the
profiles named: `scripts/render-config`, `scripts/gen-compose`, then
`scripts/runtime up -f docker-compose.yml -f compose.engines.yml -f compose.test.yml`, which waits
for health. `fixture down` stops that stack. `--dry-run` prints the commands.

- Data lives on tmpfs (the test overlay), so a fixture holds no volume and loses its data on
  stop. Check 66 proves that from the Compose config; check 64 proves it on a running stack.
- Only engine profiles and `pooler` are accepted. `ui`, `backup`, `metrics`, `prod` and `ha` start
  services with volumes of their own, and are refused with exit code 2.
- FerretDB is refused until its image exists: it has a descriptor and no Compose service.
- Fixtures run on the Compose backend only.

## What phase 1 does not do yet

| Item | Where it lands |
|---|---|
| Certificates for every engine (`scripts/certs` still issues pg, mysql and mariadb) | changes what `make certs` writes; needs its own decision |
| The backup CronJob claims (`scripts/backends/kubernetes.sh`) follow the Compose backup sidecars, not the manifest | phase 6, the data context |
| HA endpoints (HAProxy write and read ports) | phase 4, topology |
| Generating the README and versions tables from the manifest | phase 2 |
| A FerretDB test overlay | with FerretDB's image |
| Dangerous server features off in fixtures (N13's second half) | with each engine wave |
| Binary releases | phase 9; until then, build from source |

---

[← Docs index](../../README.md#documentation)
