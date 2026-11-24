package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/endpoint"
)

// ctxKeyJobKill marks a context from a dashboard job: Cancel must end the
// child process. Interactive shells keep the opposite (wait for the script).
type ctxKeyJobKill struct{}

// ContextWithJobKill returns ctx that makes runStepTo kill scripts on cancel
// (dashboard abort). Without it, non-Interruptible steps ignore cancel so a
// Ctrl-C in psql cancels a query instead of ending the session.
func ContextWithJobKill(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyJobKill{}, true)
}

func jobKillOnCancel(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyJobKill{}).(bool)
	return v
}

// step is one script run from the checkout's root. Phase 2 of the refactor
// plan puts the binary in front of the scripts without changing them: every
// command below runs the script `make` ran, with the arguments `make` passed.
type step struct {
	// Args[0] is a path relative to the root (scripts/backup).
	Args []string
	// Env is added to the process environment.
	Env []string
	// Input, when set, is the script's standard input: the answer to its
	// typed confirmation (--confirm). Otherwise the script reads ours.
	Input *string
	// Quiet discards the script's standard output.
	Quiet bool
	// Interruptible ends the script when the context is done (a log stream
	// whose page went away). Otherwise the binary waits for the script.
	Interruptible bool
}

func (s step) String() string {
	parts := append([]string{}, s.Env...)
	for _, a := range s.Args {
		parts = append(parts, shellQuote(a))
	}
	if s.Input != nil {
		parts = append(parts, "<<< "+shellQuote(strings.TrimSuffix(*s.Input, "\n")))
	}
	return strings.Join(parts, " ")
}

// shellQuote quotes an argument for display, so --dry-run prints a line that
// can be pasted into a shell.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, unsafeInShell) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// unsafeInShell is true for a rune a shell could read as syntax.
func unsafeInShell(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	default:
		return !strings.ContainsRune("/.-_,=:", r)
	}
}

// script is a step running one of the repository's scripts.
func script(args ...string) step { return step{Args: args} }

// runSteps runs the steps in order and stops at the first failure, whose exit
// status becomes the binary's (statusError). Under --dry-run it prints them.
func (a *app) runSteps(c *cobra.Command, steps ...step) error {
	root, err := a.root()
	if err != nil {
		return err
	}
	env, err := a.scriptEnv(c.Context(), root)
	if err != nil {
		return err
	}
	if a.dryRun {
		for _, s := range steps {
			if _, err := fmt.Fprintln(a.stdout, s.String()); err != nil {
				return err
			}
		}
		return nil
	}
	for _, s := range steps {
		if err := a.runStep(c.Context(), root, env, s); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) runStep(ctx context.Context, root string, env []string, s step) error {
	return a.runStepTo(ctx, root, env, s, a.stdout, a.stderr)
}

// runStepTo runs a step with its output sent to stdout and stderr: the
// terminal, or the lines a formatted view reads (pretty.go).
func (a *app) runStepTo(ctx context.Context, root string, env []string, s step, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, filepath.Join(root, s.Args[0]), s.Args[1:]...) // #nosec G204 -- a fixed script of this repository; the arguments are the caller's, as make passed them
	// An interrupt reaches the script from the terminal, as it did under
	// make; the binary waits for it rather than killing it, so Ctrl-C in a
	// psql shell cancels a query and does not end the session. Dashboard
	// jobs (ContextWithJobKill) keep the default Cancel so Abort ends them.
	if !s.Interruptible && !jobKillOnCancel(ctx) {
		cmd.Cancel = func() error { return nil }
	}
	// A dashboard job, or the page's log stream, ends with everything the
	// script started, and never waits more than WaitDelay for a child still
	// holding the output pipe once the script has gone.
	if jobKillOnCancel(ctx) {
		killGroupOnCancel(cmd)
		cmd.WaitDelay = 5 * time.Second
	}
	cmd.Dir = root
	cmd.Env = append(append([]string{}, env...), s.Env...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if s.Quiet {
		cmd.Stdout = io.Discard
	}
	switch {
	case s.Input != nil:
		cmd.Stdin = strings.NewReader(*s.Input)
	case a.noInput:
		cmd.Stdin = nil // /dev/null: a prompt reads end of input and refuses
	default:
		cmd.Stdin = a.stdin
	}
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ee) && ee.ExitCode() > 0:
		return statusError{step: s.Args[0], code: ee.ExitCode()}
	case ctx.Err() != nil:
		return ctx.Err()
	default:
		return fmt.Errorf("%s: %w", s.Args[0], err)
	}
}

