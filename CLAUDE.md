# dbpantry build rules

You are building dbpantry per docs/reference/spec.md. That file is the contract. Read it before any work.

## The loop (applies to every task, no exceptions)
1. PLAN: restate the task and name its executable check before writing code.
2. RESEARCH: before implementing any component, search the web for the current docs of the exact pinned versions involved (s6-overlay v3, the official postgres/mysql/mariadb image entrypoints, pgvector, phpmyadmin envs, Caddy). Record what you relied on as links in design.md.
3. IMPLEMENT: smallest change that can pass the check.
4. VERIFY: run the check, then `make verify`. A task is done only when both pass.
5. SELF-HEAL: on any failure, read the complete error output first, research the error before patching, and fix the root cause, never the symptom. Re-run. After 3 failed attempts on the same issue, stop and present the situation with options instead of thrashing.

## Gaps, bugs, inconsistencies
- If the spec is ambiguous, wrong, or missing something: do not silently deviate. Add an entry to design.md's decision log (what, why, what the spec said, what you did), then proceed.
- If a bug appears that `make verify` did not catch: write the failing check FIRST, commit it red, then fix until green. Every escaped bug becomes a permanent test.
- Never weaken, skip, mock, or delete a check to make it pass. If a check is wrong, fix it in a separate commit with reasoning.

## Discipline
- Commit after every green `make verify` with a message naming the phase and task.
- Pinned versions and digests everywhere; no `latest`.
- No secret in any file, layer, or log; `make check-env` must stay enforcing.
- shellcheck-clean bash; docs updated in the same commit as behavior changes.
- A container runtime is required: if `scripts/runtime info` reports no reachable engine, stop and say so rather than mocking. Never call `docker` directly in a script; use `mdb_ctr` / `mdb_compose` from `scripts/runtime-lib.sh` (check 48, design.md D-60). Runtime-specific behaviour belongs in an adapter hook in `scripts/runtimes/` (see `scripts/runtimes/README.md`), never in a `case "$MDB_RUNTIME"` in the core or a caller. Image references are fully qualified (`docker.io/library/...`, check 49).
- Scripts reach stack services through the backend layer in `scripts/backend-lib.sh` (design.md D-62, check 52): `mdb_svc_exec [-u USER] SVC CMD`, `mdb_be_running`, `mdb_svc_health`, `mdb_be_volumes`, never `mdb_compose exec` or `$(COMPOSE) exec`. Deployment-specific behaviour belongs in a backend in `scripts/backends/`; a script that only works on Compose says so with `mdb_backend_require compose`. The Kubernetes backend (D-64) reaches the cluster only through `_mdb_k8s_kubectl`/`_mdb_k8s_helm`, which pin `MDB_K8S_CONTEXT` and the namespace; engine settings reach the chart through `scripts/helm-values`, which reads `compose.engines.yml`, so never keep a second list.
- Env files: a value containing whitespace or a special character is double-quoted (`TENANTS="landlord panel api socket queue"`). Read values with `mdb_env_get`, write generated ones with `mdb_env_quote` (`scripts/env-lib.sh`, check 50); never `grep | cut -d= -f2-`.
- Markdown file names are lowercase slugs (`deploy-guide.md`), except the conventional ones that stay upper case: `README.md` (at any level), `CHANGELOG.md`, `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `LICENSE`, `CLAUDE.md`, `.github/PULL_REQUEST_TEMPLATE.md` (design.md D-61, check 51).

## Commands
- `make init` / `make init-prod` - create .env (or .env.prod) from the template, generate secrets, run check-env
- `make up` / `make down`  - stack lifecycle (never touches data)
- `make verify`            - full acceptance harness (section 18 of spec.md as scripts)
- `make verify-fast`       - subset for inner-loop iteration
- `make verify CHECKS=21,28` / `TAGS=pg,backup` - run selected checks only
- `make test-profile`      - boots the tmpfs test profile and runs its checks
- `make backup-all` / `make verify-backups` - full-fleet dumps and automated restore verification
- Full public interface: spec.md section 15.1 (certs, sockets, rotation, upgrade, import/export)
