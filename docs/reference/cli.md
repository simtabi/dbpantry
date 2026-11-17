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
| `--pretty` | Draw the step tree, progress bars and tables even when output is not a terminal (for `less -R`) |
| `-o FORMAT` | Per command. The formats are `table`, `plain`, `json`, `ndjson`, `csv`, `tsv` and `yaml`; a command refuses one it cannot write with exit 64. With `-o json` a failure is a JSON object on stderr: `{"error": {"code": "usage", "exit": 64, "message": "..."}}` |

Abbreviations are refused (`dbpantry vers` exits 64), and so is an unknown subcommand. A typed confirmation is answered with `--confirm=<name>`, never with `--yes`. `secrets rotate --yes` is the one exception: it is the `make rotate-secrets YES=1` that already existed. Values printed in a table have terminal control sequences removed.

## Output on a terminal

On a terminal, `up`, `down` and `project new` draw their work as a tree of steps with spinners and elapsed times, and then show tables. `up` and `down` read Compose's machine-readable progress (`COMPOSE_PROGRESS=json`). They group what Compose reports into Networks, Volumes, Images and Containers. An image being built shows a bar with its percentage and an estimate of the time left. The bar counts the build steps BuildKit has finished out of those it has reported so far, so the estimate firms up as the build goes. A download shows bytes. `up` ends with a **Connect** table for the engines in the selected profiles and a short list of next commands. `project new` shows what it generated or reused, the roles, each extension (with a warning when one could not be created), a table of the project, and then the paste-ready `.env` block. The block is flush left between rules, so it copies cleanly.

A step that fails prints what its script said in full, below the tree. Anywhere else (a pipe, a log, CI, the verify harness), and with `--plain`, these commands print exactly what their scripts print, so nothing that reads that output changes. `scripts/new-project --format json` is the machine-readable form `project new` draws from.

`start` runs those same views in turn: `up`, then `project new` once per project and engine, then the dashboard.

## Dashboard

