#!/bin/sh
# mdb-fetch [--verified-by WHAT] [--fingerprint FPR] URL OUT
#
# The one way an image build downloads a file (design.md D-71).
#
#   - Retries. A transient reset on a CDN -- the PGDG key fetch that failed a
#     verify run with "HTTP/2 stream 1 was not closed cleanly" -- is retried
#     with curl's exponential backoff instead of failing a 40-minute job.
#   - Verifies. The URL is looked up in checksums.txt beside this script and
#     the SHA-256 must match. A file whose digest cannot be known in
#     advance (its version comes from the base image at build time) needs
#     --verified-by naming the check the Dockerfile runs on it next, which
#     check 70 requires to be present.
#   - --fingerprint FPR treats the file as an OpenPGP public key and refuses it
#     unless its primary key fingerprint is FPR. A key can be re-published with
#     new subkeys or a later expiry, so its digest is the wrong thing to pin;
#     its fingerprint is not.
set -eu

verified_by="" fingerprint=""
while [ "$#" -gt 2 ]; do
    case "$1" in
        --verified-by) verified_by="$2"; shift 2 ;;
        --fingerprint) fingerprint="$2"; shift 2 ;;
        *) echo "mdb-fetch: unknown option $1" >&2; exit 2 ;;
    esac
done
[ "$#" -eq 2 ] || { echo "usage: mdb-fetch [--verified-by WHAT] [--fingerprint FPR] URL OUT" >&2; exit 2; }
url=$1 out=$2
sums="${FETCH_SUMS:-/usr/local/share/mdb-supply/checksums.txt}"

curl -fsSL --retry 6 --retry-all-errors --connect-timeout 30 --max-time 900 \
    -o "$out" "$url"

name="${url##*/}"
want="$(awk -v u="$url" '$1==u {print $2; exit}' "$sums" 2>/dev/null || true)"

if [ -n "$fingerprint" ]; then
    command -v gpg >/dev/null 2>&1 || { echo "mdb-fetch: --fingerprint needs gpg" >&2; exit 1; }
    got="$(GNUPGHOME="$(mktemp -d)" gpg --show-keys --with-colons "$out" 2>/dev/null \
        | awk -F: '/^fpr:/ {print $10; exit}')"
    if [ "$got" != "$fingerprint" ]; then
        echo "mdb-fetch: $name has fingerprint '${got:-none}', expected $fingerprint" >&2
        rm -f "$out"; exit 1
    fi
    echo "mdb-fetch: $name key fingerprint verified" >&2
elif [ -n "$want" ]; then
    got="$(sha256sum "$out" | awk '{print $1}')"
    if [ "$got" != "$want" ]; then
        echo "mdb-fetch: $name has sha256 $got, expected $want" >&2
        rm -f "$out"; exit 1
    fi
    echo "mdb-fetch: $name sha256 verified" >&2
elif [ -n "$verified_by" ]; then
    echo "mdb-fetch: $name not in checksums.txt; verified by: $verified_by" >&2
else
    echo "mdb-fetch: $name has no digest in $sums and no --verified-by; refusing it" >&2
    rm -f "$out"; exit 1
fi
