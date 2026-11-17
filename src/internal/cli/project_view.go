package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/ui"
)

// provisioned is what `scripts/new-project --format json` prints.
type provisioned struct {
	Engine     string   `json:"engine"`
	Title      string   `json:"title"`
	Database   string   `json:"database"`
	Owner      string   `json:"owner"`
	Readonly   string   `json:"readonly"`
	Extensions []string `json:"extensions"`
	Secrets    []struct {
		Role   string `json:"role"`
		File   string `json:"file"`
		Action string `json:"action"`
	} `json:"secrets"`
	TLSAlways bool `json:"tls_always"`
	Pooler    *struct {
		Host string `json:"host"`
		Port string `json:"port"`
	} `json:"pooler"`
	Network string `json:"network"`
	Block   string `json:"block"`
}

// projectFormatted is `project new` on a terminal: the script's JSON drawn as
// a tree of what was done, a table of the project, its warnings, and the
// paste-ready block between rules, unindented so it copies cleanly.
func (a *app) projectFormatted(c *cobra.Command, argv []string) (*provisioned, error) {
	root, err := a.root()
	if err != nil {
		return nil, err
	}
	env, err := a.scriptEnv(c.Context(), root)
	if err != nil {
		return nil, err
	}
	name := ""
	for i, v := range argv {
		if v == "--name" && i+1 < len(argv) {
			name = argv[i+1]
		}
	}
	sc := a.newScreen("project new", name)
	node := sc.add("Provision " + name)
	sc.start()

	var stdout bytes.Buffer
	var logs []string
	stderr := &lineWriter{fn: func(l string) {
		sc.update(func() {
			logs = append(logs, l)
			node.Log = tail(logs, 3)
		})
	}}
	sc.update(func() { node.Start(time.Now()) })
	err = a.runStepTo(c.Context(), root, env, script(append(argv, "--format", "json")...), &stdout, stderr)
	stderr.flush()
	var p provisioned
	if err == nil {
		err = json.Unmarshal(lastLine(stdout.Bytes()), &p)
	}
	if err != nil {
		sc.update(func() { node.Log = nil; node.Finish(ui.Failed, "", time.Now()) })
		return nil, sc.fail("scripts/new-project", strings.Join(logs, "\n"), err)
	}

	warnings := warningsOf(logs)
	sc.update(func() {
		node.Log = nil
		node.Label = fmt.Sprintf("Provisioned %s on %s", sanitize(p.Database), sanitize(p.Title))
		creds := node.Add("Credentials", ui.Done)
		for _, s := range p.Secrets {
			creds.Add(ui.Pad(sanitize(s.Role), 26)+sanitize(s.File), ui.Done).Detail = sanitize(s.Action)
		}
		roles := node.Add("Roles", ui.Done)
		roles.Add(sanitize(p.Owner), ui.Done).Detail = "owner"
		roles.Add(sanitize(p.Readonly), ui.Done).Detail = "read-only"
		if len(p.Extensions) > 0 {
			ext := node.Add("Extensions", ui.Done)
			for _, e := range p.Extensions {
				st, d := ui.Done, "created"
				if failedExtension(warnings, e) {
					st, d = ui.Warning, "not created"
				}
				ext.Add(sanitize(e), st).Detail = d
				if st == ui.Warning {
					ext.State = ui.Warning
				}
			}
		}
		state := ui.Done
		if len(warnings) > 0 {
			state = ui.Warning
		}
		node.Finish(state, "", time.Now())
	})
	sc.stop()

	st := a.style()
	a.printLines(projectTable(st, p, a.width()-2))
	if len(warnings) > 0 {
		lines := []string{st.Yellow(st.Bold(plural(len(warnings), "⚠ 1 warning", "⚠ %d warnings")))}
		for _, w := range warnings {
			lines = append(lines, "  "+st.Dim("›")+" "+sanitize(w))
		}
		a.printLines(append(lines, ""))
	}
	// The block is printed flush left, so it pastes into an .env unchanged.
	w := a.width()
	_, _ = fmt.Fprintln(a.stdout, st.Rule("Paste into your app's .env", w))
	_, _ = fmt.Fprint(a.stdout, sanitize(strings.TrimRight(p.Block, "\n"))+"\n")
	_, _ = fmt.Fprintln(a.stdout, st.Rule("Your app's compose file joins the network", w))
	_, _ = fmt.Fprintf(a.stdout, "networks:\n  default:\n    external: true\n    name: %s\n", sanitize(p.Network))
	_, _ = fmt.Fprintln(a.stdout, st.Rule("", w))
	_, _ = fmt.Fprintln(a.stdout)
	return &p, nil
}

func projectTable(st ui.Style, p provisioned, width int) []string {
	row := func(k, v string) []string { return []string{st.Dim(k), v} }
	t := ui.Table{Title: "Project " + sanitize(p.Database), Headers: []string{"Setting", "Value"}}
	t.Rows = append(t.Rows,
		row("Engine", sanitize(p.Title)+st.Dim("  ("+sanitize(p.Engine)+")")),
		row("Database", st.Bold(sanitize(p.Database))),
		row("Network", sanitize(p.Network)),
	)
	for _, s := range p.Secrets {
		t.Rows = append(t.Rows, row("Password of "+sanitize(s.Role), sanitize(s.File)))
	}
	tls := "in the prod profile (DB_SSL_CA)"
	if p.TLSAlways {
		tls = st.Green("always") + " (verify-full)"
	}
	t.Rows = append(t.Rows, row("TLS", tls))
	if p.Pooler != nil {
		t.Rows = append(t.Rows, row("Pooler", sanitize(p.Pooler.Host)+":"+sanitize(p.Pooler.Port)+st.Yellow("  required in production")))
	}
	return append(st.Table(t, width), "")
}

// warningsOf picks the warnings out of the script's log lines.
func warningsOf(logs []string) []string {
	var out []string
	for _, l := range logs {
		if i := strings.Index(l, "WARNING:"); i >= 0 {
			out = append(out, strings.TrimSpace(l[i+len("WARNING:"):]))
		}
	}
	return out
}

func failedExtension(warnings []string, ext string) bool {
	for _, w := range warnings {
		if strings.Contains(w, "extension "+ext+" ") {
			return true
		}
	}
	return false
}

// lastLine is the last non-empty line: the JSON object, after anything a
// hook printed to stdout despite the redirect.
func lastLine(b []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	return lines[len(lines)-1]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf(many, n)
}
