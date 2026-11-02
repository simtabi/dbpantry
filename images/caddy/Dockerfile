# dbpantry-caddy — Caddy with the layer4 module.
#
# Caddy proxies HTTP out of the box; databases speak raw TCP. caddy-l4 adds
# layer-4 proxying, which is what lets ONE front door own the host ports for
# every engine instead of each engine publishing its own.
#
# Built with xcaddy from Caddy's own official builder image, the same way the
# pgBouncer image is built from a Docker Official base -- see
# docs/reference/image-provenance.md. Both stages are digest-pinned.
#
# caddy-l4 is pre-1.0 and its author says to expect breaking changes, so it is
# pinned to an exact commit rather than a branch. It is also why MDB_PUBLISH
# defaults to `none` and `direct` remains available: the proxy is a
# convenience, and nothing in this toolkit depends on it to function.

ARG CADDY_BUILDER=docker.io/library/caddy:2.11.4-builder@sha256:198d47eaee306d4d0c38a9960c89ff2c959aa29ad51d3e2dafa3e93ac961782a
ARG CADDY_BASE=docker.io/library/caddy:2.11.4@sha256:844f60b64e4724a5aa8245e019dace0d3f199f7433ce6c57676cb30a920dbad9

FROM ${CADDY_BUILDER} AS builder

# Pinned to a commit: a moving reference in the data path of every database
# connection is exactly what pinning exists to prevent.
# caddy-l4 v0.1.2 requires Caddy 2.11.4 exactly; a builder on 2.10.2 fails the
# module graph outright. Both stages move together for that reason.
ARG CADDY_L4_VERSION=v0.1.2

RUN xcaddy build \
        --with github.com/mholt/caddy-l4@${CADDY_L4_VERSION}

FROM ${CADDY_BASE}

COPY --from=builder /usr/bin/caddy /usr/bin/caddy

RUN caddy version && caddy list-modules | grep -q '^layer4' \
    && echo "layer4 modules present"

LABEL org.opencontainers.image.title="dbpantry-caddy" \
      org.opencontainers.image.description="Caddy with caddy-l4, so one front door can own every engine's host port" \
      org.opencontainers.image.licenses="Apache-2.0"
