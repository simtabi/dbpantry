#!/usr/bin/env bash
# shellcheck shell=bash
#
# Compose network subnet: avoid Docker/Podman "Pool overlaps with other one
# on this address space" by picking a free /16 and writing MDB_NETWORK_SUBNET
# when the configured one is taken (design.md D-103). Sourced, never executed.

# shellcheck source=env-lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/env-lib.sh"

# Subnets already claimed by the container engine (all networks).
mdb_network_used_subnets() {
    mdb_runtime_init || return 0
    local id sub
    while read -r id; do
        [[ -n "$id" ]] || continue
        while read -r sub; do
            [[ -n "$sub" ]] || continue
            printf '%s\n' "$sub"
        done < <(mdb_ctr network inspect -f '{{range .IPAM.Config}}{{.Subnet}}{{"\n"}}{{end}}' "$id" 2>/dev/null || true)
    done < <(mdb_ctr network ls -q 2>/dev/null || true)
}

# True when the Compose project network already exists (reuse as-is).
mdb_network_own_exists() {
    local name
    name="$(mdb_project_name)_net"
    mdb_ctr network inspect "$name" >/dev/null 2>&1
}

# Pick a free 172.x.0.0/16 that does not overlap USED (newline list).
# Skips 172.29.0.0/16 (fixture default). Prefers WANT when free.
mdb_network_pick_subnet() {
    local want="$1" used="$2"
    python3 - "$want" "$used" <<'PY'
import ipaddress, sys
want = sys.argv[1].strip() or "172.28.0.0/16"
used_raw = sys.argv[2].splitlines()
fixture = ipaddress.ip_network("172.29.0.0/16")

def parse(s):
    try:
        return ipaddress.ip_network(s.strip(), strict=False)
    except Exception:
        return None

used = [n for n in (parse(s) for s in used_raw) if n is not None]

def free(n):
    if n.overlaps(fixture):
        return False
    return not any(n.overlaps(u) for u in used)

try:
    w = ipaddress.ip_network(want, strict=False)
except Exception:
    w = ipaddress.ip_network("172.28.0.0/16")
if free(w):
    print(w)
    raise SystemExit(0)
for i in range(16, 32):
    c = ipaddress.ip_network(f"172.{i}.0.0/16")
    if free(c):
        print(c)
        raise SystemExit(0)
raise SystemExit("no free 172.16.0.0/12 /16 for MDB_NETWORK_SUBNET")
PY
}

# Write KEY=VALUE into the instance .env (replace or append), quoted per check 50.
mdb_network_env_set() {
    local key="$1" value="$2" file="${3:-$(mdb_env_default_file)}" q tmp
    q="$(mdb_env_quote "$value")"
    q="${q%$'\n'}"
    mkdir -p "$(dirname "$file")"
    touch "$file"
    tmp="$(mktemp "${TMPDIR:-/tmp}/mdb-env.XXXXXX")"
    if grep -qE "^${key}=" "$file" 2>/dev/null; then
        # bash 3.2: rewrite via awk
        awk -v k="$key" -v line="${key}=${q}" '
            BEGIN { done=0 }
            $0 ~ "^"k"=" { print line; done=1; next }
            { print }
            END { if (!done) print line }
        ' "$file" >"$tmp"
    else
        cat "$file" >"$tmp"
        [[ -s "$tmp" && "$(tail -c1 "$tmp" | wc -l)" -eq 0 ]] && printf '\n' >>"$tmp"
        printf '%s=%s\n' "$key" "$q" >>"$tmp"
    fi
    mv "$tmp" "$file"
}

