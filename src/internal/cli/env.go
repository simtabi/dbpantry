package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/envtype"
)

// envCmd manages named instance homes labelled by environment type (D-100).
func (a *app) envCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Named environments (instance homes) and cross-env data move",
		Long: `Each environment is one instance home (MDB_HOME) with an environment type
(localdev, dev, test, qa, uat, trn, spt, stg, preprod, demo, sandbox, prod —
plus aliases like prd, tst). They are separate Compose projects; copy and
migrate use backup + restore between homes (design.md D-100).`,
	}
	cmd.AddCommand(
		a.envTypesCmd(),
		a.envLsCmd(),
		a.envInitCmd(),
		a.envUseCmd(),
		a.envCopyCmd(),
		a.envMigrateCmd(),
		a.envRemoveCmd(),
	)
	return cmd
}

func (a *app) envRegistryPath() (string, error) {
	if _, err := a.root(); err != nil {
		return "", err
	}
	// Registry lives next to the operator config home, not inside one instance.
	return envtype.RegistryPath(envtype.OperatorHome()), nil
}

func (a *app) envTypesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "types",
		Short: "List environment types and aliases",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			for _, s := range envtype.Catalog() {
				alias := ""
				if len(s.Aliases) > 0 {
					alias = " (" + strings.Join(s.Aliases, ", ") + ")"
				}
				flags := ""
				if s.ProdLike {
					flags += " · prod-like"
				}
				if s.Ephemeral {
					flags += " · ephemeral"
				}
				_, _ = fmt.Fprintf(a.stdout, "%-10s  %s%s%s\n", s.ID, s.Title, alias, flags)
				_, _ = fmt.Fprintf(a.stdout, "            %s\n", s.Summary)
			}
			return nil
		},
	}
}

func (a *app) envLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List registered environments",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path, err := a.envRegistryPath()
			if err != nil {
				return err
			}
			envs, err := envtype.Load(path)
			if err != nil {
				return err
			}
			if len(envs) == 0 {
				_, _ = fmt.Fprintln(a.stdout, "No environments registered. Create one with: dbpantry env init --name NAME --type TYPE")
				return nil
			}
			for _, e := range envs {
				_, _ = fmt.Fprintf(a.stdout, "%-16s  %-10s  %s\n", e.Name, e.Type, e.Home)
			}
			return nil
		},
	}
}

