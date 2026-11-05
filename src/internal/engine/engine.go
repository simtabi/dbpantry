package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// RequiredKeys are the keys every descriptor must assign, non-empty. The list
// is MDB_ENGINE_REQUIRED_KEYS in scripts/engine-lib.sh; a test fails when the
// two disagree.
var RequiredKeys = []string{
	"MDB_ENGINE_NAME", "MDB_ENGINE_TITLE", "MDB_ENGINE_FAMILY", "MDB_ENGINE_PARADIGM",
	"MDB_ENGINE_VERSIONS", "MDB_ENGINE_DEFAULT_VERSION", "MDB_ENGINE_PORT", "MDB_ENGINE_DATA_DIR",
	"MDB_ENGINE_USER", "MDB_ENGINE_ROOT_SECRET", "MDB_ENGINE_PING", "MDB_ENGINE_CLIENT",
	"MDB_ENGINE_DUMP", "MDB_ENGINE_RESTORE", "MDB_ENGINE_BACKUP_EXT", "MDB_ENGINE_AUTH_METHOD",
	"MDB_ENGINE_POOLING", "MDB_ENGINE_LICENSE", "MDB_ENGINE_OSI_APPROVED",
}

// SecretsDir is where every container reads its secrets: the checkout's
// secrets/ is mounted there read-only. Manifests carry paths under it, never
// values.
const SecretsDir = "/run/secrets"

// Boot budgets, in seconds: how long an engine may take from start to ready.
// They are the budgets check 32 (every-engine-auth) proves every engine meets
// on every run: 90 seconds, or 300 for an engine whose descriptor declares
// MDB_ENGINE_HEAVY=true (Cassandra's gossip round).
const (
	BootBudgetSeconds      = 90
	HeavyBootBudgetSeconds = 300
)

// Pooler is an engine's external connection pooler, when it has one.
type Pooler struct {
	// Service is the Compose service name on the mdb network.
	Service         string `json:"service"`
	Port            int    `json:"port"`
	HostPortDefault int    `json:"host_port_default,omitempty"`
	Image           string `json:"image"`
	SecretFile      string `json:"secret_file,omitempty"`
	RequiredInProd  bool   `json:"required_in_prod"`
	// Superuser is false for every pooler dbpantry ships: pgBouncer's
	// auth query is SECURITY DEFINER and excludes superusers, and ProxySQL
	// mirrors every user except root (config/proxysql/bootstrap.sh). Admin
	// work uses the direct endpoint.
	Superuser bool `json:"superuser"`
}

// Principal is a credential a client may use, by role. Only the path of the
// password file is published, never its contents.
type Principal struct {
	Role         string `json:"role"`
	User         string `json:"user,omitempty"`
	PasswordFile string `json:"password_file"`
}

// Engine is one descriptor, typed, plus the facts the repository adds to it
// (whether its image exists, its pinned bases).
type Engine struct {
	Name             string      `json:"name"`
	Title            string      `json:"title"`
	Family           string      `json:"family"`
	Paradigm         Paradigm    `json:"paradigm"`
	Paradigms        []Paradigm  `json:"paradigms"`
	Versions         []string    `json:"versions"`
	DefaultVersion   string      `json:"default_version"`
	Port             int         `json:"port"`
	PortOffset       int         `json:"port_offset"`
	HostPortDefault  int         `json:"host_port_default,omitempty"`
	DataDir          string      `json:"data_dir"`
	SocketDir        string      `json:"socket_dir,omitempty"`
	User             string      `json:"user"`
	RootSecret       string      `json:"root_secret"`
	RootSecretFile   string      `json:"root_secret_file"`
	RootPasswordEnv  string      `json:"root_password_env,omitempty"`
	AuthMethod       string      `json:"auth_method"`
	AuthOffUpstream  bool        `json:"auth_off_by_default_upstream"`
	TLSAlways        bool        `json:"tls_always"`
	Pooling          string      `json:"pooling"`
	PoolingRationale string      `json:"pooling_rationale,omitempty"`
	Pooler           *Pooler     `json:"pooler,omitempty"`
	Driver           string      `json:"driver,omitempty"`
	License          string      `json:"license"`
	OSIApproved      bool        `json:"osi_approved"`
	LicenseNote      string      `json:"license_note,omitempty"`
	Publish          string      `json:"publish"`
	Implemented      bool        `json:"implemented"`
	Buildable        bool        `json:"buildable"`
	Heavy            bool        `json:"heavy"`
	MinMemoryMB      int         `json:"min_memory_mb,omitempty"`
	BootBudget       int         `json:"boot_budget_seconds"`
	PITRCapable      bool        `json:"pitr_capable"`
	OverridesDir     string      `json:"overrides_dir,omitempty"`
	PITRRepoPath     string      `json:"pitr_repo_path,omitempty"`
	Principals       []Principal `json:"principals"`
	// Bases maps each maintained version to its digest-pinned upstream
	// image (images/bases.tsv).
	Bases map[string]string `json:"bases"`
	// Descriptor is every key of engine.conf, verbatim (N21: the descriptor
	// keys stay a public interface).
	Descriptor map[string]string `json:"descriptor"`
}

