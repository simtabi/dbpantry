package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/endpoint"
	"github.com/simtabi/dbpantry/src/internal/stackview"
	"github.com/simtabi/dbpantry/src/internal/ui"
)

// upFormatted is `up` on a terminal: the same scripts as the plain path, in
// the same order, drawn as a tree, with Compose's progress read as JSON
// (COMPOSE_PROGRESS=json) and the connection details as a table.
func (a *app) upFormatted(c *cobra.Command) error {
	root, err := a.root()
	if err != nil {
		return err
	}
	env, err := a.scriptEnv(c.Context(), root)
	if err != nil {
		return err
	}
	m, _, err := a.manifest()
	if err != nil {
		return err
	}
	s, err := a.settings(root)
	if err != nil {
		return err
	}
	profiles := envValue(env, "COMPOSE_PROFILES")
	doc, err := endpoint.Resolve(m, s, endpoint.Options{Profiles: profiles})
	if err != nil {
		return err
	}
	backend := s.Effective("MDB_BACKEND")
	if backend == "" {
		backend = "compose"
	}

	sc := a.newScreen("up", "profiles "+profiles, "publish "+doc.Publish, "backend "+backend)
	sc.project = composeProject(env, s.Effective("COMPOSE_PROJECT_NAME"))
	check := sc.add("Check configuration", "🔍")
	render := sc.add("Render config files", "📝")
	gen := sc.add("Generate engine services", "🧩")
	certs := sc.add("Check certificates", "🔐")
	stack := sc.add("Start the stack", "🚀")
	sc.start()

	ctx := c.Context()
	if out, err := sc.run(ctx, root, env, check, script("scripts/check-env", "--quiet"), nil); err != nil {
		return sc.fail("scripts/check-env", out, err)
	}
	out, err := sc.run(ctx, root, env, render, script("scripts/render-config"), nil)
	if err != nil {
		return sc.fail("scripts/render-config", out, err)
	}
	sc.update(func() { renderedFiles(render, out) })
	if out, err := sc.run(ctx, root, env, gen, step{Args: []string{"scripts/gen-compose"}, Quiet: true}, nil); err != nil {
		return sc.fail("scripts/gen-compose", out, err)
	}
	if out, err := sc.run(ctx, root, env, certs, script("scripts/certs", "--heal"), nil); err != nil {
		return sc.fail("scripts/certs --heal", out, err)
	}

	view := stackview.New()
	started := time.Now()
	stop := sc.watch(func() {
		stack.Children = view.Nodes()
		if p := view.Overall(started); p.Total > 0 && stack.State == ui.Running {
			stack.Progress = &p
		} else {
			stack.Progress = nil
		}
		if log := view.Log(); len(log) > 0 && stack.State == ui.Running {
			stack.Log = tail(log, 3)
		}
	})
	up := step{Args: []string{"scripts/runtime", "up"}, Env: []string{"COMPOSE_PROGRESS=json"}}
	_, err = sc.run(ctx, root, env, stack, up, view.Feed)
	stop()
	if err != nil {
		return sc.fail("scripts/runtime up", strings.Join(view.Log(), "\n"), err)
	}
	sc.update(func() { stack.Detail = upSummary(view.Nodes()) })
	if backend == "compose" {
		sc.setTail(connectRows(doc), nextItems(doc))
	}
	sc.stop()

	if backend != "compose" {
		// The backend's own notice says how to reach it (kubectl
		// port-forward, an Incus address); the table below describes Compose.
		return a.runSteps(c, script("scripts/exposure-notice"))
	}
	st := sc.st
	_, _ = fmt.Fprintln(a.stdout)
	a.printLines(connectTable(st, doc, a.width()))
	if next := nextSteps(st, doc); len(next) > 0 {
		_, _ = fmt.Fprintln(a.stdout)
		a.printLines(next)
	}
	return nil
}

// composeProject is the Compose project as scripts/runtime-lib.sh resolves
// it: the environment, then the env file, then "mdb".
func composeProject(env []string, fromFile string) string {
	if p := envValue(env, "COMPOSE_PROJECT_NAME"); p != "" {
		return p
	}
	if fromFile != "" {
		return fromFile
	}
	return "mdb"
}

// renderedFiles turns render-config's "rendered A -> B" lines into children.
func renderedFiles(n *ui.Node, out string) {
	for _, l := range strings.Split(out, "\n") {
		if _, to, ok := strings.Cut(strings.TrimPrefix(l, "rendered "), " -> "); ok && strings.HasPrefix(l, "rendered ") {
			n.Add(to, ui.Done)
		}
	}
	if len(n.Children) > 0 {
		n.Detail = strconv.Itoa(len(n.Children)) + " files"
	}
}

// upSummary counts what the run ended with: "2 images built · 2 containers".
func upSummary(groups []*ui.Node) string {
	var parts []string
	for _, g := range groups {
		kind := strings.ToLower(g.Label)
		if len(g.Children) == 1 {
			kind = strings.TrimSuffix(kind, "s")
		}
		parts = append(parts, fmt.Sprintf("%d %s", len(g.Children), kind))
	}
	return strings.Join(parts, " · ")
}

