# Management API

The dashboard's HTTP API. Every mutating call runs one `dbpantry` command as a
job (design.md D-86). The pages are clients of this API; nothing the UI can do
is missing from here, and nothing here is missing from the command line
([cli.md](cli.md)).

Base URL: the dashboard's listen address (default `http://127.0.0.1:8484`).
Content type for JSON bodies: `application/json`.

## Authentication

Three modes, from `config/dashboard.json` / the instance's `dashboard.json` /
`MDB_DASHBOARD_*` / flags (later layers win):

| Mode | Browser | Scripts |
|---|---|---|
| `token` (default) | Open the one-time URL `dbpantry dashboard` prints (`?token=…`). It becomes an `HttpOnly` `SameSite=Strict` session cookie. | `Authorization: Bearer dbpantry_…` |
| `password` | Sign in at `/login` with `secrets/dashboard_password.txt`. | Same Bearer tokens |
| `none` | Loopback only: the first GET gets a session (for CSRF). Refused on a non-loopback listen. | Same Bearer tokens |

Browser mutations (any method but GET/HEAD) also need, in every mode:

- `Sec-Fetch-Site: same-origin`, or, from a browser that sends no
  `Sec-Fetch-Site`, an `Origin` header naming this host (a request with
  neither is refused), and
- the session's CSRF token in `X-CSRF-Token` (the layout's `<meta name="csrf-token">`).

Signing in uses `GET /login` (the form) and `POST /login` (password mode;
same-origin only, since there is no session yet), and `POST /logout` ends the
session. After signing in the browser returns to `?next=`, a path on this site
only.

API tokens: `dbpantry api token create NAME --scope read|operate|admin`. Shown once,
stored as SHA-256, expiry required. Scopes nest: `admin` ⊃ `operate` ⊃ `read`.

## Status

### `GET /api/status` · `GET /api/v1/status`

The stack snapshot as JSON: services, engines, projects, backups, images, PITR
status, configuration entries. No passwords. Scope: any authenticated principal.

## Actions and choices

### `GET /api/v1/actions`

Every action the dashboard knows, with `allowed` and `reason` for this principal.
Scope: any authenticated principal.

```json
[
  {
    "name": "project.new",
    "title": "Provision a project",
    "area": "projects",
    "scope": "operate",
    "destructive": false,
    "command": "dbpantry project new --name <name> --engine <engine>",
    "params": [
      {"name": "name", "label": "Project (database) name", "kind": "name", "required": true},
      {"name": "engine", "label": "Engine", "kind": "engine", "required": true}
    ],
    "allowed": true,
    "reason": ""
  }
]
```

### `GET /api/v1/choices`

Values an action's parameters may name right now. Engines list the running ones
first; `running` names them. `image` lists every image the kit builds (each
`images/<name>/Dockerfile`), which `images.build` takes in its `engine`
parameter, kept under that name so existing callers work. `profile` lists
Compose profile tokens for the multi-select on Start / Manage profiles. Scope:
any authenticated principal.

```json
{
  "engine": ["pg", "mysql"],
  "image": ["caddy", "cassandra", "cli", "mariadb", "mysql", "patroni", "pg", "pgbouncer"],
  "running": ["pg"],
  "service": ["pg", "caddy"],
  "volume": ["mdb_pg_data"],
  "dump": ["pg_settings_20261117T000000Z.dump"],
  "profile": ["backup", "ha", "mysql", "mysql-pooler", "pg", "pg-pooler", "pooler", "ui"],
  "env-type": ["localdev", "dev", "test", "qa", "uat", "trn", "spt", "stg", "preprod", "demo", "sandbox", "prod"],
  "env-name": ["shop-dev", "shop-uat"]
}
```

`env-type` and `env-name` feed the Environments actions (`env.init`, `env.copy`,
`env.migrate`, `env.remove`; design.md D-100). Names come from the operator
registry (`~/.config/dbpantry/envs.json`).

## Jobs

### `POST /api/v1/jobs`

Start one action. One mutating job at a time; a second gets `409` naming the
running job. Destructive actions need `confirm` equal to the value the CLI
would ask for. Scope: the action's own scope.

Request:

```json
{
  "action": "project.drop",
  "params": {"name": "shop", "engine": "pg"},
  "confirm": "shop"
}
```

Response `202`:

```json
{
  "id": "…",
  "action": "project.drop",
  "args": ["project", "drop", "--name", "shop", "--engine", "pg", "--confirm", "shop"],
  "actor": "session",
  "state": "running",
  "started": "2026-11-22T10:00:00Z"
}
```

### `GET /api/v1/jobs`

