package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/simtabi/dbpantry/src/internal/endpoint"
	"github.com/simtabi/dbpantry/src/internal/ui"
)

func TestFormattedOnlyOnATerminalUnlessForced(t *testing.T) {
	a := &app{stdout: &bytes.Buffer{}, environ: func() []string { return nil }}
	if a.formatted() {
		t.Error("a buffer is not a terminal: the scripts' own output must pass through unchanged")
	}
	if a.isTTY() {
		t.Error("a buffer is not a TTY")
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

func TestJobArgvForcesPretty(t *testing.T) {
	got := strings.Join(jobArgv("/kit", "/home", []string{"up"}), " ")
	if !strings.Contains(got, "--pretty") || strings.Contains(got, "--plain") {
		t.Fatalf("dashboard jobs must use --pretty, not --plain: %s", got)
	}
	if !strings.Contains(got, "--no-input") || !strings.Contains(got, "--no-color") {
		t.Fatalf("jobs stay non-interactive and colourless: %s", got)
	}
}

func TestScreenSummaryNamesTheRunningStep(t *testing.T) {
	var buf bytes.Buffer
	a := &app{stdout: &buf, environ: func() []string { return nil }, pretty: true, noColor: true}
	sc := a.newScreen("up")
	n := sc.add("Start the stack")
	n.Start(time.Now().Add(-5 * time.Second))
	n.Progress = &ui.Progress{Done: 3, Total: 6, Started: time.Now().Add(-5 * time.Second)}
	sum := sc.summary(1, time.Now())
	if !strings.Contains(sum, "Start the stack") || !strings.Contains(sum, "%") {
		t.Fatalf("summary = %q", sum)
	}
}

func TestScreenDrawIsFlushLeft(t *testing.T) {
	var buf bytes.Buffer
	a := &app{stdout: &buf, environ: func() []string { return nil }, pretty: true, noColor: true}
	sc := a.newScreen("up", "profiles pg")
	n := sc.add("Check configuration")
	n.Finish(ui.Done, "", time.Now())
	child := n.Add("config/caddy/Caddyfile", ui.Done)
	_ = child
	lines := sc.draw(0, time.Now())
	if len(lines) < 2 {
		t.Fatalf("draw returned %d lines", len(lines))
	}
	if strings.HasPrefix(lines[0], " ") || lines[0] == "" {
		t.Errorf("title must be flush-left with no blank under it, got %q", lines[0])
	}
	if strings.HasPrefix(lines[1], " ") || lines[1] == "" {
		t.Errorf("first step must sit flush under the title (no gutter/blank), got %q", lines[1])
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "│") {
		t.Errorf("continuing tree bar must not appear:\n%s", joined)
	}
	if !strings.Contains(joined, "dbpantry") || !strings.Contains(joined, "└─") {
		t.Errorf("draw missing title/tree:\n%s", joined)
	}
}

// Off a terminal (--pretty for dashboard jobs) the title and finished steps
// must appear only in Stop's final tree — not once mid-flight and again at the end.
func TestScreenOffTTYPrintsTreeOnce(t *testing.T) {
	var buf bytes.Buffer
	a := &app{stdout: &buf, environ: func() []string { return nil }, pretty: true, noColor: true}
	sc := a.newScreen("down", "data volumes are kept")
	n := sc.add("Stop the stack")
	n.Finish(ui.Done, "", time.Now())
	sc.start()
	time.Sleep(50 * time.Millisecond)
	sc.stop()
	out := buf.String()
	if c := strings.Count(out, "dbpantry"); c != 1 {
		t.Fatalf("title must print once on Stop, got %d in %q", c, out)
	}
	if c := strings.Count(out, "Stop the stack"); c != 1 {
		t.Fatalf("finished step must print once with the final tree, got %d in %q", c, out)
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

func TestUpSummaryCountsInTheRightNumber(t *testing.T) {
	groups := []*ui.Node{
		{Label: "Networks", Children: []*ui.Node{{}}},
		{Label: "Containers", Children: []*ui.Node{{}, {}}},
	}
	if got := upSummary(groups); got != "1 network · 2 containers" {
		t.Errorf("upSummary = %q", got)
	}
}
