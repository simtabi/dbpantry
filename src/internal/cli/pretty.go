package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
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

// screenStyle is style() plus what only a terminal gets: step icons, one
// right-aligned duration column, and finished steps folded to their summary
// (folding applies off a terminal too, so the final tree in a log is short).
// DBPANTRY_NO_EMOJI turns the icons off.
func (a *app) screenStyle() ui.Style {
	st := a.style()
	tty := a.isTTY() && a.env("TERM") != "dumb"
	st.Emoji = tty && a.env("DBPANTRY_NO_EMOJI") == ""
	st.AlignTimes = tty
	st.FoldDone = true
	return st
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
	// project is the Compose project, to name a failed container's service.
	project  string
	finished bool
	failure  *runFailure
	connect  []connectRow
	next     []nextItem
}

func (a *app) newScreen(title string, facts ...string) *screen {
	s := &screen{a: a, st: a.screenStyle(), title: title, facts: facts}
	s.live = ui.NewLive(a.stdout, a.isTTY(), s.draw)
	s.live.SetSummary(s.summary)
	if a.events && !a.isTTY() {
		s.live.SetEvents(s.event)
	}
	return s
}

// runEventPrefix starts a run snapshot line in --events output. The job
// drawer renders the latest one and keeps every other line as the raw log.
const runEventPrefix = "::dbpantry-run:: "

// runSnapshot is the whole run as data: the steps and their resources, how
// it ended, and what to do next.
type runSnapshot struct {
	V       int           `json:"v"`
	Title   string        `json:"title"`
	Facts   [][2]string   `json:"facts,omitempty"`
	State   string        `json:"state"`
	Steps   []ui.SnapNode `json:"steps"`
	Failure *runFailure   `json:"failure,omitempty"`
	Connect []connectRow  `json:"connect,omitempty"`
	Next    []nextItem    `json:"next,omitempty"`
}

// runFailure is why a run stopped, cause first.
type runFailure struct {
	What     string   `json:"what"`
	Step     string   `json:"step,omitempty"`
	Index    int      `json:"index,omitempty"`
	Headline string   `json:"headline"`
	Lines    []string `json:"lines,omitempty"`
	Exit     int      `json:"exit,omitempty"`
	Service  string   `json:"service,omitempty"`
}

// event is the run snapshot line, as the steps stand now.
func (s *screen) event(now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := "running"
	switch {
	case s.failure != nil:
		state = "failed"
	case s.finished:
		state = "done"
	}
	b, err := json.Marshal(runSnapshot{
		V: 1, Title: s.title, Facts: splitFacts(s.facts), State: state,
		Steps: ui.Snapshot(s.steps, now), Failure: s.failure, Connect: s.connect, Next: s.next,
	})
	if err != nil {
		return ""
	}
	return runEventPrefix + string(b)
}

// splitFacts reads "profiles pg,mysql" as a named fact; anything else
// ("data volumes are kept", a project name) is a note with no name.
func splitFacts(facts []string) [][2]string {
	var out [][2]string
	for _, f := range facts {
		k, v, ok := strings.Cut(f, " ")
		switch k {
		case "profiles", "publish", "backend":
			if ok {
				out = append(out, [2]string{k, v})
				continue
			}
		}
		out = append(out, [2]string{"", f})
	}
	return out
}

func (s *screen) draw(frame int, now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refresh != nil {
		s.refresh()
	}
	w := s.a.width()
	head := s.st.Bold(s.st.Cyan("dbpantry")) + s.st.Dim(" › ") + s.st.Bold(s.title)
	if s.st.Emoji {
		head = "⚡ " + head
	}
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
	s.mu.Lock()
	s.finished = true
	s.mu.Unlock()
	s.live.Stop()
}

// add appends a step, with the icon a terminal draws before it.
func (s *screen) add(label string, icon ...string) *ui.Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := &ui.Node{Label: label, State: ui.Pending}
	if len(icon) > 0 {
		n.Icon = icon[0]
	}
	s.steps = append(s.steps, n)
	return n
}

