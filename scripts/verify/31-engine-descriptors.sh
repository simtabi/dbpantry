#!/usr/bin/env bash
# verify: every engine descriptor is complete, consistent, and honestly declared
# tags: fast structure engines
# phase: 6

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=../engine-lib.sh
source "$MDB_ROOT/scripts/engine-lib.sh"

cd "$MDB_ROOT" || exit 1

# SPEC section 22.1: engines are declared, not hardcoded. This check is what
# makes that a rule rather than an intention — a descriptor missing a key would
# otherwise surface as a confusing runtime failure several layers away from the
# file that actually needs fixing.

engines="$(engine_list)"
[[ -n "$engines" ]] || vfail "no engine descriptors found in engines/"

seen_offsets=""
count=0
for e in $engines; do
    engine_load "$e" || vfail "could not load the descriptor for '$e'"
    rel="${MDB_ENGINE_CONF#"$MDB_ROOT"/}"

    for key in $MDB_ENGINE_REQUIRED_KEYS; do
        [[ -n "${!key:-}" ]] || vfail "$rel: required key $key is missing or empty"
    done

    # The default version must be one the engine actually supports, or every
    # consumer picks a version with no pinned base image.
    engine_supports_version "$MDB_ENGINE_DEFAULT_VERSION" \
        || vfail "$rel: default version $MDB_ENGINE_DEFAULT_VERSION is not in VERSIONS ($MDB_ENGINE_VERSIONS)"

    # Every supported version needs a digest-pinned base.
    for v in $MDB_ENGINE_VERSIONS; do
        awk -v e="$MDB_ENGINE_NAME" -v v="$v" '$1==e && $2==v {found=1} END {exit !found}' \
            images/bases.tsv \
            || vfail "$rel: version $v has no pinned base in images/bases.tsv"
    done

    # The paradigm vocabulary is mesobia's eight values (refactor plan N11,
    # design.md D-68): the same list as src/internal/engine/paradigm.go.
    # PARADIGM_ALSO names any further ones an engine serves (ClickHouse is
    # relational, also time-series), never the primary one again.
    for p in "$MDB_ENGINE_PARADIGM" ${MDB_ENGINE_PARADIGM_ALSO:-}; do
        case "$p" in
            relational|document|wide-column|key-value|graph|search|vector|time-series) ;;
            *) vfail "$rel: unknown paradigm '$p'" ;;
        esac
    done
    for p in ${MDB_ENGINE_PARADIGM_ALSO:-}; do
        [[ "$p" != "$MDB_ENGINE_PARADIGM" ]] \
            || vfail "$rel: PARADIGM_ALSO repeats the primary paradigm '$p'"
    done

    # Pooling must be one of the two honest answers. SPEC section 22.4 is
    # explicit that a uniform pooling abstraction would be a lie, so a
    # descriptor has to commit to external or driver.
    case "$MDB_ENGINE_POOLING" in
        external)
            [[ -n "${MDB_ENGINE_POOLER_IMAGE:-}" ]] \
                || vfail "$rel: pooling=external but no POOLER_IMAGE declared"
            [[ "$MDB_ENGINE_POOLER_IMAGE" == *"@sha256:"* ]] \
                || vinfo "note: $MDB_ENGINE_NAME pooler image is not digest-pinned yet"
            ;;
        driver)
            [[ -z "${MDB_ENGINE_POOLER_IMAGE:-}" ]] \
                || vfail "$rel: pooling=driver but a POOLER_IMAGE is declared; pick one"
            [[ -n "${MDB_ENGINE_POOLING_RATIONALE:-}" ]] \
                || vfail "$rel: pooling=driver must say WHY, so the docs can explain it"
            ;;
        *) vfail "$rel: pooling must be 'external' or 'driver', got '$MDB_ENGINE_POOLING'" ;;
    esac

    # Licensing is load-bearing for an OSS project that publishes images.
    # A non-OSI licence is allowed, but it must be labelled rather than
    # discovered by a user later.
    # Licensing decides what we are allowed to PUBLISH, not merely what to
    # document. Every artefact this repository publishes should be
    # OSI-licensed, so that a downstream user never has to reason about our
    # supply chain to know what they are running.
    #
    # A source-available engine is still fully supported — it is referenced
    # from its upstream image rather than rebuilt under our namespace, which
    # leaves the licence obligation where it belongs.
    case "${MDB_ENGINE_PUBLISH:-}" in
        derive|reference) ;;
        *) vfail "$rel: PUBLISH must be 'derive' or 'reference', got '${MDB_ENGINE_PUBLISH:-unset}'" ;;
    esac

    case "$MDB_ENGINE_OSI_APPROVED" in
        true) ;;
        false)
            [[ -n "${MDB_ENGINE_LICENSE_NOTE:-}" ]] \
                || vfail "$rel: OSI_APPROVED=false requires a LICENSE_NOTE explaining the implications"
            [[ "$MDB_ENGINE_PUBLISH" == "reference" ]] \
                || vfail "$rel: $MDB_ENGINE_LICENSE is not OSI-approved, so PUBLISH must be 'reference'.
       Publishing a derived image would distribute non-OSI binaries under an
       MIT project's namespace. Reference the upstream image instead."
            vinfo "$MDB_ENGINE_NAME: $MDB_ENGINE_LICENSE is not OSI-approved; referenced, not published"
            ;;
        *) vfail "$rel: OSI_APPROVED must be true or false, got '$MDB_ENGINE_OSI_APPROVED'" ;;
    esac

    # An engine that upstream ships without authentication must say so, because
    # the image is then obliged to correct it (SPEC section 22.3).
    if [[ "${MDB_ENGINE_AUTH_OFF_BY_DEFAULT_UPSTREAM:-false}" == "true" ]]; then
        vinfo "$MDB_ENGINE_NAME: upstream ships auth OFF; the image must enable it"
    fi

    # Port offsets must be unique, numeric, and land inside the port range.
    #
    # Two engines sharing an offset resolve to the same host port, and compose
    # reports that as "port is already allocated" against whichever service
    # loses the race -- naming neither descriptor. The offset is explicit
    # precisely so this is checkable.
    off="${MDB_ENGINE_PORT_OFFSET:-}"
    [[ -n "$off" ]] \
        || vfail "$rel: no MDB_ENGINE_PORT_OFFSET; publishing would have no port to derive"
    [[ "$off" =~ ^[0-9]+$ ]] \
        || vfail "$rel: MDB_ENGINE_PORT_OFFSET='$off' is not a number"
    # 54000 is the documented default base and 100 is the pooler's own step, so
    # anything past ~11000 can push a pooler port over 65535.
    (( off < 11000 )) \
        || vfail "$rel: MDB_ENGINE_PORT_OFFSET=$off is too large; base + offset must stay under 65535"

    case " $seen_offsets " in
        *" ${off}:"*)
            prior="${seen_offsets##*" ${off}:"}"; prior="${prior%% *}"
            vfail "$rel: MDB_ENGINE_PORT_OFFSET=$off is already used by '$prior'.
       Both would publish the same host port and one would fail to bind." ;;
        *) seen_offsets="$seen_offsets ${off}:${MDB_ENGINE_NAME}" ;;
    esac

    # Every engine gets a mounted overrides directory, so every engine must have
    # one in the repository. A bind mount whose host path does not exist is not
    # an error -- Docker creates it, owned by root -- so the engine starts, the
    # operator's config fragment silently cannot be written there, and nothing
    # ever says why. Cassandra and FerretDB shipped without theirs.
    [[ -d "$MDB_ROOT/overrides/$MDB_ENGINE_NAME" ]] \
        || vfail "overrides/$MDB_ENGINE_NAME/ is missing; the generated compose file mounts it"

    count=$(( count + 1 ))
    vinfo "$(printf '%-10s %-12s pooling=%-9s %s' \
        "$MDB_ENGINE_NAME" "$MDB_ENGINE_PARADIGM" "$MDB_ENGINE_POOLING" "$MDB_ENGINE_LICENSE")"
done

vinfo "$count engine descriptor(s) valid"

# Loading one engine must not leave another's values behind, or a consumer
# iterating over engines silently uses stale settings for the optional keys.
engine_load cassandra >/dev/null 2>&1 || true
heavy_after_cassandra="${MDB_ENGINE_HEAVY:-unset}"
engine_load pg >/dev/null 2>&1 || true
heavy_after_pg="${MDB_ENGINE_HEAVY:-unset}"

[[ "$heavy_after_cassandra" == "true" && "$heavy_after_pg" == "unset" ]] \
    || vfail "descriptor state leaks between loads (HEAVY was '$heavy_after_pg' after loading pg)"
vinfo "descriptor loads are isolated: no state leaks between engines"
