# multidb-server execution kit

How to take multidb-server-design.md from spec to running, tested software with a self-healing build loop. The principle behind everything here: an agent can only self-heal what a check can catch, so every task ends in an executable check and nothing is "done" until its check passes.

## 0. What you need

- Docker Desktop, OrbStack, or colima running locally
- Claude Code (desktop app Code tab, or `npm install -g @anthropic-ai/claude-code` for the terminal)
- The spec: multidb-server-design.md
- A GitHub repo (CI is the neutral judge for the cross-platform matrix your laptop can't run)

## 1. One-time setup

```
mkdir multidb-server && cd multidb-server
git init
mkdir docs && cp ~/Downloads/multidb-server-design.md docs/SPEC.md
# create CLAUDE.md from section 2 below
git add -A && git commit -m "spec + loop rules"
claude
```

## 2. CLAUDE.md (put this at the repo root, verbatim)

```markdown
# multidb-server build rules

You are building multidb-server per docs/SPEC.md. That file is the contract. Read it before any work.

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
```

## 3. The verify harness (build this in phase 1, everything depends on it)

`make verify` is SPEC.md section 18 turned into scripts under `scripts/verify/`, each exiting nonzero on failure: boot health per enabled engine; standalone `docker run` check per image with only a password env; s6 init order and clean shutdown; every PG extension creates; TLS handshake and non-TLS rejection (prod profile); psql verify-full against the toolkit CA; socket connect through the shared sockets volume; triplet isolation (project A's role denied on project B); backup, drop, restore, row-count assertion per engine; `make backup-all` then `make verify-backups` round-trip; the official images' env compatibility scenarios against our derived images; version-switch (16→17, both volumes intact); secret grep across repo and image history. The harness is written before the features it checks, so the loop has something to heal against from day one.

## 4. Kickoff prompt (first session)

```
Read docs/SPEC.md completely. Produce DESIGN.md exactly as section 17 step 1 describes:
final service map, full env table, decision log including anything you would change
with reasons, and any extension you cannot ship on arm64 with the workaround.
Do not write any implementation code. Stop when DESIGN.md is ready for my review.
```

Review DESIGN.md yourself. Push back where you disagree. Approve explicitly.

## 5. Phase prompts (one phase per session, in order)

After approval, each session starts fresh (CLAUDE.md carries the rules) with one of:

1. "Phase 1: scaffold the repo per SPEC.md section 15 and build the complete `make verify` harness from section 3 of the kit. Checks may fail red where features don't exist yet; structure must be green."
2. "Phase 2: the PG image and dev profile: Dockerfile with s6, provisioning, certs, versioned volumes, Adminer. Loop until every PG check in `make verify` is green."
3. "Phase 3: mysql and mariadb images plus phpMyAdmin. Green their checks."
4. "Phase 4: backup, restore, test profile, prod profile, metrics, and the Caddy front door with scripts/env-render and make init-prod. Green their checks."
5. "Phase 5: cli image, scripts (new-project, upgrade, destroy guard, self-update), docs as runbooks."
6. "Phase 6: CI workflows for the full matrix; iterate until Actions is green on amd64 and arm64."

End every phase with: "Run `make verify`, self-heal until green, then summarize what changed, what you researched, and any DESIGN.md decision-log entries you added."

## 6. Session and safety discipline

- One phase per session keeps context sharp; git is the memory between sessions.
- Commit on green, always; `git revert` is your rollback when a fix regresses something.
- CI (GitHub Actions) is the neutral verifier for what your laptop can't test: the other architecture, all PG majors, the official-image env compatibility suite. The loop for CI failures is identical: Claude Code reads the Actions log, researches, fixes, pushes, repeats.
- When YOU find a problem by hand, don't describe the fix, describe the symptom and say: "write the failing check first, then heal it."

## 7. What self-healing honestly can and can't do

It reliably fixes anything the harness can detect: build failures, boot failures, broken provisioning, TLS misconfig, backup round-trip breakage, regressions. It cannot detect judgment problems: a bad default, a confusing doc, a missing feature nobody wrote a check for. Those are yours to spot in phase reviews, and the write-the-check-first protocol converts each one you find into something the loop guards forever after.
