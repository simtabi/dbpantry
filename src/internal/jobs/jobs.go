// Package jobs runs the dashboard's actions one at a time and remembers
// them (management-plan.md §3.2, design.md D-86).
//
// A job is one dbpantry command, run by Exec. The runner holds the
// one-mutation lock: the scripts assume nothing else changes the stack while
// they run, so a second job while one is running is refused with ErrBusy,
// naming the running job. Every job's output is kept line by line for the
// event stream, and every finished job appends one line to the audit log.
//
// While a job runs the dashboard can pause it (stalls on the next write),
// continue it, abort it (cancels its context), or restart a finished one
// with the same arguments (design.md D-98).
package jobs

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// State is a job's state.
type State string

// A job is running (or paused), then succeeded, failed or cancelled.
const (
	Running   State = "running"
	Paused    State = "paused"
	Succeeded State = "succeeded"
	Failed    State = "failed"
	Cancelled State = "cancelled"
)

// Job is one run of an action. Args are the command's arguments as shown
// and audited, with secrets already removed by the caller.
type Job struct {
	ID       string    `json:"id"`
	Action   string    `json:"action"`
	Args     []string  `json:"args"`
	Actor    string    `json:"actor"`
	State    State     `json:"state"`
	ExitCode int       `json:"exit_code"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitzero"`
	// Lines is the output so far, stdout and stderr in the order written.
	Lines []string `json:"lines,omitempty"`
}

// Active reports whether the job still holds work (running or paused).
func (j Job) Active() bool {
	return j.State == Running || j.State == Paused
}

// Duration is how long the job ran, or has been running.
func (j Job) Duration(now time.Time) time.Duration {
	if j.Ended.IsZero() {
		return now.Sub(j.Started)
	}
	return j.Ended.Sub(j.Started)
}

// Exec runs a command and writes its output to out. A non-nil error is a
// failure; ExitCode extracts the status from it.
type Exec func(ctx context.Context, args []string, out io.Writer) error

// ExitCoder is an error that carries a process's exit status, as
// *exec.ExitError does.
type ExitCoder interface{ ExitCode() int }

// ErrBusy is returned by Start while another job runs.
type ErrBusy struct{ Job Job }

func (e ErrBusy) Error() string {
	return fmt.Sprintf("job %s (%s) is running; one change at a time", e.Job.ID, e.Job.Action)
}

// ErrNoJob is returned for an id the runner does not know.
var ErrNoJob = errors.New("no such job")

// ErrNotActive is returned when pause/continue/abort targets a finished job.
var ErrNotActive = errors.New("job is not running")

// ErrNotPaused is returned by Continue when the job is not paused.
var ErrNotPaused = errors.New("job is not paused")

// Runner runs jobs, one at a time.
type Runner struct {
	exec  Exec
	audit string // the audit log's path; empty for none
	now   func() time.Time
	keep  int

	mu      sync.Mutex
	jobs    map[string]*job
	order   []string
	running string
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	onEnd   func(Job) // called once when each job ends (OnEnd)
}

type job struct {
	Job
	cancel    context.CancelFunc
	exclusive bool
	aborted   bool     // Abort was requested; Exec may return a plain exit status
	runArgs   []string // unredacted exec argv; never exposed in JSON
	hide      []string // values redacted in Args, blanked in output lines too
	changed   chan struct{}
	unpause   chan struct{} // closed to wake a paused writer; replaced on Pause
	times     []time.Time   // when each line of Lines was written
}

// addLine appends one output line, stamped with when it arrived. Called
// with r.mu held.
func (j *job) addLine(text string, at time.Time) {
	for _, h := range j.hide {
		text = strings.ReplaceAll(text, h, "***")
	}
	j.Lines = append(j.Lines, text)
	j.times = append(j.times, at)
}

// New returns a runner. audit is the JSON-lines audit log, created with its
// directory on first use; "" keeps none. The last keep jobs stay in memory
// for their output.
func New(exec Exec, audit string, keep int) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	if keep <= 0 {
		keep = 50
	}
	return &Runner{exec: exec, audit: audit, now: time.Now, keep: keep, jobs: map[string]*job{}, ctx: ctx, cancel: cancel}
}

// OnEnd sets the function called once for every job that ends, with the
// finished job: the dashboard emits its completion webhook there, so the
// event does not depend on whether, or how often, a page watches the job.
func (r *Runner) OnEnd(f func(Job)) {
	r.mu.Lock()
	r.onEnd = f
	r.mu.Unlock()
}

// Close cancels a running job and waits for it to end.
func (r *Runner) Close() {
	r.cancel()
	r.wg.Wait()
}

