#!/usr/bin/env bash
# verify: no floating tags anywhere; every upstream image is digest-pinned
# tags: fast structure security
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# design.md D-05 is why this check exists rather than being a convention:
# nfrastack/db-backup:latest currently publishes ONLY linux/arm64, so an amd64
# runner following a floating tag fails outright. Pinning prevents it.
#
# Both patterns are anchored to the start of the line, because a Dockerfile
# FROM is an instruction and a compose `image:` is a YAML key -- neither occurs
# mid-line. Allowing anything before them read the SQL in
#     AUTH_QUERY: SELECT username, password FROM pgbouncer.get_auth($1)
# as an unpinned image reference. Anchoring is stricter about WHERE a reference
# may appear, not more permissive about what counts as pinned.

candidates=()
while IFS= read -r f; do
    [ -n "$f" ] && candidates+=("$f")
done < <(
    find . -type f \( -name 'Dockerfile*' -o -name 'docker-compose*.yml' -o -name 'compose*.yml' \) \
        -not -path './.git/*' | sort
)

# Dozens exist; a handful means the find broke, and passing then would
# report pinned versions it never read (2026-11-24 audit, G5).
(( ${#candidates[@]} >= 10 )) || vfail "found only ${#candidates[@]} Dockerfile(s) and compose file(s); the scan is broken"

violations=0

for f in "${candidates[@]}"; do
    # A FROM or image: referencing :latest, or carrying no tag at all.
    while IFS= read -r line; do
        printf '      %s: %s\n' "${f#./}" "$line" >&2
        (( violations++ )) || true
    done < <(grep -nE '^[[:space:]]*(FROM|image:)[[:space:]]+[^[:space:]]+:latest' "$f" || true)

    # Any FROM/image without an @sha256 digest.
    while IFS= read -r line; do
        case "$line" in
            *@sha256:*|*\$\{*|*scratch*) continue ;;
            # Images this repository builds have no digest until they are
            # published. The rule exists to pin what we consume, not what we
            # produce; their bases are pinned in images/bases.tsv, validated
            # below.
            *"/dbpantry-"*) continue ;;
        esac
        printf '      %s: not digest-pinned: %s\n' "${f#./}" "$line" >&2
        (( violations++ )) || true
    done < <(grep -nE '^[[:space:]]*(FROM|image:)[[:space:]]+[a-zA-Z0-9]' "$f" || true)
done

# A Dockerfile may write `FROM ${BASE_IMAGE}` because the base varies by engine
# major. That indirection is only legitimate if the thing it resolves to is
# itself pinned, so images/bases.tsv is validated as the real source of truth
# rather than treated as an exemption.
bases="$MDB_ROOT/images/bases.tsv"
if [[ -f "$bases" ]]; then
    entries=0
    while IFS= read -r line; do
        case "$line" in ''|'#'*) continue ;; esac
        ref="$(printf '%s' "$line" | awk '{print $3}')"
        [[ -n "$ref" ]] || continue
        entries=$(( entries + 1 ))
        case "$ref" in
            *@sha256:*) ;;
            *) printf '      bases.tsv: not digest-pinned: %s\n' "$ref" >&2
               violations=$(( violations + 1 )) ;;
        esac
        case "$ref" in
            *:latest*) printf '      bases.tsv: floating tag: %s\n' "$ref" >&2
                       violations=$(( violations + 1 )) ;;
        esac
    done < "$bases"
    vinfo "images/bases.tsv: $entries pinned base(s)"
else
    vfail "images/bases.tsv is missing; it is the single source of truth for base pins"
fi

# The gotools stage (D-51) reads GO_IMAGE. scripts/build passes the bases.tsv
# row, but a Dockerfile's own default is what a bare `docker build` (and the cli
# image) uses, so a default that drifted from the row would build those on a
# different toolchain than the one recorded. They must be identical.
go_row="$(awk '$1=="toolchain" && $2=="go" {print $3; exit}' "$bases" 2>/dev/null)"
go_users=0
while IFS= read -r df; do
    go_users=$(( go_users + 1 ))
    default="$(sed -nE 's/^ARG GO_IMAGE=(.*)$/\1/p' "$df" | head -1)"
    if [[ -z "$go_row" ]]; then
        printf '      %s: uses GO_IMAGE but images/bases.tsv has no "toolchain go" row\n' "${df#./}" >&2
        violations=$(( violations + 1 ))
    elif [[ "$default" != "$go_row" ]]; then
        printf '      %s: GO_IMAGE default %s differs from images/bases.tsv %s\n' "${df#./}" "${default:-<none>}" "$go_row" >&2
        violations=$(( violations + 1 ))
    fi
done < <(grep -rlE '^FROM .*\$\{GO_IMAGE\}' images --include=Dockerfile | sort)
vinfo "GO_IMAGE: $go_users Dockerfile(s) agree with images/bases.tsv"

# One Go version, stated once: src/go.mod's toolchain line is what the binary
# builds with, and the bases row is what the images' gotools stage builds gosu,
# rclone and postgres_exporter with. They drifted once: a standard-library fix
# (GO-2026-6859, net/http) reached the binary at go1.27.4 while the images
# stayed on golang:1.27.3, and nothing said so. So the row's image tag must
# name the same release as the module's toolchain.
mod_go="$(sed -nE 's/^toolchain go([0-9]+\.[0-9]+\.[0-9]+)$/\1/p' src/go.mod 2>/dev/null)"
row_go="$(printf '%s' "$go_row" | sed -nE 's#^.*/golang:([0-9]+\.[0-9]+\.[0-9]+)-.*$#\1#p')"
if [[ -z "$mod_go" ]]; then
    vfail "src/go.mod has no 'toolchain goX.Y.Z' line to compare the images' Go with"
elif [[ -z "$row_go" ]]; then
    vfail "images/bases.tsv 'toolchain go' row is not a golang:X.Y.Z-* image: ${go_row:-<none>}"
elif [[ "$mod_go" != "$row_go" ]]; then
    vfail "images/bases.tsv builds the images on Go $row_go but src/go.mod builds dbpantry on Go $mod_go; move both to the same release"
fi
vinfo "Go toolchain: src/go.mod and images/bases.tsv both name $mod_go"

(( violations == 0 )) || vfail "$violations unpinned image reference(s); CLAUDE.md requires pinned digests, no latest"

vinfo "checked ${#candidates[@]} file(s); all image references pinned"
