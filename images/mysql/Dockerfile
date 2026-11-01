# dbpantry-mysql — MySQL with baked configuration, supervised by s6.
#
# Build context is images/, not images/mysql/, so this can share the init
# scripts and s6 tree with the MariaDB image (see images/_shared/mysql-family).
#
# BASE_IMAGE is passed digest-pinned from images/bases.tsv.
ARG BASE_IMAGE=mysql:8.4.11@sha256:b3b90af2a6552ae30c266fdb7d5dd55f3afb72404bb78d37fe8a23eb857fd3fb
# The Go toolchain the gotools stage builds with; pinned in images/bases.tsv
# (row `toolchain go`) and passed by scripts/build, like BASE_IMAGE.
ARG GO_IMAGE=golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61

# =============================================================================
# Go tools, rebuilt from their upstream release tags on a patched toolchain
# (DESIGN.md D-51). The released binaries carry the Go standard library of the
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

ARG ENGINE_VERSION=8.4
ARG S6_OVERLAY_VERSION=3.2.3.2

LABEL org.opencontainers.image.title="dbpantry-mysql" \
      org.opencontainers.image.description="MySQL with dbpantry configuration and tooling, supervised by s6-overlay" \
      org.opencontainers.image.vendor="Simtabi LLC" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/simtabi/dbpantry"

USER root

# --- patch the OS layer --------------------------------------------------------
# The 8.0 image line is no longer rebuilt on the cadence 8.4 and 9.7 are --
# 8.0.46 dates from 2026-05-05 where the others were refreshed 2026-07-28 -- so
# it ships Oracle Linux 9.7 packages whose fixes landed in 9.8. That was 47 HIGH
# findings before the repin to 8.0.46 and 20 after, all in glib2, gnutls,
# krb5-libs, libacl and libcap. They are fixable, so they get fixed; waiving
# what a package manager can patch is what turns an ignore file into a dumping
# ground.
#
# Scoping the update away from MySQL itself is the load-bearing part: otherwise
# it is free to pull a newer mysql-community-server-minimal from Oracle's repo,
# and the image stops being the digest it pins -- an 8.0.46 pin quietly serving
# some later 8.0.
#
# That scoping is --disablerepo, NOT --exclude. microdnf has no --exclude at all
# (rpm-software-management/microdnf#119, still open) and fails with "Unknown
# option", the same way it does for dnf's --noautoremove above. It does have
# --disablerepo, and the image keeps MySQL in its own repos -- mysqlN.N-server-
# minimal and mysql-tools-community -- so disabling `mysql*` leaves the update
# with only the Oracle Linux repos to draw from. Verified against 8.0.46:
# glib2 moves 2.68.4-18.el9_7.1 to 2.68.4-19.el9_8.2 and mysqld does not move.
#
# The assertion is still here rather than trusting that reasoning.
RUN set -eux; \
    before="$(mysqld --version)"; \
    microdnf update -y --disablerepo='mysql*'; \
    microdnf clean all; \
    after="$(mysqld --version)"; \
    [ "$before" = "$after" ] || { \
        echo "OS update moved the server: '$before' -> '$after'" >&2; exit 1; }

# Oracle Linux 9, so microdnf. The base already carries tar, xz, zstd, curl,
# gosu and openssl; procps-ng and unzip are what it lacks (DESIGN.md D-17).
RUN set -eux; \
    microdnf install -y procps-ng unzip findutils; \
    microdnf clean all

# --- drop MySQL Shell ----------------------------------------------------------
# Oracle bundles mysql-shell in the official image, and with it a private Python
# 3.13 under /usr/lib/mysqlsh/lib carrying its own vendored site-packages. Those
# packages -- not the database -- are the only HIGH findings trivy reports
# against this image: cryptography 46.0.5 and pyOpenSSL 25.3.0, six of them,
# all with fixes upstream that Oracle has not yet shipped.
#
# Nothing in this toolkit invokes mysqlsh. Provisioning, backup, restore and PITR
# all go through the `mysql` client and mysqlbinlog. So this is a component that
# is not used, cannot be patched here, and is the entire vulnerability surface --
# removing it is the fix, and waiving the CVEs would have been the excuse.
#
# It goes out through `rpm -e`, not `microdnf remove`. mysql-shell shares
# dependencies with the server -- openssl-libs among them -- and microdnf
# garbage-collects those, taking mysqld with it. dnf spells the opt-out
# --noautoremove; microdnf, which is what this Oracle Linux 9 base ships, does
# not have that option at all and fails with "Unknown option". `rpm -e` removes
# exactly the package named and nothing else, and still refuses if anything
# depends on it -- so a future base where something does would fail here loudly
# rather than lose it silently.
RUN set -eux; \
    if rpm -q mysql-shell >/dev/null 2>&1; then \
        rpm -e mysql-shell; \
    fi; \
    # rpm -e removes the files the package OWNS and leaves everything else, and
    # /usr/lib/mysqlsh survives it -- the bundled Python writes .pyc files and
    # dist-info that no package claims, so the tree that trivy read is exactly
    # the part rpm does not delete. The assertion below caught that; the rm is
    # what acts on it. Safe unconditionally: the package is already gone, so
    # anything still here is orphaned by definition.
    rm -rf /usr/lib/mysqlsh; \
    # Verify: the tree is gone AND the server it shared libraries with survives.
    [ ! -d /usr/lib/mysqlsh ]; \
    mysqld --version

