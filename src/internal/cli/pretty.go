package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/simtabi/dbpantry/src/internal/ui"
)

// Formatted output: a tree of steps with spinners and progress bars, then
// tables, drawn when standard output is a terminal, or when --pretty forces
// it (dashboard jobs, `less -R`). Anywhere else and under --plain, a command
// prints what its scripts print, unchanged, so nothing that reads that
// output breaks. Off a terminal, --pretty streams one-line progress with
// percentage and ETA, then prints the final tree once.

// formatted reports whether this run draws formatted output.
func (a *app) formatted() bool {
	if a.plain || a.dryRun {
		return false
	}
	if a.pretty {
		return true
	}
	if a.env("TERM") == "dumb" {
		return false
	}
	return a.isTTY()
}

// isTTY reports whether standard output is a terminal.
func (a *app) isTTY() bool {
	f, ok := a.stdout.(*os.File)
	return ok && term.IsTerminal(int(f.Fd())) // #nosec G115 -- a file descriptor fits in an int
}

// style is how formatted output is painted: colour unless --no-color,
// NO_COLOR or a dumb terminal says otherwise.
func (a *app) style() ui.Style {
	return ui.Style{Color: !a.noColor && a.env("NO_COLOR") == "" && a.env("TERM") != "dumb"}
}

// width is the terminal's width, or 100 when it cannot be read.
func (a *app) width() int {
	if f, ok := a.stdout.(*os.File); ok {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 20 { // #nosec G115 -- a file descriptor fits in an int
			return w
		}
	}
	return 100
}

// screen is one formatted command: a title line, then a tree of steps that
// is redrawn while they run.
type screen struct {
	a     *app
	st    ui.Style
	title string
	facts []string
	steps []*ui.Node
	live  *ui.Live
	// mu guards the nodes: steps change them while frames draw them.
	mu sync.Mutex
	// refresh, when set, updates nodes from a running step before a frame.
	refresh func()
}

func (a *app) newScreen(title string, facts ...string) *screen {
	s := &screen{a: a, st: a.style(), title: title, facts: facts}
	s.live = ui.NewLive(a.stdout, a.isTTY(), s.draw)
	s.live.SetSummary(s.summary)
	return s
}

func (s *screen) draw(frame int, now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refresh != nil {
		s.refresh()
	}
	w := s.a.width()
	head := s.st.Bold(s.st.Cyan("dbpantry")) + s.st.Dim(" › ") + s.st.Bold(s.title)
	if len(s.facts) > 0 {
		head += "  " + s.st.Dim(strings.Join(s.facts, " · "))
	}
	// Flush-left: no gutter indent and no blank line under the title — those
	// read as artificial margin beside the tree.
	lines := []string{ui.Truncate(head, w)}
	lines = append(lines, s.st.Tree(s.steps, w, frame, now)...)
	return lines
}

// summary is the one-line progress a non-TTY stream gets while work runs.
// Off a terminal the spinner does not animate (a fixed mark): Live already
// prints a new line whenever the summary changes, and an animated glyph
// would spam the dashboard job drawer with one row per frame.
func (s *screen) summary(frame int, now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refresh != nil {
		s.refresh()
	}
	for _, n := range s.steps {
		if n.State != ui.Running {
			continue
		}
		mark := "…"
		if s.live != nil && s.live.Terminal() {
			mark = s.st.Icon(n.State, frame)
		}
		line := mark + " " + n.Label
		if n.Progress != nil {
			line += "  " + s.st.Progress(*n.Progress, 20, now)
		} else if !n.Started.IsZero() {
			// Whole seconds only: sub-second Duration strings change every
			// tick and would force a new stream line for the same step.
			elapsed := now.Sub(n.Started).Truncate(time.Second)
			if elapsed >= time.Second {
				line += "  " + s.st.Dim(ui.Duration(elapsed))
			}
		}
		return line
	}
	return ""
}

