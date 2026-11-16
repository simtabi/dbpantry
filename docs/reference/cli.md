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
| `-o FORMAT` | Per command. The formats are `table`, `plain`, `json`, `ndjson`, `csv`, `tsv` and `yaml`; a command refuses one it cannot write with exit 64. With `-o json` a failure is a JSON object on stderr: `{"error": {"code": "usage", "exit": 64, "message": "..."}}` |

Abbreviations are refused (`dbpantry vers` exits 64), and so is an unknown subcommand. A typed confirmation is answered with `--confirm=<name>`, never with `--yes`. `secrets rotate --yes` is the one exception: it is the `make rotate-secrets YES=1` that already existed. Values printed in a table have terminal control sequences removed.

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
| `up [--profiles LIST] [--profile test]` | `check-env --quiet`, `render-config`, `gen-compose`, `scripts/runtime up`, `exposure-notice`; with `--profile test`, the test overlay | `up`, `test-profile` |
| `down`, `status` | `scripts/runtime down`, `status` | `down`, `status` |
| `logs [SERVICE…] [--follow] [--tail N]` | `scripts/runtime logs -f --tail=100` | `logs` |
| `endpoints [-o table\|json]` | the binary (phase 1) | `endpoints` |
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
