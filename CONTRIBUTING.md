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
```

Docker is required — Docker Desktop, OrbStack, or colima. Podman is best-effort
and untested in CI. If `docker info` fails, stop; nothing here is mocked.

## Conventions

- **Bash must be shellcheck-clean.** `make lint` runs shellcheck over every
  script and must pass with no warnings.
- **Pinned versions and digests everywhere.** No `latest` in any Dockerfile,
  compose file, or workflow. Renovate raises the bumps.
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
  entry it added to the `DESIGN.md` decision log.

## Spec gaps

If `docs/reference/SPEC.md` is ambiguous, wrong, or silent on something you need, do not
deviate quietly. Add an entry to the `DESIGN.md` decision log recording what the
spec said, what you did, and why — then proceed.

## Reporting security issues

Do not open a public issue. See [SECURITY.md](SECURITY.md).
