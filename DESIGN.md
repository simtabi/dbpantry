# dbpantry design

Step 1 output: research resolved, versions pinned, spec reconciled. **No implementation code exists yet and none will be written until this document is approved.**

Everything below was verified during this session against live registries, the GitHub API, and — where prose was ambiguous or absent — against containers actually booted on arm64. Where a claim came from a doc page rather than an executed check, it says so.

## 1. Settled identity

| Field | Value |
|---|---|
| Repository | `github.com/simtabi/dbpantry` (was `dbpantry-docker`, and before that `db-toolkit-docker`) |
| Images | `ghcr.io/simtabi/dbpantry-{pg,mysql,mariadb,cli}` |
| Docs | [`docs/`](docs/) in this repository; no hosted site yet |
| Product page | none yet |
| Env prefix | `MDB_` |
| Compose project | `mdb` |
| Registry visibility | private until explicitly flipped public (section 19 default) |

Resolves the three-way conflict recorded in decision **D-02**.

## 2. Final service map

| Service | Image | Profile | Internal port | Host port env | Published by default |
|---|---|---|---|---|---|
| `pg` | `ghcr.io/simtabi/dbpantry-pg:<ver>` | `pg` | 5432 | `MDB_PG_HOST_PORT` | no |
| `mysql` | `ghcr.io/simtabi/dbpantry-mysql:<ver>` | `mysql` | 3306 | `MDB_MYSQL_HOST_PORT` | no |
| `mariadb` | `ghcr.io/simtabi/dbpantry-mariadb:<ver>` | `mariadb` | 3306 | `MDB_MARIADB_HOST_PORT` (3307) | no |
| `adminer` | `adminer:5.4.1` | `ui` | 8080 | `MDB_ADMINER_HOST_PORT` | no (via Caddy) |
| `phpmyadmin` | `phpmyadmin:5.2.3` | `ui` | 80 | `MDB_PMA_HOST_PORT` | no (via Caddy) |
| `pgadmin` | `dpage/pgadmin4:9.9` | `ui` | 80 | `MDB_PGADMIN_HOST_PORT` | no (via Caddy) |
| `caddy` | `caddy:2.10.2` | `ui`, `prod` | 80/443 | `MDB_CADDY_HTTP_PORT` / `_HTTPS_PORT` | yes (the only web surface) |
| `backup` | `nfrastack/db-backup:4.9.0` | `backup` | — | — | no |
| `pgbackrest` | built into `dbpantry-pg` | `prod`, `ha` | — | — | no |
| `pg-exporter` | `quay.io/prometheuscommunity/postgres-exporter:v0.19.0` | `metrics` | 9187 | — | no |
| `mysqld-exporter` | `prom/mysqld-exporter:v0.18.0` | `metrics` | 9104 | — | no |
| `pgbouncer` | `dbpantry-pgbouncer` (built) | `prod` (required), `ha` | 6432 | `MDB_PGBOUNCER_HOST_PORT` | no |
| `etcd` | `gcr.io/etcd-development/etcd:v3.6.6` | `ha` | 2379/2380 | — | no |
| `haproxy` | `haproxy:3.2-alpine` | `ha` | 5432 w / 5433 r | `MDB_HAPROXY_*_PORT` | no |
| `cli` | `ghcr.io/simtabi/dbpantry-cli:<ver>` | on demand | — | — | no |

Profiles: engine selectors `pg` / `mysql` / `mariadb`; mode overlays `ui` / `backup` / `metrics` / `prod` / `test` / `ha`. Default `COMPOSE_PROFILES=pg,ui`.

## 3. Pinned versions

All digests resolved from the live registry this session. **Every image below is confirmed `linux/amd64` + `linux/arm64`.** Engine rows list maintained versions only (**D-57**); `images/bases.tsv` is the source of truth.