# --- mysqlbinlog, for point-in-time recovery -----------------------------------
# The official image is mysql-community-server-minimal on Oracle Linux 9 and
# ships no mysqlbinlog, so a binary log could be written and never replayed --
# an audit trail rather than a recovery path.
#
# Installing mysql-community-client does not work: it owns /usr/bin/mysql,
# mysqladmin and mysqldump too, and rpm refuses the file conflicts with
# server-minimal. So ONE FILE is extracted from that RPM instead. No package is
# installed, nothing conflicts, and nothing else from the client package ends
# up in the image.
#
# The version is read from the INSTALLED server rather than pinned here, because
# mysqlbinlog must be able to read what this exact server writes. A pinned
# version would silently drift the first time the base image bumped.
#
# The repo SERIES is derived from it too. Hardcoding mysql-8.4-community built
# fine for 8.4 and 404'd for 8.0 and 9.7 -- every other version on the menu --
# which only showed up once CI built the whole matrix rather than one default.
RUN set -eux; \
    arch="$(uname -m)"; \
    ver="$(rpm -q --qf '%{VERSION}-%{RELEASE}' mysql-community-server-minimal)"; \
    microdnf install -y cpio; \
    series="$(printf '%s' "$ver" | cut -d. -f1,2)"; \
    curl -fsSL -o /tmp/mysql-client.rpm \
        "https://repo.mysql.com/yum/mysql-${series}-community/el/9/${arch}/mysql-community-client-${ver}.${arch}.rpm"; \
    cd /tmp; \
    rpm2cpio /tmp/mysql-client.rpm | cpio -idm ./usr/bin/mysqlbinlog; \
    install -m 0755 /tmp/usr/bin/mysqlbinlog /usr/local/bin/mysqlbinlog; \
    rm -rf /tmp/mysql-client.rpm /tmp/usr; \
    microdnf remove -y cpio; \
    microdnf clean all; \
    mysqlbinlog --version

# rclone is not packaged for Oracle Linux, so it comes from upstream. It is the
# S3 push tool SPEC section 6.1 requires baked into every engine image.
# gosu, rclone from the gotools stage (DESIGN.md D-51). gosu replaces
# the base image's copy in place, so its entrypoint keeps calling the same path.
COPY --from=gotools /out/gosu /out/rclone /usr/local/bin/
RUN set -eux; gosu --version; gosu nobody true; rclone version | head -1

# s6-overlay v3 as PID 1.
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
# user-bundles.d, not s6-rc.d/user: the legacy location makes s6 rewrite files
# under /etc at boot, which fails on a read-only root filesystem (D-28).
COPY _shared/mysql-family/user-bundles.d/ /etc/s6-overlay/user-bundles.d/
COPY _shared/mysql-family/scripts/ /usr/local/lib/mdb/

# Selects the engine-specific half of the shared scripts: daemon name, client
# binary, and the [mysqld] vs [mariadbd] config section.
ENV MDB_ENGINE=mysql

RUN set -eux; \
    chmod +x /usr/local/lib/mdb/*.sh; \
    chmod +x /etc/s6-overlay/s6-rc.d/mdb-engine/run; \
    mkdir -p /etc/mysql/conf.d /var/run/mysqld; \
    chown -R mysql:mysql /var/run/mysqld

# Gracetimes stay at the s6 defaults; raising them only makes the container
# idle after everything is already down (DESIGN.md D-27).
ENV S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0 \
    S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_KEEP_ENV=1

HEALTHCHECK --interval=10s --timeout=5s --start-period=90s --retries=5 \
    CMD test ! -f /run/mdb/converge-failed && mysqladmin ping -h localhost --protocol=socket || exit 1
# The marker test makes a timed-out convergence an UNHEALTHY container
# rather than a healthy-looking engine missing its pooler account --
# convergence itself cannot exit non-zero, because that halts client-mode
# containers (docker run <image> psql/mysql) where no server ever starts.

EXPOSE 3306

ENTRYPOINT ["/init"]
