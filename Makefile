# dbpantry — the single entry point for humans and CI.
#
# Targets marked "phase N" are scaffolded but not implemented yet. They fail
# loudly rather than succeeding silently, so `make verify` stays honestly red
# until the feature behind them exists.

SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

COMPOSE_PROJECT_NAME ?= mdb
export COMPOSE_PROJECT_NAME

ENV_FILE ?= .env
ENV_FILE_PROD ?= .env.prod
COMPOSE  := docker compose

# `make init-prod ENV_FILE=path` targets a specific file; the verify harness
# uses it to render into a temporary directory rather than the repository.
ENV_FILE_ARG := $(if $(filter-out .env,$(ENV_FILE)),$(ENV_FILE),)

# Profile selection, in precedence order: PROFILES= on the command line for a
# single run, then MDB_PROFILES in .env permanently, then the documented
# default (SPEC section 7).
env_profiles := $(shell grep -E '^MDB_PROFILES=' $(ENV_FILE) 2>/dev/null | tail -1 | cut -d= -f2-)
COMPOSE_PROFILES := $(shell scripts/profiles "$(or $(PROFILES),$(env_profiles),pg,ui)")
export COMPOSE_PROFILES

# down, status, and logs must see every service regardless of the selected
# profiles, or a running container from another profile becomes invisible and
# un-stoppable through the Makefile.
all_profiles := --profile '*'

# Emit a consistent "not implemented" failure. $(1) is the owning phase.
define unimplemented
	@printf '\033[33m✗ %s\033[0m is scaffolded but not implemented yet (phase %s).\n' "$@" "$(1)" >&2; \
	printf '  See docs/reference/KIT.md section 5 for what that phase delivers.\n' >&2; \
	exit 1
endef

.PHONY: help
help: ## Show this help
	@printf 'dbpantry — make targets\n\n'
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@printf '\nProfiles: make up PROFILES=pg,mysql,ui\n'

# -----------------------------------------------------------------------------
# Setup
# -----------------------------------------------------------------------------

.PHONY: init
init: ## Create .env, generate secrets and certs, run check-env
	@scripts/init

.PHONY: init-prod
init-prod: ## Render .env.prod with prod-safe values
	@ENV_FILE="$(or $(ENV_FILE_ARG),$(ENV_FILE_PROD))" scripts/init --prod

.PHONY: check-env
check-env: ## Validate env: sentinels, required vars, port collisions
	@scripts/check-env

.PHONY: certs
certs: ## Create the toolkit CA and per-engine server certificates
	@scripts/certs

.PHONY: build
build: ## Build every engine image for this architecture
	@scripts/build

.PHONY: certs-renew
certs-renew: ## Rotate server certs with live reload (no downtime)
	@scripts/certs-renew

.PHONY: rotate-secrets
rotate-secrets: ## Rotate every database password and secret file, applied live
	@scripts/rotate-secrets $(if $(YES),--yes,)

# -----------------------------------------------------------------------------
# Lifecycle
# -----------------------------------------------------------------------------

.PHONY: render
render: ## Render every templated file (Caddyfile, pgAdmin servers.json)
	@scripts/render-config
	@scripts/gen-compose >/dev/null

.PHONY: up
up: ## Boot the stack (never touches data)
	@scripts/check-env --quiet
	@scripts/render-config
	@scripts/gen-compose >/dev/null
	@$(COMPOSE) up -d --wait
	@scripts/exposure-notice

.PHONY: down
down: ## Stop the stack (never touches data)
	@# No -v, ever. Removing volumes is `make destroy`, which demands a typed
	@# confirmation naming the volume (SPEC section 15).
	@$(COMPOSE) $(all_profiles) down --remove-orphans

.PHONY: status
status: ## Show service status
	@$(COMPOSE) $(all_profiles) ps

.PHONY: logs
logs: ## Tail service logs
	@$(COMPOSE) $(all_profiles) logs -f --tail=100

.PHONY: destroy
destroy: ## Delete data volumes, KEEPING recovery repos (typed confirmation)
	@scripts/destroy $(if $(VOLUME),--volume "$(VOLUME)",--all)

.PHONY: test-profile
test-profile: ## Boot the tmpfs speed profile and run its checks
	@printf 'TEST PROFILE: data lives on tmpfs and is DISCARDED on stop.\n'
	@scripts/render-config
	@scripts/gen-compose >/dev/null
	@# compose.engines.yml is listed explicitly. Passing any -f REPLACES the
	@# COMPOSE_FILE that normally supplies it, so omitting it leaves every
	@# engine service undefined and the overlay's `pg:` has neither an image
	@# nor a build context.
	@$(COMPOSE) -f docker-compose.yml -f compose.engines.yml -f compose.test.yml up -d --wait

# -----------------------------------------------------------------------------
# Clients and provisioning
# -----------------------------------------------------------------------------

.PHONY: psql
psql: ## PostgreSQL shell (socket-first)
	@# Socket-first: faster than TCP and it removes a network surface entirely
	@# (SPEC section 10.1).
	@$(COMPOSE) exec pg psql -h /var/run/postgresql -U $(or $(USER_NAME),postgres)

