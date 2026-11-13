# dbpantry-patroni — the PostgreSQL image, with Patroni owning the server.
#
# Built FROM dbpantry-pg rather than from the upstream base, so an HA node is
# the same PostgreSQL as a standalone one: same extensions, same hardening, same
# TLS material. A separately-based HA image would drift from the thing it is
# supposed to be a highly-available copy of.
#
# The one structural difference is who starts PostgreSQL. In the standalone
# image the upstream entrypoint does, supervised by s6 as mdb-postgres. Here
# Patroni does: it initialises the data directory, writes postgresql.conf and
# recovery settings, promotes and demotes. So mdb-postgres is removed from the
# bundle rather than left to race Patroni for the same data directory.

ARG ENGINE_VERSION=17
ARG IMAGE_PREFIX=localhost
FROM ${IMAGE_PREFIX}/dbpantry-pg:${ENGINE_VERSION}

# Pinned. Patroni's behaviour on failover is the whole point of this image, so
# it is not something to let float.
ARG PATRONI_VERSION=4.1.4

USER root

# A venv, not a system pip install. Debian marks its Python as externally
# managed (PEP 668), so a system install needs --break-system-packages, which
# is exactly the kind of override that works until a base image bump.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends python3 python3-venv; \
    python3 -m venv /opt/patroni; \
    /opt/patroni/bin/pip install --no-cache-dir --upgrade pip; \
    /opt/patroni/bin/pip install --no-cache-dir \
        "patroni[etcd3]==${PATRONI_VERSION}" \
        "psycopg[binary]"; \
    ln -sf /opt/patroni/bin/patroni     /usr/local/bin/patroni; \
    ln -sf /opt/patroni/bin/patronictl  /usr/local/bin/patronictl; \
    apt-get purge -y --auto-remove python3-venv; \
    rm -rf /var/lib/apt/lists/*; \
    patroni --version

COPY patroni/scripts/ /usr/local/lib/mdb/
COPY patroni/s6/ /etc/s6-overlay/s6-rc.d/

# The bundle is edited rather than rewritten: the stages that prepare
# permissions and certificates still apply, and only the one that starts
# PostgreSQL is replaced.
#
# mdb-provision and mdb-converge go too. Both assume the upstream entrypoint's
# first-run contract -- provision writes into /docker-entrypoint-initdb.d, which
# Patroni's bootstrap never reads -- so leaving them would create work that
# silently never happens.
#
# Dropping a service from the bundle is NOT enough on its own: s6 also starts
# anything a remaining service depends on, and mdb-exporter depends on
# mdb-postgres. That edge started the upstream entrypoint alongside Patroni,
# both aiming at the same data directory, and the entrypoint then died on a
# missing POSTGRES_PASSWORD -- an error naming neither of the real causes. So
# the service definitions are deleted outright, not just unreferenced.
RUN set -eux; \
    chmod +x /usr/local/lib/mdb/*.sh; \
    chmod +x /etc/s6-overlay/s6-rc.d/mdb-patroni/run; \
    rm -f /etc/s6-overlay/user-bundles.d/user/contents.d/mdb-postgres; \
    rm -f /etc/s6-overlay/user-bundles.d/user/contents.d/mdb-provision; \
    rm -f /etc/s6-overlay/user-bundles.d/user/contents.d/mdb-converge; \
    rm -f /etc/s6-overlay/user-bundles.d/user/contents.d/mdb-exporter; \
    rm -rf /etc/s6-overlay/s6-rc.d/mdb-postgres \
           /etc/s6-overlay/s6-rc.d/mdb-provision \
           /etc/s6-overlay/s6-rc.d/mdb-converge \
           /etc/s6-overlay/s6-rc.d/mdb-exporter; \
    touch /etc/s6-overlay/user-bundles.d/user/contents.d/mdb-patroni; \
    mkdir -p /etc/patroni /var/lib/postgresql/patroni; \
    chown -R postgres:postgres /etc/patroni /var/lib/postgresql/patroni

# Patroni's REST API. HAProxy routes on /primary and /replica against it, which
# is what makes the write port follow an election rather than a static config.
EXPOSE 8008

LABEL org.opencontainers.image.title="dbpantry-patroni" \
      org.opencontainers.image.description="dbpantry PostgreSQL under Patroni, for the rehearsal HA topology" \
      org.opencontainers.image.licenses="PostgreSQL"
