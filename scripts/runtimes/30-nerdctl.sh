# shellcheck shell=bash
# Runtime adapter: containerd through nerdctl, best effort (see README.md).

mdb_runtime_register nerdctl

_mdb_rt_nerdctl_present()   { command -v nerdctl >/dev/null 2>&1; }
_mdb_rt_nerdctl_reachable() { nerdctl info >/dev/null 2>&1; }

_mdb_rt_nerdctl_compose() {
    nerdctl compose version >/dev/null 2>&1 || return 1
    printf 'nerdctl nerdctl compose\n'
}

_mdb_rt_nerdctl_rootless() { [[ "$(id -u)" != 0 ]]; }
_mdb_rt_nerdctl_provider() { printf 'containerd\n'; }
