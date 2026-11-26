# Open follow-ups

What has not been verified yet, what is left for later, and how to close each item. Updated 2026-11-25.

## Not verified yet

| Item | State | How to close it |
|---|---|---|
| Windows (`dbpantry.exe`) | The hand-off to the Linux `dbpantry` inside WSL 2 is covered by unit tests and a cross-build only; no Windows machine ran it | On Windows 11 with WSL 2 and the Linux binary in `~/.local/bin`: `dbpantry.exe version`, `dbpantry.exe up`, and `dbpantry.exe dashboard` opening in a Windows browser ([windows](../runbooks/windows.md)) |
| FreeBSD | The binary builds for amd64 and arm64; it has never been run | Run it with bash and Podman on FreeBSD |
| Incus beyond PostgreSQL | With a separate home, the `pg` profile was run once (2026-11-24/25); `make status` and `make logs` were not repeated in that run. Other engines, HA and PITR have never run on Incus | Repeat the separate-home run with `status`, `logs` and the other profiles ([Incus](../guides/incus.md)) |
| CloudNativePG password rotation (check 61) | Skipped by `make verify` without a Kubernetes cluster | Run `make verify CHECKS=61` against a kind cluster with the CNPG operator ([Kubernetes](../guides/kubernetes.md)) |
| The release workflow | The shared `go-release.yml` and the provenance attestation are proven only on GitHub, at the first tag (design.md D-110) | Push the first `vX.Y.Z-rc.N` tag and check `gh attestation verify` against its assets |
| CI on the latest changes | GitHub Actions did not start jobs from #45 on, so the work since `main` at `f8c0d69` has been verified locally only (full `make verify`: 86 passed, 1 skipped) | The first CI run of the pull request that carries it |

## Left for later, on purpose

| Item | Why |
|---|---|
| `overrides/<engine>/` stays in the kit | It is tracked, operator-tunable configuration that the generated compose file mounts; it moves to the instance home only with a reason of its own (design.md D-84) |
| Some commands have no dashboard button | Interactive commands (`shell`, `tools`, `forward`, `env use`), commands about the binary or the repository (`self-update`, `dev`, `init`), host file paths (`data import --from`, `data export --to`), and the destructive Compose-only `pitr restore` and `upgrade` stay on the command line (design.md D-111) |
| No Git Bash or PowerShell path on Windows | The scripts need a real bash and the Linux filesystem; WSL 2 is the one supported route ([windows](../runbooks/windows.md)) |
| The dashboard does not measure the container runtime on each refresh | Each refresh stays cheap; a runtime that does not answer shows as unavailable on the page, and `dbpantry doctor` measures it ([API](api.md)) |

## Closing an item

Fix it with a failing check first, as for any bug (`CLAUDE.md`), update the page that describes it, and remove its row here in the same change.

---

[← Docs index](../../README.md#documentation)
