package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/addon"
	"github.com/simtabi/dbpantry/src/internal/config"
	"github.com/simtabi/dbpantry/src/internal/dashboard"
	"github.com/simtabi/dbpantry/src/internal/doctor"
	"github.com/simtabi/dbpantry/src/internal/endpoint"
	"github.com/simtabi/dbpantry/src/internal/engine"
	"github.com/simtabi/dbpantry/src/internal/envtype"
	"github.com/simtabi/dbpantry/src/internal/project"
)

// The stack is read by these fixed expressions, through the repository's
// own libraries, so the binary keeps no second copy of how a runtime is
// asked. Each prints one tab-separated record per line.
const (
	// servicesScript lists the running services and their health through
	// the backend layer (check 52), on every backend: name, health.
	servicesScript = `. scripts/backend-lib.sh && for s in $(mdb_be_running); do printf '%s\t%s\n' "$s" "$(mdb_svc_health "$s")"; done`
	// containersScript adds state, image and start time on Compose, by the
	// same compose ps -q and inspect runtime-lib.sh uses, which Docker,
	// Podman and nerdctl share: name, state, health, image, started.
	// stderr is kept on failure so ServicesErr names the missing runtime
	// (D-88) instead of a bare "exit status 125" from mdb_ctr's init guard.
	containersScript = `. scripts/runtime-lib.sh && ids="$(mdb_compose --profile '*' ps -a -q)" && { [ -z "$ids" ] || mdb_ctr inspect -f '{{index .Config.Labels "com.docker.compose.service"}}{{"\t"}}{{.State.Status}}{{"\t"}}{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}{{"\t"}}{{.Config.Image}}{{"\t"}}{{.State.StartedAt}}' $ids; }`
	// fixtureScript is containersScript for the fixture, whose compose
	// project is $1: the runtime's own label filter, since the fixture is
	// rendered from other compose files than the stack.
	fixtureScript = `. scripts/runtime-lib.sh && ids="$(mdb_ctr ps -a -q --filter "label=com.docker.compose.project=$1")" && { [ -z "$ids" ] || mdb_ctr inspect -f '{{index .Config.Labels "com.docker.compose.service"}}{{"\t"}}{{.State.Status}}{{"\t"}}{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}{{"\t"}}{{.Config.Image}}{{"\t"}}{{.State.StartedAt}}' $ids; }`
	// imagesScript lists every locally built dbpantry image under any
	// prefix (audit B7): ref, size, age, support label. `images --format` has
	// no label field (Docker 29.8 refuses {{.Label}} there; `ps` has it), so
	// the label comes from one `image inspect` of our images. A failing
	// runtime is an error, never an empty list (the page said "No images
	// built yet" beside eight built images, 2026-11-25).
	imagesScript = `. scripts/runtime-lib.sh && list="$(mdb_ctr images --format '{{.Repository}}:{{.Tag}}{{"\t"}}{{.Size}}{{"\t"}}{{.CreatedSince}}')" && ours="$(printf '%s\n' "$list" | awk -F'\t' 'index($1, "/dbpantry-")')" && { [ -z "$ours" ] || { labels="$(mdb_ctr image inspect --format '{{index .Config.Labels "io.simtabi.dbpantry.support"}}' $(printf '%s\n' "$ours" | cut -f1))" && paste <(printf '%s\n' "$ours") <(printf '%s\n' "$labels" | sed 's/<no value>//'); }; }`
)

func (a *app) dashboardCmd() *cobra.Command {
	var f dashboardFlags
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Serve the web dashboard: see the stack and manage it, through the same commands as the command line",
		Long: `Serve the web dashboard: which services run and their health, where each
engine listens, the projects provisioned and the backups taken, refreshed
every five seconds; and every action the command line has, run as a job
whose output streams to the page.

Every action is one dbpantry command, run by this binary with the same
code as on the command line, so the dashboard can do nothing the command
line cannot. One change runs at a time. Destructive actions need the same
typed confirmation as on the command line, and deleting data volumes is off
unless allow_destroy is set. Every change is appended to
audit/dashboard.jsonl in the instance home.

Signing in (auth, in dashboard.json, MDB_DASHBOARD_AUTH or --auth):
  token     the default: the command prints a one-time link that starts a session
  password  a sign-in page asks for secrets/dashboard_password.txt (generated once)
  none      no sign-in; loopback only

It listens on 127.0.0.1:8484 by default and answers only requests addressed
to localhost (DNS rebinding). Any other address needs --tls-cert and
--tls-key, and a sign-in mode other than none. Scripts use API tokens
(dbpantry api token create).`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return a.serveDashboard(c, f)
		},
	}
	dashboardFlagsOn(cmd, &f)
	return cmd
}

