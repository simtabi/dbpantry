package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/fixture"
)

// The stack: up, down, status, logs, forward, destroy, runtime, shell, tools.
// Every one runs what its make target ran (docs/reference/parity.tsv).

func (a *app) profilesFlag(c *cobra.Command) {
	c.Flags().StringVar(&a.profiles, "profiles", "", "profiles to run, e.g. pg,mysql,ui (default: $COMPOSE_PROFILES from make, else $PROFILES, else MDB_PROFILES, else pg,ui)")
}

func (a *app) upCmd() *cobra.Command {
	var overlay string
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Boot the stack and wait until it is healthy (never touches data)",
		Long: `Validate the env file, render the templated files, generate the engine
services, boot the selected profiles and wait for health, then say how to
connect. --profile test boots them on tmpfs with durability off instead
(compose.test.yml): data is discarded on stop.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			switch overlay {
			case "":
				if a.formatted() {
					return a.upFormatted(c)
				}
				return a.runSteps(c,
					script("scripts/check-env", "--quiet"),
					script("scripts/render-config"),
					step{Args: []string{"scripts/gen-compose"}, Quiet: true},
					// up -d --wait on Compose v2; podman-compose and nerdctl
					// compose have no --wait, so scripts/runtime polls health.
					script("scripts/runtime", "up"),
					script("scripts/exposure-notice"),
				)
			case "test":
				if !a.dryRun {
					_, _ = a.stdout.Write([]byte("TEST PROFILE: data lives on tmpfs and is DISCARDED on stop.\n"))
				}
				up := []string{"scripts/runtime", "up"}
				for _, f := range fixture.ComposeFiles {
					up = append(up, "-f", f)
				}
				return a.runSteps(c,
					script("scripts/render-config"),
					step{Args: []string{"scripts/gen-compose"}, Quiet: true},
					script(up...),
				)
			default:
				return usagef("--profile %q: the only overlay is test", overlay)
			}
		},
	}
	a.profilesFlag(cmd)
	cmd.Flags().StringVar(&overlay, "profile", "", "an overlay to boot with: test (tmpfs, durability off)")
	return cmd
}

func (a *app) downCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop the stack (never touches data; deleting volumes is destroy)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if a.formatted() {
				return a.downFormatted(c)
			}
			return a.runSteps(c, script("scripts/runtime", "down"))
		},
	}
}

func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show service status",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return a.runSteps(c, script("scripts/runtime", "status")) },
	}
}

func (a *app) logsCmd() *cobra.Command {
	var follow bool
	var tail string
	cmd := &cobra.Command{
		Use:   "logs [SERVICE...]",
		Short: "Show service logs (followed, last 100 lines, by default)",
		RunE: func(c *cobra.Command, args []string) error {
			argv := []string{"scripts/runtime", "logs"}
			argv = append(argv, boolArg(follow, "-f")...)
			argv = append(argv, "--tail="+tail)
			return a.runSteps(c, script(append(argv, args...)...))
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", true, "keep following new lines")
	cmd.Flags().StringVar(&tail, "tail", "100", "lines to show from the end of each log (all for every line)")
	return cmd
}

func (a *app) forwardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forward [SERVICE...]",
		Short: "Reach the services from this machine (Kubernetes: kubectl port-forward)",
		RunE: func(c *cobra.Command, args []string) error {
			return a.runSteps(c, script(append([]string{"scripts/runtime", "forward"}, args...)...))
		},
	}
}

func (a *app) destroyCmd() *cobra.Command {
	var volume, confirm string
	var all, recovery bool
	cmd := &cobra.Command{
		Use:   "destroy (--volume NAME | --all)",
		Short: "Delete data volumes, keeping recovery repositories unless asked (typed confirmation)",
		Long: `Delete one data volume (--volume) or every one (--all). Recovery repositories
(PITR) are kept unless --include-recovery. The script asks for the volume name,
or DESTROY ALL; --confirm answers it, and --yes never does.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if (volume == "") == !all {
				return usagef("name one volume with --volume, or pass --all")
			}
			argv := []string{"scripts/destroy"}
			if all {
				argv = append(argv, "--all")
			} else {
				argv = append(argv, "--volume", volume)
			}
			argv = append(argv, boolArg(recovery, "--include-recovery")...)
			in, err := a.confirmed("destroy", confirm, false)
			if err != nil {
				return err
			}
			return a.runSteps(c, step{Args: argv, Input: in})
		},
	}
	cmd.Flags().StringVar(&volume, "volume", "", "the data volume to delete")
	cmd.Flags().BoolVar(&all, "all", false, "delete every data volume of the project")
	cmd.Flags().BoolVar(&recovery, "include-recovery", false, "delete the recovery repositories too")
	cmd.Flags().StringVar(&confirm, "confirm", "", "the typed confirmation: the volume name, or DESTROY ALL")
	return cmd
}

func (a *app) runtimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "The detected container runtime, Compose implementation and backend",
	}
	info := &cobra.Command{
		Use:   "info",
		Short: "What was detected, and why (CONTAINER_RUNTIME= to force)",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return a.runSteps(c, script("scripts/runtime", "info")) },
	}
	name := &cobra.Command{
		Use:   "name",
		Short: "The container runtime: docker, podman or nerdctl",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return a.runSteps(c, script("scripts/runtime", "name")) },
	}
	backend := &cobra.Command{
		Use:   "backend",
		Short: "The deployment backend: compose, kubernetes or incus",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return a.runSteps(c, script("scripts/runtime", "backend")) },
	}
	cmd.AddCommand(info, name, backend)
	return cmd
}

func (a *app) shellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell SERVICE -- COMMAND [ARG...]",
		Short: "Run a client in a service's container, with a terminal (make psql, mysql, mongo, ...)",
		Long: `Run COMMAND in SERVICE's container with a terminal when there is one
(scripts/runtime exec). Each engine's client and how it authenticates are
still written in its make target (make psql, make mysql, make mariadb,
make mongo, make cassandra); the descriptors take them over in phase 4.`,
		Args: func(c *cobra.Command, args []string) error {
			if c.ArgsLenAtDash() != 1 || len(args) < 2 {
				return usagef("usage: dbpantry shell SERVICE -- COMMAND [ARG...] (or make psql, make mysql, ...)")
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			return a.runSteps(c, script(append([]string{"scripts/runtime", "exec"}, args...)...))
		},
	}
}

func (a *app) toolsCmd() *cobra.Command {
	var image string
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Open the cli image: database tools that need no server, on the stack's network",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			root, err := a.root()
			if err != nil {
				return err
			}
			project, err := a.libValue(c.Context(), root, ". scripts/runtime-lib.sh && mdb_project_name")
			if err != nil {
				return err
			}
			if image == "" {
				prefix, err := a.libValue(c.Context(), root, ". scripts/env-lib.sh && mdb_image_prefix")
				if err != nil {
					return err
				}
				image = prefix + "/dbpantry-cli:dev"
			}
			return a.runSteps(c, script("scripts/runtime", "ctr", "run", "--rm", "-it",
				"--network", project+"_net",
				"-v", filepath.Join(root, "certs")+":/certs:ro",
				"-v", filepath.Join(root, "backups")+":/work/backups",
				image))
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "the cli image (default: <MDB_IMAGE_PREFIX>/dbpantry-cli:dev)")
	return cmd
}
