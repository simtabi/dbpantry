#!/usr/bin/env bash
# verify: the runtime layer picks Docker, Podman or nerdctl correctly, and no script bypasses it
# tags: fast structure runtime
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# dbpantry runs on any OCI runtime with a Compose implementation (design.md
# D-60). That holds only while two things stay true:
#
#   1. scripts/runtime-lib.sh resolves the runtime the documented way --
#      auto order, CONTAINER_RUNTIME override, the podman-docker shim, the
#      Compose flavor, MDB_COMPOSE -- which is tested here against STUB CLIs on
#      a private PATH, so the result does not depend on what this machine has;
#   2. nothing calls `docker` directly. One forgotten `docker exec` is a script
#      that works on the author's laptop and fails on every Podman host, and a
#      grep is the only reviewer that reads every line every time.

cd "$MDB_ROOT" || exit 1

# --- 2. no direct runtime CLI calls ------------------------------------------
# Code lines only: comments may name docker. The runtime layer itself and this
# check are exempt, because naming the CLIs is their job.
direct="$(grep -nE '(^|[^[:alnum:]_./-])(docker|podman|nerdctl)( compose)? (run|exec|build|volume|rm|logs|inspect|image|images|network|stop|start|history|info|ps|pull|kill|save|tag|push|up|down|cp|port)( |$)' \
            Makefile scripts/* scripts/verify/*.sh 2>/dev/null \
          | grep -vE '^[^:]+:[0-9]+:[[:space:]]*#' \
          | grep -vE '^scripts/(runtime|runtime-lib\.sh|verify/48-runtime-detection\.sh):' || true)"
if [[ -n "$direct" ]]; then
    printf '%s\n' "$direct" | sed 's/^/      /' >&2
    vfail "a script calls a container CLI directly; use mdb_ctr / mdb_compose (scripts/runtime-lib.sh)"
fi
vinfo "no script calls docker, podman or nerdctl directly"

missing=""
for f in scripts/*; do
    [[ -f "$f" && -x "$f" ]] || continue
    case "$f" in scripts/runtime|scripts/runtime-lib.sh|scripts/backend-lib.sh) continue ;; esac
    if grep -qE '(^|[^_[:alnum:]])mdb_(ctr|compose|build|running_services|service_[a-z]+|runtime_[a-z]+)\b' "$f" \
       && ! grep -qE '(runtime|backend)-lib.sh' "$f"; then
        missing+=" $f"
    fi
done
[[ -z "$missing" ]] || vfail "uses the runtime layer without sourcing scripts/runtime-lib.sh or scripts/backend-lib.sh:$missing"
vinfo "every script that uses the runtime layer sources it"

# --- the project name in .env is the one make uses ----------------------------
# Every make target exports COMPOSE_PROJECT_NAME, and Compose ranks the
# environment above .env. When the Makefile exported a bare default, a second
# checkout naming itself in .env still ran as "mdb" and attached the first
# checkout's data volumes -- found while testing this layer on a copy.
pj="$(mktemp "${TMPDIR:-/tmp}/mdb-envproj.XXXXXX")"
add_cleanup "rm -f '$pj'"
printf 'COMPOSE_PROJECT_NAME=mdbcheck48\n' > "$pj"
# A second makefile adds a target that prints the value (make 3.81, which
# macOS ships, has no --eval).
pmk="$(mktemp "${TMPDIR:-/tmp}/mdb-envproj-mk.XXXXXX")"
add_cleanup "rm -f '$pmk'"
# shellcheck disable=SC2016  # make syntax, not shell
printf '_mdb_print_project:\n\t@echo $(COMPOSE_PROJECT_NAME)\n' > "$pmk"
got="$(cd "$MDB_ROOT" && env -u COMPOSE_PROJECT_NAME make -s --no-print-directory \
        -f Makefile -f "$pmk" ENV_FILE="$pj" _mdb_print_project 2>&1 || true)"
expect() {
    local what="$1" want="$2" got="$3"
    [[ "$got" == "$want" ]] || vfail "$what: expected '$want', got '$got'"
    vinfo "$what -> $got"
}
expect "make reads COMPOSE_PROJECT_NAME from the env file" "mdbcheck48" "$got"

# --- 1. detection, against stubs ---------------------------------------------
stubs="$(mktemp -d "${TMPDIR:-/tmp}/mdb-rt-stubs.XXXXXX")"
add_cleanup "rm -rf '$stubs'"

# stub <name> <reachable 0|1> <compose output|"none"> [version text]
stub() {
    local name="$1" up="$2" compose="$3" ver="${4:-$1 version 1.0}"
    cat > "$stubs/$name" <<STUB
#!/usr/bin/env bash
case "\$1 \${2:-}" in
    "info "*)            exit $(( up ? 0 : 1 )) ;;
    "--version "*)       echo "$ver"; exit 0 ;;
    "compose version")   [ "$compose" = none ] && exit 1; echo "$compose"; exit 0 ;;
esac
exit 0
STUB
    chmod +x "$stubs/$name"
}

# The system tools, minus every real container runtime. With /usr/bin itself on
# PATH, a host that has docker or podman there (every GitHub Ubuntu runner)
# answered for a runtime the case had not stubbed: "podman only" resolved to
# the runner's real, running docker.
tools="$(mktemp -d "${TMPDIR:-/tmp}/mdb-rt-tools.XXXXXX")"
add_cleanup "rm -rf '$tools'"
for d in /usr/bin /bin; do
    for f in "$d"/*; do
        n="${f##*/}"
        case "$n" in docker*|podman*|nerdctl*|container|socktainer|incus*|kubectl|lima*|colima) continue ;; esac
        # -L as well as -e: a dangling symlink in /usr/bin (grub-ntldr-img on the
        # 2026-11 ubuntu-latest image) fails -e, so /bin, which is /usr/bin there,
        # would link the same name twice and ln would stop the check.
        [[ -e "$tools/$n" || -L "$tools/$n" ]] || ln -s "$f" "$tools/$n"
    done
