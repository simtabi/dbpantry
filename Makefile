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
# The prefix our images are built and run under: MDB_IMAGE_PREFIX from the
# environment, else the one default in scripts/env-lib.sh (localhost). Set but
# empty counts as unset, as it does for the scripts.
MDB_IMAGE_PREFIX := $(or $(MDB_IMAGE_PREFIX),$(shell bash -c '. scripts/env-lib.sh && mdb_image_prefix' 2>/dev/null))
# Every container call goes through scripts/runtime, which picks Docker, Podman
# or nerdctl (CONTAINER_RUNTIME=auto|docker|podman|nerdctl; design.md D-60).
RUNTIME  := scripts/runtime
COMPOSE  := $(RUNTIME) compose

# `make init-prod ENV_FILE=path` targets a specific file; the verify harness
# uses it to render into a temporary directory rather than the repository.
ENV_FILE_ARG := $(if $(filter-out .env,$(ENV_FILE)),$(ENV_FILE),)

# The dbpantry binary (design.md D-68): every target below runs a command of
# it, and the command runs the script the target used to run (refactor plan
# phase 2, docs/reference/cli.md). The binary is built from src/ when a Go file
# changed, so the first target after a change builds it.
GO ?= go
GOLANGCI_LINT_VERSION ?= v2.15.0
# The host build lives in build/<os>/ (macos, linux, windows, freebsd: the
# org's build layout, one folder per OS and nothing else in build/).
HOST_OS := $(shell uname -s | tr '[:upper:]' '[:lower:]' | sed -e 's/^darwin$$/macos/' -e 's/^mingw.*\|^msys.*\|^cygwin.*/windows/')
DBPANTRY := build/$(HOST_OS)/dbpantry
GO_VERSION_STAMP := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_COMMIT_STAMP := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

DBPANTRY_SOURCES := $(shell find src -name '*.go' -o -name go.mod -o -name go.sum)

define build_dbpantry
	@mkdir -p "$(dir $(DBPANTRY))"
	@CGO_ENABLED=0 $(GO) -C src build -trimpath \
		-ldflags "-s -w -X github.com/simtabi/dbpantry/src/internal/cli.version=$(GO_VERSION_STAMP) -X github.com/simtabi/dbpantry/src/internal/cli.commit=$(GO_COMMIT_STAMP)" \
		-o ../$(DBPANTRY) ./cmd/dbpantry
	@printf 'built %s\n' "$(DBPANTRY)" >&2
endef

$(DBPANTRY): $(DBPANTRY_SOURCES)
	$(build_dbpantry)

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
runtime: $(DBPANTRY) ## Show the detected container runtime and Compose (CONTAINER_RUNTIME= to force)
	@$(DBPANTRY) runtime info

.PHONY: init
init: $(DBPANTRY) ## Create .env, generate secrets and certs, run check-env
	@$(DBPANTRY) init

.PHONY: init-prod
init-prod: $(DBPANTRY) ## Render .env.prod with prod-safe values
	@ENV_FILE="$(or $(ENV_FILE_ARG),$(ENV_FILE_PROD))" $(DBPANTRY) init --prod

.PHONY: prod-overlay
prod-overlay: $(DBPANTRY) ## Add the prod overlay to COMPOSE_FILE in ENV_FILE (idempotent; for existing prod installs)
	@$(DBPANTRY) config prod-overlay "$(ENV_FILE)"

.PHONY: check-env
check-env: $(DBPANTRY) ## Validate env: sentinels, required vars, port collisions
	@$(DBPANTRY) config validate

.PHONY: certs
certs: $(DBPANTRY) ## Create the toolkit CA and per-engine server certificates
	@$(DBPANTRY) certs init

.PHONY: build
build: $(DBPANTRY) ## Build every engine image for this architecture
	@$(DBPANTRY) images build

.PHONY: certs-renew
certs-renew: $(DBPANTRY) ## Rotate server certs with live reload (no downtime)
	@$(DBPANTRY) certs renew

.PHONY: rotate-secrets
rotate-secrets: $(DBPANTRY) ## Rotate every database password and secret file, applied live
	@$(DBPANTRY) secrets rotate $(if $(YES),--yes,)

# -----------------------------------------------------------------------------
# Lifecycle
# -----------------------------------------------------------------------------

.PHONY: render
render: $(DBPANTRY) ## Render every templated file (Caddyfile, pgAdmin servers.json)
	@$(DBPANTRY) config render

.PHONY: up
up: $(DBPANTRY) ## Boot the stack (never touches data)
	@# check-env --quiet, render-config, gen-compose, scripts/runtime up (which
	@# waits for health on every Compose implementation), exposure-notice.
	@$(DBPANTRY) up

