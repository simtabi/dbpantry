// Package ui draws the command line's human output: a tree of steps with
// spinners, progress bars with a percentage and an estimate of the time left,
// and tables. It writes nothing on its own; commands hand it a writer.
//
// Everything degrades: without a terminal there is no animation and no
// cursor movement (one final frame is printed), and without colour (NO_COLOR,
// --no-color, TERM=dumb) the same layout is drawn in plain text.
package ui

import (
	"regexp"
	"strings"
)

// Style decides how text is painted. The zero value paints nothing.
type Style struct {
	// Color enables ANSI colour and weight.
	Color bool
	// Emoji draws a node's Icon before its label (terminals only: a log or
	// a dashboard stream stays plain).
	Emoji bool
	// AlignTimes right-aligns durations into one column and draws a running
	// node's progress bar on its own row instead of a line beneath it.
	AlignTimes bool
	// FoldDone hides the children of a finished top-level step that has a
	// summary: what passed folds to "2 files", the active or failed step stays open.
	FoldDone bool
}

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	blue   = "\x1b[34m"
	cyan   = "\x1b[36m"
)

func (s Style) paint(code, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return code + text + reset
}

// Bold paints text bold when colour is on.
func (s Style) Bold(t string) string { return s.paint(bold, t) }

// Dim paints text faint when colour is on.
func (s Style) Dim(t string) string { return s.paint(dim, t) }

// Red paints text red when colour is on.
func (s Style) Red(t string) string { return s.paint(red, t) }

// Green paints text green when colour is on.
func (s Style) Green(t string) string { return s.paint(green, t) }

// Yellow paints text yellow when colour is on.
func (s Style) Yellow(t string) string { return s.paint(yellow, t) }

// Blue paints text blue when colour is on.
func (s Style) Blue(t string) string { return s.paint(blue, t) }

// Cyan paints text cyan when colour is on.
func (s Style) Cyan(t string) string { return s.paint(cyan, t) }

// State is where a step or a resource is.
type State int

// The states, in the order a step moves through them.
const (
	Pending State = iota
	Running
	Done
	Warning
	Failed
	Skipped
)

// String names the state the way the run snapshot (and the job drawer) does.
func (st State) String() string {
	switch st {
	case Running:
		return "running"
	case Done:
		return "done"
	case Warning:
		return "warning"
	case Failed:
		return "failed"
	case Skipped:
		return "skipped"
	default:
		return "pending"
	}
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Icon is the state's mark; a running state animates with frame.
func (s Style) Icon(st State, frame int) string {
	switch st {
	case Running:
		return s.Cyan(spinner[frame%len(spinner)])
	case Done:
		return s.Green("✔")
	case Warning:
		return s.Yellow("⚠")
	case Failed:
		return s.Red("✖")
	case Skipped:
		return s.Dim("–")
	default:
		return s.Dim("○")
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// Width is the number of terminal cells text takes, ignoring colour codes.
// Pictographic emoji (step icons) take two cells; everything else one.
func Width(text string) int {
	n := 0
	for _, r := range ansi.ReplaceAllString(text, "") {
		n += runeWidth(r)
	}
	return n
}

func runeWidth(r rune) int {
	switch {
	case r == 0xFE0F || r == 0x200D:
		return 0
	case r >= 0x1F300 && r <= 0x1FAFF, r == 0x26A1:
		return 2
	default:
		return 1
	}
}

// Pad right-pads text with spaces to n cells.
func Pad(text string, n int) string {
	if w := Width(text); w < n {
		return text + strings.Repeat(" ", n-w)
	}
	return text
}

// Truncate shortens text to n cells, ending it with an ellipsis. Colour is
// dropped from a truncated value, so a cut never leaves an escape open.
func Truncate(text string, n int) string {
	if n <= 0 || Width(text) <= n {
		return text
	}
	if n == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range ansi.ReplaceAllString(text, "") {
		w := runeWidth(r)
		if used+w > n-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
}
