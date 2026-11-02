#!/usr/bin/env bash
# shellcheck shell=bash
#
# Shared helpers for dbpantry verify checks.
#
# Contract for every check script:
#   exit 0  -> PASS
#   exit 3  -> SKIP (genuinely inapplicable here; a reason is mandatory)
#   other   -> FAIL
#
# SKIP is not a way to make a red check quiet. A feature that does not exist
# yet must FAIL, not skip. Skip only when the check cannot apply to this
# machine or profile selection at all, and always say why.

set -euo pipefail

MDB_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export MDB_ROOT

# Engines are declared, not hardcoded (SPEC section 22.1).
# shellcheck source=../engine-lib.sh
. "$MDB_ROOT/scripts/engine-lib.sh"

# Every container call goes through the runtime layer (DESIGN.md D-60).
# shellcheck source=../runtime-lib.sh
. "$MDB_ROOT/scripts/runtime-lib.sh"

readonly VERIFY_EXIT_SKIP=3

if [[ -t 1 ]] && [[ -z "${NO_COLOR:-}" ]]; then
    C_DIM=$'\033[2m'; C_OFF=$'\033[0m'
else
    C_DIM=''; C_OFF=''
fi
readonly C_DIM C_OFF

# Informational line inside a check.
vinfo() { printf '      %s%s%s\n' "$C_DIM" "$*" "$C_OFF"; }

# Fail this check with a reason.
vfail() { printf '      FAIL: %s\n' "$*" >&2; exit 1; }

# Skip this check with a mandatory reason.
vskip() {
    [[ -n "${1:-}" ]] || { printf '      SKIP called with no reason\n' >&2; exit 1; }
    printf '      SKIP: %s\n' "$*"
    exit "$VERIFY_EXIT_SKIP"
}

# A container runtime is required. CLAUDE.md is explicit: if none is available
# we stop and say so rather than mocking, so this is a FAIL and never a SKIP.
# Which runtime is decided once, in scripts/runtime-lib.sh (DESIGN.md D-60).
need_runtime() {
    mdb_runtime_init >/dev/null 2>&1 \
        || vfail "no container runtime found (CONTAINER_RUNTIME=${CONTAINER_RUNTIME:-auto}); see docs/guides/runtimes.md"
    mdb_runtime_ready \
        || vfail "$MDB_RUNTIME is not running ($MDB_CTR_CMD info failed); this harness never mocks it"
}
# The historical name, kept so a check written before D-60 still reads.
need_docker() { need_runtime; }

# Read a variable from .env, falling back to .env.example, then to a default.
env_get() {
    local key="$1" default="${2:-}" file val
    for file in "$MDB_ROOT/.env" "$MDB_ROOT/.env.example"; do
        [[ -f "$file" ]] || continue
        val="$(mdb_env_get "$key" "$file")"
        if [[ -n "$val" ]]; then printf '%s\n' "$val"; return 0; fi
    done
    printf '%s\n' "$default"
}

# Is an engine selected by the active profiles?
engine_selected() {
    local engine="$1" profiles
    profiles="${COMPOSE_PROFILES:-$(env_get MDB_PROFILES 'pg,ui')}"
    [[ ",${profiles}," == *",${engine},"* ]]
}

# Does a local image exist?
image_exists() { mdb_ctr image inspect "$1" >/dev/null 2>&1; }

# Fully-qualified name for one of our engine images.
#   image_name pg      -> ghcr.io/simtabi/dbpantry-pg:17
#   image_name mariadb -> ghcr.io/simtabi/dbpantry-mariadb:11.8
image_name() {
    local engine="$1" prefix ver
    prefix="${MDB_IMAGE_PREFIX:-ghcr.io/simtabi}"
    ver="$(engine_version "$engine")"
    [[ -n "$ver" ]] || vfail "unknown engine: $engine"
    printf '%s/dbpantry-%s:%s\n' "$prefix" "$engine" "$ver"
}