`make dashboard` (or `dbpantry dashboard`) serves a read-only web dashboard on `http://127.0.0.1:8484/`. It is part of the Go binary, built with [Tabler](https://tabler.io) 1.7.5's own CSS and JavaScript, both embedded, so it needs no Node and no second server. It uses Tabler's two-row header: the brand, a Connect button, the live indicator, the light/dark switch and a stack menu on top, then a menu with icons and dropdowns. Its pages:

| Page | Shows |
|---|---|
| Overview | Services healthy, engines enabled, projects and images, the publish mode with what it means, the running services, where to connect, the projects |
| Services | Every container: state, health, time up, image (state, uptime and image on the Compose backend; health on every backend) |
| Images | The locally built `dbpantry-*` images, their size and age |
| Engines, and one page per engine | Addresses from containers and from this machine, the pooler and whether it runs, TLS, boot budget, the extensions created per project, the principals and their password files, the engine's projects and backups |
| Projects, and one page per project | The paste-ready `.env` block, the password blurred until shown, with copy buttons. Also the compose snippet that joins the network, the roles, password files, TLS, pooler and extensions |
| Backups | The dumps in the backup directory, newest first |
| Connect | The three publish modes and which is set, every engine's address, the network snippet, TLS and the toolkit CA, shells |

The dashboard refreshes every five seconds by fetching the current page's content again; a project's page, which holds a password, does not. It serves the stack as JSON at `/api/status`, which carries no passwords. It answers only requests addressed to `localhost`, so another web page cannot read it through DNS rebinding. A project's page also refuses a cross-site request (`Sec-Fetch-Site`). `--listen` on another address works, with a warning, because the dashboard has no login. When the container runtime does not answer, the dashboard says services are unavailable rather than showing an empty stack.

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
| `config prod-overlay [ENV_FILE]` | `scripts/prod-overlay` | `prod-overlay` |
| `manifest [-o …] [--matrix …]`, `manifest check-helm` | the binary (phase 1) | `manifest` |
| `start [--profiles LIST] [--project NAME[:ENGINE]…] [--no-dashboard] [--listen ADDR]` | `up`, then `project new` for each project on each engine in the profiles (or the one named), then `dashboard` | `start` (`PROJECTS=`, `NO_DASHBOARD=1`, `LISTEN=`) |
| `up [--profiles LIST] [--profile test]` | `check-env --quiet`, `render-config`, `gen-compose`, `scripts/runtime up`, `exposure-notice`; with `--profile test`, the test overlay | `up`, `test-profile` |
| `down`, `status` | `scripts/runtime down`, `status` | `down`, `status` |
| `logs [SERVICE…] [--follow] [--tail N]` | `scripts/runtime logs -f --tail=100` | `logs` |
| `endpoints [-o table\|json]` | the binary (phase 1) | `endpoints` |
| `dashboard [--listen ADDR]` | the binary: a read-only web dashboard, `127.0.0.1:8484` by default | `dashboard` |
| `forward [SERVICE…]` | `scripts/runtime forward` | none |
| `destroy (--volume NAME \| --all) [--include-recovery] [--confirm …]` | `scripts/destroy` | `destroy` |
| `fixture up\|down` | the binary (phase 1) | `fixture-up`, `fixture-down` |
| `runtime info\|name\|backend` | `scripts/runtime` | `runtime` |
| `shell SERVICE -- COMMAND…` | `scripts/runtime exec` | `psql`, `mysql`, `mariadb`, `mongo`, `cassandra` |
| `tools [--image IMAGE]` | the cli image on the stack's network | `shell` |
| `project new --name N [--engine E] [--no-pooler-sync]` | `scripts/new-project`, then `pooler sync` | `new-project` |
| `backup run (--engine E [--db D] \| --all)` | `scripts/backup` | `backup`, `backup-all` |
| `backup verify` | `scripts/verify-backups` | `verify-backups` |
| `restore --engine --db --file [--confirm DB]` | `scripts/restore` | `restore` |
| `pitr info\|backup\|restore` | `scripts/pitr` | `pitr-info`, `pitr-backup`, `pitr-restore` |
| `data import\|export` | `scripts/transfer` | `import`, `export` |
| `upgrade --engine --from --to [--confirm]` | `scripts/upgrade` | `upgrade` |
| `ha status\|failover\|reinit` | `scripts/ha` | `ha-status`, `ha-failover`, `ha-reinit` |
| `pooler sync ENGINE` | restarts the engine's pooler when it mirrors credentials at start | none |
| `certs init\|renew` | `scripts/certs`, `scripts/certs-renew` | `certs`, `certs-renew` |
| `secrets rotate [--yes] [--confirm ROTATE]` | `scripts/rotate-secrets`, then `pooler sync` | `rotate-secrets` |
| `images build`, `images scan [ENGINE]` | `scripts/build`, `scripts/scan` | `build`, `scan` |
| `verify [--checks …] [--tags …] [--fast] [--structure]` | `scripts/verify/run.sh` | `verify`, `verify-fast`, `verify-structure` |
| `dev lint`, `dev release-notes VERSION` | `scripts/lint.sh`, `scripts/release-notes` | `lint` |

`make help` still lists the make targets, and `make dbpantry` and the `go-*` targets build and test the binary itself.

### Shells

Each engine's client and the way it signs in still live in its make target, so `make psql` runs `dbpantry shell pg -- sh -c 'exec psql …'`. `dbpantry shell` with no command is a usage error. Phase 4 moves the clients into the engine descriptors, because the Go module may not name an engine (check 63).

### Pooler sync

ProxySQL cannot look a credential up when a client logs in. It copies the server's password verifiers when it starts, so a user that `project new` creates, or a password that `secrets rotate` changes, cannot log in through it until it copies them again. `pooler sync ENGINE` restarts the pooler when its descriptor names `MDB_ENGINE_POOLER_BOOTSTRAP` and the pooler is running. pgBouncer looks credentials up on every login, so it is never restarted. `project new` and `secrets rotate` sync afterwards (refactor plan §14, N8). This needs the Compose backend.

## Not in phase 2

The plan's §7 marks some commands **(new)**, and none of them has a script behind it yet: `doctor`, `uninstall`, `config show|diff|get|set|schema`, `engines`, `versions`, `exec`, `restart`, `project ls|show|drop`, the new `backup` verbs, `target`, `ha switchover`, `pooler status`, `certs show`, `secrets ls`, `posture`, `images verify`, `health`, `monitor`, `dashboards`, `provider`, `host`, `server`, `deploy`, `api` and `reset`. Each one lands with a ledger row and a check in the phase that builds it.

---

[← Docs index](../../README.md#documentation)
