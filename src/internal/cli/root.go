// Package cli is the dbpantry command tree. It is the only package that
// imports cobra; commands parse flags and call the domain packages.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/config"
	"github.com/simtabi/dbpantry/src/internal/engine"
	"github.com/simtabi/dbpantry/src/internal/kit"
)

// Build metadata, stamped with -ldflags -X at build time.
var (
	version = "dev"
	commit  = "none"
)

// app is what every command shares.
type app struct {
	rootFlag string
	// homeFlag is --home: the instance home (design.md D-84, D-85).
	homeFlag string
	// rootDir is the checkout or extracted kit root() resolved, once.
	rootDir string
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	environ func() []string
	log     *slog.Logger

	// Global flags (refactor plan §7).
	dryRun  bool
	noInput bool
	noColor bool
	// plain prints the scripts' own output on a terminal too (pretty.go).
	plain bool
	// pretty draws the formatted output off a terminal too (less -R, a test).
	pretty bool
	// profiles is a wrapped command's --profiles, when it has one.
	profiles string
	// jsonErrors is set by a command run with -o json: its failure is
	// reported as a JSON object on stderr.
	jsonErrors bool
}

// Execute runs the command line and returns the process exit code.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, environ func() []string) int {
	a := &app{stdin: os.Stdin, stdout: stdout, stderr: stderr, environ: environ}
	a.log = slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cmd := a.rootCmd() //nolint:contextcheck // cobra hands ctx to every command through ExecuteContext
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	err := cmd.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	var se statusError
	if errors.As(err, &se) {
		// The script has already said why; its status is the answer.
		return se.code
	}
	return writeError(stderr, err, a.jsonErrors)
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "dbpantry",
		Short: "The dbpantry database toolkit",
		Long: `dbpantry runs and describes the toolkit's database engines.

Every make target is an alias for one of these commands (docs/reference/cli.md).
Most still run the script the make target ran before, unchanged; the Go port
replaces them one bounded context at a time (refactor plan, phase 2).`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	root.PersistentFlags().StringVar(&a.rootFlag, "root", "",
		"the dbpantry checkout (default: $DBPANTRY_DIR, else the nearest directory above the current one holding engines/ and scripts/engine-lib.sh, else the nearest one above the binary, else the kit built into this binary, extracted under the home)")
	root.PersistentFlags().StringVar(&a.homeFlag, "home", "",
		"the instance home: .env, secrets/, certs/, backups/ and generated files (default: $DBPANTRY_HOME, else $MDB_HOME, else the checkout, or ~/.config/dbpantry when running from the built-in kit)")
	pf := root.PersistentFlags()
	pf.BoolVar(&a.dryRun, "dry-run", false, "print the scripts a command would run, and run nothing")
	pf.BoolVar(&a.noInput, "no-input", false, "never prompt: a command that needs a typed confirmation without --confirm exits 7")
	pf.BoolVar(&a.noColor, "no-color", false, "no colour in output (also NO_COLOR)")
	pf.BoolVar(&a.plain, "plain", false, "print the scripts' own output, without the step tree, progress bars and tables drawn on a terminal")
	pf.BoolVar(&a.pretty, "pretty", false, "draw the step tree, progress bars and tables even when output is not a terminal (e.g. into less -R)")
	root.MarkFlagsMutuallyExclusive("plain", "pretty")

	// The tree, declared one command at a time (TestTreeIsDeclared).
	root.AddCommand(
		// Setup
		a.initCmd(), a.versionCmd(), a.selfUpdateCmd(),
		// Config
		a.configCmd(),
		// Engines
		a.manifestCmd(),
		// Stack
		a.startCmd(), a.upCmd(), a.downCmd(), a.statusCmd(), a.logsCmd(), a.endpointsCmd(), a.dashboardCmd(), a.forwardCmd(), a.destroyCmd(),
		// Fixtures
		a.fixtureCmd(),
		// Runtime
		a.runtimeCmd(),
		// Shells
		a.shellCmd(), a.toolsCmd(),
		// Projects
		a.projectCmd(),
		// Data
		a.backupCmd(), a.restoreCmd(), a.pitrCmd(), a.dataCmd(), a.upgradeCmd(),
		// Topology
		a.haCmd(), a.poolerCmd(),
		// Security
		a.certsCmd(), a.secretsCmd(), a.imagesCmd(),
		// Development
		a.verifyCmd(), a.devCmd(),
	)
	strictArgs(root)
	return root
}

