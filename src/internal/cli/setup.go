package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/config"
)

// Setup and config: init, self-update, config validate|render|prod-overlay|
// show|set. validate/render wrap the scripts; show and set are Go (phase F).

func (a *app) initCmd() *cobra.Command {
	var prod bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create .env (or, with --prod, .env.prod), generate secrets and certificates, run check-env",
		Long: `Create .env from .env.example, generate every secret and certificate, and
validate the result (scripts/init). With --prod, render .env.prod with
prod-safe values instead; ENV_FILE names another target file.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return a.runSteps(c, script(append([]string{"scripts/init"}, boolArg(prod, "--prod")...)...))
		},
	}
	cmd.Flags().BoolVar(&prod, "prod", false, "render .env.prod (or ENV_FILE) for production")
	return cmd
}

func (a *app) selfUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "self-update",
		Short: "Update the toolkit checkout itself (fast-forward only), never project data",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return a.runSteps(c, script("scripts/self-update")) },
	}
}

func (a *app) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show, edit, validate and render the configuration",
	}
	var quiet bool
	validate := &cobra.Command{
		Use:   "validate",
		Short: "Validate the env file: sentinels, required variables, port collisions (scripts/check-env)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return a.runSteps(c, script(append([]string{"scripts/check-env"}, boolArg(quiet, "--quiet")...)...))
		},
	}
	validate.Flags().BoolVar(&quiet, "quiet", false, "print only problems")
	render := &cobra.Command{
		Use:   "render",
		Short: "Render every templated file and generate the engine services",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return a.runSteps(c, script("scripts/render-config"), step{Args: []string{"scripts/gen-compose"}, Quiet: true})
		},
	}
	overlay := &cobra.Command{
		Use:   "prod-overlay [ENV_FILE]",
		Short: "Add the prod overlay to COMPOSE_FILE in an env file (idempotent; for existing prod installs)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			// No argument: the script's default, the instance's .env
			// ($MDB_HOME/.env, design.md D-84).
			return a.runSteps(c, script(append([]string{"scripts/prod-overlay"}, args...)...))
		},
	}
	var origin bool
	var outFmt string
	show := &cobra.Command{
		Use:   "show",
		Short: "List the effective settings; with --origin, say whether each came from the env or the file",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			root, err := a.root()
			if err != nil {
				return err
			}
			s, err := a.settings(root)
			if err != nil {
				return err
			}
			entries, err := s.Show(filepath.Join(root, ".env.example"))
			if err != nil {
				return err
			}
			if outFmt == "json" {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(entries)
			}
			for _, e := range entries {
				if origin {
					fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\n", e.Key, e.Value, e.Origin)
				} else if e.Value != "" || e.Origin != config.OriginNone {
					fmt.Fprintf(c.OutOrStdout(), "%s=%s\n", e.Key, e.Value)
				}
			}
			return nil
		},
	}
	show.Flags().BoolVar(&origin, "origin", false, "print key, value and origin (env|file|unset)")
	show.Flags().StringVarP(&outFmt, "output", "o", "text", "text or json")
	set := &cobra.Command{
		Use:   "set KEY VALUE",
		Short: "Write one MDB_ key into the instance's env file (quoted per check 50)",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			root, err := a.root()
			if err != nil {
				return err
			}
			s, err := a.settings(root)
			if err != nil {
				return err
			}
			if err := s.Set(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "wrote %s in %s\n", args[0], s.EnvFile)
			return nil
		},
	}
	cmd.AddCommand(validate, render, overlay, show, set)
	return cmd
}