// Start runs the command in the background as a new job and returns it.
// An exclusive job (anything that changes the stack) holds the lock: while
// one runs, Start refuses another exclusive job with ErrBusy. A job that only
// reads is never refused and never blocks one.
func (r *Runner) Start(action, actor string, exclusive bool, args, shown []string) (Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if exclusive && r.running != "" {
		return Job{}, ErrBusy{Job: r.jobs[r.running].snapshot()}
	}
	id, err := newID()
	if err != nil {
		return Job{}, err
	}
	jctx, jcancel := context.WithCancel(r.ctx)
	j := &job{
		Job:       Job{ID: id, Action: action, Args: shown, Actor: actor, State: Running, Started: r.now()},
		cancel:    jcancel,
		exclusive: exclusive,
		runArgs:   append([]string(nil), args...),
		hide:      hidden(args, shown),
		changed:   make(chan struct{}),
		unpause:   make(chan struct{}),
	}
	close(j.unpause) // not paused: writes pass through
	r.jobs[id] = j
	r.order = append(r.order, id)
	if exclusive {
		r.running = id
	}
	r.trim()
	r.wg.Add(1)
	go r.run(jctx, j, args, exclusive)
	return j.snapshot(), nil
}

// hidden lists the values the caller redacted: each argument whose shown
// form differs from it ("x" shown as "***", "--flag=x" as "--flag=***").
// Output lines are blanked of them too, so a command that echoes its
// arguments cannot undo the redaction. Values under 4 bytes are left alone:
// blanking them would mangle ordinary text.
func hidden(args, shown []string) []string {
	var out []string
	for i := 0; i < len(args) && i < len(shown); i++ {
		if args[i] == shown[i] {
			continue
		}
		v := args[i]
		if k, rest, ok := strings.Cut(v, "="); ok && shown[i] == k+"=***" {
			v = rest
		}
		if len(v) >= 4 {
			out = append(out, v)
		}
	}
	return out
}

func (r *Runner) run(ctx context.Context, j *job, args []string, exclusive bool) {
	defer r.wg.Done()
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			r.mu.Lock()
			j.addLine(sc.Text(), r.now())
			j.notify()
			r.mu.Unlock()
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	out := &pausingWriter{r: r, j: j, w: pw}
	err := r.exec(ctx, args, out)
	_ = pw.Close()
	<-done

	r.mu.Lock()
	final := j.snapshot()
	aborted := j.aborted || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil
	r.mu.Unlock()
	final.Ended = r.now()
	switch {
	case aborted:
		final.State = Cancelled
		final.ExitCode = 130
		final.Error = "aborted"
	case err != nil:
		final.State = Failed
		final.ExitCode = 1
		var ec ExitCoder
		if errors.As(err, &ec) {
			final.ExitCode = ec.ExitCode()
		}
		final.Error = err.Error()
	default:
		final.State = Succeeded
	}
	// The audit line is written before the job reads as ended, so whoever
	// sees it end (a Watch, the next Start) also finds it in the log.
	r.record(final)

	r.mu.Lock()
	j.Ended, j.State, j.ExitCode, j.Error = final.Ended, final.State, final.ExitCode, final.Error
	if exclusive {
		r.running = ""
	}
	j.notify()
	r.mu.Unlock()
}

// pausingWriter stalls Write while the job is paused, so in-process and
// scripted commands that emit output stop making progress until Continue.
type pausingWriter struct {
	r *Runner
	j *job
	w io.Writer
}

func (p *pausingWriter) Write(b []byte) (int, error) {
	for {
		p.r.mu.Lock()
		state := p.j.State
		ch := p.j.unpause
		p.r.mu.Unlock()
		if state != Paused {
			return p.w.Write(b)
		}
		select {
		case <-ch:
		case <-p.r.ctx.Done():
			return 0, p.r.ctx.Err()
		}
	}
}

// Abort cancels a running or paused job. The command's context ends; the job
// finishes as cancelled once Exec returns.
func (r *Runner) Abort(id string) (Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if !ok {
		return Job{}, ErrNoJob
	}
	if !j.Active() {
		return Job{}, ErrNotActive
	}
	if j.State == Paused {
		j.State = Running
		close(j.unpause) // wake any stalled write so Cancel can land
	}
	j.aborted = true
	j.cancel()
	j.addLine("· aborted by operator", r.now())
	j.notify()
	return j.snapshot(), nil
}

// Pause stalls the job on its next output write and marks it paused.
func (r *Runner) Pause(id string) (Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if !ok {
		return Job{}, ErrNoJob
	}
	if j.State != Running {
		if j.State == Paused {
			return j.snapshot(), nil
		}
		return Job{}, ErrNotActive
	}
	j.State = Paused
	j.unpause = make(chan struct{})
	j.addLine("· paused — output is held until you continue", r.now())
	j.notify()
	return j.snapshot(), nil
}

// Continue resumes a paused job.
func (r *Runner) Continue(id string) (Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if !ok {
		return Job{}, ErrNoJob
	}
	if j.State != Paused {
		return Job{}, ErrNotPaused
	}
	j.State = Running
	close(j.unpause)
	j.addLine("· continued", r.now())
	j.notify()
	return j.snapshot(), nil
}

