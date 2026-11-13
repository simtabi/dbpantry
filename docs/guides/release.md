# Release

How a version of dbpantry ships, and what a consumer gets.

> **Status:** [`release.yml`](../../.github/workflows/release.yml) implements this
> contract. v0.1.0 was tagged by hand, and the GitHub release has not been
> published for any version yet.
>
> **Container images are no longer published** (owner, 2026-11-13: a release ships
> on the GitHub release only). The `ghcr.io/simtabi/dbpantry-*` images are frozen:
> no new tag is pushed, and nothing is deleted from ghcr.io. Measured 2026-11-13,
> no dbpantry image had been published there. Build the images with `make build`.

## What ships

A release is one git tag, `vX.Y.Z`, pushed to GitHub (see [Publishing](#publishing)).
It produces the GitHub release, and nothing else:

| Artefact | Where |
|---|---|
| Release notes, from the version's `CHANGELOG.md` section | GitHub release |
| Source tarball, `dbpantry-X.Y.Z.tar.gz`, and its `.sha256` | GitHub release |
| Build provenance attestation for the tarball | GitHub attestations |

The images are the toolkit's product, but they are built where they run:
`make build` builds every engine image, the `cli` image and the derived
`dbpantry-pgbouncer` and `dbpantry-patroni` images from the digest-pinned bases.
Before a release is published, CI still builds every engine image and the `cli`
image on **amd64 and arm64**, on native runners, and scans each one. There is no
emulated build: an engine with no arm64 upstream image fails the release rather
than shipping as a QEMU image that corrupts data under load.

**MongoDB is not published.** It is SSPL-licensed, so the toolkit references the
upstream image rather than deriving one. See [Licensing](licensing.md). Check 31
fails the build if that ever changes silently.

## Image tags (frozen)

No image is published any more. This is the tag scheme `release.yml` used for
`ghcr.io/simtabi/dbpantry-*` until 2026-11-13, kept so that a tag already pulled
can be read. `make build` tags local images `<major>` (engines) and `dev` (cli).

| Tag | Example | Moves |
|---|---|---|
| `<major>-<release>` | `dbpantry-pg:17-0.1.1` | never |
| `<major>` | `dbpantry-pg:17` | to each new release |
| `<release>`, cli only | `dbpantry-cli:0.1.1` | never |
| `<tag>-amd64`, `<tag>-arm64` | `dbpantry-pg:17-0.1.1-arm64` | never |

The per-architecture tags were the two halves of each multi-architecture
manifest. `latest` was never published.

## Versioning

Semantic versioning, applied to *the toolkit*, not to the databases inside it.

| Change | Bump |
|---|---|
| A `MDB_` variable is removed or changes meaning | major |
| A `make` target is removed or its arguments change | major |
| A default changes in a way that alters running behaviour | major |
| An engine or engine version is added | minor |
| A new `make` target or variable | minor |
| An engine's patch version is repinned | patch |
| A fix that changes no interface | patch |

Adding a **major version of a database** to the menu is a minor bump for the
toolkit. Removing one is major, because a checkout pinned to it stops starting.

## Tagging

```bash
git tag -a v1.4.0 -m "..."
git push origin v1.4.0
```

**Pushing a `vX.Y.Z` tag releases it.** Push one only for final code that the
owner has approved: tags are final, and the push starts the release workflow.
Nothing is published from a branch or a pull request.

## Publishing

A release runs in two ways, from any directory:

```bash
git -C ~/Artisan/projects/opensource/simtabi/packages/dbpantry push origin vX.Y.Z   # releases vX.Y.Z
gh workflow run release.yml --repo simtabi/dbpantry -f tag=vX.Y.Z                    # the same, for a tag already pushed
```

- The tag push is the normal path. The dispatch is for a tag that is already
  on GitHub: a run that failed, or a tag pushed before this workflow existed.
- `vX.Y.Z` must be a tag **already on GitHub** (see [Tagging](#tagging)). Every
  job checks out `refs/tags/vX.Y.Z`, so the version and the commit come from the
  tag, never from the branch a dispatch was started from.
- Anything but a `vX.Y.Z` tag that exists on GitHub is refused by the
  workflow's first step, before anything is checked out or built.

`src/internal/release`'s `TestReleaseRunsOnAVersionTagOrOnRequest` fails if
`release.yml` gains a branch push, `schedule`, `pull_request` or `release`
trigger, a tag filter that is not a version, loses the required `tag` input,
stops refusing first, checks out anything but the tag, or publishes a container
image (a registry login or push, a manifest, an attestation pushed to a
registry, a `docker/` publish action, or `packages: write`).

## The workflow

`release.yml` runs on a pushed version tag or a dispatch naming one (see
[Publishing](#publishing)). Its jobs run in this order, and each refuses as
early as it can:

| Job | What it does | Refuses when |
|---|---|---|
| resolve release | refuses first unless the tag is a `vX.Y.Z` tag on GitHub, then checks it out, reads the version from it and the images to scan from the engine descriptors | the tag is not a version tag on GitHub, the tag is not on `main`, `CHANGELOG.md` has no section for it, or MongoDB is in the matrix |
| scan | builds every image on both architectures and runs the Trivy gate | any image fails to build or has a fixable HIGH or CRITICAL finding |
| verify | runs `make verify` on amd64 and arm64 | any check fails |
| GitHub release | once scan and verify have passed, creates the release with the notes and the source tarball, its checksum and provenance | the release already exists |

Nothing is published until scan and verify have passed for every image. The
images are built on native runners for each architecture, never under QEMU.
The workflow uses no third-party action: trivy is a binary checked against its
published checksums, and GitHub's own `actions/checkout` and `actions/attest`
float on their major tag, per the Simtabi action rule.

dbpantry builds no release binaries, so it keeps its own jobs rather than
calling `simtabi/.github`'s shared `go-release.yml`, which runs GoReleaser.

Verify the source tarball's provenance with:

```bash
gh attestation verify dbpantry-X.Y.Z.tar.gz --repo simtabi/dbpantry
```

## Release notes

Every release carries a real description, taken from that version's
`CHANGELOG.md` section — never auto-generated notes alone and never "see
CHANGELOG". The workflow extracts the `## [X.Y.Z]` block and passes it as the
release body, with the contributor list appended.

[`scripts/release-notes`](../../scripts/release-notes) does the extraction, and the
workflow runs it before anything is built. Preview the next release's notes with:

```bash
scripts/release-notes Unreleased
```

`CHANGELOG.md` follows Keep a Changelog. An entry says what changed for someone
running this, not what was edited.

## Before tagging

The org-wide shipping checklist is the gate. Specific to this repository:

```bash
make lint                # shellcheck, clean
make verify              # the full harness, on both architectures in CI
make verify-structure    # every check is well-formed and runnable
```

`make verify` is the definition of done — SPEC section 18's acceptance criteria
are the checks, so a green harness is the criteria being met rather than a proxy
for it.

Then confirm, by hand:

- `CHANGELOG.md` has a section for the version, and it is honest about breaking changes
- `.env.example` documents every new variable (check 05 enforces this, but read it)
- no engine version reached end of upstream support without a note
- `design.md` records any decision made during the cycle

## Cutting a release

1. In a pull request, rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`, add
   an empty `## [Unreleased]` above it, and update the compare links at the
   bottom of `CHANGELOG.md`. `scripts/release-notes X.Y.Z` prints the body the
   release will carry.
2. Merge it, once CI is green.
3. Once the owner approves the version, tag the merge commit on `main` and push
   the tag (see [Tagging](#tagging) and [Publishing](#publishing)). The push
   starts the release.

## Digest pinning

Every base image is pinned by digest in `images/bases.tsv`, and every declared
engine version must have a pinned base or check 31 fails. `latest` appears
nowhere.

Repinning is a deliberate act: update the digest, rebuild, run the harness, and
release it as a patch. A base that silently moved underneath a tag is how a
"working" release stops working with no commit to blame.

## Consuming a release

```bash
git checkout v1.4.0
make init
make up
```

`make build` builds the images. They are no longer published to ghcr.io, so
there is no image to pull without the repository.

`make self-update` updates the toolkit itself and never touches project data or
`secrets/`.

---

[← Docs index](../../README.md#documentation)
