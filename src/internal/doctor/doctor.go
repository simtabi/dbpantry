// Package doctor collects host, binary and container-runtime facts for
// `dbpantry doctor` and the dashboard System page (design.md D-93).
package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"
)

// Report is one reading of this machine and the detected container runtime.
type Report struct {
	Generated time.Time `json:"generated"`
	Version   string    `json:"version"`
	Commit    string    `json:"commit"`
	GoVersion string    `json:"go_version"`
	GOOS      string    `json:"goos"`
	GOARCH    string    `json:"goarch"`
	NumCPU    int       `json:"num_cpu"`
	Hostname  string    `json:"hostname,omitempty"`
	Root      string    `json:"root,omitempty"`
	Home      string    `json:"home,omitempty"`
	Backend   string    `json:"backend,omitempty"`
	// Runtime is nil until it is measured (`dbpantry doctor` runs
	// scripts/runtime info; the dashboard does not), so a report never
	// claims "reachable": false for a runtime nobody asked.
	Runtime *Runtime `json:"runtime,omitempty"`
	Notes   []string `json:"notes,omitempty"`
}

// Runtime is what scripts/runtime info reports (D-60 adapters).
type Runtime struct {
	Name      string `json:"name,omitempty"`
	Provider  string `json:"provider,omitempty"`
	CLI       string `json:"cli,omitempty"`
	Compose   string `json:"compose,omitempty"`
	Flavor    string `json:"flavor,omitempty"`
	Reachable bool   `json:"reachable"`
	Rootless  bool   `json:"rootless"`
	Raw       string `json:"raw,omitempty"`
}

// Host fills the fields that need no container runtime.
func Host(version, commit, root, home string) Report {
	host, _ := os.Hostname()
	return Report{
		Generated: time.Now().UTC(),
		Version:   version,
		Commit:    commit,
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		NumCPU:    runtime.NumCPU(),
		Hostname:  host,
		Root:      root,
		Home:      home,
	}
}

// ParseRuntimeInfo fills Runtime from the text of `scripts/runtime info`
// (and tolerates a failing engine: the script still prints the facts).
func ParseRuntimeInfo(raw string) Runtime {
	rt := Runtime{Raw: strings.TrimSpace(raw)}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "backend:"):
			// handled by the caller via Report.Backend when set separately
		case strings.HasPrefix(line, "runtime:"):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "runtime:"))
			rt.Name = firstToken(rest)
			if i := strings.Index(rest, "provided by "); i >= 0 {
				rt.Provider = strings.Trim(strings.TrimSpace(rest[i+len("provided by "):]), "()")
				rt.Reachable = true
			}
		case strings.HasPrefix(line, "cli:"):
			rt.CLI = strings.TrimSpace(strings.TrimPrefix(line, "cli:"))
		case strings.HasPrefix(line, "compose:"):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "compose:"))
			rt.Compose = rest
			if i := strings.Index(rest, "["); i >= 0 {
				rt.Flavor = strings.Trim(rest[i:], "[] ")
				rt.Compose = strings.TrimSpace(rest[:i])
			}
		case strings.HasPrefix(line, "engine:"):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "engine:"))
			if strings.HasPrefix(rest, "reachable") {
				rt.Reachable = true
				if i := strings.Index(rest, "provided by "); i >= 0 {
					rt.Provider = strings.Trim(strings.TrimSpace(rest[i+len("provided by "):]), "()")
				}
			}
		case strings.HasPrefix(line, "rootless:"):
			rt.Rootless = strings.Contains(strings.ToLower(line), "yes")
		}
	}
	return rt
}

func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t("); i >= 0 {
		return s[:i]
	}
	return s
}

// WriteText prints a human report.
func WriteText(w io.Writer, r Report) error {
	rt, engine, rootless := Runtime{}, "not measured", "-"
	if r.Runtime != nil {
		rt = *r.Runtime
		engine = map[bool]string{true: "reachable", false: "NOT reachable"}[rt.Reachable]
		rootless = map[bool]string{true: "yes", false: "no"}[rt.Rootless]
	}
	_, err := fmt.Fprintf(w, `dbpantry doctor
  generated:  %s
  version:    %s
  commit:     %s
  go:         %s (%s/%s, %d CPUs)
  hostname:   %s
  root:       %s
  home:       %s
  backend:    %s
  runtime:    %s
  provider:   %s
  cli:        %s
  compose:    %s [%s]
  engine:     %s
  rootless:   %s
`,
		r.Generated.Format(time.RFC3339),
		orDash(r.Version), orDash(r.Commit),
		r.GoVersion, r.GOOS, r.GOARCH, r.NumCPU,
		orDash(r.Hostname), orDash(r.Root), orDash(r.Home),
		orDash(r.Backend),
		orDash(rt.Name), orDash(rt.Provider),
		orDash(rt.CLI), orDash(rt.Compose), orDash(rt.Flavor),
		engine, rootless,
	)
	if err != nil {
		return err
	}
	for _, n := range r.Notes {
		if _, err := fmt.Fprintf(w, "  note:       %s\n", n); err != nil {
			return err
		}
	}
	if rt.Raw != "" {
		if _, err := fmt.Fprintf(w, "\n--- scripts/runtime info ---\n%s\n", rt.Raw); err != nil {
			return err
		}
	}
	return nil
}

// WriteJSON prints the report as JSON.
func WriteJSON(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