func (a *app) envInitCmd() *cobra.Command {
	var name, typ, home, project string
	cmd := &cobra.Command{
		Use:   "init --name NAME --type TYPE",
		Short: "Create an instance home for an environment type and register it",
		Long: `Creates the home directory, runs dbpantry init into it (when not --dry-run),
sets MDB_ENV_TYPE and COMPOSE_PROJECT_NAME, and registers the environment.

Use a distinct --home (or accept the default under ~/.config/dbpantry/envs/NAME)
so production and localdev never share volumes.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			spec, err := envtype.Parse(typ)
			if err != nil {
				return usagef("%v", err)
			}
			if name == "" {
				return usagef("--name is required")
			}
			if !envtype.ValidName(name) {
				return usagef("name %q: want lowercase [a-z][a-z0-9-]{0,47}", name)
			}
			reg, err := a.envRegistryPath()
			if err != nil {
				return err
			}
			if home == "" {
				home = filepath.Join(filepath.Dir(reg), "envs", name)
			}
			if project == "" {
				project = envtype.DefaultProject(name)
			}
			if a.dryRun {
				_, _ = fmt.Fprintf(a.stdout, "would init env %s type=%s home=%s project=%s\n", name, spec.ID, home, project)
				return nil
			}
			if err := os.MkdirAll(home, 0o700); err != nil {
				return err
			}
			if err := a.runSelf(c, home, "init"); err != nil {
				return err
			}
			if err := patchEnvFile(filepath.Join(home, ".env"), map[string]string{
				"MDB_ENV_TYPE":         string(spec.ID),
				"COMPOSE_PROJECT_NAME": project,
			}); err != nil {
				return err
			}
			if spec.SuggestOverlays != "" {
				envPath := filepath.Join(home, ".env")
				cur := readEnvKey(envPath, "MDB_PROFILES")
				merged := mergeCSV(cur, spec.SuggestOverlays)
				_ = patchEnvFile(envPath, map[string]string{"MDB_PROFILES": merged})
			}
			entry := envtype.Entry{
				Name: name, Type: spec.ID, Home: home, Project: project,
				Created: time.Now().UTC(),
				Notes:   spec.Title,
			}
			if err := envtype.Upsert(reg, entry); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(a.stdout, "environment %s (%s) ready at %s\n", name, spec.ID, home)
			_, _ = fmt.Fprintf(a.stdout, "  use:  dbpantry --home %s up\n", home)
			_, _ = fmt.Fprintf(a.stdout, "  or:   dbpantry env use %s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "short name (e.g. shop-uat)")
	cmd.Flags().StringVar(&typ, "type", "", "environment type (dbpantry env types)")
	cmd.Flags().StringVar(&home, "home", "", "instance home path (default: ~/.config/dbpantry/envs/NAME)")
	cmd.Flags().StringVar(&project, "project", "", "COMPOSE_PROJECT_NAME (default: mdb-NAME)")
	return cmd
}

func (a *app) envUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use NAME",
		Short: "Print export lines to point a shell at a registered environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			reg, err := a.envRegistryPath()
			if err != nil {
				return err
			}
			e, err := envtype.Get(reg, args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(a.stdout, "export MDB_HOME=%q\n", e.Home)
			_, _ = fmt.Fprintf(a.stdout, "export DBPANTRY_HOME=%q\n", e.Home)
			if e.Project != "" {
				_, _ = fmt.Fprintf(a.stdout, "export COMPOSE_PROJECT_NAME=%q\n", e.Project)
			}
			_, _ = fmt.Fprintf(a.stdout, "# eval \"$(dbpantry env use %s)\"\n", e.Name)
			return nil
		},
	}
}

func (a *app) envCopyCmd() *cobra.Command {
	var from, to, eng, db, confirm string
	cmd := &cobra.Command{
		Use:   "copy --from ENV --to ENV --engine ENGINE --db DB",
		Short: "Copy one database between environments (export then restore)",
		Long: `Exports a dump from the source environment's home and restores it into the
destination. Both may be registry names or absolute paths. The destination
database is replaced; --confirm must match the database name.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if from == "" || to == "" || eng == "" || db == "" {
				return usagef("--from, --to, --engine and --db are required")
			}
			reg, err := a.envRegistryPath()
			if err != nil {
				return err
			}
			src, err := envtype.ResolveHome(reg, from)
			if err != nil {
				return err
			}
			dst, err := envtype.ResolveHome(reg, to)
			if err != nil {
				return err
			}
			if src == dst {
				return usagef("source and destination homes are the same")
			}
			in, err := a.confirmed("database", confirm, a.env("CONFIRM") == "yes")
			if err != nil {
				return err
			}
			if confirm != "" && confirm != db {
				return usagef("confirmation %q does not match database %q", confirm, db)
			}
			tmpDir, err := os.MkdirTemp("", "mdb-env-copy-*")
			if err != nil {
				return err
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()
			dump := filepath.Join(tmpDir, eng+"_"+db+".dump")
			if a.dryRun {
				_, _ = fmt.Fprintf(a.stdout, "would copy %s/%s from %s → %s\n", eng, db, src, dst)
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "exporting %s/%s from %s\n", eng, db, src)
			if err := a.runInHome(c, src, step{
				Args: []string{"scripts/transfer", "export", "--engine", eng, "--db", db, "--to", dump},
			}); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(a.stdout, "restoring into %s\n", dst)
			return a.runInHome(c, dst, step{
				Args:  []string{"scripts/restore", "--engine", eng, "--db", db, "--file", dump},
				Input: in,
			})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "source environment name or home path")
	cmd.Flags().StringVar(&to, "to", "", "destination environment name or home path")
	cmd.Flags().StringVar(&eng, "engine", "", "engine")
	cmd.Flags().StringVar(&db, "db", "", "database / project name")
	cmd.Flags().StringVar(&confirm, "confirm", "", "type the database name to confirm replace")
	return cmd
}

// envMigrateCmd is copy with migrate wording (same export+restore path).
func (a *app) envMigrateCmd() *cobra.Command {
	cmd := a.envCopyCmd()
	cmd.Use = "migrate --from ENV --to ENV --engine ENGINE --db DB"
	cmd.Short = "Migrate a database between environments (same as copy)"
	cmd.Aliases = []string{"move"}
	return cmd
}

func (a *app) envRemoveCmd() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "remove NAME",
		Short: "Unregister an environment (optionally delete its home)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			reg, err := a.envRegistryPath()
			if err != nil {
				return err
			}
			e, err := envtype.Get(reg, args[0])
			if err != nil {
				return err
			}
			if a.dryRun {
				_, _ = fmt.Fprintf(a.stdout, "would unregister %s (home %s)\n", e.Name, e.Home)
				return nil
			}
			if err := envtype.Delete(reg, e.Name); err != nil {
				return err
			}
			if purge {
				if err := os.RemoveAll(e.Home); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(a.stdout, "removed %s and deleted %s\n", e.Name, e.Home)
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "unregistered %s (home left at %s)\n", e.Name, e.Home)
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete the instance home directory")
	return cmd
}

// runSelf runs this binary with --home HOME and subcommand args.
func (a *app) runSelf(c *cobra.Command, home string, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	argv := append([]string{"--home", home, "--no-input"}, args...)
	return a.runSteps(c, step{Args: append([]string{exe}, argv...)})
}

// patchEnvFile sets keys in an .env file (creates the file when missing).
func patchEnvFile(path string, kv map[string]string) error {
	var lines []string
	b, err := os.ReadFile(path) // #nosec G304 -- instance .env
	if err == nil {
		lines = strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	} else if !os.IsNotExist(err) {
		return err
	}
	for k, v := range kv {
		found := false
		line := k + "=" + quoteEnv(v)
		for i, l := range lines {
			if strings.HasPrefix(l, k+"=") || strings.HasPrefix(l, "export "+k+"=") {
				lines[i] = line
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, line)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600) // #nosec G703 -- an instance home's own .env
}

func quoteEnv(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, " \t\"'`$\\#") {
		return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
	}
	return v
}

func readEnvKey(path, key string) string {
	b, err := os.ReadFile(path) // #nosec G304 -- instance .env
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		if !strings.HasPrefix(line, key+"=") {
			continue
		}
		v := strings.TrimPrefix(line, key+"=")
		return strings.Trim(v, `"'`)
	}
	return ""
}

func mergeCSV(base, extra string) string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		for _, p := range strings.Split(s, ",") {
			p = strings.TrimSpace(p)
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	add(base)
	add(extra)
	return strings.Join(out, ",")
}
