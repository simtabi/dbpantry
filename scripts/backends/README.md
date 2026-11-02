# Deployment backends

`MDB_BACKEND` chooses how the stack is deployed (design.md D-62). Each file here
is one backend; `scripts/backend-lib.sh` loads them all and dispatches to the
one selected. The container runtime under the Compose backend is a separate
choice (`CONTAINER_RUNTIME`, `scripts/runtimes/`, D-60).

```
compose.sh      Docker Compose, podman compose or nerdctl compose (the default)
kubernetes.sh   the Helm chart in deploy/helm/dbpantry, via kubectl and helm (D-64)
```

Backends kept outside the repo, or under test, load from the directories in
`MDB_BACKEND_DIRS` (colon-separated), after the built-in ones.

## The interface

A file calls `mdb_backend_register <name>` and defines `_mdb_be_<name>_<verb>`.
Scripts never call these directly; they call the public verb in
`backend-lib.sh`, which fails with a message naming the backend when the
selected one lacks a verb.

| Verb | Public function | Contract |
|---|---|---|
| `up` (required) | `mdb_be_up` | deploy, wait until every service is healthy |
| `down` (required) | `mdb_be_down` | stop; never deletes data |
| `status` | `mdb_be_status` | what is running, for people |
| `running` (required) | `mdb_be_running` | running service names, one per line |
| `logs` | `mdb_be_logs` | stack logs; `-f` and `--tail=N` are passed through |
| `exec` (required) | `mdb_svc_exec [-u USER] SVC CMD...` | run in a service, stdin attached, no tty |
| `exec_tty` | `mdb_svc_exec_tty` | the same with a terminal when there is one |
| `health` (required) | `mdb_svc_health SVC` | `healthy`, `unhealthy`, `starting`, `none` or `absent` |
| `cp` | `mdb_svc_cp SVC:PATH DEST` | copy a file out of a service |
| `volumes` (required) | `mdb_be_volumes` | the stack's persistent volumes, one per line |
| `volume_rm` | `mdb_be_volume_rm NAME` | delete one (only `make destroy` calls it) |
| `image_load` | `mdb_be_image_load REF` | make a locally built image visible to the backend |
| `secrets_sync` | `mdb_be_secrets_sync` | publish `secrets/` to the backend |
| `info` | `mdb_be_info` | lines for `scripts/runtime info` |
| `forward` | `mdb_be_forward` | reach the services from this machine until interrupted (`scripts/runtime forward`) |

`_mdb_svc_args "$@"` splits `[-u USER] SVC CMD...` for a backend: it sets
`_MDB_SVC_USER` and `_MDB_SVC`, and `shift "$_MDB_SVC_SHIFT"` leaves the command.

Volumes whose names end in `_pitr` or start with `pitr-` are recovery
repositories: `make destroy` keeps them unless told otherwise.

A script that only works on some backends says so first with
`mdb_backend_require compose || exit 64`. Check 52 enforces the contract, and
that nothing outside this layer reaches a service through `compose exec`.
