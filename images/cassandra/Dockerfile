# dbpantry-cassandra — Apache Cassandra with authentication actually on.
#
# The headline: the official image ships `AllowAllAuthenticator` AND
# `AllowAllAuthorizer`. That is not weak authentication, it is none, with no
# authorization either — any client that reaches port 9042 is a superuser.
# SPEC section 22.3 requires every engine here to correct that at build time.
#
# Build context is images/, like every other image.
ARG BASE_IMAGE=docker.io/library/cassandra:5.0.9@sha256:1d18946ead0b0d22ed0371bd7384d86f540465c489f2935e88df35e755073f6b
# The Go toolchain the gotools stage builds with; pinned in images/bases.tsv
# (row `toolchain go`) and passed by scripts/build, like BASE_IMAGE.
ARG GO_IMAGE=docker.io/library/golang:1.27.3-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9

# =============================================================================
# Go tools, rebuilt from their upstream release tags on a patched toolchain
# (design.md D-51). The released binaries carry the Go standard library of the
# day they were cut, and their CVEs with it; the same tag rebuilt on current Go
# does not. Cross-compiled on the build platform: arm64 is never emulated.
# =============================================================================
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS gotools

ARG TARGETOS
ARG TARGETARCH
COPY --chmod=0755 _shared/gotools/build.sh /usr/local/bin/build-go-tool
COPY _shared/gotools/floors.txt /usr/local/share/build-go-tool/floors.txt

# gosu: the tag the base image already ships, and the binary its entrypoint and
# ours call. Every finding against it was in the Go standard library.
ARG GOSU_VERSION=1.19
RUN build-go-tool gosu https://github.com/tianon/gosu "$GOSU_VERSION" . /out/gosu

FROM ${BASE_IMAGE}

ARG ENGINE_VERSION=5.0
ARG S6_OVERLAY_VERSION=3.2.3.2
# true when scripts/build or compose builds from MDB_CASSANDRA_BASE_IMAGE, an
# unmaintained series (design.md D-57). jars.tsv has no rows for one, so its
# vendored JARs are left as upstream shipped them; the image is unsupported.
ARG UNSUPPORTED_VERSION=false

LABEL org.opencontainers.image.title="dbpantry-cassandra" \
      org.opencontainers.image.description="Apache Cassandra with authentication and authorization enabled, supervised by s6-overlay" \
      org.opencontainers.image.vendor="Simtabi LLC" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.source="https://github.com/simtabi/dbpantry"

USER root
ENV DEBIAN_FRONTEND=noninteractive

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl procps xz-utils zstd; \
    # The base digest predates Debian's pcre2, expat and perl-base security
    # updates. They are fixable, so they are fixed -- named packages only, so
    # nothing else in the pinned base moves.
    apt-get install -y --only-upgrade libpcre2-8-0 libexpat1 perl-base; \
    # CVE-2026-109518 (HIGH, apt 3.0.3, fixed in 3.0.4 on trixie, which
    # cassandra:5.0.9 moved to). apt and its library move together.
    apt-get install -y --only-upgrade apt libapt-pkg7.0; \
    rm -rf /var/lib/apt/lists/*

# Cassandra vendors its Java dependencies in lib/, and 5.0 ships guava,
# snappy-java, jackson, netty and lz4-java below their security fixes, with no
# newer Cassandra release to repin to. Each
# is replaced with a fixed release from Maven Central, pinned by sha256 in
# jars.tsv (design.md D-55). The series is read from the base image itself, so
# a BASE_IMAGE and ENGINE_VERSION that disagree fail here rather than install
# the other series' JARs. `cassandra -v` then starts the JVM on the new
# classpath; the boot checks prove the rest.
COPY cassandra/jars.tsv /usr/local/share/mdb/cassandra-jars.tsv
COPY --chmod=0755 cassandra/replace-jars.sh /usr/local/bin/replace-jars
RUN set -eux; \
    series="${CASSANDRA_VERSION%.*}"; \
    if [ "$series" != "$ENGINE_VERSION" ]; then \
        echo "base image is Cassandra $CASSANDRA_VERSION but ENGINE_VERSION is $ENGINE_VERSION" >&2; \
        exit 1; \
    fi; \
    UNSUPPORTED_VERSION="$UNSUPPORTED_VERSION" \
        replace-jars /usr/local/share/mdb/cassandra-jars.tsv "$series" "${CASSANDRA_HOME:?}/lib"; \
    cassandra -v

# gosu from the gotools stage (design.md D-51). gosu replaces
# the base image's copy in place, so its entrypoint keeps calling the same path.
COPY --from=gotools /out/gosu /usr/local/bin/
RUN set -eux; gosu --version; gosu nobody true

# s6-overlay v3 as PID 1, same contract as every other engine image.
RUN set -eux; \
    case "$(dpkg --print-architecture)" in \
        amd64) s6_arch='x86_64' ;; \
        arm64) s6_arch='aarch64' ;; \
        *) echo "unsupported architecture: $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac; \
    url="https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}"; \
    curl -fsSL -o /tmp/s6-noarch.tar.xz "${url}/s6-overlay-noarch.tar.xz"; \
    curl -fsSL -o /tmp/s6-arch.tar.xz   "${url}/s6-overlay-${s6_arch}.tar.xz"; \
    tar -C / -Jxpf /tmp/s6-noarch.tar.xz; \
    tar -C / -Jxpf /tmp/s6-arch.tar.xz; \
    rm -f /tmp/s6-noarch.tar.xz /tmp/s6-arch.tar.xz

COPY cassandra/s6/ /etc/s6-overlay/s6-rc.d/
COPY cassandra/user-bundles.d/ /etc/s6-overlay/user-bundles.d/
COPY cassandra/scripts/ /usr/local/lib/mdb/

ENV MDB_ENGINE=cassandra

RUN set -eux; \
    chmod +x /usr/local/lib/mdb/*.sh; \
    chmod +x /etc/s6-overlay/s6-rc.d/mdb-engine/run; \
    mkdir -p /mdb/overrides /run/mdb

# Gracetimes at the s6 defaults; raising them only makes the container idle
# after everything is already down (design.md D-27).
ENV S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0 \
    S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_KEEP_ENV=1

# Cassandra is slow to become queryable even on a healthy start: it has a JVM
# to warm and a gossip round to complete. The start period reflects reality
# rather than optimism.
HEALTHCHECK --interval=15s --timeout=10s --start-period=180s --retries=6 \
    CMD nodetool status | grep -q '^UN' || exit 1

EXPOSE 9042

ENTRYPOINT ["/init"]
