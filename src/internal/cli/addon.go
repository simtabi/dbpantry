package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/addon"
	"github.com/simtabi/dbpantry/src/internal/endpoint"
	"github.com/simtabi/dbpantry/src/internal/engine"
)

func (a *app) addonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "addon",
		Short: "Install and manage poolers, PostgreSQL extensions, embed toggles and UIs",
		Long: `Declarative add-ons the stack already ships (design.md D-96): pgBouncer /
ProxySQL poolers, the PostgreSQL image extension suite, embed exporter/backup
toggles, and UI profile front ends. Unsupported engines (e.g. MongoDB external
pooling) stay listed but cannot be enabled.`,
	}
	cmd.AddCommand(a.addonLsCmd(), a.addonEnableCmd(), a.addonDisableCmd())
	return cmd
}

func (a *app) addonLsCmd() *cobra.Command {
	var engine, out string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List add-ons and whether each is on",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			rows, err := a.addonStatuses(engine)
			if err != nil {
				return err
			}
			if out == "json" {
				enc := json.NewEncoder(a.stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			fmt.Fprintf(a.stdout, "%-28s %-10s %-10s %-6s %s\n", "ID", "ENGINE", "KIND", "ON", "TITLE")
			for _, r := range rows {
				on := "no"
				if !r.Supported {
					on = "n/a"
				} else if r.Enabled {
					on = "yes"
				}
				fmt.Fprintf(a.stdout, "%-28s %-10s %-10s %-6s %s\n", r.ID, r.Engine, r.Kind, on, r.Title)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&engine, "engine", "", "limit to one engine")
	cmd.Flags().StringVarP(&out, "output", "o", "table", "table or json")
	return cmd
}

func (a *app) addonEnableCmd() *cobra.Command {
	var engine, project string
	cmd := &cobra.Command{
		Use:   "enable ID",
		Short: "Enable an add-on (pooler profile, extension list, embed toggle, or ui)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return a.addonSet(c, args[0], engine, project, true)
		},
	}
	cmd.Flags().StringVar(&engine, "engine", "", "engine (required when the add-on spans several)")
	cmd.Flags().StringVar(&project, "project", "", "for extensions: one project DB; empty = every known project on that engine")
	return cmd
}

func (a *app) addonDisableCmd() *cobra.Command {
	var engine string
	cmd := &cobra.Command{
		Use:   "disable ID",
		Short: "Disable an add-on (does not DROP PostgreSQL extensions from existing databases)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return a.addonSet(c, args[0], engine, "", false)
		},
	}
	cmd.Flags().StringVar(&engine, "engine", "", "engine (required when the add-on spans several)")
	return cmd
}

func (a *app) addonStatuses(engineFilter string) ([]addon.Status, error) {
	root, err := a.root()
	if err != nil {
		return nil, err
	}
	m, _, err := a.manifest()
	if err != nil {
		return nil, err
	}
	s, err := a.settings(root)
	if err != nil {
		return nil, err
	}
	cat := addon.Catalog(m.Engines)
	if engineFilter != "" {
		cat = addon.ForEngine(cat, engineFilter)
	}
	profiles := addon.ParseProfiles(s.Effective("MDB_PROFILES"))
	if len(profiles) == 0 {
		profiles = addon.ParseProfiles(s.Effective("COMPOSE_PROFILES"))
	}
	var initExt, preload []string
	for _, x := range strings.Split(s.Effective("MDB_PG_INIT_EXTENSIONS"), ",") {
		if x = strings.TrimSpace(x); x != "" {
			initExt = append(initExt, x)
		}
	}
	for _, x := range strings.Split(s.Effective("MDB_PG_SHARED_PRELOAD"), ",") {
		if x = strings.TrimSpace(x); x != "" {
			preload = append(preload, x)
		}
	}
	engEnabled := map[string]bool{}
	for _, e := range m.Engines {
		engEnabled[e.Name] = profiles.Has(e.Name)
	}
	envTruthy := func(key string) bool {
		v := strings.ToLower(strings.TrimSpace(s.Effective(key)))
		return v == "1" || v == "true" || v == "yes" || v == "on"
	}
	return addon.Evaluate(cat, profiles, initExt, preload, nil, engEnabled, nil, envTruthy), nil
}

