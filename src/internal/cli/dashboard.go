package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/dashboard"
	"github.com/simtabi/dbpantry/src/internal/endpoint"
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
	containersScript = `. scripts/runtime-lib.sh && ids="$(mdb_compose --profile '*' ps -a -q 2>/dev/null)" && { [ -z "$ids" ] || mdb_ctr inspect -f '{{index .Config.Labels "com.docker.compose.service"}}{{"\t"}}{{.State.Status}}{{"\t"}}{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}{{"\t"}}{{.Config.Image}}{{"\t"}}{{.State.StartedAt}}' $ids; }`
	// fixtureScript is containersScript for the fixture, whose compose
	// project is $1: the runtime's own label filter, since the fixture is
	// rendered from other compose files than the stack.
	fixtureScript = `. scripts/runtime-lib.sh && ids="$(mdb_ctr ps -a -q --filter "label=com.docker.compose.project=$1" 2>/dev/null)" && { [ -z "$ids" ] || mdb_ctr inspect -f '{{index .Config.Labels "com.docker.compose.service"}}{{"\t"}}{{.State.Status}}{{"\t"}}{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}{{"\t"}}{{.Config.Image}}{{"\t"}}{{.State.StartedAt}}' $ids; }`
	// imagesScript lists the stack's locally built images: ref, size, age.
	imagesScript = `. scripts/env-lib.sh && . scripts/runtime-lib.sh && p="$(mdb_image_prefix)/dbpantry-" && mdb_ctr images --format '{{.Repository}}:{{.Tag}}{{"\t"}}{{.Size}}{{"\t"}}{{.CreatedSince}}' | grep -F "$p" || true`
)

func (a *app) dashboardCmd() *cobra.Command {
	var listen string
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Serve a read-only web dashboard of the stack: services, engines, projects, backups",
		Long: `Serve a web page describing the stack, refreshed every five seconds: which
services run and their health, where each engine listens, the projects
make new-project provisioned (password files named, never read) and the
backups taken. It changes nothing.

It listens on 127.0.0.1:8484 by default and answers only requests addressed
to localhost, so another web page cannot read it through DNS rebinding.
Listening on another address prints a warning: the page names every
project and service, and it has no login.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return a.serveDashboard(c, listen)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:8484", "the address to serve on")
	return cmd
}

// serveDashboard serves the dashboard on listen until the command is
// interrupted. `start` runs it too.
func (a *app) serveDashboard(c *cobra.Command, listen string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return usagef("--listen %q: %v", listen, err)
	}
	root, err := a.root()
	if err != nil {
		return err
	}
	env, err := a.scriptEnv(c.Context(), root)
	if err != nil {
		return err
	}
	backupDir, err := a.libValue(c.Context(), root, `. scripts/env-lib.sh && mdb_backup_dir "$PWD" "${MDB_ENV_FILE:-$PWD/.env}"`)
	if err != nil {
		return err
	}
	var allowed []string
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		allowed = []string{"localhost", "127.0.0.1", "::1"}
	} else {
		a.log.Warn("the dashboard has no login and is listening beyond this machine", "listen", listen)
	}
	network, err := a.libValue(c.Context(), root, ". scripts/runtime-lib.sh && mdb_project_name")
	if err != nil {
		return err
	}
	h, err := dashboard.Handler(dashboard.Sources{
		Snapshot: func(ctx context.Context) (dashboard.Snapshot, error) {
			return a.snapshot(ctx, root, env, backupDir, network+"_net")
		},
		Project: func(ctx context.Context, eng, name string) (project.Provisioned, error) {
			return a.showProject(ctx, root, env, eng, name)
		},
	}, allowed)
	if err != nil {
		return err
	}
	if a.dryRun {
		_, err := fmt.Fprintf(a.stdout, "would serve the dashboard on http://%s/\n", listen)
		return err
	}
	ln, err := (&net.ListenConfig{}).Listen(c.Context(), "tcp", listen)
	if err != nil {
		return err
	}
	st := a.style()
	_, _ = fmt.Fprintf(a.stdout, "\n %s %s  %s\n %s\n\n",
		st.Green("●"), st.Bold("dbpantry dashboard"), st.Cyan("http://"+ln.Addr().String()+"/"),
		st.Dim("read-only · refreshes every 5s · Ctrl-C to stop"))
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-c.Context().Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown) //nolint:contextcheck // the command's context is already done
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
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
	if snap.Projects, err = dashboard.ScanProjects(filepath.Join(root, "secrets"), names); err != nil {
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
	var rowsErr, fixtureErr, imgsErr error
	wg.Add(2)
	go func() { defer wg.Done(); rows, rowsErr = a.records(ctx, root, env, script) }()
	go func() { defer wg.Done(); imgs, imgsErr = a.records(ctx, root, env, imagesScript) }()
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
	snap.Services = services(rows)
	if fixtureErr == nil {
		snap.Fixture = services(fixtureRows)
		if len(snap.Fixture) > 0 {
			snap.FixtureProject = fixtureProject
		}
	}
	if imgsErr != nil {
		snap.ImagesErr = imgsErr.Error()
	}
	for _, f := range imgs {
		if len(f) == 3 {
			snap.Images = append(snap.Images, dashboard.Image{Ref: f[0], Size: f[1], Created: f[2]})
		}
	}
	return snap, nil
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
