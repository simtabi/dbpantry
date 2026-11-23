# Dashboard

The web UI served by `dbpantry dashboard`. It shows the stack and runs the same
commands as the command line, as jobs whose output streams into a drawer
(design.md D-86, `.claude/design/dbpantry/management-plan.md`).

## Start it

```bash
dbpantry dashboard
# or, after init / start:
make dashboard
```

Default listen address: `127.0.0.1:8484`. Prefer `dbpantry dashboard` or
`make dashboard` from a normal shell (or `dbpantry start`); the binary itself
puts Docker Desktop and other CLIs on PATH for its scripts, so a thin agent
PATH no longer blanks the Services page (design.md D-88).

**Open the printed URL on `localhost` or `127.0.0.1` only.** The dashboard
refuses any other `Host` (DNS rebinding). A proxy hostname such as
`*.test:8484` answers `421 Misdirected Request`.

With auth mode `token` (the default), the process prints a one-time URL:

```text
http://127.0.0.1:8484/?token=…
```

Open that link once. Reloads keep the session cookie. For a local demo with no
sign-in (loopback only):

```bash
make demo-dashboard          # detached AUTH=none; survives the terminal
make demo-dashboard-stop     # free the port
scripts/demo-dashboard status
# foreground (dies with the shell):
make dashboard AUTH=none MDB_HOME=/tmp/mdb-live
```

Demo URL (no token): **http://127.0.0.1:8484/** — open that exact address in
the browser (not a rewritten host). `LISTEN=0.0.0.0:8484` still prints
`http://127.0.0.1:8484/` so the link is clickable. The detached demo uses
`scripts/demo-dashboard` (`start_new_session`) so an agent/make SIGHUP does
not take it down.

## Navigation

The menu is five groups, not a flat list of every page:

| Group | Contains |
|---|---|
| **Overview** | The home page |
| **Stack** | Services, Images, and every engine (enabled first) |
| **Data** | **Databases & users** (hosting-style wizard), Projects (badge = password-file count; orphaned listed), Backups |
| **Operate** | Operations, **Add-ons**, **System** (`doctor`), Clients (connection details, project `.env`, provision, verify), Configuration, Jobs, Status |
| **Connect** | How an application reaches this stack |
| **Help** | In-app Help and About, language switcher (en/es/fr/de/pt/ja), shells, TLS, status JSON, CLI and licensing docs on GitHub |

## What each page does

