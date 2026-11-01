#!/usr/bin/env bash
# verify: no credential is committed anywhere in the repository
# tags: fast security
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# SPEC section 18: "repo/image-history grep finds no credential".
# Passwords travel by the _FILE convention only, so an assignment with a
# literal value on the right-hand side is a finding by definition.
#
# Two refinements, both learned by this check firing on itself in phase 1:
#
#  1. The key must END with a secret-ish word. MDB_MYSQL_NATIVE_PASSWORD_COMPAT
#     is a boolean toggle, not a credential, and matching "contains PASSWORD"
#     flagged it.
#
#  2. The harness needs throwaway passwords for containers that live for
#     seconds. Those carry the mandatory `mdb-throwaway-` prefix, which is
#     exempt HERE ONLY. Deliberately a value convention rather than a path
#     exemption: exempting scripts/verify/ wholesale would create somewhere a
#     real credential could hide, whereas a real credential will never carry
#     this prefix.
#
#  3. The value may name WHERE the secret lives rather than being one, which is
#     the whole point of the _FILE convention. `MDB_ENGINE_ROOT_SECRET` holds
#     a secret's filename and `SECRET=/run/secrets/...` holds its path. Both are
#     indirection, the same class as ${...} and $(...) below.
#
#     Kept narrow on purpose. An absolute path is unambiguous, and a bare
#     filename is exempt only when it matches this repo's secret-file naming
#     convention (`<thing>_password.txt`), so a literal credential that merely
#     happens to end in .txt is still a finding. Passwords do contain slashes,
#     so a *relative* path is not exempt.

files=()
if [[ -d .git ]]; then
    while IFS= read -r f; do [ -n "$f" ] && files+=("$f"); done < <(git ls-files)
else
    while IFS= read -r f; do [ -n "$f" ] && files+=("$f"); done \
        < <(find . -type f -not -path './.git/*' | sed 's|^\./||')
fi

violations=0
report() { printf '      %s\n' "$*" >&2; violations=$(( violations + 1 )); }

# Keys ending in one of these are credential-bearing.
SECRET_SUFFIX='(PASSWORD|PASSWD|SECRET|PASSPHRASE|ACCESS_KEY|PRIVATE_KEY|KEY_SECRET)'

for f in "${files[@]}"; do
    [[ -f "$f" ]] || continue
    case "$f" in
        # Prose that quotes variable names is documentation, not configuration.
        docs/SPEC.md|docs/KIT.md|DESIGN.md|docs/archive/KICKOFF-PROMPT.txt|CHANGELOG.md) continue ;;
        scripts/verify/03-secret-scan-repo.sh) continue ;;
    esac

    while IFS= read -r assignment; do
        # The match may carry the leading delimiter; drop it.
        assignment="${assignment#"${assignment%%[![:space:]]*}"}"
        key="${assignment%%=*}"
        value="${assignment#*=}"

        # Only identifiers; skips glob patterns like *PASSWORD= in case blocks.
        printf '%s' "$key" | grep -qE '^[A-Za-z_][A-Za-z0-9_]*$' || continue
        # Only keys that END with a secret word.
        printf '%s' "$key" | grep -qE "${SECRET_SUFFIX}\$" || continue

        [[ -z "$value" ]] && continue

        case "$value" in
            mdb-throwaway-*) continue ;;              # harness fixtures, see above
            *\$\{*|*\$\(*|__FILE__*|secrets/*) continue ;;  # indirection, not a literal
            # Names where the secret lives; see refinement 3 above.
            /*) continue ;;
            *_password.txt|*_secret.txt|*_passphrase.txt) continue ;;
            CHANGE_ME*) continue ;;                     # sentinel; check-env rejects it
            # The value charset above stops at '(' so that shell case patterns
            # are not mistaken for assignments. That truncates a command
            # substitution such as $(cat file) down to a bare '$', which is an
            # expansion, never a credential.
            '$') continue ;;
        esac

        report "$f: $key=<redacted literal>"
        # A real credential contains no shell glob or list metacharacters; a
        # case pattern such as *PASSWORD=|*SECRET=) does, which is how the two
        # are told apart.
    done < <(grep -ohE '(^|[[:space:]])[A-Za-z_][A-Za-z0-9_]*=[^[:space:]"'"'"'\\|)(*;&]+' "$f" 2>/dev/null || true)

    if grep -qE 'BEGIN (RSA |EC |OPENSSH |PGP )?PRIVATE KEY' "$f" 2>/dev/null; then
        report "$f: contains private key material"
    fi
done

# certs/ and secrets/ must be ignored, not tracked.
if [[ -d .git ]]; then
    for d in certs secrets; do
        while IFS= read -r tracked; do
            [[ -z "$tracked" ]] && continue
            [[ "$(basename "$tracked")" == ".gitkeep" ]] && continue
            report "$tracked is tracked; $d/ contents must stay gitignored"
        done < <(git ls-files "$d" 2>/dev/null || true)
    done
fi

(( violations == 0 )) || vfail "$violations potential credential(s) committed"

vinfo "scanned ${#files[@]} tracked file(s); no credentials found"
