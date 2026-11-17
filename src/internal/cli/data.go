package cli

import (
	"github.com/spf13/cobra"
)

// Projects and data: project new, backup run|verify, restore, pitr, data
// import|export, upgrade. Each wraps the script its make target ran.

func (a *app) projectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Provision application databases",
	}
	var name, eng string
	var noSync, fx bool
	newCmd := &cobra.Command{
		Use:   "new",
		Short: "Provision a database, its roles and extensions, and print the Laravel .env block",
		Long: `Provision a database with an application role and a read-only companion, and
print a paste-ready Laravel .env block (scripts/new-project). When the engine's
pooler mirrors credentials at start (ProxySQL) and is running, it is then
restarted so the new user can log in through it (N8); --no-pooler-sync skips that.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return a.newProject(c, name, eng, noSync, fx)
		},
	}
	newCmd.Flags().StringVar(&name, "name", "", "the project (database) name")
	newCmd.Flags().StringVar(&eng, "engine", "", "the engine (default: the script's, PostgreSQL)")
	newCmd.Flags().BoolVar(&noSync, "no-pooler-sync", false, "do not restart a mirroring pooler afterwards")
	newCmd.Flags().BoolVar(&fx, "fixture", false, "provision in the fixture (`fixture up`), with password files under secrets/fixture/; give each test run its own --name for an isolated database")
	cmd.AddCommand(newCmd)
	return cmd
}

// newProject provisions one project with scripts/new-project, drawn as a
// tree and tables on a terminal, then syncs a mirroring pooler. An empty
// engine is the script's default. With fixture, it provisions in the
// fixture (fixtureEnv: its compose project and MDB_FIXTURE=1, so the
// password files go under secrets/fixture/) and skips the pooler sync,
// which acts on the stack's compose project; a test suite connects to the
// engine directly. `start` runs it too.
func (a *app) newProject(c *cobra.Command, name, eng string, noSync, fixture bool) error {
	argv := []string{"scripts/new-project", "--name", name}
	if eng != "" {
		argv = append(argv, "--engine", eng)
	}
	var env []string
	if fixture {
		m, root, err := a.manifest()
		if err != nil {
			return err
		}
		s, err := a.settings(root)
		if err != nil {
			return err
		}
		if env, _, err = fixtureEnv(m, s); err != nil {
			return err
		}
	}
	if a.formatted() {
		if _, err := a.projectFormatted(c, argv, env); err != nil {
			return err
		}
	} else if err := a.runSteps(c, step{Args: argv, Env: env}); err != nil {
		return err
	}
	if noSync || eng == "" || fixture {
		return nil
	}
	return a.syncPooler(c, eng, true)
}

func (a *app) backupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Dump databases and verify the dumps",
	}
	var eng, db string
	var all bool
	run := &cobra.Command{
		Use:   "run (--engine ENGINE [--db DB] | --all)",
		Short: "Dump one database (or every database of an engine), or with --all every engine plus PG globals",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if all {
				return a.runSteps(c, script("scripts/backup", "--all"))
			}
			argv := append([]string{"scripts/backup"}, strArg(c, "engine", eng)...)
			return a.runSteps(c, script(append(argv, strArg(c, "db", db)...)...))
		},
	}
	run.Flags().StringVar(&eng, "engine", "", "the engine to dump")
	run.Flags().StringVar(&db, "db", "", "one database (default: every one)")
	run.Flags().BoolVar(&all, "all", false, "every database on every enabled engine, plus PG globals")
	verify := &cobra.Command{
		Use:   "verify",
		Short: "Restore the latest set into throwaway containers and assert row counts",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return a.runSteps(c, script("scripts/verify-backups")) },
	}
	cmd.AddCommand(run, verify)
	return cmd
}

func (a *app) restoreCmd() *cobra.Command {
	var eng, db, file, confirm string
	cmd := &cobra.Command{
		Use:   "restore --engine ENGINE --db DB --file DUMP",
		Short: "Restore a dump into a database, replacing its contents (typed confirmation)",
		Long: `Restore a checksummed dump into a database (scripts/restore). The script asks
for the database name; --confirm answers it, as CONFIRM=yes in the environment
does. --yes never does.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			in, err := a.confirmed("database", confirm, a.env("CONFIRM") == "yes")
			if err != nil {
				return err
			}
			return a.runSteps(c, step{Args: []string{"scripts/restore", "--engine", eng, "--db", db, "--file", file}, Input: in})
		},
	}
	cmd.Flags().StringVar(&eng, "engine", "", "the engine")
	cmd.Flags().StringVar(&db, "db", "", "the database to replace")
	cmd.Flags().StringVar(&file, "file", "", "the dump to restore")
	cmd.Flags().StringVar(&confirm, "confirm", "", "the typed confirmation: the database name")
	return cmd
}

