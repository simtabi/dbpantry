package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Topology (ha, pooler), security (certs, secrets, images) and development
// (verify, dev). Each wraps the script its make target ran, except pooler
// sync (refactor plan §14, N8), which restarts a Compose service.

func (a *app) haCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ha",
		Short: "The Patroni cluster: status, controlled failover, replica rebuild",
	}
	var node, confirm string
	status := &cobra.Command{
		Use:   "status",
		Short: "Show cluster topology and replication lag",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return a.runSteps(c, script("scripts/ha", "status")) },
	}
	failover := &cobra.Command{
		Use:   "failover [--node NODE]",
		Short: "Controlled switchover to another node (typed confirmation: the cluster name)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			in, err := a.confirmed("cluster", confirm, false)
			if err != nil {
				return err
			}
			return a.runSteps(c, step{Args: append([]string{"scripts/ha", "failover"}, strArg(c, "node", node)...), Input: in})
		},
	}
	failover.Flags().StringVar(&node, "node", "", "the node to promote (default: Patroni chooses)")
	reinit := &cobra.Command{
		Use:   "reinit --node NODE",
		Short: "Rebuild one replica from the current leader (typed confirmation: the node)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			in, err := a.confirmed("node", confirm, false)
			if err != nil {
				return err
			}
			return a.runSteps(c, step{Args: []string{"scripts/ha", "reinit", "--node", node}, Input: in})
		},
	}
	reinit.Flags().StringVar(&node, "node", "", "the replica to rebuild")
	for _, c := range []*cobra.Command{failover, reinit} {
		c.Flags().StringVar(&confirm, "confirm", "", "the typed confirmation the script asks for")
	}
	cmd.AddCommand(status, failover, reinit)
	return cmd
}

