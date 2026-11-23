package ui

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Live redraws a region of the terminal while work runs: the spinner and
// progress frames. On a terminal it moves the cursor back over the previous
// frame. Off a terminal it prints a one-line summary whenever that summary
// changes (so a log or a dashboard job stream sees motion without escape
// sequences), streams nested tree / progress lines as they change, and Stop
// prints the final full frame once.
type Live struct {
	w        io.Writer
	terminal bool
	draw     func(frame int, now time.Time) []string
	// summary, when set, is the one-line progress printed off a terminal.
	// When nil, stream mode stays quiet until Stop (aside from tree lines).
	summary func(frame int, now time.Time) string
	// events, when set, is a machine-readable line printed off a terminal
	// whenever it changes, and once more at Stop (the run snapshot).
	events    func(now time.Time) string
	lastEvent string

	mu       sync.Mutex
	lines    int
	frame    int
	stop     chan struct{}
	done     chan struct{}
	lastTree map[string]string // off-TTY: last printed line per treeStreamKey
}

// NewLive returns a region drawn by draw. terminal says w is a terminal.
func NewLive(w io.Writer, terminal bool, draw func(frame int, now time.Time) []string) *Live {
	return &Live{w: w, terminal: terminal, draw: draw}
}

// SetSummary sets the one-line progress printer used off a terminal.
func (l *Live) SetSummary(fn func(frame int, now time.Time) string) {
	l.summary = fn
}

// SetEvents sets the machine-readable line printed off a terminal whenever
// it changes. On a terminal it is never called.
func (l *Live) SetEvents(fn func(now time.Time) string) {
	l.events = fn
}

func (l *Live) emitEvent(now time.Time) {
	if l.events == nil {
		return
	}
	if ev := l.events(now); ev != "" && ev != l.lastEvent {
		l.lastEvent = ev
		_, _ = fmt.Fprintln(l.w, ev)
	}
}

// Terminal reports whether Live redraws in place.
func (l *Live) Terminal() bool { return l.terminal }

// Start begins redrawing. On a terminal it updates ten times a second; off a
// terminal it prints a progress summary whenever the summary changes.
func (l *Live) Start() {
	l.stop, l.done = make(chan struct{}), make(chan struct{})
	interval := 100 * time.Millisecond
	if !l.terminal {
		interval = 750 * time.Millisecond
	} else {
		_, _ = io.WriteString(l.w, "\x1b[?25l") // hide the cursor while drawing
	}
	go func() {
		defer close(l.done)
		t := time.NewTicker(interval)
		defer t.Stop()
		var last string
		for {
			if l.terminal {
				l.Refresh()
			} else {
				l.streamTick(&last)
			}
			select {
			case <-l.stop:
				return
			case <-t.C:
			}
		}
	}()
}