// scriptEnv is the environment every script runs with: ours, plus what the
// Makefile exports when it is not already set. make computes
// COMPOSE_PROFILES from PROFILES, then MDB_PROFILES in the env file, then
// the default, through scripts/profiles; run directly, the binary does the
// same, and --profiles wins over all three. PATH always gains the well-known
// container CLI directories (D-88), so a thin launchd/agent PATH still finds
// Docker Desktop.
func (a *app) scriptEnv(ctx context.Context, root string) ([]string, error) {
	env := withContainerPath(append([]string{}, a.environ()...))
	if a.noColor {
		env = append(env, "NO_COLOR=1")
	}
	if a.env("COMPOSE_PROFILES") != "" && a.profiles == "" {
		return env, nil
	}
	raw := a.profiles
	if raw == "" {
		raw = a.env("PROFILES")
	}
	if raw == "" {
		s, err := a.settings(root)
		if err != nil {
			return nil, err
		}
		raw = s.File("MDB_PROFILES")
	}
	if raw == "" {
		raw = endpoint.DefaultProfiles
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, filepath.Join(root, "scripts", "profiles"), raw) // #nosec G204 -- the repository's own script; the profile list is an argument
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = root, env, &out, a.stderr
	if err := cmd.Run(); err != nil {
		return nil, usagef("profiles %q: %v", raw, err)
	}
	return append(env, "COMPOSE_PROFILES="+strings.TrimSpace(out.String())), nil
}

// runInHome runs one step in another instance home (env copy, env migrate)
// with the environment scriptEnv would give it were that home this one. The
// caller's COMPOSE_PROJECT_NAME, MDB_ENV_FILE, COMPOSE_PROFILES and PROFILES
// are dropped, since each outranks the home's .env, so the step reaches that
// home's own stack: its project from its .env, its profiles from its
// MDB_PROFILES (2026-11-24 audit, K3).
func (a *app) runInHome(c *cobra.Command, home string, s step) error {
	root, err := a.root()
	if err != nil {
		return err
	}
	base := a.environ
	b := *a
	b.profiles = ""
	b.environ = func() []string {
		out := make([]string, 0, len(base())+1)
		for _, kv := range base() {
			switch k, _, _ := strings.Cut(kv, "="); k {
			case "MDB_HOME", "MDB_ENV_FILE", "COMPOSE_PROJECT_NAME", "COMPOSE_PROFILES", "PROFILES":
				continue
			}
			out = append(out, kv)
		}
		return append(out, "MDB_HOME="+home)
	}
	env, err := b.scriptEnv(c.Context(), root)
	if err != nil {
		return err
	}
	return a.runStep(c.Context(), root, env, s)
}

// confirmed returns a step's standard input for a typed confirmation: the
// --confirm value, or nil to let the script prompt. Under --no-input, with
// no --confirm and nothing else satisfying the prompt, it refuses: exit 7.
func (a *app) confirmed(what, value string, satisfied bool) (*string, error) {
	if value != "" {
		in := value + "\n"
		return &in, nil
	}
	if !satisfied && a.noInput {
		return nil, confirmError{what: what}
	}
	return nil, nil
}

// libValue prints one value computed by the repository's bash libraries
// (the image prefix, the project name), so the binary never keeps a second
// copy of a default the scripts own.
func (a *app) libValue(ctx context.Context, root, expr string) (string, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "bash", "-c", expr) // #nosec G204 -- a fixed expression of this package
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = root, withContainerPath(a.environ()), &out, a.stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", expr, err)
	}
	return strings.TrimSpace(out.String()), nil
}

func boolArg(b bool, flag string) []string {
	if b {
		return []string{flag}
	}
	return nil
}

func strArg(c *cobra.Command, name, value string) []string {
	if c.Flags().Changed(name) {
		return []string{"--" + name, value}
	}
	return nil
}

// sanitize removes terminal control sequences from a value bound for a
// terminal (N19): a database name or an error message carrying ESC could
// otherwise move the cursor, retitle the window or hide text.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		// C0 and C1 controls, DEL, and bytes that are not UTF-8 (a lone
		// 0x9b is the one-byte CSI on some terminals).
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
}