// connectRow is where one running engine listens. Host is empty when the
// engine is not published to this machine; Pooled is empty with no pooler,
// and PoolerOff says the pooler profile is not running.
type connectRow struct {
	Engine    string `json:"engine"`
	Network   string `json:"network"`
	Host      string `json:"host,omitempty"`
	Pooled    string `json:"pooled,omitempty"`
	PoolerOff bool   `json:"pooler_off,omitempty"`
	TLSAlways bool   `json:"tls_always,omitempty"`
}

func connectRows(doc endpoint.Document) []connectRow {
	var rows []connectRow
	for _, e := range doc.Endpoints {
		if !e.Enabled {
			continue
		}
		r := connectRow{
			Engine:    sanitize(e.Title),
			Network:   fmt.Sprintf("%s:%d", sanitize(e.Direct.Network.Host), e.Direct.Network.Port),
			TLSAlways: e.TLSAlways,
		}
		if p := e.Direct.Published; p != nil {
			r.Host = fmt.Sprintf("%s:%d", p.Address, p.Port)
		}
		if e.Pooled != nil {
			r.Pooled = sanitize(fmt.Sprintf("%s:%d", e.Pooled.Network.Host, e.Pooled.Network.Port))
			if !e.Pooled.Enabled {
				// The pooler profile is not running; say so rather than
				// list an address nothing answers on.
				r.Pooled, r.PoolerOff = sanitize(e.Pooled.Network.Host), true
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// connectTable is where each running engine listens, from a container on the
// stack's network and from this machine.
func connectTable(st ui.Style, doc endpoint.Document, width int) []string {
	title := "Connect"
	if st.Emoji {
		title = "🔌 " + title
	}
	t := ui.Table{Title: title, Headers: []string{"Engine", "From containers", "From this machine", "Pooled", "TLS"}}
	for _, r := range connectRows(doc) {
		host := st.Dim("not published")
		if r.Host != "" {
			host = st.Green(r.Host)
		}
		pooled := st.Dim("–")
		switch {
		case r.PoolerOff:
			pooled = st.Dim(r.Pooled + " · off")
		case r.Pooled != "":
			pooled = r.Pooled
		}
		tls := st.Dim("prod only")
		if r.TLSAlways {
			tls = st.Green("always")
		}
		t.Rows = append(t.Rows, []string{st.Bold(r.Engine), r.Network, host, pooled, tls})
	}
	if len(t.Rows) == 0 {
		return nil
	}
	return st.Table(t, width)
}

// nextItem is one command someone usually wants after up.
type nextItem struct {
	Label string `json:"label"`
	Cmd   string `json:"cmd"`
	Note  string `json:"note,omitempty"`
}

func nextItems(doc endpoint.Document) []nextItem {
	items := []nextItem{{Label: "Provision a project", Cmd: "make new-project NAME=myapp ENGINE=<engine>"}}
	if doc.Publish == "none" {
		items = append(items, nextItem{Label: "Reach from a GUI client", Cmd: "set MDB_PUBLISH=direct in " + envFileName(doc.EnvFile) + ", then make up"})
	}
	return append(items,
		nextItem{Label: "Open the dashboard", Cmd: "make dashboard"},
		nextItem{Label: "Open a shell", Cmd: "make help", Note: "one shell target per engine"},
		nextItem{Label: "Every endpoint", Cmd: "dbpantry endpoints -o json"},
	)
}

// nextSteps are the commands someone usually wants after up.
func nextSteps(st ui.Style, doc endpoint.Document) []string {
	title := "Next"
	if st.Emoji {
		title = "👉 " + title
	}
	lines := []string{st.Bold(title)}
	for _, it := range nextItems(doc) {
		line := st.Dim("›") + " " + ui.Pad(it.Label, 24) + st.Cyan(it.Cmd)
		if it.Note != "" {
			line += st.Dim("  — " + it.Note)
		}
		lines = append(lines, line)
	}
	return lines
}

func envFileName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	if path == "" {
		return ".env"
	}
	return path
}

func envValue(env []string, key string) string {
	v := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			v = strings.TrimPrefix(kv, key+"=")
		}
	}
	return v
}

func tail(lines []string, n int) []string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = sanitize(l)
	}
	return out
}

// downFormatted is `down` on a terminal: what Compose stops and removes,
// grouped by kind. Volumes are never among them (destroy removes those).
func (a *app) downFormatted(c *cobra.Command) error {
	root, err := a.root()
	if err != nil {
		return err
	}
	env, err := a.scriptEnv(c.Context(), root)
	if err != nil {
		return err
	}
	sc := a.newScreen("down", "data volumes are kept")
	stack := sc.add("Stop the stack", "🛑")
	sc.start()
	view := stackview.New()
	started := time.Now()
	stop := sc.watch(func() {
		stack.Children = view.Nodes()
		if p := view.Overall(started); p.Total > 0 && stack.State == ui.Running {
			stack.Progress = &p
		} else {
			stack.Progress = nil
		}
	})
	down := step{Args: []string{"scripts/runtime", "down"}, Env: []string{"COMPOSE_PROGRESS=json"}}
	_, err = sc.run(c.Context(), root, env, stack, down, view.Feed)
	stop()
	if err != nil {
		return sc.fail("scripts/runtime down", strings.Join(view.Log(), "\n"), err)
	}
	sc.update(func() {
		if len(stack.Children) == 0 {
			stack.Detail = "nothing was running"
		} else {
			stack.Detail = upSummary(stack.Children) + " removed"
		}
	})
	sc.stop()
	return nil
}