// Load reads every engines/*/engine.conf below enginesDir, in the order
// scripts/engine-lib.sh's engine_list uses (by engine name). The repository
// root is enginesDir/.., as it is for engine-lib.sh.
func Load(enginesDir string) ([]Engine, error) {
	confs, err := filepath.Glob(filepath.Join(enginesDir, "*", "engine.conf"))
	if err != nil {
		return nil, err
	}
	if len(confs) == 0 {
		return nil, fmt.Errorf("no engine descriptors in %s", enginesDir)
	}
	root := filepath.Dir(enginesDir)
	bases, err := readBases(filepath.Join(root, "images", "bases.tsv"))
	if err != nil {
		return nil, err
	}
	var (
		out  []Engine
		errs []error
		seen = map[string]string{}
	)
	for _, conf := range confs {
		f, err := os.Open(conf) // #nosec G304 -- the path comes from a glob of the repository's own engines/ directory
		if err != nil {
			errs = append(errs, err)
			continue
		}
		d, err := Parse(f, conf)
		_ = f.Close()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		e, err := fromDescriptor(d, root, bases)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if prior, dup := seen[e.Name]; dup {
			errs = append(errs, fmt.Errorf("%s: engine name %q is already declared by %s", conf, e.Name, prior))
			continue
		}
		seen[e.Name] = conf
		out = append(out, e)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func fromDescriptor(d Descriptor, root string, bases map[string]map[string]string) (Engine, error) {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(d.Path+": "+format, a...)) }
	for _, k := range RequiredKeys {
		if d.Value(k) == "" {
			bad("required key %s is missing or empty", k)
		}
	}
	num := func(key string, required bool) int {
		v := d.Value(key)
		if v == "" {
			if required {
				bad("%s is missing", key)
			}
			return 0
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			bad("%s=%q is not a non-negative integer", key, v)
		}
		return n
	}
	boolean := func(key string) bool {
		switch v := d.Value(key); v {
		case "true":
			return true
		case "", "false":
			return false
		default:
			bad("%s must be true or false, got %q", key, v)
			return false
		}
	}
	secretFile := func(name string) string {
		if name == "" {
			return ""
		}
		return SecretsDir + "/" + name
	}

	e := Engine{
		Name:             d.Value("MDB_ENGINE_NAME"),
		Title:            d.Value("MDB_ENGINE_TITLE"),
		Family:           d.Value("MDB_ENGINE_FAMILY"),
		Paradigm:         Paradigm(d.Value("MDB_ENGINE_PARADIGM")),
		Versions:         strings.Fields(d.Value("MDB_ENGINE_VERSIONS")),
		DefaultVersion:   d.Value("MDB_ENGINE_DEFAULT_VERSION"),
		Port:             num("MDB_ENGINE_PORT", true),
		PortOffset:       num("MDB_ENGINE_PORT_OFFSET", true),
		HostPortDefault:  num("MDB_ENGINE_HOST_PORT_DEFAULT", false),
		DataDir:          d.Value("MDB_ENGINE_DATA_DIR"),
		SocketDir:        d.Value("MDB_ENGINE_SOCKET_DIR"),
		User:             d.Value("MDB_ENGINE_USER"),
		RootSecret:       d.Value("MDB_ENGINE_ROOT_SECRET"),
		RootPasswordEnv:  d.Value("MDB_ENGINE_ROOT_PW_ENV"),
		AuthMethod:       d.Value("MDB_ENGINE_AUTH_METHOD"),
		AuthOffUpstream:  boolean("MDB_ENGINE_AUTH_OFF_BY_DEFAULT_UPSTREAM"),
		TLSAlways:        boolean("MDB_ENGINE_TLS_ALWAYS"),
		Pooling:          d.Value("MDB_ENGINE_POOLING"),
		PoolingRationale: d.Value("MDB_ENGINE_POOLING_RATIONALE"),
		Driver:           d.Value("MDB_ENGINE_DRIVER"),
		License:          d.Value("MDB_ENGINE_LICENSE"),
		OSIApproved:      boolean("MDB_ENGINE_OSI_APPROVED"),
		LicenseNote:      d.Value("MDB_ENGINE_LICENSE_NOTE"),
		Publish:          d.Value("MDB_ENGINE_PUBLISH"),
		Heavy:            boolean("MDB_ENGINE_HEAVY"),
		MinMemoryMB:      num("MDB_ENGINE_MIN_MEMORY_MB", false),
		PITRCapable:      boolean("MDB_ENGINE_PITR_CAPABLE"),
		OverridesDir:     d.Value("MDB_ENGINE_OVERRIDES_DIR"),
		PITRRepoPath:     d.Value("MDB_ENGINE_PITR_REPO_PATH"),
		Descriptor:       d.Map(),
	}
	e.RootSecretFile = secretFile(e.RootSecret)
	if e.Publish == "" {
		e.Publish = "derive" // engine-lib.sh's default
	}

	// The vocabulary (N11): one primary paradigm, plus any the engine also
	// serves (MDB_ENGINE_PARADIGM_ALSO, space-separated; ClickHouse is
	// relational and also time-series, mesobia ADR 0047).
	if e.Paradigm != "" && !e.Paradigm.Valid() {
		bad("unknown paradigm %q (want one of: %s)", e.Paradigm, paradigmList())
	}
	e.Paradigms = []Paradigm{e.Paradigm}
	for _, p := range strings.Fields(d.Value("MDB_ENGINE_PARADIGM_ALSO")) {
		pp := Paradigm(p)
		if !pp.Valid() {
			bad("MDB_ENGINE_PARADIGM_ALSO: unknown paradigm %q (want one of: %s)", p, paradigmList())
		}
		if pp == e.Paradigm {
			bad("MDB_ENGINE_PARADIGM_ALSO repeats the primary paradigm %q", p)
		}
		e.Paradigms = append(e.Paradigms, pp)
	}

	if !contains(e.Versions, e.DefaultVersion) && e.DefaultVersion != "" {
		bad("default version %s is not in VERSIONS (%s)", e.DefaultVersion, strings.Join(e.Versions, " "))
	}

	// engine_implemented: a referenced engine runs the upstream image, a
	// derived one needs its Dockerfile.
	e.Implemented = e.Publish == "reference" || isFile(filepath.Join(root, "images", e.Name, "Dockerfile"))
	e.Buildable = e.Publish == "derive" && e.Implemented

	e.BootBudget = BootBudgetSeconds
	if e.Heavy {
		e.BootBudget = HeavyBootBudgetSeconds
	}

	// The pooler, gated as scripts/gen-compose gates it: external pooling, an
	// image, and a compose-pooler.yml beside the descriptor.
	if e.Pooling == "external" && d.Value("MDB_ENGINE_POOLER_IMAGE") != "" &&
		isFile(filepath.Join(filepath.Dir(d.Path), "compose-pooler.yml")) {
		e.Pooler = &Pooler{
			Service:         e.Name + "-pooler",
			Port:            num("MDB_ENGINE_POOLER_PORT", true),
			HostPortDefault: num("MDB_ENGINE_POOLER_HOST_PORT_DEFAULT", false),
			Image:           d.Value("MDB_ENGINE_POOLER_IMAGE"),
			SecretFile:      secretFile(d.Value("MDB_ENGINE_POOLER_SECRET")),
			RequiredInProd:  boolean("MDB_ENGINE_POOLING_REQUIRED_IN_PROD"),
		}
	}

	// N13: principals by role, as paths. The admin is the engine's root
	// secret; project roles come from Project.
	e.Principals = []Principal{{Role: "admin", PasswordFile: e.RootSecretFile}}

	e.Bases = map[string]string{}
	for _, v := range e.Versions {
		if b := bases[e.Name][v]; b != "" {
			e.Bases[v] = b
		}
	}

	if len(errs) > 0 {
		return Engine{}, errors.Join(errs...)
	}
	return e, nil
}

// ProjectPrincipals are the two roles scripts/new-project creates for a
// project NAME on this engine: the owner (app) and its read-only companion,
// with the password files it writes (secrets/<engine>_<name>_password.txt and
// secrets/<engine>_<name>_readonly_password.txt).
func (e Engine) ProjectPrincipals(project string) []Principal {
	return []Principal{
		{Role: "app", User: project, PasswordFile: SecretsDir + "/" + e.Name + "_" + project + "_password.txt"},
		{Role: "read-only", User: project + "_readonly", PasswordFile: SecretsDir + "/" + e.Name + "_" + project + "_readonly_password.txt"},
	}
}

// readBases parses images/bases.tsv: engine, version, image, whitespace
// separated, # comments, the way engine_pinned_base's awk reads it.
func readBases(path string) (map[string]map[string]string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the repository's own images/bases.tsv
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if out[f[0]] == nil {
			out[f[0]] = map[string]string{}
		}
		if _, dup := out[f[0]][f[1]]; !dup { // awk exits on the first match
			out[f[0]][f[1]] = f[2]
		}
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
