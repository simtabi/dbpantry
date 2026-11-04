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

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/config"
	"github.com/simtabi/dbpantry/src/internal/engine"
)

// Build metadata, stamped with -ldflags -X at build time.
var (
	version = "dev"
	commit  = "none"
)

// Exit codes. 0 and 1 keep the meaning the scripts give them (refactor plan
// §6.1); 2 is a usage error.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// usageError marks an error as the caller's, for exit code 2.
type usageError struct{ error }

func usagef(format string, a ...any) error { return usageError{fmt.Errorf(format, a...)} }

// app is what every command shares.
type app struct {
	rootFlag string
	stdout   io.Writer
	stderr   io.Writer
	environ  func() []string
	log      *slog.Logger
}

// Execute runs the command line and returns the process exit code.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, environ func() []string) int {
	a := &app{stdout: stdout, stderr: stderr, environ: environ}
	a.log = slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cmd := a.rootCmd() //nolint:contextcheck // cobra hands ctx to every command through ExecuteContext
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	err := cmd.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	_, _ = fmt.Fprintf(stderr, "dbpantry: %v\n", err)
	var ue usageError
	if errors.As(err, &ue) {
		return ExitUsage
	}
	return ExitError
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "dbpantry",
		Short:         "The dbpantry database toolkit",
		Long:          "dbpantry runs and describes the toolkit's database engines. Phase 1 of the Go port: the engine manifest, endpoints and test fixtures. Everything else is still `make` (docs/reference/manifest.md).",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	root.PersistentFlags().StringVar(&a.rootFlag, "root", "",
		"the dbpantry checkout (default: $DBPANTRY_DIR, else the nearest directory above the current one holding engines/ and scripts/engine-lib.sh, else the one above the binary)")
	root.AddCommand(a.versionCmd(), a.manifestCmd(), a.endpointsCmd(), a.fixtureCmd())
	return root
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			_, err := fmt.Fprintf(a.stdout, "dbpantry %s (%s)\n", version, commit)
			return err
		},
	}
}

func (a *app) env(key string) string {
	for _, kv := range a.environ() {
		if len(kv) > len(key) && kv[:len(key)] == key && kv[len(key)] == '=' {
			return kv[len(key)+1:]
		}
	}
	return ""
}

// root finds the checkout.
func (a *app) root() (string, error) {
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
		if d := filepath.Dir(filepath.Dir(exe)); isRoot(d) {
			return d, nil
		}
	}
	return "", usagef("no dbpantry checkout found: run inside one, set DBPANTRY_DIR, or pass --root")
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