// setTail records the Connect rows and next steps the final snapshot carries.
func (s *screen) setTail(connect []connectRow, next []nextItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connect, s.next = connect, next
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
		// Stop unfinished compose children so the final tree (and drawer)
		// cannot keep spinning under a failed step.
		node.AbandonRunning(ended)
		node.Progress = nil
	} else {
		node.Finish(ui.Done, "", ended)
		node.Progress = nil
	}
	s.mu.Unlock()
	// Off a terminal, print the finished step so the drawer flips the
	// spinner to ✔/✖ immediately. Stop still prints the full tree once; the
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

// fail stops the screen and prints why, cause first: the headline (a
// script's {"error":true,"message":…} read out), then everything the failed
// step said, its exit status, and where to look next.
func (s *screen) fail(what, output string, err error) error {
	var lines []string
	if out := strings.TrimRight(output, "\n"); out != "" {
		for _, l := range strings.Split(out, "\n") {
			lines = append(lines, sanitize(l))
		}
	}
	f := &runFailure{What: what, Headline: errorHeadline(lines, err), Lines: lines, Exit: exitCodeOf(err)}
	s.mu.Lock()
	for i, n := range s.steps {
		if n.State == ui.Failed {
			f.Step, f.Index = n.Label, i+1
			break
		}
	}
	f.Service = failedService(s.steps, s.project)
	s.failure = f
	s.mu.Unlock()
	s.stop()

	st, out := s.st, s.a.stdout
	_, _ = fmt.Fprintln(out)
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.TrimSpace(l) == f.Headline }) {
		// A read-out message leads; a plain last line is not printed twice.
		_, _ = fmt.Fprintln(out, st.Bold(st.Red("✖ "+f.Headline)))
	}
	if len(lines) > 0 {
		title := what + " said"
		if st.Emoji {
			title = "💥 " + title
		}
		_, _ = fmt.Fprintln(out, st.Rule(title, s.a.width()))
		for _, l := range lines {
			if errorLine(l) {
				l = st.Red(l)
			}
			_, _ = fmt.Fprintln(out, l)
		}
	}
	if f.Exit > 0 {
		_, _ = fmt.Fprintln(out, st.Dim(fmt.Sprintf("exit %d", f.Exit)))
	}
	if f.Service != "" {
		_, _ = fmt.Fprintln(out, st.Dim("›")+" "+ui.Pad("Read its log", 22)+st.Cyan("dbpantry logs "+f.Service))
	}
	return err
}

// errorHeadline is the one line that says what went wrong: the message of
// the last {"error":…} object a script printed, else its last line, else
// the error itself.
func errorHeadline(lines []string, err error) string {
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(l, "{") {
			continue
		}
		var m struct {
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(l), &m) == nil && m.Message != "" {
			return sanitize(m.Message)
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	if err != nil {
		return sanitize(err.Error())
	}
	return "failed"
}

func errorLine(l string) bool {
	t := strings.TrimSpace(l)
	return strings.HasPrefix(t, `{"error"`) || strings.HasPrefix(strings.ToLower(t), "error")
}

// exitCodeOf is the failed command's exit status, 1 when it has none.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) && ec.ExitCode() > 0 {
		return ec.ExitCode()
	}
	return 1
}

var replicaSuffix = regexp.MustCompile(`-\d+$`)

// failedService names the Compose service of the first failed container
// (<project>-<service>-<n>), so the failure can point at its log.
func failedService(steps []*ui.Node, project string) string {
	var walk func(nodes []*ui.Node, parent string) string
	walk = func(nodes []*ui.Node, parent string) string {
		for _, n := range nodes {
			if len(n.Children) > 0 {
				if svc := walk(n.Children, n.Label); svc != "" {
					return svc
				}
				continue
			}
			if n.State != ui.Failed || parent != "Containers" {
				continue
			}
			name := replicaSuffix.ReplaceAllString(strings.TrimSpace(n.Label), "")
			if project != "" {
				name = strings.TrimPrefix(name, project+"-")
			}
			return name
		}
		return ""
	}
	return walk(steps, "")
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
