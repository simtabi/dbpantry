package ui

import (
	"strings"
	"time"
)

// Node is one line of a tree and the lines nested under it.
type Node struct {
	Label string
	// Icon is drawn before the label when the style allows emoji: 🚀, 🐳.
	Icon string
	// Detail follows the label, dimmed: what was done, or why it failed.
	Detail string
	// Status is the resource's own word for where it is ("Healthy",
	// "Pulled"), kept for the run snapshot even when Detail omits it.
	Status string
	State  State
	// Started and Ended time the node; a running node shows time so far.
	Started, Ended time.Time
	// Progress, when set, is drawn on a line of its own under the label.
	Progress *Progress
	// Log is the last lines a running step printed, shown dimmed beneath it.
	Log      []string
	Children []*Node
}

// Add appends a child and returns it.
func (n *Node) Add(label string, st State) *Node {
	c := &Node{Label: label, State: st}
	n.Children = append(n.Children, c)
	return c
}

// Start marks the node running from now.
func (n *Node) Start(now time.Time) {
	n.State, n.Started = Running, now
}

// Finish marks the node finished in state st, with an optional detail.
func (n *Node) Finish(st State, detail string, now time.Time) {
	n.State, n.Ended = st, now
	if detail != "" {
		n.Detail = detail
	}
}

// AbandonRunning settles every unfinished descendant of a failed step, so
// the final frame says what really happened: work that never began is
// "not started", work cut off mid-way is "stopped", and neither is drawn as
// a failure (only the resource that failed is). A group takes the worst of
// its children.
func (n *Node) AbandonRunning(now time.Time) {
	if n == nil {
		return
	}
	for _, c := range n.Children {
		c.AbandonRunning(now)
		if len(c.Children) > 0 && (c.State == Running || c.State == Pending) {
			c.State, c.Ended, c.Progress = worst(c.Children), now, nil
			continue
		}
		switch c.State {
		case Running, Pending:
			detail := "stopped"
			if c.State == Pending || c.Started.IsZero() || strings.EqualFold(strings.TrimSpace(c.Detail), "waiting") {
				detail = "not started"
			}
			c.State, c.Ended, c.Progress, c.Detail = Skipped, now, nil, detail
		}
	}
}

func worst(nodes []*Node) State {
	st := Done
	for _, c := range nodes {
		switch {
		case c.State == Failed:
			return Failed
		case c.State == Skipped:
			st = Skipped
		case c.State == Warning && st == Done:
			st = Warning
		}
	}
	return st
}

// Tree draws nodes as an indented tree, one string per line, at most width
// cells each (0 for no limit). frame animates the spinners.
func (s Style) Tree(nodes []*Node, width, frame int, now time.Time) []string {
	var out []string
	for _, n := range nodes {
		out = s.node(out, n, "", "", width, frame, now, 0)
	}
	return out
}

func (s Style) node(out []string, n *Node, lead, childLead string, width, frame int, now time.Time, depth int) []string {
	left := lead + s.Icon(n.State, frame) + " " + s.label(n)
	bar := ""
	running := n.Progress != nil && n.State == Running
	if running && s.AlignTimes {
		bar = s.Progress(*n.Progress, 12, now)
	}
	out = append(out, s.row(left, bar, s.timing(n, now), width))
	if running && !s.AlignTimes {
		out = append(out, fit(childLead+"  "+s.Progress(*n.Progress, 24, now), width))
	}
	if n.State == Running || n.State == Failed {
		for _, l := range n.Log {
			out = append(out, fit(childLead+"  "+s.Dim(l), width))
		}
	}
	if s.FoldDone && depth == 0 && n.State == Done && n.Detail != "" {
		return out
	}
	for i, c := range n.Children {
		// Spaces for the continuing lead (not "│"): a dim vertical bar reads as
		// a column of dots in many terminals and fights the checkmarks.
		branch, next := "├─ ", "   "
		if i == len(n.Children)-1 {
			branch, next = "└─ ", "   "
		}
		out = s.node(out, c, childLead+s.Dim(branch), childLead+next, width, frame, now, depth+1)
	}
	return out
}

// row lays out one tree line. Inline it is "label  bar  1.2s"; aligned, the
// bar and the duration sit in right-hand columns shared by every row.
func (s Style) row(left, bar, dur string, width int) string {
	if !s.AlignTimes || width <= 0 {
		line := left
		if bar != "" {
			line += "  " + bar
		}
		if dur != "" {
			line += "  " + s.Dim(dur)
		}
		return fit(line, width)
	}
	col := min(width, 100)
	tail := bar
	if dur != "" {
		if tail != "" {
			tail += "  "
		}
		tail += s.Dim(strings.Repeat(" ", max(0, 6-Width(dur))) + dur)
	}
	if tail == "" {
		return fit(left, width)
	}
	room := col - Width(tail) - 2
	if room < 12 {
		return fit(left+"  "+tail, width)
	}
	left = fit(left, room)
	return left + strings.Repeat(" ", col-Width(left)-Width(tail)) + tail
}

func (s Style) label(n *Node) string {
	l := strings.TrimRight(n.Label, " ")
	if s.Emoji && n.Icon != "" {
		l = n.Icon + " " + l
	}
	switch n.State {
	case Failed:
		l = s.Red(l)
	case Pending, Skipped:
		l = s.Dim(l)
	}
	if n.Detail == "" {
		return l
	}
	// Pad names back to the group's column so details line up.
	if pad := Width(n.Label) - Width(strings.TrimRight(n.Label, " ")); pad > 0 {
		l += strings.Repeat(" ", pad)
	}
	switch n.State {
	case Failed:
		return l + "  " + s.Red(n.Detail)
	case Skipped, Warning:
		return l + "  " + s.Yellow(n.Detail)
	case Running:
		return l + "  " + s.Cyan(n.Detail)
	default:
		return l + "  " + s.Dim(n.Detail)
	}
}

func (s Style) timing(n *Node, now time.Time) string {
	switch {
	case n.Started.IsZero():
		return ""
	case n.State == Running:
		return Duration(now.Sub(n.Started))
	case !n.Ended.IsZero():
		return Duration(n.Ended.Sub(n.Started))
	default:
		return ""
	}
}

func fit(line string, width int) string {
	if width > 0 && Width(line) > width {
		return Truncate(line, width)
	}
	return line
}

// Rule is a horizontal rule, with a title when one is given:
// "── Paste into your .env ─────".
func (s Style) Rule(title string, width int) string {
	if width <= 0 || width > 80 {
		width = 80
	}
	if title == "" {
		return s.Dim(strings.Repeat("─", width))
	}
	rest := width - Width(title) - 4
	if rest < 3 {
		rest = 3
	}
	return s.Dim("── ") + s.Bold(title) + " " + s.Dim(strings.Repeat("─", rest))
}