# Before compose up: if MDB_NETWORK_SUBNET overlaps an existing engine pool and
# our project network is not already created, pick a free /16, persist it, and
# export it for this process. Honours MDB_NETWORK_SUBNET_LOCK.
mdb_network_ensure_subnet() {
    local envf want used pick lock gw first
    envf="$(mdb_env_default_file)"
    lock="$(mdb_env_get MDB_NETWORK_SUBNET_LOCK "$envf")"
    [[ -z "$lock" ]] && lock="${MDB_NETWORK_SUBNET_LOCK:-}"
    want="$(mdb_env_get MDB_NETWORK_SUBNET "$envf")"
    want="${want:-${MDB_NETWORK_SUBNET:-172.28.0.0/16}}"

    if mdb_network_own_exists; then
        export MDB_NETWORK_SUBNET="$want"
        return 0
    fi

    used="$(mdb_network_used_subnets | sort -u | tr '\n' '\n')"
    pick="$(mdb_network_pick_subnet "$want" "$used")" || {
        _mdb_rt_err "could not pick a free MDB_NETWORK_SUBNET (engine address space exhausted?)"
        return 1
    }
    if [[ "$pick" == "$want" ]]; then
        export MDB_NETWORK_SUBNET="$want"
        return 0
    fi

    case "$(printf '%s' "$lock" | tr '[:upper:]' '[:lower:]')" in
        1|true|yes|on|lock)
            _mdb_rt_err "MDB_NETWORK_SUBNET=$want overlaps an existing engine network; set a free CIDR or unset MDB_NETWORK_SUBNET_LOCK"
            return 1
            ;;
    esac

    # Only the env file's own project gets the move written. A project named in
    # the environment (check 27's throwaway mdbverifyNNNN, started from the
    # operator's checkout) uses the move for this run and leaves the file alone.
    local fileproj
    fileproj="$(mdb_env_get COMPOSE_PROJECT_NAME "$envf")"
    if [[ "$(mdb_project_name)" != "${fileproj:-mdb}" ]]; then
        export MDB_NETWORK_SUBNET="$pick"
        gw="$(mdb_env_get MDB_NETWORK_GATEWAY "$envf")"
        if [[ -n "$gw" ]] && ! python3 -c "import ipaddress,sys; sys.exit(0 if ipaddress.ip_address('$gw') in ipaddress.ip_network('$pick') else 1)"; then
            MDB_NETWORK_GATEWAY="$(python3 -c "import ipaddress; print(next(ipaddress.ip_network('$pick').hosts()))")"
            export MDB_NETWORK_GATEWAY
        fi
        printf 'dbpantry: MDB_NETWORK_SUBNET %s overlapped an existing pool; project %s uses %s for this run (%s belongs to project %s, left unchanged)\n' \
            "$want" "$(mdb_project_name)" "$pick" "$envf" "${fileproj:-mdb}" >&2
        return 0
    fi

    mdb_network_env_set MDB_NETWORK_SUBNET "$pick" "$envf"
    export MDB_NETWORK_SUBNET="$pick"

    gw="$(mdb_env_get MDB_NETWORK_GATEWAY "$envf")"
    if [[ -n "$gw" ]]; then
        first="$(python3 -c "import ipaddress; print(next(ipaddress.ip_network('$pick').hosts()))")"
        if ! python3 -c "import ipaddress,sys; sys.exit(0 if ipaddress.ip_address('$gw') in ipaddress.ip_network('$pick') else 1)"; then
            mdb_network_env_set MDB_NETWORK_GATEWAY "$first" "$envf"
            export MDB_NETWORK_GATEWAY="$first"
            printf 'dbpantry: MDB_NETWORK_SUBNET %s overlapped an existing pool; moved to %s (gateway %s)\n' "$want" "$pick" "$first" >&2
            return 0
        fi
    fi
    printf 'dbpantry: MDB_NETWORK_SUBNET %s overlapped an existing pool; moved to %s (wrote %s)\n' "$want" "$pick" "$envf" >&2
}

export -f mdb_network_used_subnets mdb_network_own_exists mdb_network_pick_subnet \
    mdb_network_env_set mdb_network_ensure_subnet 2>/dev/null || true
