package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/endpoint"
)

// startCmd is the one command for a working day: up, then the projects,
// then the dashboard. Each step is the command of the same name, run in
// turn; nothing here does work those commands do not.
func (a *app) startCmd() *cobra.Command {
	var projects []string
	var noDashboard bool
	var f dashboardFlags
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Boot the stack, provision projects on it, then serve the dashboard: everything in one command",
		Long: `Run, in order:

  1. up                 boot the selected profiles and wait until they are healthy
  2. project new        for each --project: NAME provisions it on every engine
                        in the profiles, NAME:ENGINE on that engine only
  3. dashboard          serve the web dashboard until Ctrl-C (--no-dashboard to
                        stop after step 2, e.g. in a script or CI)

Every step is idempotent: running start again boots nothing new, reuses
each project's passwords, and leaves the data as it was. Ctrl-C stops the
dashboard only; the stack keeps running (make down stops it).`,
		Example: `  dbpantry start --profiles pg,mysql --project settings
  make start PROFILES=pg,mysql PROJECTS=settings,billing:pg`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			specs, err := parseProjects(projects)
			if err != nil {
				return err
			}
			if err := a.up(c); err != nil {
				return err
			}
			if len(specs) > 0 {
				enabled, err := a.enabledEngines(c)
				if err != nil {
					return err
				}
				for _, s := range specs {
					engines := enabled
					if s.engine != "" {
						engines = []string{s.engine}
					}
					for _, eng := range engines {
						if err := a.newProject(c, s.name, eng, false, false); err != nil {
							return err
						}
					}
				}
			}
			if noDashboard || a.dryRun {
				return nil
			}
			return a.serveDashboard(c, f)
		},
	}
	a.profilesFlag(cmd)
	cmd.Flags().StringSliceVar(&projects, "project", nil, "a project to provision, NAME or NAME:ENGINE; repeat or comma-separate for more")
	cmd.Flags().BoolVar(&noDashboard, "no-dashboard", false, "stop after provisioning instead of serving the dashboard")
	dashboardFlagsOn(cmd, &f)
	return cmd
}

type projectSpec struct{ name, engine string }

// parseProjects reads NAME and NAME:ENGINE. Names are checked by
// scripts/new-project, which owns the rule; only the shape is checked here.
func parseProjects(raw []string) ([]projectSpec, error) {
	var out []projectSpec
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		name, eng, _ := strings.Cut(r, ":")
		if name == "" || strings.Contains(eng, ":") {
			return nil, usagef("--project %q: write NAME or NAME:ENGINE", r)
		}
		out = append(out, projectSpec{name: name, engine: eng})
	}
	return out, nil
}

// enabledEngines are the engines in the profiles this run boots.
func (a *app) enabledEngines(c *cobra.Command) ([]string, error) {
	root, err := a.root()
	if err != nil {
		return nil, err
	}
	env, err := a.scriptEnv(c.Context(), root)
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
	doc, err := endpoint.Resolve(m, s, endpoint.Options{Profiles: envValue(env, "COMPOSE_PROFILES")})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range doc.Endpoints {
		if e.Enabled {
			out = append(out, e.Engine)
		}
	}
	if len(out) == 0 {
		return nil, usagef("no engine is in the profiles %s; name one with --project NAME:ENGINE", envValue(env, "COMPOSE_PROFILES"))
	}
	return out, nil
}
