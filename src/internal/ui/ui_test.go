package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestBarShowsFractionAndPercent(t *testing.T) {
	cases := []struct {
		f    float64
		want string
	}{
		{0, "░░░░░░░░░░   0%"},
		{0.5, "█████░░░░░  50%"},
		{1, "██████████ 100%"},
		{1.7, "██████████ 100%"}, // clamped
		{-1, "░░░░░░░░░░   0%"},
	}
	for _, c := range cases {
		if got := (Style{}).Bar(c.f, 10); got != c.want {
			t.Errorf("Bar(%v) = %q, want %q", c.f, got, c.want)
		}
	}
}

func TestETAWaitsForEvidenceThenExtrapolates(t *testing.T) {
	if _, ok := ETA(time.Second, 0.5); ok {
		t.Error("an ETA after one second is a guess, not an estimate")
	}
	if _, ok := ETA(time.Minute, 0.01); ok {
		t.Error("an ETA at 1% is a guess, not an estimate")
	}
	left, ok := ETA(30*time.Second, 0.25)
	if !ok || left != 90*time.Second {
		t.Errorf("ETA(30s, 25%%) = %v, %v; want 1m30s", left, ok)
	}
	if left, ok := ETA(time.Hour, 1); !ok || left != 0 {
		t.Errorf("finished work has no time left, got %v", left)
	}
}

func TestDurationAndBytesReadLikeAPerson(t *testing.T) {
	for d, want := range map[time.Duration]string{
		850 * time.Millisecond:  "850ms",
		4200 * time.Millisecond: "4.2s",
		42 * time.Second:        "42s",
		65 * time.Second:        "1m05s",
		62 * time.Minute:        "1h02m",
	} {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
	for n, want := range map[int64]string{512: "512B", 5325: "5.2KiB", 137143746: "130.8MiB"} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestProgressLineHasCountsAndETA(t *testing.T) {
	now := time.Now()
	p := Progress{Done: 25, Total: 100, Started: now.Add(-30 * time.Second)}
	got := (Style{}).Progress(p, 10, now)
	for _, want := range []string{" 25%", "25/100", "~1m30s left"} {
		if !strings.Contains(got, want) {
			t.Errorf("progress line %q lacks %q", got, want)
		}
	}
	early := Progress{Done: 1, Total: 100, Started: now}
	if got := (Style{}).Progress(early, 10, now); !strings.Contains(got, "estimating…") {
		t.Errorf("an early progress line should say it is estimating: %q", got)
	}
}

func TestTreeDrawsBranchesStatesAndTimes(t *testing.T) {
	now := time.Now()
	root := &Node{Label: "Start the stack", State: Done, Started: now.Add(-2 * time.Second), Ended: now}
	images := root.Add("Images", Done)
	images.Add("localhost/dbpantry-pg:17", Done).Detail = "Built"
	c := root.Add("Containers", Running)
	c.Started = now.Add(-5 * time.Second)
	got := strings.Join((Style{}).Tree([]*Node{root}, 0, 0, now), "\n")
	want := strings.Join([]string{
		"✔ Start the stack  2.0s",
		"├─ ✔ Images",
		"   └─ ✔ localhost/dbpantry-pg:17  Built",
		"└─ ⠋ Containers  5.0s",
	}, "\n")
	if got != want {
		t.Errorf("tree:\n%s\nwant:\n%s", got, want)
	}
}

func TestTreeTruncatesToWidth(t *testing.T) {
	n := &Node{Label: strings.Repeat("x", 50), State: Done}
	for _, l := range (Style{Color: true}).Tree([]*Node{n}, 20, 0, time.Now()) {
		if Width(l) > 20 {
			t.Errorf("line %q is %d cells, over 20", l, Width(l))
		}
	}
}

func TestTableAlignsAndFits(t *testing.T) {
	st := Style{Color: true}
	tbl := Table{Headers: []string{"Engine", "Port"}, Rows: [][]string{{st.Green("PostgreSQL"), "5432"}, {"MySQL", "3306"}}}
	lines := st.Table(tbl, 0)
	if len(lines) != 6 {
		t.Fatalf("a two-row table is 6 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	w := Width(lines[0])
	for _, l := range lines {
		if Width(l) != w {
			t.Errorf("ragged table: %q is %d cells, the top border %d", l, Width(l), w)
		}
	}
	for _, l := range st.Table(tbl, 16) {
		if Width(l) > 16 {
			t.Errorf("%q exceeds the 16-cell limit", l)
		}
	}
}

func TestLiveOffATerminalPrintsOnlyTheFinalFrame(t *testing.T) {
	var buf bytes.Buffer
	frames := 0
	l := NewLive(&buf, false, func(int, time.Time) []string {
		frames++
		return []string{"frame"}
	})
	l.Start()
	l.Refresh()
	l.Stop()
	if got := buf.String(); got != "frame\n" {
		t.Errorf("off a terminal the output is the last frame alone, got %q", got)
	}
	if strings.Contains(buf.String(), "\x1b") {
		t.Error("escape sequences written to a non-terminal")
	}
}

func TestLiveOnATerminalRedrawsInPlace(t *testing.T) {
	var buf bytes.Buffer
	l := NewLive(&buf, true, func(int, time.Time) []string { return []string{"a", "b"} })
	l.Refresh()
	l.Refresh()
	l.Stop()
	if !strings.Contains(buf.String(), "\x1b[2A") {
		t.Errorf("a redraw moves the cursor back over the two lines: %q", buf.String())
	}
}
