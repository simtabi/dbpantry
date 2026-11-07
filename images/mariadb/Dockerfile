# dbpantry-mariadb — MariaDB with baked configuration, supervised by s6.
#
# Build context is images/, not images/mariadb/, so this shares the init
# scripts and s6 tree with the MySQL image (see images/_shared/mysql-family).
#
# BASE_IMAGE is passed digest-pinned from images/bases.tsv.
ARG BASE_IMAGE=docker.io/library/mariadb:11.8.9@sha256:6422478cb8e159f080fb1d8ccf65101e26fe51385787fde7d16c3b165a331f15
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

# gosu: the tag the base image already ships, and the binary its entrypoint and
# ours call. Every finding against it was in the Go standard library.
ARG GOSU_VERSION=1.19
RUN build-go-tool gosu https://github.com/tianon/gosu "$GOSU_VERSION" . /out/gosu

# rclone: grpc raised past CVE-2026-84445 (fixed at d5a41119e0e3; 1.75.1 vendors
# an earlier 1.84.0-dev pseudo-version) and x/net past CVE-2026-78669 (fixed in
# v0.60.0; 1.75.1 vendors v0.57.0). VersionSuffix is cleared so the binary
# reports v1.75.1 rather than a -DEV build.
ARG RCLONE_VERSION=1.75.1
RUN LDFLAGS="-X github.com/rclone/rclone/fs.VersionSuffix=" \
    build-go-tool rclone https://github.com/rclone/rclone "v${RCLONE_VERSION}" . /out/rclone \
        google.golang.org/grpc@v1.84.0-dev.0.20260825144003-d5a41119e0e3 \
        golang.org/x/net@v0.60.0

FROM ${BASE_IMAGE}

ARG ENGINE_VERSION=11.8
ARG S6_OVERLAY_VERSION=3.2.3.2

LABEL org.opencontainers.image.title="dbpantry-mariadb" \
      org.opencontainers.image.description="MariaDB with dbpantry configuration and tooling, supervised by s6-overlay" \
      org.opencontainers.image.vendor="Simtabi LLC" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/simtabi/dbpantry"

USER root
ENV DEBIAN_FRONTEND=noninteractive

# Ubuntu 24.04, so apt. The base already carries tar, xz, zstd, gosu, procps
# and openssl; curl and unzip are what it lacks (design.md D-17).
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl unzip; \
    # An Ubuntu 22.04 base -- 10.11, which is no longer maintained here but
    # can still be built through MDB_MARIADB_BASE_IMAGE (design.md D-57) --
    # ships libssl3 below its security update, so it is upgraded where it is
    # installed. The maintained bases are Ubuntu 24.04, which ships libssl3t64
    # instead, and their pinned packages do not move.
    if dpkg-query -W -f='${Status}' libssl3 2>/dev/null | grep -q 'install ok installed'; then \
        apt-get install -y --only-upgrade libssl3; \
    fi; \
    # CVE-2026-109518 (HIGH, apt 2.8.3 on Ubuntu 24.04, fixed in
    # 2.8.4ubuntu0.1). apt and its library move together; the library is
    # libapt-pkg6.0t64 on 24.04 and libapt-pkg6.0 on 22.04, so it is named by
    # what is installed.
    apt-get install -y --only-upgrade apt \
        $(dpkg-query -W -f='${Package}\n' 'libapt-pkg*'); \
    rm -rf /var/lib/apt/lists/*

# rclone from upstream rather than the distribution, so both engine images
# carry the same version (SPEC section 6.1).
# gosu, rclone from the gotools stage (design.md D-51). gosu replaces
# the base image's copy in place, so its entrypoint keeps calling the same path.
COPY --from=gotools /out/gosu /out/rclone /usr/local/bin/
RUN set -eux; gosu --version; gosu nobody true; rclone version | head -1

RUN set -eux; \
    case "$(uname -m)" in \
        x86_64)  s6_arch='x86_64' ;; \
        aarch64) s6_arch='aarch64' ;; \
    esac; \
    url="https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}"; \
    curl -fsSL -o /tmp/s6-noarch.tar.xz "${url}/s6-overlay-noarch.tar.xz"; \
    curl -fsSL -o /tmp/s6-arch.tar.xz   "${url}/s6-overlay-${s6_arch}.tar.xz"; \
    tar -C / -Jxpf /tmp/s6-noarch.tar.xz; \
    tar -C / -Jxpf /tmp/s6-arch.tar.xz; \
    rm -f /tmp/s6-noarch.tar.xz /tmp/s6-arch.tar.xz

COPY _shared/mysql-family/s6/ /etc/s6-overlay/s6-rc.d/
COPY _shared/mysql-family/user-bundles.d/ /etc/s6-overlay/user-bundles.d/
COPY _shared/mysql-family/scripts/ /usr/local/lib/mdb/

ENV MDB_ENGINE=mariadb

RUN set -eux; \
    chmod +x /usr/local/lib/mdb/*.sh; \
    chmod +x /etc/s6-overlay/s6-rc.d/mdb-engine/run; \
    mkdir -p /etc/mysql/conf.d /var/run/mysqld; \
    chown -R mysql:mysql /var/run/mysqld

ENV S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0 \
    S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_KEEP_ENV=1

HEALTHCHECK --interval=10s --timeout=5s --start-period=90s --retries=5 \
    CMD test ! -f /run/mdb/converge-failed && mariadb-admin ping -h localhost --protocol=socket || exit 1
# The marker test makes a timed-out convergence an UNHEALTHY container
# rather than a healthy-looking engine missing its pooler account --
# convergence itself cannot exit non-zero, because that halts client-mode
# containers (docker run <image> psql/mysql) where no server ever starts.

EXPOSE 3306

ENTRYPOINT ["/init"]