# The configured version for an engine: MDB_<ENGINE>_VERSION if set, otherwise
# whatever its descriptor declares as the default. No engine names here.
engine_version() {
    local engine="$1" upper ver
    [[ "$engine" == "cli" ]] && { printf 'dev'; return 0; }
    upper="$(printf '%s' "$engine" | tr '[:lower:]-' '[:upper:]_')"
    ver="$(env_get "MDB_${upper}_VERSION" '')"
    if [[ -n "$ver" ]]; then printf '%s' "$ver"; return 0; fi
    ( engine_load "$engine" >/dev/null 2>&1 && printf '%s' "$MDB_ENGINE_DEFAULT_VERSION" )
}

# Fail with a clear message when the image a check needs has not been built.
#
# "Not built" and "was here and vanished" are different problems with different
# fixes, and saying the wrong one costs real time: a full harness run takes tens
# of minutes, Docker reclaims unused images under disk pressure, and the
# resulting "run: make build" points at a build that already succeeded.
need_image() {
    local img="$1"
    image_exists "$img" && return 0

    # A check run on its own has no snapshot from the runner, so take one at
    # first use. It cannot see images reclaimed before the check started, but
    # it catches the common case -- a long check whose image vanishes partway
    # through -- and without it a standalone run gets the misleading
    # "run: make build" that this whole mechanism exists to avoid.
    if [[ -z "${MDB_IMAGE_SNAPSHOT:-}" ]]; then
        MDB_IMAGE_SNAPSHOT="$(mktemp)"
        export MDB_IMAGE_SNAPSHOT
        add_cleanup "rm -f '$MDB_IMAGE_SNAPSHOT'"
        mdb_ctr images --format '{{.Repository}}:{{.Tag}}' > "$MDB_IMAGE_SNAPSHOT" 2>/dev/null || true
    fi

    if [[ -n "${MDB_IMAGE_SNAPSHOT:-}" ]] && [[ -f "$MDB_IMAGE_SNAPSHOT" ]] \
       && grep -qxF "$img" "$MDB_IMAGE_SNAPSHOT" 2>/dev/null; then

        # It was here and is gone, so REBUILD it rather than failing a check
        # whose subject is not the image. Volume leaks were the main cause of
        # the disk pressure behind this and are fixed, but a shared machine can
        # still reclaim under someone else's load, and a forty-minute run
        # should not be lost to that.
        #
        # Deliberately only for the reclaimed case: an image that was never
        # built is a different problem with a different fix, and rebuilding
        # silently there would hide "you forgot to run make build".
        local engine="${img##*/}"; engine="${engine%%:*}"; engine="${engine#dbpantry-}"
        if [[ -f "$MDB_ROOT/images/$engine/Dockerfile" ]]; then
            printf '      image %s was reclaimed mid-run; rebuilding\n' "$img" >&2
            if ( cd "$MDB_ROOT" && ./scripts/build "$engine" >/dev/null 2>&1 ); then
                image_exists "$img" && { printf '      rebuilt %s\n' "$img" >&2; return 0; }
            fi
        fi

        vfail "image RECLAIMED mid-run: $img
       It existed when this run started and has since been removed, which
       Docker does to unused images under disk pressure. This is an
       environment failure, not a build failure -- rebuilding and re-running
       the check alone will pass. Free disk space to run the full harness."
    fi

    # Never built. Before repeating "run: make build", check whether that advice
    # is even true -- for a NON-DEFAULT version it is not. `make build` builds
    # each engine at the single version .env names, so a check that needs
    # pg:16 while the default is 18 can never be satisfied by it: the suggested
    # command rebuilds pg:18 and the check fails again, identically. Checks 14
    # and 29 exist precisely to exercise a second major, and both failed this
    # way on a runner that had run `make build` exactly as instructed.
    #
    # So build the version that was asked for, when the repo declares a base for
    # it. Anything not in bases.tsv is a genuine "you forgot", and still says so.
    local engine="${img##*/}"; engine="${engine%%:*}"; engine="${engine#dbpantry-}"
    local want="${img##*:}"
    if [[ -f "$MDB_ROOT/images/$engine/Dockerfile" ]] \
       && awk -v e="$engine" -v v="$want" \
              '$1==e && $2==v {f=1} END{exit !f}' "$MDB_ROOT/images/bases.tsv" 2>/dev/null; then
        # Built here rather than through scripts/build, which resolves the
        # version from .env and ignores the environment -- `MDB_PG_VERSION=16
        # ./scripts/build pg` cheerfully produces pg:17. Passing the version as
        # a build arg is the same thing build_image does, minus the .env lookup
        # that is wrong here, and it cannot be raced by a parallel check the way
        # rewriting .env could.
        local base
        printf '      %s is a declared version that make build does not cover; building it\n' "$img" >&2
        base="$(awk -v e="$engine" -v v="$want" '$1==e && $2==v {print $3; exit}' \
                "$MDB_ROOT/images/bases.tsv")"
        if mdb_build -f "$MDB_ROOT/images/$engine/Dockerfile" \
                --build-arg "BASE_IMAGE=$base" \
                --build-arg "ENGINE_VERSION=$want" \
                -t "$img" "$MDB_ROOT/images" >/dev/null 2>&1; then
            image_exists "$img" && { printf '      built %s\n' "$img" >&2; return 0; }
        fi
        vfail "could not build $img, though images/bases.tsv declares
       $base for $engine $want."
    fi

    vfail "image not built yet: $img (run: make build)"
}

