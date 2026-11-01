# Release

How a version of dbpantry ships, and what a consumer gets.

> **Status:** the repository has `ci.yml` and `lint.yml`. The release workflow
> described below is **not yet in `.github/workflows/`** — it is KIT phase 6, and
> until it lands the images are built locally by `make build` and nothing is
> published. This page is the contract it will implement, not a description of
> something already running. It is here so the contract is settled before the
> automation is written, rather than inferred from it afterwards.

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

**MongoDB is not published.** It is SSPL-licensed, so the toolkit references the
upstream image rather than deriving one. See [Licensing](licensing.md). Check 31
fails the build if that ever changes silently.

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

## Release notes

Every release carries a real description, taken from that version's
`CHANGELOG.md` section — never auto-generated notes alone and never "see
CHANGELOG". The workflow extracts the `## [X.Y.Z]` block and passes it as the
release body, with the contributor list appended.

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
- `DESIGN.md` records any decision made during the cycle

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

[← Docs index](../README.md#documentation)
