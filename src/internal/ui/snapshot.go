package ui

import (
	"strings"
	"time"
)

// SnapNode is a node as data: what the dashboard's job drawer renders, so it
// never has to parse the drawn text back into a tree. Times are Unix
// milliseconds; the drawer works out running durations itself, so a snapshot
// only changes when something does.
type SnapNode struct {
	Label  string `json:"label"`
	Icon   string `json:"icon,omitempty"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status,omitempty"`
	// Started and Ended are Unix milliseconds (0 when unknown).
	Started int64 `json:"started,omitempty"`
	Ended   int64 `json:"ended,omitempty"`
	// Pct is the progress in whole percent, when the node has a bar.
	Pct *int `json:"pct,omitempty"`
	// ETA is the estimated seconds left, rounded so it does not change every tick.
	ETA int64 `json:"eta,omitempty"`
	// Done and Total are the bar's units; Done is left out for byte counts,
	// which move on every chunk (the drawer derives it from Pct).
	Done     int64      `json:"done,omitempty"`
	Total    int64      `json:"total,omitempty"`
	Bytes    bool       `json:"bytes,omitempty"`
	Children []SnapNode `json:"children,omitempty"`
}

// Snapshot turns nodes into data, as they stand at now.
func Snapshot(nodes []*Node, now time.Time) []SnapNode {
	out := make([]SnapNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, snap(n, now))
	}
	return out
}

func snap(n *Node, now time.Time) SnapNode {
	s := SnapNode{
		Label:   strings.TrimSpace(n.Label),
		Icon:    n.Icon,
		State:   n.State.String(),
		Detail:  n.Detail,
		Status:  n.Status,
		Started: millis(n.Started),
		Ended:   millis(n.Ended),
	}
	if p := n.Progress; p != nil && n.State == Running && p.Total > 0 {
		pct := int(clamp(p.Fraction()) * 100)
		s.Pct, s.Total, s.Bytes = &pct, p.Total, p.Bytes
		if !p.Bytes {
			s.Done = p.Done
		}
		if !p.Started.IsZero() {
			if left, ok := ETA(now.Sub(p.Started), p.Fraction()); ok {
				s.ETA = roundETA(left)
			}
		}
	}
	for _, c := range n.Children {
		s.Children = append(s.Children, snap(c, now))
	}
	return s
}

func roundETA(d time.Duration) int64 {
	secs := int64(d.Round(time.Second) / time.Second)
	if secs <= 10 {
		return max(secs, 1)
	}
	return (secs + 4) / 5 * 5
}

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