// dashboardFlagsOn declares the dashboard's flags on the commands that
// serve it (dashboard, start).
func dashboardFlagsOn(cmd *cobra.Command, f *dashboardFlags) {
	cmd.Flags().StringVar(&f.listen, "listen", dashboard.Defaults.Listen, "the address the dashboard serves on (default: dashboard.json's listen)")
	cmd.Flags().StringVar(&f.auth, "auth", dashboard.Defaults.Auth, "how a browser signs in: token, password or none (default: dashboard.json's auth)")
	cmd.Flags().BoolVar(&f.allowDestroy, "allow-destroy", false, "allow deleting data volumes from the dashboard (default: dashboard.json's allow_destroy)")
	cmd.Flags().StringVar(&f.tlsCert, "tls-cert", "", "serve HTTPS with this certificate (needed beyond loopback)")
	cmd.Flags().StringVar(&f.tlsKey, "tls-key", "", "the key of --tls-cert")
	cmd.MarkFlagsRequiredTogether("tls-cert", "tls-key")
}

// serveDashboard serves the dashboard until the command is interrupted.
// `start` runs it too.
func (a *app) serveDashboard(c *cobra.Command, f dashboardFlags) error {
	root, err := a.root()
	if err != nil {
		return err
	}
	settings, err := a.settings(root)
	if err != nil {
		return err
	}
	ds, err := a.dashboardSettings(root, settings, f, c.Flags().Changed)
	if err != nil {
		return usagef("dashboard: %v", err)
	}
	listen, err := dashboard.ParseListen(ds.Listen)
	if err != nil {
		return usagef("listen %q: %v", ds.Listen, err)
	}
	env, err := a.scriptEnv(c.Context(), root)
	if err != nil {
		return err
	}
	backupDir, err := a.libValue(c.Context(), root, `. scripts/env-lib.sh && mdb_backup_dir "$MDB_HOME" "${MDB_ENV_FILE:-$MDB_HOME/.env}"`)
	if err != nil {
		return err
	}
	var allowed []string
	if listen.Network == "tcp" && listen.Local {
		allowed = []string{"localhost", "127.0.0.1", "::1"}
	}
	network, err := a.libValue(c.Context(), root, ". scripts/runtime-lib.sh && mdb_project_name")
	if err != nil {
		return err
	}
	home := a.home(root)
	paths := pathsFor(root, home)
	var password []byte
	if ds.Auth == dashboard.AuthPassword {
		var made bool
		if password, made, err = dashboardPassword(paths.password); err != nil {
			return err
		}
		if made {
			a.log.Info("generated the dashboard password", "file", paths.password)
		}
	}
	cookieTag := "unix"
	if listen.Network == "tcp" {
		if _, port, err := net.SplitHostPort(listen.Address); err == nil {
			cookieTag = port
		}
	}
	auth, err := dashboard.NewAuth(ds.Auth, password, dashboard.NewTokenStore(paths.tokens), sessionTTL(ds), "dbpantry_session_"+cookieTag)
	if err != nil {
		return usagef("dashboard: %v", err)
	}
	runner := a.newRunner(root)
	defer runner.Close()
	m, _, err := a.manifest()
	if err != nil {
		return err
	}
	aboutEngines := make([]dashboard.EngineLicense, 0, len(m.Engines))
	for _, e := range m.Engines {
		aboutEngines = append(aboutEngines, dashboard.EngineLicense{
			Name: e.Name, Title: e.Title, License: e.License,
			OSIApproved: e.OSIApproved, Note: e.LicenseNote, Publish: e.Publish,
		})
	}
	h, err := dashboard.Handler(dashboard.Sources{
		Snapshot: func(ctx context.Context) (dashboard.Snapshot, error) {
			return a.snapshot(ctx, root, env, backupDir, network+"_net")
		},
		Project: func(ctx context.Context, eng, name string) (project.Provisioned, error) {
			return a.showProject(ctx, root, env, eng, name)
		},
		Logs: a.serviceLogs(root, env),
	}, dashboard.Options{
		AllowedHosts: allowed, Auth: auth, Runner: runner, Settings: ds,
		Env: settings.Effective, Volumes: a.volumes(root, env),
		BackupDir: backupDir, Audit: paths.audit, TLS: f.tlsCert != "",
		About: dashboard.AboutInfo{Version: version, Commit: commit, Engines: aboutEngines},
		Hooks: a.hooksBus(home),
	})
	if err != nil {
		return err
	}
	scheme := "http"
	if f.tlsCert != "" {
		scheme = "https"
	}
	// Browser URL uses the listen setting, never 0.0.0.0/:: (those bind all
	// interfaces but are not a host a browser can open). Unix sockets have no
	// browser URL — scripts use curl --unix-socket.
	browse := listen.Address
	url := ""
	if listen.Network == "tcp" {
		if h, p, err := net.SplitHostPort(listen.Address); err == nil && (h == "0.0.0.0" || h == "::" || h == "") {
			browse = net.JoinHostPort("127.0.0.1", p)
		}
		url = scheme + "://" + browse + "/"
	} else {
		url = "unix://" + listen.Address
	}
	if a.dryRun {
		_, err := fmt.Fprintf(a.stdout, "would serve the dashboard on %s (auth %s)\n", url, ds.Auth)
		return err
	}
	if listen.Network == "unix" {
		_ = os.Remove(listen.Address) // stale socket from a previous run
	}
	ln, err := (&net.ListenConfig{}).Listen(c.Context(), listen.Network, listen.Address)
	if err != nil {
		return err
	}
	if listen.Network == "unix" {
		_ = os.Chmod(listen.Address, 0o660) // #nosec G302 -- local API socket; group share is intentional
	}
	note := "sign-in " + ds.Auth
	switch {
	case listen.Network == "unix":
		note = "unix socket · curl --unix-socket " + listen.Address + " http://localhost/api/v1/status · auth " + ds.Auth
	case ds.Auth == dashboard.AuthToken:
		url += "?token=" + auth.LaunchToken()
		note = "the link signs you in once · open it on localhost / 127.0.0.1 (Host check)"
	case ds.Auth == dashboard.AuthPassword:
		note = "password in " + paths.password + " · open http://" + browse + "/ on localhost"
	case ds.Auth == dashboard.AuthNone:
		note = "sign-in none · open " + url + " (Host must be localhost or 127.0.0.1)"
	}
	st := a.style()
	_, _ = fmt.Fprintf(a.stdout, "\n %s %s  %s\n %s\n\n",
		st.Green("●"), st.Bold("dbpantry dashboard"), st.Cyan(url),
		st.Dim(note+" · audit "+paths.audit+" · Ctrl-C to stop"))
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-c.Context().Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown) //nolint:contextcheck // the command's context is already done
	}()
	if f.tlsCert != "" {
		err = srv.ServeTLS(ln, f.tlsCert, f.tlsKey)
	} else {
		err = srv.Serve(ln)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if j, ok := runner.Running(); ok {
		a.log.Info("waiting for the running job to finish", "job", j.ID, "action", j.Action)
	}
	return nil
}

