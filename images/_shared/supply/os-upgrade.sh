#!/bin/sh
# mdb-os-upgrade
#
# Apply every update the base image's DISTRIBUTION has published, and nothing
# from anyone else (design.md D-71).
#
# Every image is pinned to a base digest, and a digest is a snapshot: Debian,
# Ubuntu and Oracle Linux keep shipping security fixes after it was taken, and
# Trivy reports each of them as fixable. Upgrading them one package at a time
# (pcre2, then expat, then perl, then apt...) meant every new advisory failed
# the build until someone added another --only-upgrade line to every image it
# touched. This upgrades the whole distribution instead.
#
# The engine must not move. Third-party repositories -- PGDG, MariaDB's,
# Oracle's MySQL repos -- are left out of the upgrade, so the server the digest
# pins stays the server that runs:
#   apt      updates and upgrades against the distribution's own sources only
#            (debian.sources / ubuntu.sources / sources.list); a package from
#            another repository has no newer candidate there and stays put.
#   microdnf repositories matching OS_UPGRADE_DISABLE_REPOS are disabled.
#   apk      Alpine has no third-party repositories in these images.
#
# OS_UPGRADE_PROBE, when set, is a command whose output names the engine
# version. It runs before and after, and a difference fails the build.
set -eu

probe="${OS_UPGRADE_PROBE:-}"
before=""
[ -z "$probe" ] || before="$(sh -c "$probe" 2>&1)"

if command -v apt-get >/dev/null 2>&1; then
    parts="$(mktemp -d)"
    for f in /etc/apt/sources.list.d/debian.sources /etc/apt/sources.list.d/ubuntu.sources; do
        [ -f "$f" ] && cp "$f" "$parts/"
    done
    if [ -z "$(ls -A "$parts")" ] && ! grep -qE '^[[:space:]]*deb[[:space:]]' /etc/apt/sources.list 2>/dev/null; then
        echo "mdb-os-upgrade: no distribution apt sources found" >&2
        exit 1
    fi
    set -- -o "Dir::Etc::SourceParts=$parts" -o Dir::Etc::SourceList=/etc/apt/sources.list
    apt-get "$@" update
    DEBIAN_FRONTEND=noninteractive apt-get "$@" -y \
        -o Dpkg::Options::=--force-confold -o Dpkg::Options::=--force-confdef upgrade
    rm -rf "$parts" /var/lib/apt/lists/*
elif command -v microdnf >/dev/null 2>&1; then
    if [ -n "${OS_UPGRADE_DISABLE_REPOS:-}" ]; then
        microdnf update -y --disablerepo="$OS_UPGRADE_DISABLE_REPOS"
    else
        microdnf update -y
    fi
    microdnf clean all
elif command -v apk >/dev/null 2>&1; then
    apk upgrade --no-cache
else
    echo "mdb-os-upgrade: no supported package manager" >&2
    exit 1
fi

if [ -n "$probe" ]; then
    after="$(sh -c "$probe" 2>&1)"
    if [ "$before" != "$after" ]; then
        echo "mdb-os-upgrade: the OS upgrade moved the engine: '$before' -> '$after'" >&2
        exit 1
    fi
    echo "mdb-os-upgrade: engine unchanged ($after)" >&2
fi
