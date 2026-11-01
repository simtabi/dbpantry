# Complete DB Toolkit, design doc and build prompt

One document, two jobs. Sections 1-16 are the design spec. Section 17 is the build contract. Hand the whole file to the builder; the builder produces DESIGN.md, stops for approval, then implements. Answer section 19 before starting.

Architecture, in three layers, and one correction that matters: this is a toolkit STACK, not a single toolkit image, since one container runs one concern. Layer 1: per-engine build stages producing multidb-server-pg, multidb-server-mysql, and multidb-server-mariadb, each a derived image with comprehensive configuration, provisioning, healthchecks, and the full tool suite baked in, supervised by s6-overlay as PID 1. Layer 2: every engine image is standalone-complete, fully usable via docker run alone, including optional embedded helpers (metrics, backup cron) behind env toggles. Layer 3: a thin docker-compose orchestrator that fires up whichever engines the user selects. Baked-in means baked-in defaults only; env vars and optional mounts override everything, so images stay twelve-factor. What makes it "one tool" is one repo, one .env, one Makefile, one command. Project name: multidb-server (repo laranail/multidb-server; images multidb-server-pg, multidb-server-mysql, multidb-server-mariadb, multidb-server-cli). Env prefix: MDB_. KISS and DRY govern everything; nothing hardcoded.

## 1. Vision

A complete local database toolkit: PostgreSQL (full suite), MySQL, and MariaDB, each version-switchable, with browser UIs for all of them, installed once and shared by many projects. Dev-first: it exists to make local development easy and identical across macOS, Windows, and Linux (amd64 and arm64), free of host-OS installs. Prod-capable: the same stack deploys to a VPS hardened, TLS-only, and backed up. A new project connects in under a minute.

## 2. Prior art (what we take, what we skip)