// strictArgs makes every argument error a usage error (exit 64). A group
// (a command with subcommands and nothing of its own to run) prints its help
// when called bare and refuses an unknown subcommand, where cobra would
// print the help and exit 0, so a mistyped command can never look like a
// success.
func strictArgs(c *cobra.Command) {
	if c.HasSubCommands() && c.RunE == nil && c.Run == nil {
		c.Args = cobra.ArbitraryArgs
		c.RunE = func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q for %q", args[0], c.CommandPath())
			}
			return c.Help()
		}
	}
	if v := c.Args; v != nil {
		c.Args = func(c *cobra.Command, args []string) error {
			if err := v(c, args); err != nil {
				var ue usageError
				if errors.As(err, &ue) {
					return err
				}
				return usageError{err}
			}
			return nil
		}
	}
	for _, sub := range c.Commands() {
		strictArgs(sub)
	}
}

func (a *app) versionCmd() *cobra.Command {
	var output string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if asJSON {
				output = "json"
			}
			if err := a.output(output, "plain", "json", "yaml"); err != nil {
				return err
			}
			switch output {
			case "json":
				return writeJSON(a.stdout, map[string]string{"version": version, "commit": commit})
			case "yaml":
				_, err := fmt.Fprintf(a.stdout, "version: %q\ncommit: %q\n", version, commit)
				return err
			default:
				_, err := fmt.Fprintf(a.stdout, "dbpantry %s (%s)\n", version, commit)
				return err
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "plain", "output format: plain, json or yaml")
	cmd.Flags().BoolVar(&asJSON, "json", false, "the same as -o json")
	return cmd
}

func (a *app) env(key string) string {
	for _, kv := range a.environ() {
		if len(kv) > len(key) && kv[:len(key)] == key && kv[len(key)] == '=' {
			return kv[len(key)+1:]
		}
	}
	return ""
}

// home is the instance home for a checkout: $MDB_HOME, else the checkout
// (design.md D-84). root() puts --home and DBPANTRY_HOME into MDB_HOME, so
// this and every script read the same one.
func (a *app) home(root string) string {
	return config.Home(root, a.environ)
}

// root finds the checkout, or extracts the kit built into the binary when
// there is none (step 5, design.md D-85), and settles the instance home:
// --home, else DBPANTRY_HOME, else MDB_HOME, else the checkout itself, or
// ~/.config/dbpantry for the built-in kit. A home other than the root is
// handed to every script as MDB_HOME. Resolved once per command.
func (a *app) root() (string, error) {
	if a.rootDir != "" {
		return a.rootDir, nil
	}
	root, err := a.findRoot()
	if errors.Is(err, errNoCheckout) {
		root, err = a.extractKit()
	}
	if err != nil {
		return "", err
	}
	home := a.explicitHome()
	if home == "" {
		home = a.env("MDB_HOME")
	}
	if home == "" {
		home = root
		if a.fromKit(root) {
			home = config.DefaultHome(a.environ)
		}
	}
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	if home != root {
		base := a.environ
		a.environ = func() []string {
			out := make([]string, 0, len(base())+1)
			for _, kv := range base() {
				if !strings.HasPrefix(kv, "MDB_HOME=") {
					out = append(out, kv)
				}
			}
			return append(out, "MDB_HOME="+home)
		}
	}
	a.rootDir = root
	return root, nil
}