// snapshot reads the stack for one page. A runtime that does not answer is
// reported as unavailable, never as nothing running.
func (a *app) snapshot(ctx context.Context, root string, env []string, backupDir, network string) (dashboard.Snapshot, error) {
	m, _, err := a.manifest()
	if err != nil {
		return dashboard.Snapshot{}, err
	}
	s, err := a.settings(root)
	if err != nil {
		return dashboard.Snapshot{}, err
	}
	doc, err := endpoint.Resolve(m, s, endpoint.Options{Profiles: envValue(env, "COMPOSE_PROFILES")})
	if err != nil {
		return dashboard.Snapshot{}, err
	}
	backend := s.Effective("MDB_BACKEND")
	if backend == "" {
		backend = "compose"
	}
	shown := backupDir
	if rel, err := filepath.Rel(root, backupDir); err == nil && !strings.HasPrefix(rel, "..") {
		shown = rel + "/"
	}
	snap := dashboard.Snapshot{
		Generated: time.Now(), Version: version, Backend: backend, Root: root, Network: network,
		Endpoints: doc, BackupDir: shown, Extensions: map[string][]string{},
	}

	var names []string
	for _, e := range m.Engines {
		names = append(names, e.Name)
		key := "MDB_" + strings.ToUpper(strings.ReplaceAll(e.Name, "-", "_")) + "_INIT_EXTENSIONS"
		for _, x := range strings.Split(s.Effective(key), ",") {
			if x = strings.TrimSpace(x); x != "" {
				snap.Extensions[e.Name] = append(snap.Extensions[e.Name], x)
			}
		}
	}
	if snap.Projects, err = dashboard.ScanProjects(filepath.Join(a.home(root), "secrets"), names); err != nil {
		return dashboard.Snapshot{}, err
	}
	if snap.Backups, err = dashboard.ScanBackups(backupDir, names); err != nil {
		snap.BackupsErr = err.Error()
	}

	script := servicesScript
	if backend == "compose" {
		script = containersScript
	}
	fixtureProject := ""
	if backend == "compose" {
		if fx, _, err := fixtureEnv(m, s); err == nil {
			fixtureProject = envValue(fx, "COMPOSE_PROJECT_NAME")
		}
	}

	// The readings are independent and each runs the runtime's CLI, so they
	// run at once: a page costs the slowest of them, not their sum.
	var wg sync.WaitGroup
	var rows, fixtureRows, imgs [][]string
	var rowsErr, fixtureErr, imgsErr, listingErr error
	var listing project.Listing
	wg.Add(3)
	go func() { defer wg.Done(); rows, rowsErr = a.records(ctx, root, env, script) }()
	go func() { defer wg.Done(); imgs, imgsErr = a.records(ctx, root, env, imagesScript) }()
	go func() { defer wg.Done(); listing, listingErr = a.projectListing(ctx, root, env) }()
	if fixtureProject != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fixtureRows, fixtureErr = a.records(ctx, root, env, fixtureScript, fixtureProject)
		}()
	}
	wg.Wait()

	if rowsErr != nil {
		snap.ServicesErr = rowsErr.Error()
	}
	if listingErr != nil {
		snap.ProjectsErr = listingErr.Error()
	} else {
		snap.Unmanaged = dashboard.Reconcile(snap.Projects, listing)
	}
	snap.Services = services(rows)
	snap.WebUIs = webUIs(s, snap.Up)
	if fixtureErr == nil {
		snap.Fixture = services(fixtureRows)
		if len(snap.Fixture) > 0 {
			snap.FixtureProject = fixtureProject
		}
	}
	if imgsErr != nil {
		snap.ImagesErr = imgsErr.Error()
	}
	snap.ImagePrefix = s.Effective("MDB_IMAGE_PREFIX")
	if snap.ImagePrefix == "" {
		snap.ImagePrefix = "localhost"
	}
	for _, f := range imgs {
		if len(f) >= 3 {
			support := ""
			if len(f) >= 4 {
				support = f[3]
			}
			snap.Images = append(snap.Images, dashboard.ParseImage(f[0], f[1], f[2], support))
		}
	}
	snap.PITR = pitrStatuses(m, s)
	snap.LastVerify = lastVerify(a.home(root))
	snap.ConfigFile = s.EnvFile
	if entries, err := s.Show(filepath.Join(root, ".env.example")); err == nil {
		for _, e := range entries {
			snap.Config = append(snap.Config, dashboard.ConfigEntry{
				Key: e.Key, Value: e.Value, Origin: string(e.Origin),
				Group: e.Group, Comment: e.Comment, Secret: e.Secret,
			})
		}
	}
	snap.Addons = addonStatuses(m.Engines, s, snap)
	rep := doctor.Host(version, commit, root, a.home(root))
	rep.Backend = snap.Backend
	// Runtime details stay on `dbpantry doctor`; the page links there and
	// shows host facts without a second runtime-info storm on every refresh.
	snap.Doctor = &rep
	if envs, err := envtype.Load(envtype.RegistryPath(envtype.OperatorHome())); err == nil {
		for _, e := range envs {
			snap.Environments = append(snap.Environments, dashboard.Environment{
				Name: e.Name, Type: string(e.Type), Home: e.Home, Project: e.Project, Notes: e.Notes,
			})
		}
	}
	return snap, nil
}