func (a *app) pitrCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pitr",
		Short: "Point-in-time recovery: the window, a backup, a recovery",
	}
	var eng, kind, to, confirm string
	engineFlag := func(c *cobra.Command) {
		c.Flags().StringVar(&eng, "engine", "", "the engine (default: the script's, PostgreSQL)")
	}
	info := &cobra.Command{
		Use:   "info",
		Short: "Show the recoverable window",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return a.runSteps(c, script(append([]string{"scripts/pitr", "info"}, strArg(c, "engine", eng)...)...))
		},
	}
	engineFlag(info)
	backup := &cobra.Command{
		Use:   "backup",
		Short: "Take a PITR backup (--type full, diff or incr)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			argv := append([]string{"scripts/pitr", "backup"}, strArg(c, "engine", eng)...)
			return a.runSteps(c, script(append(argv, strArg(c, "type", kind)...)...))
		},
	}
	engineFlag(backup)
	backup.Flags().StringVar(&kind, "type", "", "full, diff or incr")
	restore := &cobra.Command{
		Use:   "restore --to TARGET",
		Short: "Recover to a point in time (a time for PostgreSQL, <binlog>:<pos> for the MySQL family)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			in, err := a.confirmed("engine or stanza", confirm, false)
			if err != nil {
				return err
			}
			argv := append([]string{"scripts/pitr", "restore"}, strArg(c, "engine", eng)...)
			return a.runSteps(c, step{Args: append(argv, "--to", to), Input: in})
		},
	}
	engineFlag(restore)
	restore.Flags().StringVar(&to, "to", "", "the recovery target")
	restore.Flags().StringVar(&confirm, "confirm", "", "the typed confirmation the script asks for (the stanza, or the engine)")
	cmd.AddCommand(info, backup, restore)
	return cmd
}

func (a *app) dataCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data",
		Short: "Move data in and out (scripts/transfer)",
	}
	var eng, db, from, fromHost, fromUser, to string
	imp := &cobra.Command{
		Use:   "import --engine ENGINE --db DB (--from FILE | --from-host HOST)",
		Short: "Move data in, from a file or another server",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			argv := []string{"scripts/transfer", "import", "--engine", eng, "--db", db}
			argv = append(argv, strArg(c, "from", from)...)
			argv = append(argv, strArg(c, "from-host", fromHost)...)
			argv = append(argv, strArg(c, "from-user", fromUser)...)
			return a.runSteps(c, script(argv...))
		},
	}
	imp.Flags().StringVar(&from, "from", "", "a dump file to load")
	imp.Flags().StringVar(&fromHost, "from-host", "", "a server to copy from")
	imp.Flags().StringVar(&fromUser, "from-user", "", "the user on --from-host")
	exp := &cobra.Command{
		Use:   "export --engine ENGINE --db DB [--to FILE]",
		Short: "Move data out to a file",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			argv := []string{"scripts/transfer", "export", "--engine", eng, "--db", db}
			return a.runSteps(c, script(append(argv, strArg(c, "to", to)...)...))
		},
	}
	exp.Flags().StringVar(&to, "to", "", "the file to write")
	for _, c := range []*cobra.Command{imp, exp} {
		c.Flags().StringVar(&eng, "engine", "", "the engine")
		c.Flags().StringVar(&db, "db", "", "the database")
	}
	cmd.AddCommand(imp, exp)
	return cmd
}

func (a *app) upgradeCmd() *cobra.Command {
	var eng, from, to, confirm string
	cmd := &cobra.Command{
		Use:   "upgrade --engine ENGINE --from MAJOR --to MAJOR",
		Short: "Guided major-version migration (typed confirmation)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			in, err := a.confirmed("upgrade", confirm, a.env("CONFIRM") == "yes")
			if err != nil {
				return err
			}
			return a.runSteps(c, step{Args: []string{"scripts/upgrade", "--engine", eng, "--from", from, "--to", to}, Input: in})
		},
	}
	cmd.Flags().StringVar(&eng, "engine", "", "the engine")
	cmd.Flags().StringVar(&from, "from", "", "the current major")
	cmd.Flags().StringVar(&to, "to", "", "the target major")
	cmd.Flags().StringVar(&confirm, "confirm", "", "the typed confirmation the script asks for")
	return cmd
}
