# Runtime adapters

Each file here teaches `scripts/runtime-lib.sh` (design.md D-60) about one
container runtime. The core knows no runtime by name. It loads these files in
filename order, and that order is the auto-detection priority. To add a backend,
add a file. You don't need to edit the core or any caller.

```
10-docker.sh    Docker Engine and every Docker-API product (Desktop, OrbStack, Colima, Rancher Desktop/moby)
20-podman.sh    Podman, native or podman machine
30-nerdctl.sh   containerd through nerdctl (best effort)
```

Adapters kept outside the repo, or under test, load from the directories in
`MDB_RUNTIME_ADAPTER_DIRS` (colon-separated), after the built-in ones.

## The interface

A file registers its name and defines functions prefixed `_mdb_rt_<name>_`:

| function | required | contract |
| --- | --- | --- |
| `mdb_runtime_register <name>` | yes | called once at the top of the file |
| `_mdb_rt_<name>_present` | yes | exit 0 if the CLI is installed |
| `_mdb_rt_<name>_reachable` | yes | exit 0 if the engine answers |
| `_mdb_rt_<name>_compose` | yes | print `<flavor> <command...>` for the Compose implementation, or fail. Flavors: `compose-v2`, `podman-compose`, `nerdctl` |
| `_mdb_rt_<name>_ctr` | no | print the container CLI command (default: `<name>`) |
| `_mdb_rt_<name>_alias` | no | print another adapter's name if this CLI is really that runtime (the podman-docker shim) |
| `_mdb_rt_<name>_validate` | no | fail, with a message on stderr, if this setup can't work (Podman's split store) |
| `_mdb_rt_<name>_build` | no | build an image (default: `mdb_ctr build "$@"`) |
| `_mdb_rt_<name>_rootless` | no | exit 0 if the engine is rootless (default: no) |
| `_mdb_rt_<name>_provider` | no | print the product providing the engine (default: `<name>`) |

Every caller sees only `mdb_ctr`, `mdb_compose`, `mdb_build` and the service-state
helpers, which use Docker CLI and Compose Specification syntax. A new backend
plugs in as long as it provides that, either natively or through a compose
flavor that the core knows how to drive.

## Planned backends

Kubernetes, LXC/Incus and Apple's `container` tool are planned as follow-up
adapters. None of them speaks the Compose Specification, so each adapter will
also need a deployment path that maps services to its own objects (manifests,
instances, `container run`). They aren't usable yet. `CONTAINER_RUNTIME` lists
only the adapters that are present in this directory.
