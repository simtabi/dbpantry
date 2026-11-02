# Kubernetes

`MDB_BACKEND=kubernetes` deploys the engines to a Kubernetes cluster with the
Helm chart in [deploy/helm/dbpantry](../../deploy/helm/dbpantry), configured
from the same `.env` as Compose (design.md D-64). `make up`, `make down`,
`make psql`, `make backup`, `make restore` and `make destroy` work the same
way; they reach the databases through `kubectl exec` instead of
`compose exec`.

It works on local clusters (OrbStack, kind, Docker Desktop, Rancher Desktop)
and on real ones. Tested: OrbStack's built-in cluster (Kubernetes 1.35) and
kind 0.33 (all five engines on kind; PG and MySQL on both).

## Requirements

| Tool | Version | Notes |
|---|---|---|
| kubectl | within one minor version of the cluster | on PATH |
| Helm | 3.x or 4.x | on PATH; tested with 4.3.1 |
| kind | 0.20 or later | only for kind clusters, to load the local images |
| A StorageClass | any `ReadWriteOnce` class | the cluster's default is used unless `MDB_K8S_STORAGE_CLASS` names one |

## Quick start

```bash
# .env
MDB_BACKEND=kubernetes
MDB_K8S_CONTEXT=orbstack          # required: `kubectl config get-contexts`
MDB_PROFILES="pg,mysql"

make build    # local images (skip on a real cluster that pulls from GHCR)
make up       # secrets, certs and overrides, then helm upgrade --install --wait
make psql
make backup   # dumps stream through kubectl exec into backups/, as on Compose
```

`MDB_K8S_CONTEXT` has no default on purpose. A backend that deployed to
whatever context happened to be current would, sooner or later, deploy a
development database to production.

| Setting | Default | What it does |
|---|---|---|
| `MDB_K8S_CONTEXT` | none, required | kubeconfig context; `KUBECONFIG` is honoured |
| `MDB_K8S_NAMESPACE` | `COMPOSE_PROJECT_NAME` | namespace, created when missing |
| `MDB_K8S_RELEASE` | `COMPOSE_PROJECT_NAME` | Helm release name |
| `MDB_K8S_STORAGE_CLASS` | the cluster's default | StorageClass of every volume |
| `MDB_K8S_VALUES` | none | extra values files, colon-separated, applied last |
| `MDB_K8S_LOAD_IMAGES` | `auto` | copy local images into the cluster: `auto` on `kind-*` contexts only |

## What `make up` does

1. `scripts/helm-values` renders a values file from `.env`: `MDB_PROFILES`
   enables engines, `MDB_<ENGINE>_VERSION` is the image tag,
   `MDB_<ENGINE>_MEM_LIMIT` and `_CPU_LIMIT` become requests and limits, and
   every variable the engine's Compose service passes is passed the same way,
   resolved the way Compose resolves it (environment, then `.env`, then the
   default). Every value is a double-quoted string.
2. Publishes `secrets/*.txt` as the Secret `dbpantry-secrets` (mounted at
   `/run/secrets`, as Compose bind-mounts it), `certs/<engine>/` plus
   `certs/ca.crt` as `dbpantry-certs-<engine>` (the CA key never leaves the
   checkout), and `overrides/<engine>/` as the ConfigMap
   `dbpantry-overrides-<engine>`. It uses `kubectl apply`, so running it again
   after `make rotate-secrets` updates them.
3. On a kind context, `kind load docker-image` for each locally built engine
   image. OrbStack and Docker Desktop share Docker's image store already; a
   real cluster pulls from the registry, so push your images there or use the
   published ones on GHCR.
4. `helm upgrade --install --wait` with that values file and any
   `MDB_K8S_VALUES`.

`make down` uninstalls the release and waits for the pods to stop. The
PersistentVolumeClaims stay (`persistentVolumeClaimRetentionPolicy: Retain`),
so `make up` brings the same data back. Only `make destroy` deletes them, with
the same typed confirmation as on Compose, and it keeps the `pitr-*` recovery
volumes unless you add `--include-recovery`.

## What gets deployed

One StatefulSet per enabled engine, with one replica:

| Object | Name | Notes |
|---|---|---|
| Service | `pg`, `mysql`, `mariadb`, `mongodb`, `cassandra` | the Compose service names, so apps use the same host names; `service.prefixWithRelease: true` prefixes them |
| Headless Service | `<release>-<engine>-hl` | stable pod DNS |
| StatefulSet | `<release>-<engine>` | probes are the image healthchecks |
| PVCs | `data-<release>-<engine>-0`, `pitr-<release>-<engine>-0` | `pitr` for PG, MySQL and MariaDB |
| NetworkPolicy | `<release>-dbpantry` | only pods in the namespace, plus `networkPolicy.extraFrom` |
| ServiceAccount | `<release>-dbpantry` | no API token mounted |

The containers run with the same hardening as the Compose services: no
privilege escalation, every capability dropped except the five the init needs
(`CHOWN`, `SETUID`, `SETGID`, `DAC_OVERRIDE`, `FOWNER`), and the
`RuntimeDefault` seccomp profile. That passes the Pod Security "baseline"
level. It does not pass "restricted", because the images start as root and
drop to the engine's user, exactly as they do under Compose.

## Reaching the databases

Inside the cluster, connect to the Service: `pg.<namespace>.svc:5432`. From
your machine:

```bash
scripts/runtime forward   # kubectl port-forward each engine to 127.0.0.1:MDB_<ENGINE>_HOST_PORT
```

The Services are `ClusterIP`. Set `service.type: LoadBalancer` in an
`MDB_K8S_VALUES` file to publish them, and only with `MDB_TLS_ENFORCE=true`.

## Differences from Compose

- **Not on Kubernetes yet:** the poolers, the UIs (Adminer, CloudBeaver,
  Caddy), the exporters, the scheduled backup sidecar and HA. `make up` names
  any profile it skips. `make pitr` restore and `make upgrade` need the
  Compose backend and refuse to run here.
- **No Ingress.** The database protocols are not HTTP, so an Ingress cannot
  carry them; use a Service of type LoadBalancer, or a TCP route in your
  gateway.
- **No `nofile` ulimit.** Kubernetes cannot set per-container ulimits.
  `MDB_MYSQL_NOFILE` and `MDB_MARIADB_NOFILE` take effect only where the
  container runtime's default allows it; raise the node or runtime default if
  MySQL warns about open files.
- **A crash-looping pod blocks its own fix.** If an engine fails to start and
  you change its settings, a StatefulSet does not replace a pod that never
  became ready. Delete the pod
  (`kubectl delete pod <release>-<engine>-0`) and the new one uses the new
  settings. This is standard StatefulSet behaviour, not specific to dbpantry.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `needs MDB_K8S_CONTEXT` | set it in `.env`; see `kubectl config get-contexts` |
| Helm says `TLS handshake timeout` while kubectl works | the API server name resolves to an IPv6 address that does not answer, and Helm does not fall back to IPv4. Seen with OrbStack's `k8s.orb.local`. Point a kubeconfig context at `https://127.0.0.1:26443` with `tls-server-name: k8s.orb.local` |
| `ErrImagePull` on kind | build the images first, or set `MDB_K8S_LOAD_IMAGES=true` |
| MongoDB: `Permission denied` on the password file | the chart mounts MongoDB's Secret `0444` because its entrypoint reads it as `mongodb`; keep `engines.mongodb.secretMode` |
