# dbpantry — the single entry point for humans and CI.
#
# Targets marked "phase N" are scaffolded but not implemented yet. They fail
# loudly rather than succeeding silently, so `make verify` stays honestly red
# until the feature behind them exists.

SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

ENV_FILE ?= .env

# One value from the env file, unquoted the way Compose unquotes it
# (scripts/env-lib.sh): KEY="a b" reads as a b, not "a b".
env_get = $(shell bash -c '. scripts/env-lib.sh && mdb_env_get "$$1" "$$2"' _ $(1) $(ENV_FILE) 2>/dev/null)

# The project name comes from the command line or the environment, then from
# COMPOSE_PROJECT_NAME in the env file, then the default. It must be read from
# the file here: exporting a bare "mdb" default would beat .env (Compose ranks
# the environment above .env), so a second checkout with its own name would
# silently drive the first one's containers and volumes. Check 48 keeps it so.
env_project := $(call env_get,COMPOSE_PROJECT_NAME)
COMPOSE_PROJECT_NAME ?= $(or $(env_project),mdb)
export COMPOSE_PROJECT_NAME
ENV_FILE_PROD ?= .env.prod
# Every container call goes through scripts/runtime, which picks Docker, Podman
# or nerdctl (CONTAINER_RUNTIME=auto|docker|podman|nerdctl; design.md D-60).
RUNTIME  := scripts/runtime
COMPOSE  := $(RUNTIME) compose

# `make init-prod ENV_FILE=path` targets a specific file; the verify harness
# uses it to render into a temporary directory rather than the repository.
ENV_FILE_ARG := $(if $(filter-out .env,$(ENV_FILE)),$(ENV_FILE),)

# Profile selection, in precedence order: PROFILES= on the command line for a
# single run, then MDB_PROFILES in .env permanently, then the documented
# default (SPEC section 7).
env_profiles := $(call env_get,MDB_PROFILES)
COMPOSE_PROFILES := $(shell scripts/profiles "$(or $(PROFILES),$(env_profiles),pg,ui)")
export COMPOSE_PROFILES

# down, status, and logs must see every service regardless of the selected
# profiles, or a running container from another profile becomes invisible and
# un-stoppable through the Makefile.
all_profiles := --profile '*'

# Emit a consistent "not implemented" failure. $(1) is the owning phase.
define unimplemented
	@printf '\033[33m✗ %s\033[0m is scaffolded but not implemented yet (phase %s).\n' "$@" "$(1)" >&2; \
	printf '  See docs/reference/kit.md section 5 for what that phase delivers.\n' >&2; \
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

.PHONY: runtime
runtime: ## Show the detected container runtime and Compose (CONTAINER_RUNTIME= to force)
	@$(RUNTIME) info

.PHONY: init
init: ## Create .env, generate secrets and certs, run check-env
	@scripts/init

.PHONY: init-prod
init-prod: ## Render .env.prod with prod-safe values
	@ENV_FILE="$(or $(ENV_FILE_ARG),$(ENV_FILE_PROD))" scripts/init --prod

.PHONY: prod-overlay
prod-overlay: ## Add the prod overlay to COMPOSE_FILE in ENV_FILE (idempotent; for existing prod installs)
	@scripts/prod-overlay "$(ENV_FILE)"

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
	@# up -d --wait on Compose v2; podman-compose and nerdctl compose have no
	@# --wait, so scripts/runtime polls container health the same way.
	@$(RUNTIME) up
	@scripts/exposure-notice

.PHONY: down
down: ## Stop the stack (never touches data)
	@# No -v, ever. Removing volumes is `make destroy`, which demands a typed
	@# confirmation naming the volume (SPEC section 15).
	@$(RUNTIME) down

.PHONY: status
status: ## Show service status
	@$(RUNTIME) status

.PHONY: logs
logs: ## Tail service logs
	@$(RUNTIME) logs -f --tail=100

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
	@$(RUNTIME) up -f docker-compose.yml -f compose.engines.yml -f compose.test.yml

# -----------------------------------------------------------------------------
# Clients and provisioning
# -----------------------------------------------------------------------------

.PHONY: psql
psql: ## PostgreSQL shell (socket-first)
	@# Socket-first: faster than TCP and it removes a network surface entirely
	@# (SPEC section 10.1). The pod's PGHOST wins: CloudNativePG's socket is
	@# /controller/run.
	@$(RUNTIME) exec pg sh -c 'exec psql -h "$${PGHOST:-/var/run/postgresql}" -U "$$1"' sh $(or $(USER_NAME),postgres)

