package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestAbortEndsTheWholeJob: cancelling a dashboard job (ContextWithJobKill)
// ends the script and everything it started. Until 2026-11-24 only the
// script was killed; a child it started (docker compose, a build, here
// sleep) kept the output pipe open, so the job stayed Running, and held the
// one-change lock, until that child finished on its own.
func TestAbortEndsTheWholeJob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are a Unix mechanism; Windows kills the process alone")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o750); err != nil {
		t.Fatal(err)
	}
	// A child that outlives the script unless its group is killed, holding
	// the script's stdout.
	body := "#!/usr/bin/env bash\necho started\nsleep 30 &\nwait\n"
	if err := os.WriteFile(filepath.Join(root, "scripts", "slow"), []byte(body), 0o700); err != nil { // #nosec G306 -- an executable stub in a test directory
		t.Fatal(err)
	}
	a := &app{environ: os.Environ}
	ctx, cancel := context.WithCancel(ContextWithJobKill(context.Background()))
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- a.runStepTo(ctx, root, os.Environ(), script("scripts/slow"), &out, &out) }()
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	cancel()
	select {
	case <-done:
		if d := time.Since(start); d > 8*time.Second {
			t.Errorf("the job ended %s after the abort", d)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the job did not end within 15s of the abort: a child of the script kept it running")
	}
}
