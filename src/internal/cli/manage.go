package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/simtabi/dbpantry/src/internal/config"
	"github.com/simtabi/dbpantry/src/internal/dashboard"
	"github.com/simtabi/dbpantry/src/internal/engine"
	"github.com/simtabi/dbpantry/src/internal/hooks"
	"github.com/simtabi/dbpantry/src/internal/jobs"
)

// dashboardFlags are the dashboard's command-line settings, the last of its
// layers (dashboard.Settings): each one set overrides the files and .env.
type dashboardFlags struct {
	listen       string
	auth         string
	allowDestroy bool
	tlsCert      string
	tlsKey       string
}

// managePaths are where the dashboard keeps its state in the instance home.
type managePaths struct {
	settings []string // the kit's dashboard.json, then the instance's
	password string   // secrets/dashboard_password.txt
	tokens   string   // api-tokens.json
	audit    string   // audit/dashboard.jsonl
}

func pathsFor(root, home string) managePaths {
	return managePaths{
		settings: []string{filepath.Join(root, "config", "dashboard.json"), filepath.Join(home, "dashboard.json")},
		password: filepath.Join(home, "secrets", "dashboard_password.txt"),
		tokens:   filepath.Join(home, "api-tokens.json"),
		audit:    filepath.Join(home, "audit", "dashboard.jsonl"),
	}
}

// dashboardSettings layers the dashboard's settings: the shipped
// dashboard.json, the instance's, MDB_DASHBOARD_* in .env, then the flags
// the command was given (refactor plan §8.8).
func (a *app) dashboardSettings(root string, s *config.Settings, f dashboardFlags, changed func(string) bool) (dashboard.Settings, error) {
	p := pathsFor(root, a.home(root))
	ds, err := dashboard.LoadSettings(p.settings, s.Effective)
	if err != nil {
		return ds, err
	}
	if changed("listen") {
		if _, _, err := net.SplitHostPort(f.listen); err != nil {
			return ds, fmt.Errorf("--listen %q: %w", f.listen, err)
		}
		ds.Listen = f.listen
	}
	if changed("auth") {
		ds.Auth = f.auth
	}
	if changed("allow-destroy") {
		ds.AllowDestroy = f.allowDestroy
	}
	return ds, ds.Validate(f.tlsCert != "")
}

// dashboardPassword reads the password-mode password, generating one the
// first time (0600 in the 0700 secrets directory, as init's secrets are).
// It returns whether it generated it, so the command can say where it is.
func dashboardPassword(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the instance's own secret file
	if err == nil && strings.TrimSpace(string(b)) != "" {
		return b, false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, false, err
	}
	pw := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(path, []byte(pw+"\n"), 0o600); err != nil {
		return nil, false, err
	}
	return []byte(pw), true, nil
}

// exitStatus is a job's command's exit status, as jobs.ExitCoder reads it.
type exitStatus int

func (e exitStatus) Error() string { return "exit status " + strconv.Itoa(int(e)) }

// ExitCode is the status.
func (e exitStatus) ExitCode() int { return int(e) }

// jobExec runs a dashboard job as this binary's own command tree, in
// process: the job is the command a user would type, parsed and run by the
// same code, against the same root and home. It never prompts (--no-input):
// the dashboard has collected the typed confirmation already and passes it
// as --confirm, which the command checks again. --pretty draws the same
// step tree, progress bars and ETA the CLI shows on a terminal (D-89), so
// the job drawer is not a BuildKit wall; --no-color keeps the stream plain
// text for the SSE log.
func (a *app) jobExec(root, home string) jobs.Exec {
	return func(ctx context.Context, args []string, out io.Writer) error {
		argv := jobArgv(root, home, args)
		code := Execute(ContextWithJobKill(ctx), argv, out, out, a.environ)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if code != ExitOK {
			return exitStatus(code)
		}
		return nil
	}
}

// jobArgv is the flag prefix every dashboard job runs with: --pretty so the
// drawer gets the tree / progress / ETA view, never a BuildKit wall, and
// --events so it renders steps from run snapshots rather than parsing text.
func jobArgv(root, home string, args []string) []string {
	return append([]string{"--root", root, "--home", home, "--pretty", "--events", "--no-input", "--no-color"}, args...)
}

