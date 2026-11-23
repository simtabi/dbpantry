# What the statuses mean

The dashboard draws a small badge wherever something has a state: an engine,
a service's health check, a project, a job, and the Live badge in the header.
This page lists every one, what it says about the stack, and what to do next.
The dashboard shows the same table under **Help › What the statuses mean**
(`/help#statuses`), and each badge's tooltip carries its meaning.

## How an engine's status is worked out

An engine's status comes from two facts: whether it is in `MDB_PROFILES` (the
engines `dbpantry up` starts), and whether its container is running now.

| In `MDB_PROFILES` | Container running | Status |
|---|---|---|
| yes | yes | `running` |
| yes | no | `stopped` |
| no | yes | `leftover` |
| no | no | `disabled` |

`disabled` never deletes anything: an engine's data volume stays until you
remove it with `dbpantry destroy`. To see the same facts in a terminal,
`dbpantry status` lists the containers and their health, and
`dbpantry endpoints` lists every engine with whether its profile is enabled.

## Every badge

<!-- states:start (generated from src/internal/dashboard/states.go: MDB_UPDATE_DOCS=1 go -C src test ./internal/dashboard/ -run TestStatusGuideMatchesTheDocs) -->

### Engines

Shown on Stack › By engine, Engines, an engine's page, Overview.

| Badge | Meaning | What to do |
|---|---|---|
| `running` | In MDB_PROFILES and its container is up, so it answers on the addresses its page lists. | Nothing. |
| `stopped` | In MDB_PROFILES, but its container is not running: the stack is down, the container exited, or it has not started yet. | Start the stack (dbpantry up). If it stops again, read dbpantry logs ENGINE. |
| `leftover` | Its container is still running, but the engine is no longer in MDB_PROFILES: the profile was removed after it started. It still answers, but dbpantry up no longer manages it. | Add it back to MDB_PROFILES, or stop it with dbpantry service stop ENGINE. Its data volume stays. |
| `disabled` | Not in MDB_PROFILES and no container is running. Nothing is lost: its data volume, if it ever had one, is kept. | Use “Start with …” on its page, or add it to MDB_PROFILES and run dbpantry up. |

### Service health

Shown on Services, Status, the header counters.

| Badge | Meaning | What to do |
|---|---|---|
| `healthy` | The container's health check passes. | Nothing. |
| `starting` | The container is up and its health check has not passed yet. Engines get a boot budget (90s, or 300s for heavy ones) before this counts as a failure. | Wait. If it stays here past the budget, read dbpantry logs SERVICE. |
| `unhealthy` | The health check keeps failing, so the service is up but not answering as it should. | Read dbpantry logs SERVICE, then dbpantry restart SERVICE. |
| `no check` | Running, and the image declares no health check, so running is all dbpantry can say. | Nothing. |

### Projects

Shown on Projects, Databases & users, an engine's page.

| Badge | Meaning | What to do |
|---|---|---|
| `active` | The database exists and its password files are in secrets/. | Nothing. |
| `orphaned` | Password files are in secrets/, but the engine has no such database: it was dropped outside dbpantry, or the volume was replaced. | Provision it again (dbpantry project new) or drop the files (dbpantry project drop). |
| `unmanaged` | The engine has this database, but no password file names it: dbpantry did not create it. | Adopt it with dbpantry project new --name NAME to give it dbpantry's roles. |
| `unknown` | The engine is not running or did not answer, so dbpantry cannot tell whether the database exists. | Start the engine; the state settles on the next refresh. |

### Jobs

Shown on the job drawer, Jobs, Overview's recent activity.

| Badge | Meaning | What to do |
|---|---|---|
| `running` | The command is still running. One mutating job runs at a time. | Nothing. |
| `succeeded` | The command exited 0. | Nothing. |
| `failed` | The command exited non-zero; the badge shows the exit code and the drawer opens on the failure. | Read the failure banner, fix the cause, then Retry. |

### Live badge

Shown on the header, beside the history arrows.

| Badge | Meaning | What to do |
|---|---|---|
| `Live` | The page refreshes its data every 5 seconds. | Nothing. |
| `Paused` | You paused it, or the page keeps a form or a password on screen and never refreshes on its own. | Click the badge to resume, where the page allows it. |
| `Retrying` | One or two refreshes failed in a row; it keeps trying. | Nothing. |
| `Disconnected` | Three refreshes failed in a row: the dashboard process stopped or the network dropped. | Check the dbpantry dashboard process, then click the badge to retry. |

<!-- states:end -->

## Related

- [Dashboard](dashboard.md): every page and the actions it offers.
- [Configuration](configuration.md): `MDB_PROFILES` and the other keys.
- [CLI reference](../reference/cli.md): `status`, `endpoints`, `logs`, `service stop`.

---

[← Docs index](../../README.md#documentation)