// explicitHome is --home, else DBPANTRY_HOME: a home the user named for this
// binary, as opposed to MDB_HOME, which the scripts read.
func (a *app) explicitHome() string {
	if a.homeFlag != "" {
		return a.homeFlag
	}
	return a.env("DBPANTRY_HOME")
}

// fromKit reports whether root is a kit this binary extracted: a directory
// under <home>/kit/ holding the kit's MANIFEST.
func (a *app) fromKit(root string) bool {
	_, err := os.Stat(filepath.Join(root, kit.ManifestName))
	return err == nil && filepath.Base(filepath.Dir(root)) == "kit"
}

// extractKit is step 5: the kit built into this binary, extracted (or
// verified, when already there) to <home>/kit/<version>. The home is the one
// the user named, else MDB_HOME, else ~/.config/dbpantry.
func (a *app) extractKit() (string, error) {
	k, err := kit.Embedded()
	if errors.Is(err, kit.ErrNoKit) {
		return "", usagef("%v; %v", errNoCheckout, err)
	}
	if err != nil {
		return "", err
	}
	home := a.explicitHome()
	if home == "" {
		home = a.env("MDB_HOME")
	}
	if home == "" {
		home = config.DefaultHome(a.environ)
	}
	// The home holds secrets/, so it is made 0700 here, before the kit's
	// directory inside it can create it with a wider mode.
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	dir := filepath.Join(home, "kit", kit.DirName(version))
	extracted, err := k.Ensure(dir)
	if err != nil {
		return "", fmt.Errorf("the built-in kit: %w", err)
	}
	if extracted {
		a.log.Info("extracted the built-in kit", "dir", dir, "files", len(k.Entries))
	}
	return dir, nil
}

// errNoCheckout is findRoot's answer when no step finds a checkout.
var errNoCheckout = errors.New("no dbpantry checkout found: run inside one, set DBPANTRY_DIR, or pass --root")

// findRoot is steps 1 to 4: --root, DBPANTRY_DIR, the nearest checkout above
// the current directory, the nearest above the binary.
func (a *app) findRoot() (string, error) {
	if a.rootFlag != "" {
		return checkRoot(a.rootFlag)
	}
	if d := a.env("DBPANTRY_DIR"); d != "" {
		return checkRoot(d)
	}
	if wd, err := os.Getwd(); err == nil {
		for d := wd; ; d = filepath.Dir(d) {
			if isRoot(d) {
				return d, nil
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		// build/<os>/dbpantry: the checkout is two levels above the binary's
		// directory; walk up rather than assume the depth.
		for d := filepath.Dir(exe); filepath.Dir(d) != d; d = filepath.Dir(d) {
			if isRoot(d) {
				return d, nil
			}
		}
	}
	return "", errNoCheckout
}

func checkRoot(d string) (string, error) {
	abs, err := filepath.Abs(d)
	if err != nil {
		return "", err
	}
	if !isRoot(abs) {
		return "", usagef("%s is not a dbpantry checkout (no engines/ or scripts/engine-lib.sh)", abs)
	}
	return abs, nil
}

func isRoot(d string) bool {
	st, err := os.Stat(filepath.Join(d, "engines"))
	if err != nil || !st.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(d, "scripts", "engine-lib.sh"))
	return err == nil
}

// manifest loads the engines. MDB_ENGINES_DIR overrides where they are read
// from, as it does for scripts/engine-lib.sh.
func (a *app) manifest() (engine.Manifest, string, error) {
	root, err := a.root()
	if err != nil {
		return engine.Manifest{}, "", err
	}
	dir := a.env("MDB_ENGINES_DIR")
	if dir == "" {
		dir = filepath.Join(root, "engines")
	}
	engines, err := engine.Load(dir)
	if err != nil {
		return engine.Manifest{}, "", err
	}
	return engine.NewManifest(engines), root, nil
}

func (a *app) settings(root string) (*config.Settings, error) {
	return config.Load(root, a.environ)
}