# `make up`, keeping the output for the failure path.
#
# Nine call sites ran `mdb_up ... || vfail "make up failed"`,
# which names the command and nothing whatsoever about why it failed. Six checks
# reported exactly that string on CI at once, and it was equally consistent with
# a missing image, a port collision, an unhealthy container and a compose syntax
# error -- so the harness produced six identical sentences and no lead.
#
# The tail is bounded because a failing boot can emit hundreds of lines of pull
# progress, and the useful part -- the service that went unhealthy, the port that
# was taken -- is always at the end.
mdb_up() {
    local out rc=0
    out="$(make up "$@" 2>&1)" || rc=$?
    if (( rc != 0 )); then
        printf '%s\n' "$out" | tail -25 | sed 's/^/      /' >&2
    fi
    return "$rc"
}

# Resolve the digest-pinned upstream base for an engine + version from the
# single source of truth in images/bases.tsv.
base_image() {
    local engine="$1" version="$2" line
    line="$(awk -v e="$engine" -v v="$version" \
        '$1==e && $2==v {print $3; exit}' "$MDB_ROOT/images/bases.tsv" 2>/dev/null || true)"
    [[ -n "$line" ]] || vfail "no base pinned for $engine $version in images/bases.tsv"
    printf '%s\n' "$line"
}

# Build one of our images, passing the pinned base and the engine version.
build_image() {
    local engine="$1" tag="$2" version base
    version="$(engine_version "$engine")"

    # Context is images/ so MySQL and MariaDB can share images/_shared.
    base="$(base_image "$engine" "$version")"
    mdb_build \
        -f "$MDB_ROOT/images/$engine/Dockerfile" \
        --build-arg "BASE_IMAGE=$base" \
        --build-arg "ENGINE_VERSION=$version" \
        -t "$tag" "$MDB_ROOT/images"
}

# --- cleanup -----------------------------------------------------------------
#
# There is exactly ONE EXIT trap, installed here, and checks must never install
# their own. A per-check `trap ... EXIT` REPLACES this one rather than adding to
# it, which silently defeats container cleanup: an early version of this harness
# leaked 11 containers holding ~1.8 GB across a single run, which then slowed
# every later check. Register extra work with add_cleanup instead.

CLEANUP_CONTAINERS=()
CLEANUP_VOLUMES=()
CLEANUP_NETWORKS=()
CLEANUP_COMMANDS=()

track_container() { CLEANUP_CONTAINERS+=("$1"); }
track_volume()    { CLEANUP_VOLUMES+=("$1"); }
add_cleanup()     { CLEANUP_COMMANDS+=("$1"); }
# For a check's own network: removed after the tracked containers, because a
# network with a container still attached refuses `network rm`. Run with the
# other commands, it failed silently and leaked one network per run.
track_network()   { CLEANUP_NETWORKS+=("$1"); }

