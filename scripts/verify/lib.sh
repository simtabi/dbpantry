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

# Every container call goes through the runtime layer (design.md D-60).
# shellcheck source=../backend-lib.sh
. "$MDB_ROOT/scripts/backend-lib.sh"

readonly VERIFY_EXIT_SKIP=3

# Throwaway passwords for containers that live for seconds, generated per run so no
# credential is ever written in the source. Each keeps the mdb-throwaway- prefix that
# scripts/verify/03-secret-scan-repo.sh recognises, and labels keep them distinct: a
# check that needs a wrong password gets one that cannot equal the right one.
_mdb_tw_salt="$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
TW_ALPHA="mdb-throwaway-alpha-${_mdb_tw_salt}"
TW_APP="mdb-throwaway-app-${_mdb_tw_salt}"
TW_BETA="mdb-throwaway-beta-${_mdb_tw_salt}"
TW_BOUNCER="mdb-throwaway-bouncer-${_mdb_tw_salt}"
TW_MYFILE="mdb-throwaway-myfile-${_mdb_tw_salt}"
TW_MYPITR="mdb-throwaway-mypitr-${_mdb_tw_salt}"
TW_PGFILE="mdb-throwaway-pgfile-${_mdb_tw_salt}"
TW_PITR="mdb-throwaway-pitr-${_mdb_tw_salt}"
TW_PLAINTEXT="mdb-throwaway-plaintext-${_mdb_tw_salt}"
TW_RLS="mdb-throwaway-rls-${_mdb_tw_salt}"
TW_RLSAPP="mdb-throwaway-rlsapp-${_mdb_tw_salt}"
TW_SUPER="mdb-throwaway-super-${_mdb_tw_salt}"
TW_USER="mdb-throwaway-user-${_mdb_tw_salt}"
TW_VERIFY="mdb-throwaway-verify-${_mdb_tw_salt}"
TW_WRONG_PASSWORD="mdb-throwaway-wrong-password-${_mdb_tw_salt}"
# shellcheck disable=SC2034  # read by the verify checks that source this file
readonly TW_ALPHA TW_APP TW_BETA TW_BOUNCER TW_MYFILE TW_MYPITR TW_PGFILE TW_PITR TW_PLAINTEXT TW_RLS TW_RLSAPP TW_SUPER TW_USER TW_VERIFY TW_WRONG_PASSWORD

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
# Which runtime is decided once, in scripts/runtime-lib.sh (design.md D-60).
need_runtime() {
    mdb_runtime_init >/dev/null 2>&1 \
        || vfail "no container runtime found (CONTAINER_RUNTIME=${CONTAINER_RUNTIME:-auto}); see docs/guides/runtimes.md"
    mdb_runtime_ready \
        || vfail "$MDB_RUNTIME is not running ($MDB_CTR_CMD info failed); this harness never mocks it"
}
# The historical name, kept so a check written before D-60 still reads.
need_docker() { need_runtime; }

