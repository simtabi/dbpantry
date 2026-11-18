package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Live redraws a region of the terminal while work runs: the spinner and
// progress frames. On a terminal it moves the cursor back over the previous
// frame; anywhere else it prints only the final frame, once, so a log or a
// pipe gets a clean record and no escape sequences.
type Live struct {
	w        io.Writer
	terminal bool
	draw     func(frame int, now time.Time) []string

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

// Start begins redrawing ten times a second. It does nothing off a terminal.
func (l *Live) Start() {
	if !l.terminal {
		return
	}
	l.stop, l.done = make(chan struct{}), make(chan struct{})
	_, _ = io.WriteString(l.w, "\x1b[?25l") // hide the cursor while drawing
	go func() {
		defer close(l.done)
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			l.Refresh()
			select {
			case <-l.stop:
				return
			case <-t.C:
			}
		}
	}()
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
// prints that frame, the only one.
func (l *Live) Stop() {
	if l.terminal && l.stop != nil {
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