.PHONY: down
down: $(DBPANTRY) ## Stop the stack (never touches data)
	@# No -v, ever. Removing volumes is `make destroy`, which demands a typed
	@# confirmation naming the volume (SPEC section 15).
	@$(DBPANTRY) down

.PHONY: status
status: $(DBPANTRY) ## Show service status
	@$(DBPANTRY) status

.PHONY: logs
logs: $(DBPANTRY) ## Tail service logs
	@$(DBPANTRY) logs

.PHONY: destroy
destroy: $(DBPANTRY) ## Delete data volumes, KEEPING recovery repos (typed confirmation)
	@$(DBPANTRY) destroy $(if $(VOLUME),--volume "$(VOLUME)",--all)

.PHONY: test-profile
test-profile: $(DBPANTRY) ## Boot the tmpfs speed profile and run its checks
	@# render-config, gen-compose, then scripts/runtime up with
	@# docker-compose.yml, compose.engines.yml and compose.test.yml named
	@# explicitly: any -f replaces COMPOSE_FILE, so the engines file must be one.
	@$(DBPANTRY) up --profile test

# -----------------------------------------------------------------------------
# Clients and provisioning
# -----------------------------------------------------------------------------

.PHONY: psql
psql: $(DBPANTRY) ## PostgreSQL shell (socket-first)
	@# Socket-first: faster than TCP and it removes a network surface entirely
	@# (SPEC section 10.1). The pod's PGHOST wins: CloudNativePG's socket is
	@# /controller/run.
	@$(DBPANTRY) shell pg -- sh -c 'exec psql -h "$${PGHOST:-/var/run/postgresql}" -U "$$1"' sh $(or $(USER_NAME),postgres)

.PHONY: mysql
mysql: $(DBPANTRY) ## MySQL shell (socket-first)
	@$(DBPANTRY) shell mysql -- mysql --protocol=socket -uroot \
		-p"$$(cat secrets/mysql_root_password.txt)"

.PHONY: mariadb
mariadb: $(DBPANTRY) ## MariaDB shell (socket-first)
	@$(DBPANTRY) shell mariadb -- mariadb --protocol=socket -uroot \
		-p"$$(cat secrets/mariadb_root_password.txt)"

.PHONY: mongo
mongo: $(DBPANTRY) ## MongoDB shell (mongosh, authenticated)
	@$(DBPANTRY) shell mongodb -- mongosh \
		"mongodb://$(or $(USER_NAME),root):$$(cat secrets/mongodb_root_password.txt)@127.0.0.1:27017/?authSource=admin"

.PHONY: cassandra
cassandra: $(DBPANTRY) ## Cassandra shell (cqlsh, authenticated)
	@$(DBPANTRY) shell cassandra -- cqlsh -u $(or $(USER_NAME),cassandra) \
		-p "$$(cat secrets/cassandra_root_password.txt)"

.PHONY: scan
scan: $(DBPANTRY) ## Scan built images for vulnerabilities (honours .trivyignore.yaml)
	@$(DBPANTRY) images scan $(ENGINE)

.PHONY: shell
shell: $(DBPANTRY) ## Open the cli image (tools with no server required)
	@# On the stack's network, with certs/ read-only and backups/ at /work/backups.
	@$(DBPANTRY) tools --image $(MDB_IMAGE_PREFIX)/dbpantry-cli:dev

.PHONY: new-project
new-project: $(DBPANTRY) ## Provision DB + roles + extensions, print the Laravel .env block
	@$(DBPANTRY) project new --name "$(NAME)" --engine "$(or $(ENGINE),pg)"

# -----------------------------------------------------------------------------
# Backup and restore
# -----------------------------------------------------------------------------

.PHONY: backup
backup: $(DBPANTRY) ## Dump one database (ENGINE= DB=)
	@$(DBPANTRY) backup run --engine "$(ENGINE)" $(if $(DB),--db "$(DB)",)

.PHONY: backup-all
backup-all: $(DBPANTRY) ## Dump every database on every enabled engine, plus PG globals
	@$(DBPANTRY) backup run --all

.PHONY: restore
restore: $(DBPANTRY) ## Guided, confirmed restore (ENGINE= DB= FILE=)
	@$(DBPANTRY) restore --engine "$(ENGINE)" --db "$(DB)" --file "$(FILE)"

.PHONY: pitr-info
pitr-info: $(DBPANTRY) ## Show the recoverable window (ENGINE=pg|mysql|mariadb)
	@$(DBPANTRY) pitr info --engine "$(or $(ENGINE),pg)"

.PHONY: pitr-backup
pitr-backup: $(DBPANTRY) ## Take a PITR backup (ENGINE= TYPE=full|diff|incr)
	@$(DBPANTRY) pitr backup --engine "$(or $(ENGINE),pg)" $(if $(TYPE),--type "$(TYPE)",)

