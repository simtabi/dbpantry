package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type exitErr int

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitErr) ExitCode() int { return int(e) }

// gated is an Exec that prints its args, then waits for release, then
// returns code (0: success).
func gated(release <-chan struct{}, code int) Exec {
	return func(ctx context.Context, args []string, out io.Writer) error {
		_, _ = fmt.Fprintln(out, "start", strings.Join(args, " "))
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		_, _ = fmt.Fprintln(out, "done")
		if code != 0 {
			return exitErr(code)
		}
		return nil
	}
}

func waitEnd(t *testing.T, r *Runner, id string) Job {
	t.Helper()
	var final Job
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Watch(ctx, id, 0, func(int, string, time.Time) error { return nil }, func(j Job) error { final = j; return nil }); err != nil {
		t.Fatal(err)
	}
	return final
}

// TestOneMutationAtATime is the lock (plan §3.2): a second job while one
// runs is refused, naming the running one, and the next is accepted once it
// ends.
func TestOneMutationAtATime(t *testing.T) {
	release := make(chan struct{})
	audit := filepath.Join(t.TempDir(), "audit", "dashboard.jsonl")
	r := New(gated(release, 0), audit, 10)
	defer r.Close()
	first, err := r.Start("stack.up", "admin", true, []string{"up"}, []string{"up"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Start("stack.down", "admin", true, []string{"down"}, []string{"down"})
	var busy ErrBusy
	if !errors.As(err, &busy) || busy.Job.ID != first.ID {
		t.Fatalf("second Start = %v, want ErrBusy naming %s", err, first.ID)
	}
	if j, ok := r.Running(); !ok || j.ID != first.ID {
		t.Fatalf("Running = %v %v", j.ID, ok)
	}
	// A read is never refused, and does not take the lock.
	read, err := r.Start("ha.status", "admin", false, []string{"ha", "status"}, []string{"ha", "status"})
	if err != nil {
		t.Fatalf("a read while a change runs was refused: %v", err)
	}
	if j, _ := r.Running(); j.ID != first.ID {
		t.Fatalf("the read took the lock from %s", first.ID)
	}
	_ = read
	close(release)
	if j := waitEnd(t, r, first.ID); j.State != Succeeded || len(j.Lines) != 2 {
		t.Fatalf("first job ended %s with %d lines: %v", j.State, len(j.Lines), j.Lines)
	}
	if _, err := r.Start("stack.down", "admin", true, []string{"down"}, []string{"down"}); err != nil {
		t.Fatalf("Start after the first ended: %v", err)
	}
}

// TestWatchSendsEverythingInOrder: a stream opened after lines were written
// gets them first, then the rest, then the end, with the exit code.
func TestWatchSendsEverythingInOrder(t *testing.T) {
	release := make(chan struct{})
	r := New(gated(release, 3), "", 10)
	defer r.Close()
	j, err := r.Start("images.build", "admin", true, []string{"images", "build"}, []string{"images", "build"})
	if err != nil {
		t.Fatal(err)
	}
	// Let the first line land before watching.
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, _ := r.Get(j.ID)
		if len(got.Lines) == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	go func() { time.Sleep(20 * time.Millisecond); close(release) }()
	var lines []string
	var final Job
	err = r.Watch(context.Background(), j.ID, 0, func(n int, l string, _ time.Time) error {
		if n != len(lines) {
			t.Errorf("line %d arrived as number %d", len(lines), n)
		}
		lines = append(lines, l)
		return nil
	}, func(f Job) error { final = f; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "|") != "start images build|done" {
		t.Errorf("lines = %q", lines)
	}
	if final.State != Failed || final.ExitCode != 3 {
		t.Errorf("final = %s exit %d, want failed exit 3", final.State, final.ExitCode)
	}
	if _, err := r.Get("nope"); !errors.Is(err, ErrNoJob) {
		t.Errorf("Get(nope) = %v", err)
	}
}

// TestAuditRecordsEveryJob: one line per finished job, with its actor,
// action, redacted args, result and duration; read back newest first.
func TestAuditRecordsEveryJob(t *testing.T) {
	audit := filepath.Join(t.TempDir(), "audit", "dashboard.jsonl")
	open := make(chan struct{})
	close(open)
	r := New(gated(open, 0), audit, 10)
	defer r.Close()
	shown := Redact([]string{"restore", "--confirm", "app", "--password=hunter2"}, "--confirm", "--password")
	a, err := r.Start("backup.restore", "token:ci", true, []string{"restore"}, shown)
	if err != nil {
		t.Fatal(err)
	}
	waitEnd(t, r, a.ID)
	r2 := New(gated(open, 7), audit, 10)
	defer r2.Close()
	b, err := r2.Start("stack.down", "admin", true, []string{"down"}, []string{"down"})
	if err != nil {
		t.Fatal(err)
	}
	waitEnd(t, r2, b.ID)

	got, err := ReadAudit(audit, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("audit has %d entries, want 2", len(got))
	}
	if got[0].ID != b.ID || got[0].Result != Failed || got[0].ExitCode != 7 {
		t.Errorf("newest entry = %+v", got[0])
	}
	if got[1].Actor != "token:ci" || strings.Join(got[1].Args, " ") != "restore --confirm *** --password=***" {
		t.Errorf("older entry = %+v", got[1])
	}
	for _, e := range got {
		if strings.Contains(strings.Join(e.Args, " "), "hunter2") {
			t.Errorf("a secret reached the audit log: %v", e.Args)
		}
	}
}

func TestReadAuditMissingIsEmpty(t *testing.T) {
	got, err := ReadAudit(filepath.Join(t.TempDir(), "none.jsonl"), 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("ReadAudit(missing) = %v, %v", got, err)
	}
}
