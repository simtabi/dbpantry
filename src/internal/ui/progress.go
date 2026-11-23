package ui

import (
	"fmt"
	"strings"
	"time"
)

// Bar draws a bar of width cells for fraction (0 to 1) followed by its
// percentage, e.g. "██████░░░░  62%". In colour the bar is a thin rule,
// green over dim ("━━━━━━━━━━"); without colour the two halves need
// different glyphs to be told apart.
func (s Style) Bar(fraction float64, width int) string {
	fraction = clamp(fraction)
	if width < 1 {
		width = 1
	}
	full := int(fraction*float64(width) + 0.5)
	fill, rest := "█", "░"
	if s.Color {
		fill, rest = "━", "━"
	}
	bar := s.Green(strings.Repeat(fill, full)) + s.Dim(strings.Repeat(rest, width-full))
	return fmt.Sprintf("%s %3d%%", bar, int(fraction*100))
}

// ETA estimates the time left for work that has done fraction of itself in
// elapsed time, assuming the rate so far holds. It is false until there is
// enough to go on: under 2% done, or under two seconds in.
func ETA(elapsed time.Duration, fraction float64) (time.Duration, bool) {
	fraction = clamp(fraction)
	if fraction >= 1 {
		return 0, true
	}
	if fraction < 0.02 || elapsed < 2*time.Second {
		return 0, false
	}
	total := time.Duration(float64(elapsed) / fraction)
	return total - elapsed, true
}

// Duration formats d the way a person reads it: 850ms, 4.2s, 1m05s, 1h02m.
func Duration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// Bytes formats a byte count with a binary unit: 512B, 5.2KiB, 130.8MiB.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Progress is how far a piece of work has got: Done of Total units, from
// Started. Total may grow as more work is discovered (an image build finds
// its steps as it goes), so the ETA is labelled an estimate.
type Progress struct {
	Done, Total int64
	// Bytes says the units are bytes, so they are shown as sizes.
	Bytes   bool
	Started time.Time
}

// Fraction is Done/Total, or 0 when nothing is known.
func (p Progress) Fraction() float64 {
	if p.Total <= 0 {
		return 0
	}
	return float64(p.Done) / float64(p.Total)
}

// Progress draws the bar, the counts and the estimate: "███░░  40%  4/10  ~12s left".
func (s Style) Progress(p Progress, width int, now time.Time) string {
	f := p.Fraction()
	parts := []string{s.Bar(f, width)}
	if p.Bytes {
		parts = append(parts, s.Dim(Bytes(p.Done)+" / "+Bytes(p.Total)))
	} else {
		parts = append(parts, s.Dim(fmt.Sprintf("%d/%d", p.Done, p.Total)))
	}
	if !p.Started.IsZero() && f < 1 {
		if left, ok := ETA(now.Sub(p.Started), f); ok {
			parts = append(parts, s.Dim("~"+Duration(left)+" left"))
		} else {
			parts = append(parts, s.Dim("estimating…"))
		}
	}
	return strings.Join(parts, "  ")
}

func clamp(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}