// Restart starts the same action again. If the job is still active it is
// aborted first; the new job is returned.
func (r *Runner) Restart(id, actor string) (Job, error) {
	r.mu.Lock()
	j, ok := r.jobs[id]
	if !ok {
		r.mu.Unlock()
		return Job{}, ErrNoJob
	}
	action, exclusive := j.Action, j.exclusive
	prevActor := j.Actor
	args := append([]string(nil), j.runArgs...)
	shown := append([]string(nil), j.Args...)
	active := j.Active()
	cancel := j.cancel
	if active && j.State == Paused {
		j.State = Running
		close(j.unpause)
	}
	r.mu.Unlock()

	if active {
		cancel()
		ctx, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		_ = r.Watch(ctx, id, 0, func(int, string, time.Time) error { return nil }, func(Job) error { return nil })
	}
	if actor == "" {
		actor = prevActor
	}
	return r.Start(action, actor, exclusive, args, shown)
}

// notify wakes every Watch of the job. Called with r.mu held.
func (j *job) notify() {
	close(j.changed)
	j.changed = make(chan struct{})
}

func (j *job) snapshot() Job {
	c := j.Job
	c.Lines = append([]string(nil), j.Lines...)
	return c
}

// trim forgets the oldest finished jobs beyond keep. Called with r.mu held.
func (r *Runner) trim() {
	for len(r.order) > r.keep {
		id := r.order[0]
		if r.jobs[id].Active() {
			return
		}
		delete(r.jobs, id)
		r.order = r.order[1:]
	}
}

// Get returns one job, its output included.
func (r *Runner) Get(id string) (Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if !ok {
		return Job{}, ErrNoJob
	}
	return j.snapshot(), nil
}

// List returns the jobs in memory, newest first, without their output.
func (r *Runner) List() []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Job, 0, len(r.order))
	for i := len(r.order) - 1; i >= 0; i-- {
		j := r.jobs[r.order[i]].Job
		j.Lines = nil
		out = append(out, j)
	}
	return out
}

// Running returns the running exclusive job, if any: the one holding the
// lock. A paused exclusive job still holds it.
func (r *Runner) Running() (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == "" {
		return Job{}, false
	}
	return r.jobs[r.running].snapshot(), true
}

// Watch calls emit with each output line from line `from` on (and when it
// was written), then with the
// finished job, and returns when the job ends or ctx is done. It is how the
// event stream follows a job: lines already written are sent at once, so a
// stream opened late misses nothing. Paused jobs stay open.
func (r *Runner) Watch(ctx context.Context, id string, from int, line func(n int, text string, at time.Time) error, end func(Job) error) error {
	for {
		r.mu.Lock()
		j, ok := r.jobs[id]
		if !ok {
			r.mu.Unlock()
			return ErrNoJob
		}
		lines := append([]string(nil), j.Lines[min(from, len(j.Lines)):]...)
		times := append([]time.Time(nil), j.times[min(from, len(j.times)):]...)
		active := j.Active()
		final := j.snapshot()
		changed := j.changed
		r.mu.Unlock()
		for i, l := range lines {
			var at time.Time
			if i < len(times) {
				at = times[i]
			}
			if err := line(from+i, l, at); err != nil {
				return err
			}
		}
		from += len(lines)
		if !active {
			return end(final)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// Entry is one line of the audit log.
type Entry struct {
	Time       time.Time `json:"time"`
	ID         string    `json:"id"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	Args       []string  `json:"args"`
	Result     State     `json:"result"`
	ExitCode   int       `json:"exit_code"`
	DurationMS int64     `json:"duration_ms"`
}

// record appends the finished job to the audit log. A log that cannot be
// written is reported on stderr and does not fail the job, which has
// already happened.
func (r *Runner) record(j Job) {
	r.mu.Lock()
	onEnd := r.onEnd
	r.mu.Unlock()
	if onEnd != nil {
		onEnd(j)
	}
	if r.audit == "" {
		return
	}
	e := Entry{Time: j.Ended.UTC(), ID: j.ID, Actor: j.Actor, Action: j.Action, Args: j.Args, Result: j.State, ExitCode: j.ExitCode, DurationMS: j.Ended.Sub(j.Started).Milliseconds()}
	if err := Append(r.audit, e); err != nil {
		fmt.Fprintf(os.Stderr, "dbpantry: audit log %s: %v\n", r.audit, err)
	}
}

// Append writes one entry to an audit log, creating it 0600 in a 0700
// directory: it names every change and who made it.
func Append(path string, e Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600) // #nosec G304 -- the instance's own audit log
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// ReadAudit returns the audit log's entries, newest first, at most n (all
// when n <= 0). A missing log is no entries. A line that does not parse is
// skipped rather than hiding the rest.
func ReadAudit(path string, n int) ([]Entry, error) {
	f, err := os.Open(path) // #nosec G304 -- the instance's own audit log
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, sc.Err()
}

// Redact replaces the value after any flag in secret with "***", and any
// --flag=value form of it, so the audit log and the page never show one.
func Redact(args []string, secret ...string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		for _, s := range secret {
			switch {
			case out[i] == s && i+1 < len(out):
				out[i+1] = "***"
				i++
			case strings.HasPrefix(out[i], s+"="):
				out[i] = s + "=***"
			}
		}
	}
	return out
}

func newID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
