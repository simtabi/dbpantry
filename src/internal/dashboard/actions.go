package dashboard

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/simtabi/dbpantry/src/internal/config"
	"github.com/simtabi/dbpantry/src/internal/envtype"
)

// Values are an action's parameters, by name, as the page or an API client
// sent them, after validation.
type Values map[string]string

// Kinds of parameter, each with its own validation (validate below). Names
// and identifiers are checked against a pattern before they reach a command
// line; an engine or a service must also be one the stack has.
const (
	KindEngine   = "engine"
	KindImage    = "image" // an image the kit builds: images/<name>/Dockerfile
	KindService  = "service"
	KindName     = "name"     // a database or project name
	KindKey      = "key"      // an MDB_ configuration key
	KindText     = "text"     // a free-form value (no control characters)
	KindProfiles = "profiles" // a comma-separated profile list
	KindChecks   = "checks"   // a comma-separated list of check numbers
	KindVolume   = "volume"
	KindDump     = "dump" // a dump file in the backup directory, by name
	KindNode     = "node"
	KindPITRType = "pitr-type"
	KindEnvType  = "env-type" // localdev, dev, test, qa, uat, prod, …
	KindEnvName  = "env-name" // registered environment name
)

// Param is one of an action's inputs.
type Param struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
}

// Action is one thing the dashboard can do. Every action is one existing
// dbpantry command (Command), run as a job through the same binary, so the
// dashboard never implements an operation a second time (management-plan.md
// §3, design.md D-86).
type Action struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Area  string `json:"area"`
	// Scope is the least scope that may run it.
	Scope Scope `json:"scope"`
	// Destructive actions need the CLI's typed confirmation, which the
	// server checks before the job starts (decision 3).
	Destructive bool `json:"destructive"`
	// Destroy actions delete data volumes. They are refused unless
	// allow_destroy is set (§8.7: destructive routes off unless enabled).
	Destroy bool    `json:"destroy"`
	Params  []Param `json:"params"`
	// ConfirmHint says what to type for a destructive action.
	ConfirmHint string `json:"confirm_hint,omitempty"`
	// Command is the dbpantry command line for validated values.
	Command func(Values) []string `json:"-"`
	// ConfirmValue is the typed value a destructive action needs; env looks
	// up a setting the value depends on (the HA cluster's name).
	ConfirmValue func(v Values, env func(string) string) string `json:"-"`
	// Secret names the flags whose values never reach the audit log.
	Secret []string `json:"-"`
	// Mask, when set, hides a positional value in the arguments as shown
	// and audited (Redact handles flags only); the command gets them whole.
	Mask func(shown []string, v Values) []string `json:"-"`
}

// optional appends flag and value when the value is set.
func optional(args []string, flag, value string) []string {
	if value == "" {
		return args
	}
	return append(args, flag, value)
}