func (s *screen) start() {
	// Off a terminal the title appears only in Stop's final tree. Live
	// streams the running summary, nested tree lines, and (via run) each
	// finished step so the job drawer can flip spinners to ✔ without waiting
	// for Stop — the drawer coalesces by step label, so the final tree does
	// not double the rows.
	s.live.Start()
}

// stop draws the final frame and leaves the cursor on the next line (no
// extra blank — callers add one blank only when another section follows).
func (s *screen) stop() {
	s.live.Stop()
}

func (s *screen) add(label string) *ui.Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := &ui.Node{Label: label, State: ui.Pending}
	s.steps = append(s.steps, n)
	return n
}

// update changes nodes while frames are drawn.
func (s *screen) update(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

// watch makes refresh run before every frame until the returned func is called.
func (s *screen) watch(refresh func()) func() {
	s.mu.Lock()
	s.refresh = refresh
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.refresh()
		s.refresh = nil
		s.mu.Unlock()
	}
}

// run runs one step under node: running while it runs, then done or failed.
// Its output goes to onLine, line by line, and is kept; on failure it is
// printed in full below the tree, since that is what says why. Without
// onLine the last three lines are shown under the node while it runs.
func (s *screen) run(ctx context.Context, root string, env []string, node *ui.Node, st step, onLine func(string)) (string, error) {
	s.mu.Lock()
	node.Start(time.Now())
	s.mu.Unlock()
	var all bytes.Buffer
	var mu sync.Mutex
	w := &lineWriter{fn: func(line string) {
		mu.Lock()
		all.WriteString(line + "\n")
		mu.Unlock()
		if onLine != nil {
			onLine(line)
			return
		}
		s.mu.Lock()
		node.Log = append(node.Log, sanitize(line))
		if len(node.Log) > 3 {
			node.Log = node.Log[len(node.Log)-3:]
		}
		s.mu.Unlock()
	}}
	err := s.a.runStepTo(ctx, root, env, st, w, w)
	w.flush()
	s.mu.Lock()
	node.Log = nil
	ended := time.Now()
	label, started := node.Label, node.Started
	if err != nil {
		node.Finish(ui.Failed, "", ended)
	} else {
		node.Finish(ui.Done, "", ended)
	}
	s.mu.Unlock()
	// Off a terminal, print the finished step so the drawer flips the
	// spinner to ✔ immediately. Stop still prints the full tree once; the
	// drawer coalesces by label. Nested under-the-hood rows stream from Live.
	if !s.live.Terminal() {
		icon := s.st.Icon(ui.Done, 0)
		if err != nil {
			icon = s.st.Icon(ui.Failed, 0)
		}
		detail := ""
		if !started.IsZero() {
			detail = "  " + s.st.Dim(ui.Duration(ended.Sub(started)))
		}
		s.live.Print(icon + " " + label + detail)
	}
	if err != nil {
		return all.String(), err
	}
	return all.String(), nil
}

// fail stops the screen and prints what a failed step said.
func (s *screen) fail(what, output string, err error) error {
	s.stop()
	if out := strings.TrimRight(output, "\n"); out != "" {
		_, _ = fmt.Fprintln(s.a.stdout)
		_, _ = fmt.Fprintln(s.a.stdout, s.st.Rule(what+" said", s.a.width()))
		for _, l := range strings.Split(out, "\n") {
			_, _ = fmt.Fprintln(s.a.stdout, sanitize(l))
		}
	}
	return err
}

// lineWriter calls fn once per complete line written to it.
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	fn  func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := string(bytes.TrimRight(w.buf[:i], "\r"))
		w.buf = w.buf[i+1:]
		w.fn(line)
	}
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.fn(string(w.buf))
		w.buf = nil
	}
}

var _ io.Writer = (*lineWriter)(nil)

// printLines writes lines to standard output, flush-left.
func (a *app) printLines(lines []string) {
	for _, l := range lines {
		_, _ = fmt.Fprintln(a.stdout, l)
	}
}