| Component | Pin | Digest | Source |
|---|---|---|---|
| pgvector PG17 | `0.8.6-pg17` | `sha256:7ae6051efd0e60444282c27c7e141af07f322ce033300e727a49c3dd11075e38` | [pgvector](https://hub.docker.com/r/pgvector/pgvector) |
| pgvector PG18 | `0.8.6-pg18` | `sha256:691673308c99d2161ba298736f3147f1f22d79de2fb7ec93ae9b4afcab870b62` | ibid |
| MySQL 8.4 LTS | `8.4.11` | `sha256:b3b90af2a6552ae30c266fdb7d5dd55f3afb72404bb78d37fe8a23eb857fd3fb` | [mysql](https://hub.docker.com/_/mysql) |
| MySQL 9.7 LTS | `9.7.2` | `sha256:257388edf9c84dbc04c763625446d5f3fa6ed60d1b0873bc552c614ba0a7ab4e` | ibid |
| MariaDB 11.8 LTS | `11.8.9` | `sha256:6422478cb8e159f080fb1d8ccf65101e26fe51385787fde7d16c3b165a331f15` | [mariadb](https://hub.docker.com/_/mariadb) |
| MariaDB 12.3 LTS | `12.3.3` | `sha256:d46e102fa8b9b72bfcdeb56f9dd538742092119dc8b11f31fe662605cd234cd9` | ibid |
| MongoDB 8.0 | `8.0.34` | `sha256:b9924c330a8bbc5800fe9c69f04a84e221eca7b28fd049a0354147d9908fa142` | [mongo](https://hub.docker.com/_/mongo); referenced, not published |
| MongoDB 9.0 | `9.0.4` | `sha256:452bdaf2127b8ff851e31410a267809b9829eded219979f2210e7c7efb85a058` | ibid |
| Cassandra 5.0 | `5.0.9` | `sha256:1d18946ead0b0d22ed0371bd7384d86f540465c489f2935e88df35e755073f6b` | [cassandra](https://hub.docker.com/_/cassandra) |
| Adminer | `5.4.1` | `sha256:a3167350c4eb9ae4473b8ea0f49c8e5ae74c87b240ee2f6086521dba2a6bf243` | [adminer](https://hub.docker.com/_/adminer) |
| phpMyAdmin | `5.2.3` | `sha256:5e2289bcd500868ed4ac9261ddd7cbcc6e20036f83031ade72667b15dca31c60` | [phpmyadmin](https://hub.docker.com/_/phpmyadmin) |
| pgAdmin | `9.9` | `sha256:5d9624a93634d1c5e595619cc57b1d330758120d1baf445fa97300c0c1fc3c0a` | [pgadmin4](https://www.pgadmin.org/docs/pgadmin4/latest/container_deployment.html) |
| Caddy | `2.10.2` | `sha256:c3d7ee5d2b11f9dc54f947f68a734c84e9c9666c92c88a7f30b9cba5da182adb` | [caddy](https://caddyserver.com/docs/) |
| pgBouncer | `dbpantry-pgbouncer` (built from `debian:12-slim` + PGDG) | n/a — built here | see **D-10**, superseded by **D-48** |
| Go toolchain (build stage) | `golang:1.27.3-bookworm` | `sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9` | [golang](https://hub.docker.com/_/golang); see **D-51** |
| gosu / rclone / postgres_exporter (in-image) | `1.19` / `v1.75.1` / `v0.20.1`, rebuilt from tag | n/a — built here | see **D-51** |
| postgres_exporter | `v0.19.0` | `sha256:e8a170b85eab07c75c2b0f3aa2806be5c2fe5bba46fd0336914e1f36572cde08` | prometheus-community |
| mysqld_exporter | `v0.18.0` | `sha256:2598c0571f383708e19016d119bb45c06128a9ebc962c9f49483278ac5a94c41` | prometheus |
| db-backup | `nfrastack/db-backup:4.9.0` | `sha256:80d3bc0524611d85fd82738b228f88a92077ad2d0a36ae32237d6a25a93e2439` | see **D-03** |
| etcd | `v3.6.6` | `sha256:60a30b5d81b2217555e2cfb9537f655b7ba97220b99c39ee2e162a7127225890` | etcd-development |
| s6-overlay | `v3.2.3.2` (2026-07-16) | release assets `s6-overlay-{noarch,x86_64,aarch64}.tar.xz` | [s6-overlay](https://github.com/just-containers/s6-overlay) |
| Patroni | `4.1.4` (pip) | no image published — see **D-11** | [patroni](https://github.com/patroni/patroni) |
| pgBackRest | `2.59.0` | PGDG apt package | [pgbackrest](https://pgbackrest.org) |

Base distro note: the unsuffixed `pgvector/pgvector:0.8.6-pgNN` tags are **Debian 12 bookworm** (verified: `PG 17.10 (Debian 17.10-1.pgdg12+1)`). `-trixie` variants exist; see **D-16**.

## 4. PG extension matrix — arm64 ground truth

Verified by booting `pgvector/pgvector:0.8.6-pg17` **on arm64** and querying PGDG apt directly, not by reading availability pages.

| Extension | arm64 source | Version | Status |
|---|---|---|---|
| vector | PGDG `postgresql-17-pgvector` | 0.8.6 | apt |
| postgis (+topology, raster) | PGDG `postgresql-17-postgis-3` | 3.6.4 | apt |
| pg_cron | PGDG `postgresql-17-cron` | 1.6.7 | apt |
| pgaudit | PGDG `postgresql-17-pgaudit` | 17.1 | apt |
| pg_repack | PGDG `postgresql-17-repack` | 1.5.3 | apt |
| pg_partman | PGDG `postgresql-17-partman` | 5.5.0 | apt — **spec's arm64 concern was misplaced** |
| pgtap | PGDG `postgresql-17-pgtap` | 1.3.4 | apt |
| http | PGDG `postgresql-17-http` | 1.7.2 | apt — **also fine on arm64** |
| hypopg | PGDG `postgresql-17-hypopg` | 1.4.3 | apt (bonus) |
| plpython3u | PGDG `postgresql-plpython3-17` | 17.10 | apt, behind `MDB_PG_PLPYTHON` |
| pg_stat_statements, pg_trgm, uuid-ossp, pgcrypto, citext, hstore | base image contrib | — | already present |
| **pg_graphql** | official `.deb`, **pg14–18 × amd64+arm64** | 1.6.1 | download release asset |
| **pg_net** | source build (C + libcurl) | 0.20.5 | builder stage |
| **pgsodium** | source build (C + libsodium) | 3.1.11 | builder stage |
| **pgjwt** | vendored SQL (pure plpgsql) | unversioned | see **D-06** |

**There is no extension in section 5 that cannot ship on arm64.** The honest answer to the named unknown is that the gap does not exist; four extensions simply need a non-apt path, and all four are architecture-portable. `pg_graphql` — the one most likely to have been a real problem, being Rust/pgrx — publishes prebuilt arm64 `.deb`s covering our entire 15–18 matrix.

## 5. Capacity truth table

Reproduced from SPEC.md section 21.1 as the sizing contract:

| Tier | MDB_MEM | shared_buffers | max_connections | RAM on connections | pgBouncer pool | App connections |
|---|---|---|---|---|---|---|
| dev-small | 2 GB | 512 MB | 100 | ~1.0 GB | direct | 100 |
| prod-small | 4 GB | 1 GB | 100 | ~1.0 GB | 25 | ~1000 |
| prod-medium | 8 GB | 2 GB | 200 | ~2.0 GB | 50 | ~2500 |
| prod-large | 16 GB | 4 GB | 300 | ~3.0 GB | 100 | ~5000 |
| anti-pattern | any | any | 500 direct | ~5.0 GB idle | none | thrashing |

~10 MB per direct PG connection. Pooling is **required** under `prod`, enforced by `check-env`.

## 6. Environment variable reference

### Core
| Variable | Default | Description | Profile |
|---|---|---|---|
| `COMPOSE_PROJECT_NAME` | `mdb` | Stable container/network/volume names | all |
| `MDB_PROFILES` | `pg,ui` | Persistent profile selection | all |
| `MDB_TZ` | `UTC` | Timezone baked into every engine | all |
| `MDB_BIND_ADDR` | `127.0.0.1` | Host bind address for every published port | all |
| `MDB_MEM` | `4` | Memory budget (GB) driving PGTune-style presets | all |
| `MDB_CPUS` | `2` | CPU budget driving tuning presets | all |
| `MDB_STOP_GRACE` | `60s` | `stop_grace_period`; Docker's 10s kills mid-checkpoint | all |
| `MDB_SOCKETS` | `false` | Share a unix socket volume across containers | all |
| `MDB_NETWORK_SUBNET` | `172.28.0.0/16` | Internal network subnet | all |
| `MDB_LOG_MAX_SIZE` / `_MAX_FILE` | `10m` / `3` | json-file rotation caps | all |

### PostgreSQL
| Variable | Default | Description | Profile |
|---|---|---|---|
| `MDB_PG_VERSION` | `17` | Selects image tag **and** data volume name | `pg` |
| `MDB_PG_HOST_PORT` | unset | Publish 5432 when set | `pg` |
| `MDB_PG_DATABASES` | unset | `db:user:__FILE__` triplets, comma-separated | `pg` |
| `MDB_PG_INIT_EXTENSIONS` | `vector,pg_stat_statements` | Per-database `CREATE EXTENSION` at provision | `pg` |
| `MDB_PG_SHARED_PRELOAD` | `pg_stat_statements` | Assembled into `shared_preload_libraries` | `pg` |
| `MDB_PG_SHM` | `256m` | `shm_size`; must be ≥ `maintenance_work_mem` for parallel HNSW | `pg` |
| `MDB_PG_PLPYTHON` | `false` | Enable untrusted plpython3u (superuser only) | `pg` |
| `MDB_PG_SLOW_MS` | `500` dev / `0` prod | `log_min_duration_statement` | `pg` |
| `MDB_PG_EMBED_EXPORTER` | `false` | Run exporter under s6 inside the engine container | standalone |
| `MDB_PG_EMBED_BACKUP` | `false` | Run scheduled dumps under s6 inside the container | standalone |
| `MDB_PG_SYNC_MODE` | `off` | Synchronous commit for zero-data-loss replication | `ha` |

### MySQL / MariaDB
| Variable | Default | Description | Profile |
|---|---|---|---|
| `MDB_MYSQL_VERSION` | `8.4` | Image tag + volume name | `mysql` |
| `MDB_MARIADB_VERSION` | `11.8` | Image tag + volume name | `mariadb` |
| `MDB_MYSQL_HOST_PORT` | unset | Publish 3306 when set | `mysql` |
| `MDB_MARIADB_HOST_PORT` | unset | Publish 3307 when set | `mariadb` |
| `MDB_MYSQL_DATABASES` / `MDB_MARIADB_DATABASES` | unset | Same triplet contract as PG | resp. |
| `MDB_MYSQL_NATIVE_PASSWORD_COMPAT` | `false` | Re-enable `mysql_native_password` (8.4 dropped it) | `mysql` |
| `MDB_MYSQL_SLOW_SECONDS` / `MDB_MARIADB_SLOW_SECONDS` | `1` dev / off prod | Slow query log threshold | resp. |
| `MDB_MYSQL_NOFILE` / `MDB_MARIADB_NOFILE` | `10240` | nofile ulimit | resp. |

### TLS and secrets
| Variable | Default | Description | Profile |
|---|---|---|---|
| `MDB_TLS_ENFORCE` | `false` dev / `true` prod | `require_secure_transport` + hostssl-only pg_hba | all |
| `MDB_EXTRA_SANS` | unset | Extra SANs on generated server certs | all |
| `MDB_MTLS` | `false` | Client certificate enforcement | all |
| `MDB_CERT_DAYS` | `825` | Server certificate lifetime | all |
| `MDB_*_PASSWORD_FILE` | `secrets/*.txt` | `_FILE` convention; never a plain value | all |

### Backup
| Variable | Default | Description | Profile |
|---|---|---|---|
| `MDB_BACKUP_SCHEDULE` | `0300` | Nightly start (HHMM) | `backup` |
| `MDB_BACKUP_COMPRESSION` | `ZSTD` | Maps to `DBnn_COMPRESSION` | `backup` |
| `MDB_BACKUP_ENCRYPT` | `false` dev / `true` prod | Maps to `DBnn_ENCRYPT` | `backup` |
| `MDB_BACKUP_RETAIN_DAILY/WEEKLY/MONTHLY` | `7` / `4` / `6` | GFS retention tiers | `backup` |
| `MDB_S3_*` (`BUCKET`, `KEY_ID`, `KEY_SECRET`, `REGION`, `HOST`) | unset | S3-compatible target; wiring present, disabled until set | `backup` |
| `MDB_BACKUP_NOTIFY_URL` | unset | Failure webhook so backups never fail silently | `backup` |

### UI, proxy, HA
| Variable | Default | Description | Profile |
|---|---|---|---|
| `MDB_CADDY_HTTP_PORT` / `_HTTPS_PORT` | `80` / `443` | Caddy published ports | `ui`, `prod` |
| `MDB_UI_DOMAIN` | `db.localhost` | Hostname suffix for UI routes | `ui` |
| `MDB_UI_BASIC_AUTH_USER` / `_HASH` | unset / unset | Required under `prod` | `prod` |
| `MDB_PGADMIN_EMAIL` / `_PASSWORD_FILE` | — | pgAdmin login | `ui` |
| `MDB_PGBOUNCER_POOL_MODE` | `transaction` | Pool mode | `prod`, `ha` |
| `MDB_PGBOUNCER_DEFAULT_POOL_SIZE` | from tier | Per capacity table | `prod`, `ha` |
| `MDB_HA_ENABLE` | `false` | Master HA switch | `ha` |
| `MDB_HA_CLUSTER_NAME` | `mdb-pg` | Patroni scope | `ha` |
| `MDB_HA_NODE_NAME` | hostname | Patroni node identity | `ha` |
| `MDB_HA_ETCD_HOSTS` | `etcd1:2379,...` | etcd endpoints (3-node quorum) | `ha` |
| `MDB_HAPROXY_WRITE_PORT` / `_READ_PORT` | `5432` / `5433` | Leader / replica routing | `ha` |
| `MDB_HA_FAILOVER_BUDGET` | `30` | Seconds allowed for election in the CI assertion | `ha` |

## 7. Decision log

Every spec issue found, and how it was resolved. Entries marked **[needs your call]** are the ones worth your attention.

**D-01 — SPEC.md had no section 21.** The kickoff prompt required reconciling "section 21 (scale, HA, sync)" and producing its capacity truth table; the design doc ended at section 20. *Resolution:* authored section 21 from the verified HA research baseline and appended it to SPEC.md as an approved amendment (you approved this before I wrote it). Its requirements are folded into services, env, commands, CI, and acceptance above.

**D-02 — Three-way identity conflict.** SPEC line 5 says "repo laranail/multidb-server"; section 16 publishes to `ghcr.io/simtabi/`; the actual checkout is `simtabi/multidb-server`. *Resolution:* `simtabi/multidb-server` + `ghcr.io/simtabi/*`, per your call.

**D-03 — The backup sidecar moved namespace and the spec's reference is stale.** `tiredofit/docker-db-backup` is now published as **`nfrastack/db-backup`**. `tiredofit/db-backup` last published `4.1.100` on 2026-03-13; `nfrastack/db-backup` is at `4.9.0` (2026-07-30). *Resolution:* pin `nfrastack/db-backup:4.9.0` by digest; SPEC references updated in docs during phase 4.

**D-04 — [needs your call] The free tier caps backup jobs at 3, which is exactly our engine count.** The project moved to a sponsorware model: "*To unlock advanced features, one must provide a code…*" and "*A limit of 3 can be created when not in advanced mode.*" I verified by parsing every table in the README that **no environment variable is actually `Adv.`-gated** — S3, encryption, checksums, `EXTRA_BACKUP_OPTS` and `BACKUP_GLOBALS` are all free. The only real constraint is the **3-job ceiling**, and pg + mysql + mariadb is exactly 3, leaving zero headroom for a fourth job (a second PG major during a migration window, or an HA replica). *Resolution offered, your pick:*
  - **(a) Recommended — write our own sidecar.** SPEC section 11 already mandates "one dump helper, two callers", and section 6.1 already bakes `rclone` and `zstd` into every engine image. A thin cron container on `multidb-server-cli` driving that same script removes a third-party dependency, the sponsorware risk, and the job ceiling, at the cost of writing retention/notification logic ourselves (~150 lines).
  - **(b) Keep `nfrastack/db-backup:4.9.0`** and accept a hard 3-job ceiling, documenting that a 4th engine or migration-window job requires a sponsor code.
  I will implement (a) unless you say otherwise, and either way the shared dump helper is the single source of dump flags.

**D-05 — Floating tags on the backup image are currently arm64-only.** `nfrastack/db-backup:latest` and `:alpine_3.24` publish **only** `linux/arm64` right now; the versioned `4.9.0` and `4.9.0-alpine_3.24` are correctly amd64+arm64. An amd64 CI runner pulling `latest` would fail outright. *Resolution:* no change needed — CLAUDE.md's "pinned digests everywhere, no `latest`" rule already prevents this. Recorded because it is a live demonstration that the rule earns its keep.

**D-06 — [needs your call] pgjwt is unmaintained.** `michelp/pgjwt` has **no releases at all** and its last push was **2023-03-02**, over three years ago. It is not in PGDG. It is ~50 lines of plpgsql over `pgcrypto`. *Resolution offered:* vendor the SQL into `images/pg/initdb.d/` pinned to a specific commit, documented as vendored-and-frozen; or drop it from the default suite. I lean **vendor**, because it is trivial, arch-independent, and dropping it silently changes the spec's promised extension list. Say the word if you would rather drop it.

**D-07 — Four extensions are not in PGDG; none is an arm64 blocker.** `pg_graphql`, `pg_net`, `pgsodium`, `pgjwt`. *Resolution:* `pg_graphql` 1.6.1 publishes official prebuilt `.deb`s for **pg14–18 on both amd64 and arm64** — download, no build. `pg_net` 0.20.5 and `pgsodium` 3.1.11 build from source in a builder stage (both C, both portable). `pgjwt` per D-06.

**D-08 — The spec's arm64 worry was aimed at the wrong extensions.** It named `http` and `pg_partman` as at-risk. Both are in PGDG for arm64 (`1.7.2` and `5.5.0`, verified on a booted arm64 container). No workaround needed.

**D-09 — PostgreSQL will shut down uncleanly under s6 unless we override the stop signal.** The official postgres image sets `STOPSIGNAL SIGINT` (fast shutdown). s6 sends **SIGTERM**, which PostgreSQL interprets as *smart* shutdown — it waits for every client to disconnect voluntarily, so it hangs until `S6_KILL_GRACETIME` expires and then takes SIGKILL, forcing crash recovery on the next boot. This is silent: the container appears to stop normally. *Resolution:* the PG s6 service directory gets a **`down-signal`** file containing `SIGINT` (confirmed supported in the s6 service directory format, replacing SIGTERM for `s6-svc -d`). `make verify` gets a dedicated clean-shutdown check that asserts the next boot logs no recovery.

**D-10 — The official pgBouncer image is amd64-only and years stale.** `pgbouncer/pgbouncer:latest` is a single-arch (amd64) manifest at version **1.15.0**, while upstream pgBouncer is at **1.25.2**. It would break the arm64 half of the matrix and ship a very old pooler. *Resolution:* use **`edoburu/pgbouncer:v1.25.2-p0`** (multi-arch amd64+arm64, current upstream version).

**D-11 — Patroni publishes no production image.** The Patroni project ships only a Dockerfile and compose explicitly marked development-only. Spilo (`ghcr.io/zalando/spilo-NN`) is multi-arch and production-grade, **but it bundles its own PostgreSQL**, which would replace `multidb-server-pg` entirely and discard our extension suite, baked config, and certs — directly contradicting the spec's core premise. *Resolution:* layer **Patroni 4.1.4 (pip)** onto our own `multidb-server-pg` image as an s6 service, gated by the `ha` profile. Keeps one PG image across dev, prod, and HA. Costs us the Patroni bootstrap wiring that Spilo would have given free; that work lands in phase 7.

**D-12 — etcd pinned at `v3.6.6`**, multi-arch confirmed. Section 21 states the 3-node minimum explicitly, because a 2-node etcd has *lower* availability than a single node.

**D-13 — MariaDB 11.4 auto-TLS does not conflict with our CA (verified empirically).** I booted `mariadb:11.4.12` twice. With no certs configured: `have_ssl=YES`, `ssl_ca`/`ssl_cert`/`ssl_key` all **empty**, and **no `.pem` written to the datadir** — so zero-config SSL uses an **ephemeral, in-memory, per-start** certificate. With our CA-issued certs in `/etc/mysql/conf.d`: our paths are honoured exactly and `require_secure_transport=ON` works. *Resolution:* explicit certs take precedence; auto-generation only fills a gap. Documented consequence: clients **cannot** use verify-full against the ephemeral cert (it changes every restart), which is precisely why `make certs` and the mounted CA are mandatory rather than optional.

**D-14 — MariaDB's "latest 11.x" is now 11.8, and it is also LTS.** The spec's third MariaDB slot assumed a rolling non-LTS release. *Resolution:* offer `10.11` / `11.4` (default) / `11.8`, all LTS, all pinned. Strictly better than tracking a short-lived release.

**D-15 — MySQL 9.x innovation releases are short-lived.** Current is `9.7.2`. Innovation releases are superseded roughly quarterly and unsupported once replaced. *Resolution:* pin `9.7.2`, and document in UPGRADE.md that the 9.x slot is a preview track, not a deployment target; 8.4 LTS stays the default.

**D-16 — pgvector base distro choice.** Unsuffixed `0.8.6-pgNN` tags are Debian 12 **bookworm**; `-trixie` (Debian 13) variants exist. *Resolution:* build on **bookworm** now, because every PGDG extension version in section 4 was verified against `bookworm-pgdg`. A trixie migration is a tracked follow-up, not a phase-2 blocker.

**D-17 — MySQL and MariaDB do not share a base OS.** MySQL official images are **Oracle Linux 9**; MariaDB is **Ubuntu 24.04** (verified: `11.4.12-MariaDB-ubu2404`). Package managers, config paths, and available client tooling differ. *Resolution:* the two Dockerfiles cannot share a tooling layer; `images/mysql/` uses `microdnf`, `images/mariadb/` uses `apt`. Flagged because "baked-in conf.d + client tooling" in section 6 reads as though one recipe covers both. It does not.

**D-18 — `read_only` rootfs conflicts with every engine.** Section 9 asks for read-only root "wherever the engine tolerates it". PostgreSQL needs writable `PGDATA`, `/var/run/postgresql`, and `/tmp`; MySQL/MariaDB need `/var/run/mysqld` and `/tmp`; s6 needs `/run`. *Resolution:* `read_only: true` **with** an explicit tmpfs list per engine, proven by a `make verify` check that boots each engine read-only. If any engine fails that check it gets `read_only: false` with the reason recorded here rather than a silently dropped hardening claim.

**D-19 — Non-root and s6 as PID 1 interact.** s6-overlay must start as root to run its init stages (permissions, cert placement, conf assembly), then drop to the engine user via `s6-setuidgid`. A container-level `USER postgres` would break stage 1. *Resolution:* container runs as root PID 1 (s6), every long-running service drops privileges; `make verify` asserts no engine process runs as uid 0. This satisfies the intent of section 9 while being honest that the *container user* is root.

**D-20 — "No `latest`" vs. the spec's moving tags.** Section 6 lists tags `:9` and `:11`, which are moving aliases. *Resolution:* our published tags keep those friendly aliases, but every **upstream** base is pinned to an exact minor **and digest**; Renovate raises the bumps.

**D-21 — Supply-chain signing.** Section 9 says "signs images with cosign", while the Simtabi org convention settles on `attest-build-provenance` as the mandatory default with cosign optional. *Resolution:* build provenance attestation + syft SBOM are mandatory in CI; cosign is not added in v1 absent a named downstream consumer. Recorded so the SECURITY.md verification instructions match what CI actually produces.

**D-22 — Repo hygiene: `CLAUDE.md` was being silently excluded.** Your global gitignore (`~/.config/git/ignore:22`) ignores `CLAUDE.md`, so the first commit dropped it while committing `.DS_Store`. Since KIT.md makes CLAUDE.md the contract that binds every session and git the memory between them, losing it would quietly break the whole loop. *Resolution:* repo `.gitignore` negates it (`!CLAUDE.md`), which wins over `core.excludesFile`; `.DS_Store` untracked and ignored.

**D-24 — Every script must be bash 3.2 compatible, because macOS ships bash 3.2.** Found in phase 1 when `make lint` died with `mapfile: command not found`. macOS ships GNU bash **3.2.57** (the last GPLv2 release) and this machine has no Homebrew bash, so `mapfile` (bash 4.0), associative arrays (`declare -A`, bash 4.0), and friends are all unavailable. SPEC section 1 promises the toolkit works "identical across macOS, Windows, and Linux", so depending on a bash the platform does not ship would break that promise on the primary development platform — and it would have broken quietly, since CI runs Linux with bash 5. *Resolution:* every script is written to bash 3.2. Array building uses `while IFS= read -r` loops instead of `mapfile`; `check-env`'s port-collision tracking uses a plain string of `port:key` pairs instead of an associative array. A repo `.shellcheckrc` sets `source-path=SCRIPTDIR` so checks can source `lib.sh` through a dynamic path without tripping SC1091. Recorded because it is a standing constraint on every future phase, not a one-off fix.

**D-25 — pgsodium is held at 3.1.9, and no base image would fix it.** pgsodium 3.1.11 fails to compile on our base: `crypto_ipcrypt_NDX_KEYBYTES undeclared`. Its release commit is literally *"Add IP address encryption (crypto_ipcrypt) and bump to libsodium 1.0.2x"* — so 3.1.11 requires libsodium ≥ 1.0.20, while Debian 12 bookworm ships **1.0.18**. I checked whether moving to the trixie base (the D-16 follow-up) would fix it: **Debian 13 trixie ships the same 1.0.18**, so it would not. *Resolution:* pin `PGSODIUM_VERSION=3.1.9`, the last release that builds against 1.0.18, verified by compiling it. The only loss is the brand-new `crypto_ipcrypt` feature, which nothing in SPEC requires. Revisit when either Debian ships libsodium ≥ 1.0.20 or we vendor libsodium.

**D-26 — pg_graphql's .deb ships symlinks that would dangle, and that PG 18 would shadow.** The official package does not put its files in the extension directory: it installs them under `/var/lib/postgresql/extension` and symlinks them from `/usr/share/postgresql/<major>/extension`. Two independent failures follow. First, our builder stage copies only `/usr`, so the links dangle and PostgreSQL reports `Could not open extension control file … No such file or directory` **while `ls` shows the entry present** — a genuinely confusing signature. Second, and worse in production: **PG 18 mounts the data volume at `/var/lib/postgresql` itself**, so even a correct copy would be shadowed by the volume at runtime, and pg_graphql would vanish the moment a user switched majors. *Resolution:* the builder flattens those symlinks into real files and then asserts none remain. Caught by check 09, which is exactly the kind of failure that would otherwise have shipped.

**D-27 — Raising the s6 gracetimes made shutdown worse, not safer.** I set `S6_KILL_GRACETIME` and `S6_SERVICES_GRACETIME` to 30000ms reasoning that a database needs time to stop. Check 10 then failed with a 31s shutdown, and the timestamped log showed why: s6 brought PostgreSQL down cleanly, checkpoint and all, **in about one second** — then sat idle for the remaining 30s before PID 1 exited. The gracetimes do not give a service more time to stop (s6-supervise already waits indefinitely for a longrun to exit); they only govern how long s6 waits *after* everything is already down. *Resolution:* leave both at the s6 default of 3000ms; the database's real budget is `stop_grace_period` / `docker stop -t`. Shutdown went 31s → 4s. Worth recording because the intuitive setting is the harmful one.

**D-28 — Read-only rootfs needed two non-obvious fixes.** SPEC section 9 asks for `read_only` where the engine tolerates it, and D-18 promised an explicit tmpfs list. Two failures had to be fixed to get there, neither of which announces itself clearly:
  - **Docker mounts `--tmpfs` `noexec` by default.** s6 executes `/run/s6/basedir/bin/init`, so the container died at stage 0 with `Permission denied` and exit 126 — a signature that reads like file ownership and is not. `/run` and `/docker-entrypoint-initdb.d` need `exec`.
  - **The user bundle must live in `/etc/s6-overlay/user-bundles.d`, not `/etc/s6-overlay/s6-rc.d/user`.** The legacy location makes s6 *write* a `type` file under `/etc` during boot, which a read-only filesystem refuses. s6 had been printing a deprecation warning about exactly this from the first build.
  Generated state (conf, certificates) also moved to `/run/mdb/`, so the writable set is `/run`, `/tmp`, `/var/run/postgresql`, `/docker-entrypoint-initdb.d`, plus the data volume — and nothing under `/etc` or `/usr`.

**D-29 — D-17 was right about the divergence and wrong about what it costs.** I recorded in D-17 that MySQL (Oracle Linux 9) and MariaDB (Ubuntu 24.04) cannot share a tooling layer, and concluded the two images would need separate recipes. Probing them directly showed the divergence is narrower than that: the package manager differs (`microdnf` vs `apt-get`), the client binaries differ (`mysql`/`mysqladmin` vs `mariadb`/`mariadb-admin`), and the missing tools differ (MySQL lacks `procps`/`unzip`, MariaDB lacks `curl`) — but **both include `/etc/mysql/conf.d`, and MariaDB includes it last**, so one generated file dropped there configures either engine and still loses to a user override. *Resolution:* one shared set of init scripts and one shared s6 tree in `images/_shared/mysql-family/`, selected by a `MDB_ENGINE` baked at build time; only the two Dockerfiles differ. This required moving every image's build context from `images/<engine>/` to `images/`, which is why the PG Dockerfile's COPY paths changed too. Recorded because the original entry would have led a later phase to duplicate the whole script set.

**D-30 — The engine descriptor, and why the refactor came before the engines.** Adding MongoDB and Cassandra to a codebase with three hardcoded engines would have meant editing compose, backup, restore, new-project, check-env and a dozen checks per engine — six places to forget one. Engines are now declared in `engines/<name>/engine.conf` and read by generic machinery; family hooks in `engines/_family/<family>.sh` carry the few operations that genuinely need code. Descriptors were written for the three EXISTING engines first, deliberately: if they had not fitted, the abstraction was wrong, and that is far cheaper to learn before two more are built on it. They fitted. See SPEC section 22 and `docs/architecture.md`.

**D-31 — Pooling is a capability, not a service every engine gets.** The request was "connection pooling for all database types". Researching it properly showed a uniform abstraction would be a lie: PostgreSQL *needs* pgBouncer (one OS process per connection, ~10 MB each), MySQL merely benefits from ProxySQL (thread-per-connection is far cheaper), and **MongoDB and Cassandra drivers pool natively** — an external proxy in front of either is an anti-pattern that breaks topology discovery and retryable writes for Mongo, and token-aware routing for Cassandra. The descriptor therefore declares `external` or `driver`, and where it says `driver` the answer is documentation (reuse one client per process), not infrastructure. Check 31 forces every engine to commit to one and explain it.

**D-32 — We publish only OSI-licensed artefacts; MongoDB is referenced, not derived.** My first recommendation was to publish an SSPL-derived MongoDB image with clear labelling, which is what most Docker toolkits do. The better rule: every artefact this project publishes is OSI-licensed, so a downstream user never has to reason about our supply chain. `MDB_ENGINE_PUBLISH=reference` means the upstream image is used directly and configured at runtime — MongoDB stays fully supported, and the licence obligation stays with MongoDB. It generalises: any engine that changes licence moves by flipping one field, with no artefact to withdraw. **FerretDB** was added as the OSI-licensed document alternative (Apache 2.0, MongoDB wire protocol, PostgreSQL storage), with its real gaps recorded in the descriptor rather than discovered later.

**D-33 — MongoDB defaults to 7.0, not the newest, because 8.x cannot start on modern kernels.** MongoDB 8.x refuses to start on Linux kernel 6.19+ ([SERVER-121912](https://jira.mongodb.org/browse/SERVER-121912)). Verified here on kernel 7.0.14: **8.3.7 crash-loops, 7.0.39 runs**. That kernel is not exotic — OrbStack and current distributions ship it — so defaulting to 8.x would have handed a crash loop to a large share of users on first boot. 8.0 and 8.3 remain on the menu.

**D-34 — Both new engines ship insecure, and correcting that is the main value.** MongoDB runs with **no authentication** unless root credentials are supplied. Cassandra ships `AllowAllAuthenticator` **and** `AllowAllAuthorizer` — not weak auth, none, and no authorization either. Worse, merely enabling `PasswordAuthenticator` leaves a superuser named `cassandra` whose password is also `cassandra`: auth that is on with a publicly known credential is barely better than auth that is off. The Cassandra image now flips both settings, refuses to start if it cannot verify them, and rotates that default credential to the generated secret. Check 32 asserts across **every** engine that unauthenticated connections are refused, so a new engine inherits the guarantee by existing.

**D-35 — The pooler stores no application password; it resolves them with `auth_query`.** The obvious pgBouncer setup gives it a userlist of every user and password, making the pooler a second place every credential lives. Instead it has one login role of its own and looks verifiers up through a `SECURITY DEFINER` function, `pgbouncer.get_auth`, which returns a single row and **excludes superusers** — so compromising the pooler yields one credential rather than all of them, and never the superuser's. Its `search_path` is pinned to `pg_catalog`, because a `SECURITY DEFINER` function resolving unqualified names through the caller's path is the CVE-2018-1058 shape. The function is created by a new `mdb-converge` stage that runs on **every** start rather than only at first init, so enabling the pooler on an existing volume does not require destroying the data. Check 33 asserts the behaviour: eight clients collapse onto a pool of two, the userlist contains no application password, and a wrong password is still refused.

**D-36 — Cassandra rate-limits password changes, so provisioning tests the credential instead of resetting it.** The natural idempotent pattern — `CREATE ROLE IF NOT EXISTS ... WITH PASSWORD`, then `ALTER ROLE ... WITH PASSWORD` — fails **every** time on Cassandra, not intermittently: passwords may only be changed once per 5000 ms per role, and the server reports the refusal as `code=1001 [Coordinator node overloaded]`, which names neither the role nor the real cause. The hook therefore only alters a password when it is genuinely wrong, tested by logging in with it, and retries around the limiter when it must. Two other Cassandra-specific traps were found the same way and are recorded in the hook: `cqlsh -e` splits its argument on `;`, so a statement string beginning with a newline produces an empty leading statement and exits non-zero **after** running everything else; and `_cass_set_password` avoids a trailing `[ ] && sleep` list, which returns 1 on its last iteration and trips `set -e` in any caller that has not already neutralised it.

**D-37 — HA is a rehearsal topology, and the profile is the only switch.** Every node runs on one host, so the host is a single point of failure that Patroni cannot fix; what the stack gives you is a genuine election to practise on before you need one. Two design points came out of building it. First, `PROFILES=ha` is the *only* switch: a separate `MDB_HA_ENABLE` flag existed alongside it and gated nothing but a validation, so `PROFILES=ha` with the flag left false ran HA with no quorum check at all. Second, **HA replaces the `pg` profile rather than joining it** — HAProxy owns the PostgreSQL port and routes it to the current leader, so running both binds 5432 twice; `check-env` now compares *effective* ports, defaults included, because a default that collides is still a collision.

**D-38 — Four failures in the Patroni image, each reported as something else.** Worth recording because all four are silent in the same way. (1) The etcd host list written into `patroni.yml` as a comma-separated scalar is a YAML parse error at the first comma: Patroni exits before contacting etcd and the container merely looks unhealthy. (2) Dropping `mdb-postgres` from the s6 bundle is not enough, because s6 starts anything a remaining service *depends* on and `mdb-exporter` depends on it — the upstream entrypoint then ran alongside Patroni against the same data directory and died on a missing `POSTGRES_PASSWORD`. (3) The data volume must be mounted one level *above* the data directory: Patroni renames that directory when bootstrapping a replica, and a mount point cannot be renamed ("Device or resource busy", while the leader looks fine). (4) `s6-setuidgid` changes the uid but not the environment, so `HOME` stayed `/root`; libpq could not read `/root/.postgresql/`, treated that as a failed TLS start, and **retried unencrypted**, which the `hostssl` replication rule correctly refused — the only visible error named the pg_hba rule.

**D-39 — A failover budget at or below Patroni's `ttl` is unreachable by construction.** The check budgeted 30s against Patroni's default `ttl` of 30s: the leader lock does not expire until the ttl elapses, so no election can *begin* inside the budget however healthy the cluster is. Retuned to `ttl 20 / loop_wait 5 / retry_timeout 5` (Patroni requires `ttl >= loop_wait + 2 × retry_timeout`) with a 60s budget; real failovers now complete in ~20s. Lower is not automatically better — a short ttl makes a brief stall look like a dead leader, and demoting a healthy primary costs more than a few seconds of failover. Relatedly, Patroni will not promote a replica lagging more than `maximum_lag_on_failover`, so **if every replica is behind there is no election at all** and every node logs "I am not the healthiest node". That is correct behaviour, and the check now waits for genuine catch-up before killing the leader rather than reporting it as a broken failover mechanism.

**D-40 — ProxySQL holds password *verifiers*, which is why it could be shipped at all.** I first declined to wire ProxySQL, on the grounds that it has no equivalent of pgBouncer's `auth_query` and would therefore have to store every application password — giving up on MySQL, where pooling is not even necessary, the property D-35 goes to some trouble to preserve. That reasoning was half right and the conclusion was wrong. ProxySQL cannot look a credential up on demand, but what it stores can be the **verifier**: the `caching_sha2_password` hash copied straight out of `mysql.user.authentication_string`, so the pooler knows no more than the server already does. Copied as HEX and reassembled with `UNHEX()` because the value is binary, which is also why configuration goes through the admin interface rather than a `.cnf` — `UNHEX()` is a SQL function and a config file cannot express it. Three supporting decisions: the pooler gets its **own narrow account** (`SELECT` on `mysql.user`, `REPLICATION CLIENT`, nothing else) created by a new MySQL-family convergence stage, because root is restricted to the local socket and that hardening is not worth relaxing; the admin interface on 6032 is **never published**, since it can read every verifier held; and check 35 asserts the stored value carries the `$A$` verifier prefix and does not contain the password, without which this integration would be a security regression over not having it.

**D-41 — Compose profiles are OR, so `pooler` is expanded before compose sees it.** A single shared `pooler` profile starts *every* pooler, including those whose engine was not selected, and compose then rejects the entire project with "service mariadb-pooler depends on undefined service mariadb". Compose has no way to express "pooler AND mysql". Each pooler therefore has its own profile (`pg-pooler`, `mysql-pooler`), and `scripts/profiles` expands `pooler` into the ones matching the selected engines — so the documented `make up PROFILES=pg,pooler` keeps working without compose needing AND semantics.

**D-42 — pgBackRest closes PITR and off-site in one dependency.** The alternative was a hand-rolled `archive_command` copying WAL somewhere, which gets you archiving and nothing else — no retention, no verification that the archive is replayable, no repository format that restore understands. pgBackRest gives WAL archiving, an S3-compatible repository, encryption at rest and `check` from one package. Off by DEFAULT because archiving on a laptop fills a disk with segments nobody will replay; **required** in prod, along with `repo1-type=s3`, because a posix repository lives on the machine it is protecting. The repository gets its own volume, not a directory inside PGDATA, and is not per-major: a backup taken before an upgrade is exactly the one wanted after a failed upgrade. Two failure modes were found building it and are recorded in the code — a convergence stage that exited early on an unrelated missing secret and so silently skipped PITR entirely, and `%%p` in a printf format reaching postgresql.conf as an escaped literal, which handed pgbackrest the two characters `%p` instead of a WAL path and hung the first backup forever.

**D-43 — The vulnerability scanner is a pinned binary, not the trivy action.** In March 2026 an attacker force-pushed 75 of `aquasecurity/trivy-action`'s 76 version tags to steal CI secrets. Referencing it by tag is exactly the supply-chain risk a scanner exists to reduce, so CI downloads the trivy binary at a pinned version and verifies it against the release checksums — the same reasoning that pins every base image by digest, applied to the thing doing the scanning. Scanning fails the build only on HIGH/CRITICAL **with a known fix**: unfixed findings are reported but tolerated, because a scanner that cannot be made green stops being read. Waivers must justify themselves — check 38 rejects a bare CVE ID, a stub reason, or a waiver with no revisit condition, and also fails if CI stops scanning or stops honouring the ignore file, which is the pairing that rots.

**D-44 — An off-site setting that silently does nothing is worse than none.** `MDB_S3_BUCKET` and its credential files were documented in `.env.example` and `push_s3` read none of them: it ran `rclone copy :s3:` with no configuration, failed, and the failure was swallowed by a log line while `backup-all` reported success. That is the worst shape a backup bug can take, because everything looks configured. rclone is now given credentials from the `_FILE` secrets, and a failed push **aborts the backup** rather than being logged. Check 39 asserts the wiring rather than needing an object store: every documented setting is read, credentials are exported, and no tolerated-failure form remains.

**D-45 — Point-in-time recovery targets a binary-log COORDINATE, not a timestamp.** The obvious interface is `--to '2026-08-09 12:00:00'`, and it is quietly broken: `mariadb-binlog` given several files and `--stop-datetime` **exits after the first file and silently skips the rest** ([MDEV-35528](https://jira.mariadb.org/browse/MDEV-35528)). A time-bounded replay therefore recovers almost nothing while reporting success — in testing it emitted 41 lines and no `CREATE DATABASE`, cutting events whose timestamps were demonstrably earlier than the target. Positions have no such bug, no second-granularity ambiguity, and no timezone interpretation, and `--stop-position` applies to the **last file named**, which is exactly the semantics recovery needs: every earlier file replays in full and the final one stops at a byte offset. So `make pitr-restore ENGINE=mariadb TO=binlog.000002:873`, with `make pitr-info` printing the current coordinate and dumps recording theirs. Check 40 proves it end to end.

**D-46 — MySQL claims PITR by extracting one file from a package it cannot install.** The official image is `mysql-community-server-minimal` on Oracle Linux 9 and ships no `mysqlbinlog`, so a binary log could be written and never replayed — an audit trail, not a recovery path. Installing `mysql-community-client` does not work: it also owns `/usr/bin/mysql`, `mysqladmin` and `mysqldump`, and rpm refuses the file conflicts with server-minimal. Six routes were tried before the one that works — the OL9 `mysql` 8.0 client (same conflict), `mysql-community-client` from the enabled repos (absent; the image points at MySQL's *docker* subtree, which carries only server-minimal), MySQL Shell (present, but 8.4.10, and `util.dumpBinlogs`/`loadBinlogs` arrived in Shell 9.2), third-party images carrying the binary (unpullable here), and the generic tarball (no arm64 *minimal* variant). The answer is to fetch the matching client RPM and extract **only** `/usr/bin/mysqlbinlog` from it: no package is installed, nothing conflicts, and nothing else from that package enters the image. Its version is read from the installed server with `rpm -q` rather than pinned, because `mysqlbinlog` must read what this exact server writes and a pinned version would drift silently the first time the base image bumped.

**D-47 — The coordinate statement is spelled three ways, so all three are tried.** MySQL 8.4 **removed** `SHOW MASTER STATUS` in favour of `SHOW BINARY LOG STATUS`; MariaDB added `SHOW BINLOG STATUS` and kept the old name as an alias. Branching on a version string would need updating at every release, so `scripts/pitr` and check 40 try the three in order and use the first that answers. This is the kind of divergence that makes a single-engine check worthless: MariaDB passed on `SHOW MASTER STATUS` while MySQL aborted on it, so check 40 now exercises **both** engines by default.

**D-48 — pgBouncer is built here, not pulled from a third party (supersedes D-10).** D-10 chose `edoburu/pgbouncer` because the vendor's own image is amd64-only and stuck at 1.15.0. That solved the architecture problem and left this toolkit's only community rebuild in the supply chain — an organisation that can be compromised independently of pgBouncer itself, and whose update cadence we do not control, which is the same argument that pins every other reference by digest. `images/pgbouncer/Dockerfile` now builds it from a Docker Official base plus the **PGDG** package, maintained by the PostgreSQL project: current version, both architectures, no third party. The image also carries its own `ENTRYPOINT`, which the replacement initially did not — `docker run` then fell through to the base image's shell and exited 0, a container that "started successfully" having done nothing, and precisely the failure check 33 exists to catch. `IMAGE-PROVENANCE.md` records every image and its publisher, and check 42 fails the build on any namespace that is neither a Docker Official Image nor the upstream project's own.

**D-49 — Publishing a host port is a decision, not a default.** A modest profile set published **ten** host ports, which is ten chances to collide with something already on the machine — and every collision surfaces as a Docker error at the very end of a boot, naming neither the setting nor the process. `MDB_PUBLISH` now takes `none` (the default: publish nothing, applications join the network and use service names, which is what `make new-project` has always printed), `direct` (the old behaviour) or `proxy` (one front door owns the ports). In the latter two, a port is `MDB_PORT_BASE + offset`, so a collision is fixed by moving one number rather than hunting per-engine variables. The offset is **explicit in each descriptor**, not derived from the engine's own port: MongoDB's 27017 plus a 54000 base is 81017, past the end of the port range, and derived offsets would also shift when an engine is added. Check 31 asserts they are unique.

**D-50 — Caddy gets the layer4 module so one front door can serve raw TCP.** Caddy proxies HTTP natively and databases do not speak it, so `proxy` mode needs [caddy-l4](https://github.com/mholt/caddy-l4), built with xcaddy from Caddy's own official builder image — the same shape as the pgBouncer build. Two constraints came out of it. caddy-l4 v0.1.2 requires Caddy **2.11.4** exactly, so both stages moved together; a 2.10.2 builder fails the module graph outright. And the generated routes must sit inside a `layer4 { }` global option — emitted bare, Caddy parses each as a global option and rejects the whole config with *"unrecognized global option: :54004"*, which names the port rather than the missing wrapper. caddy-l4 is pre-1.0 and its author says to expect breaking changes, which is precisely why the default is `none` and `direct` remains: the proxy is a convenience, and nothing here depends on it to function.

**D-51 — gosu, rclone and postgres_exporter are rebuilt from their release tags, not waived.** All three were the newest upstream releases, and all three carried HIGH findings that had been fixed elsewhere. gosu 1.19 dates from 2025-09 and was compiled against a Go standard library with fifteen CVEs fixed since. postgres_exporter 0.20.1 was built the same way and also vendors `golang.org/x/net` and `golang.org/x/crypto` below their fixes. rclone 1.75.1 vendors a gRPC pseudo-version older than the CVE-2026-84445 fix. The previous answer was 26 expiring waivers, due to lapse on 2026-11-09 and fail every build. Each image now has a `gotools` stage that clones the same tag, raises only the named vulnerable modules (`go get module@fixed`, then `go mod tidy`), and builds on the toolchain pinned in `images/bases.tsv` (row `toolchain go`), which `scripts/build` passes as `GO_IMAGE`. It runs on `$BUILDPLATFORM` and sets `GOARCH` from `TARGETARCH`, so arm64 is cross-compiled rather than emulated. gosu lands on the base image's own path, so the upstream `docker-entrypoint.sh` and our convergence scripts call it unchanged. Every image proves the result at build time: `gosu nobody true`, `rclone version`, `postgres_exporter --version`. Check 02 fails if a Dockerfile's `GO_IMAGE` default disagrees with the bases row, since that default is what a bare `docker build` uses. Sources: [tianon/gosu 1.19](https://github.com/tianon/gosu/releases/tag/1.19), [rclone v1.75.1 go.mod](https://github.com/rclone/rclone/blob/v1.75.1/go.mod), [postgres_exporter v0.20.1 go.mod](https://github.com/prometheus-community/postgres_exporter/blob/v0.20.1/go.mod), [Docker `--platform=$BUILDPLATFORM` cross-compilation](https://docs.docker.com/build/building/multi-platform/#cross-compilation).

**D-23 — Section 19 defaults applied unchanged**, per the kickoff prompt: no plpython3u (toggle available), Tailscale-first prod docs on generic Ubuntu LTS, S3 wiring present but disabled until credentials exist, `ghcr.io/simtabi` private until flipped, defaults PG 17 / MySQL 8.4 / MariaDB 11.4, no extensions beyond sections 5–6, public Laranail conventions only.

## 8. arm64 deliverability

**Nothing in the spec is undeliverable on arm64.** The named unknown anticipated gaps that do not exist:

| Anticipated risk | Actual arm64 status | Workaround needed |
|---|---|---|
| `pg_graphql` (Rust/pgrx) | official prebuilt `.deb`, pg14–18, arm64 | none |
| `pg_net` | source build, portable C | builder stage |
| `pgsodium` | source build, portable C + libsodium | builder stage |
| `pgjwt` | pure plpgsql, arch-independent | vendoring (D-06), not arch |
| `http` | PGDG arm64 1.7.2 | none |
| `pg_partman` | PGDG arm64 5.5.0 | none |
| Every pinned image in §3 | amd64 + arm64 confirmed | none |

The only genuine arm64 hazards found were in **tooling, not extensions**: the official pgBouncer image (amd64-only, D-10) and the backup image's floating tags (arm64-only, D-05). Both are resolved by pinning.

## 9. What happens on approval

Phase 1 builds the complete `make verify` harness first — every SPEC section 18 criterion plus the section 21 additions and the D-09/D-18/D-19 checks — red where features do not exist yet. Then phases 2–7 in order, one per session, each ending on a green `make verify` and a commit.

---

**Awaiting your approval.** The two entries I would most like a decision on before phase 4 are **D-04** (own backup sidecar vs. the 3-job ceiling) and **D-06** (vendor pgjwt vs. drop it). Everything else I consider settled unless you disagree.

**D-51 — Vulnerability waivers are package-scoped and expire.** The flat `.trivyignore` recorded a reason and a revisit condition per CVE, both as prose in a trailing comment. That is a promise, not a mechanism: nothing read "revisit on base repin", and nothing would ever have failed if a waiver written for a 2026 base image were still silencing findings in 2029. It was also **global** — one file applied to every image, so accepting a Java CVE for Cassandra would have accepted it in any image that grew a Java dependency later, silently and without anyone deciding to. The replacement is [`.trivyignore.yaml`](.trivyignore.yaml), where each entry carries `purls` (where the waiver applies), `statement` (why it cannot be fixed here) and `expired_at` (when the decision stops being valid). Expiry is enforced twice: trivy stops applying a waiver past its date, and check 38 fails the harness first, naming the entry — so the revisit happens whether or not anyone remembers. Check 38 also rejects an expiry more than 180 days out, because one far enough away is decoration.

The immediate cause was Cassandra, whose images fail the scan on 11 (5.0) and 7 (4.1) HIGH findings in `jackson-databind`, `netty-*`, `snakeyaml` and `logback` — JARs vendored inside the Apache distribution. Unlike the MySQL case, where the finding was in MySQL Shell and the fix was to delete a component nothing used, there is nothing here to remove and nothing to repin to: `images/bases.tsv` already pins 5.0.8 and 4.1.11, both published 2026-08-05 and the newest of either series, and the fixed versions exist in the libraries rather than in any Cassandra release. Swapping a JAR inside `lib/` is not a configuration Apache tests. So these are waived, scoped to their exact Maven coordinates, expiring 2026-11-09.

Two things fell out of building the check. `python3` parses the file but **PyYAML does not**, because it is absent from the system python on macOS and the check would have passed on CI and failed on a laptop; the hand parser is strict instead, failing on any construct it does not recognise rather than skipping it, so a malformed waiver cannot read as an absent one. And `scripts/scan` — which advertises itself as "the same scan CI runs" — was still bind-mounting the deleted flat file, a path docker would have created as an empty directory, dropping every waiver while blaming the CVEs. Check 38 now asserts that the local wrapper and CI read the same file.

**D-52 — microdnf is not dnf, and that is now enforced rather than remembered.** Hardening the MySQL images meant removing one package and updating the rest, and both attempts reached for a dnf option microdnf does not have: `remove --noautoremove` (to stop rpm collecting `openssl-libs` along with `mysql-shell`) and then `update --exclude='mysql*'` (to stop the update pulling a newer server out from under the digest pin). microdnf is a deliberately minimal client and rejects an unknown option outright — at build time, on a runner, minutes into a matrix job — so each cost a full CI round, and the second broke two engine versions that had just gone green. `--exclude` has been requested since 2020 ([microdnf#119](https://github.com/rpm-software-management/microdnf/issues/119)) and does not exist. What does exist is `--disablerepo`, and because the official image keeps MySQL in its own repos (`mysqlN.N-server-minimal`, `mysql-tools-community`), disabling `mysql*` leaves the update drawing only from the Oracle Linux repos — the same intent, expressible. Check 43 fails on any dnf-only flag passed to microdnf, so the next person reaching for `--best` or `--skip-broken` finds out in a second on their own machine rather than in twenty minutes on a runner. It reads only the code half of each line, because the comment in `images/mysql/Dockerfile` explaining why `--exclude` is absent must not be the thing that fails the build.

**D-53 — No container may depend on the host uid of a generated file.** Three services failed on CI and nowhere else, with three different symptoms and one cause: `make init` generates `secrets/` (0700, files 0600) and `certs/` on the host, owned by whoever ran it, and on Linux that ownership is exactly what a container process sees. Patroni's leader died on `server.key` ("must be owned by the database user or root") and the replicas waited forever for a primary, reported as a different unhealthy node each run. pgAdmin — the only container that reads a secret as a non-root user (uid 5050) with no root phase — crash-looped on its password file and Caddy answered 502. pgBouncer, whose image baked `USER postgres`, printed `cannot read /run/secrets/pgbouncer_password.txt` and exited 1. On macOS the Docker implementation flattens ownership, so all three worked on the machine they were written on; that is what made the class invisible.

Compose's `uid`/`gid`/`mode` fields on file-backed secrets are not the fix: outside swarm they are silently ignored ([docker/compose#9648](https://github.com/docker/compose/issues/9648); the PR to honour them, [#12625](https://github.com/docker/compose/pull/12625), was closed unmerged). A first pgAdmin fix set them, verified only that compose accepted the syntax — on macOS, where the failing condition cannot occur — and changed nothing.

The rule that follows: **our images read secrets as root and drop privileges at exec** (Patroni stages certs with `install -o postgres`; pgBouncer now starts root and drops via `setpriv` — the `USER postgres` directive was what forced the unprivileged read). For **third-party images that start unprivileged** (pgAdmin), the secret is mounted per-file — stepping around the 0700 directory — and generated world-readable, with the 0700 directory as the host-side guard; `render-config` heals the mode on existing checkouts because `init` refuses to run twice. And `scripts/build` now accepts derived image names explicitly: `scripts/build pgbouncer` used to match nothing, build zero images and exit 0, which cost one full round of testing against a stale image.

**D-54 — Renamed to dbpantry; the machine-facing `mdb` names stay.** The owner found "multidb-server-docker" a poor name and chose dbpantry on 2026-11-01: a pantry keeps a household's staples ready for every meal, as this keeps databases ready for every project. The repository moved from `simtabi/multidb-server-docker` to `simtabi/dbpantry` (GitHub redirects the old URL), and the product and image names followed (`dbpantry-<engine>`); nothing had been published, so no image alias is needed, but a local checkout must run `make build` again (the next `make up` regenerates the compose files). What did **not** change is everything existing state already depends on: the `MDB_` variables, the `mdb` Compose project and `mdb_net` network (and so every container and volume name), the engine descriptor contract, the PostgreSQL include marker `# multidb-server include` (a new marker would append the include block to every existing `postgresql.conf` a second time) and the Patroni `/multidb-server/` namespace (a new one orphans a running cluster). Renaming those buys nothing a user can see and costs a migration. `docs/SPEC.md`, `docs/KIT.md`, `docs/KICKOFF-PROMPT.txt`, released CHANGELOG entries and this decision log keep the old names as history. Check 44 fails on the old names anywhere else.

**D-55 — Cassandra's vulnerable vendored JARs are replaced at build time, not waived.** On 2026-11-01 the Trivy gate failed every Cassandra build on guava (CVE-2026-102554; 27.0-jre in 4.1, 32.0.1-jre in 5.0, fixed in 33.7.2) and snappy-java 1.1.10.4 (CVE-2026-108106, CVE-2026-93451, fixed in 1.1.10.9), and 22 waivers for jackson, netty, logback, lz4-java and snakeyaml were due to lapse on 2026-11-09, 2026-11-12 and 2026-12-31. Cassandra 4.1.12 and 5.0.9 are still the newest releases, so there was nothing to repin to, and the waivers rested on one claim: that replacing a JAR in `lib/` is an untested configuration. The owner chose to replace them and test the result rather than keep waiving. [`images/cassandra/jars.tsv`](images/cassandra/jars.tsv) lists, per series, the JAR removed, the Maven Central coordinate installed and its sha256, each checked against Central's published checksums when the row was written. `replace-jars.sh` downloads every JAR and checks its sha256 before it touches `lib/`, and fails if a JAR the manifest names is missing, so a base repin that moved a library stops the build instead of leaving two versions on the classpath. The series comes from the base image's `CASSANDRA_VERSION` and must equal `ENGINE_VERSION`; `cassandra -v` then starts the JVM on the new classpath, and check 45 validates the manifest. The versions: guava 33.7.2-jre with failureaccess 1.0.3 (which it requires), snappy-java 1.1.10.12, jackson 2.22.4 (annotations 2.22), lz4-java 1.12.1, and netty 4.1.139.Final with netty-tcnative 2.0.84.Final, the pair netty's own build declares. 5.0 swaps one for one; 4.1 replaces its `netty-all` 4.1.58 fat JAR with the netty modules Cassandra references, and also takes logback 1.2.13 (the last 1.2.x, matching its configuration) and snakeyaml 1.33. The evidence is a bytecode reference check over every class in `lib/`: each class, method and field reference into a replaced library is resolved against the new classpath and the JDK, and compared with the stock image. 4.1 gains no unresolved reference. 5.0 gains one: `OcspHttpHandler` in netty-handler-ssl-ocsp calls `FullHttpResponse.release()` from netty-codec-http, which neither the stock image nor this one ships (stock 5.0 already cannot resolve that class), and Cassandra does not use OCSP. A reference check cannot see native code, so each image also loads netty-tcnative (BoringSSL), epoll, snappy-java and lz4-java natively and round-trips data through them. That caught the one break the bytecode check missed: netty-tcnative after 2.0.36 ships its native code only in the platform-classified JARs, so 4.1 adds the `linux-x86_64` and `linux-aarch_64` ones. Both series were then booted with check 32's container setup and the Cassandra family hooks: authentication enforced, a project provisioned twice, 200 rows each in LZ4-, Snappy- and Deflate-compressed tables and a JSON table read back after a flush, a compaction and a restart, a healthy healthcheck, and no error or linkage failure in the log. Trivy then finds no fixable HIGH or CRITICAL finding left in the replaced libraries but one. That one is CVE-2022-1471 in snakeyaml, fixed only in 2.0, which removed the `Composer(Parser, Resolver)` and `CustomClassLoaderConstructor(Class, ClassLoader)` constructors that Cassandra 4.1's `YamlConfigurationLoader` calls; a 4.1 node with snakeyaml 2.x cannot read `cassandra.yaml`. Its waiver stays, scoped to `snakeyaml@1.33` and still expiring on 2026-11-09, when it has to be decided again: no swap can close it, only a Cassandra 4.1 release or dropping 4.1. The cost is that each Cassandra repin re-decides `jars.tsv`, and the image no longer runs the exact library set Apache tested, so an incompatibility neither check can see (reflection, behaviour) would surface at runtime; the boot checks are the guard. Sources: [guava releases](https://github.com/google/guava/releases), [snappy-java releases](https://github.com/xerial/snappy-java/releases), [netty 4.1.139.Final](https://netty.io/news/), [SnakeYAML](https://bitbucket.org/snakeyaml/snakeyaml), [Maven Central](https://repo1.maven.org/maven2/).

**D-57 — Each engine maintains its current and latest version, and any other runs only by explicit, unsupported choice.** The owner set the policy on 2026-11-01, after Cassandra 4.1's last waiver came due: across every engine, maintain only the newest supported line and the one before it, confirmed from each project's own end-of-life page, and keep older versions possible through a configurable override marked unsupported. Every version on a menu costs a digest pin, a build on two architectures, a Trivy gate, the acceptance checks, and a waiver whenever its upstream stops shipping fixes; the menu had grown to sixteen versions across six engines, several of them on their way out upstream. Where an engine runs a long-term track beside a short-lived one, the two lines are the two newest long-term releases, because a line supported only until the next quarterly release would leave the menu every quarter. The result, from 16 versions to 10: PostgreSQL 15 · 16 · 17 · 18 → **17** · 18 (five-year community support; 18 to 2030-11-14, 17 to 2029-11-08). MySQL 8.0 · 8.4 · 9.7 → **8.4** · 9.7, the two LTS lines (8.0 left Premier and Extended Support in April 2026; Innovation releases are supported only until the next). MariaDB 10.11 · 11.4 · 11.8 → **11.8** · 12.3, the two newest LTS lines (12.3 to 2029-06-12, 11.8 to 2028-06-04; 13.x is rolling), and the default moves from 11.4 to 11.8. MongoDB 7.0 · 8.0 · 8.3 → **8.0** · 9.0, the two newest Major Releases (9.0 to 2031-10-31, 8.0 to 2029-10-31; 8.3 is a Minor Release, patched only until the next), and the default moves from 7.0 to 8.0. Cassandra 4.1 · 5.0 → **5.0**. FerretDB stays at 2.

*The MongoDB default.* 7.0 was the default because 8.x refused to start on Linux kernel 6.19 and later (SERVER-121912). MongoDB 8.0.30 lifted that for kernel 7.0.14 and later (SERVER-125742); 8.0.34 and 9.0.4 were booted on OrbStack's 7.0.14 for this decision, and GitHub's ubuntu-24.04 runners are below 6.19. Kernels 6.19 to 7.0.13 still cannot run 8.0, so the release notes and `docs/installation.md` say how to check and how to run 7.0 instead.

*The override.* A version off the menu needs `MDB_<ENGINE>_BASE_IMAGE`: the upstream image to build it from, or for MongoDB to run, pinned by digest. `check-env` refuses an off-menu version without it, so a typo cannot select an unmaintained engine, and refuses a tag without a digest, so an unsupported version is still a reproducible one; with it, `check-env` warns on every start that the version is unsupported, with no scan guarantee. `scripts/build` and the generated compose file pass it as `BASE_IMAGE` (compose previously built every version from the Dockerfile's default base), `scripts/build` labels the image `io.simtabi.dbpantry.support=unsupported`, and the Cassandra build keeps upstream's JARs for a series `jars.tsv` has no rows for instead of failing. A `.env` that names a version leaving the menu now fails `check-env` loudly, and its per-major volume is untouched; `docs/UPGRADE.md` shows how to build the old image once through the override so `make upgrade` can read it. Moving checks 14 and 29 to 17 → 18 found that `make upgrade` mounted PostgreSQL 18 at `/var/lib/postgresql/data`, which 18 no longer uses, so it restored into an anonymous volume; it now mounts each side at its own path. Check 29 also runs under its own Compose project now, because it removes the volumes it uses and the default project's `mdb_pg17_data` is the operator's.

*Cassandra 4.1.* 4.1 is still maintained upstream, until 7.0 ships, so dropping it is an exception to the policy rather than a result of it. It vendors snakeyaml, and CVE-2022-1471 is fixed only in 2.0, which removed constructors 4.1's `YamlConfigurationLoader` calls (D-55). The last waiver, scoped to `snakeyaml@1.33`, was due to expire on 2026-11-09. Five ways out were weighed. *Keep waiving*: the finding is reachable only through `cassandra.yaml`, which the toolkit mounts and an attacker able to change already controls the node — `YamlConfigurationLoader` builds its parser with the full `Constructor`, and `verifyReplacements` reads the raw file through `new Yaml()` — so a VEX statement of `vulnerable_code_cannot_be_controlled_by_adversary` is arguable, but it is an argument that never expires, which is what the waiver format exists to prevent. *Backport the loader*: CASSANDRA-18875 moved 5.0 to snakeyaml 2.1; the change applies to 4.1 as a patch, but that means building Cassandra from source and shipping a 4.1 Apache never released. *A newer 2.x on 4.1*: CASSANDRA-20848 records that 4.1 cannot take snakeyaml 2.4 either. *Wait for upstream*: CASSANDRA-18614, the 4.1 ticket, is open with no fix version. *Drop 4.1*: chosen. 5.0 ships snakeyaml 2.1 and is unaffected, and a 4.1.12 → 5.0.9 migration was rehearsed on 2026-11-01 — `nodetool drain`, the 4.1 volume copied, 5.0.9 booted on the copy, `upgradesstables` clean, 200 of 200 rows, 5.0 in `storage_compatibility_mode: CASSANDRA_4`, and the 4.1 volume unchanged — which is the procedure in `docs/UPGRADE.md`. `.trivyignore.yaml` has no Cassandra entries.

*Roadmap — restoring Cassandra 4.1.* 4.1 returns to the menu when a 4.1 release ships snakeyaml 2.x (watch CASSANDRA-18614) and no other finding needs a waiver: a `cassandra 4.1` row in `images/bases.tsv`, `4.1` back in the descriptor's `MDB_ENGINE_VERSIONS`, its `jars.tsv` rows re-derived against that release (the 0.1.x rows are in git history, and replaced netty, logback and the tcnative classifiers), and D-55's bytecode and boot checks rerun. A source-built 4.1 is not on the roadmap.

*Keeping it current.* When a new line ships, the oldest maintained one leaves the menu in the next release and the CHANGELOG says so; `docs/versions.md` holds the table and its sources. CI's build matrix is derived from the descriptors and `images/bases.tsv`, so a version change is those two edits. Sources: [PostgreSQL versioning policy](https://www.postgresql.org/support/versioning/), [MySQL release model](https://dev.mysql.com/doc/refman/9.7/en/mysql-releases.html), [MySQL EOL notice](https://www.mysql.com/support/eol-notice.html), [MariaDB maintenance periods](https://mariadb.org/about/), [MongoDB lifecycle schedules](https://www.mongodb.com/legal/support-policy/lifecycles), [MongoDB 8.0 release notes](https://www.mongodb.com/docs/v8.0/release-notes/8.0/), [Cassandra downloads](https://cassandra.apache.org/_/download.html), [CASSANDRA-18614](https://issues.apache.org/jira/browse/CASSANDRA-18614), [CASSANDRA-18875](https://issues.apache.org/jira/browse/CASSANDRA-18875), [CASSANDRA-20848](https://issues.apache.org/jira/browse/CASSANDRA-20848), [OpenVEX specification](https://github.com/openvex/spec).
