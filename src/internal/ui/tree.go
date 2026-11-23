package ui

import (
	"strings"
	"time"
)

// Node is one line of a tree and the lines nested under it.
type Node struct {
	Label string
	// Detail follows the label, dimmed: what was done, or why it failed.
	Detail string
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

// AbandonRunning marks every still-running or pending descendant as failed so
// a Stop frame after an error does not keep spinning under a failed parent
// (dashboard job drawer was left with live spinners after ✖ Start the stack).
func (n *Node) AbandonRunning(now time.Time) {
	if n == nil {
		return
	}
	for _, c := range n.Children {
		switch c.State {
		case Running, Pending:
			c.State, c.Ended = Failed, now
			c.Progress = nil
			if c.Detail == "" {
				c.Detail = "stopped"
			}
		}
		c.AbandonRunning(now)
	}
}

// Tree draws nodes as an indented tree, one string per line, at most width
// cells each (0 for no limit). frame animates the spinners.
func (s Style) Tree(nodes []*Node, width, frame int, now time.Time) []string {
	var out []string
	for _, n := range nodes {
		out = s.node(out, n, "", "", width, frame, now)
	}
	return out
}

func (s Style) node(out []string, n *Node, lead, childLead string, width, frame int, now time.Time) []string {
	line := lead + s.Icon(n.State, frame) + " " + s.label(n)
	if d := s.timing(n, now); d != "" {
		line += "  " + s.Dim(d)
	}
	out = append(out, fit(line, width))
	if n.Progress != nil && n.State == Running {
		barWidth := 24
		out = append(out, fit(childLead+"  "+s.Progress(*n.Progress, barWidth, now), width))
	}
	if n.State == Running || n.State == Failed {
		for _, l := range n.Log {
			out = append(out, fit(childLead+"  "+s.Dim(l), width))
		}
	}
	for i, c := range n.Children {
		// Spaces for the continuing lead (not "│"): a dim vertical bar reads as
		// a column of dots in many terminals and fights the checkmarks.
		branch, next := "├─ ", "   "
		if i == len(n.Children)-1 {
			branch, next = "└─ ", "   "
		}
		out = s.node(out, c, childLead+s.Dim(branch), childLead+next, width, frame, now)
	}
	return out
}

func (s Style) label(n *Node) string {
	l := n.Label
	switch n.State {
	case Failed:
		l = s.Red(l)
	case Pending, Skipped:
		l = s.Dim(l)
	}
	if n.Detail != "" {
		l += "  " + s.Dim(n.Detail)
	}
	return l
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