.PHONY: mysql
mysql: ## MySQL shell (socket-first)
	@$(RUNTIME) exec mysql mysql --protocol=socket -uroot \
		-p"$$(cat secrets/mysql_root_password.txt)"

.PHONY: mariadb
mariadb: ## MariaDB shell (socket-first)
	@$(RUNTIME) exec mariadb mariadb --protocol=socket -uroot \
		-p"$$(cat secrets/mariadb_root_password.txt)"

.PHONY: mongo
mongo: ## MongoDB shell (mongosh, authenticated)
	@$(RUNTIME) exec mongodb mongosh \
		"mongodb://$(or $(USER_NAME),root):$$(cat secrets/mongodb_root_password.txt)@127.0.0.1:27017/?authSource=admin"

.PHONY: cassandra
cassandra: ## Cassandra shell (cqlsh, authenticated)
	@$(RUNTIME) exec cassandra cqlsh -u $(or $(USER_NAME),cassandra) \
		-p "$$(cat secrets/cassandra_root_password.txt)"

.PHONY: scan
scan: ## Scan built images for vulnerabilities (honours .trivyignore.yaml)
	@scripts/scan $(ENGINE)

.PHONY: shell
shell: ## Open the cli image (tools with no server required)
	@$(RUNTIME) ctr run --rm -it \
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
verify: ## Full acceptance harness (SPEC section 18 as scripts); CHECKS=21,28 or TAGS=pg,backup to narrow
	@VERIFY_CHECKS="$(CHECKS)" VERIFY_TAGS="$(or $(TAGS),$(VERIFY_TAGS))" scripts/verify/run.sh

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

# -----------------------------------------------------------------------------
# The dbpantry binary (design.md D-68). The Go module lives in src/, so every
# target runs go with -C src: a bare `go test ./...` at the root finds no
# packages and passes. The binary is built from source only; nothing here
# releases it (docs/reference/manifest.md).
# -----------------------------------------------------------------------------

GO ?= go
GOLANGCI_LINT_VERSION ?= v2.15.0
DBPANTRY := build/dbpantry
GO_VERSION_STAMP := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_COMMIT_STAMP := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

.PHONY: dbpantry
dbpantry: ## Build the dbpantry binary into build/dbpantry
	@mkdir -p build
	@CGO_ENABLED=0 $(GO) -C src build -trimpath \
		-ldflags "-s -w -X github.com/simtabi/dbpantry/src/internal/cli.version=$(GO_VERSION_STAMP) -X github.com/simtabi/dbpantry/src/internal/cli.commit=$(GO_COMMIT_STAMP)" \
		-o ../$(DBPANTRY) ./cmd/dbpantry
	@printf 'built %s\n' "$(DBPANTRY)" >&2

.PHONY: go-ci
go-ci: go-fmt-check go-vet go-test ## What CI runs for the Go module: format check, vet, tests

.PHONY: go-fmt-check
go-fmt-check: ## Fail if any Go file is not gofmt-clean
	@out="$$(gofmt -l src)"; if [[ -n "$$out" ]]; then printf 'not gofmt-clean:\n%s\n' "$$out" >&2; exit 1; fi

.PHONY: go-vet
go-vet: ## go vet the module
	@$(GO) -C src vet ./...

.PHONY: go-test
go-test: ## Go tests with the race detector
	@$(GO) -C src test -race -count=1 ./...

.PHONY: go-lint
go-lint: ## golangci-lint, built with this Go so it understands the stdlib it reads
	@cd src && $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

.PHONY: manifest
manifest: dbpantry ## Print the engine manifest as JSON (OUTPUT=tsv|helm, MATRIX=versions|engines)
	@$(DBPANTRY) manifest $(if $(MATRIX),--matrix "$(MATRIX)",-o "$(or $(OUTPUT),json)")

.PHONY: endpoints
endpoints: dbpantry ## Where to connect to each engine (OUTPUT=json, PROFILES=, PROJECT=)
	@$(DBPANTRY) endpoints -o "$(or $(OUTPUT),table)" $(if $(PROFILES),--profiles "$(PROFILES)",) $(if $(PROJECT),--project "$(PROJECT)",)

.PHONY: fixture-up
fixture-up: dbpantry ## Boot disposable tmpfs engines for a test suite (PROFILES=pg,mysql)
	@$(DBPANTRY) fixture up $(if $(PROFILES),--profiles "$(PROFILES)",)

.PHONY: fixture-down
fixture-down: dbpantry ## Stop the fixture engines (they hold no volume)
	@$(DBPANTRY) fixture down
