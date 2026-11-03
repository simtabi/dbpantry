# Contributing

Where this file is silent, the [Simtabi contributing guide](https://github.com/simtabi/.github/blob/HEAD/CONTRIBUTING.md) applies.

## The rule that matters most

Nothing is done until a check proves it. Every change either passes an existing
`make verify` check or brings a new one with it. If you find a bug that
`make verify` did not catch, **write the failing check first**, commit it red,
then fix until green. Every escaped bug becomes a permanent test.

Never weaken, skip, mock, or delete a check to make a build pass. If a check is
itself wrong, fix it in a separate commit that explains the reasoning.

## Getting set up

```bash
git clone https://github.com/simtabi/dbpantry.git
cd dbpantry
make init
make verify-structure   # harness self-test; must be green
make verify             # full acceptance harness
make verify-fast        # the fast, Docker-light subset
make verify CHECKS=21,28        # only these checks
make verify TAGS=backup,pg      # checks carrying any of these tags
```

Where things live: compose files and the Makefile at the root, one command
per file in `scripts/`, checks in `scripts/verify/`, engine descriptors in
`engines/`, image sources in `images/`, files mounted into third-party
containers in `config/`, and documentation in `docs/guides/`,
`docs/runbooks/` and `docs/reference/`. The reasoning is in
[docs/reorganization-plan.md](docs/reorganization-plan.md).

Checks never touch your data. A check may create or remove only volumes and
Compose projects whose names start with `mdbverify`, `mdbscratch` or
`mdb-verify-` (`scripts/verify/lib.sh` refuses anything else), every check gets
a private `MDB_BACKUP_DIR` that is removed when it exits, and a check that
needs a whole stack with its own secrets runs in `scratch_stack`. Check 60 fails
a check that removes a volume around those guards, names an operator volume
such as `mdb_pg17_data`, or spells the checkout's `backups/` path.

A container runtime is required: Docker (Desktop, OrbStack, Colima, Rancher
Desktop) or Podman; nerdctl is best effort. CI runs on Docker. Check yours with
`scripts/runtime info`, and if no engine is reachable, stop; nothing here is
mocked. Scripts reach the runtime only through `scripts/runtime-lib.sh`; see
[Container runtimes](docs/guides/runtimes.md).

## Conventions

- **Bash must be shellcheck-clean.** `make lint` runs shellcheck over every
  script and must pass with no warnings.
- **Pinned versions and digests everywhere.** No `latest` in any Dockerfile,
  compose file, or workflow. Dependabot raises action and Caddy bumps; the
  digest-pinned bases in `images/bases.tsv` are re-pinned by hand (see
  [Release](docs/guides/release.md)).
- **No secret in any file, layer, or log.** Passwords travel via the `_FILE`
  convention only. `make verify` greps the repository and image history for
  credentials and fails on a hit.
- **LF line endings** everywhere, enforced by `.gitattributes`.
- **Named volumes for data**, never bind mounts — WSL2 performance and
  permissions both depend on it.
- Docs are runbooks: exact commands, verification steps, written for a tired
  reader at 2am. Update them in the same commit as the behavior they describe.

## Commits and pull requests

- Subject line ≤ 72 characters, imperative mood. The body explains *why*.
- No emoji in commit messages.
- Name the phase and task where it applies, e.g. `phase 2: pg image s6 tree`.
- A pull request should describe what changed, which check proves it, and any
  entry it added to the `design.md` decision log.

## Spec gaps

If `docs/reference/spec.md` is ambiguous, wrong, or silent on something you need, do not
deviate quietly. Add an entry to the `design.md` decision log recording what the
spec said, what you did, and why — then proceed.

## Reporting security issues

Do not open a public issue. See [SECURITY.md](SECURITY.md).