done

# Resolve in a clean environment: only the stubs and the runtime-free system
# tools on PATH, and no inherited MDB_* resolution.
resolve() {
    # shellcheck disable=SC2016  # expanded by the inner bash
    env -i PATH="$stubs:$tools" HOME="$HOME" "$@" \
        bash -c '. "$1"; mdb_runtime_init 2>&1 && printf "%s|%s|%s\n" "$MDB_RUNTIME" "$MDB_COMPOSE_CMD" "$MDB_COMPOSE_FLAVOR"' \
        _ "$MDB_ROOT/scripts/runtime-lib.sh" | tail -1 || true
}

reset_stubs() { rm -f "$stubs"/*; }

reset_stubs
stub docker 1 "Docker Compose version v2.40.0"
stub podman 1 "Docker Compose version v2.40.0"
expect "auto, docker and podman both up" "docker|docker compose|compose-v2" "$(resolve)"
expect "CONTAINER_RUNTIME=podman" "podman|podman compose|compose-v2" "$(resolve CONTAINER_RUNTIME=podman)"

reset_stubs
stub docker 0 "Docker Compose version v2.40.0"
stub podman 1 "Docker Compose version v2.40.0"
expect "auto, docker installed but stopped" "podman|podman compose|compose-v2" "$(resolve)"

reset_stubs
stub podman 1 "podman-compose version 1.5.0"
expect "podman compose delegating to podman-compose" "podman|podman compose|podman-compose" "$(resolve)"

reset_stubs
stub docker 1 "podman-compose version 1.5.0" "podman version 5.6.0"
stub podman 1 "podman-compose version 1.5.0"
expect "podman-docker shim counts as podman" "podman|podman compose|podman-compose" "$(resolve)"

reset_stubs
stub nerdctl 1 "nerdctl Compose version v2.1.0"
expect "auto, nerdctl only" "nerdctl|nerdctl compose|nerdctl" "$(resolve)"

reset_stubs
stub docker 1 "Docker Compose version v2.40.0"
expect "MDB_COMPOSE override" "docker|docker-compose -p x|compose-v2" "$(resolve MDB_COMPOSE='docker-compose -p x')"

# A rootless connection on a rootful podman machine: `podman compose` would use
# the machine's rootful socket while the CLI uses the rootless store.
# machine_stub <machine rootful> <cli rootless>
machine_stub() {
    cat > "$stubs/podman" <<STUB
#!/usr/bin/env bash
case "\$*" in
    "info --format "*)      echo "$2"; exit 0 ;;
    "info"*)                exit 0 ;;
    "machine inspect"*)     echo "$1"; exit 0 ;;
    "compose version"*)     echo "Docker Compose version v2.40.0"; exit 0 ;;
esac
exit 0
STUB
    chmod +x "$stubs/podman"
}
reset_stubs
machine_stub true true
got="$(resolve CONTAINER_RUNTIME=podman)"
[[ "$got" == *"runtime-podman.md"* ]] || vfail "rootless connection on a rootful machine was accepted ($got)"
vinfo "podman: rootless connection on a rootful machine refused"
machine_stub true false
expect "podman: rootful machine, rootful connection" "podman|podman compose|compose-v2" "$(resolve CONTAINER_RUNTIME=podman)"
machine_stub false true
expect "podman: rootless machine, rootless connection" "podman|podman compose|compose-v2" "$(resolve CONTAINER_RUNTIME=podman)"

# --- adapters: the interface, and a new backend plugging in -----------------
for f in "$MDB_ROOT"/scripts/runtimes/[0-9]*.sh; do
    name="$(sed -n 's/^mdb_runtime_register \([a-z0-9_-]*\).*/\1/p' "$f")"
    [[ -n "$name" ]] || vfail "${f#"$MDB_ROOT"/} does not call mdb_runtime_register"
    for hook in present reachable compose; do
        grep -q "^_mdb_rt_${name}_${hook}()" "$f" || vfail "${f#"$MDB_ROOT"/} lacks the required _mdb_rt_${name}_${hook}"
    done
done
vinfo "every adapter registers and defines present, reachable and compose"

extra="$(mktemp -d "${TMPDIR:-/tmp}/mdb-rt-adapters.XXXXXX")"
add_cleanup "rm -rf '$extra'"
cat > "$extra/90-fakert.sh" <<'ADAPTER'
mdb_runtime_register fakert
_mdb_rt_fakert_present()   { return 0; }
_mdb_rt_fakert_reachable() { return 0; }
_mdb_rt_fakert_compose()   { printf 'compose-v2 fakert-compose\n'; }
_mdb_rt_fakert_ctr()       { printf 'fakert-cli\n'; }
_mdb_rt_fakert_build()     { echo "fakert build $*"; }
ADAPTER
reset_stubs
expect "a new adapter plugs in with no core change" "fakert|fakert-compose|compose-v2" \
    "$(resolve MDB_RUNTIME_ADAPTER_DIRS="$extra")"
stub docker 1 "Docker Compose version v2.40.0"
expect "auto keeps built-in priority over a later adapter" "docker|docker compose|compose-v2" \
    "$(resolve MDB_RUNTIME_ADAPTER_DIRS="$extra")"
expect "CONTAINER_RUNTIME selects a plugged-in adapter" "fakert|fakert-compose|compose-v2" \
    "$(resolve MDB_RUNTIME_ADAPTER_DIRS="$extra" CONTAINER_RUNTIME=fakert)"
# shellcheck disable=SC2016  # expanded by the inner bash
got="$(env -i PATH="$stubs:$tools" HOME="$HOME" MDB_RUNTIME_ADAPTER_DIRS="$extra" CONTAINER_RUNTIME=fakert \
        bash -c '. "$1"; bash -c "mdb_build -t x ."' _ "$MDB_ROOT/scripts/runtime-lib.sh" 2>&1 || true)"
[[ "$got" == "fakert build -t x ." ]] || vfail "mdb_build did not reach the adapter's build hook in a child shell ($got)"
vinfo "adapter hooks are exported to child shells"

got="$(resolve CONTAINER_RUNTIME=lxc)"
[[ "$got" == *"is not one of"* ]] || vfail "CONTAINER_RUNTIME=lxc was accepted ($got)"
vinfo "CONTAINER_RUNTIME=lxc refused"

reset_stubs
got="$(resolve)"
[[ "$got" == *"no container runtime found"* ]] || vfail "no runtime installed gave '$got'"
vinfo "no runtime installed -> a clear error"

# --- and the real one, when this machine has one ------------------------------
if mdb_runtime_ready; then
    vinfo "this machine: $MDB_RUNTIME via $(mdb_runtime_provider), compose: $MDB_COMPOSE_CMD [$MDB_COMPOSE_FLAVOR]"
else
    vinfo "this machine: no reachable runtime (stub tests above still ran)"
fi
