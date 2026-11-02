#!/bin/sh
# build-go-tool <name> <git-url> <tag> <package> <output> [module@version ...]
#
# Builds one upstream Go binary from its release tag, on the toolchain this
# stage runs on, for the image's target architecture (design.md D-51).
#
# The released binaries of gosu, postgres_exporter and rclone are compiled with
# whichever Go was current on their release day, so a Go standard-library CVE
# fixed since then stays in the image until upstream cuts a new release -- gosu
# last released in 2025. Rebuilding the same tag on a patched toolchain removes
# those findings without changing a line of the tool's own code.
#
# Trailing module@version arguments raise a vulnerable dependency to its fixed
# version before building. Each is a floor raised for a named CVE, recorded in
# the Dockerfile beside the call; nothing else in the module graph moves.
#
# Cross-compiled: the stage runs on the BUILD platform and sets GOARCH from
# TARGETARCH, so an arm64 image never compiles under emulation.
set -eu

[ "$#" -ge 5 ] || { echo "usage: build-go-tool <name> <git-url> <tag> <package> <output> [module@version ...]" >&2; exit 2; }
name=$1 url=$2 tag=$3 pkg=$4 out=$5
shift 5

: "${TARGETARCH:?TARGETARCH is unset; build with BuildKit}"

src="/src/$name"
git clone --quiet --depth 1 --branch "$tag" "$url" "$src"
cd "$src"

if [ "$#" -gt 0 ]; then
    for module in "$@"; do
        go get "$module"
    done
    go mod tidy
fi

CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="$TARGETARCH" \
    go build -trimpath -ldflags "-s -w ${LDFLAGS:-}" -o "$out" "$pkg"

# Record what went in, so `go version -m` on the image answers "built with what".
go version -m "$out" | head -3