| Page | Shows | Actions |
|---|---|---|
| Overview | CRM-style home: welcome lead, eight KPI cards, stack funnel, needs-attention list, services, connect, projects, recent jobs | Start / stop the stack, run checks, provision, open Status |
| Services | Containers, fixture | Restart, logs, copy a `dbpantry shell` command, fixture up/down |
| Images | Local builds, grouped by prefix | Build, scan |
| Engines / one engine | Endpoints, principals, pooler state. The page header holds the one Start; the status strip under it explains the state and opens Add-ons | New project, backup, restart, pooler sync, rotate this engine's passwords or every engine's (`secrets.rotate`, typed ROTATE; this engine's button is disabled until it runs) |
| Projects / one project | Active, orphaned, unmanaged; extensions the engine reports. Rows use a ⋮ menu (connection, backup, rotate, drop). Unmanaged databases can be adopted via `project.new`. The Data badge counts every password-file project and the dropdown lists them all (orphaned marked, no connection link). Passwords blur until **Show password** (Live pauses; never logged) | New, rotate, drop, backup, adopt, connection `.env`, reveal |
| Databases & users | Shared-hosting wizard over the same projects: provision steps, privileges matrix (owner / readonly), rename = create under new name then drop, link to password reveal (design.md D-97) | `project.new`, rotate, drop, adopt |
| Add-ons | Multi-step wizard: pick engine(s) in Single or Multiple mode → filter catalog (pooler/extension/embed/ui) → review queued `dbpantry addon …` argv → apply as a job. Unsupported rows stay listed but cannot enable. Deep-links `?engine=pg` (single) and `?engines=pg,mysql` (multi). Status shows supported+enabled only (design.md D-96) | `addon.enable` / `addon.disable` |
| Environments | Named instance homes by type (localdev / dev / test / qa / uat / trn / spt / stg / preprod / demo / sandbox / prod). Create, copy/migrate a database between homes, unregister. Concurrent *stacks* = separate homes. CLI: `dbpantry env …` (design.md D-100) | `env.init`, `env.copy`, `env.migrate`, `env.remove`; also `backup.run` / `data.import` / `data.export` on this home |
| Primary & replicas | `/ha`: PostgreSQL primary (writes) and replicas (reads) via the `ha` profile (Patroni rehearsal). Prefer primary/replica wording. Concurrent *engines* in one stack = several tokens in `MDB_PROFILES` (e.g. `pg,mysql,mariadb,cassandra`) | `ha.status`, `ha.failover`, `ha.reinit`; `stack.up` with profiles |
| System | Host, binary and runtime facts (`dbpantry doctor`, design.md D-93) | — |
| Help / About | Localised chrome (en/es/fr/de/pt/ja); About shows version, commit, MIT text, UI notices and engine licences | Language cookie (`mdb_lang`) |
| Backups | Dumps, PITR on/off, last verify | Back up, verify, restore, PITR info/backup |
| Connect | Publish modes (`#publish` cards), every engine's address (copy in Quick access), compose `networks:` snippet, TLS CA, UI front ends when `ui` is up, and **Run from this dashboard** (scoped jobs) | Switch `MDB_PUBLISH` + Start to apply; ⋮ engine menu; `project.new`, `stack.start`, `up`/`down`, `verify`; shell commands stay Copy (+ Logs) |
| Page header → history | Back / Forward for in-app navigation (session stack) | — |
| Header → Instance menu | Short links: Configuration, Connect, About, Status JSON, WebUIs, sign-in | — |
| Configuration | Instance summary (profiles, network, env file) plus every `MDB_` key with origin (`env` / `file` / `unset`) | Validate, render, set a key; switch profiles / subnet / project name |
| Connect | Publish modes (`MDB_PUBLISH`) with mode alerts, engine addresses (copy in Quick access), equal-height join/TLS, scoped jobs | Switch publish mode, Start to apply, copy addresses, provision |
| Status | Health %, services up/down, engines, images/projects, jobs, Live badge legend | Open JSON, verify, deep-link services/engines |
| Operations | Security, topology, health, destroy, API tokens | Certs, secrets rotate, HA, pooler, verify, destroy |
| Jobs | In-memory jobs and the audit log | Re-open a job's output |

Every button that **changes** something opens a confirm modal first (warning for
operate actions; typed confirmation for destructive ones — the same value the
CLI checks). Missing parameters are collected in that modal. Output streams in
a slide-in job drawer as the same step tree, progress bars, percentage and ETA
the CLI draws on a terminal (jobs run with `--pretty`, not a BuildKit wall).
The drawer bar tracks `%` from those lines. While a job is running the drawer
stays open (close, Escape and backdrop are blocked) so progress keeps
streaming; a reload warns and re-opens the job from `sessionStorage` if it is
still running; the confirm modal likewise stays put until the action has
started. The Live badge pauses or resumes the five-second refresh and shows
**Live / Paused / Retrying / Disconnected** by colour (design.md D-92). Every
badge the dashboard draws (engine, health, project, job, Live) is explained,
with what to do next, in [What the statuses mean](statuses.md) and on
**Help › What the statuses mean**. Add-ons
and Databases keep Live off so the wizards keep their step and form state.
The header shows a stats dropdown (services / engines / images) beside Live, plus
a notifications bell; Back uses history when it can; the page-header
Back/Forward pair uses a session stack so Forward works after Back.
`/status` is the dedicated health page. First paint shows a short splash with
percentage and stage labels (`data-dbpantry-*` / design.md D-102), then reveals
the page once the document (and optional `#content`) is ready.
The page never runs a free-form shell: only named actions from
`GET /api/v1/actions` (design.md D-90). Interactive `dbpantry shell …`
commands are copied for your terminal. Every action button and nav link
carries a tooltip or popover that names what it does and which `dbpantry`
command it runs. Short toast messages report start, success, failure and
copies. Read-only jobs with every parameter already known may run without a
modal. Language: `?lang=`, `mdb_lang` cookie, then `Accept-Language`
(design.md D-91). The management API is summarised on `/help` and documented
in [api.md](../reference/api.md) (also `GET /api/v1/actions` and
`GET /api/v1/status` from the browser).

## Authentication and settings

Shipped defaults: `config/dashboard.json`. The instance may override them with
`dashboard.json` in the home, then `MDB_DASHBOARD_*` in `.env`, then flags:

| Setting | Values | Notes |
|---|---|---|
| `auth` | `token`, `password`, `none` | `none` is refused off loopback |
| `allow_destroy` | bool | Volume deletion off unless true |
| `listen` | address | Overridden by `--listen` |
| `confirm` | confirmation policy | Destructive actions still need the CLI's typed value |

Password mode reads `secrets/dashboard_password.txt`. Create API tokens with
`dbpantry api token create` or on Operations → API tokens.

## Instance home

The dashboard reads and writes the instance home (`MDB_HOME` / `--home` /
`~/.config/dbpantry` when running from the embedded kit): `.env`, `secrets/`,
`certs/`, `backups/`, `audit/dashboard.jsonl`, `api-tokens.json`. It never
edits the kit (design.md D-84, D-85).