// Actions are the dashboard's actions, by area (management-plan.md §3.1).
// Each is declared on its own, so `grep 'Name: "stack.up"'` finds it.
var Actions = []Action{
	// --- Stack ---------------------------------------------------------------
	{
		Name: "stack.up", Title: "Start the stack", Area: "stack", Scope: ScopeOperate,
		Params:  []Param{{Name: "profiles", Label: "Profiles (empty: the configured ones)", Kind: KindProfiles}},
		Command: func(v Values) []string { return optional([]string{"up"}, "--profiles", v["profiles"]) },
	},
	{
		Name: "stack.down", Title: "Stop the stack", Area: "stack", Scope: ScopeOperate,
		Command: func(Values) []string { return []string{"down"} },
	},
	{
		// start without serving the dashboard again: the operator is already
		// here. Profiles and an optional project map to `dbpantry start
		// --no-dashboard` (design.md D-81 start composition).
		Name: "stack.start", Title: "Boot and provision", Area: "stack", Scope: ScopeOperate,
		Params: []Param{
			{Name: "profiles", Label: "Profiles (empty: the configured ones)", Kind: KindProfiles},
			{Name: "project", Label: "Project to provision (NAME or NAME:ENGINE; empty: skip)", Kind: KindText},
		},
		Command: func(v Values) []string {
			args := []string{"start", "--no-dashboard"}
			args = optional(args, "--profiles", v["profiles"])
			if p := strings.TrimSpace(v["project"]); p != "" {
				args = append(args, "--project", p)
			}
			return args
		},
	},
	{
		Name: "service.start", Title: "Start a service", Area: "services", Scope: ScopeOperate,
		Params:  []Param{{Name: "service", Label: "Service", Kind: KindService, Required: true}},
		Command: func(v Values) []string { return []string{"service", "start", v["service"]} },
	},
	{
		Name: "service.stop", Title: "Stop a service", Area: "services", Scope: ScopeOperate,
		Params:  []Param{{Name: "service", Label: "Service", Kind: KindService, Required: true}},
		Command: func(v Values) []string { return []string{"service", "stop", v["service"]} },
	},
	{
		Name: "service.restart", Title: "Restart a service", Area: "services", Scope: ScopeOperate,
		Params:  []Param{{Name: "service", Label: "Service", Kind: KindService, Required: true}},
		Command: func(v Values) []string { return []string{"restart", v["service"]} },
	},
	{
		Name: "fixture.up", Title: "Start the fixture", Area: "stack", Scope: ScopeOperate,
		Params:  []Param{{Name: "profiles", Label: "Profiles (empty: the configured ones)", Kind: KindProfiles}},
		Command: func(v Values) []string { return optional([]string{"fixture", "up"}, "--profiles", v["profiles"]) },
	},
	{
		Name: "fixture.down", Title: "Stop the fixture", Area: "stack", Scope: ScopeOperate,
		Command: func(Values) []string { return []string{"fixture", "down"} },
	},

	// --- Images --------------------------------------------------------------
	{
		Name: "images.build", Title: "Build images", Area: "images", Scope: ScopeOperate,
		// "engine" stays the parameter's name, so API callers posting an
		// engine keep working; any image the kit builds is accepted, the
		// pooler, HA, proxy and tools images included.
		Params: []Param{{Name: "engine", Label: "Image (empty: every image)", Kind: KindImage}},
		Command: func(v Values) []string {
			if v["engine"] == "" {
				return []string{"images", "build"}
			}
			return []string{"images", "build", v["engine"]}
		},
	},
	{
		Name: "images.scan", Title: "Scan images", Area: "images", Scope: ScopeOperate,
		Params: []Param{{Name: "engine", Label: "Engine (empty: every engine)", Kind: KindEngine}},
		Command: func(v Values) []string {
			if v["engine"] == "" {
				return []string{"images", "scan"}
			}
			return []string{"images", "scan", v["engine"]}
		},
	},

	// --- Projects ------------------------------------------------------------
	{
		Name: "project.new", Title: "Provision a project", Area: "projects", Scope: ScopeOperate,
		Params: []Param{
			{Name: "name", Label: "Project (database) name", Kind: KindName, Required: true},
			{Name: "engine", Label: "Engine", Kind: KindEngine, Required: true},
		},
		Command: func(v Values) []string {
			return []string{"project", "new", "--name", v["name"], "--engine", v["engine"]}
		},
	},
	{
		// Applications using the old passwords stop connecting, so it asks
		// for the project name as drop does.
		Name: "project.rotate", Title: "Rotate a project's passwords", Area: "projects", Scope: ScopeOperate, Destructive: true,
		Params: []Param{
			{Name: "name", Label: "Project (database) name", Kind: KindName, Required: true},
			{Name: "engine", Label: "Engine", Kind: KindEngine, Required: true},
		},
		ConfirmHint: "the project name",
		Command: func(v Values) []string {
			return []string{"project", "rotate", "--name", v["name"], "--engine", v["engine"]}
		},
		ConfirmValue: func(v Values, _ func(string) string) string { return v["name"] },
	},
	{
		Name: "project.drop", Title: "Drop a project", Area: "projects", Scope: ScopeAdmin, Destructive: true,
		Params: []Param{
			{Name: "name", Label: "Project (database) name", Kind: KindName, Required: true},
			{Name: "engine", Label: "Engine", Kind: KindEngine, Required: true},
		},
		ConfirmHint: "the project name",
		Command: func(v Values) []string {
			return []string{"project", "drop", "--name", v["name"], "--engine", v["engine"]}
		},
		ConfirmValue: func(v Values, _ func(string) string) string { return v["name"] },
	},

	// --- Backups -------------------------------------------------------------
	{
		Name: "backup.run", Title: "Back up", Area: "backups", Scope: ScopeOperate,
		Params: []Param{
			{Name: "engine", Label: "Engine (empty: every database on every engine)", Kind: KindEngine},
			{Name: "db", Label: "Database (empty: every one on the engine)", Kind: KindName},
		},
		Command: func(v Values) []string {
			if v["engine"] == "" {
				return []string{"backup", "run", "--all"}
			}
			return optional([]string{"backup", "run", "--engine", v["engine"]}, "--db", v["db"])
		},
	},
	{
		Name: "backup.verify", Title: "Verify the backups", Area: "backups", Scope: ScopeOperate,
		Command: func(Values) []string { return []string{"backup", "verify"} },
	},
	{
		Name: "backup.restore", Title: "Restore a dump", Area: "backups", Scope: ScopeAdmin, Destructive: true,
		Params: []Param{
			{Name: "engine", Label: "Engine", Kind: KindEngine, Required: true},
			{Name: "db", Label: "Database to replace", Kind: KindName, Required: true},
			{Name: "file", Label: "Dump", Kind: KindDump, Required: true},
		},
		ConfirmHint: "the database name",
		Command: func(v Values) []string {
			return []string{"restore", "--engine", v["engine"], "--db", v["db"], "--file", v["file"]}
		},
		ConfirmValue: func(v Values, _ func(string) string) string { return v["db"] },
	},
	{
		Name: "pitr.info", Title: "Point-in-time recovery window", Area: "backups", Scope: ScopeRead,
		Params:  []Param{{Name: "engine", Label: "Engine (empty: PostgreSQL)", Kind: KindEngine}},
		Command: func(v Values) []string { return optional([]string{"pitr", "info"}, "--engine", v["engine"]) },
	},
	{
		Name: "pitr.backup", Title: "Point-in-time recovery backup", Area: "backups", Scope: ScopeOperate,
		Params: []Param{
			{Name: "engine", Label: "Engine (empty: PostgreSQL)", Kind: KindEngine},
			{Name: "type", Label: "Type: full, diff or incr (empty: the script's)", Kind: KindPITRType},
		},
		Command: func(v Values) []string {
			return optional(optional([]string{"pitr", "backup"}, "--engine", v["engine"]), "--type", v["type"])
		},
	},

	// --- Configuration -------------------------------------------------------
	{
		Name: "config.validate", Title: "Validate the configuration", Area: "config", Scope: ScopeRead,
		Command: func(Values) []string { return []string{"config", "validate"} },
	},
	{
		Name: "config.render", Title: "Render the configuration", Area: "config", Scope: ScopeOperate,
		Command: func(Values) []string { return []string{"config", "render"} },
	},
	{
		// Puts compose.prod.yml into the instance's COMPOSE_FILE (design.md
		// D-73), which check-env asks for before a prod boot. Idempotent, and
		// only the instance's own .env: no parameter names a file.
		Name: "config.prod-overlay", Title: "Add the prod overlay", Area: "config", Scope: ScopeAdmin,
		Command: func(Values) []string { return []string{"config", "prod-overlay"} },
	},
	{
		Name: "config.set", Title: "Set a configuration key", Area: "config", Scope: ScopeAdmin,
		Params: []Param{
			{Name: "key", Label: "Key (MDB_…)", Kind: KindKey, Required: true},
			{Name: "value", Label: "Value", Kind: KindText, Required: true},
		},
		// "--" so a value such as -1 or --help is never read as a flag.
		Command: func(v Values) []string { return []string{"config", "set", "--", v["key"], v["value"]} },
		// A key the Configuration page masks is masked in the job too.
		Mask: func(shown []string, v Values) []string {
			if !config.LooksSecret(v["key"]) {
				return shown
			}
			out := append([]string(nil), shown...)
			for i := range out {
				if out[i] == v["value"] {
					out[i] = "***"
				}
			}
			return out
		},
	},

	// --- Security ------------------------------------------------------------
	{
		Name: "certs.init", Title: "Issue certificates", Area: "security", Scope: ScopeAdmin,
		Command: func(Values) []string { return []string{"certs", "init"} },
	},
	{
		Name: "certs.renew", Title: "Renew certificates", Area: "security", Scope: ScopeAdmin,
		Command: func(Values) []string { return []string{"certs", "renew"} },
	},
	{
		Name: "secrets.rotate", Title: "Rotate passwords", Area: "security", Scope: ScopeAdmin, Destructive: true,
		Params:      []Param{{Name: "engine", Label: "Engine (empty: every running engine)", Kind: KindEngine}},
		ConfirmHint: "ROTATE",
		Command: func(v Values) []string {
			return optional([]string{"secrets", "rotate"}, "--engine", v["engine"])
		},
		ConfirmValue: func(Values, func(string) string) string { return "ROTATE" },
	},

	// --- Topology ------------------------------------------------------------
	{
		Name: "ha.status", Title: "Primary / replica status", Area: "topology", Scope: ScopeRead,
		Command: func(Values) []string { return []string{"ha", "status"} },
	},
	{
		Name: "ha.failover", Title: "Switch primary (failover)", Area: "topology", Scope: ScopeAdmin, Destructive: true,
		Params:      []Param{{Name: "node", Label: "Node to promote (empty: Patroni chooses)", Kind: KindNode}},
		ConfirmHint: "the cluster name",
		Command:     func(v Values) []string { return optional([]string{"ha", "failover"}, "--node", v["node"]) },
		ConfirmValue: func(_ Values, env func(string) string) string {
			if c := env("MDB_HA_CLUSTER_NAME"); c != "" {
				return c
			}
			return "mdb-pg"
		},
	},
	{
		Name: "ha.reinit", Title: "Rebuild an HA replica", Area: "topology", Scope: ScopeAdmin, Destructive: true,
		Params:       []Param{{Name: "node", Label: "Replica", Kind: KindNode, Required: true}},
		ConfirmHint:  "the node name",
		Command:      func(v Values) []string { return []string{"ha", "reinit", "--node", v["node"]} },
		ConfirmValue: func(v Values, _ func(string) string) string { return v["node"] },
	},
	{
		Name: "pooler.sync", Title: "Sync a pooler", Area: "topology", Scope: ScopeOperate,
		Params:  []Param{{Name: "engine", Label: "Engine", Kind: KindEngine, Required: true}},
		Command: func(v Values) []string { return []string{"pooler", "sync", v["engine"]} },
	},
	{
		Name: "addon.enable", Title: "Enable an add-on", Area: "topology", Scope: ScopeOperate,
		Params: []Param{
			{Name: "id", Label: "Add-on id (e.g. pooler.pg, ext.vector)", Kind: KindText, Required: true},
			{Name: "engine", Label: "Engine", Kind: KindEngine},
		},
		Command: func(v Values) []string {
			args := optional([]string{"addon", "enable"}, "--engine", v["engine"])
			return append(args, "--", v["id"])
		},
	},
	{
		Name: "addon.disable", Title: "Disable an add-on", Area: "topology", Scope: ScopeOperate,
		Params: []Param{
			{Name: "id", Label: "Add-on id", Kind: KindText, Required: true},
			{Name: "engine", Label: "Engine", Kind: KindEngine},
		},
		Command: func(v Values) []string {
			args := optional([]string{"addon", "disable"}, "--engine", v["engine"])
			return append(args, "--", v["id"])
		},
	},

	// --- Environments (D-100) ------------------------------------------------
	{
		Name: "env.init", Title: "Create an environment", Area: "environments", Scope: ScopeAdmin,
		Params: []Param{
			{Name: "name", Label: "Name (e.g. shop-uat)", Kind: KindEnvName, Required: true},
			{Name: "type", Label: "Environment type", Kind: KindEnvType, Required: true},
		},
		Command: func(v Values) []string {
			return []string{"env", "init", "--name", v["name"], "--type", v["type"]}
		},
	},
	{
		Name: "env.copy", Title: "Copy a database between environments", Area: "environments", Scope: ScopeAdmin, Destructive: true,
		Params: []Param{
			{Name: "from", Label: "From environment", Kind: KindEnvName, Required: true},
			{Name: "to", Label: "To environment", Kind: KindEnvName, Required: true},
			{Name: "engine", Label: "Engine", Kind: KindEngine, Required: true},
			{Name: "db", Label: "Database", Kind: KindName, Required: true},
		},
		ConfirmHint: "the database name",
		Command: func(v Values) []string {
			return []string{"env", "copy", "--from", v["from"], "--to", v["to"], "--engine", v["engine"], "--db", v["db"]}
		},
		ConfirmValue: func(v Values, _ func(string) string) string { return v["db"] },
	},
	{
		Name: "env.migrate", Title: "Migrate a database between environments", Area: "environments", Scope: ScopeAdmin, Destructive: true,
		Params: []Param{
			{Name: "from", Label: "From environment", Kind: KindEnvName, Required: true},
			{Name: "to", Label: "To environment", Kind: KindEnvName, Required: true},
			{Name: "engine", Label: "Engine", Kind: KindEngine, Required: true},
			{Name: "db", Label: "Database", Kind: KindName, Required: true},
		},
		ConfirmHint: "the database name",
		Command: func(v Values) []string {
			return []string{"env", "migrate", "--from", v["from"], "--to", v["to"], "--engine", v["engine"], "--db", v["db"]}
		},
		ConfirmValue: func(v Values, _ func(string) string) string { return v["db"] },
	},
	{
		Name: "env.remove", Title: "Unregister an environment", Area: "environments", Scope: ScopeAdmin, Destructive: true,
		Params:       []Param{{Name: "name", Label: "Environment name", Kind: KindEnvName, Required: true}},
		ConfirmHint:  "the environment name",
		Command:      func(v Values) []string { return []string{"env", "remove", v["name"]} },
		ConfirmValue: func(v Values, _ func(string) string) string { return v["name"] },
	},

	// --- Health --------------------------------------------------------------
	{
		Name: "verify", Title: "Run the verify harness", Area: "health", Scope: ScopeOperate,
		Params: []Param{{Name: "checks", Label: "Checks, e.g. 21,28 (empty: the fast subset)", Kind: KindChecks}},
		Command: func(v Values) []string {
			if v["checks"] == "" {
				return []string{"verify", "--fast"}
			}
			return []string{"verify", "--checks", v["checks"]}
		},
	},

	// --- Destroy -------------------------------------------------------------
	{
		Name: "destroy.volume", Title: "Delete a data volume", Area: "destroy", Scope: ScopeAdmin, Destructive: true, Destroy: true,
		Params:       []Param{{Name: "volume", Label: "Volume", Kind: KindVolume, Required: true}},
		ConfirmHint:  "the volume name",
		Command:      func(v Values) []string { return []string{"destroy", "--volume", v["volume"]} },
		ConfirmValue: func(v Values, _ func(string) string) string { return v["volume"] },
	},
}

