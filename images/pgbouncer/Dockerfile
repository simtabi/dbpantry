# dbpantry-pgbouncer — pgBouncer from Debian's own package.
#
# pgBouncer publishes no official image, and this was the toolkit's only
# third-party rebuild (edoburu/pgbouncer). A third party is an organisation
# that can be compromised independently of the project it packages, and whose
# update cadence you do not control -- the same argument that pins every
# reference by digest.
#
# So it is built here instead: a Docker Official Image base plus the PGDG
# package, which is maintained by the PostgreSQL project. Nothing in the
# toolkit now depends on a community rebuild.

ARG BASE_IMAGE=docker.io/library/debian:12-slim
FROM ${BASE_IMAGE}

ARG PGBOUNCER_VERSION

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg postgresql-common; \
    # PGDG rather than Debian stable: pgBouncer's transaction-pooling
    # behaviour around prepared statements changed materially in 1.21, and
    # docs/guides/pooling.md relies on the newer behaviour.
    /usr/share/postgresql-common/pgdg/apt.postgresql.org.sh -y; \
    apt-get update; \
    apt-get install -y --no-install-recommends pgbouncer; \
    apt-get purge -y --auto-remove curl gnupg; \
    rm -rf /var/lib/apt/lists/*; \
    pgbouncer --version

# The upstream image's entrypoint generated pgbouncer.ini from environment
# variables. That contract is reproduced here so the descriptor and
# engines/postgres/compose-pooler.yml did not have to change.
COPY pgbouncer/entrypoint.sh /entrypoint.sh
COPY pgbouncer/bootstrap.sh /usr/local/bin/mdb-pgbouncer-bootstrap

RUN set -eux; \
    chmod +x /entrypoint.sh /usr/local/bin/mdb-pgbouncer-bootstrap; \
    mkdir -p /etc/pgbouncer; \
    chown -R postgres:postgres /etc/pgbouncer

# No USER directive: the container starts as root so the bootstrap script can
# read the mounted secret -- whose host ownership no in-container user can
# match -- and it drops to postgres via setpriv before pgBouncer starts. The
# process that listens on 6432 is postgres; check 33 connects and proves it.
EXPOSE 6432

# Self-contained: `docker run` on this image starts a pooler, without compose
# supplying an entrypoint. Leaving it unset meant the image fell through to the
# base's shell and exited 0 -- a container that "started successfully" and did
# nothing, which is exactly the failure check 33 exists to catch.
ENTRYPOINT ["/usr/local/bin/mdb-pgbouncer-bootstrap"]

LABEL org.opencontainers.image.title="dbpantry-pgbouncer" \
      org.opencontainers.image.description="pgBouncer from the PGDG package, built rather than pulled from a third party" \
      org.opencontainers.image.licenses="ISC"
