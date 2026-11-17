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
	check := sc.add("Check configuration")
	render := sc.add("Render config files")
	gen := sc.add("Generate engine services")
	stack := sc.add("Start the stack")
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
	sc.stop()

	if backend != "compose" {
		// The backend's own notice says how to reach it (kubectl
		// port-forward, an Incus address); the table below describes Compose.
		return a.runSteps(c, script("scripts/exposure-notice"))
	}
	a.printLines(connectTable(a.style(), doc, a.width()-2))
	a.printLines(nextSteps(a.style(), doc))
	return nil
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
		parts = append(parts, fmt.Sprintf("%d %s", len(g.Children), strings.ToLower(g.Label)))
	}
	return strings.Join(parts, " · ")
}

// connectTable is where each running engine listens, from a container on the
// stack's network and from this machine.
func connectTable(st ui.Style, doc endpoint.Document, width int) []string {
	t := ui.Table{Title: "Connect", Headers: []string{"Engine", "From containers", "From this machine", "Pooled", "TLS"}}
	for _, e := range doc.Endpoints {
		if !e.Enabled {
			continue
		}
		host := st.Dim("not published")
		if p := e.Direct.Published; p != nil {
			host = st.Green(fmt.Sprintf("%s:%d", p.Address, p.Port))
		}
		pooled := st.Dim("–")
		if e.Pooled != nil {
			pooled = fmt.Sprintf("%s:%d", e.Pooled.Network.Host, e.Pooled.Network.Port)
		}
		tls := st.Dim("prod only")
		if e.TLSAlways {
			tls = st.Green("always")
		}
		t.Rows = append(t.Rows, []string{
			st.Bold(sanitize(e.Title)),
			fmt.Sprintf("%s:%d", sanitize(e.Direct.Network.Host), e.Direct.Network.Port),
			host, sanitize(pooled), tls,
		})
	}
	if len(t.Rows) == 0 {
		return nil
	}
	return append(st.Table(t, width), "")
}

// nextSteps are the commands someone usually wants after up.
func nextSteps(st ui.Style, doc endpoint.Document) []string {
	lines := []string{st.Bold("Next")}
	item := func(what, cmd string) {
		lines = append(lines, "  "+st.Dim("›")+" "+ui.Pad(what, 22)+st.Cyan(cmd))
	}
	item("Provision a project", "make new-project NAME=myapp ENGINE=<engine>")
	if doc.Publish == "none" {
		item("Reach from a GUI client", "set MDB_PUBLISH=direct in "+envFileName(doc.EnvFile)+", then make up")
	}
	item("Open the dashboard", "make dashboard")
	item("Open a shell", "make help  (one shell target per engine)")
	item("Every endpoint", "dbpantry endpoints -o json")
	return append(lines, "")
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
	stack := sc.add("Stop the stack")
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
