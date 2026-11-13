#!/usr/bin/env bash
# verify: every image reference names its registry, so every runtime resolves it the same way
# tags: fast structure runtime
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Docker silently expands "mysql:8.4" to docker.io/library/mysql:8.4. Podman
# does not: it consults registries.conf, and with short-name-mode=enforcing and
# no unqualified-search-registries (the Podman machine default) a short name
# that is not in its alias table -- pgvector/pgvector, proxysql/proxysql --
# fails to pull, or prompts for a registry and then fails without a TTY. A
# fully-qualified name means the same image on Docker, Podman and nerdctl
# (design.md D-60).

cd "$MDB_ROOT" || exit 1
scripts/gen-compose >/dev/null 2>&1 || vfail "scripts/gen-compose failed"

bad=0
checked=0
judge() {
    local where="$1" ref="$2" first
    # Compose variables are resolved later; their DEFAULTS are checked as refs.
    # shellcheck disable=SC2016
    case "$ref" in ''|\$*) return 0 ;; esac
    checked=$(( checked + 1 ))
    first="${ref%%/*}"
    # A registry host has a dot or a port, or is localhost; with no slash at
    # all the reference is a bare Official Image name.
    if [[ "$ref" != */* ]] || { [[ "$first" != *.* && "$first" != *:* && "$first" != localhost ]]; }; then
        printf '      %s: %s is not fully qualified (add docker.io/ or docker.io/library/)\n' "$where" "$ref" >&2
        bad=$(( bad + 1 ))
    fi
}

# image: lines, including the defaults inside ${VAR:-default}.
while IFS=: read -r file _ rest; do
    ref="$(printf '%s' "$rest" | sed -E 's/^[[:space:]]*image:[[:space:]]*//; s/^"//; s/"$//')"
    # Every ${VAR:-default} is replaced by its default, innermost first, so a
    # nested ${MDB_X_IMAGE:-${MDB_X_BASE_IMAGE:-docker.io/...}} and a prefixed
    # ${MDB_IMAGE_PREFIX:-localhost}/dbpantry-pg:${MDB_PG_VERSION:-17} are both
    # judged by the name they resolve to when nothing is set.
    prev=""
    while [[ "$ref" != "$prev" ]]; do
        prev="$ref"
        ref="$(printf '%s' "$ref" | sed -E 's/\$\{[A-Za-z_][A-Za-z0-9_]*:-([^${}]*)\}/\1/g')"
    done
    judge "$file" "$ref"
done < <(grep -nE '^[[:space:]]*image:' docker-compose.yml compose.*.yml 2>/dev/null)

# BASE_IMAGE build-arg defaults in compose files.
while IFS=: read -r file _ rest; do
    ref="$(printf '%s' "$rest" | sed -E 's/.*:-//; s/\}+[[:space:]]*$//')"
    judge "$file" "$ref"
done < <(grep -nE 'BASE_IMAGE:[[:space:]]*\$\{[A-Z_]+:-' docker-compose.yml compose.*.yml 2>/dev/null)

# images/bases.tsv, the single source of truth for upstream bases.
while IFS=$'\t' read -r e v ref; do
    case "$e" in ''|'#'*) continue ;; esac
    judge "images/bases.tsv ($e $v)" "$ref"
done < images/bases.tsv

# ARG defaults that are FROM'd: *_IMAGE, *_BASE and *_BUILDER.
while IFS=: read -r file _ rest; do
    ref="${rest#*=}"
    judge "$file" "$ref"
done < <(grep -nE '^ARG [A-Z_]*(IMAGE|BASE|BUILDER)=' images/*/Dockerfile 2>/dev/null | grep -v 'IMAGE_PREFIX')

# Engine descriptors.
while IFS= read -r line; do
    file="${line%%:*}"; ref="$(printf '%s' "$line" | sed -E "s/.*_IMAGE='//; s/'$//")"
    judge "$file" "$ref"
done < <(grep -HoE "_IMAGE='[^']+'" engines/*/engine.conf 2>/dev/null)

(( checked > 0 )) || vfail "no image references found; this check is not looking at anything"
(( bad == 0 )) || vfail "$bad image reference(s) would not resolve on Podman (listed above)"
vinfo "$checked image reference(s), all fully qualified"
