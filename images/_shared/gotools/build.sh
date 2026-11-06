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
# Vulnerable dependencies are raised from floors.txt, installed beside this
# script (design.md D-70): every module listed there that the tool depends on at
# an older version is raised to the floor before building, and nothing else in
# the module graph moves. A floor never downgrades and never adds a module.
#
# Trailing module@version arguments still work and are applied after the
# floors, exactly as before, but check 69 refuses them in a Dockerfile: a fix
# written inline has to be repeated in every image that builds the same tool,
# which is how one x/net bump was missed in four Dockerfiles at once.
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

# ver_lt A B: true when semantic version A sorts before B. sort -V orders the
# numeric fields correctly but puts a release BEFORE its own pre-releases, the
# opposite of semver, so equal bases are settled by the pre-release rule.
ver_lt() {
    [ "$1" = "$2" ] && return 1
    a_base=${1%%-*} b_base=${2%%-*}
    if [ "$a_base" != "$b_base" ]; then
        [ "$(printf '%s\n%s\n' "$a_base" "$b_base" | sort -V | head -1)" = "$a_base" ]
        return
    fi
    # Same base: a release outranks any pre-release of it.
    [ "$1" = "$a_base" ] && return 1
    [ "$2" = "$b_base" ] && return 0
    [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1)" = "$1" ]
}

floors="${GO_FLOORS:-/usr/local/share/build-go-tool/floors.txt}"
changed=0
if [ -r "$floors" ]; then
    while read -r module floor _; do
        case "$module" in ''|'#'*) continue ;; esac
        current="$(go list -m -f '{{.Version}}' "$module" 2>/dev/null)" || continue
        [ -n "$current" ] || continue
        if ver_lt "$current" "$floor"; then
            echo "build-go-tool: $name: raising $module $current -> $floor" >&2
            go get "$module@$floor"
            changed=1
        fi
    done < "$floors"
else
    echo "build-go-tool: no floors file at $floors" >&2
    exit 1
fi

if [ "$#" -gt 0 ]; then
    for module in "$@"; do
        go get "$module"
    done
    changed=1
fi
[ "$changed" -eq 1 ] && go mod tidy

CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="$TARGETARCH" \
    go build -trimpath -ldflags "-s -w ${LDFLAGS:-}" -o "$out" "$pkg"

# Record what went in, so `go version -m` on the image answers "built with what".
go version -m "$out" | head -3
