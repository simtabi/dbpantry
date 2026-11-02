#!/usr/bin/env bash
# Replace vendored JARs in Cassandra's lib/ with the fixed releases pinned in
# jars.tsv (design.md D-55).
#
#   replace-jars.sh <manifest> <series> <libdir>
#
# Every JAR is downloaded and its sha256 checked BEFORE lib/ is touched, so a
# failed download or a checksum mismatch leaves the base image's lib/ intact
# and fails the build. Then each replaced JAR is removed and its successor
# installed with the same ownership and mode Cassandra ships (root, 0644).
set -euo pipefail

manifest="${1:?usage: replace-jars.sh <manifest> <series> <libdir>}"
series="${2:?usage: replace-jars.sh <manifest> <series> <libdir>}"
lib="${3:?usage: replace-jars.sh <manifest> <series> <libdir>}"
repo="https://repo1.maven.org/maven2"

die() { echo "replace-jars: $*" >&2; exit 1; }

[[ -f "$manifest" ]] || die "no manifest at $manifest"
[[ -d "$lib" ]] || die "no lib directory at $lib"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

olds=()
news=()

# The || clause reads a last row that has no trailing newline.
while IFS=$'\t' read -r s old coord sha || [[ -n "$s" ]]; do
    [[ -z "$s" || "$s" == \#* ]] && continue
    [[ "$s" == "$series" ]] || continue
    [[ "$sha" =~ ^[0-9a-f]{64}$ ]] || die "row for $coord has no valid sha256"

    IFS=: read -r group artifact version classifier <<< "$coord"
    [[ -n "$group" && -n "$artifact" && -n "$version" ]] || die "bad coordinate: $coord"
    jar="$artifact-$version${classifier:+-$classifier}.jar"

    # Base drift: the row was written against a lib/ that had this JAR. If it
    # is gone, the base moved and the row must be re-decided, not guessed at.
    if [[ "$old" != "-" && ! -f "$lib/$old" ]]; then
        die "$old is not in $lib; the base image no longer matches jars.tsv"
    fi
    if [[ -e "$lib/$jar" && "$old" != "$jar" ]]; then
        die "$jar is already in $lib; the base image no longer matches jars.tsv"
    fi

    curl -fsSL --retry 3 -o "$tmp/$jar" "$repo/${group//.//}/$artifact/$version/$jar" \
        || die "download failed: $coord"
    echo "$sha  $tmp/$jar" | sha256sum -c --quiet - \
        || die "sha256 mismatch for $coord; refusing to install it"

    olds+=("$old")
    news+=("$jar")
done < "$manifest"

# A series with no rows is a series this project does not maintain. Built on
# purpose from MDB_CASSANDRA_BASE_IMAGE (UNSUPPORTED_VERSION=true) it keeps
# upstream's JARs, said loudly; built any other way it is a mistake.
if (( ${#news[@]} == 0 )); then
    if [[ "${UNSUPPORTED_VERSION:-false}" == "true" ]]; then
        echo "replace-jars: WARNING: no rows for Cassandra $series; its vendored JARs are left as upstream ships them (unsupported, no scan guarantee)" >&2
        exit 0
    fi
    die "jars.tsv has no rows for Cassandra $series"
fi

for i in "${!news[@]}"; do
    [[ "${olds[$i]}" == "-" ]] || rm -f "$lib/${olds[$i]}"
    install -o root -g root -m 0644 "$tmp/${news[$i]}" "$lib/${news[$i]}"
    echo "replace-jars: ${olds[$i]} -> ${news[$i]}"
done