.PHONY: pitr-restore
pitr-restore: $(DBPANTRY) ## Recover to a point in time (ENGINE= TO=<file>:<pos>, or a time for pg)
	@$(DBPANTRY) pitr restore --engine "$(or $(ENGINE),pg)" --to "$(TO)"

.PHONY: verify-backups
verify-backups: $(DBPANTRY) ## Restore the latest set into throwaway containers, assert row counts
	@$(DBPANTRY) backup verify

.PHONY: import
import: $(DBPANTRY) ## Move data in (ENGINE= DB= FROM= or FROM_HOST=)
	@$(DBPANTRY) data import --engine "$(ENGINE)" --db "$(DB)" \
		$(if $(FROM),--from "$(FROM)",) $(if $(FROM_HOST),--from-host "$(FROM_HOST)",) \
		$(if $(FROM_USER),--from-user "$(FROM_USER)",)

.PHONY: export
export: $(DBPANTRY) ## Move data out (ENGINE= DB= [TO=])
	@$(DBPANTRY) data export --engine "$(ENGINE)" --db "$(DB)" $(if $(TO),--to "$(TO)",)

.PHONY: upgrade
upgrade: $(DBPANTRY) ## Guided major-version migration (ENGINE= FROM= TO=)
	@$(DBPANTRY) upgrade --engine "$(ENGINE)" --from "$(FROM)" --to "$(TO)"

# -----------------------------------------------------------------------------
# Scale and HA (SPEC section 21)
# -----------------------------------------------------------------------------

.PHONY: ha-status
ha-status: $(DBPANTRY) ## Show cluster topology and replication lag
	@$(DBPANTRY) ha status

.PHONY: ha-failover
ha-failover: $(DBPANTRY) ## Controlled switchover (typed confirmation required)
	@$(DBPANTRY) ha failover $(if $(NODE),--node "$(NODE)",)

.PHONY: ha-reinit
ha-reinit: $(DBPANTRY) ## Rebuild one replica from the current leader (NODE=)
	@$(DBPANTRY) ha reinit --node "$(NODE)"

# -----------------------------------------------------------------------------
# Verification — real from phase 1, because everything heals against it
# -----------------------------------------------------------------------------

.PHONY: verify
verify: $(DBPANTRY) ## Full acceptance harness (SPEC section 18 as scripts); CHECKS=21,28 or TAGS=pg,backup to narrow
	@$(DBPANTRY) verify --checks "$(CHECKS)" --tags "$(or $(TAGS),$(VERIFY_TAGS))"

.PHONY: verify-fast
verify-fast: $(DBPANTRY) ## Inner-loop subset of the harness
	@$(DBPANTRY) verify --fast

.PHONY: verify-structure
verify-structure: $(DBPANTRY) ## Harness self-test: every check is well-formed and runnable
	@$(DBPANTRY) verify --structure

.PHONY: lint
lint: $(DBPANTRY) ## shellcheck every script in the repository
	@$(DBPANTRY) dev lint

.PHONY: self-update
self-update: $(DBPANTRY) ## Update the toolkit itself, never project data
	@$(DBPANTRY) self-update

# -----------------------------------------------------------------------------
# The dbpantry binary (design.md D-68). The Go module lives in src/, so every
# target runs go with -C src: a bare `go test ./...` at the root finds no
# packages and passes. The binary is built from source only; nothing here
# releases it (docs/reference/manifest.md).
# -----------------------------------------------------------------------------

.PHONY: dbpantry
dbpantry: ## Build the dbpantry binary into build/<os>/dbpantry (always; other targets build it when src/ changed)
	$(build_dbpantry)

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
manifest: $(DBPANTRY) ## Print the engine manifest as JSON (OUTPUT=tsv|helm, MATRIX=versions|engines)
	@$(DBPANTRY) manifest $(if $(MATRIX),--matrix "$(MATRIX)",-o "$(or $(OUTPUT),json)")

.PHONY: endpoints
endpoints: $(DBPANTRY) ## Where to connect to each engine (OUTPUT=json, PROFILES=, PROJECT=)
	@$(DBPANTRY) endpoints -o "$(or $(OUTPUT),table)" $(if $(PROFILES),--profiles "$(PROFILES)",) $(if $(PROJECT),--project "$(PROJECT)",)

.PHONY: fixture-up
fixture-up: $(DBPANTRY) ## Boot disposable tmpfs engines for a test suite (PROFILES=pg,mysql)
	@$(DBPANTRY) fixture up $(if $(PROFILES),--profiles "$(PROFILES)",)

.PHONY: fixture-down
fixture-down: $(DBPANTRY) ## Stop the fixture engines (they hold no volume)
	@$(DBPANTRY) fixture down
