package cli

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/project"
	"github.com/simtabi/dbpantry/src/internal/ui"
)

// projectFormatted is `project new` on a terminal, with stepEnv added to the
// script's environment (the fixture's, under --fixture): the script's JSON drawn as
// a tree of what was done, a table of the project, its warnings, and the
// paste-ready block between rules, unindented so it copies cleanly.
func (a *app) projectFormatted(c *cobra.Command, argv, stepEnv []string) (*project.Provisioned, error) {
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
	err = a.runStepTo(c.Context(), root, env, step{Args: append(argv, "--format", "json"), Env: stepEnv}, &stdout, stderr)
	stderr.flush()
	var p project.Provisioned
	if err == nil {
		p, err = project.Parse(stdout.Bytes())
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
		roleWidth := max(ui.Width(sanitize(p.Owner)), ui.Width(sanitize(p.Readonly)))
		roles.Add(ui.Pad(sanitize(p.Owner), roleWidth), ui.Done).Detail = "owner"
		roles.Add(ui.Pad(sanitize(p.Readonly), roleWidth), ui.Done).Detail = "read-only"
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
	_, _ = fmt.Fprintln(a.stdout)
	a.printLines(projectTable(st, p, a.width()))
	if len(warnings) > 0 {
		_, _ = fmt.Fprintln(a.stdout)
		lines := []string{st.Yellow(st.Bold(plural(len(warnings), "⚠ 1 warning", "⚠ %d warnings")))}
		for _, w := range warnings {
			lines = append(lines, st.Dim("›")+" "+sanitize(w))
		}
		a.printLines(lines)
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

func projectTable(st ui.Style, p project.Provisioned, width int) []string {
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf(many, n)
}
