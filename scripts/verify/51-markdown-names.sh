#!/usr/bin/env bash
# verify: Markdown files use lowercase slug names, except git and GitHub conventional files
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# design.md D-61. Conventional names stay upper case because git, GitHub or an
# agent tool looks for them by name: README.md at any level, CHANGELOG.md,
# CONTRIBUTING.md, SECURITY.md, CODE_OF_CONDUCT.md, CLAUDE.md and the PR
# template. Everything else is a lowercase slug (deploy-guide.md). The same
# list as the owner's sim-standards pre-commit hook.

cd "$MDB_ROOT" || exit 1
git rev-parse --is-inside-work-tree >/dev/null 2>&1 \
    || vskip "not a git checkout; this check reads tracked file names"

bad=0
total=0
while IFS= read -r path; do
    [[ -n "$path" ]] || continue
    total=$(( total + 1 ))
    base="${path##*/}"
    case "$path" in
        .github/PULL_REQUEST_TEMPLATE.md|.github/ISSUE_TEMPLATE/*) continue ;;
    esac
    case "$base" in
        README.md|CHANGELOG.md|CONTRIBUTING.md|SECURITY.md|CODE_OF_CONDUCT.md) continue ;;
        LICENSE.md|AUTHORS.md|SUPPORT.md|CLAUDE.md|AGENTS.md|SKILL.md) continue ;;
    esac
    stem="${base%.md}"
    if ! [[ "$stem" =~ ^[a-z0-9]+(-[a-z0-9]+)*(\.[a-z0-9]+(-[a-z0-9]+)*)*$ ]]; then
        printf '      %s\n' "$path" >&2
        bad=$(( bad + 1 ))
    fi
    # A lowercase twin of a conventional name is the wrong case, not a slug.
    case "$base" in
        readme.md|changelog.md|contributing.md|security.md|code_of_conduct.md|claude.md)
            printf '      %s (conventional name, keep it upper case)\n' "$path" >&2
            bad=$(( bad + 1 )) ;;
    esac
done < <(git ls-files '*.md')

(( bad == 0 )) || vfail "$bad Markdown file name(s) break the naming rule (listed above)"
vinfo "$total Markdown file(s): conventional names upper case, the rest lowercase slugs"
