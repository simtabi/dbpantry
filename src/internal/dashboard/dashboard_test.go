package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simtabi/dbpantry/src/internal/endpoint"
)

func fixture(context.Context) (Snapshot, error) {
	return Snapshot{
		Generated: time.Now(), Version: "test", Backend: "compose",
		Endpoints: endpoint.Document{Publish: "none", Profiles: []string{"pg", "ui"}, Endpoints: []endpoint.Endpoint{
			{Engine: "pg", Title: "PostgreSQL", Enabled: true, TLSAlways: true, Direct: endpoint.Access{Network: endpoint.Address{Host: "pg", Port: 5432}}},
		}},
		Services: []Service{{Name: "pg", Health: "healthy"}, {Name: "caddy", Health: "none"}},
		Projects: []Project{{Engine: "pg", Name: "settings", Owner: "settings_user", Readonly: "settings_user_readonly", OwnerSecret: "secrets/pg_settings_user_password.txt"}},
		Backups:  []Backup{{File: "pg_settings_20261117T000000Z.dump", Engine: "pg", Size: 2048, Modified: time.Now()}},
	}, nil
}

func serve(t *testing.T, collect Collect, hosts []string, path, host string) *http.Response {
	t.Helper()
	h, err := Handler(collect, hosts)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func body(t *testing.T, r *http.Response) string {
	t.Helper()
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDashboardServes(t *testing.T) {
	r := serve(t, fixture, nil, "/", "")
	page := body(t, r)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d", r.StatusCode)
	}
	for _, want := range []string{
		`<link rel="icon" type="image/svg+xml" href="/assets/favicons/favicon.svg">`,
		`/assets/css/tabler.min.css`,
		`PostgreSQL`, `settings_user_readonly`, `pg_settings_20261117T000000Z.dump`,
		`status-green`, `2 <span class="text-secondary fs-3">/ 2</span>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("no restrictive CSP: %q", csp)
	}

	part := body(t, serve(t, fixture, nil, "/partials/body", ""))
	if strings.Contains(part, "<html") || !strings.Contains(part, "settings_user") {
		t.Error("the partial is the body alone, with the data")
	}

	var snap Snapshot
	if err := json.Unmarshal([]byte(body(t, serve(t, fixture, nil, "/api/status", ""))), &snap); err != nil || len(snap.Services) != 2 {
		t.Errorf("/api/status is the snapshot as JSON: %v %+v", err, snap.Services)
	}
}

// The Go web UI rule: an embedded SVG favicon, linked from every page,
// served as image/svg+xml.
func TestFaviconIsServedAsSVG(t *testing.T) {
	r := serve(t, fixture, nil, "/assets/favicons/favicon.svg", "")
	b := body(t, r)
	if r.StatusCode != http.StatusOK || r.Header.Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(b, "<svg") {
		t.Errorf("favicon: %d %q", r.StatusCode, r.Header.Get("Content-Type"))
	}
	if r := serve(t, fixture, nil, "/assets/css/tabler.min.css", ""); r.StatusCode != http.StatusOK || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/css") {
		t.Errorf("stylesheet: %d %q", r.StatusCode, r.Header.Get("Content-Type"))
	}
}

func TestOtherHostsAreRefused(t *testing.T) {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	if r := serve(t, fixture, hosts, "/", "evil.example:8484"); r.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("a rebinding Host was answered: %d", r.StatusCode)
	}
	for _, h := range []string{"localhost:8484", "127.0.0.1:8484", "[::1]:8484"} {
		if r := serve(t, fixture, hosts, "/", h); r.StatusCode != http.StatusOK {
			t.Errorf("Host %s refused: %d", h, r.StatusCode)
		}
	}
}

func TestUnavailableRuntimeIsNotAnEmptyStack(t *testing.T) {
	down := func(ctx context.Context) (Snapshot, error) {
		s, _ := fixture(ctx)
		s.Services, s.ServicesErr = nil, "permission denied while trying to connect to the docker API"
		return s, nil
	}
	page := body(t, serve(t, down, nil, "/", ""))
	if !strings.Contains(page, "Services are unavailable") || strings.Contains(page, "Nothing is running") {
		t.Error("an unreachable runtime must read as unavailable, never as nothing running")
	}
	broken := func(context.Context) (Snapshot, error) { return Snapshot{}, errors.New("no manifest") }
	if r := serve(t, broken, nil, "/", ""); r.StatusCode != http.StatusInternalServerError {
		t.Errorf("a failed read is an error page: %d", r.StatusCode)
	}
}

// Every asset file in this package's source sits in resources/assets/<kind>/
// (web-assets rule), and the vendored stylesheet is the one its README names.
// The walk reads the directory on disk, not the embedded files: a stray file
// is never embedded, so an embed walk could not see it.
func TestAssetsLayoutAndVendoredStylesheet(t *testing.T) {
	kinds := map[string]bool{"js": true, "css": true, "fonts": true, "images": true, "media": true, "favicons": true}
	assetExt := map[string]bool{".js": true, ".css": true, ".svg": true, ".png": true, ".jpg": true, ".ico": true, ".woff": true, ".woff2": true, ".webp": true, ".gif": true}
	n := 0
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !assetExt[filepath.Ext(p)] {
			return err
		}
		n++
		parts := strings.Split(filepath.ToSlash(p), "/")
		if len(parts) < 4 || parts[0] != "resources" || parts[1] != "assets" || !kinds[parts[2]] {
			t.Errorf("%s is not under resources/assets/<kind>/", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 5 {
		t.Fatalf("inspected %d asset files; the walk found too few to mean anything", n)
	}
	b, err := os.ReadFile(filepath.Join("resources", "assets", "css", "tabler.min.css")) // #nosec G304 -- a fixed path in this package
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	readme, err := os.ReadFile(filepath.Join("resources", "assets", "css", "README.md")) // #nosec G304 -- a fixed path in this package
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), hex.EncodeToString(sum[:])) || !strings.Contains(string(b[:200]), "Tabler v1.7.5") {
		t.Error("tabler.min.css is not the Tabler 1.7.5 file README.md records")
	}
}

func TestScanProjectsAndBackups(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{
		"pg_settings_user_password.txt", "pg_settings_user_readonly_password.txt",
		"mysql_my_app_user_password.txt", "pgadmin_password.txt", "pg_password.txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := ScanProjects(dir, []string{"pg", "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Engine != "mysql" || ps[0].Name != "my_app" || ps[0].ReadonlySecret != "" ||
		ps[1].Name != "settings" || ps[1].ReadonlySecret == "" {
		t.Errorf("projects: %+v", ps)
	}
	if none, err := ScanProjects(filepath.Join(dir, "absent"), nil); err != nil || none != nil {
		t.Errorf("no secrets directory is no projects: %v %v", none, err)
	}

	bdir := t.TempDir()
	for i, f := range []string{"pg_a_1.dump", "mysql_b_2.sql.gz", "env-before-reorg.bak"} {
		p := filepath.Join(bdir, f)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	bs, err := ScanBackups(bdir, []string{"pg", "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 || bs[0].File != "mysql_b_2.sql.gz" {
		t.Errorf("backups, newest first, dumps only: %+v", bs)
	}
}