func addonStatuses(engines []engine.Engine, s *config.Settings, snap dashboard.Snapshot) []addon.Status {
	profiles := addon.ParseProfiles(s.Effective("MDB_PROFILES"))
	if len(profiles) == 0 {
		profiles = addon.ParseProfiles(s.Effective("COMPOSE_PROFILES"))
	}
	var initExt, preload []string
	for _, x := range strings.Split(s.Effective("MDB_PG_INIT_EXTENSIONS"), ",") {
		if x = strings.TrimSpace(x); x != "" {
			initExt = append(initExt, x)
		}
	}
	for _, x := range strings.Split(s.Effective("MDB_PG_SHARED_PRELOAD"), ",") {
		if x = strings.TrimSpace(x); x != "" {
			preload = append(preload, x)
		}
	}
	engEnabled := map[string]bool{}
	for _, e := range engines {
		engEnabled[e.Name] = profiles.Has(e.Name)
	}
	projectExt := map[string][]string{}
	for _, p := range snap.Projects {
		if len(p.Extensions) > 0 {
			projectExt[p.Name] = p.Extensions
		}
	}
	envTruthy := func(key string) bool {
		v := strings.ToLower(strings.TrimSpace(s.Effective(key)))
		return v == "1" || v == "true" || v == "yes" || v == "on"
	}
	return addon.Evaluate(addon.Catalog(engines), profiles, initExt, preload, projectExt, engEnabled, snap.Up, envTruthy)
}

