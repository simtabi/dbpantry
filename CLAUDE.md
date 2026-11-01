# dbpantry build rules

You are building dbpantry per docs/SPEC.md. That file is the contract. Read it before any work.

## The loop (applies to every task, no exceptions)
1. PLAN: restate the task and name its executable check before writing code.
2. RESEARCH: before implementing any component, search the web for the current docs of the exact pinned versions involved (s6-overlay v3, the official postgres/mysql/mariadb image entrypoints, pgvector, tiredofit/db-backup, phpmyadmin envs, Caddy). Record what you relied on as links in DESIGN.md.
3. IMPLEMENT: smallest change that can pass the check.
4. VERIFY: run the check, then `make verify`. A task is done only when both pass.
5. SELF-HEAL: on any failure, read the complete error output first, research the error before patching, and fix the root cause, never the symptom. Re-run. After 3 failed attempts on the same issue, stop and present the situation with options instead of thrashing.

## Gaps, bugs, inconsistencies
- If the spec is ambiguous, wrong, or missing something: do not silently deviate. Add an entry to DESIGN.md's decision log (what, why, what the spec said, what you did), then proceed.
- If a bug appears that `make verify` did not catch: write the failing check FIRST, commit it red, then fix until green. Every escaped bug becomes a permanent test.
- Never weaken, skip, mock, or delete a check to make it pass. If a check is wrong, fix it in a separate commit with reasoning.

## Discipline
- Commit after every green `make verify` with a message naming the phase and task.
- Pinned versions and digests everywhere; no `latest`.
- No secret in any file, layer, or log; `make check-env` must stay enforcing.
- shellcheck-clean bash; docs updated in the same commit as behavior changes.
- Docker required: if `docker info` fails, stop and say so rather than mocking.

## Commands
- `make init` / `make init-prod` - create .env (or .env.prod) from the template, generate secrets, run check-env
- `make up` / `make down`  - stack lifecycle (never touches data)
- `make verify`            - full acceptance harness (section 18 of SPEC.md as scripts)
- `make verify-fast`       - subset for inner-loop iteration
- `make test-profile`      - boots the tmpfs test profile and runs its checks
- `make backup-all` / `make verify-backups` - full-fleet dumps and automated restore verification
- Full public interface: SPEC.md section 15.1 (certs, sockets, rotation, upgrade, import/export)
