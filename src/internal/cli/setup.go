package cli

import (
	"github.com/spf13/cobra"
)

// Setup and config: init, self-update, config validate|render|prod-overlay.
// Each wraps the script its make target ran (docs/reference/parity.tsv).

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
		Short: "Validate and render the configuration",
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
	cmd.AddCommand(validate, render, overlay)
	return cmd
}
