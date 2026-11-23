package cli

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/dashboard"
	"github.com/simtabi/dbpantry/src/internal/project"
	"github.com/simtabi/dbpantry/src/internal/ui"
)

// project ls and project drop (management plan, Phase E). The engines are
// asked what they have (scripts/project ls); the password files say what
// dbpantry provisioned; project.Join puts the two together, here and on the
// dashboard's Projects page.

func (a *app) projectLsCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the projects and what their engines say about each",
		Long: `List the projects dbpantry provisioned (by their password files) and what
each running engine says about them: active (the database exists), orphaned
(the engine has no such database; only the password files are left, and
project drop removes them), or unknown (the engine is not running or did not
answer). Databases no password file names are listed as unmanaged; dbpantry
did not provision them and will not drop them.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := a.output(output, "table", "json"); err != nil {
				return err
			}
			root, err := a.root()
			if err != nil {
				return err
			}
			env, err := a.scriptEnv(c.Context(), root)
			if err != nil {
				return err
			}
			l, err := a.projectListing(c.Context(), root, env)
			if err != nil {
				return err
			}
			known, err := a.knownProjects(root)
			if err != nil {
				return err
			}
			entries := l.Join(known)
			if output == "json" {
				return writeJSON(a.stdout, entries)
			}
			st := a.style()
			t := ui.Table{Headers: []string{"PROJECT", "ENGINE", "STATE", "EXTENSIONS"}}
			for _, e := range entries {
				t.Rows = append(t.Rows, []string{sanitize(e.Name), sanitize(e.Engine), stateLabel(st, e.State), extensionsLabel(e.Extensions)})
			}
			a.printLines(st.Table(t, a.width()))
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table or json")
	return cmd
}

func (a *app) projectDropCmd() *cobra.Command {
	return a.confirmedProjectCmd("drop",
		"Drop a project: its database, roles, password files and client certificate (typed confirmation)",
		`Drop a project dbpantry provisioned (scripts/project drop): its database and
everything in it, its owner and read-only roles, their password files and its
client certificate. Backups of the database are kept. Only a project with a
password file can be dropped, never a name the engines keep for themselves,
and never while its engine is not answering.`,
		[]string{"scripts/project", "drop"})
}

func (a *app) projectRotateCmd() *cobra.Command {
	return a.confirmedProjectCmd("rotate",
		"Give a project's roles new passwords and print the new .env block (typed confirmation)",
		`Give a project's owner and read-only roles new passwords (scripts/new-project
--rotate) and print the new .env block. The new passwords are set on the
engine before the password files are replaced, so a failure leaves the old
ones working. Applications using the old passwords stop connecting until they
are given the new.`,
		[]string{"scripts/new-project", "--rotate"})
}

// confirmedProjectCmd is a project command that changes what applications
// depend on (drop, rotate): the script asks for the project name, which
// --confirm answers, as CONFIRM=yes in the environment does (--yes never
// does), and a mirroring pooler is restarted afterwards, as after project
// new.
func (a *app) confirmedProjectCmd(verb, short, long string, argv []string) *cobra.Command {
	var name, eng, confirm string
	var noSync bool
	cmd := &cobra.Command{
		Use:   verb + " --name NAME [--engine ENGINE]",
		Short: short,
		Long: long + `

The script asks for the project name; --confirm answers it, as CONFIRM=yes in
the environment does. --yes never does. A mirroring pooler is then restarted,
as after project new; --no-pooler-sync skips that.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			in, err := a.confirmed("project name", confirm, a.env("CONFIRM") == "yes")
			if err != nil {
				return err
			}
			args := append(append([]string{}, argv...), "--name", name)
			if eng != "" {
				args = append(args, "--engine", eng)
			}
			if err := a.runSteps(c, step{Args: args, Input: in}); err != nil {
				return err
			}
			if noSync || eng == "" {
				return nil
			}
			return a.syncPooler(c, eng, true)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "the project (database) name")
	cmd.Flags().StringVar(&eng, "engine", "", "the engine (default: the script's, PostgreSQL)")
	cmd.Flags().StringVar(&confirm, "confirm", "", "the typed confirmation: the project name")
	cmd.Flags().BoolVar(&noSync, "no-pooler-sync", false, "do not restart a mirroring pooler afterwards")
	return cmd
}

// projectListing asks every running engine for its databases. The script's
// log lines are returned with its failure.
func (a *app) projectListing(ctx context.Context, root string, env []string) (project.Listing, error) {
	var stdout, stderr bytes.Buffer
	none := ""
	err := a.runStepTo(ctx, root, env, step{Args: []string{"scripts/project", "ls", "--format", "json"}, Input: &none}, &stdout, &stderr)
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return project.Listing{}, fmt.Errorf("%w: %s", err, msg)
		}
		return project.Listing{}, err
	}
	return project.ParseListing(stdout.Bytes())
}

// knownProjects are the stack's projects by their password files.
func (a *app) knownProjects(root string) ([]project.Known, error) {
	m, _, err := a.manifest()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range m.Engines {
		names = append(names, e.Name)
	}
	ps, err := dashboard.ScanProjects(filepath.Join(a.home(root), "secrets"), names)
	if err != nil {
		return nil, err
	}
	known := make([]project.Known, 0, len(ps))
	for _, p := range ps {
		known = append(known, project.Known{Engine: p.Engine, Name: p.Name})
	}
	return known, nil
}

func stateLabel(st ui.Style, s project.State) string {
	switch s {
	case project.Active:
		return st.Green(string(s))
	case project.Orphaned:
		return st.Yellow(string(s))
	default:
		return st.Dim(string(s))
	}
}

func extensionsLabel(exts []string) string {
	if exts == nil {
		return "-"
	}
	return sanitize(strings.Join(exts, ","))
}
