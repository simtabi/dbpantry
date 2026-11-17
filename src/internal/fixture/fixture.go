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
	"encoding/binary"
	"fmt"
	"io"
	"net"
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
func UpPlan(profiles []string, fx []string) []Step {
	env := append([]string{"COMPOSE_PROFILES=" + strings.Join(profiles, ",")}, fx...)
	return []Step{
		{Args: []string{"scripts/render-config"}, Quiet: true},
		{Args: []string{"scripts/gen-compose"}, Quiet: true},
		{Args: append([]string{"scripts/runtime", "up"}, fileArgs()...), Env: env},
	}
}

// DownPlan is what `fixture down` runs: stop the fixture stack. It removes no
// volume: the overlay never created one, and `down` never touches data.
func DownPlan(fx []string) []Step {
	return []Step{{Args: append([]string{"scripts/runtime", "down"}, fileArgs()...), Env: fx}}
}

// DefaultPortBase is where a fixture's host ports start when
// MDB_FIXTURE_PORT_BASE is unset: far enough from the long-lived stack's
// MDB_PORT_BASE (54000) that both run at once.
const DefaultPortBase = 55000

// SecretsSubdir is the directory under secrets/ that new-project writes a
// fixture project's password files to, so Cleanup drops them together with
// the engines' tmpfs data.
const SecretsSubdir = "fixture"

// ProjectName is the compose project a fixture runs as. It is its own:
// until 2026-11-17 a fixture ran as the long-lived stack's project, so
// `fixture up` replaced every application's engine with a tmpfs one and
// `fixture down` stopped them all.
func ProjectName(stack string) string {
	if stack == "" {
		stack = "mdb"
	}
	return stack + "-fixture"
}

// Env is the environment of every fixture step, and of new-project and
// endpoints aimed at the fixture (--fixture): its compose project, MDB_FIXTURE=1
// (new-project's switch to secrets/fixture/), and each engine's and pooler's
// host port at portBase + offset, overriding the defaults gen-compose bakes
// from MDB_PORT_BASE. It sets neither MDB_PUBLISH nor MDB_PORT_BASE, which
// gen-compose reads from the env file only.
func Env(m engine.Manifest, stack string, portBase int, subnet string) ([]string, error) {
	env := []string{"COMPOSE_PROJECT_NAME=" + ProjectName(stack), "MDB_FIXTURE=1", "MDB_NETWORK_SUBNET=" + subnet}
	poolers := 0
	for _, e := range m.Implemented() {
		up := strings.ToUpper(strings.ReplaceAll(e.Name, "-", "_"))
		env = append(env, fmt.Sprintf("MDB_%s_HOST_PORT=%d", up, portBase+e.PortOffset))
		if e.Pooler != nil {
			poolers++
			addr, err := PoolerAddr(subnet, poolers)
			if err != nil {
				return nil, err
			}
			env = append(env,
				fmt.Sprintf("MDB_%s_POOLER_HOST_PORT=%d", up, portBase+e.PortOffset+PoolerPortStep),
				fmt.Sprintf("MDB_FIXTURE_%s_POOLER_IP=%s", up, addr),
				fmt.Sprintf("MDB_FIXTURE_%s_POOLER_ADDR=%s/32", up, addr))
		}
	}
	return env, nil
}

// DefaultSubnet is the fixture network's subnet when MDB_FIXTURE_NETWORK_SUBNET
// is unset. It must differ from the stack's (MDB_NETWORK_SUBNET, default
// 172.28.0.0/16): Docker refuses a second network on an overlapping pool, so
// until 2026-11-17 `fixture up` failed whenever the stack was running.
const DefaultSubnet = "172.29.0.0/16"

// PoolerAddr is scripts/gen-compose's pooler_addr: the idx-th address down
// from the subnet's broadcast address, where Docker's dynamic allocation (from
// the bottom) never reaches. Poolers are numbered from 1 in engine-name order,
// as gen-compose numbers them.
func PoolerAddr(subnet string, idx int) (string, error) {
	_, n, err := net.ParseCIDR(subnet)
	if err != nil || n.IP.To4() == nil {
		return "", fmt.Errorf("fixture subnet %q is not an IPv4 CIDR", subnet)
	}
	ones, _ := n.Mask.Size()
	base := binary.BigEndian.Uint32(n.IP.To4())
	host := uint32(1)<<(32-ones) - 1
	if uint32(idx) >= host {
		return "", fmt.Errorf("fixture subnet %q is too small for %d poolers", subnet, idx)
	}
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, (base|host)-uint32(idx))
	return ip.String(), nil
}

// Overlaps reports whether two IPv4 CIDRs share any address.
func Overlaps(a, b string) bool {
	_, x, errA := net.ParseCIDR(a)
	_, y, errB := net.ParseCIDR(b)
	if errA != nil || errB != nil {
		return false
	}
	return x.Contains(y.IP) || y.Contains(x.IP)
}

// PoolerPortStep mirrors endpoint.PoolerPortStep: a pooler publishes at its
// engine's port + 100. endpoint's TestPoolerPortStepMatchesFixture keeps the two
// equal (endpoint cannot be imported here without a cycle in its tests).
const PoolerPortStep = 100

// Cleanup removes the password files new-project wrote for the fixture's
// projects. Their roles lived in the fixture's tmpfs and are gone once it is
// down, so the files would only accumulate.
func Cleanup(root string) error {
	return os.RemoveAll(filepath.Join(root, "secrets", SecretsSubdir))
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