# Remove, at exit, every file the check adds under backups/ and nothing else.
#
# Checks that take real dumps write into the operator's backups/ directory. Left
# behind, they piled up by the hundred across runs and were indistinguishable
# from the operator's own dumps. The snapshot is taken when this is called, so
# call it before the first dump; anything already there is never touched.
cleanup_new_backups() {
    local list
    list="$(mktemp "${TMPDIR:-/tmp}/mdb-backups.XXXXXX")" || return 0
    find "$MDB_ROOT/backups" -type f > "$list" 2>/dev/null || true
    add_cleanup "find '$MDB_ROOT/backups' -type f ! -name .gitkeep | grep -vxF -f '$list' | while IFS= read -r f; do rm -f \"\$f\"; done; rm -f '$list'"
}

mdb_cleanup() {
    local item
    for item in ${CLEANUP_COMMANDS[@]+"${CLEANUP_COMMANDS[@]}"}; do
        eval "$item" >/dev/null 2>&1 || true
    done
    for item in ${CLEANUP_CONTAINERS[@]+"${CLEANUP_CONTAINERS[@]}"}; do
        # -v removes the container's ANONYMOUS volumes with it.
        #
        # Without it every throwaway container leaks one: the engine images
        # declare VOLUME for their data directory, so a `docker run` with no
        # -v gets an anonymous volume that outlives `docker rm -f`. A full
        # harness run starts dozens of such containers, and the leak had
        # reached 420 volumes and 34GB here -- which is disk pressure, which is
        # what makes Docker reclaim IMAGES mid-run. The harness was breaking
        # itself, one orphaned volume at a time.
        if [[ -n "$item" ]]; then mdb_ctr rm -f -v "$item" >/dev/null 2>&1 || true; fi
    done
    for item in ${CLEANUP_VOLUMES[@]+"${CLEANUP_VOLUMES[@]}"}; do
        if [[ -n "$item" ]]; then mdb_ctr volume rm -f "$item" >/dev/null 2>&1 || true; fi
    done
    for item in ${CLEANUP_NETWORKS[@]+"${CLEANUP_NETWORKS[@]}"}; do
        if [[ -n "$item" ]]; then mdb_ctr network rm "$item" >/dev/null 2>&1 || true; fi
    done
}
trap mdb_cleanup EXIT

# Wait until a command succeeds, or fail after a budget in seconds.
wait_for() {
    local budget="$1" what="$2"; shift 2
    local i
    for (( i = 0; i < budget; i++ )); do
        if "$@" >/dev/null 2>&1; then return 0; fi
        sleep 1
    done
    vfail "timed out after ${budget}s waiting for: $what"
}

# Wait for a DATABASE to be genuinely ready, which is not the same as answering.
#
# The official entrypoints start a TEMPORARY server during first-run
# initialisation (socket-only, to run initdb.d), shut it down, and then start
# the real one. A single successful probe can land on that temporary server and
# the very next command then fails with "no such file or directory" as the
# socket disappears underneath it.
#
# Requiring consecutive successes rides out that restart window. Verified
# against the real log sequence: "ready to accept connections" (temp) ->
# "shutting down" -> "init process complete" -> "ready to accept connections".
wait_ready() {
    local budget="$1" what="$2"; shift 2
    local i streak=0 needed=3
    for (( i = 0; i < budget; i++ )); do
        if "$@" >/dev/null 2>&1; then
            streak=$(( streak + 1 ))
            (( streak >= needed )) && return 0
        else
            streak=0
        fi
        sleep 1
    done
    vfail "timed out after ${budget}s waiting for: $what"
}

# Require a file to exist, else fail with a useful message.
need_file() {
    [[ -f "$1" ]] || vfail "expected file does not exist yet: ${1#"$MDB_ROOT"/}"
}

# Require a directory to exist.
need_dir() {
    [[ -d "$1" ]] || vfail "expected directory does not exist yet: ${1#"$MDB_ROOT"/}"
}

# Run a command, capturing output, and fail with the full output on error.
# Reading the complete error output first is the self-heal rule; this makes
# that output available rather than swallowing it.
run_or_fail() {
    local out rc=0
    out="$("$@" 2>&1)" || rc=$?
    if (( rc != 0 )); then
        printf '      command failed (rc=%s): %s\n' "$rc" "$*" >&2
        printf '      %s\n' "$out" >&2
        exit 1
    fi
    printf '%s' "$out"
}
