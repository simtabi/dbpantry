#!/usr/bin/env bash
# verify: every path a compose file mounts or builds from exists or is generated
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# A bind mount whose source is missing does not fail loudly. Docker creates an
# empty DIRECTORY in its place, so a config file that moved (config/ grouping,
# DESIGN.md D-59) turns into a container reading an empty directory where its
# Caddyfile or haproxy.cfg should be, and the error surfaces three layers away.
# This reads every ./ source and every build context and asks the filesystem.
#
# Some sources are created at runtime and are legitimately absent from a fresh
# clone. Each is listed with the command that makes it, and for the rendered
# ones the template it is rendered from must exist instead.

cd "$MDB_ROOT" || exit 1

# The generated compose files are part of what is checked, so make sure they
# reflect the descriptors on disk now rather than whenever they were last made.
scripts/gen-compose >/dev/null 2>&1 || vfail "scripts/gen-compose failed"

# runtime path -> what must exist instead (empty: created by make init / make up)
generated_stand_in() {
    case "$1" in
        .env)                          printf '.env.example' ;;
        secrets|secrets/*)             printf 'secrets' ;;
        certs|certs/*)                 printf 'certs' ;;
        backups)                       printf 'backups' ;;
        config/caddy/Caddyfile)        printf 'config/caddy/Caddyfile.tmpl' ;;
        config/caddy/servers.json)     printf 'config/caddy/servers.json.tmpl' ;;
        config/caddy/layer4.caddy)     printf 'scripts/gen-compose' ;;
        *)                             return 1 ;;
    esac
}

files=()
for f in docker-compose.yml compose.*.yml; do
    [ -f "$f" ] && files+=("$f")
done
(( ${#files[@]} > 0 )) || vfail "no compose files found"

missing=0
checked=0
for f in "${files[@]}"; do
    # Short-form bind mounts (- ./src:/dst[:ro]) and long-form secret files
    # (file: ./src). Build contexts are handled below with their dockerfile.
    while IFS= read -r src; do
        src="${src#./}"
        checked=$(( checked + 1 ))
        [ -e "$src" ] && continue
        if stand_in="$(generated_stand_in "$src")"; then
            [ -e "$stand_in" ] && continue
            printf '      %s: %s is generated, but %s is missing\n' "$f" "$src" "$stand_in" >&2
        else
            printf '      %s: mount source %s does not exist\n' "$f" "$src" >&2
        fi
        missing=$(( missing + 1 ))
    done < <(grep -hE '^[[:space:]]*-[[:space:]]+"?\./[^:"]+:|^[[:space:]]*file:[[:space:]]+\./' "$f" \
                | sed -E 's/^[[:space:]]*(-|file:)[[:space:]]+"?//; s/[:"].*$//')

    # context: ./dir followed by dockerfile: rel/path, resolved against the context.
    ctx=""
    while IFS= read -r line; do
        case "$line" in
            *context:*)
                ctx="$(printf '%s' "$line" | sed -E 's/^[[:space:]]*context:[[:space:]]*//; s/^\.\///')"
                checked=$(( checked + 1 ))
                if [ ! -d "$ctx" ]; then
                    printf '      %s: build context %s does not exist\n' "$f" "$ctx" >&2
                    missing=$(( missing + 1 ))
                fi
                ;;
            *dockerfile:*)
                df="$(printf '%s' "$line" | sed -E 's/^[[:space:]]*dockerfile:[[:space:]]*//')"
                checked=$(( checked + 1 ))
                if [ ! -f "${ctx:-.}/$df" ]; then
                    printf '      %s: dockerfile %s/%s does not exist\n' "$f" "${ctx:-.}" "$df" >&2
                    missing=$(( missing + 1 ))
                fi
                ;;
        esac
    done < <(grep -E '^[[:space:]]*(context|dockerfile):' "$f")
done

# Descriptors name repo files that gen-compose mounts; check them at the source
# too, so a descriptor for a profile nobody has generated yet is still covered.
for d in engines/*/engine.conf; do
    while IFS= read -r p; do
        [ -n "$p" ] || continue
        checked=$(( checked + 1 ))
        if [ ! -f "$p" ]; then
            printf '      %s: MDB_ENGINE_POOLER_BOOTSTRAP=%s does not exist\n' "$d" "$p" >&2
            missing=$(( missing + 1 ))
        fi
    done < <(sed -nE "s/^MDB_ENGINE_POOLER_BOOTSTRAP=['\"]?([^'\"]+)['\"]?$/\1/p" "$d")
done

(( missing == 0 )) || vfail "$missing compose path(s) point at nothing (listed above)"
vinfo "$checked mount source(s), build context(s) and dockerfile(s) across ${#files[@]} compose file(s) all exist"