// services reads containersScript's or servicesScript's records, sorted by
// name: name and health, or name, state, health, image and start time.
func services(rows [][]string) []dashboard.Service {
	var out []dashboard.Service
	for _, f := range rows {
		svc := dashboard.Service{Name: f[0]}
		if len(f) == 2 {
			svc.Health = f[1]
		} else if len(f) >= 5 {
			svc.State, svc.Health, svc.Image = f[1], f[2], f[3]
			if t, err := time.Parse(time.RFC3339Nano, f[4]); err == nil && f[1] == "running" {
				svc.Started = t
			}
		}
		out = append(out, svc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// records runs one of the expressions above, with args as its $1…, and
// splits its output into tab-separated fields, every value sanitised (N19).
// Its error is what the expression said on stderr.
func (a *app) records(ctx context.Context, root string, env []string, expr string, args ...string) ([][]string, error) {
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	argv := append([]string{"-c", expr, "dashboard"}, args...)
	cmd := exec.CommandContext(sctx, "bash", argv...) // #nosec G204 -- a fixed expression of this package; args are a compose project name
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = root, env, &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(sanitize(msg))
	}
	var rows [][]string
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		f := strings.Split(sanitize(l), "\t")
		if f[0] != "" {
			rows = append(rows, f)
		}
	}
	return rows, nil
}

// showProject prints one existing project's connection block through
// scripts/new-project --show, which provisions nothing and generates
// nothing. A project with no password file is ErrNoProject.
func (a *app) showProject(ctx context.Context, root string, env []string, eng, name string) (project.Provisioned, error) {
	var out, errOut bytes.Buffer
	st := script("scripts/new-project", "--name", name, "--engine", eng, "--show", "--format", "json")
	if err := a.runStepTo(ctx, root, env, st, &out, &errOut); err != nil {
		if strings.Contains(errOut.String(), "no project") {
			return project.Provisioned{}, dashboard.ErrNoProject
		}
		return project.Provisioned{}, fmt.Errorf("scripts/new-project --show: %s", sanitize(strings.TrimSpace(errOut.String())))
	}
	return project.Parse(out.Bytes())
}