// serviceLogs streams a service's log through the backend layer, as `dbpantry
// logs` does. The process is ended when the page goes away.
func (a *app) serviceLogs(root string, env []string) dashboard.Logs {
	return func(ctx context.Context, service, tail string, follow bool, w io.Writer) error {
		args := []string{"scripts/runtime", "logs", "--tail=" + tail}
		if follow {
			args = append(args, "-f")
		}
		st := script(append(args, service)...)
		st.Interruptible = true
		return a.runStepTo(ctx, root, env, st, w, w)
	}
}

// volumesScript lists the project's data volumes through the backend layer.
const volumesScript = `. scripts/backend-lib.sh && mdb_be_volumes`

func (a *app) volumes(root string, env []string) func(context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		rows, err := a.records(ctx, root, env, volumesScript)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r[0])
		}
		return out, nil
	}
}

// pitrStatuses is each PITR-capable engine's on/off state from its env
// (audit B8). Enabling needs a restart, so the page does not run pitr info.
func pitrStatuses(m engine.Manifest, s *config.Settings) []dashboard.PITRStatus {
	var out []dashboard.PITRStatus
	for _, e := range m.Engines {
		if !e.PITRCapable {
			continue
		}
		key := "MDB_" + strings.ToUpper(strings.ReplaceAll(e.Name, "-", "_")) + "_PITR"
		on := envTruthy(s.Effective(key))
		st := dashboard.PITRStatus{Engine: e.Name, Capable: true, Enabled: on}
		switch {
		case on && e.PITRRepoPath != "":
			st.Summary = "on · archive at " + e.PITRRepoPath
		case on:
			st.Summary = "on"
		default:
			st.Summary = "off — set " + key + "=true and restart"
		}
		out = append(out, st)
	}
	return out
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// lastVerify is the most recent backup.verify entry in the audit log.
func lastVerify(home string) *dashboard.VerifyResult {
	entries, err := jobs.ReadAudit(filepath.Join(home, "audit", "dashboard.jsonl"), 0)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.Action == "backup.verify" {
			return &dashboard.VerifyResult{At: e.Time, Result: string(e.Result), Actor: e.Actor}
		}
	}
	return nil
}

// webUIs are the browser front ends the ui profile serves behind Caddy, for
// the ones whose service is running (Caddyfile.tmpl: adminer., pma.,
// pgadmin. under MDB_UI_DOMAIN).
func webUIs(s *config.Settings, up func(string) bool) []dashboard.WebUI {
	domain := s.Effective("MDB_UI_DOMAIN")
	if domain == "" {
		domain = "db.localhost"
	}
	port := ""
	if p := s.Effective("MDB_CADDY_HTTPS_PORT"); p != "" && p != "443" {
		port = ":" + p
	}
	var out []dashboard.WebUI
	for _, u := range []struct{ service, name, host string }{
		{"adminer", "Adminer", "adminer"},
		{"phpmyadmin", "phpMyAdmin", "pma"},
		{"pgadmin", "pgAdmin", "pgadmin"},
	} {
		if up(u.service) {
			out = append(out, dashboard.WebUI{Name: u.name, URL: fmt.Sprintf("https://%s.%s%s/", u.host, domain, port)})
		}
	}
	return out
}

// newRunner is the job runner every dashboard action goes through.
func (a *app) newRunner(root string) *jobs.Runner {
	home := a.home(root)
	return jobs.New(a.jobExec(root, home), pathsFor(root, home).audit, 50)
}

// hooksBus is the outbound signed-webhook / event bus (design.md D-99).
func (a *app) hooksBus(home string) *hooks.Bus {
	b, err := hooks.New(filepath.Join(home, "webhooks.json"), 200)
	if err != nil {
		fmt.Fprintf(a.stderr, "dbpantry: webhooks store: %v\n", err)
		b, _ = hooks.New("", 200)
	}
	return b
}

// sessionTTL is a browser session's lifetime from the settings.
func sessionTTL(ds dashboard.Settings) time.Duration {
	return time.Duration(ds.SessionHours) * time.Hour
}