// actionByName finds an action.
func actionByName(name string) (Action, bool) {
	for _, a := range Actions {
		if a.Name == name {
			return a, true
		}
	}
	return Action{}, false
}

var patterns = map[string]*regexp.Regexp{
	KindName:     regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`),
	KindKey:      regexp.MustCompile(`^MDB_[A-Z][A-Z0-9_]{0,62}$`),
	KindText:     regexp.MustCompile(`^[^\x00-\x1f]{1,512}$`),
	KindProfiles: regexp.MustCompile(`^[a-z0-9_-]+(,[a-z0-9_-]+)*$`),
	KindChecks:   regexp.MustCompile(`^[0-9]{1,3}(,[0-9]{1,3})*$`),
	KindVolume:   regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`),
	KindDump:     regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`),
	KindNode:     regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`),
	KindPITRType: regexp.MustCompile(`^(full|diff|incr)$`),
	KindEngine:   regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`),
	KindImage:    regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`),
	KindService:  regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`),
	KindEnvType:  regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`),
	KindEnvName:  regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`),
}

// ErrServicesUnavailable is validate's error for a service parameter while
// the runtime does not answer: an outage, never "no such service".
var ErrServicesUnavailable = errors.New("services are unavailable")

// Choices are the values the stack has for the kinds that must name
// something real. Engines lists the running ones first, and Running names
// them, so a form can default to an engine that is up.
type Choices struct {
	// ServicesErr says why the services could not be read (the runtime did
	// not answer). A service parameter is then unavailable, not unknown.
	ServicesErr string   `json:"services_error,omitempty"`
	Engines     []string `json:"engine"`
	// Buildable are the images the kit builds, by name: every
	// images/<name>/Dockerfile, which scripts/build takes as an argument.
	Buildable []string `json:"image"`
	Running   []string `json:"running"`
	Services  []string `json:"service"`
	Volumes   []string `json:"volume"`
	Dumps     []string `json:"dump"`
	// Profiles are Compose profile tokens the stack can start (engines,
	// poolers, ui, ha, backup…). Forms multi-select them into a CSV.
	Profiles []string `json:"profile"`
	// ActiveProfiles is the instance's current MDB_PROFILES selection, so the
	// form can show what is already on and pre-check when editing.
	ActiveProfiles []string `json:"active_profiles"`
	// EnvTypes are canonical environment type ids (D-100).
	EnvTypes []string `json:"env-type"`
	// EnvNames are registered environment names from envs.json.
	EnvNames []string `json:"env-name"`
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// validate checks every parameter of an action and returns the clean
// values: an unknown parameter, a missing required one, a value that does
// not match its kind, or an engine, service, volume or dump the stack does
// not have is an error naming it.
func (a Action) validate(in map[string]string, c Choices) (Values, error) {
	out := Values{}
	known := map[string]bool{}
	for _, p := range a.Params {
		known[p.Name] = true
		v := strings.TrimSpace(in[p.Name])
		if v == "" {
			if p.Required {
				return nil, fmt.Errorf("%s is required", p.Name)
			}
			continue
		}
		if re := patterns[p.Kind]; re == nil || !re.MatchString(v) {
			return nil, fmt.Errorf("%s %q is not a valid %s", p.Name, v, p.Kind)
		}
		switch p.Kind {
		case KindEngine:
			if !contains(c.Engines, v) {
				return nil, fmt.Errorf("there is no engine %q", v)
			}
		case KindImage:
			if !contains(c.Buildable, v) {
				return nil, fmt.Errorf("the kit builds no image %q", v)
			}
		case KindService:
			if c.ServicesErr != "" {
				return nil, fmt.Errorf("%w: %s", ErrServicesUnavailable, c.ServicesErr)
			}
			if !contains(c.Services, v) {
				return nil, fmt.Errorf("there is no service %q in the stack", v)
			}
		case KindVolume:
			if !contains(c.Volumes, v) {
				return nil, fmt.Errorf("there is no volume %q in the project", v)
			}
		case KindDump:
			if !contains(c.Dumps, v) {
				return nil, fmt.Errorf("there is no dump %q in the backup directory", v)
			}
		case KindEnvType:
			if _, err := envtype.Parse(v); err != nil {
				return nil, err
			}
		case KindEnvName:
			// Pattern-only here: env.init creates a new name; copy/migrate/remove
			// still fail in the CLI when the registry has no such entry. When
			// Choices lists names, prefer them in the form UI (manage.js).
		}
		out[p.Name] = v
	}
	var unknown []string
	for k := range in {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown parameter(s): %s", strings.Join(unknown, ", "))
	}
	return out, nil
}