func (a *app) poolerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pooler",
		Short: "The engines' connection poolers",
	}
	sync := &cobra.Command{
		Use:   "sync ENGINE",
		Short: "Make the engine's pooler see users created or changed since it started",
		Long: `ProxySQL cannot look a credential up on demand: it mirrors the server's
password verifiers when it starts (config/proxysql/bootstrap.sh). A user that
new-project creates, or a password that rotation changes, afterwards cannot
log in through it until it mirrors again. sync restarts the engine's pooler
when it mirrors at start (its descriptor names MDB_ENGINE_POOLER_BOOTSTRAP)
and is running; pgBouncer looks credentials up per login and needs nothing.
Compose backend only.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error { return a.syncPooler(c, args[0], false) },
	}
	cmd.AddCommand(sync)
	return cmd
}

// syncPooler restarts eng's pooler when it mirrors credentials at start and
// is running. quiet (after new-project or rotation) says nothing when there
// is nothing to do.
func (a *app) syncPooler(c *cobra.Command, eng string, quiet bool) error {
	m, root, err := a.manifest()
	if err != nil {
		return err
	}
	e, ok := m.Engine(eng)
	if !ok {
		return usagef("no engine %q in engines/", eng)
	}
	say := func(format string, v ...any) error {
		if quiet {
			return nil
		}
		_, err := fmt.Fprintf(a.stdout, format+"\n", v...)
		return err
	}
	if e.Pooler == nil {
		if quiet {
			return nil
		}
		return usagef("%s has no pooler", e.Name)
	}
	if e.Descriptor["MDB_ENGINE_POOLER_BOOTSTRAP"] == "" {
		return say("%s looks credentials up on each login; nothing to sync", e.Pooler.Service)
	}
	s, err := a.settings(root)
	if err != nil {
		return err
	}
	if b := s.Effective("MDB_BACKEND"); b != "" && b != "compose" {
		if quiet {
			return nil
		}
		return usagef("pooler sync runs on the compose backend only (MDB_BACKEND=%s)", b)
	}
	if !a.dryRun {
		running, err := a.libValue(c.Context(), root,
			". scripts/runtime-lib.sh && mdb_service_running "+shellQuote(e.Pooler.Service)+" && echo yes || true")
		if err != nil {
			return err
		}
		if strings.TrimSpace(running) != "yes" {
			return say("%s is not running; it mirrors when it starts", e.Pooler.Service)
		}
	}
	if err := a.runSteps(c, step{Args: []string{"scripts/runtime", "compose", "restart", e.Pooler.Service}, Quiet: true}); err != nil {
		return err
	}
	if !a.dryRun {
		_, err = fmt.Fprintf(a.stdout, "restarted %s; it mirrors the server's users as it starts\n", e.Pooler.Service)
	}
	return err
}

func (a *app) certsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "certs",
		Short: "The toolkit CA and the engines' server certificates",
	}
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Create the toolkit CA and per-engine server certificates",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return a.runSteps(c, script("scripts/certs")) },
	}
	renew := &cobra.Command{
		Use:   "renew",
		Short: "Rotate the server certificates with a live reload (no downtime)",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return a.runSteps(c, script("scripts/certs-renew")) },
	}
	cmd.AddCommand(initCmd, renew)
	return cmd
}

func (a *app) secretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Database passwords and secret files",
	}
	var yes bool
	var confirm, engine string
	rotate := &cobra.Command{
		Use:   "rotate",
		Short: "Rotate database passwords and secret files, applied live (typed confirmation: ROTATE)",
		Long: `Rotate every database password and secret file and apply them to the running
servers (scripts/rotate-secrets). --engine limits it to one engine: its
superuser and every project role on it. The script asks for ROTATE;
--confirm=ROTATE answers it. --yes is make rotate-secrets YES=1, kept for
compatibility. Poolers that mirror credentials at start are then restarted
(pooler sync).`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			in, err := a.confirmed("ROTATE", confirm, yes)
			if err != nil {
				return err
			}
			args := append([]string{"scripts/rotate-secrets"}, boolArg(yes, "--yes")...)
			if engine != "" {
				args = append(args, "--engine", engine)
			}
			if err := a.runSteps(c, step{Args: args, Input: in}); err != nil {
				return err
			}
			m, _, err := a.manifest()
			if err != nil {
				return err
			}
			for _, e := range m.Implemented() {
				if engine != "" && e.Name != engine {
					continue
				}
				if err := a.syncPooler(c, e.Name, true); err != nil {
					return err
				}
			}
			return nil
		},
	}
	rotate.Flags().BoolVar(&yes, "yes", false, "skip the prompt (make rotate-secrets YES=1)")
	rotate.Flags().StringVar(&confirm, "confirm", "", "the typed confirmation: ROTATE")
	rotate.Flags().StringVar(&engine, "engine", "", "rotate only this engine's passwords (pg, mysql or mariadb)")
	cmd.AddCommand(rotate)
	return cmd
}

func (a *app) imagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "images",
		Short: "Build and scan the engine images",
	}
	build := &cobra.Command{
		Use:   "build [ENGINE...]",
		Short: "Build the engine images for this architecture (default: every engine)",
		RunE: func(c *cobra.Command, args []string) error {
			return a.runSteps(c, script(append([]string{"scripts/build"}, args...)...))
		},
	}
	scan := &cobra.Command{
		Use:   "scan [ENGINE]",
		Short: "Scan built images for vulnerabilities (honours .trivyignore.yaml)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return a.runSteps(c, script(append([]string{"scripts/scan"}, args...)...))
		},
	}
	cmd.AddCommand(build, scan)
	return cmd
}

func (a *app) verifyCmd() *cobra.Command {
	var fast, structure bool
	var checks, tags string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Run the acceptance harness (spec section 18 as scripts)",
		Long: `Run the acceptance harness (scripts/verify/run.sh). --checks 21,28 or
--tags pg,backup narrow it; --fast runs the inner-loop subset (tag fast);
--structure validates the harness itself and runs no check.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if structure {
				return a.runSteps(c, script("scripts/verify/run.sh", "--structure-only"))
			}
			if fast {
				tags = "fast"
			}
			return a.runSteps(c, step{Args: []string{"scripts/verify/run.sh"},
				Env: []string{"VERIFY_CHECKS=" + checks, "VERIFY_TAGS=" + tags}})
		},
	}
	cmd.Flags().BoolVar(&fast, "fast", false, "the inner-loop subset (checks tagged fast)")
	cmd.Flags().BoolVar(&structure, "structure", false, "validate the harness only")
	cmd.Flags().StringVar(&checks, "checks", "", "only these checks, e.g. 21,28")
	cmd.Flags().StringVar(&tags, "tags", "", "only checks carrying one of these tags, e.g. pg,backup")
	return cmd
}

func (a *app) devCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Development tools for this repository",
	}
	lint := &cobra.Command{
		Use:   "lint",
		Short: "shellcheck every script in the repository",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return a.runSteps(c, script("scripts/lint.sh")) },
	}
	notes := &cobra.Command{
		Use:   "release-notes VERSION",
		Short: "Print one version's CHANGELOG section (or Unreleased), the body of its GitHub release",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return a.runSteps(c, script("scripts/release-notes", args[0]))
		},
	}
	cmd.AddCommand(lint, notes)
	return cmd
}