| Project | Take | Skip |
|---|---|---|
| supabase/postgres | Single-Dockerfile PG_VERSION pattern; extension curation | The image itself (heavy, platform-coupled) |
| pgvector/pgvector | Base image for our PG build; PG_MAJOR tag convention | Nothing |
| danielptv/postgres-multiple-databases | database:username:password triplet provisioning format | Depending on their image; we vendor scripts |
| mrts multi-DB script | The initdb.d mechanism (works on mysql/mariadb images too) | Single-owner model |
| tiredofit/db-backup | Backup sidecar; it natively covers Postgres, MySQL, and MariaDB with multi-job DBnn_ config, S3, encryption | Its non-SQL engines |
| phpmyadmin (official) | MySQL-family UI; PMA_HOSTS serves both servers from one instance | Running it exposed in prod |
| adminer, dpage/pgadmin4, dbeaver/cloudbeaver | Adminer as the universal UI (speaks PG, MySQL, MariaDB); pgAdmin for deep PG work; CloudBeaver documented alternative | UIs in prod by default |
| official mysql, mariadb images | Base layers for our derived engine images; both honor /docker-entrypoint-initdb.d | Consuming them raw (config and tooling now bake into our images) |
| postgis/postgis | Pattern reference; PostGIS ships in our PG image now | Separate variant (it's default now) |
| Laravel Sail, DDEV | Proof pattern: dev stacks as one-service-per-container compose (Sail runs PHP, MySQL, Redis as separate services; DDEV separates web and database containers) | Their app-framework coupling |

## 3. Core decisions

| # | Decision | Why | Rejected |
|---|---|---|---|
| 1 | Stack of single-purpose containers, one repo/env/Makefile | One-process-per-container; engines and UIs compose cleanly | One mega-image (anti-pattern, unbuildable) |
| 2 | Per-engine, per-major data volumes: pg17_data, mysql84_data, mariadb114_data | Switching an image major on an old volume fails hard (Postgres refuses: "database files are incompatible with server"); versioned volumes make switching safe and let versions coexist | One shared data volume (guaranteed breakage) |
| 3 | Engines toggled by compose profiles: pg, mysql, mariadb (any combination) | Run only what a project needs; RAM stays sane | Always-on all engines |
| 4 | PG image full-fat by default; a -slim tag is the opt-out | The toolkit's promise is "everything ready"; dev boxes tolerate size | Lean default with add-on menu (previous design, inverted per requirements) |
| 5 | TLS: PG on always (scripted certs); MySQL/MariaDB optional in dev, enforced in prod profile | Dev friction vs alignment, rebalanced for three engines | TLS forced everywhere in dev |
| 6 | Secrets via _FILE convention; sentinel check blocks CHANGE_ME values | Plain env leaks; works with Docker secrets | Creds in files/images |
| 7 | No ports published by default; per-service env toggles with bind address (127.0.0.1 default) | Most DB breaches are "accidentally public" | Default 0.0.0.0 |
| 8 | Backup sidecar: tiredofit/db-backup jobs per engine; pgBackRest PITR included for PG prod | One sidecar covers all three engines; PITR now standard, not optional | Cron-in-container |
| 9 | Makefile is the single entry point; WSL2/git-bash documented for Windows | One interface for humans and CI | Raw docker commands |
| 10 | Version switching = change MDB_<ENGINE>_VERSION, volumes follow | Explicit, reversible, data preserved per major | latest tags (breakage by surprise) |
| 11 | Per-engine derived images with baked defaults; compose is a thin orchestrator only | Standalone reuse (docker run works alone), config versioned with the image, no mount fragility | Mounting vendored scripts into official images (earlier draft) |
| 12 | One combined multidb-server-cli image for all client tooling (psql, mysql/mariadb clients, dump/restore, toolkit scripts) | CLI tools are not daemons, so the one-process rule doesn't apply; tools usable with no engine container running | Needing a running engine container just to get a client shell |
| 13 | s6-overlay supervises every engine image; embedded helpers (exporter, backup cron) exist behind env toggles, OFF under compose | Proper PID 1, zombie reaping, ordered init; standalone docker run can self-backup and self-report without the stack; everything supervised must serve that one engine | A supervisor hosting multiple engines or UIs (that's the compose layer's job) |

### 3.1 One image or split, settled

Split. Servers are processes with lifecycles: one healthcheck, one restart policy, one exit code per container. Combining engines needs a supervisor, turns one engine's crash into everyone's restart or a silent failure, interleaves logs into an unparsable stream, and breaks per-engine memory limits. Tags explode too: split is 4 PG + 3 MySQL + 3 MariaDB images; combined is their cartesian product, and any engine's CVE forces rebuilding and re-pulling everything. The per-major volume scheme in decision 2 also only maps cleanly to per-engine containers. The ecosystem settled this long ago: Sail and DDEV both run databases as separate containers, and Docker's own guidance is one area of concern per container. The "one tool" feel lives in the compose layer, the Makefile, and the single .env, not in a shared filesystem. The lone place a combined image is right is the cli image (decision 12), because tools aren't services.

## 4. Engines and version switching

- Version menus (pinned, env-selected):
  - Postgres: 15, 16, 17 (default), 18
  - MySQL: 8.0, 8.4 LTS (default), 9.x innovation
  - MariaDB: 10.11 LTS, 11.4 LTS (default), latest 11.x
- MDB_PG_VERSION / MDB_MYSQL_VERSION / MDB_MARIADB_VERSION select image tag AND data volume name. Switching majors starts a fresh versioned volume; the old one stays intact for rollback or migration.
- PG 18+ image change: data mounts at /var/lib/postgresql (major-version-specific subdirs) rather than /var/lib/postgresql/data; the compose handles both paths by version so pg_upgrade --link works.
- make upgrade ENGINE=pg FROM=16 TO=17: guided dump/restore (default) or pg_upgrade path; equivalents for mysql (in-place minor, dump for major) and mariadb documented in UPGRADE.md.
- Running two majors of the same engine simultaneously (e.g. pg16 and pg17 during a migration window) is a documented compose-override recipe (second service with distinct port and volume), not a core toggle.
- SQLite note: needs no server; the toolkit only documents client tips. Redis/Mongo/etc. are explicitly out of scope v1, listed under Future.

## 5. The PostgreSQL image (full suite)

- FROM pgvector/pgvector:<ver>-pg${PG_MAJOR}, pinned digest; PGDG apt for the rest in one layer.
- Included and enabled-ready: vector, postgis (+ topology, raster), pg_cron, pgaudit, pg_stat_statements, pg_trgm, uuid-ossp, pgcrypto, citext, hstore, pg_repack, pg_partman, pgtap, http, pgjwt, pgsodium, pg_net, pg_graphql. Where no PGDG/apt package exists for an arch, build from source in a builder stage; if one is genuinely unavailable on arm64, document the gap in DESIGN.md rather than silently dropping it.
- plpython3u available behind MDB_PG_PLPYTHON=true (superuser-only, off by default; it is an untrusted language).
- shared_preload_libraries assembled from env; MDB_PG_INIT_EXTENSIONS per-database CREATE EXTENSION at provision time.
- Tags: multidb-server-pg:17 (full, the default) and :17-slim (official base + vector + contrib essentials only, no PostGIS or supabase-family extensions); matrix 15-18.

## 6. The MySQL and MariaDB images

- multidb-server-mysql and multidb-server-mariadb: FROM the official images, pinned by digest, with baked-in conf.d (utf8mb4 + utf8mb4_unicode_ci, explicit sql_mode, timezone from MDB_TZ), vendored provisioning scripts in /docker-entrypoint-initdb.d, healthchecks (mysqladmin/mariadb-admin ping), OCI labels, and client tooling.
- Baked config is defaults only; every setting remains env-overridable and conf mounts still win, so a plain docker run of either image yields a fully configured server and compose users can still tune freely.
- Same triplet provisioning contract as PG: MDB_MYSQL_DATABASES / MDB_MARIADB_DATABASES = "db:user:__FILE__,...".
- Tags: multidb-server-mysql:8.0, :8.4, :9; multidb-server-mariadb:10.11, :11.4, :11.
- MySQL 8.4+ removes mysql_native_password by default; caching_sha2_password is the standard, with a documented compat toggle for legacy clients.
- Both run concurrently: distinct host ports (defaults 3306 mysql, 3307 mariadb) when published.

### 6.1 Supervision, tooling, and embedded services (all engine images)

- s6-overlay v3 is PID 1 in every engine image: zombie reaping, clean shutdown ordering, and ordered init stages (fix permissions → assemble conf from env → certs → provisioning → engine start). The engine itself remains the main supervised service.
- Full tool suite baked into every engine image: that engine's clients, dump/restore utilities, compression (zstd), an S3 push tool (rclone), and the toolkit scripts. The separate multidb-server-cli image remains as tools-without-a-server; engine images carry the same tools so a bare docker run needs nothing else.
- Embedded helper services, env-toggled and OFF by default under compose (compose runs these as separate services instead): MDB_<ENGINE>_EMBED_EXPORTER runs the metrics exporter under s6 inside the engine container; MDB_<ENGINE>_EMBED_BACKUP runs scheduled dumps with retention and optional S3 push. These exist so a standalone docker run image can self-report and self-backup.
- Hard rule that keeps the supervisor honest: everything under one s6 tree serves that one engine. Never a second engine, never a UI. Cross-engine composition is the compose layer's job, full stop.
- Integration risk, named so the builder respects it: the upstream docker-entrypoint.sh scripts own first-run initdb and the POSTGRES_*/MYSQL_* env semantics. s6 wraps and invokes them as the engine service; it does not replace them. First-run behavior, _FILE handling, and /docker-entrypoint-initdb.d execution must stay behavior-compatible with the official images, and CI proves it by running the official images' env scenarios against ours.

## 7. The stack

| Service | Image | Profile | Default host port (env-overridable, publish opt-in) |
|---|---|---|---|
| pg | multidb-server-pg:<ver> | pg | 5432 |
| mysql | multidb-server-mysql:<ver> | mysql | 3306 |
| mariadb | multidb-server-mariadb:<ver> | mariadb | 3307 |
| adminer | adminer (pinned) | ui | 8080 (all engines) |
| phpmyadmin | phpmyadmin (pinned) | ui | 8081, PMA_HOSTS=mysql,mariadb |
| pgadmin | dpage/pgadmin4 (pinned) | ui | 8082 |
| backup | tiredofit/db-backup | backup | none |
| pgbackrest | pgBackRest | prod | none |
| metrics | postgres_exporter + mysqld_exporter | metrics | none published |
| cli | multidb-server-cli (all clients + scripts) | on demand | none; used via make psql / make mysql / make shell |
| pgbouncer | pgbouncer (pinned) | prod, optional | none; for connection-hungry apps |
| caddy | caddy (pinned) | ui, prod | MDB_CADDY_HTTP_PORT/MDB_CADDY_HTTPS_PORT (defaults 80/443); hostname-routes all UIs |

Profiles come in two kinds: engine selectors (pg, mysql, mariadb) and mode overlays (ui, backup, metrics, prod, test). The default is COMPOSE_PROFILES=pg,ui, which is what a bare make up boots; MDB_PROFILES in .env changes it permanently, PROFILES= on the command line changes it for one run.

Prod profile: UIs off, limits on, TLS enforced, log caps set. UIs in prod only behind reverse proxy auth or IP allowlist, documented, never default.

Test profile: speed over durability for CI and test suites. PGDATA on tmpfs with fsync=off, synchronous_commit=off, full_page_writes=off, and the MySQL-family equivalents; throwaway triplet databases; notes for Laravel parallel testing (one database per process). Data loss on stop is the point, and the docs say so in bold.

UIs come pre-wired: Adminer via ADMINER_DEFAULT_SERVER, pgAdmin with a baked servers.json listing the PG service (login via MDB_PGADMIN_EMAIL/PASSWORD), phpMyAdmin via PMA_HOSTS. Every UI opens ready to log in instead of asking for hostnames.

Caddy is the web front door. In dev it hostname-routes the UIs (adminer.db.localhost, pgadmin.db.localhost, pma.db.localhost; *.localhost resolves to 127.0.0.1 with no hosts-file edits) with Caddy's internal CA providing local HTTPS. In prod it is the only exposed web surface: automatic Let's Encrypt certificates and basic auth on every UI route, satisfying the "reverse proxy with auth" requirement concretely. The Caddyfile is a template rendered by scripts/env-render; direct per-UI ports remain available in dev when Caddy is off.

Port mapping contract: internal container ports are fixed and never change (5432, 3306, 80, and so on); everything host-side is .env-driven. Every published binding is written as ${MDB_<SERVICE>_HOST_PORT:-default}:internal, so resolving any port collision on any machine is a one-line .env change, never a compose edit.

Runtime knobs that are always forgotten and therefore specified here: MDB_PG_SHM (shm_size for the PG service, default 256m; parallel HNSW index builds need shm at least maintenance_work_mem); MDB_STOP_GRACE (stop_grace_period per engine, default 60s, because the docker default of 10s force-kills a database mid-checkpoint); nofile ulimits set for the MySQL family; COMPOSE_PROJECT_NAME pinned to mdb so container, network, and volume names are stable across machines; vm.overcommit and swappiness guidance for VPS hosts lives in OPERATIONS.md.

## 8. Multi-project provisioning

- Triplets per engine (section 6 and MDB_PG_DATABASES), passwords via _FILE.
- make new-project NAME=x ENGINE=pg|mysql|mariadb: creates DB, least-privilege owner role, readonly role, runs init extensions (PG), prints the paste-ready Laravel .env block.
- PG RLS kit: rls-template.sql (tenant policies via set_config('app.tenant_id')), roles doc, worked Laravel 13 example.

## 9. Security

- Auth: scram-sha-256 (PG), caching_sha2_password (MySQL), ed25519 or native (MariaDB); no trust/empty-password anywhere, including localhost.
- Apps never get superuser/root; root and postgres reserved for make-driven maintenance.
- Internal network, env-configurable subnet, static IPs per engine for stable client config and pg_hba.
- Containers: non-root where images support it, cap_drop ALL plus needed, no privileged, resource limits from env.
- make check-env refuses to start with sentinel passwords. trivy scanning in CI, pinned digests, Renovate config.
- Container hardening beyond caps: security_opt no-new-privileges, default seccomp profile kept, tmpfs for /tmp, read_only rootfs wherever the engine tolerates it, and make rotate-secrets to rotate every database password and secret file with the changes applied live.
- Supply chain: CI generates an SBOM (syft) per image and signs images with cosign; verification instructions in SECURITY.md.

### 9.1 Certificates (database TLS, distinct from Caddy's web TLS)

- make certs creates a toolkit-internal CA once, then per-engine server certificates with SANs covering the service names, localhost, and any MDB_EXTRA_SANS. Keys land with 0600 permissions owned by the engine user, which PG refuses to start without.
- Clients get one ca.crt and connect verify-full; the Laravel snippets ship with sslmode=verify-full (PG) and the equivalent ssl options (MySQL family) pointing at it.
- Prod: mount real CA-issued certs, or distribute the toolkit CA to clients; both paths documented. MySQL and MariaDB run require_secure_transport=ON in the prod profile so plaintext TCP is refused at the server; unix sockets count as secure transport, so socket connections keep working.
- make certs-renew rotates server certs without downtime: PG reloads via pg_reload_conf, MySQL via ALTER INSTANCE RELOAD TLS, MariaDB via FLUSH SSL.
- Optional mutual TLS behind MDB_MTLS=true: client certificates enforced via clientcert=verify-full in pg_hba and REQUIRE X509 grants on the MySQL side.
- Caddy's certificates are a separate concern (web UIs); engines never share the web certs.

## 10. Networking and external access

- Default publish: nothing. Apps join the mdb network by service name.
- Dev exposure per engine: MDB_PG_PUBLISH=5432 etc., bound to MDB_BIND_ADDR (default 127.0.0.1).
- Prod access recipes in OPERATIONS.md, in order: Tailscale/WireGuard sidecar (network_mode: service:ts, DB never on public internet), SSH tunnel, public port as last resort with ufw allowlist + TLS-only + fail2ban note.

### 10.1 Unix socket connections

- MDB_SOCKETS=true creates a shared sockets volume mounted into each engine (PG at /var/run/postgresql, MySQL family at /var/run/mysqld). Any container that mounts the volume connects locally with no TCP at all, which is both faster and removes a network surface.
- make psql / make mysql / make mariadb prefer the socket when present; the Laravel snippets include the unix_socket (MySQL) and host=/var/run/postgresql (PG) configuration variants.
- Auth discipline holds on sockets too: scram/password auth for app roles; peer auth stays reserved for make-driven maintenance as the engine user.
- Platform truth stated in the docs: container-to-container sockets work everywhere; host-to-container sockets work only on native Linux, because Docker Desktop on macOS and Windows runs a VM between your host and the containers. On those platforms the host uses TCP on 127.0.0.1, apps in containers still get sockets.

## 11. Backups and restore

- Dump correctness is specified, not assumed. PG: pg_dump custom format (-Fc, compressible, selective restore) per database, plus pg_dumpall --globals-only every run so roles and grants are never lost; a per-DB dump without globals restores into a server with no users. MySQL family: mysqldump/mariadb-dump with --single-transaction --routines --triggers --events --quick, because the defaults silently omit stored procedures and events and a "complete" backup without those flags isn't one.
- Sidecar jobs per enabled engine (DB01 pg, DB02 mysql, DB03 mariadb), nightly, zstd, checksums, retention env with GFS tiers (dailies kept N days, weeklies M weeks, monthlies K months), S3-compatible target env-ready (S3/R2/B2/Wasabi/MinIO). Backup encryption is ON by default in the prod profile, optional in dev. Failure notifications wired via sidecar env (email/webhook), so backups never fail silently.
- make backup-all dumps every database on every enabled engine plus PG globals in one command; make backup / make restore ENGINE=x DB=y FILE=z handle the single cases, restore guided and confirmed.
- One dump helper, two callers: the scheduled sidecar jobs and the make targets invoke the same script from scripts/, so flags can never drift between the nightly path and the manual path.
- Verification is automated, not aspirational: make verify-backups restores the latest backup set into throwaway containers and asserts row counts; it runs weekly via CI or cron, and RESTORE.md remains the 2am runbook. A backup that has never been restored is a hope, not a backup.
- Beyond logical dumps: PG PITR via pgBackRest in the prod profile (WAL to the same S3 target), and pg_basebackup documented as the physical path for large datasets where logical restore times stop being acceptable.

## 12. Logging and metrics

- All engines log to stdout; json-file rotation caps from env. Slow-query logging per engine via env (PG log_min_duration_statement, MySQL/MariaDB slow_query_log), sane defaults dev on / prod off.
- pgaudit available, one-env enable. Metrics profile ships postgres_exporter and mysqld_exporter on the internal network; Grafana/Prometheus are out of scope, endpoints documented.

## 13. Configuration reference

- One .env.example, every variable documented inline, grouped by engine, all MDB_-prefixed.
- Engine config = image defaults + mounted conf.d overrides; PG presets (dev-small, prod-medium) computed PGTune-style from MDB_MEM/MDB_CPUS; MySQL-family equivalents (innodb_buffer_pool_size etc.) from the same two vars.
- MDB_TZ, locale, and all ports/versions/toggles in one place. PG locale and encoding are init-time only: MDB_LOCALE applies at first initdb, and changing it later means dump and restore; the docs say this loudly next to the variable.
- Project initialization commands: make init copies .env.example to .env, generates strong random secrets into secrets/ files, runs make certs so the always-on PG TLS can actually boot, and finishes with check-env; make init-prod renders .env.prod from the same template with prod-safe values (TLS enforced, no published UI or engine ports, backups on, Caddy as the only web surface). Both are idempotent and refuse to overwrite an existing env file without FORCE=1.
- scripts/env-render is the single interpolation path for every templated file (Caddyfile, conf.d fragments, pg_hba, pgAdmin servers.json). envsubst-based with ${VAR:-default} support, it hard-fails with a list of any required-but-unset variables, and never writes secret values into rendered world-readable files; secrets stay _FILE references end to end.

## 14. Laravel integration

- Snippets for config/database.php covering pgsql and mysql connections side by side, sslmode/ssl options, and multi-connection apps.
- make new-project output paste-ready; Horizon/queue keepalive notes; pgBouncer guidance for connection-hungry setups (Octane, Horizon) including transaction-pooling caveats; RLS example app slice per Laranail conventions.

## 15. Repo layout and conventions

```
multidb-server/
  images/
    pg/       # Dockerfile, conf/, initdb.d/  (baked into multidb-server-pg)
    mysql/    # Dockerfile, conf/, initdb.d/  (baked into multidb-server-mysql)
    mariadb/  # Dockerfile, conf/, initdb.d/  (baked into multidb-server-mariadb)
    cli/      # Dockerfile: psql, mysql/mariadb clients, dump/restore tools, toolkit scripts
  docker-compose.yml        # thin orchestrator: selection + wiring only
  compose.prod.yml
  .env.example
  Makefile
  overrides/{pg,mysql,mariadb}/   # optional runtime conf mounts (win over baked defaults)
  caddy/            # Caddyfile.tmpl, rendered by scripts/env-render
  certs/            # gitignored; make certs writes the CA and server certs here
  secrets/          # gitignored; make init writes generated password files here
  scripts/          # init, env-render, backup, restore, new-project, check-env, upgrade
  rls/
  docs/             # OPERATIONS.md RESTORE.md UPGRADE.md WINDOWS.md ONBOARDING.md (migrate existing native/homebrew databases into the toolkit)
  .github/workflows/
  README.md CONTRIBUTING.md SECURITY.md CHANGELOG.md LICENSE (MIT)
```
- Data safety in the Makefile: make down never touches data; make destroy removes volumes only after typed confirmation naming the volume. make self-update upgrades the toolkit itself (git pull, image pull, CHANGELOG migration notes surfaced) without touching project data.

### 15.1 Makefile command reference (the complete public interface)

| Command | Does |
|---|---|
| make init / init-prod | Create .env / .env.prod from template, generate secrets and certs, run check-env |
| make check-env | Validate env: sentinels, required vars, port collisions |
| make up / down / status / logs | Stack lifecycle and visibility (down never touches data) |
| make destroy | Delete data volumes, typed confirmation required |
| make certs / certs-renew | Create toolkit CA + server certs / rotate certs with live reload |
| make rotate-secrets | Rotate all DB passwords and secret files, apply live |
| make psql / mysql / mariadb / shell | Client shells (socket-first); shell opens the cli image |
| make new-project NAME= ENGINE= | Provision DB + roles + extensions, print Laravel .env block |
| make backup / backup-all | One dump / every DB on every enabled engine + PG globals |
| make restore ENGINE= DB= FILE= | Guided, confirmed restore |
| make verify-backups | Restore latest set into throwaway containers, assert row counts |
| make import / export | Move data in/out (files, existing native DBs per ONBOARDING.md) |
| make upgrade ENGINE= FROM= TO= | Guided major-version migration |
| make test-profile | Boot the tmpfs speed profile and run its checks |
| make verify / verify-fast | Full acceptance harness / inner-loop subset |
| make self-update | Update the toolkit itself, never project data |
- Laranail hygiene: ten-line README quickstart, SECURITY.md (opensource@simtabi.com), semver CHANGELOG, shellcheck-clean bash, LF line endings enforced via .gitattributes (Windows safety), named volumes for data (never bind mounts; WSL2 performance and permissions), Docker Desktop / OrbStack / colima supported, podman best-effort noted.

## 16. CI and publishing

- buildx matrices per engine, all to ghcr.io/simtabi/: multidb-server-pg (15,16,17,18) x (amd64, arm64) x (full, slim); multidb-server-mysql (8.0, 8.4, 9) x (amd64, arm64); multidb-server-mariadb (10.11, 11.4, 11) x (amd64, arm64); multidb-server-cli (amd64, arm64). Upstream digests pinned, bump-tested by Renovate PRs.
- Smoke per image: boots healthy standalone via docker run (no compose); s6 init stages run in order and shutdown is clean; EMBED toggles verified on and off; every baked setting takes effect; every extension in section 5 creates (PG); TLS handshake (PG); non-TLS rejected in prod profile; triplet isolation verified; backup/restore round-trip; version-switch test (start 16, switch env to 17, both volumes intact).
- trivy gate; releases cut images + notes.

## 17. Build contract (the prompt)

You are a senior database and DevOps engineer building the Complete DB Toolkit for Simtabi's Laranail ecosystem. In order, without skipping the gate:
1. Read this document fully. Produce DESIGN.md: final service map, full env table (name, default, description, profile), decision log including anything you'd change from sections 2-16 with reasons, and any extension you cannot ship on arm64 with the workaround. STOP for approval.
2. Implement smallest-working-core first: PG image, compose with pg+ui profiles, provisioning, certs, versioned volumes. Then mysql/mariadb profiles and phpMyAdmin. Then backup, prod, metrics. Then scripts, docs, CI.
3. Everything configurable; no secret in any layer, file, or log; make check-env enforces.
4. Docs are runbooks: exact commands, verification steps, written for a tired reader at 2am.
5. Finish by executing section 18 and reporting results.

## 18. Acceptance criteria

- Fresh machine, any OS: clone, make init, make up → PG + Adminer running in two commands; make up PROFILES=pg,mysql,ui brings the rest, and Caddy serves every UI at its *.db.localhost hostname over HTTPS.
- make init-prod renders a prod env that passes check-env with TLS enforced, nothing published except Caddy, and any host port remappable from .env alone.
- Each engine image runs standalone: docker run ghcr.io/simtabi/multidb-server-<engine> with only a password env yields a fully configured server, no compose required; with EMBED toggles on, the same container self-backs-up on schedule and exposes metrics.
- Version switch: change MDB_PG_VERSION 16→17, make up; new volume in use, old intact; make upgrade migrates data; same demonstrated for MySQL 8.0→8.4.
- Both MySQL and MariaDB run concurrently with phpMyAdmin seeing both via one UI.
- Two projects per engine, isolated roles, cross-access denied; PG RLS demo passes.
- Fresh VPS path ends TLS-only, unpublished-or-firewalled, nightly S3 backups, PITR active for PG, no UI exposed.
- Restore drill passes with row-count verification on all three engines; make backup-all produces per-database dumps plus PG globals, and make verify-backups restores the latest set green.
- Sockets: make psql connects over the shared unix socket, and a sidecar container mounting the sockets volume connects with no TCP.
- TLS end to end: psql sslmode=verify-full against the toolkit CA succeeds; prod-profile MySQL-family connections without TLS are refused by the server.
- CI matrix green; trivy clean or waived with notes; repo/image-history grep finds no credential.

## 19. Open questions

1. Python: the plpython3u toggle described in section 5, a Python tooling sidecar, or both?
2. Typical concurrency: do your projects usually need one engine at a time or several at once (affects default profiles and RAM guidance)?
3. First prod target: VPS provider/OS, and is Tailscale/WireGuard acceptable, or must ports be publicly reachable?
4. S3-compatible backup target day one? Which, and credential style?
5. Registry: ghcr.io/simtabi public, private, or Docker Hub mirror?
6. PG default 17 or 18? MySQL default 8.4 LTS confirmed?
7. Any extension or engine feature current projects need beyond sections 5-6?
8. A written Laranail conventions doc beyond what's public?

Defaults if unanswered, so the build can proceed without blocking: no plpython3u (toggle stays available), Tailscale-first prod docs written against a generic Ubuntu LTS VPS, S3 backup wiring present but disabled until credentials exist, ghcr.io/simtabi private until flipped public, defaults PG 17 / MySQL 8.4 / MariaDB 11.4, no extensions beyond sections 5-6, and only the public Laranail conventions. Any answer later simply overrides the default.

## 20. References

Seed article: https://dev.to/rafi021/set-up-postgresql-and-adminer-using-docker-for-local-web-development-104m
postgres image: https://hub.docker.com/_/postgres  |  mysql: https://hub.docker.com/_/mysql  |  mariadb: https://hub.docker.com/_/mariadb
phpMyAdmin image (PMA_HOSTS, PMA_ARBITRARY, PMA_SSLS): https://hub.docker.com/_/phpmyadmin
Adminer: https://hub.docker.com/_/adminer  |  pgAdmin: https://www.pgadmin.org/docs/pgadmin4/latest/container_deployment.html  |  CloudBeaver: https://hub.docker.com/r/dbeaver/cloudbeaver
pgvector: https://github.com/pgvector/pgvector  |  images: https://hub.docker.com/r/pgvector/pgvector
supabase/postgres: https://github.com/supabase/postgres
Multi-DB provisioning: https://github.com/danielptv/postgres-multiple-databases  |  https://github.com/mrts/docker-postgresql-multiple-databases
Backups: https://github.com/tiredofit/docker-db-backup  |  https://pgbackrest.org
Major-version volume incompatibility and upgrade discussion: https://github.com/docker-library/postgres/issues/37  |  PG18 mount change: https://github.com/docker-library/postgres/issues/1377
Safe major upgrades walkthrough: https://tech-couch.com/post/upgrading-a-postgresql-database-with-docker
Postgres-in-Docker practices: https://sliplane.io/blog/best-practices-for-postgres-in-docker
Hardened SSL walkthrough: https://www.red-gate.com/simple-talk/?p=107543
Least-privilege containers: https://dohost.us/index.php/2026/07/08/securing-your-data-layer-least-privilege-best-practices-for-containerized-postgresql/
Tailscale sidecar pattern: https://cristian.livadaru.net/db-backups-tailscale/
RLS: https://www.postgresql.org/docs/current/ddl-rowsecurity.html  |  Tuning: https://pgtune.leopard.in.ua
Simtabi / Laranail: https://github.com/simtabi  |  https://packagist.org/packages/laranail/
One-service-per-container guidance: https://docs.docker.com/engine/containers/multi-service_container/
s6-overlay (in-image supervision): https://github.com/just-containers/s6-overlay
Caddy (auto-HTTPS reverse proxy): https://caddyserver.com/docs/
PG backup and SSL docs: https://www.postgresql.org/docs/current/backup.html  |  https://www.postgresql.org/docs/current/ssl-tcp.html
mysqldump complete-backup flags: https://oneuptime.com/blog/post/2026-03-31-mysql-backup-mysqldump/view
Socket sharing between containers: https://betterprogramming.pub/how-to-share-a-postgres-socket-between-docker-containers-ad126e430de7
Dev-stack prior art: https://laravel.com/docs/12.x/sail  |  https://ddev.readthedocs.io/

## 21. Scale, HA, and sync

Amendment authored during step 1 and approved before implementation: the kickoff prompt required a section 21 that the design doc did not contain. Written from the verified HA research baseline; DESIGN.md decision log entry D-01 records the gap. Requirements below are folded into services, env, commands, CI, and acceptance like any other section.

### 21.1 Vertical first, and the capacity truth table

Scale up before scaling out. A single well-tuned engine on adequate RAM serves the overwhelming majority of Laranail projects, and every HA mechanism below adds failure modes of its own. The numbers that decide when to stop tuning and start pooling:

| Tier | MDB_MEM | shared_buffers | max_connections (direct) | RAM spent on connections | pgBouncer default_pool_size | App-side connections supported |
|---|---|---|---|---|---|---|
| dev-small | 2 GB | 512 MB | 100 | ~1.0 GB | not used (direct) | 100 |
| prod-small | 4 GB | 1 GB | 100 | ~1.0 GB | 25 | ~1000 |
| prod-medium | 8 GB | 2 GB | 200 | ~2.0 GB | 50 | ~2500 |
| prod-large | 16 GB | 4 GB | 300 | ~3.0 GB | 100 | ~5000 |
| anti-pattern | any | any | 500 direct | ~5.0 GB before a single query runs | none | context-switch thrashing |

Roughly 10 MB per direct PostgreSQL connection is the planning figure: ~500 direct connections cost about 5 GB of RAM before a single query executes. That is the whole argument for pooling. **Connection pooling is mandatory in the prod profile**, not optional, and pgBouncer moves from "optional" in section 7 to required whenever MDB_PROFILES contains prod. Transaction-pooling caveats (no session state, no prepared statements outside the transaction, no LISTEN/NOTIFY) are documented in OPERATIONS.md next to the Laravel/Octane/Horizon guidance in section 14.

### 21.2 PostgreSQL high availability

PostgreSQL has no built-in automatic failover; something outside the engine must elect a leader. The stack, all under the `ha` profile:

- **Patroni** on each PG node, owning promotion and demotion, with **etcd** as the distributed configuration store. etcd runs as a quorum of **3 nodes minimum, never 2** — a two-node etcd has a *lower* effective availability than one node, because it cannot form a majority after a single loss.
- **HAProxy** routes by querying Patroni's REST health endpoints rather than guessing: `/primary` backs the **write port** and `/replica` backs the **read port**. Two ports, one cluster, no split-brain writes.
- **pgBouncer per node**, colocated with its engine, so pooling survives a failover instead of pointing at a dead leader.
- **pgBackRest** against a shared repository (the same S3 target as section 11) providing PITR and, critically, replica bootstrap without dumping the primary.
- **Client fallback**: libpq multi-host connection strings with `target_session_attrs=read-write` reach the current leader with no proxy at all. This is the documented path for teams that would rather not operate HAProxy.

**Named SPOF, with mitigations, because pretending otherwise is how HA stacks lie**: a single HAProxy container, or a single shared pgBouncer, is itself a single point of failure — the cluster survives a database loss and dies at the proxy. Mitigations, in ascending order of effort: client-side libpq multi-host (removes the proxy entirely); two HAProxy instances behind a keepalived VIP; or DNS/anycast fronting. OPERATIONS.md states plainly which one a given deployment chose. Single-host `docker compose` HA is a **rehearsal topology only** — it demonstrates election and failover but shares a kernel, a disk, and a power supply, and must never be presented as production HA.

### 21.3 Replication and sync

- **PostgreSQL**: streaming replication, asynchronous by default. `MDB_PG_SYNC_MODE=on` switches to synchronous commit for zero-data-loss at a latency cost; the tradeoff is stated at the variable.
- **MySQL / MariaDB**: asynchronous replication (GTID-based on MySQL, semi-sync available on MariaDB) is **documented, not automated**. There is no Patroni equivalent in scope for v1, so failover is a runbook, and REPLICATION.md says so rather than implying parity with the PG path.
- Read-replica routing is an application concern; the toolkit exposes the read port and documents the Laravel read/write connection split.

### 21.4 Folded requirements

- **Services**: `etcd`, `haproxy`, `patroni` (PG image variant), `pgbouncer` promoted to required under prod — all under the `ha` profile.
- **Env**: `MDB_HA_*` (enable, node name, cluster name, etcd hosts), `MDB_PGBOUNCER_*` (pool mode, sizes), `MDB_PG_SYNC_MODE`, `MDB_HAPROXY_*` ports.
- **Commands**: `make ha-status` (cluster topology and lag), `make ha-failover` (controlled switchover, typed confirmation), `make ha-reinit NODE=` (rebuild a replica from the pgBackRest repo).
- **CI**: an HA smoke job boots a 3-node cluster, kills the leader, and asserts a new leader is elected and the write port follows it within a bounded time.
- **Acceptance**: see the section 18 additions for HA.

### 21.5 Acceptance additions

- `make up PROFILES=pg,ha` boots a 3-node Patroni cluster with etcd quorum; `make ha-status` shows one leader and two streaming replicas.
- Killing the leader container elects a new leader, and the HAProxy write port routes to it, within MDB_HA_FAILOVER_BUDGET seconds.
- The read port serves only replicas; writes against it are refused.
- `make ha-reinit NODE=pg3` rebuilds that replica from the pgBackRest repo and it rejoins streaming.
- Capacity: a prod-profile boot without a reachable pooler fails `check-env` rather than starting unpooled.

## 22. The engine model, NoSQL support, and pooling policy

Amendment. Section 4 placed Mongo and friends under Future and out of scope for
v1; that is superseded here at the maintainer's direction. The change is not
"add two engines" — it is "stop hardcoding engines", so that adding the next one
is a descriptor and a Dockerfile rather than an edit to every script, compose
file, and check.

### 22.1 Engines are declared, not hardcoded

Every engine ships a **descriptor**: a single declarative file stating what the
generic machinery needs to know about it. Nothing else in the toolkit may
contain an `if engine == ...` branch that a descriptor could express.

A descriptor declares: family and paradigm; supported versions; data directory,
socket path and internal port; the client, admin/ping, dump and restore commands
with their required flags; the provisioning hook; health check; TLS
configuration keys; pooling capability; and the backup strategy.

The test of the abstraction is not that it works for a new engine. It is that
the three engines that already exist fit it without special cases. If they do
not, the abstraction is wrong, and that is much cheaper to discover before two
more engines are built on top of it.

### 22.2 Paradigms, and what stays common

| Paradigm | Engines | "Database" means | Provisioning |
|---|---|---|---|
| Relational | PostgreSQL, MySQL, MariaDB | database + owner role | SQL |
| Document | MongoDB | database + user with roles | JS via mongosh |
| Wide-column | Cassandra | keyspace + role | CQL |

What is genuinely common across all of them, and therefore stays in the generic
layer: image build and pinning, s6 supervision and ordered init, TLS material
and rotation, secret handling via `_FILE`, the multi-project triplet contract,
scheduled and manual backup with checksums and verified restore, health checks,
container hardening, and the acceptance harness.

What is genuinely per-engine, and therefore lives only in the descriptor and its
hooks: the wire protocol, the provisioning statements, the dump and restore
commands, and the pooling story.

### 22.3 Authentication is ON by default, on every engine

Both new engines ship insecure by default and this is the single most important
thing the toolkit corrects:

- **MongoDB** runs with **no authentication** unless root credentials are
  supplied. Any client that reaches the port is an administrator.
- **Cassandra** ships `AllowAllAuthenticator` **and** `AllowAllAuthorizer`.
  Not weak authentication — none, and no authorization either.

SPEC section 9 already forbids trust and empty-password auth anywhere. Every
engine image in this toolkit therefore enables authentication as a build-time
default, and `check-env` refuses to start one that has it disabled.

### 22.4 Pooling is a capability, not a service every engine gets

Connection pooling is not a universal concept, and pretending otherwise would
ship three-quarters of a lie:

| Engine | Pooling | Why |
|---|---|---|
| PostgreSQL | **pgBouncer, mandatory in prod** | One OS process per connection, ~10 MB each. ~500 connections cost ~5 GB before a query runs (section 21.1). |
| MySQL / MariaDB | **ProxySQL, optional** | Thread-per-connection is far cheaper than a process, so pooling is a scaling aid rather than a survival requirement. ProxySQL also provides routing and query rules. |
| MongoDB | **Driver-side only** | Drivers implement pooling natively (the CMAP specification). An external pooler in front of MongoDB is an anti-pattern: it breaks server discovery, topology monitoring and retryable writes. |
| Cassandra | **Driver-side only** | The driver holds connections per node under a load-balancing policy and multiplexes thousands of concurrent requests per connection. A proxy would defeat token-aware routing. |

So the descriptor declares `pooling=external:<image>` or `pooling=driver`, and
the docs explain the difference rather than shipping a proxy nobody should use.
The correct guidance for MongoDB and Cassandra is to reuse one client instance
per process, which is a documentation problem, not an infrastructure one.

### 22.5 Licensing decides what we publish, not just what we document

The repository is MIT. Published images inherit the licence of what they are
built FROM, which was already true for the SQL engines and becomes pointed once
a source-available engine is in scope.

The rule: **every artefact this project publishes is OSI-licensed.** A
downstream user should never have to reason about our supply chain to know what
they are running. So each engine declares a publish policy:

| Policy | Meaning | Applies to |
|---|---|---|
| `derive` | We build and publish `multidb-server-<engine>` from the upstream base | Every OSI-licensed engine |
| `reference` | We publish nothing; the upstream image is used directly and configured at runtime | Source-available engines |

**MongoDB Community is SSPL**, which the Open Source Initiative has explicitly
declined to recognise as open source. Publishing a derived image would mean
distributing SSPL binaries under an MIT project's namespace. So MongoDB is
`reference`: fully supported, configured by the toolkit, but pulled from
MongoDB rather than from us. The licence obligation stays where it belongs.

This generalises. Any engine that later changes licence — and several have —
moves to `reference` by flipping one descriptor field, with no code change and
no republished artefact to withdraw.

**FerretDB** exists in the engine list for the same reason: Apache 2.0, speaks
the MongoDB wire protocol so existing drivers connect unchanged, and stores
data in PostgreSQL through Microsoft's open source DocumentDB extension. It is
the document database with no licensing caveat at all.

It is not a complete substitute and the docs must not imply otherwise. As of
early 2026 the gaps against MongoDB include `$lookup` and `$facet`, full
transactions, change streams, GridFS, and `$text` indexes. It is a drop-in for
the majority of workloads, not all of them; applications needing those features
should use the `mongodb` engine and accept the licence.

`check-env` and check 31 enforce the rule: an engine whose licence is not
OSI-approved and whose policy is `derive` fails the harness.

### 22.6 Acceptance additions

- Adding an engine requires no edit to compose, backup, restore, provisioning,
  or any check; a descriptor and a Dockerfile are sufficient.
- Every engine, including new ones, is covered by the generic boot, auth, TLS,
  isolation, backup round-trip and hardening checks by virtue of being declared.
- No engine accepts unauthenticated connections, on any transport.
- `make verify` proves pooling works where the descriptor claims it, and the
  docs explain driver-side pooling where it claims that instead.
