package ui

import (
	"bytes"
	"strings"
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
