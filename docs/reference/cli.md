# The dbpantry command line

`dbpantry` is the toolkit's front door: 58 commands, and every `make` target is an alias for one of them.

Phase 2 of the Go port (the refactor plan's §10) puts the binary in front of the scripts without changing what they do. Most commands still run the script their make target ran, with the arguments it passed, and exit with that script's status. `docs/reference/parity.tsv` lists each make target, its command, what proves it and the phase that ports its logic to Go. Check 74 and `TestLedgerCoversTheTree` keep that file true.

## Building it

`make` builds `build/<os>/dbpantry` from `src/` the first time a target needs it after a Go file changes, so every target now needs the Go toolchain named in `src/go.mod`. `make dbpantry` always rebuilds. Run it directly from the checkout, or from anywhere with `--root` or `DBPANTRY_DIR`.

## Global flags

| Flag | Effect |
|---|---|
| `--root DIR` | The checkout (default: `$DBPANTRY_DIR`, else the nearest one above the current directory, else above the binary) |
| `--dry-run` | Print the scripts a command would run, quoted for a shell, and run nothing |
| `--no-input` | Never prompt: a command that needs a typed confirmation and has no `--confirm` exits 7 without running anything |
| `--no-color` | No colour (`NO_COLOR=1` for the scripts) |
| `--plain` | Print the scripts' own output even on a terminal, with no step tree, progress bars or tables |
| `--pretty` | Draw the step tree, progress bars and tables even when output is not a terminal (dashboard jobs, `less -R`). Off a terminal it streams one-line progress with percentage and ETA (elapsed buckets every 3s when % is not yet known), streams nested tree lines as Compose reports them, prints each finished step as it completes, then prints the final tree once |
| `-o FORMAT` | Per command. The formats are `table`, `plain`, `json`, `ndjson`, `csv`, `tsv` and `yaml`; a command refuses one it cannot write with exit 64. With `-o json` a failure is a JSON object on stderr: `{"error": {"code": "usage", "exit": 64, "message": "..."}}` |

Abbreviations are refused (`dbpantry vers` exits 64), and so is an unknown subcommand. A typed confirmation is answered with `--confirm=<name>`, never with `--yes`. `secrets rotate --yes` is the one exception: it is the `make rotate-secrets YES=1` that already existed. Values printed in a table have terminal control sequences removed.

## Output on a terminal

On a terminal, `up`, `down` and `project new` draw their work as a tree of steps with spinners and elapsed times, and then show tables. `up` and `down` read Compose's machine-readable progress (`COMPOSE_PROGRESS=json`). They group what Compose reports into Networks, Volumes, Images and Containers. An image being built shows a bar with its percentage and an estimate of the time left. The bar counts the build steps BuildKit has finished out of those it has reported so far, so the estimate firms up as the build goes. A download shows bytes. `up` ends with a **Connect** table for the engines in the selected profiles and a short list of next commands. `project new` shows what it generated or reused, the roles, each extension (with a warning when one could not be created), a table of the project, and then the paste-ready `.env` block. The block is flush left between rules, so it copies cleanly.

A step that fails prints what its script said in full, below the tree. Anywhere else (a pipe, a log, CI, the verify harness), and with `--plain`, these commands print exactly what their scripts print, so nothing that reads that output changes. `--pretty` overrides that for people reading a non-TTY stream (the dashboard job drawer uses it). `scripts/new-project --format json` is the machine-readable form `project new` draws from.

`start` runs those same views in turn: `up`, then `project new` once per project and engine, then the dashboard.

## Dashboard

`make dashboard` (or `dbpantry dashboard`) serves the web dashboard on `http://127.0.0.1:8484/`. It is part of the Go binary, built with [Tabler](https://tabler.io) 1.7.5's own CSS and JavaScript, both embedded, so it needs no Node and no second server. It shows the stack and runs the same `dbpantry` commands as jobs (design.md D-86). Header chrome: Live refresh, stack-stats dropdown, notifications, language (`EN`), Instance menu, then grouped nav (Overview · Stack · Data · Operate · Connect · Help). Open the printed URL on `localhost` / `127.0.0.1` only (other Hosts get 421). Auth: `token` (default one-time link), `password`, or `none` on loopback (`make dashboard AUTH=none`). Pages:

| Page | Shows |
|---|---|
| Overview | CRM-style KPIs, stack funnel, needs-attention, services, where to connect, projects, recent jobs |
| Status | Health %, services, engines, images/projects, jobs, Live badge legend |
| Services | Every container: state, health, time up, image; fixture containers when `fixture up` is running |
| Images | The locally built `dbpantry-*` images, their size and age |
| Engines, and one page per engine | Addresses, pooler, TLS, extensions, principals, projects and backups; ⋮ scoped actions |
| Projects, and one page per project | Active / orphaned / unmanaged; paste-ready `.env` (password blurred until Show); ⋮ actions |
| Databases & users | Shared-hosting wizard over projects (privileges matrix, rename-via-recreate) |
| Add-ons / System | Declarative poolers/extensions/UIs; host + runtime facts (`doctor`) |
| Backups | Dumps, PITR, last verify |
| Connect | Publish modes (`#publish`), engine addresses, network snippet, TLS, Run-from-dashboard jobs |
| Configuration / Operations / Jobs | Env keys, security/topology/health/tokens, job list + audit |
| Help / About | Localised help; version, MIT and engine licences |

Each engine is marked running, stopped (in the profiles, but its service is not up) or not enabled. A project's page also warns when its engine is not running. The dashboard refreshes every five seconds (`X-Partial: 1`); a project's page does not. Readings are stale-while-revalidate (design.md D-83). JSON without passwords: `/api/v1/status` (and legacy `/api/status`). Management API: `GET /api/v1/actions`, `POST /api/v1/jobs` — see [api.md](api.md) and `/help`. When the container runtime does not answer, services show as unavailable rather than an empty stack.

A project's page reads its block through `scripts/new-project --show`, which provisions nothing and generates nothing; it prints the block of a project that already exists, from its password files.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Failure |
| 6 | Cancelled (interrupted) |
| 7 | A typed confirmation is needed and `--no-input` forbids the prompt |
| 64 | Usage error: an unknown command, flag, argument or output format |
| other | A wrapped script's own status, passed through unchanged |

These follow the table dbpantry shares with mesobia (refactor plan §7). Codes 2 to 5, 10, 11 and 78 are reserved there for commands that do not exist yet. Usage errors were exit 2 in phase 1.

## Commands

| Command | Runs | Make target |
|---|---|---|
| `init [--prod]` | `scripts/init [--prod]` | `init`, `init-prod` |
| `version [-o plain\|json\|yaml] [--json]` | the binary | none |
| `completion` | cobra's shell completion | none |
| `self-update` | `scripts/self-update` | `self-update` |
| `config validate [--quiet]` | `scripts/check-env` | `check-env` |
| `config render` | `scripts/render-config`, `scripts/gen-compose` | `render` |
| `config show [--origin] [-o text\|json]` | the binary: every documented `MDB_` key's effective value; `--origin` adds `env`, `file` or `unset` | none |
| `config set KEY VALUE` | the binary: write one `MDB_` key into the instance's `.env`, quoted per check 50 | none |
| `config prod-overlay [ENV_FILE]` | `scripts/prod-overlay` | `prod-overlay` |
| `manifest [-o …] [--matrix …]`, `manifest check-helm` | the binary (phase 1) | `manifest` |
| `start [--profiles LIST] [--project NAME[:ENGINE]…] [--no-dashboard] [--listen ADDR]` | `up`, then `project new` for each project on each engine in the profiles (or the one named), then `dashboard` | `start` (`PROJECTS=`, `NO_DASHBOARD=1`, `LISTEN=`) |
| `up [--profiles LIST] [--profile test]` | `check-env --quiet`, `render-config`, `gen-compose`, `scripts/runtime up`, `exposure-notice`; with `--profile test`, the test overlay | `up`, `test-profile` |
| `down`, `status` | `scripts/runtime down`, `status` | `down`, `status` |
| `logs [SERVICE…] [--follow] [--tail N]` | `scripts/runtime logs -f --tail=100` | `logs` |
| `restart SERVICE…` | `scripts/runtime restart` (Compose: `compose restart`; other backends refuse it by name) | none |
| `endpoints [-o table\|json]` | the binary (phase 1); the table's PROFILE column is `enabled` or `disabled` ([statuses](../guides/statuses.md)) | `endpoints` |
| `dashboard [--listen ADDR] [--auth token\|password\|none] [--allow-destroy] [--tls-cert F --tls-key F]` | the binary: the web dashboard, `127.0.0.1:8484` by default; every action runs one of these commands as a job (`GET /api/v1/actions`, Help in the UI; full write-up in the checkout's `docs/reference/api.md`) | `dashboard` |
| `api token create NAME [--scope read\|operate\|admin] [--expires DUR]`, `api token ls`, `api token revoke NAME\|ID` | the binary: API tokens for the dashboard API, stored as SHA-256 in `api-tokens.json` in the instance home | none |
| `forward [SERVICE…]` | `scripts/runtime forward` | none |
| `destroy (--volume NAME \| --all) [--include-recovery] [--confirm …]` | `scripts/destroy` | `destroy` |
| `fixture up\|down` | the binary (phase 1) | `fixture-up`, `fixture-down` |
| `runtime info\|name\|backend` | `scripts/runtime` | `runtime` |
| `doctor [-o table\|json]` | the binary: host, binary and `runtime info` facts (dashboard `/system`, design.md D-93) | none |
| `addon ls [--engine E] [-o table\|json]` | the binary: declarative add-on catalog with effective state (design.md D-96) | none |
| `addon enable\|disable ID [--engine E] [--project N]` | the binary: merge profiles / env / init-extensions; disable never `DROP EXTENSION` | none |
| `env types` | the binary: catalog of environment kinds (localdev, dev, test, qa, uat, trn, spt, stg, preprod, demo, sandbox, prod + aliases) | none |
| `env ls` | the binary: registered instance homes (`~/.config/dbpantry/envs.json`) | none |
| `env init --name N --type T [--home PATH] [--project P]` | `init` into a new home, set `MDB_ENV_TYPE` / `COMPOSE_PROJECT_NAME`, register | none |
| `env use NAME` | print `export MDB_HOME=…` lines for `eval "$(…)"` | none |
| `env copy\|migrate\|move --from A --to B --engine E --db D [--confirm D]` | `scripts/transfer export` then `scripts/restore` between two homes (design.md D-100) | none |
| `env remove NAME [--purge]` | unregister (optionally delete the home) | none |
| `shell SERVICE -- COMMAND…` | `scripts/runtime exec` | `psql`, `mysql`, `mariadb`, `mongo`, `cassandra` |
| `tools [--image IMAGE]` | the cli image on the stack's network | `shell` |
| `project new --name N [--engine E] [--no-pooler-sync]` | `scripts/new-project`, then `pooler sync` | `new-project` |
| `project ls [-o table\|json]` | `scripts/project ls`, joined with the password files: each project active, orphaned (the engine has no such database) or unknown (the engine is not answering), with the extensions it has; databases no password file names are listed as unmanaged | none |
| `project rotate --name N [--engine E] [--confirm N] [--no-pooler-sync]` | `scripts/new-project --rotate`: new passwords for both roles, set on the engine before the password files are replaced; then `pooler sync` | none |
| `project drop --name N [--engine E] [--confirm N] [--no-pooler-sync]` | `scripts/project drop`: the database, both roles, their password files and client certificate; backups are kept. Only a project with a password file, never a reserved name, never while the engine is not answering; then `pooler sync` | none |
| `backup run (--engine E [--db D] \| --all)` | `scripts/backup` | `backup`, `backup-all` |
| `backup verify` | `scripts/verify-backups` | `verify-backups` |
| `restore --engine --db --file [--confirm DB]` | `scripts/restore` | `restore` |
| `pitr info\|backup\|restore` | `scripts/pitr` | `pitr-info`, `pitr-backup`, `pitr-restore` |
| `data import\|export` | `scripts/transfer` | `import`, `export` |
| `upgrade --engine --from --to [--confirm]` | `scripts/upgrade` | `upgrade` |
| `ha status\|failover\|reinit` | `scripts/ha` | `ha-status`, `ha-failover`, `ha-reinit` |
| `pooler sync ENGINE` | restarts the engine's pooler when it mirrors credentials at start | none |
| `certs init\|renew\|trust\|untrust` | `scripts/certs`, `scripts/certs-renew`, `scripts/certs --trust\|--untrust` | `certs`, `certs-renew`, `certs-trust`, `certs-untrust` |
| `secrets rotate [--yes] [--confirm ROTATE] [--engine pg\|mysql\|mariadb]` | `scripts/rotate-secrets [--engine E]`, then `pooler sync` (only E's pooler with `--engine`) | `rotate-secrets` |
| `images build [IMAGE…]`, `images scan [IMAGE]` | `scripts/build`, `scripts/scan` | `build`, `scan` |
| `verify [--checks …] [--tags …] [--fast] [--structure]` | `scripts/verify/run.sh` | `verify`, `verify-fast`, `verify-structure` |
| `dev lint`, `dev release-notes VERSION` | `scripts/lint.sh`, `scripts/release-notes` | `lint` |

`make help` still lists the make targets, and `make dbpantry` and the `go-*` targets build and test the binary itself.

### Shells

Each engine's client and the way it signs in still live in its make target, so `make psql` runs `dbpantry shell pg -- sh -c 'exec psql …'`. `dbpantry shell` with no command is a usage error. Phase 4 moves the clients into the engine descriptors, because the Go module may not name an engine (check 63).

### Pooler sync

ProxySQL cannot look a credential up when a client logs in. It copies the server's password verifiers when it starts, so a user that `project new` creates, or a password that `secrets rotate` changes, cannot log in through it until it copies them again. `pooler sync ENGINE` restarts the pooler when its descriptor names `MDB_ENGINE_POOLER_BOOTSTRAP` and the pooler is running. pgBouncer looks credentials up on every login, so it is never restarted. `project new` and `secrets rotate` sync afterwards (refactor plan §14, N8). This needs the Compose backend.

## Not in phase 2

The plan's §7 marks some commands **(new)**. The management plan (2026-11-22) brought forward what the dashboard needed: `config show --origin`, `config set`, `restart`, `project ls|rotate|drop`, `api token …`, plus `doctor` and `addon ls|enable|disable` (see the table above). Still not built: `uninstall`, `config diff|get|schema`, `engines`, `versions`, `exec`, `project show`, the rest of the new backup verbs, `target`, `ha switchover`, `pooler status`, `certs show`, `secrets ls`, `posture`, `images verify`, `health`, `monitor`, `dashboards`, `provider`, `host`, `server`, `deploy` and `reset`. Each one lands with a ledger row and a check in the phase that builds it.

---

[← Docs index](../../README.md#documentation)
