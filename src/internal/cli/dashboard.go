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
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/simtabi/dbpantry/src/internal/dashboard"
	"github.com/simtabi/dbpantry/src/internal/endpoint"
)

// servicesScript lists the running services and their health through the
// backend layer (check 52), one "name<TAB>health" line each.
const servicesScript = `. scripts/backend-lib.sh && for s in $(mdb_be_running); do printf '%s\t%s\n' "$s" "$(mdb_svc_health "$s")"; done`

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
			host, _, err := net.SplitHostPort(listen)
			if err != nil {
				return usagef("--listen %q: %v", listen, err)
			}
			var allowed []string
			if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
				allowed = []string{"localhost", "127.0.0.1", "::1"}
			} else {
				a.log.Warn("the dashboard has no login and is listening beyond this machine", "listen", listen)
			}
			h, err := dashboard.Handler(func(ctx context.Context) (dashboard.Snapshot, error) {
				return a.snapshot(ctx, root, env, backupDir)
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
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:8484", "the address to serve on")
	return cmd
}

// snapshot reads the stack for one page. A runtime that does not answer is
// reported as unavailable, never as nothing running.
func (a *app) snapshot(ctx context.Context, root string, env []string, backupDir string) (dashboard.Snapshot, error) {
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
	snap := dashboard.Snapshot{Generated: time.Now(), Version: version, Backend: backend, Endpoints: doc, BackupDir: shown}

	var names []string
	for _, e := range m.Engines {
		names = append(names, e.Name)
	}
	if snap.Projects, err = dashboard.ScanProjects(filepath.Join(root, "secrets"), names); err != nil {
		return dashboard.Snapshot{}, err
	}
	if snap.Backups, err = dashboard.ScanBackups(backupDir, names); err != nil {
		snap.BackupsErr = err.Error()
	}

	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(sctx, "bash", "-c", servicesScript) // #nosec G204 -- a fixed expression of this package
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = root, env, &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		snap.ServicesErr = sanitize(msg)
		return snap, nil
	}
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		name, health, ok := strings.Cut(l, "\t")
		if !ok || name == "" {
			continue
		}
		snap.Services = append(snap.Services, dashboard.Service{Name: name, Health: health})
	}
	return snap, nil
}