// streamSpinner matches the braille glyphs Icon uses for Running. Off a
// terminal those glyphs change every tick; streamStable ignores them so a
// dashboard job stream does not grow one line per spinner frame.
var streamSpinner = regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]`)

// streamANSI strips SGR colour codes so tree keys compare equal with or without colour.
var streamANSI = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// streamStable is the comparison key for off-terminal progress: spinner
// animation is ignored; bare elapsed times advance every 3s so the drawer
// can show a progress bar once a step crosses that threshold; %/ETA changes
// always count as a new line.
func streamStable(s string) string {
	s = streamSpinner.ReplaceAllString(s, "…")
	if i := strings.LastIndexByte(s, ' '); i >= 0 {
		tail := s[i+1:]
		if secs, ok := streamDurationSeconds(tail); ok {
			// 0–2s stay quiet after the first tick; then one line per 3s.
			bucket := (secs / 3) * 3
			if bucket < 3 {
				return s[:i] + " …"
			}
			return s[:i] + " " + fmt.Sprintf("%ds", bucket)
		}
	}
	return s
}

// streamDurationSeconds parses Duration-style tails (850ms, 4.2s, 25s, 1m05s).
func streamDurationSeconds(tail string) (int, bool) {
	if strings.HasSuffix(tail, "ms") {
		return 0, true
	}
	if strings.HasSuffix(tail, "s") && !strings.Contains(tail, "m") {
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSuffix(tail, "s"), "%f", &f); err == nil {
			return int(f), true
		}
	}
	if strings.Contains(tail, "m") && strings.HasSuffix(tail, "s") {
		var m, sec int
		if _, err := fmt.Sscanf(tail, "%dm%ds", &m, &sec); err == nil {
			return m*60 + sec, true
		}
	}
	return 0, false
}

// treeStreamKey identifies a nested tree / progress row so off-TTY updates
// replace the previous line for the same resource instead of appending forever.
func treeStreamKey(line string) string {
	plain := strings.TrimSpace(streamANSI.ReplaceAllString(line, ""))
	if plain == "" {
		return ""
	}
	branch := strings.ContainsAny(plain, "├└│")
	bar := strings.ContainsAny(plain, "█░") && strings.Contains(plain, "%")
	if !branch && !bar {
		return ""
	}
	plain = strings.TrimLeft(plain, " \t└├│─")
	plain = streamSpinner.ReplaceAllString(plain, "")
	for _, icon := range []string{"✔ ", "✖ ", "⚠ ", "○ ", "… ", "▸ ", "– "} {
		plain = strings.TrimPrefix(plain, icon)
	}
	plain = strings.TrimSpace(plain)
	if bar {
		// Digits and % change every tick; keep one stream row per bar slot.
		stable := regexp.MustCompile(`\d+%?`).ReplaceAllString(plain, "#")
		return "progress:" + strings.TrimSpace(stable)
	}
	if i := strings.Index(plain, "  "); i >= 0 {
		plain = plain[:i]
	}
	return "tree:" + strings.TrimSpace(plain)
}

func (l *Live) streamTick(last *string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.frame++
	frame := l.frame
	now := time.Now()
	if sumFn := l.summary; sumFn != nil {
		sum := strings.TrimSpace(sumFn(frame, now))
		if sum != "" {
			key := streamStable(sum)
			if key != *last {
				*last = key
				_, _ = fmt.Fprintln(l.w, sum)
			}
		}
	}
	defer l.emitEvent(now)
	if l.draw == nil {
		return
	}
	if l.lastTree == nil {
		l.lastTree = map[string]string{}
	}
	for _, line := range l.draw(frame, now) {
		key := treeStreamKey(line)
		if key == "" {
			continue
		}
		stable := streamStable(streamANSI.ReplaceAllString(line, ""))
		if l.lastTree[key] == stable {
			continue
		}
		l.lastTree[key] = stable
		_, _ = fmt.Fprintln(l.w, line)
	}
}

// Refresh redraws the region now.
func (l *Live) Refresh() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.frame++
	l.paint(l.draw(l.frame, time.Now()))
}

// Print writes lines above the region, which is then redrawn below them:
// output that must stay on screen (a log line, a warning) while work goes on.
func (l *Live) Print(lines ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.terminal {
		l.clear()
	}
	for _, s := range lines {
		_, _ = fmt.Fprintln(l.w, s)
	}
	if l.terminal {
		l.paint(l.draw(l.frame, time.Now()))
	}
}

// Stop draws the final frame and leaves it on screen; off a terminal it
// prints that frame, the only full one.
func (l *Live) Stop() {
	if l.stop != nil {
		close(l.stop)
		<-l.done
		l.stop = nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	final := l.draw(l.frame, now)
	if !l.terminal {
		for _, s := range final {
			_, _ = fmt.Fprintln(l.w, s)
		}
		l.emitEvent(now)
		return
	}
	l.paint(final)
	l.lines = 0
	_, _ = io.WriteString(l.w, "\x1b[?25h")
}

func (l *Live) clear() {
	if l.lines > 0 {
		// Up to the region's first line, then erase to the end of the screen.
		_, _ = fmt.Fprintf(l.w, "\x1b[%dA\r\x1b[J", l.lines)
		l.lines = 0
	}
}

func (l *Live) paint(lines []string) {
	if !l.terminal {
		return
	}
	l.clear()
	var b strings.Builder
	for _, s := range lines {
		b.WriteString(s)
		b.WriteString("\x1b[K\n")
	}
	_, _ = io.WriteString(l.w, b.String())
	l.lines = len(lines)
}
