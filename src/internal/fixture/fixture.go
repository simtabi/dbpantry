// Package fixture boots and stops disposable engines for test suites (G3):
// mesobia's driver conformance suite runs against them.
//
// It wraps the scripts rather than replacing them (the strangler step of the
// refactor plan §3): `fixture up` runs what `make test-profile` runs, with the
// profiles the caller names, and `fixture down` stops that stack. The test
// overlay (compose.test.yml) puts every engine's data on tmpfs, so a fixture
// leaves no volume behind (check 64).
package fixture

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/simtabi/dbpantry/src/internal/engine"
)

// ComposeFiles are the files a fixture runs with, in order. Listing any -f
// replaces COMPOSE_FILE, so the generated engines file is named explicitly,
// as `make test-profile` names it.
var ComposeFiles = []string{"docker-compose.yml", "compose.engines.yml", "compose.test.yml"}

// Step is one command of a plan, run from the repository root.
type Step struct {
	Args []string
	// Env is added to the process environment.
	Env []string
	// Quiet discards the command's standard output.
	Quiet bool
}

func (s Step) String() string {
	return strings.TrimSpace(strings.Join(s.Env, " ") + " " + strings.Join(s.Args, " "))
}

// Runner executes steps. Tests substitute one that records them.
type Runner interface {
	Run(ctx context.Context, root string, s Step) error
}

// ExecRunner runs steps as processes, with the caller's stdout and stderr.
type ExecRunner struct {
	Stdout, Stderr io.Writer
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, root string, s Step) error {
	cmd := exec.CommandContext(ctx, filepath.Join(root, s.Args[0]), s.Args[1:]...) // #nosec G204 -- a fixed script of this repository, not user input
	cmd.Dir = root
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.Stdout, cmd.Stderr = r.Stdout, r.Stderr
	if s.Quiet {
		cmd.Stdout = io.Discard
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", s.String(), err)
	}
	return nil
}

// ValidateProfiles refuses any profile a fixture cannot honour. A fixture
// runs engines and their poolers only: the ui, backup, metrics, prod and ha
// profiles start services with volumes of their own, and a fixture that
// leaves a volume behind is not disposable.
func ValidateProfiles(m engine.Manifest, profiles []string) error {
	if len(profiles) == 0 {
		return fmt.Errorf("no profiles: name the engines, e.g. --profiles pg,mysql")
	}
	allowed := map[string]bool{}
	var names []string
	for _, e := range m.Implemented() {
		allowed[e.Name] = true
		names = append(names, e.Name)
		if e.Pooler != nil {
			allowed[e.Pooler.Service] = true
		}
	}
	for _, p := range profiles {
		if !allowed[p] {
			if e, ok := m.Engine(p); ok && !e.Implemented {
				return fmt.Errorf("profile %q: %s is declared but its image does not exist yet (images/%s/Dockerfile)", p, e.Title, e.Name)
			}
			return fmt.Errorf("profile %q is not available to a fixture; use engine profiles (%s) and pooler", p, strings.Join(names, ", "))
		}
	}
	return nil
}

// UpPlan is what `fixture up` runs: render the templated files, generate the
// engine services, and boot the selected profiles with the test overlay,
// waiting for health (scripts/runtime up).
func UpPlan(profiles []string) []Step {
	env := []string{"COMPOSE_PROFILES=" + strings.Join(profiles, ",")}
	return []Step{
		{Args: []string{"scripts/render-config"}, Quiet: true},
		{Args: []string{"scripts/gen-compose"}, Quiet: true},
		{Args: append([]string{"scripts/runtime", "up"}, fileArgs()...), Env: env},
	}
}

// DownPlan is what `fixture down` runs: stop the fixture stack. It removes no
// volume: the overlay never created one, and `down` never touches data.
func DownPlan() []Step {
	return []Step{{Args: append([]string{"scripts/runtime", "down"}, fileArgs()...)}}
}

// Run executes a plan in order, stopping at the first failure.
func Run(ctx context.Context, r Runner, root string, plan []Step) error {
	for _, s := range plan {
		if err := r.Run(ctx, root, s); err != nil {
			return err
		}
	}
	return nil
}

func fileArgs() []string {
	var out []string
	for _, f := range ComposeFiles {
		out = append(out, "-f", f)
	}
	return out
}
