#!/usr/bin/env bash
# verify: every image comes from upstream, not a third-party rebuild
# tags: fast security structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# "Use official images wherever possible" is easy to agree with and easy to
# erode: one convenient third-party rebuild at a time, each defensible on its
# own. A third party is an organisation that can be compromised independently
# of the project it packages, and whose update cadence you do not control --
# which is the same argument that pins every reference by digest.
#
# So the allowlist is namespaces, not individual images: a Docker Official
# Image (no namespace at all) or the upstream project's own namespace. Anything
# else has to be justified in docs/reference/image-provenance.md, by name.

need_file "$MDB_ROOT/docs/reference/image-provenance.md"

# Namespaces belonging to the projects whose software they package.
UPSTREAM='^(pgvector/pgvector|dpage/pgadmin4|proxysql/proxysql|prom/[a-z-]+|aquasec/trivy|ghcr\.io/ferretdb/[a-z-]+|quay\.io/coreos/etcd|quay\.io/prometheuscommunity/[a-z-]+|ghcr\.io/simtabi/[a-z-]+)$'

violations=0
checked=0

while IFS= read -r ref; do
    [[ -z "$ref" ]] && continue
    # Strip the tag and digest; what matters is who publishes it.
    repo="${ref%%@*}"; repo="${repo%:*}"
    # Compose variables are not image names. The pattern matches literally.
    # shellcheck disable=SC2016
    case "$repo" in ''|\$*|*'${'*) continue ;; esac

    checked=$(( checked + 1 ))

    # References are fully qualified so Podman resolves them without a search
    # list (design.md D-60). docker.io/library/x is the Official Image x, and
    # docker.io/ns/x is ns/x; judge the publisher, not the spelling.
    repo="${repo#docker.io/library/}"; repo="${repo#docker.io/}"

    # A Docker Official Image has no namespace: "mysql", not "someone/mysql".
    case "$repo" in
        */*) ;;
        *) continue ;;
    esac

    printf '%s' "$repo" | grep -qE "$UPSTREAM" && continue

    # Everything else must be named in the provenance document, so an exception
    # is a decision someone wrote down rather than a line nobody noticed.
    if grep -qF "$repo" "$MDB_ROOT/docs/reference/image-provenance.md"; then
        vinfo "note: $repo is not upstream but is justified in docs/reference/image-provenance.md"
        continue
    fi

    printf '      %s is a third-party rebuild and is not justified in docs/reference/image-provenance.md\n' "$repo" >&2
    violations=$(( violations + 1 ))
done < <(
    grep -rhoE '(^|[[:space:]])image:[[:space:]]*[A-Za-z0-9][A-Za-z0-9./_-]*(:[A-Za-z0-9._-]+)?(@sha256:[a-f0-9]+)?' \
        "$MDB_ROOT"/docker-compose.yml "$MDB_ROOT"/compose.*.yml 2>/dev/null \
        | sed -E 's/.*image:[[:space:]]*//'
    grep -hoE '^[a-z0-9]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+' "$MDB_ROOT/images/bases.tsv" 2>/dev/null \
        | awk '{print $3}'
    grep -hoE "_IMAGE='[^']+'" "$MDB_ROOT"/engines/*/engine.conf 2>/dev/null \
        | sed -E "s/.*='//; s/'$//"
)

(( violations == 0 )) || vfail "$violations third-party image(s) with no justification"
(( checked > 0 )) || vfail "no image references were found; this check is not looking at anything"

vinfo "$checked image reference(s): all official, upstream, or justified"
