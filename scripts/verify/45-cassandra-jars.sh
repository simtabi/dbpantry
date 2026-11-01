#!/usr/bin/env bash
# verify: Cassandra's replaced JARs are pinned by checksum and actually installed
# tags: fast structure security
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# images/cassandra/jars.tsv swaps vulnerable JARs in Cassandra's lib/ for fixed
# releases from Maven Central (DESIGN.md D-55). A download is only as safe as
# the checksum that pins it, so this fails on any row without a full sha256,
# on a series images/bases.tsv does not build, and on a Dockerfile that no
# longer runs the swap -- the case where the manifest still looks like
# coverage but nothing reads it.

cd "$MDB_ROOT" || exit 1

manifest="images/cassandra/jars.tsv"
dockerfile="images/cassandra/Dockerfile"
need_file "$MDB_ROOT/$manifest"
need_file "$MDB_ROOT/images/cassandra/replace-jars.sh"

series_built="$(awk -F'\t' '$1 == "cassandra" { print $2 }' images/bases.tsv)"
[[ -n "$series_built" ]] || vfail "images/bases.tsv has no cassandra rows"

errors=""
rows=0
seen=""

# The || clause reads a last row that has no trailing newline.
while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    rows=$(( rows + 1 ))
    IFS=$'\t' read -r s old coord sha extra <<< "$line"

    [[ -z "${extra:-}" && -n "${sha:-}" ]] \
        || { errors+="        not four tab-separated columns: $line"$'\n'; continue; }
    grep -qxF -- "$s" <<< "$series_built" \
        || errors+="        $coord: series $s is not a cassandra row in images/bases.tsv"$'\n'
    [[ "$old" == "-" || "$old" == *.jar ]] \
        || errors+="        $coord: replaces must be a .jar file name or -, not '$old'"$'\n'
    [[ "$coord" =~ ^[A-Za-z0-9_.-]+:[A-Za-z0-9_.-]+:[A-Za-z0-9_.-]+(:[A-Za-z0-9_.-]+)?$ ]] \
        || errors+="        not a group:artifact:version[:classifier] coordinate: $coord"$'\n'
    [[ "$sha" =~ ^[0-9a-f]{64}$ ]] \
        || errors+="        $coord: sha256 must be 64 lowercase hex characters"$'\n'

    # Two rows installing the same file, or removing the same one, mean one of
    # them is dead and the reader cannot tell which.
    for key in "$s new $coord" "$s old $old"; do
        [[ "$key" == *" old -" ]] && continue
        grep -qxF -- "$key" <<< "$seen" && errors+="        duplicate row in series $s: ${key#* * }"$'\n'
        seen+="$key"$'\n'
    done
done < "$manifest"

while IFS= read -r s; do
    awk -F'\t' -v s="$s" '$1 == s { f = 1 } END { exit !f }' "$manifest" \
        || errors+="        no rows for Cassandra $s; replace-jars.sh would fail its build"$'\n'
done <<< "$series_built"

grep -q 'replace-jars' "$dockerfile" \
    || errors+="        $dockerfile does not run replace-jars.sh; jars.tsv would be decoration"$'\n'

if [[ -n "$errors" ]]; then
    printf '%s' "$errors" >&2
    vfail "$manifest is not a complete, checksum-pinned manifest (DESIGN.md D-55)"
fi

vinfo "$rows replaced JAR(s) across $(wc -l <<< "$series_built" | tr -d ' ') Cassandra series, each pinned by sha256"
