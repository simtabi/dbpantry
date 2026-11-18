package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/simtabi/dbpantry/src/internal/endpoint"
	"github.com/simtabi/dbpantry/src/internal/ui"
)

func TestFormattedOnlyOnATerminalUnlessForced(t *testing.T) {
	a := &app{stdout: &bytes.Buffer{}, environ: func() []string { return nil }}
	if a.formatted() {
		t.Error("a buffer is not a terminal: the scripts' own output must pass through unchanged")
	}
	a.pretty = true
	if !a.formatted() {
		t.Error("--pretty draws the formatted view anywhere")
	}
	a.dryRun = true
	if a.formatted() {
		t.Error("--dry-run prints the scripts it would run, never a view")
	}
	a.dryRun, a.pretty, a.plain = false, false, true
	if a.formatted() {
		t.Error("--plain is never formatted")
	}
}

func TestStyleHonoursNoColor(t *testing.T) {
	a := &app{environ: func() []string { return []string{"NO_COLOR=1"} }}
	if a.style().Color {
		t.Error("NO_COLOR turns colour off")
	}
	a = &app{environ: func() []string { return nil }, noColor: true}
	if a.style().Color {
		t.Error("--no-color turns colour off")
	}
}

func TestLineWriterSplitsLines(t *testing.T) {
	var got []string
	w := &lineWriter{fn: func(l string) { got = append(got, l) }}
	_, _ = w.Write([]byte("one\r\ntw"))
	_, _ = w.Write([]byte("o\nthree"))
	w.flush()
	if strings.Join(got, "|") != "one|two|three" {
		t.Errorf("lines: %q", got)
	}
}

func TestRenderedFilesBecomeChildren(t *testing.T) {
	n := &ui.Node{}
	renderedFiles(n, "rendered config/caddy/Caddyfile.tmpl -> config/caddy/Caddyfile\nnoise\nrendered a.tmpl -> a\n")
	if len(n.Children) != 2 || n.Children[0].Label != "config/caddy/Caddyfile" || n.Detail != "2 files" {
		t.Errorf("children %+v detail %q", n.Children, n.Detail)
	}
}

func TestWarningsMarkTheirExtension(t *testing.T) {
	logs := []string{
		"[new-project] generated secrets/pg_x_user_password.txt",
		"  WARNING: extension vector could not be created: no control file",
	}
	w := warningsOf(logs)
	if len(w) != 1 || !strings.HasPrefix(w[0], "extension vector") {
		t.Fatalf("warnings: %q", w)
	}
	if !failedExtension(w, "vector") || failedExtension(w, "vec") || failedExtension(w, "pg_stat_statements") {
		t.Error("only the named extension is marked")
	}
}

func TestConnectTableListsEnabledEnginesOnly(t *testing.T) {
	doc := endpoint.Document{Endpoints: []endpoint.Endpoint{
		{Title: "Alpha", Enabled: true, Direct: endpoint.Access{Network: endpoint.Address{Host: "alpha", Port: 1}, Published: &endpoint.Published{Address: "127.0.0.1", Port: 54001}}},
		{Title: "Beta", Enabled: false, Direct: endpoint.Access{Network: endpoint.Address{Host: "beta", Port: 2}}},
	}}
	got := strings.Join(connectTable(ui.Style{}, doc, 0), "\n")
	if !strings.Contains(got, "Alpha") || !strings.Contains(got, "127.0.0.1:54001") || strings.Contains(got, "Beta") {
		t.Errorf("the table names the running engines, with their published port:\n%s", got)
	}
	if lines := connectTable(ui.Style{}, endpoint.Document{}, 0); lines != nil {
		t.Error("no enabled engine, no table")
	}
}
