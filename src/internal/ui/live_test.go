package ui

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveStreamsSummaryOffTerminal(t *testing.T) {
	var buf bytes.Buffer
	var frame atomic.Int32
	var saw atomic.Bool
	live := NewLive(&buf, false, func(f int, now time.Time) []string {
		return []string{"full tree line"}
	})
	live.SetSummary(func(f int, now time.Time) string {
		frame.Store(int32(f))
		if f < 2 {
			return ""
		}
		saw.Store(true)
		return "▸ Start the stack  50%  ~10s left"
	})
	live.Start()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if saw.Load() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	live.Stop()
	out := buf.String()
	if !strings.Contains(out, "50%") {
		t.Fatalf("expected a progress summary, got %q (frames=%d)", out, frame.Load())
	}
	if !strings.Contains(out, "full tree line") {
		t.Fatalf("Stop should print the final tree: %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("off-terminal stream must not emit escape sequences: %q", out)
	}
}

func TestLiveStreamSkipsSpinnerFrames(t *testing.T) {
	// Off a terminal, spinner glyphs and sub-second durations must not
	// each become a new line — the dashboard job drawer coalesces steps.
	var buf bytes.Buffer
	live := NewLive(&buf, false, func(int, time.Time) []string {
		return []string{"final"}
	})
	var n atomic.Int32
	live.SetSummary(func(f int, now time.Time) string {
		n.Add(1)
		glyphs := []string{"⠋", "⠙", "⠹", "⠸"}
		return glyphs[f%len(glyphs)] + " Check configuration  " + Duration(time.Duration(f)*100*time.Millisecond)
	})
	live.Start()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n.Load() >= 6 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	live.Stop()
	lines := 0
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.Contains(line, "Check configuration") {
			lines++
		}
	}
	if lines > 2 {
		t.Fatalf("spinner frames must coalesce; got %d progress lines in %q", lines, buf.String())
	}
}

func TestLiveTerminalOnlyFinalIsNotEnough(t *testing.T) {
	// Off a terminal without a summary, Start stays quiet and Stop prints once.
	var buf bytes.Buffer
	live := NewLive(&buf, false, func(int, time.Time) []string {
		return []string{"only-final"}
	})
	live.Start()
	time.Sleep(200 * time.Millisecond)
	live.Stop()
	if got := strings.TrimSpace(buf.String()); got != "only-final" {
		t.Fatalf("Stop = %q", got)
	}
}

func TestLiveStreamsTreeLinesOffTerminal(t *testing.T) {
	// Nested ├─ / └─ rows must stream mid-flight so the job drawer shows
	// under-the-hood compose work before Stop.
	// Live writes from its own goroutine while the test polls the output, so
	// the buffer is read and written under one lock (go test -race).
	var buf syncBuffer
	var phase atomic.Int32
	live := NewLive(&buf, false, func(int, time.Time) []string {
		if phase.Load() == 0 {
			return []string{"dbpantry › up", "… Start the stack"}
		}
		return []string{
			"dbpantry › up",
			"… Start the stack",
			"├─ Containers",
			"└─ … mdblive-pg-1",
		}
	})
	live.SetSummary(func(int, time.Time) string { return "… Start the stack" })
	live.Start()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "Start the stack") {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	phase.Store(1)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "mdblive-pg-1") {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	live.Stop()
	out := buf.String()
	if !strings.Contains(out, "├─ Containers") || !strings.Contains(out, "mdblive-pg-1") {
		t.Fatalf("expected nested tree mid-flight, got %q", out)
	}
}

func TestStreamStableBucketsElapsedEveryThreeSeconds(t *testing.T) {
	if streamStable("… Start the stack  1s") != streamStable("… Start the stack  2s") {
		t.Fatal("sub-3s elapsed must share one stream key")
	}
	if streamStable("… Start the stack  3s") == streamStable("… Start the stack  1s") {
		t.Fatal("3s elapsed must advance the stream so the drawer can show a progress bar")
	}
	if streamStable("… Start the stack  3s") != streamStable("… Start the stack  5s") {
		t.Fatal("3s–5s must share the same 3s bucket")
	}
	if streamStable("… Start the stack  50%") == streamStable("… Start the stack  51%") {
		t.Fatal("percentage changes must always stream")
	}
	if treeStreamKey("├─ ✔ mdblive-pg-1  1.0s") != "tree:mdblive-pg-1" {
		t.Fatalf("treeStreamKey = %q", treeStreamKey("├─ ✔ mdblive-pg-1  1.0s"))
	}
}

// syncBuffer is a bytes.Buffer safe for one writer and one reader at once.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
