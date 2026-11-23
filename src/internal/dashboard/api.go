package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/simtabi/dbpantry/src/internal/hooks"
	"github.com/simtabi/dbpantry/src/internal/jobs"
)

// Options configure the management side of the dashboard (management-plan.md
// §3, design.md D-86). The zero value is not usable: Auth, Runner and
// Settings are required by Handler.
type Options struct {
	// AllowedHosts, when set, are the only Host names answered (DNS
	// rebinding).
	AllowedHosts []string
	Auth         *Auth
	Runner       *jobs.Runner
	Settings     Settings
	// Env looks a key up in the instance's settings (.env), for the values a
	// typed confirmation depends on.
	Env func(string) string
	// Choices lists the engines, services, volumes and dumps an action may
	// name. Unset, Handler reads them from the snapshot and Volumes.
	Choices func(context.Context) (Choices, error)
	// Volumes lists the project's data volumes, through the backend.
	Volumes func(context.Context) ([]string, error)
	// BackupDir is where dumps are; a dump parameter names a file in it.
	BackupDir string
	// Audit is the audit log, for the Jobs page's history.
	Audit string
	// TLS is true when the server terminates TLS, so cookies are Secure.
	TLS bool
	// About fills the /about page (version, commit, engine licences).
	About AboutInfo
	// Hooks is the outbound event bus and signed webhooks (design.md D-99).
	// Nil disables /api/v1/hooks and /api/v1/events.
	Hooks *hooks.Bus
}

type ctxKey struct{}

// principal is the request's principal, set by the auth gate.
func principal(r *http.Request) Principal {
	p, _ := r.Context().Value(ctxKey{}).(Principal)
	return p
}

// public paths need no login: the stylesheets and scripts the login page
// itself uses, and the login form.
func public(path string) bool {
	return strings.HasPrefix(path, "/assets/") || path == "/favicon.ico" || path == "/login"
}