# The dbpantry binary (design.md D-68), built from src/ into a private
# directory removed at exit, so a check never runs a stale build/<os>/dbpantry.
# DBPANTRY_BIN names a prebuilt one instead. Go is a prerequisite like the
# container runtime: without it the check FAILS, it never skips.
# Not in $(...): the cleanup it registers would be lost with the subshell.
need_dbpantry() {
    if [[ -n "${DBPANTRY_BIN:-}" ]]; then
        [[ -x "$DBPANTRY_BIN" ]] || vfail "DBPANTRY_BIN=$DBPANTRY_BIN is not an executable"
        return 0
    fi
    command -v go >/dev/null 2>&1 \
        || vfail "go is not installed; the dbpantry binary is built from src/ (docs/reference/manifest.md)"
    local dir
    dir="$(mktemp -d "${TMPDIR:-/tmp}/mdb-verify-bin.XXXXXX")" || vfail "mktemp failed"
    add_cleanup "rm -rf '$dir'"
    CGO_ENABLED=0 go -C "$MDB_ROOT/src" build -o "$dir/dbpantry" ./cmd/dbpantry >&2 \
        || vfail "could not build the dbpantry binary (go -C src build ./cmd/dbpantry)"
    DBPANTRY_BIN="$dir/dbpantry"
    export DBPANTRY_BIN
}

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
#   image_name pg      -> localhost/dbpantry-pg:17
#   image_name mariadb -> localhost/dbpantry-mariadb:11.8
# (under MDB_IMAGE_PREFIX when it is set; see mdb_image in scripts/env-lib.sh)
image_name() {
    local engine="$1" ver
    ver="$(engine_version "$engine")"
    [[ -n "$ver" ]] || vfail "unknown engine: $engine"
    mdb_image "$engine" "$ver"
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
CLEANUP_PROJECTS=()
CLEANUP_COMMANDS=()

track_container() { CLEANUP_CONTAINERS+=("$1"); }
track_volume()    { require_test_name "$1" volume; CLEANUP_VOLUMES+=("$1"); }
add_cleanup()     { CLEANUP_COMMANDS+=("$1"); }
# For a check's own network: removed after the tracked containers, because a
# network with a container still attached refuses `network rm`. Run with the
# other commands, it failed silently and leaked one network per run.
track_network()   { CLEANUP_NETWORKS+=("$1"); }

# Remove, at exit, every file the check adds under MDB_BACKUP_DIR and nothing else.
#
# Checks once wrote their dumps into the operator's backups/ directory, where
# they piled up by the hundred and were indistinguishable from real ones, and a
# retention run there deleted about 175 real dumps (August 2026). MDB_BACKUP_DIR
# is now always a private temporary directory for checks (below), so this only
# tidies that directory early; it never looks at the checkout's backups/. The
# snapshot is taken when this is called, so call it before the first dump.
cleanup_new_backups() {
    local list
    list="$(mktemp "${TMPDIR:-/tmp}/mdb-backups.XXXXXX")" || return 0
    find "$MDB_BACKUP_DIR" -type f > "$list" 2>/dev/null || true
    add_cleanup "find '$MDB_BACKUP_DIR' -type f ! -name .gitkeep | grep -vxF -f '$list' | while IFS= read -r f; do rm -f \"\$f\"; done; rm -f '$list'"
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
        # track_volume already refused anything else; checked again here so no
        # path into this array can ever remove an operator volume.
        is_test_name "$item" || continue
        mdb_ctr volume rm -f "$item" >/dev/null 2>&1 || true
    done
    for item in ${CLEANUP_NETWORKS[@]+"${CLEANUP_NETWORKS[@]}"}; do
        if [[ -n "$item" ]]; then mdb_ctr network rm "$item" >/dev/null 2>&1 || true; fi
    done
    # Last: every volume Compose labelled with a test project the check ran
    # (use_test_project), after the commands above have brought it down.
    local v
    for item in ${CLEANUP_PROJECTS[@]+"${CLEANUP_PROJECTS[@]}"}; do
        is_test_name "$item" || continue
        for v in $(mdb_ctr volume ls -q --filter "label=com.docker.compose.project=$item" 2>/dev/null); do
            mdb_ctr volume rm -f "$v" >/dev/null 2>&1 || true
        done
    done
}
trap mdb_cleanup EXIT

# --- what a check may touch --------------------------------------------------
#
# A check may create or remove only volumes and Compose projects whose names
# carry a test prefix. Check 29 once worked on <project>_pg17_data with the
# operator's project name, and check 27 removed <project>_patroni*_data and
# <project>_etcd*_data for whatever project .env named: an operator's real HA
# data on any machine that ran the harness. The guard sits in every path that
# removes a volume here (track_volume, remove_test_volumes, mdb_cleanup), so a
# check cannot opt out of it, and check 60 fails any check that removes a
# volume another way or names an operator volume.
#
#   mdbverify<N>   a check's own Compose project (use_test_project)
#   mdbscratch<N>  scratch_stack's project
#   mdb-verify-    a check's own containers, volumes and networks
readonly MDB_TEST_PREFIXES='mdbverify mdbscratch mdb-verify-'

is_test_name() {
    local name="$1" p
    [[ -n "$name" ]] || return 1
    for p in $MDB_TEST_PREFIXES; do
        [[ "$name" == "$p"* ]] && return 0
    done
    return 1
}

# require_test_name NAME [WHAT]: fail the check rather than touch NAME.
require_test_name() {
    is_test_name "$1" \
        || vfail "refusing to touch ${2:-name} '$1': a check may only create or remove names starting with one of: $MDB_TEST_PREFIXES"
}

# Remove volumes now, for a check that starts from nothing. Test names only.
remove_test_volumes() {
    local v
    for v in "$@"; do
        require_test_name "$v" volume
        mdb_ctr volume rm -f "$v" >/dev/null 2>&1 || true
    done
}

# A Compose project of the check's own, for a check that runs the stack with
# the checkout's .env and secrets but must not share its volumes. Exported, so
# make, the scripts and Compose all resolve it before .env.
use_test_project() {
    local p="${1:-mdbverify$$}"
    require_test_name "$p" project
    COMPOSE_PROJECT_NAME="$p"
    export COMPOSE_PROJECT_NAME
    CLEANUP_PROJECTS+=("$p")
}

# Backups never go to the checkout's backups/ from a check. MDB_BACKUP_DIR (the
# environment wins over .env in scripts/backup, restore and verify-backups;
# check 59) is a private directory removed at exit, and MDB_UPGRADE_DIR, where
# scripts/upgrade keeps its working dumps, sits inside it. scratch_run unsets
# both, so a scratch stack still uses its own tree's backups/.
# The checkout's own dump directory, named once here so no check spells the
# path (check 60 fails a check that does). Compare against it; never write it.
# shellcheck disable=SC2034  # read by the verify checks that source this file
readonly MDB_CHECKOUT_BACKUPS="$MDB_ROOT/backups"
MDB_VERIFY_BACKUP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mdb-verify-backups.XXXXXX")" \
    || vfail "could not create a private backup directory for the check"
MDB_BACKUP_DIR="$MDB_VERIFY_BACKUP_DIR"
MDB_UPGRADE_DIR="$MDB_VERIFY_BACKUP_DIR/upgrade"
export MDB_VERIFY_BACKUP_DIR MDB_BACKUP_DIR MDB_UPGRADE_DIR
add_cleanup "rm -rf '$MDB_VERIFY_BACKUP_DIR'"

# A throwaway checkout with its own stack, for checks that change state the
# operator owns: secrets, databases, volumes.
#
# Most checks run against the checkout's own .env, project and secrets/, which
# is fine for reading and fatal for writing: a rotation check rotated the real
# passwords, and a retention run once deleted 175 real dumps. This copies the
# tree WITHOUT .git, backups/, secrets/, certs/, .env and overrides/local,
# writes a fresh .env (a unique COMPOSE_PROJECT_NAME, every host port moved to
# a free high port, backups in the scratch tree) and generates new secrets and
# certificates there with scripts/init. Run commands inside it with
# `(cd "$dir" && ...)`: every script there resolves its root, .env and secrets
# from its own location, never this checkout's.
#
# Cleanup stops that project and deletes its volumes (only that project's,
# by the project name) and then the tree. It refuses to clean anything whose
# project name is not the generated one.
#
# Usage: scratch_stack PROFILES; then use $SCRATCH_DIR and $SCRATCH_PROJECT.
# Not in $(...): the cleanup it registers would be lost with the subshell.
_port_busy() {
    if command -v lsof >/dev/null 2>&1; then
        lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
    else
        (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
    fi
}

scratch_stack() {
    local profiles="$1" sdir proj base key
    sdir="$(mktemp -d "${TMPDIR:-/tmp}/mdb-scratch.XXXXXX")" || vfail "mktemp failed"
    proj="mdbscratch$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
    SCRATCH_PROJECT="$proj"; SCRATCH_DIR="$sdir"
    # Registered first, so a failure below still removes the tree.
    add_cleanup "scratch_stack_cleanup '$sdir' '$proj'"
    export SCRATCH_PROJECT SCRATCH_DIR
    # Anchored: an unanchored "certs" also drops scripts/certs.
    rsync -a --exclude /.git --exclude /backups --exclude /secrets --exclude /certs \
        --exclude /.env --exclude /.env.prod --exclude /overrides/local "$MDB_ROOT/" "$sdir/" \
        || vfail "could not copy the checkout to $sdir"
    mkdir -p "$sdir/backups"
    # init ends with check-env, which fails here whenever the operator's own
    # stack holds the default ports; the ports are moved below and checked then.
    ( cd "$sdir" && scripts/init >/dev/null 2>&1 ) || true
    [[ -s "$sdir/.env" && -s "$sdir/secrets/pg_superuser_password.txt" && -s "$sdir/certs/ca.crt" \
       && "$sdir/secrets" != "$MDB_ROOT/secrets" ]] \
        || vfail "scripts/init did not create the scratch checkout's .env, secrets and certs"
    # Every port key moves to a run of free high ports. The active lines are
    # deleted rather than overridden: check-env reads every assignment.
    base=$(( 41000 + (RANDOM % 200) * 100 ))
    sed -i.bak -E '/^(COMPOSE_PROJECT_NAME|MDB_PROFILES|MDB_BACKUP_DIR|MDB_NETWORK_SUBNET|MDB_[A-Z0-9_]*PORT)=/d' "$sdir/.env" \
        && rm -f "$sdir/.env.bak"
    {
        printf '\n# scratch_stack (verify): never the operator project\n'
        printf 'COMPOSE_PROJECT_NAME=%s\n' "$proj"
        printf 'MDB_PROFILES="%s"\n' "$profiles"
        printf 'MDB_BACKUP_DIR="%s"\n' "$sdir/backups"
        # Its own subnet too: the operator's mdb_net already holds the default.
        printf 'MDB_NETWORK_SUBNET=10.%s.%s.0/24\n' "$(( 200 + RANDOM % 50 ))" "$(( RANDOM % 250 ))"
        for key in $(grep -oE '^#?MDB_[A-Z0-9_]*PORT=' "$MDB_ROOT/.env.example" | tr -d '#=' | sort -u); do
            # A random base can still land on a listener (another tool's
            # ephemeral port), which check-env then rightly refuses.
            while _port_busy "$base"; do base=$(( base + 1 )); done
            printf '%s=%s\n' "$key" "$base"
            base=$(( base + 1 ))
        done
    } >> "$sdir/.env"
    # Last assignment wins for both Compose and mdb_env_get; make sure.
    [[ "$(mdb_env_get COMPOSE_PROJECT_NAME "$sdir/.env")" == "$proj" ]] \
        || vfail "the scratch .env does not resolve to the scratch project"
    scratch_run scripts/check-env --quiet >/dev/null 2>&1 \
        || vfail "check-env rejects the scratch .env: $(scratch_run scripts/check-env 2>&1 | tail -5)"
}

# Run a command in the scratch checkout, with nothing from this process that
# could point it back at the operator's: Compose reads COMPOSE_* from the
# environment before .env.
scratch_run() {
    [[ -n "${SCRATCH_DIR:-}" && "$SCRATCH_DIR" != "$MDB_ROOT" ]] || vfail "scratch_run before scratch_stack"
    ( cd "$SCRATCH_DIR" && env -u COMPOSE_PROJECT_NAME -u COMPOSE_FILE -u COMPOSE_PROFILES \
        -u MDB_ENV_FILE -u ENV_FILE -u MDB_BACKUP_DIR -u MDB_UPGRADE_DIR -u MDB_BACKEND -u MDB_BACKEND_READY \
        MDB_RT_ROOT="$SCRATCH_DIR" "$@" )
}

# `make up` in the scratch stack, with mdb_up's failure tail.
scratch_up() {
    local out rc=0
    out="$(scratch_run make up "$@" 2>&1)" || rc=$?
    (( rc == 0 )) || printf '%s\n' "$out" | tail -25 | sed 's/^/      /' >&2
    return "$rc"
}

# A service exec in the scratch stack.
scratch_exec() {
    # shellcheck disable=SC2016  # expanded by the inner bash
    scratch_run bash -c '. scripts/backend-lib.sh && mdb_svc_exec "$@"' scratch_exec "$@"
}

# Every named service in the scratch stack is healthy.
scratch_healthy() {
    # shellcheck disable=SC2016  # expanded by the inner bash
    scratch_run bash -c '. scripts/backend-lib.sh && for s in "$@"; do mdb_svc_healthy "$s" || exit 1; done' scratch_healthy "$@"
}

scratch_stack_cleanup() {
    local sdir="$1" proj="$2" v
    [[ "$proj" == mdbscratch* && -d "$sdir" && "$sdir" != "$MDB_ROOT" && "$sdir" == */mdb-scratch.* ]] || return 0
    if [[ "$(mdb_env_get COMPOSE_PROJECT_NAME "$sdir/.env" 2>/dev/null)" == "$proj" ]]; then
        SCRATCH_DIR="$sdir" scratch_run make down >/dev/null 2>&1 || true
    fi
    for v in $(mdb_ctr volume ls -q --filter "label=com.docker.compose.project=$proj" 2>/dev/null); do
        mdb_ctr volume rm -f "$v" >/dev/null 2>&1 || true
    done
    rm -rf "$sdir"
}

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

# wait_ready_while BUDGET CEILING WHAT BUSY... -- CMD...
# wait_ready, except that past BUDGET seconds it keeps waiting, up to CEILING,
# for as long as the BUSY command succeeds (work visibly still in progress). A
# loaded machine then gets the time it needs, and a genuinely stuck one still
# fails at BUDGET instead of the ceiling.
wait_ready_while() {
    local budget="$1" ceiling="$2" what="$3"; shift 3
    local -a busy=()
    while (( $# )) && [[ "$1" != -- ]]; do busy+=("$1"); shift; done
    shift
    local start=$SECONDS streak=0 needed=3 elapsed
    while :; do
        if "$@" >/dev/null 2>&1; then
            streak=$(( streak + 1 ))
            (( streak >= needed )) && return 0
        else
            streak=0
        fi
        elapsed=$(( SECONDS - start ))
        if (( elapsed >= budget )); then
            if (( elapsed >= ceiling )) || ! "${busy[@]}" >/dev/null 2>&1; then
                vfail "timed out after ${elapsed}s waiting for: $what"
            fi
        fi
        sleep 1
    done
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