.PHONY: mysql
mysql: ## MySQL shell (socket-first)
	@$(COMPOSE) exec mysql mysql --protocol=socket -uroot \
		-p"$$(cat secrets/mysql_root_password.txt)"

.PHONY: mariadb
mariadb: ## MariaDB shell (socket-first)
	@$(COMPOSE) exec mariadb mariadb --protocol=socket -uroot \
		-p"$$(cat secrets/mariadb_root_password.txt)"

.PHONY: mongo
mongo: ## MongoDB shell (mongosh, authenticated)
	@$(COMPOSE) exec mongodb mongosh \
		"mongodb://$(or $(USER_NAME),root):$$(cat secrets/mongodb_root_password.txt)@127.0.0.1:27017/?authSource=admin"

.PHONY: cassandra
cassandra: ## Cassandra shell (cqlsh, authenticated)
	@$(COMPOSE) exec cassandra cqlsh -u $(or $(USER_NAME),cassandra) \
		-p "$$(cat secrets/cassandra_root_password.txt)"

.PHONY: scan
scan: ## Scan built images for vulnerabilities (honours .trivyignore.yaml)
	@scripts/scan $(ENGINE)

.PHONY: shell
shell: ## Open the cli image (tools with no server required)
	@docker run --rm -it \
		--network $(COMPOSE_PROJECT_NAME)_net \
		-v "$(CURDIR)/certs:/certs:ro" \
		-v "$(CURDIR)/backups:/work/backups" \
		ghcr.io/simtabi/dbpantry-cli:dev

.PHONY: new-project
new-project: ## Provision DB + roles + extensions, print the Laravel .env block
	@scripts/new-project --name "$(NAME)" --engine "$(or $(ENGINE),pg)"

# -----------------------------------------------------------------------------
# Backup and restore
# -----------------------------------------------------------------------------

.PHONY: backup
backup: ## Dump one database (ENGINE= DB=)
	@scripts/backup --engine "$(ENGINE)" $(if $(DB),--db "$(DB)",)

.PHONY: backup-all
backup-all: ## Dump every database on every enabled engine, plus PG globals
	@scripts/backup --all

.PHONY: restore
restore: ## Guided, confirmed restore (ENGINE= DB= FILE=)
	@scripts/restore --engine "$(ENGINE)" --db "$(DB)" --file "$(FILE)"

.PHONY: pitr-info
pitr-info: ## Show the recoverable window (ENGINE=pg|mysql|mariadb)
	@scripts/pitr info --engine "$(or $(ENGINE),pg)"

.PHONY: pitr-backup
pitr-backup: ## Take a PITR backup (ENGINE= TYPE=full|diff|incr)
	@scripts/pitr backup --engine "$(or $(ENGINE),pg)" $(if $(TYPE),--type "$(TYPE)",)

.PHONY: pitr-restore
pitr-restore: ## Recover to a point in time (ENGINE= TO=<file>:<pos>, or a time for pg)
	@scripts/pitr restore --engine "$(or $(ENGINE),pg)" --to "$(TO)"

.PHONY: verify-backups
verify-backups: ## Restore the latest set into throwaway containers, assert row counts
	@scripts/verify-backups

.PHONY: import
import: ## Move data in (ENGINE= DB= FROM= or FROM_HOST=)
	@scripts/transfer import --engine "$(ENGINE)" --db "$(DB)" \
		$(if $(FROM),--from "$(FROM)",) $(if $(FROM_HOST),--from-host "$(FROM_HOST)",) \
		$(if $(FROM_USER),--from-user "$(FROM_USER)",)

.PHONY: export
export: ## Move data out (ENGINE= DB= [TO=])
	@scripts/transfer export --engine "$(ENGINE)" --db "$(DB)" $(if $(TO),--to "$(TO)",)

.PHONY: upgrade
upgrade: ## Guided major-version migration (ENGINE= FROM= TO=)
	@scripts/upgrade --engine "$(ENGINE)" --from "$(FROM)" --to "$(TO)"

# -----------------------------------------------------------------------------
# Scale and HA (SPEC section 21)
# -----------------------------------------------------------------------------

.PHONY: ha-status
ha-status: ## Show cluster topology and replication lag
	@scripts/ha status

.PHONY: ha-failover
ha-failover: ## Controlled switchover (typed confirmation required)
	@scripts/ha failover $(if $(NODE),--node "$(NODE)",)

.PHONY: ha-reinit
ha-reinit: ## Rebuild one replica from the current leader (NODE=)
	@scripts/ha reinit --node "$(NODE)"

# -----------------------------------------------------------------------------
# Verification — real from phase 1, because everything heals against it
# -----------------------------------------------------------------------------

.PHONY: verify
verify: ## Full acceptance harness (SPEC section 18 as scripts)
	@scripts/verify/run.sh

.PHONY: verify-fast
verify-fast: ## Inner-loop subset of the harness
	@VERIFY_TAGS=fast scripts/verify/run.sh

.PHONY: verify-structure
verify-structure: ## Harness self-test: every check is well-formed and runnable
	@scripts/verify/run.sh --structure-only

.PHONY: lint
lint: ## shellcheck every script in the repository
	@scripts/lint.sh

.PHONY: self-update
self-update: ## Update the toolkit itself, never project data
	@scripts/self-update