// gate decides who a request is from and whether it may proceed:
//
//   - token: a GET carrying ?token=<launch token> starts a session and is
//     redirected to the same page without it; the token then stops working.
//   - password: a page without a session is sent to /login.
//   - none: every browser gets a session on its first GET, which exists to
//     carry the CSRF token (loopback only; Settings.Validate).
//   - an Authorization: Bearer dbpantry_… header is an API token, with its
//     own scope and no cookie.
//
// Every request that changes something (any method but GET and HEAD) from a
// browser session must pass the same-origin check and carry the session's
// CSRF token, in every mode.
func gate(next http.Handler, o Options) http.Handler {
	a := o.Auth
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if public(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		p, ok := a.identify(r)
		if !ok && r.Header.Get("Authorization") != "" {
			// A token that is wrong, expired or revoked is refused, never
			// treated as a browser without a session.
			apiError(w, http.StatusUnauthorized, "the API token is not valid: it is unknown, expired or revoked")
			return
		}
		if !ok && r.Method == http.MethodGet {
			if t := r.URL.Query().Get("token"); t != "" && a.exchange(t) {
				if _, err := a.newSession(w, o.TLS); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				q := r.URL.Query()
				q.Del("token")
				u := *r.URL
				u.RawQuery = q.Encode()
				http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
				return
			}
			if a.Mode() == AuthNone {
				np, err := a.newSession(w, o.TLS)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				p, ok = np, true
			}
		}
		if !ok {
			unauthorized(w, r, a.Mode())
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && p.Session {
			if !sameOrigin(r) {
				apiError(w, http.StatusForbidden, "cross-site request refused")
				return
			}
			if !csrfOK(r, p) {
				apiError(w, http.StatusForbidden, "missing or wrong CSRF token")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

// unauthorized answers a request with no principal: JSON for the API, the
// login page in password mode, and a page saying where the link is in token
// mode.
func unauthorized(w http.ResponseWriter, r *http.Request, mode string) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		apiError(w, http.StatusUnauthorized, "not signed in: open the dashboard's link, sign in, or send an API token")
		return
	}
	if mode == AuthPassword && r.Method == http.MethodGet {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, signedOutPage)
}

// signedOutPage is what a browser without a session sees in token mode.
const signedOutPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in · dbpantry</title><link rel="icon" type="image/svg+xml" href="/assets/favicons/favicon.svg"><link rel="stylesheet" href="/assets/css/tabler.min.css"></head>
<body class="d-flex flex-column"><div class="page page-center"><div class="container container-tight py-4"><div class="card card-md"><div class="card-body">
<h2 class="h2 text-center mb-3">Open the dashboard's link</h2>
<p class="text-secondary">This dashboard signs you in with the one-time link <code>dbpantry dashboard</code> printed when it started. Open that link, or restart the dashboard for a new one.</p>
</div></div></div></div></body></html>`

func apiError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// actionView is an action as the API lists it: what it needs and whether
// this principal may run it now.
type actionView struct {
	Action
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
	Confirm bool   `json:"confirm"`
	// Command is the command line it runs, parameters as placeholders.
	Command string `json:"command"`
}

// views lists every action for p, each marked allowed or not with the
// reason: its scope, destroy being off, or the one change already running
// (for an exclusive action).
func views(o Options, p Principal) []actionView {
	running, busy := o.Runner.Running()
	out := make([]actionView, 0, len(Actions))
	for _, a := range Actions {
		v := actionView{Action: a, Allowed: true, Confirm: a.Destructive || o.Settings.Confirm == ConfirmAll && a.Scope > ScopeRead, Command: commandLine(a)}
		switch {
		case p.Scope < a.Scope:
			v.Allowed, v.Reason = false, "needs the "+a.Scope.String()+" scope"
		case a.Destroy && !o.Settings.AllowDestroy:
			v.Allowed, v.Reason = false, "deleting volumes is off (allow_destroy in dashboard.json)"
		case busy && a.Scope > ScopeRead:
			v.Allowed, v.Reason = false, "job "+running.ID+" ("+running.Action+") is running"
		}
		out = append(out, v)
	}
	return out
}

type jobRequest struct {
	Action  string            `json:"action"`
	Params  map[string]string `json:"params"`
	Confirm string            `json:"confirm"`
}

// startJob is POST /api/v1/jobs. In order: the action exists, the principal's
// scope covers it, destroy is enabled when it deletes volumes, its
// parameters validate against the stack, and a destructive action carries
// its typed confirmation. Only then does it start, and the CLI checks the
// confirmation again.
func startJob(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		var req jobRequest
		dec := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			apiError(w, http.StatusBadRequest, "the body must be {\"action\", \"params\", \"confirm\"}: "+err.Error())
			return
		}
		a, ok := actionByName(req.Action)
		if !ok {
			apiError(w, http.StatusNotFound, "no action named "+strconv.Quote(req.Action))
			return
		}
		if p.Scope < a.Scope {
			apiError(w, http.StatusForbidden, a.Name+" needs the "+a.Scope.String()+" scope; this request has "+p.Scope.String())
			return
		}
		if a.Destroy && !o.Settings.AllowDestroy {
			apiError(w, http.StatusForbidden, "deleting data volumes is off: set allow_destroy in dashboard.json (or MDB_DASHBOARD_ALLOW_DESTROY=true) to enable it")
			return
		}
		var c Choices
		if o.Choices != nil {
			var err error
			if c, err = o.Choices(r.Context()); err != nil {
				apiError(w, http.StatusServiceUnavailable, "the stack could not be read: "+err.Error())
				return
			}
		}
		v, err := a.validate(req.Params, c)
		if err != nil {
			apiError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		for _, prm := range a.Params {
			if prm.Kind == KindDump && v[prm.Name] != "" {
				v[prm.Name] = filepath.Join(o.BackupDir, v[prm.Name])
			}
		}
		args := a.Command(v)
		if a.Destructive {
			want := a.ConfirmValue(v, o.Env)
			if req.Confirm != want {
				apiError(w, http.StatusUnprocessableEntity, a.Name+" needs its typed confirmation: "+a.ConfirmHint)
				return
			}
			args = append(args, "--confirm", want)
		}
		shown := jobs.Redact(args, a.Secret...)
		j, err := o.Runner.Start(a.Name, p.Actor, a.Scope > ScopeRead, args, shown)
		var busy jobs.ErrBusy
		if errors.As(err, &busy) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": busy.Error(), "job": busy.Job})
			return
		}
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		emitJob(o, hooks.JobStarted, j, p.Actor)
		w.Header().Set("Location", "/api/v1/jobs/"+j.ID)
		writeJSON(w, http.StatusAccepted, j)
	}
}

// events is GET /api/v1/jobs/{id}/events: the job as server-sent events,
// one "line" event per output line (its id is the line's number, so a
// reconnecting EventSource resumes with Last-Event-ID) and one "end" event
// with the finished job.
func events(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			apiError(w, http.StatusInternalServerError, "streaming is not supported")
			return
		}
		id := r.PathValue("id")
		if _, err := o.Runner.Get(id); err != nil {
			apiError(w, http.StatusNotFound, err.Error())
			return
		}
		from := 0
		if last := r.Header.Get("Last-Event-ID"); last != "" {
			if n, err := strconv.Atoi(last); err == nil && n >= 0 {
				from = n + 1
			}
		}
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		fl.Flush()
		err := o.Runner.Watch(r.Context(), id, from, func(n int, line string) error {
			b, _ := json.Marshal(sanitizeLine(line))
			if _, err := fmt.Fprintf(w, "id: %d\nevent: line\ndata: %s\n\n", n, b); err != nil {
				return err
			}
			fl.Flush()
			return nil
		}, func(j jobs.Job) error {
			typ := hooks.JobSucceeded
			switch j.State {
			case jobs.Failed:
				typ = hooks.JobFailed
			case jobs.Cancelled:
				typ = hooks.JobCancelled
			}
			emitJob(o, typ, j, j.Actor)
			j.Lines = nil
			b, _ := json.Marshal(j)
			_, err := fmt.Fprintf(w, "event: end\ndata: %s\n\n", b)
			fl.Flush()
			return err
		})
		_ = err // the client went away, or the job ended: either way the stream is over
	}
}

// sanitizeLine strips terminal control sequences from a command's output,
// which the page shows as text.
func sanitizeLine(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case esc:
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				esc = false
			}
		case r == 0x1b:
			esc = true
		case r == '\t' || r >= 0x20 && r != 0x7f:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// history is GET /api/v1/jobs: the jobs in memory, then the audit log's
// older entries, newest first.
func history(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		entries, err := jobs.ReadAudit(o.Audit, 200)
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobs": o.Runner.List(), "audit": entries})
	}
}

type tokenRequest struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
	Days  int    `json:"days"`
}

// tokenRoutes are GET and POST /api/v1/tokens and DELETE
// /api/v1/tokens/{id}: the same store `dbpantry api token` uses. Only an
// admin manages tokens, and a token is returned once, when it is created.
func tokenRoutes(mux *http.ServeMux, o Options) {
	store := o.Auth.tokens
	admin := func(w http.ResponseWriter, r *http.Request) bool {
		if store == nil {
			apiError(w, http.StatusNotImplemented, "this dashboard has no token store")
			return false
		}
		if principal(r).Scope < ScopeAdmin {
			apiError(w, http.StatusForbidden, "managing API tokens needs the admin scope")
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/v1/tokens", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		ts, err := store.List()
		if err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, ts)
	})
	mux.HandleFunc("POST /api/v1/tokens", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		var req tokenRequest
		dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			apiError(w, http.StatusBadRequest, "the body must be {\"name\", \"scope\", \"days\"}: "+err.Error())
			return
		}
		scope, err := ParseScope(req.Scope)
		if err != nil {
			apiError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if req.Days < 1 || req.Days > 366 {
			apiError(w, http.StatusUnprocessableEntity, "days must be from 1 to 366")
			return
		}
		plain, t, err := store.Create(req.Name, scope, time.Duration(req.Days)*24*time.Hour, time.Now())
		if err != nil {
			apiError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		auditToken(o, r, "token.create", t)
		writeJSON(w, http.StatusCreated, map[string]any{"token": plain, "info": t})
	})
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		id := r.PathValue("id")
		if err := store.Revoke(id); err != nil {
			apiError(w, http.StatusNotFound, err.Error())
			return
		}
		auditToken(o, r, "token.revoke", Token{ID: id})
		w.WriteHeader(http.StatusNoContent)
	})
}

// auditToken records a token change in the audit log, as every change is.
func auditToken(o Options, r *http.Request, action string, t Token) {
	if o.Audit == "" {
		return
	}
	args := []string{"api", "token", strings.TrimPrefix(action, "token."), t.ID}
	if t.Name != "" {
		args = append(args, t.Name, "--scope", t.Scope.String())
	}
	_ = jobs.Append(o.Audit, jobs.Entry{Time: time.Now().UTC(), Actor: principal(r).Actor, Action: action, Args: args, Result: jobs.Succeeded})
}

// Logs streams a service's log lines to w; set by the command that serves
// the dashboard, so the page reads logs through the backend layer.
type Logs func(ctx context.Context, service, tail string, follow bool, w io.Writer) error

// logStream is GET /api/v1/services/{service}/logs?tail=N&follow=1: the
// service's log as server-sent "line" events, ended by an "end" event.
func logStream(o Options, logs Logs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok || logs == nil {
			apiError(w, http.StatusNotImplemented, "logs are not available")
			return
		}
		svc := r.PathValue("service")
		c, err := o.Choices(r.Context())
		if err != nil {
			apiError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if !patterns[KindService].MatchString(svc) || !contains(c.Services, svc) {
			apiError(w, http.StatusNotFound, "there is no service "+strconv.Quote(svc)+" in the stack")
			return
		}
		tail := r.URL.Query().Get("tail")
		if tail == "" {
			tail = "200"
		}
		if n, err := strconv.Atoi(tail); (err != nil || n < 1 || n > 10000) && tail != "all" {
			apiError(w, http.StatusUnprocessableEntity, "tail must be a number from 1 to 10000, or all")
			return
		}
		follow := r.URL.Query().Get("follow") == "1"
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		fl.Flush()
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
		defer cancel()
		sw := &sseLines{w: w, fl: fl}
		err = logs(ctx, svc, tail, follow, sw)
		sw.flush()
		end := map[string]string{"service": svc}
		if err != nil && ctx.Err() == nil {
			end["error"] = sanitizeLine(err.Error())
		}
		b, _ := json.Marshal(end)
		_, _ = fmt.Fprintf(w, "event: end\ndata: %s\n\n", b)
		fl.Flush()
	}
}

// sseLines turns a byte stream into "line" events, one per line.
type sseLines struct {
	w   io.Writer
	fl  http.Flusher
	buf []byte
	n   int
}

func (s *sseLines) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := strings.IndexByte(string(s.buf), '\n')
		if i < 0 {
			break
		}
		if err := s.send(string(s.buf[:i])); err != nil {
			return 0, err
		}
		s.buf = s.buf[i+1:]
	}
	return len(p), nil
}

func (s *sseLines) send(line string) error {
	b, _ := json.Marshal(sanitizeLine(line))
	if _, err := fmt.Fprintf(s.w, "id: %d\nevent: line\ndata: %s\n\n", s.n, b); err != nil {
		return err
	}
	s.n++
	s.fl.Flush()
	return nil
}

func (s *sseLines) flush() {
	if len(s.buf) > 0 {
		_ = s.send(string(s.buf))
		s.buf = nil
	}
}

// login is GET and POST /login (password mode).
func login(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := o.Auth
		if a.Mode() != AuthPassword {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		next := r.URL.Query().Get("next")
		if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
			next = "/"
		}
		failed := false
		if r.Method == http.MethodPost {
			// No session yet, so no CSRF token: the same-origin check is what
			// stops another site posting a guessed password here.
			if !sameOrigin(r) {
				apiError(w, http.StatusForbidden, "cross-site request refused")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if a.checkPassword(r.PostFormValue("password")) {
				if _, err := a.newSession(w, o.TLS); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				http.Redirect(w, r, next, http.StatusSeeOther)
				return
			}
			failed = true
			time.Sleep(500 * time.Millisecond) // a guessing script pays for each try
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if failed {
			w.WriteHeader(http.StatusUnauthorized)
		}
		_ = loginTemplate.Execute(w, map[string]any{"Next": next, "Failed": failed})
	}
}

// logout is POST /logout.
func logout(o Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		o.Auth.logout(w, r)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}
