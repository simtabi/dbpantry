package cli

import (
	"bytes"
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/doctor"
)

// doctorCmd reports host, binary and container-runtime facts (design.md D-93).
func (a *app) doctorCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Host, binary and container-runtime facts for this machine",
		Long: `Print what dbpantry sees on this machine: Go build, hostname, instance
home, deployment backend, and the container runtime scripts/runtime info
detects (Docker Desktop, OrbStack, Colima, Rancher Desktop, Podman, nerdctl /
containerd, Apple container via socktainer). Use -o json for automation.

The dashboard System page (/system) shows the same report. This binary is the
launcher for the stack and the dashboard (dbpantry dashboard / make dashboard);
there is no second Go tool.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return a.runDoctor(c.Context(), strings.EqualFold(out, "json"))
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "text", "text or json")
	return cmd
}

func (a *app) runDoctor(ctx context.Context, asJSON bool) error {
	root, err := a.root()
	if err != nil {
		return err
	}
	home := a.home(root)
	rep := doctor.Host(version, commit, root, home)
	if s, err := a.settings(root); err == nil {
		if b := s.Effective("MDB_BACKEND"); b != "" {
			rep.Backend = b
		} else {
			rep.Backend = "compose"
		}
	} else {
		rep.Backend = "compose"
		rep.Notes = append(rep.Notes, "settings: "+err.Error())
	}

	env, err := a.scriptEnv(ctx, root)
	if err != nil {
		rep.Notes = append(rep.Notes, "script env: "+err.Error())
	} else {
		var buf bytes.Buffer
		st := script("scripts/runtime", "info")
		if err := a.runStepTo(ctx, root, env, st, &buf, &buf); err != nil {
			rep.Notes = append(rep.Notes, "runtime info: "+err.Error())
		}
		if buf.Len() > 0 {
			rt := doctor.ParseRuntimeInfo(buf.String())
			rep.Runtime = &rt
		}
	}
	if rep.Runtime == nil || !rep.Runtime.Reachable {
		rep.Notes = append(rep.Notes, "No reachable container engine. Install Docker Desktop, OrbStack, Colima, Rancher Desktop, Podman, or nerdctl; see docs/guides/runtimes.md.")
	}

	if asJSON {
		return doctor.WriteJSON(a.stdout, rep)
	}
	return doctor.WriteText(a.stdout, rep)
}
