package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/endpoint"
	"github.com/simtabi/dbpantry/src/internal/fixture"
)

func (a *app) manifestCmd() *cobra.Command {
	var output, matrix string
	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "Print the engine manifest generated from engines/*/engine.conf",
		Long: `Print the engine manifest: every descriptor, typed, as JSON (schema_version 1,
docs/reference/manifest.md).

  -o json     the manifest (default)
  -o tsv      every descriptor key as engine<TAB>key<TAB>value
  -o engines  one line per engine: name, implemented, buildable, pooler service
  -o helm     the chart facts each engine block of deploy/helm/dbpantry must restate
  --matrix    the CI build matrix (versions | engines), one line of JSON`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			m, _, err := a.manifest()
			if err != nil {
				return err
			}
			if matrix != "" {
				if err := m.WriteMatrix(a.stdout, matrix); err != nil {
					return usageError{err}
				}
				return nil
			}
			switch output {
			case "json":
				return m.WriteJSON(a.stdout)
			case "tsv":
				return m.WriteTSV(a.stdout)
			case "helm":
				return m.WriteHelm(a.stdout)
			case "engines":
				return m.WriteEngines(a.stdout)
			default:
				return usagef("unknown output %q (want json, tsv, engines or helm)", output)
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "json", "output format: json, tsv, engines or helm")
	cmd.Flags().StringVar(&matrix, "matrix", "", "print the CI build matrix instead: versions or engines")
	cmd.AddCommand(a.checkHelmCmd())
	return cmd
}

func (a *app) checkHelmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check-helm [VALUES]",
		Short: "Fail when the chart's values restate an engine fact differently from its descriptor",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			m, root, err := a.manifest()
			if err != nil {
				return err
			}
			path := filepath.Join(root, "deploy", "helm", "dbpantry", "values.yaml")
			if len(args) == 1 {
				path = args[0]
			}
			b, err := os.ReadFile(path) // #nosec G304 -- a values file the operator names
			if err != nil {
				return err
			}
			problems, err := m.CheckHelm(b)
			if err != nil {
				return err
			}
			if len(problems) > 0 {
				return fmt.Errorf("%s disagrees with engines/:\n  %s", path, strings.Join(problems, "\n  "))
			}
			_, err = fmt.Fprintf(a.stdout, "%s matches engines/ for %d implemented engine(s)\n", path, len(m.Implemented()))
			return err
		},
	}
}

func (a *app) endpointsCmd() *cobra.Command {
	var output, profiles, project string
	cmd := &cobra.Command{
		Use:   "endpoints",
		Short: "Print where to connect to each engine: network, published and pooled endpoints",
		Long: `Print, for every engine the stack can run, its address on the mdb network, the
host port it is published on (MDB_PUBLISH=direct or proxy), its pooled endpoint
when it has a pooler, its boot budget, and its principals as password-file paths.

Superusers cannot authenticate through either pooler, so admin work uses the
direct endpoint ("superuser": false on every pooled one).`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			m, root, err := a.manifest()
			if err != nil {
				return err
			}
			s, err := a.settings(root)
			if err != nil {
				return err
			}
			doc, err := endpoint.Resolve(m, s, endpoint.Options{Profiles: profiles, Project: project})
			if err != nil {
				return err
			}
			switch output {
			case "json":
				enc := json.NewEncoder(a.stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(doc)
			case "table":
				return writeEndpointsTable(a, doc)
			default:
				return usagef("unknown output %q (want json or table)", output)
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table or json")
	cmd.Flags().StringVar(&profiles, "profiles", "", "profiles to report as enabled (default: $PROFILES, else MDB_PROFILES, else pg,ui)")
	cmd.Flags().StringVar(&project, "project", "", "also list the app and read-only principals `make new-project NAME=<project>` creates")
	return cmd
}

func writeEndpointsTable(a *app, doc endpoint.Document) error {
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ENGINE\tENABLED\tNETWORK\tPUBLISHED\tPOOLED\tBOOT BUDGET")
	for _, e := range doc.Endpoints {
		pub, pooled := "-", "-"
		if p := e.Direct.Published; p != nil {
			pub = fmt.Sprintf("%s:%d (%s)", p.Address, p.Port, p.Via)
		}
		if e.Pooled != nil {
			pooled = fmt.Sprintf("%s:%d", e.Pooled.Network.Host, e.Pooled.Network.Port)
			if p := e.Pooled.Published; p != nil {
				pooled += fmt.Sprintf(" / %s:%d", p.Address, p.Port)
			}
		}
		_, _ = fmt.Fprintf(tw, "%s\t%t\t%s:%d\t%s\t%s\t%ds\n",
			e.Engine, e.Enabled, e.Direct.Network.Host, e.Direct.Network.Port, pub, pooled, e.BootBudget)
	}
	return tw.Flush()
}

func (a *app) fixtureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fixture",
		Short: "Boot or stop disposable engines (tmpfs, test overlay) for a test suite",
	}
	var profiles string
	var dryRun bool
	run := func(c *cobra.Command, up bool) error {
		m, root, err := a.manifest()
		if err != nil {
			return err
		}
		s, err := a.settings(root)
		if err != nil {
			return err
		}
		if b := s.Effective("MDB_BACKEND"); b != "" && b != "compose" {
			return usagef("fixtures run on the compose backend only (MDB_BACKEND=%s)", b)
		}
		var plan []fixture.Step
		if up {
			selected := endpoint.Profiles(m, s, profiles)
			if err := fixture.ValidateProfiles(m, selected); err != nil {
				return usageError{err}
			}
			plan = fixture.UpPlan(selected)
		} else {
			plan = fixture.DownPlan()
		}
		if dryRun {
			for _, st := range plan {
				if _, err := fmt.Fprintln(a.stdout, st.String()); err != nil {
					return err
				}
			}
			return nil
		}
		for _, st := range plan {
			a.log.Info("fixture", "run", st.String())
		}
		return fixture.Run(c.Context(), fixture.ExecRunner{Stdout: a.stdout, Stderr: a.stderr}, root, plan)
	}
	up := &cobra.Command{
		Use:   "up",
		Short: "Boot the selected engines on tmpfs and wait until they are healthy",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return run(c, true) },
	}
	up.Flags().StringVar(&profiles, "profiles", "", "engines to boot, e.g. pg,mysql,pooler (default: $PROFILES, else MDB_PROFILES)")
	down := &cobra.Command{
		Use:   "down",
		Short: "Stop the fixture stack (it holds no volume to remove)",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return run(c, false) },
	}
	for _, c := range []*cobra.Command{up, down} {
		c.Flags().BoolVar(&dryRun, "dry-run", false, "print the commands instead of running them")
	}
	cmd.AddCommand(up, down)
	return cmd
}
