# Release

How a version of dbpantry ships, and what a consumer gets.

> **Status:** [`release.yml`](../../.github/workflows/release.yml) implements this
> contract. It has not run yet: v0.1.0 was released by hand, with no images, so
> the next tag is the first that publishes anything. Until then the images exist
> only where `make build` built them.

## What ships

A release is one git tag, `vX.Y.Z`, that produces:

| Artefact | Where |
|---|---|
| Engine images, one per engine per supported major | `ghcr.io/simtabi/dbpantry-<engine>` |
| The `cli` image | `ghcr.io/simtabi/dbpantry-cli` |
| Source tarball | GitHub release |
| SBOM per image | attached to the image |
| Build provenance attestation | attached to the image |

Every image is built for **amd64 and arm64**, from the same digest-pinned bases,
and both architectures are proven by CI before anything is pushed. There is no
emulated build: an engine with no arm64 upstream image is not published for
arm64 rather than shipped as a QEMU image that corrupts data under load.

The derived images, `dbpantry-pgbouncer` and `dbpantry-patroni`, are not
published: `make build` builds them locally from the engine images it has just
built.

**MongoDB is not published.** It is SSPL-licensed, so the toolkit references the
upstream image rather than deriving one. See [Licensing](licensing.md). Check 31
fails the build if that ever changes silently.

## Image tags

| Tag | Example | Moves |
|---|---|---|
| `<major>-<release>` | `dbpantry-pg:17-0.1.1` | never |
| `<major>` | `dbpantry-pg:17` | to each new release |
| `<release>`, cli only | `dbpantry-cli:0.1.1` | never |
| `<tag>-amd64`, `<tag>-arm64` | `dbpantry-pg:17-0.1.1-arm64` | never |

Pin `<major>-<release>`, or better its digest, for anything that matters. The
per-architecture tags are the two halves of each multi-architecture manifest;
pull the manifest, not them. `latest` is never published.

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

The tag drives everything. Nothing is published from a branch.

## The workflow

`release.yml` runs on a `vX.Y.Z` tag and nothing else. Its jobs run in this
order, and each refuses as early as it can:

| Job | What it does | Refuses when |
|---|---|---|
| resolve release | reads the version from the tag and the images from the engine descriptors | the tag is not on `main`, `CHANGELOG.md` has no section for it, or MongoDB is in the matrix |
| scan | builds every image on both architectures and runs the Trivy gate | any image fails to build or has a fixable HIGH or CRITICAL finding |
| verify | runs `make verify` on amd64 and arm64 | any check fails |
| publish | builds again, scans again, pushes each architecture, and attaches its SBOM and build provenance | the second scan fails |
| manifest | joins the two architectures under the tags above, with provenance | — |
| GitHub release | creates the release with the notes and the source tarball, its checksum and provenance | the release already exists |

Nothing is pushed until scan and verify have passed for every image, so a
finding in one engine cannot leave the others half-published under the version.
The images are built on native runners for each architecture, never under QEMU.
The workflow uses no third-party action: docker login and `docker buildx
imagetools` come from the runner, and trivy and syft are binaries checked
against their published checksums. GitHub's own `actions/checkout` and
`actions/attest` float on their major tag, per the Simtabi action rule.

Verify an image's provenance with:

```bash
gh attestation verify oci://ghcr.io/simtabi/dbpantry-pg:17-0.1.1 --owner simtabi
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
3. Tag the merge commit on `main` and push the tag (see [Tagging](#tagging)).

The first publish creates each `dbpantry-*` package on GHCR, and GHCR creates a
package private. The owner sets each one public once, in the package settings;
until then a pull without credentials fails.

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

Or pull images directly without the repository, pinned by digest for anything
that matters.

`make self-update` updates the toolkit itself and never touches project data or
`secrets/`.

---

[← Docs index](../../README.md#documentation)
