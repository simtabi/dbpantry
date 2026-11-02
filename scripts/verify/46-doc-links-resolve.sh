#!/usr/bin/env bash
# verify: every relative link in the Markdown docs points at a file that exists
# tags: fast structure docs
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# The docs were reorganised into guides/, runbooks/ and reference/ (design.md
# D-59), which rewrote some two hundred relative links at once. A link that
# points nowhere fails silently: GitHub renders it, and the reader finds out
# with a 404 at the moment they needed the page. A grep reads every link on
# every run; a human review does not.
#
# Only relative targets are checked. URLs, mailto: and same-page #anchors are
# out of scope, and an anchor on a relative link is stripped: the file has to
# exist, the heading is the reader's problem.

cd "$MDB_ROOT" || exit 1

docs=()
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    while IFS= read -r f; do
        [ -n "$f" ] && [ -f "$f" ] && docs+=("$f")
    done < <(git ls-files '*.md')
else
    while IFS= read -r f; do
        [ -n "$f" ] && docs+=("${f#./}")
    done < <(find . -name '*.md' -not -path './backups/*' -not -path './.git/*')
fi
(( ${#docs[@]} > 0 )) || vfail "found no Markdown files to check"

broken=0
links=0
for f in "${docs[@]}"; do
    dir="$(dirname "$f")"
    # Links inside fenced code blocks are examples, not links; drop the fences
    # before extracting. The awk prints only lines outside ``` blocks.
    while IFS= read -r target; do
        [ -n "$target" ] || continue
        case "$target" in
            http://*|https://*|mailto:*|\#*|\<*) continue ;;
        esac
        path="${target%%#*}"
        [ -n "$path" ] || continue
        links=$(( links + 1 ))
        if [ ! -e "$dir/$path" ]; then
            printf '      %s: %s\n' "$f" "$target" >&2
            broken=$(( broken + 1 ))
            continue
        fi
        # macOS filesystems ignore case, so design.md "exists" when only
        # DESIGN.md does, and the link breaks only on Linux and on GitHub.
        # Compare the last component against the real directory listing.
        base="$(basename "${path%/}")"
        case "$base" in .|..|'') continue ;; esac
        parent="$(dirname "$dir/${path%/}")"
        exact=0
        for entry in "$parent"/* "$parent"/.[!.]*; do
            if [ "${entry##*/}" = "$base" ]; then exact=1; break; fi
        done
        if (( ! exact )); then
            printf '      %s: %s (wrong case)\n' "$f" "$target" >&2
            broken=$(( broken + 1 ))
        fi
    done < <(awk '/^[[:space:]]*```/ {fence = !fence; next} !fence' "$f" \
                | grep -oE '\]\([^) ]+\)' | sed -E 's/^\]\(//; s/\)$//')
done

(( broken == 0 )) || vfail "$broken relative link(s) point at nothing (listed above)"
vinfo "$links relative link(s) across ${#docs[@]} Markdown file(s) all resolve"
