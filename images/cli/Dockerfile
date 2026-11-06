# dbpantry-cli — every client tool, no server.
#
# SPEC decision 12: CLI tools are not daemons, so the one-process-per-container
# rule does not apply to them. This is the one place a combined image is right,
# and it exists so the tools are usable with NO engine container running --
# inspecting a dump, restoring into a remote host, or opening a shell against
# a database that lives somewhere else entirely.
#
# Build context is images/, like every other image here.
ARG BASE_IMAGE=docker.io/library/debian:12-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241
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

# rclone: its vulnerable dependencies (grpc, x/net) are raised from
# _shared/gotools/floors.txt (design.md D-70). VersionSuffix is cleared so the
# binary reports v1.75.1 rather than a -DEV build.
ARG RCLONE_VERSION=1.75.1
RUN LDFLAGS="-X github.com/rclone/rclone/fs.VersionSuffix=" \
    build-go-tool rclone https://github.com/rclone/rclone "v${RCLONE_VERSION}" . /out/rclone

FROM ${BASE_IMAGE}

ARG PG_CLIENT_VERSION=17

LABEL org.opencontainers.image.title="dbpantry-cli" \
      org.opencontainers.image.description="PostgreSQL, MySQL and MariaDB clients, dump/restore tools, and the dbpantry scripts" \
      org.opencontainers.image.vendor="Simtabi LLC" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/simtabi/dbpantry"

ENV DEBIAN_FRONTEND=noninteractive

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        gnupg \
        jq \
        less \
        openssl \
        procps \
        unzip \
        zstd; \
    rm -rf /var/lib/apt/lists/*

# PostgreSQL client from PGDG so its major matches the engines this toolkit
# runs, rather than whatever Debian happens to ship.
RUN set -eux; \
    . /etc/os-release; \
    install -d /usr/share/postgresql-common/pgdg; \
    curl -fsSo /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc \
        https://www.postgresql.org/media/keys/ACCC4CF8.asc; \
    echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt ${VERSION_CODENAME}-pgdg main" \
        > /etc/apt/sources.list.d/pgdg.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends "postgresql-client-${PG_CLIENT_VERSION}"; \
    rm -rf /var/lib/apt/lists/*

# MySQL-family clients. Debian's mariadb-client also provides the `mysql` and
# `mysqldump` names, so both families work from one package.
#
# Worth knowing: those names are MariaDB's implementations. For INTERACTIVE use
# against MySQL that is fine, but scheduled dumps deliberately do not run from
# here -- each backup sidecar runs on its own engine's image so it uses that
# engine's native dump tool (see docker-compose.yml).
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends mariadb-client; \
    rm -rf /var/lib/apt/lists/*

# rclone for S3-compatible targets, matching the engine images' version.
# rclone from the gotools stage (design.md D-51).
COPY --from=gotools /out/rclone /usr/local/bin/
RUN set -eux; rclone version | head -1

# The toolkit scripts, so backup/restore/new-project are available here too.
COPY --chmod=0755 cli/entrypoint.sh /usr/local/bin/mdb-entrypoint

# No server runs here, so there is no engine user to drop to and nothing
# listening. Running as a non-root user keeps a mounted host directory from
# being written as root.
RUN set -eux; \
    useradd --create-home --uid 1000 --shell /bin/bash mdb
USER mdb
WORKDIR /work

ENTRYPOINT ["/usr/local/bin/mdb-entrypoint"]
CMD ["bash"]
