# Reorganization plan

Status: executed on branch `refactor/reorganize` (2026-11-01). Decision record:
[D-59](../design.md#7-decision-log).

## Principle

The layout already follows one rule well: **a directory per input that feeds the
stack**. `engines/` declares, `images/` builds, `scripts/` operates, `scripts/verify/`
proves. Moves are made only where a directory breaks that rule or a reader cannot
find something. Anything that existing checkouts, `.env` files or volumes depend
on stays where it is.

## How the files relate (inputs → outputs)

| Input | Read by | Output |
|---|---|---|
| `.env` (from `.env.example`) + `secrets/` | `scripts/check-env`, every script | validated configuration |
| `engines/<engine>/engine.conf` + `compose-*.yml`, `engines/_family/*.sh` | `scripts/gen-compose`, `scripts/engine-lib.sh` | `compose.engines.yml`, `compose.proxy.yml`, `config/caddy/layer4.caddy` (generated) |
| `config/caddy/*.tmpl` | `scripts/render-config` → `scripts/env-render` | `config/caddy/Caddyfile`, `config/caddy/servers.json` (generated) |
| `config/haproxy/haproxy.cfg`, `config/proxysql/bootstrap.sh` | compose (bind mounts) | HA front door, ProxySQL pooler |
| `images/<name>/`, `images/_shared/`, `images/bases.tsv` | `scripts/build`, compose `build:` | `ghcr.io/simtabi/dbpantry-*` images |
| `docker-compose.yml` + `compose.*.yml` | `docker compose` via `COMPOSE_FILE` | running stack, named volumes |
| running stack | `scripts/backup`, `scripts/pitr`, `scripts/transfer` | `backups/` (gitignored) |
| `scripts/certs` | — | `certs/` (gitignored) |
| `overrides/<engine>/` | compose (bind mounts) | per-engine runtime config the user owns |
| everything above | `scripts/verify/NN-*.sh` | `make verify` verdict |

## Moves

### 1. Auxiliary-service configuration → `config/`

| From | To |
|---|---|
| `caddy/` | `config/caddy/` |
| `haproxy/haproxy.cfg` | `config/haproxy/haproxy.cfg` |
| `proxysql/bootstrap.sh` | `config/proxysql/bootstrap.sh` |

Why: these three top-level directories hold the same kind of thing: files
bind-mounted into containers whose image the toolkit does not otherwise configure
(Caddy's config, HAProxy's config, ProxySQL's startup script). They were three of
thirteen root entries. `scripts/render-config` now renders into the directory its
name points at. The generated `Caddyfile`, `servers.json` and `layer4.caddy` move
with their templates, and `.gitignore` follows.

Updated: `docker-compose.yml`, `compose.ha.yml`, `scripts/render-config`,
`scripts/gen-compose`, `engines/{mysql,mariadb}/engine.conf`
(`MDB_ENGINE_POOLER_BOOTSTRAP`), checks 01 and 18, `.gitignore`, docs.

### 2. Documentation grouped by how it is read

| Folder | Contents | Read when |
|---|---|---|
| `docs/guides/` | installation, getting-started, configuration, versions, architecture, adding-an-engine, pooling, licensing, release | learning or extending the toolkit |
| `docs/runbooks/` | OPERATIONS, RESTORE, UPGRADE, ONBOARDING, WINDOWS | doing one operational task, often under pressure |
| `docs/reference/` | SPEC, KIT, acceptance, image-provenance (was root `IMAGE-PROVENANCE.md`) | checking the contract, the evidence, or a fact |
| `docs/archive/` | KICKOFF-PROMPT.txt | history only |

Why: nineteen flat files mixed three audiences, and the README index already
grouped them that way (Guides / Reference / Recipes). The folders now match the
index. The runbooks keep their upper-case names because SPEC section 15 names them
and the repository refers to them by name. `IMAGE-PROVENANCE.md` was the only
document at the root that is neither a GitHub health file nor part of the build
contract, so it joins the reference set.

Updated: every relative link (rewritten mechanically, then proven by the new
check 46), every `docs/...` path in scripts, comments, error messages, workflows,
the issue template and `CLAUDE.md`, and checks 01, 03, 42 and 44.

## Kept where they are, on purpose

| Path | Why it stays |
|---|---|
| `docker-compose.yml`, `compose.*.yml` at the root | `COMPOSE_FILE` in every existing `.env` names them by root path, and `make init` refuses to overwrite an `.env`. Moving them would break every checkout on its next `make up`. |
| `overrides/<engine>/` | Users put their own files there. Moving it silently orphans their configuration. |
| `scripts/` (flat) | One script per Makefile target, and the scripts are a documented interface (`scripts/backup --engine ...`). Sub-folders would lengthen every path without a grouping a reader needs. |
| `scripts/verify/` | The harness is addressed as "check NN" everywhere; its location is part of that contract. |
| `engines/`, `images/`, `rls/` | Already one concern each. |
| `design.md`, `CLAUDE.md` at the root | The build contract names them there (KIT section 2). |
| `README.md`, `CHANGELOG.md`, `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `LICENSE` | GitHub looks for them at the root. |
| `backups/`, `certs/`, `secrets/` | Runtime output, gitignored, mounted by path. |

## Errors, bugs and inconsistencies to fix in the same pass

- check 28 leaves a `*_readonly_password.txt` secret behind on every run;
- check 21 leaves its round-trip dumps in `backups/` and its `roundtrip`
  databases in the engines;
- `design.md` uses D-51 for two different decisions, and seven decisions sit
  after the end of the document instead of in the decision log;
- workflow comments say actions are pinned to commits while they float on major
  tags, per the Simtabi action rule;
- `gen-compose` says bootstrap scripts are never mounted, directly above the line
  that mounts ProxySQL's;
- `CONTRIBUTING.md` names Renovate, which this repository does not use
  (Dependabot raises the bumps);
- the acceptance page still says the repository has no remote and CI has never run;
- `VERIFY_TAGS` accepts only a single tag.

## To add

- check 46: every relative link in tracked Markdown resolves;
- check 47: every bind-mount source and build context in the compose files exists;
- `make verify CHECKS=21,28` and multi-tag `VERIFY_TAGS=pg,backup`.

## Rollback

Each move is one commit with its reference updates, so reverting that commit
restores the old paths. Generated files at the old paths (`caddy/Caddyfile`,
`caddy/servers.json`, `caddy/layer4.caddy`) are gitignored and are regenerated at
the new location by the next `make up`. The old copies are safe to delete.

## Found while executing

- **FerretDB was declared but never built.** `engines/ferretdb/engine.conf`
  exists, `images/ferretdb/Dockerfile` does not, and `gen-compose` still emitted
  a build for it, so `make up PROFILES=ferretdb` failed inside compose. Check 47
  caught it on its first run. The fix makes the state honest (no service, a
  `check-env` refusal by name, docs say so); building the image itself is
  open work, not part of a reorganisation.
- **A local `.env` pinning a version that left the menu** (MariaDB 11.4, removed
  by D-57) makes every `make up` fail `check-env`, whatever the profiles. That is
  the intended behaviour; the fix is per machine, in `.env`, not in the repo.
- **Three backup bugs surfaced once check 22 stopped relying on check 21's
  leftovers**: host-side `backup-all` dumped only the first database per
  engine (`docker compose exec` ate the list on stdin); retention pruned any
  old file in `backups/`, not only dumps; and the MySQL row count used a
  cached estimate, failing good restores. All three are fixed, each with an
  assertion in check 22 that failed first.
- **The backup-profile checks run retention against the operator's real
  `backups/`.** Dumps older than `MDB_BACKUP_RETAIN_DAILY` days are pruned by
  design whenever the sidecar or `backup-all` runs, including under
  `make verify`. Pointing the harness at a scratch `MDB_BACKUP_DIR` is open
  work.
- **GFS retention is documented but only the daily tier exists.**
  `MDB_BACKUP_RETAIN_WEEKLY` and `MDB_BACKUP_RETAIN_MONTHLY` are read by
  nothing; operations.md now says so. Implementing the tiers is open work.
