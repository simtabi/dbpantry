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
// sequences), and Stop prints the final full frame once.
type Live struct {
	w        io.Writer
	terminal bool
	draw     func(frame int, now time.Time) []string
	// summary, when set, is the one-line progress printed off a terminal.
	// When nil, stream mode stays quiet until Stop.
	summary func(frame int, now time.Time) string

	mu    sync.Mutex
	lines int
	frame int
	stop  chan struct{}
	done  chan struct{}
}

// NewLive returns a region drawn by draw. terminal says w is a terminal.
func NewLive(w io.Writer, terminal bool, draw func(frame int, now time.Time) []string) *Live {
	return &Live{w: w, terminal: terminal, draw: draw}
}

// SetSummary sets the one-line progress printer used off a terminal.
func (l *Live) SetSummary(fn func(frame int, now time.Time) string) {
	l.summary = fn
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

// streamStable is the comparison key for off-terminal progress: spinner
// animation and sub-second duration flicker do not count as a new line.
func streamStable(s string) string {
	s = streamSpinner.ReplaceAllString(s, "…")
	// Bucket trailing durations like "751ms" / "1.5s" to whole seconds so
	// the stream only advances when the step or the progress %/ETA moves.
	if i := strings.LastIndexByte(s, ' '); i >= 0 {
		tail := s[i+1:]
		if strings.HasSuffix(tail, "ms") || strings.HasSuffix(tail, "s") {
			s = s[:i] + " …"
		}
	}
	return s
}

func (l *Live) streamTick(last *string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	sumFn := l.summary
	if sumFn == nil {
		return
	}
	l.frame++
	frame := l.frame
	sum := strings.TrimSpace(sumFn(frame, time.Now()))
	if sum == "" {
		return
	}
	key := streamStable(sum)
	if key == *last {
		return
	}
	*last = key
	_, _ = fmt.Fprintln(l.w, sum)
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
	final := l.draw(l.frame, time.Now())
	if !l.terminal {
		for _, s := range final {
			_, _ = fmt.Fprintln(l.w, s)
		}
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
