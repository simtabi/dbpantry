# Licensing

What this project is licensed under, what the images you pull are licensed
under, and why those are not the same question.

## The rule

**Every artefact this project publishes is OSI-licensed.**

You should never have to reason about our supply chain to know what you are
running. That means an engine whose licence is not OSI-approved is *referenced*
rather than *rebuilt* under our namespace — fully supported, fully configured by
the toolkit, but pulled from its own vendor.

## Two different licences

**The repository is MIT.** Every script, Dockerfile, descriptor and document
here is MIT, and you can do what you like with them.

**A published image inherits the licence of what it is built FROM.** That was
always true — `dbpantry-pg` contains PostgreSQL under the PostgreSQL licence,
`dbpantry-mysql` contains MySQL under GPLv2 — and it is not a caveat unique to
this project. Our layers are MIT; the database inside is whatever its authors
chose.

## Per-engine

| Engine | Licence | OSI approved | We publish an image |
|---|---|---|---|
| PostgreSQL | PostgreSQL | Yes | Yes |
| MySQL | GPL-2.0 | Yes | Yes |
| MariaDB | GPL-2.0 | Yes | Yes |
| Apache Cassandra | Apache-2.0 | Yes | Yes |
| FerretDB | Apache-2.0 | Yes | Yes |
| **MongoDB** | **SSPL-1.0** | **No** | **No — referenced** |

## MongoDB and the SSPL

MongoDB Community is licensed under the Server Side Public License. The Open
Source Initiative has explicitly declined to recognise the SSPL as an open
source licence, so MongoDB Community is **source-available**, not open source.

The SSPL permits redistribution, so publishing a derived image would be legal.
We do not, because it would mean distributing non-OSI binaries under an
MIT project's namespace, and a user auditing what they run should not have to
discover that.

**Nothing is lost.** MongoDB is a first-class engine here:

- the same descriptor, the same provisioning, the same backup and restore
- the same authentication guarantee — the toolkit turns auth on, which the
  upstream image does not
- the same commands

The only difference is that `docker compose up` pulls `mongo:8.0.x` from
MongoDB's own registry rather than `dbpantry-mongodb` from ours.

**What the SSPL asks of you**, in practice: its service clause binds anyone
*offering MongoDB as a service* to release the source of the service stack.
Running MongoDB as a dependency of your own application does not trigger it.
That is a summary, not legal advice — if you are building a hosted product on
MongoDB, read the licence.

**If SSPL is unacceptable to you**, use the `ferretdb` engine instead. It is
Apache 2.0, speaks the MongoDB wire protocol so your existing drivers connect
unchanged, and stores data in PostgreSQL. Its limitations are real and
documented rather than glossed: no `$lookup` or `$facet` aggregation stages,
partial transaction support, no change streams, GridFS or `$text` indexes. A
drop-in for most workloads, not all.

## Why this is enforced, not merely documented

Check 31 fails the harness if an engine whose licence is not OSI-approved is
set to `PUBLISH=derive`. Licensing intentions decay; a check does not.

It also generalises. Several databases have changed licence in recent years —
Redis, Elastic, and MongoDB itself among them. When one does, moving it is one
field in its descriptor, with no code change and no published artefact to
withdraw.

## Verifying what you run

Every published image carries an accurate
`org.opencontainers.image.licenses` label for its own contents, plus a build
provenance attestation:

```bash
docker inspect ghcr.io/simtabi/dbpantry-pg:17 \
  --format '{{ index .Config.Labels "org.opencontainers.image.licenses" }}'

gh attestation verify oci://ghcr.io/simtabi/dbpantry-pg:17 --owner simtabi
```

---

[← Docs index](../README.md#documentation)