func (a *app) addonSet(c *cobra.Command, id, engineName, project string, enable bool) error {
	root, err := a.root()
	if err != nil {
		return err
	}
	m, _, err := a.manifest()
	if err != nil {
		return err
	}
	s, err := a.settings(root)
	if err != nil {
		return err
	}
	spec, ok := addon.ByID(addon.Catalog(m.Engines), id)
	if !ok {
		return usagef("unknown add-on %q (see dbpantry addon ls)", id)
	}
	if !spec.Supported {
		return usagef("%s is not supported: %s", id, spec.Rationale)
	}
	eng := engineName
	if eng == "" {
		if len(spec.Engines) == 1 {
			eng = spec.Engines[0]
		} else {
			return usagef("--engine is required for %s", id)
		}
	}
	found := false
	for _, e := range spec.Engines {
		if e == eng {
			found = true
			break
		}
	}
	if !found {
		return usagef("%s does not apply to engine %s", id, eng)
	}

	verb := "disabled"
	if enable {
		verb = "enabled"
	}

	switch spec.Kind {
	case addon.KindPooler:
		cur := s.Effective("MDB_PROFILES")
		if cur == "" {
			cur = "pg,ui"
		}
		next := addon.MergeProfiles(cur, spec.Profile, enable)
		if err := s.Set("MDB_PROFILES", next); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "wrote MDB_PROFILES=%s in %s\n", next, s.EnvFile)
		svc := eng + "-pooler"
		if enable {
			if err := a.runSteps(c, script("scripts/runtime", "compose", "up", "-d", svc)); err != nil {
				fmt.Fprintf(a.stderr, "note: start the pooler with: dbpantry up --profiles %s\n", next)
				return err
			}
		} else {
			_ = a.runSteps(c, script("scripts/runtime", "compose", "stop", svc))
		}
	case addon.KindExtension:
		name := strings.TrimPrefix(spec.ID, "ext.")
		init := addon.MergeCSV(s.Effective("MDB_PG_INIT_EXTENSIONS"), name, enable)
		if err := s.Set("MDB_PG_INIT_EXTENSIONS", init); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "wrote MDB_PG_INIT_EXTENSIONS in %s\n", s.EnvFile)
		if spec.NeedsPreload {
			pre := addon.MergeCSV(s.Effective("MDB_PG_SHARED_PRELOAD"), name, enable)
			if err := s.Set("MDB_PG_SHARED_PRELOAD", pre); err != nil {
				return err
			}
			fmt.Fprintln(a.stderr, "note: shared_preload_libraries changed; restart pg for the library to load")
		}
		if enable {
			if err := a.addonCreateExtension(c, eng, name, project); err != nil {
				fmt.Fprintf(a.stderr, "note: CREATE EXTENSION on live databases failed: %v\n", err)
				fmt.Fprintln(a.stderr, "note: INIT list is updated; new projects will get the extension; retry or run SQL by hand")
			}
		} else {
			fmt.Fprintln(a.stderr, "note: existing databases keep the extension; new projects will not get it from INIT (no DROP)")
		}
	case addon.KindUI:
		cur := s.Effective("MDB_PROFILES")
		if cur == "" {
			cur = endpoint.DefaultProfiles
		}
		next := addon.MergeProfiles(cur, "ui", enable)
		if err := s.Set("MDB_PROFILES", next); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "wrote MDB_PROFILES=%s in %s\n", next, s.EnvFile)
	case addon.KindEmbed:
		val := "false"
		if enable {
			val = "true"
		}
		if err := s.Set(spec.ConfigKey, val); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "wrote %s=%s in %s\n", spec.ConfigKey, val, s.EnvFile)
	default:
		return usagef("unsupported kind %s", spec.Kind)
	}
	fmt.Fprintf(a.stdout, "addon %s %s for %s\n", verb, id, eng)
	return nil
}

// addonCreateExtension runs CREATE EXTENSION IF NOT EXISTS on active project
// databases (or one --project). The extension name comes from the catalog only.
func (a *app) addonCreateExtension(c *cobra.Command, eng, extName, project string) error {
	root, err := a.root()
	if err != nil {
		return err
	}
	if !a.engineHasDriver(root, eng, "pgsql") {
		return nil
	}
	known, err := a.knownProjects(root)
	if err != nil {
		return err
	}
	var dbs []string
	if project != "" {
		dbs = []string{project}
	} else {
		for _, k := range known {
			if k.Engine == eng {
				dbs = append(dbs, k.Name)
			}
		}
	}
	if len(dbs) == 0 {
		fmt.Fprintln(a.stderr, "note: no project databases to CREATE EXTENSION on yet")
		return nil
	}
	// Catalog names are [a-z0-9_-]+; refuse anything else before interpolating.
	if !safeExtName(extName) {
		return usagef("refusing unsafe extension name %q", extName)
	}
	var first error
	for _, db := range dbs {
		if !safeDBName(db) {
			fmt.Fprintf(a.stderr, "skip CREATE EXTENSION on %q: invalid name\n", db)
			continue
		}
		sql := `CREATE EXTENSION IF NOT EXISTS "` + extName + `" CASCADE`
		err := a.runSteps(c, script("scripts/runtime", "exec", eng, "psql", "-U", "postgres", "-d", db, "-v", "ON_ERROR_STOP=1", "-c", sql))
		if err != nil {
			fmt.Fprintf(a.stderr, "CREATE EXTENSION %s on %s: %v\n", extName, db, err)
			if first == nil {
				first = err
			}
			continue
		}
		fmt.Fprintf(a.stdout, "CREATE EXTENSION %s on %s\n", extName, db)
	}
	return first
}

func safeExtName(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if i == 0 && (r == '-' || (r >= '0' && r <= '9')) {
			return false
		}
		if !ok {
			return false
		}
	}
	return true
}

func safeDBName(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if i == 0 && (r < 'a' || r > 'z') {
			return false
		}
		if !ok {
			return false
		}
	}
	return true
}

// engineHasDriver reports whether eng's descriptor uses the given driver
// (e.g. pgsql). Engine names are never written out as string literals here.
func (a *app) engineHasDriver(root, eng, driver string) bool {
	es, err := engine.Load(filepath.Join(root, "engines"))
	if err != nil {
		return false
	}
	for _, e := range es {
		if e.Name == eng && e.Driver == driver {
			return true
		}
	}
	return false
}
