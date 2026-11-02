#!/usr/bin/env bash
# verify: no credential is present in any built image layer or history
# tags: security
# phase: 5

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 18: "repo/image-history grep finds no credential."
# A secret passed as a build ARG survives in the image history even when the
# final filesystem is clean, which is exactly the case a filesystem-only scan
# would miss.

checked=0
for engine in pg mysql mariadb cli; do
    img="$(image_name "$engine")"
    image_exists "$img" || vfail "image not built yet: $img"

    history="$(mdb_ctr history --no-trunc --format '{{.CreatedBy}}' "$img" 2>/dev/null || true)"
    [[ -n "$history" ]] || vfail "could not read image history for $img"

    while IFS= read -r pattern; do
        if printf '%s' "$history" | grep -qiE "$pattern"; then
            printf '%s' "$history" | grep -iE "$pattern" | head -3 >&2
            vfail "$img: image history matches credential pattern /$pattern/"
        fi
    done <<'PATTERNS'
(password|passwd|secret)[[:space:]]*=[[:space:]]*[^$"'{[:space:]]{4,}
BEGIN [A-Z ]*PRIVATE KEY
aws_secret_access_key
PATTERNS

    # Env baked into the image must not carry a literal password.
    env_vars="$(mdb_ctr image inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$img" 2>/dev/null || true)"
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        case "$line" in
            *PASSWORD=|*PASSWORD_FILE=*|*SECRET=|*=) continue ;;
            *PASSWORD=*|*SECRET=*)
                vfail "$img: baked env carries a literal credential: ${line%%=*}=<redacted>" ;;
        esac
    done <<< "$env_vars"

    (( checked++ )) || true
done

vinfo "$checked image(s): history and baked env are credential-free"