Jobs in memory, then the audit log. Scope: read.

### `GET /api/v1/jobs/{id}`

One job, including output lines so far. Scope: read.

### `GET /api/v1/jobs/{id}/events`

Server-sent events while the job runs:

| Event | Data |
|---|---|
| `line` | `{"line": "…", "t": 1763900000123}` — one output line and when the job wrote it (unix ms) |
| `end` | the finished job as JSON |

Dashboard jobs run the CLI with `--events`, so pretty commands (`up`, `down`,
`project new`) also print `::dbpantry-run:: {json}` lines: a snapshot of the
whole run tree (steps, groups, resources, states, timings, progress, failure
cause, Connect rows, Next steps), repeated when it changes and once at the end.
The drawer renders the newest snapshot and hides those lines from the raw log
(design.md D-106). Other clients may ignore any line with that prefix.

Scope: read.

### `POST /api/v1/jobs/{id}/control`

Pause, continue, abort or restart a job (design.md D-98). Scope: the job's
action's (an operate token cannot control an admin job). A restart is a new run
of the action: for a destructive one it needs the typed confirmation again, and
it is refused for a volume delete while `allow_destroy` is off.

```json
{ "op": "abort" }
{ "op": "restart", "confirm": "ROTATE" }
```

`op` is one of `abort` (alias `stop`), `pause`, `continue` (alias `resume`),
`restart`. Pause stalls the command on its next output write; abort cancels
its context (dashboard jobs kill child scripts). Restart aborts an active job
first, then starts the same action again (`202` + `Location`).

Job states: `running` · `paused` · `succeeded` · `failed` · `cancelled`.

## Events and signed webhooks

Projects and outside systems talk to dbpantry through the Bearer API above.
dbpantry talks back through **signed outbound webhooks** and a live event
stream (design.md D-99). There is no inbound webhook receiver — that would be
a second auth surface; call `POST /api/v1/jobs` with a token instead.

### `GET /api/v1/events` · `GET /api/v1/events/stream`

Recent in-memory events (newest first; `?limit=` up to 500), and an SSE stream
of the same. Event types include `job.started`, `job.succeeded`, `job.failed`,
`job.cancelled`, `job.paused`, `job.continued`, `alert.raised`. Scope: read.

### `GET /api/v1/hooks` · `POST /api/v1/hooks` · `DELETE /api/v1/hooks/{id}`

Admin-only webhook subscriptions. Create returns the signing secret **once**.
The Operations page manages the same list for an admin: New webhook shows the signing secret once, and each row has Delete.
Endpoints live in `$MDB_HOME/webhooks.json` (0600).

```json
{ "url": "https://app.example/hooks/dbpantry", "events": ["job.failed", "job.succeeded"] }
```

Empty `events` means all. Delivery is `POST` JSON with:

| Header | Meaning |
|---|---|
| `X-Dbpantry-Event` | event type |
| `X-Dbpantry-Delivery` | event id |
| `X-Dbpantry-Timestamp` | unix seconds |
| `X-Dbpantry-Signature` | `sha256=` + hex HMAC-SHA256(secret, `timestamp + "." + body`) |

HTTPS only (plain `http://` allowed solely for loopback). Verify with a
five-minute timestamp skew. Secrets never appear in list responses.

## Logs

### `GET /api/v1/services/{service}/logs`

Server-sent events of a service's log (`dbpantry logs SERVICE`). Scope: read.

## Tokens

### `GET /api/v1/tokens` · `POST /api/v1/tokens` · `DELETE /api/v1/tokens/{id}`

List, create and revoke API tokens. Create returns the plaintext once. Scope: admin.

## Pages (HTML)

The same authentication gates the HTML pages: `/`, `/status`, `/system`,
`/services`, `/services/{service}/logs`, `/images`, `/engines`,
`/engines/{engine}`, `/projects`, `/projects/{engine}/{name}`, `/databases`,
`/backups`, `/addons`, `/envs`, `/ha`, `/connect`, `/config`, `/operations`,
`/jobs`, `/help`, `/about`. A partial refresh sends `X-Partial: 1` and receives
the page content without the layout.

A project's page carries its password, so it needs the operate scope; a job's
detail and event stream need its action's scope, since a job's output can carry
what only that scope may see (the block `project new` prints). `/operations`
lists API tokens to an admin only. `src/internal/dashboard`'s
`TestEveryRouteIsDocumented` fails when a route is registered and missing here.

## Errors

JSON bodies use `{"error": "…"}`. Common statuses: `401` unauthenticated, `403`
wrong scope / CSRF / cross-site, `404` unknown job or page, `409` another job is
running, `422` bad parameters or confirmation.
