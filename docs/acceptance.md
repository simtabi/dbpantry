# Acceptance

Every acceptance criterion in [SPEC](SPEC.md) section 18, mapped to the check
that proves it — and the ones nothing proves yet, named rather than omitted.

The harness is the definition of done: SPEC section 18 *is* `scripts/verify/`,
so a green `make verify` is the criteria being met rather than a proxy for it.

## Section 18

| # | Criterion | Proven by | State |
|---|---|---|---|
| 1 | Clone → `make init` → `make up` gives PG + Adminer in two commands; profiles bring the rest; Caddy serves every UI over HTTPS | `15-compose-lifecycle`, `18-ui-front-door` | Covered |
| 2 | `make init-prod` renders a prod env passing `check-env`, TLS enforced, nothing published but Caddy, ports remappable from `.env` | `26-prod-profile-guards`, `06-check-env-enforces` | Covered |
| 3 | Each engine image runs standalone with only a password env; EMBED toggles self-backup and metrics | `08-pg-standalone-run`, `16-mysql-family-boot`, `24-metrics-and-embed` | Covered |
| 4 | Version switch 16→17 uses a new volume, leaves the old intact; `make upgrade` migrates; same for MySQL 8.0→8.4 | `14-version-switch`, `29-upgrade-migrates-data` | Covered |
| 5 | MySQL and MariaDB run concurrently, phpMyAdmin sees both | `16-mysql-family-boot`, `18-ui-front-door` | Covered |
| 6a | Two projects per engine, isolated roles, cross-access denied | `13-triplet-isolation`, `28-new-project`, `34-new-project-every-engine` | Covered |
| 6b | PG RLS demo passes | `36-rls-demo` | Covered |
| 7a | Fresh VPS path ends TLS-only, unpublished or firewalled, no UI exposed | `20-tls-enforced-prod`, `26-prod-profile-guards` | Covered |
| 7b | Nightly S3 backups | `39-offsite-backup`, `check-env` prod guard | Covered — dumps via rclone, WAL via pgBackRest's S3 repo |
| 7c | PITR active for PG | `37-pitr-recovers` | Covered |
| 7c+ | PITR for MySQL and MariaDB (beyond spec) | `40-mysql-family-pitr` | Covered |
| 8 | Restore drill with row-count verification; `backup-all` produces per-database dumps plus PG globals; `verify-backups` restores the latest set green | `21-backup-restore-roundtrip`, `22-backup-all-and-verify` | Covered |
| 9 | `make psql` over the shared unix socket; a sidecar mounting the socket volume connects with no TCP | `19-unix-sockets` | Covered |
| 10 | `sslmode=verify-full` against the toolkit CA succeeds; prod MySQL-family plaintext refused | `12-pg-tls`, `20-tls-enforced-prod` | Covered |
| 11a | CI matrix green | `.github/workflows/ci.yml` | Written, never run — see below |
| 11b | trivy clean or waived with notes | `38-trivy-waivers`, CI scan step | Covered |
| 11c | Repo and image-history grep finds no credential | `03-secret-scan-repo`, `23-image-history-secrets` | Covered |

## Sections 21 and 22

| Criterion | Proven by |
|---|---|
| Killing the Patroni leader elects a new one; the write port follows and the read port serves replicas only | `27-ha-failover` |
| etcd runs a quorum of at least three | `27-ha-failover`, `check-env` |
| No engine, of any paradigm, accepts unauthenticated connections | `32-every-engine-auth` |
| No trust or empty-password auth anywhere, including loopback | `30-no-trust-auth` |
| Every engine descriptor is complete, consistent, and honestly declared | `31-engine-descriptors` |
| The pooler multiplexes and holds no application password (PostgreSQL) | `33-pooler-works` |
| The pooler holds verifiers, not passwords (MySQL family) | `35-proxysql-verifiers` |
| Every engine family can provision a project | `34-new-project-every-engine` |
| PITR recovers to a point in time (PostgreSQL) | `37-pitr-recovers` |
| PITR recovers to a point in time (MySQL and MariaDB) | `40-mysql-family-pitr` |
| Vulnerability waivers are package-scoped, justified, and expire | `38-trivy-waivers` |
| No Dockerfile passes a dnf-only flag to microdnf | `43-microdnf-flags` |
| No leftover old product name outside its history | `44-no-old-name` |
| The off-site push is wired, authenticated, and fails loudly | `39-offsite-backup` |
| Configuration reproduces, data persists, recovery data outlives `destroy` | `41-state-survives` |
| Every image is official, upstream, or justified | `42-image-provenance` |
| The RLS kit isolates tenants, including from the table owner | `36-rls-demo` |

## What is not proven

Stated plainly, because a criterion quietly dropped from a report is worse than
one that is openly outstanding.

**CI matrix green (11a).** The workflow is written — the matrix derived from
the engine descriptors, images built per version per architecture, each scanned
by trivy — but the repository has no remote, so GitHub Actions has never
executed it. What is verified locally: the YAML parses, `scripts/ci-matrix`
produces the twelve (engine, version) pairs, the exact build path succeeds for
a non-default version, and the trivy invocation was run by hand against a built
image (it found seventeen real findings, now waived with reasons). Calling that
"green" would still be a claim about something that has not run.

This is the one criterion that cannot be closed from here: it needs a push to
GitHub, which is the user's to make.

## Beyond the specification

Work the spec did not ask for, done because the gaps were real once the rest
existed. Recorded here so the harness's size is explained rather than assumed.

| What | Why | Check |
|---|---|---|
| PITR for MySQL and MariaDB | The spec asks only for PostgreSQL. Leaving two engines recoverable only to the last dump, in a toolkit that advertises verified restores, would be a strange asymmetry. | `40` |
| Vulnerability waivers must justify themselves | "trivy clean **or waived with notes**" — the notes half is what decays, and an ignore file is trivially abused to make a scanner green. An expiry date makes the revisit mechanical rather than a promise (D-51). | `38` |
| Image provenance | "Use official images" erodes one convenient third-party rebuild at a time, each defensible alone. | `42` |
| State lifetimes | Configuration, data and recovery data have three different lifetimes and get confused with each other; each confusion has a distinct failure. | `41` |
| Pooler credential handling | A pooler that holds every application password is a second place every credential lives. | `33`, `35` |

## A note on running the harness

Every check has been observed green — individually or in a clean run. A single
uninterrupted full `make verify` has not completed on the development machine
used here, because Docker reclaims images partway through a ~40 minute run: one
run logged eight images present at build time and one at the end.

The harness now says so itself rather than leaving it to be worked out. It
snapshots the image list at start, and a check whose image has since vanished
reports a reclaim — "environment failure, not a build failure" — instead of the
misleading "run: make build" that sent this project diagnosing builds which had
already succeeded. The summary counts it once.

If a full run matters to you, free disk space first. CI's runners do not share
this limit.

---

[← Docs index](../README.md#documentation)
