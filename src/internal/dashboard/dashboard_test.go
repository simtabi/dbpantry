package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simtabi/dbpantry/src/internal/addon"
	"github.com/simtabi/dbpantry/src/internal/endpoint"
	"github.com/simtabi/dbpantry/src/internal/jobs"
	"github.com/simtabi/dbpantry/src/internal/project"
)

func fixture(context.Context) (Snapshot, error) {
	return Snapshot{
		Generated: time.Now(), Version: "test", Backend: "compose", Root: "/srv/dbpantry", Network: "mdb_net",
		Extensions: map[string][]string{"pg": {"vector"}},
		Images:     []Image{{Ref: "localhost/dbpantry-pg:17", Size: "1.2GB", Created: "2 hours ago"}},
		Endpoints: endpoint.Document{Publish: "none", Profiles: []string{"pg", "ui"}, Endpoints: []endpoint.Endpoint{
			{Engine: "pg", Title: "PostgreSQL", Enabled: true, TLSAlways: true, RotateSecrets: true, Direct: endpoint.Access{Network: endpoint.Address{Host: "pg", Port: 5432}}},
		}},
		Services: []Service{{Name: "pg", Health: "healthy", State: "running", Image: "localhost/dbpantry-pg:17", Started: time.Now().Add(-time.Hour)}, {Name: "caddy", Health: "none"}},
		Projects: []Project{{Engine: "pg", Name: "settings", Owner: "settings_user", Readonly: "settings_user_readonly", OwnerSecret: "secrets/pg_settings_user_password.txt"}}, // #nosec G101 -- a file path, not a credential
		Backups:  []Backup{{File: "pg_settings_20261117T000000Z.dump", Engine: "pg", Size: 2048, Modified: time.Now()}},
		WebUIs:   []WebUI{{Name: "Adminer", URL: "https://adminer.db.localhost/"}},
		Addons: []addon.Status{
			{Spec: addon.Spec{ID: "pooler.pg", Title: "pgBouncer", Kind: addon.KindPooler, Supported: true}, Engine: "pg", EngineEnabled: true, Enabled: true, Running: true},
			{Spec: addon.Spec{ID: "pooler.mongodb", Title: "MongoDB pooler", Kind: addon.KindPooler, Supported: false, Rationale: "drivers"}, Engine: "mongodb", EngineEnabled: true},
			{Spec: addon.Spec{ID: "ext.vector", Title: "vector", Kind: addon.KindExtension, Supported: true}, Engine: "pg", EngineEnabled: true, Enabled: true},
		},
	}, nil
}

func showFixture(_ context.Context, engine, name string) (project.Provisioned, error) {
	if engine != "pg" || name != "settings" {
		return project.Provisioned{}, ErrNoProject
	}
	return project.Provisioned{
		Engine: "pg", Title: "PostgreSQL", Database: "settings", Owner: "settings_user", Readonly: "settings_user_readonly",
		Network: "mdb_net", TLSAlways: true, Pooler: &project.Pooler{Host: "pg-pooler", Port: "6432"},
		Secrets: []project.Secret{{Role: "settings_user", File: "secrets/pg_settings_user_password.txt", Action: "existing"}},
		Block:   "DB_HOST=pg\nDB_PASSWORD=" + testPassword + "\n# a <comment>\n",
	}, nil
}

// testPassword is generated per run (testing.md: no credential literals).
var testPassword = func() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}()

func serve(t *testing.T, collect func(context.Context) (Snapshot, error), hosts []string, path, host string) *httptest.ResponseRecorder {
	t.Helper()
	return serveReq(t, collect, hosts, path, host, nil)
}

// testOptions are the management options the page tests run with: no
// sign-in, and a runner whose commands succeed without running anything.
func testOptions(t *testing.T, hosts []string) Options {
	t.Helper()
	auth, err := NewAuth(AuthNone, nil, NewTokenStore(filepath.Join(t.TempDir(), "tokens.json")), time.Hour, "s")
	if err != nil {
		t.Fatal(err)
	}
	r := jobs.New(func(context.Context, []string, io.Writer) error { return nil }, "", 10)
	t.Cleanup(r.Close)
	return Options{AllowedHosts: hosts, Auth: auth, Runner: r, Settings: Defaults}
}

func serveReq(t *testing.T, collect func(context.Context) (Snapshot, error), hosts []string, path, host string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	h, err := Handler(Sources{Snapshot: collect, Project: showFixture}, testOptions(t, hosts))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	if host != "" {
		req.Host = host
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// body is the response's body. A recorder holds it in memory, so there is
// nothing to close.
func body(_ *testing.T, r *httptest.ResponseRecorder) string { return r.Body.String() }

func TestDashboardServes(t *testing.T) {
	r := serve(t, fixture, nil, "/", "")
	page := body(t, r)
	if r.Code != http.StatusOK {
		t.Fatalf("GET / = %d", r.Code)
	}
	for _, want := range []string{
		`<link rel="icon" type="image/svg+xml" href="/assets/favicons/favicon.svg">`,
		`/assets/css/tabler.min.css`, `/assets/js/tabler.min.js`,
		`id="navbar-menu"`, `nav-item active`, `href="/projects/pg/settings"`,
		`PostgreSQL`, `2 <span class="text-secondary fs-3">of 2</span>`,
		// Health uses badges (not fixed-height .status pills) so labels do not spill.
		`bg-green-lt`, `>healthy<`,
		// CRM-style Overview (design.md D-92). Start is the stack.up button
		// (icon markup means the label is not a bare ">Start<" substring).
		`Welcome back`, `Stack funnel`, `Needs attention`, `data-action="stack.up"`, `mdb-stats-toggle`, `card card-link`,
		// Grouped nav: Stack / Data / Operate, not a flat nine-item bar.
		`>Stack</span>`, `>Data</span>`, `>Operate</span>`, `id="navbar-stack"`, `id="navbar-data"`, `id="navbar-operate"`,
		`mdb-page-history`, `data-nav-back`, `data-nav-forward`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the overview lacks %q", want)
		}
	}
	if svc := body(t, serve(t, fixture, nil, "/services", "")); !strings.Contains(svc, `data-copy="#shell-pg"`) {
		t.Error("the services page offers a copyable shell command per service")
	} else {
		if strings.Contains(svc, "running, no check") {
			t.Error("health label must not use the long 'running, no check' string that overflows Tabler .status pills")
		}
		if !strings.Contains(svc, ">no check<") || !strings.Contains(svc, "bg-azure-lt") {
			t.Error("Health=none must render as a compact azure badge 'no check'")
		}
		if !strings.Contains(svc, "mdb-cell-ref") {
			t.Error("image cells must use mdb-cell-ref so digests wrap")
		}
	}
	if csp := r.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("no restrictive CSP: %q", csp)
	}
	// Every page renders, with the layout, and lists what it is about.
	n := 0
	for path, want := range map[string]string{
		"/services": "localhost/dbpantry-pg:17", "/images": "1.2GB", "/engines": "Details and principals",
		"/engines/pg": "vector", "/projects": "settings_user_readonly", "/backups": "pg_settings_20261117T000000Z.dump",
		"/connect": "How to connect", "/operations": "Rotate passwords", "/config": "Configuration",
		"/status": "Live badge legend", "/jobs": "No jobs yet", "/services/pg/logs": `data-logs="pg"`,
		"/help": "Documentation", "/about": "MIT License",
		"/databases": "Provision wizard", "/addons": "mdb-addon-wizard", "/system": "This machine",
		"/envs": "Environment types", "/ha": "Primary",
	} {
		n++
		r := serve(t, fixture, nil, path, "")
		if b := body(t, r); r.Code != http.StatusOK || !strings.Contains(b, want) || !strings.Contains(b, `id="navbar-menu"`) {
			t.Errorf("GET %s = %d, want the layout and %q", path, r.Code, want)
		}
	}
	if logs := body(t, serve(t, fixture, nil, "/services/pg/logs", "")); !strings.Contains(logs, `mdb-log-viewer`) || !strings.Contains(logs, `data-logs-filter`) || !strings.Contains(logs, `data-logs-level`) {
		t.Error("the logs page must offer the structured viewer with filter and level chips")
	}
	if n != len(pages)-3 { // overview, project and error are checked on their own
		t.Fatalf("checked %d pages of %d", n, len(pages)-3)
	}
	if addons := body(t, serve(t, fixture, nil, "/addons?engine=pg", "")); !strings.Contains(addons, `data-engine="pg"`) || !strings.Contains(addons, `data-mode="single"`) || !strings.Contains(addons, `mdb-addon-steps`) || !strings.Contains(addons, `/assets/js/addons.js`) || !strings.Contains(addons, `mdb-addon-engine-card`) {
		t.Error("/addons wizard must expose steps, single engine filter, cards and addons.js")
	}
	if addons := body(t, serve(t, fixture, nil, "/addons", "")); !strings.Contains(addons, `data-no-refresh`) {
		t.Error("/addons must opt out of Live refresh so wizard step/selection state is not wiped every 5s")
	}
	if multi := body(t, serve(t, fixture, nil, "/addons?engines=pg,mysql", "")); !strings.Contains(multi, `data-engines="pg,mysql"`) || !strings.Contains(multi, `data-mode="multi"`) || !strings.Contains(multi, `data-addon-mode="multi"`) {
		t.Error("/addons?engines= must paint multi mode and the validated engine list")
	}
	if bad := body(t, serve(t, fixture, nil, "/addons?engines=pg,!!!", "")); !strings.Contains(bad, `data-engines="pg"`) || strings.Contains(bad, `!!!`) {
		t.Error("/addons must drop invalid engine tokens from ?engines=")
	}
	st := body(t, serve(t, fixture, nil, "/status", ""))
	if !strings.Contains(st, "pgBouncer") || strings.Contains(st, "pooler.mongodb") || strings.Contains(st, "MongoDB pooler") {
		t.Error("/status Add-ons card must list supported+enabled only (D-96)")
	}
	if !strings.Contains(st, `data-mdb-addon-manage`) || !strings.Contains(st, `data-addon-toggle`) || !strings.Contains(st, "PostgreSQL") {
		t.Error("/status Add-ons must group by engine with on/off toggles")
	}
	eng := body(t, serve(t, fixture, nil, "/engines/pg", ""))
	if !strings.Contains(eng, `/addons?engine=pg`) || !strings.Contains(eng, "pgBouncer") {
		t.Error("engine page must link the add-ons wizard with ?engine= and show supported rows")
	}
	if !strings.Contains(eng, `mdb-page-crumbs`) || !strings.Contains(eng, `mdb-addon-manage-compact`) {
		t.Error("engine page must use page crumbs and compact add-on manage")
	}
	if strings.Contains(eng, `mdb-addon-engine-head`) {
		t.Error("compact add-on manage on the engine page must not repeat the engine head")
	}
	if !strings.Contains(eng, `data-action="secrets.rotate" data-params="{&#34;engine&#34;:&#34;pg&#34;}"`) || !strings.Contains(eng, `data-action="secrets.rotate" data-params="{&#34;engine&#34;:&#34;&#34;}"`) {
		t.Error("Principals must offer rotating this engine's passwords and every engine's")
	}
	if !strings.Contains(eng, `data-cmd-shortcut`) || !strings.Contains(eng, `aria-keyshortcuts="Meta+K Control+K /"`) || strings.Contains(eng, `title="Search pages and actions (⌘K)"`) {
		t.Error("the header search trigger names its shortcuts for the platform and carries no tooltip repeating its label")
	}
	if n := strings.Count(eng, `data-bs-target="#mdb-engine-addons"`); n != 1 {
		t.Errorf("engine page opens Add-ons from %d places; one trigger is enough", n)
	}
	if !strings.Contains(eng, `mdb-engine-hero`) || !strings.Contains(eng, `mdb-engine-addr-list`) || !strings.Contains(eng, `mdb-engine-addr-row`) {
		t.Error("engine page must use the hero status strip and compact address rows")
	}
	if strings.Contains(eng, `mdb-engine-addr-tile`) {
		t.Error("engine Where to connect must not use the old unbalanced address tiles")
	}
	if !strings.Contains(eng, `id="mdb-engine-addons"`) || !strings.Contains(eng, `data-bs-target="#mdb-engine-addons"`) {
		t.Error("engine Add-ons must open in a slide-in panel, not a heavy inline card")
	}
	if strings.Contains(eng, `card mb-3 mdb-engine-addons`) {
		t.Error("engine page must not keep the inline Add-ons card beside Where to connect")
	}
	jsDash, err := os.ReadFile(filepath.Join("resources", "assets", "js", "dashboard.js")) // #nosec G304 -- fixed path in this package
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsDash), ".offcanvas.show") {
		t.Error("Live refresh must skip while an offcanvas (engine Add-ons) is open")
	}
	if ov := body(t, serve(t, fixture, nil, "/", "")); !strings.Contains(ov, `data-dbpantry-splash`) || !strings.Contains(ov, `/assets/js/pageloader.js`) || !strings.Contains(ov, `data-dbpantry-loading`) {
		t.Error("layout must ship the data-dbpantry-* splash and pageloader.js")
	} else if !strings.Contains(ov, `data-dbpantry-splash-pct`) || !strings.Contains(ov, `defer src="/assets/js/pageloader.js"`) {
		t.Error("splash must show percentage and load pageloader with defer so the splash root exists")
	}
	if db := body(t, serve(t, fixture, nil, "/databases", "")); !strings.Contains(db, `data-state="paused"`) || !strings.Contains(db, `data-refresh-toggle`) {
		t.Error("databases wizard must keep Live off so a partial refresh cannot wipe the form")
	}
	ha := body(t, serve(t, fixture, nil, "/ha", ""))
	if !strings.Contains(ha, "Primary") || !strings.Contains(ha, "Replica") || !strings.Contains(ha, `data-action="ha.status"`) {
		t.Error("/ha must explain primary/replicas and offer ha.status")
	}
	if !strings.Contains(ha, "Several engines at once") || !strings.Contains(ha, "MDB_PROFILES") {
		t.Error("/ha must document concurrent engines via MDB_PROFILES")
	}
	engs := body(t, serve(t, fixture, nil, "/engines", ""))
	if !strings.Contains(engs, "Start several engines") || !strings.Contains(engs, `href="/ha"`) {
		t.Error("engines page must offer multi-engine start and a link to /ha")
	}
	if r := serve(t, fixture, nil, "/engines/nope", ""); r.Code != http.StatusNotFound {
		t.Errorf("an unknown engine is 404, got %d", r.Code)
	}

	part := body(t, serveReq(t, fixture, nil, "/services", "", map[string]string{"X-Partial": "1"}))
	if strings.Contains(part, "<html") || !strings.Contains(part, "dbpantry-pg") {
		t.Error("a partial is the page's content alone, with the data")
	}

	var snap Snapshot
	if err := json.Unmarshal([]byte(body(t, serve(t, fixture, nil, "/api/status", ""))), &snap); err != nil || len(snap.Services) != 2 {
		t.Errorf("/api/status is the snapshot as JSON: %v %+v", err, snap.Services)
	}
}

func TestProjectPageBlursThePasswordAndRefusesCrossSite(t *testing.T) {
	r := serve(t, fixture, nil, "/projects/pg/settings", "")
	b := body(t, r)
	if r.Code != http.StatusOK {
		t.Fatalf("GET project = %d", r.Code)
	}
	for _, want := range []string{
		`DB_PASSWORD=<span class="mdb-secret" data-secret>` + testPassword + `</span>`,
		`# a &lt;comment&gt;`, `data-no-refresh`, `pg-pooler:6432`, `data-copy="#env-block"`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("the project page lacks %q", want)
		}
	}
	if r := serve(t, fixture, nil, "/projects/pg/nope", ""); r.Code != http.StatusNotFound {
		t.Errorf("an unknown project is 404, got %d", r.Code)
	}
	for site, want := range map[string]int{"cross-site": http.StatusForbidden, "same-site": http.StatusForbidden, "same-origin": http.StatusOK, "none": http.StatusOK} {
		r := serveReq(t, fixture, nil, "/projects/pg/settings", "", map[string]string{"Sec-Fetch-Site": site})
		if b := body(t, r); r.Code != want || (want != http.StatusOK && strings.Contains(b, testPassword)) {
			t.Errorf("Sec-Fetch-Site %s: %d, want %d", site, r.Code, want)
		}
	}
	if strings.Contains(body(t, serve(t, fixture, nil, "/api/status", "")), testPassword) {
		t.Error("/api/status must never carry a password")
	}
}

// The Go web UI rule: an embedded SVG favicon, linked from every page,
// served as image/svg+xml.
func TestFaviconIsServedAsSVG(t *testing.T) {
	r := serve(t, fixture, nil, "/assets/favicons/favicon.svg", "")
	b := body(t, r)
	if r.Code != http.StatusOK || r.Header().Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(b, "<svg") {
		t.Errorf("favicon: %d %q", r.Code, r.Header().Get("Content-Type"))
	}
	if r := serve(t, fixture, nil, "/assets/css/tabler.min.css", ""); r.Code != http.StatusOK || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/css") {
		t.Errorf("stylesheet: %d %q", r.Code, r.Header().Get("Content-Type"))
	}
}

func TestOtherHostsAreRefused(t *testing.T) {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	if r := serve(t, fixture, hosts, "/", "evil.example:8484"); r.Code != http.StatusMisdirectedRequest {
		t.Errorf("a rebinding Host was answered: %d", r.Code)
	}
	for _, h := range []string{"localhost:8484", "127.0.0.1:8484", "[::1]:8484"} {
		if r := serve(t, fixture, hosts, "/", h); r.Code != http.StatusOK {
			t.Errorf("Host %s refused: %d", h, r.Code)
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
	if r := serve(t, broken, nil, "/", ""); r.Code != http.StatusInternalServerError {
		t.Errorf("a failed read is an error page: %d", r.Code)
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
	readme, err := os.ReadFile(filepath.Join("resources", "assets", "README.md")) // #nosec G304 -- a fixed path in this package
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"css/tabler.min.css", "js/tabler.min.js"} {
		b, err := os.ReadFile(filepath.Join("resources", "assets", filepath.FromSlash(f))) // #nosec G304 -- a fixed path in this package
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if !strings.Contains(string(readme), f+" sha256: `"+hex.EncodeToString(sum[:])+"`") || !strings.Contains(string(b[:200]), "Tabler v1.7.5") {
			t.Errorf("%s is not the Tabler 1.7.5 file resources/assets/README.md records", f)
		}
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

func TestReadingsAreServedAtOnceAndRenewedInTheBackground(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	gate := make(chan struct{}, 4)
	read := func(context.Context) (Snapshot, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n > 1 {
			<-gate // a background reading waits until the test lets it finish
		}
		return Snapshot{Version: strconv.Itoa(n)}, nil
	}
	now := time.Now()
	var clockMu sync.Mutex
	clock := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now }
	get := cached(read, clock)

	if s, _ := get(context.Background()); s.Version != "1" {
		t.Fatalf("the first request waits for the first reading, got %q", s.Version)
	}
	for range 3 {
		if s, _ := get(context.Background()); s.Version != "1" {
			t.Fatalf("a fresh reading is served as is, got %q", s.Version)
		}
	}
	clockMu.Lock()
	now = now.Add(fresh)
	clockMu.Unlock()
	for range 3 {
		// Stale: served at once, and only one background reading starts.
		if s, _ := get(context.Background()); s.Version != "1" {
			t.Fatalf("a stale reading is still served at once, got %q", s.Version)
		}
	}
	gate <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if s, _ := get(context.Background()); s.Version == "2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background reading never replaced the stale one")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("three stale requests started %d readings, want 1 (2 in all)", calls-1)
	}

	failing := cached(func(context.Context) (Snapshot, error) { return Snapshot{}, errors.New("down") }, clock)
	if _, err := failing(context.Background()); err == nil {
		t.Error("with no good reading, a failure is returned")
	}

	// Cold concurrent hits: one reading only, every waiter sees it.
	var coldCalls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	cold := cached(func(context.Context) (Snapshot, error) {
		if coldCalls.Add(1) == 1 {
			close(started)
			<-release
		}
		return Snapshot{Version: "cold"}, nil
	}, time.Now)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := cold(context.Background())
			if err != nil || s.Version != "cold" {
				errs <- fmt.Errorf("got %+v: %w", s, err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if n := coldCalls.Load(); n != 1 {
		t.Errorf("eight cold requests started %d readings, want 1", n)
	}
}

func TestErrorsAreDashboardPages(t *testing.T) {
	for path, want := range map[string]string{
		"/nope":                 "Nothing lives at /nope",
		"/engines/nope":         "There is no engine named nope",
		"/projects/pg/nope":     "There is no project named nope on PostgreSQL",
		"/projects/zz/whatever": "There is no engine named zz",
	} {
		r := serve(t, fixture, nil, path, "")
		b := body(t, r)
		if r.Code != http.StatusNotFound || !strings.Contains(b, want) || !strings.Contains(b, `id="navbar-menu"`) || !strings.Contains(b, "Back to the overview") {
			t.Errorf("GET %s = %d, want a 404 dashboard page saying %q", path, r.Code, want)
		}
	}
	broken := func(context.Context) (Snapshot, error) { return Snapshot{}, errors.New("no manifest") }
	r := serve(t, broken, nil, "/services", "")
	if b := body(t, r); r.Code != http.StatusInternalServerError || !strings.Contains(b, "The stack could not be read: no manifest") {
		t.Errorf("a failed reading is a 500 dashboard page, got %d", r.Code)
	}
}

func TestEnginesSayWhetherTheyRun(t *testing.T) {
	stopped := func(ctx context.Context) (Snapshot, error) {
		s, _ := fixture(ctx)
		s.Services = nil // the stack is down; pg is still in the profiles
		return s, nil
	}
	if b := body(t, serve(t, fixture, nil, "/", "")); !strings.Contains(b, `bg-green-lt`) || !strings.Contains(b, `>running</span>`) {
		t.Error("a running engine is marked running")
	}
	for _, path := range []string{"/", "/engines", "/engines/pg"} {
		if b := body(t, serve(t, stopped, nil, path, "")); !strings.Contains(b, ">stopped</span>") {
			t.Errorf("%s: an enabled engine with no running service is not marked stopped", path)
		}
	}
	if b := body(t, serve(t, stopped, nil, "/engines/pg", "")); !strings.Contains(b, "mdb-engine-hero-stopped") || !strings.Contains(b, "not answering") || !strings.Contains(b, "nothing answers until you start the stack") {
		t.Error("the engine page does not warn that the engine is down")
	}
	if b := body(t, serve(t, stopped, nil, "/engines/pg", "")); !strings.Contains(b, `data-action="stack.up"`) || !strings.Contains(b, `&#34;profiles&#34;:`) {
		t.Error("stopped engine page must offer Start the stack with profiles prefills")
	}
	if b := body(t, serve(t, stopped, nil, "/engines/pg", "")); strings.Count(b, `data-action="stack.up"`) != 1 {
		t.Errorf("stopped engine page offers Start %d times; the page header is the one place", strings.Count(b, `data-action="stack.up"`))
	}
	if b := body(t, serve(t, stopped, nil, "/engines/pg", "")); !strings.Contains(b, "Start PostgreSQL first") || strings.Contains(b, `&#34;engine&#34;:&#34;pg&#34;}" data-bs-toggle="tooltip" data-bs-placement="top" title="Rotate passwords`) {
		t.Error("a stopped engine's Rotate button must be disabled with the reason, since rotation needs the database up")
	}
	if b := body(t, serve(t, stopped, nil, "/engines", "")); strings.Contains(b, "stretched-link") {
		t.Error("engine cards must not use stretched-link (it fights the ⋮ menu)")
	}
	if b := body(t, serve(t, stopped, nil, "/projects/pg/settings", "")); !strings.Contains(b, "PostgreSQL is not running") {
		t.Error("the project page does not warn that its engine is down")
	}
}

func TestServicesShowTheFixture(t *testing.T) {
	withFixture := func(ctx context.Context) (Snapshot, error) {
		s, _ := fixture(ctx)
		s.FixtureProject = "mdb-fixture"
		s.Fixture = []Service{{Name: "pg", State: "running", Health: "healthy", Image: "localhost/dbpantry-pg:17"}}
		return s, nil
	}
	if b := body(t, serve(t, withFixture, nil, "/services", "")); !strings.Contains(b, "mdb-fixture") || !strings.Contains(b, "dbpantry fixture down") {
		t.Error("the services page does not show a running fixture")
	}
	if b := body(t, serve(t, fixture, nil, "/services", "")); strings.Contains(b, "dbpantry fixture down") {
		t.Error("the fixture card is shown with no fixture")
	}
}

func TestRefreshCarriesTheReadingTime(t *testing.T) {
	r := serveReq(t, fixture, nil, "/services", "", map[string]string{"X-Partial": "1"})
	if v := r.Header().Get("X-Generated"); len(v) != len("15:04:05") {
		t.Errorf("X-Generated = %q, want the reading's hh:mm:ss for the footer", v)
	}
}

func TestEnvBlockShowsTheRealCAPath(t *testing.T) {
	got := string(envBlock("DB_SSLROOTCERT=/path/to/dbpantry/certs/ca.crt\n", "/srv/dbpantry"))
	if !strings.Contains(got, "DB_SSLROOTCERT=/srv/dbpantry/certs/ca.crt") || strings.Contains(got, "/path/to") {
		t.Errorf("the block keeps the placeholder path: %q", got)
	}
}

// The Projects page says what the engines say: an orphaned project is
// flagged with how to drop it, extensions are the ones the database has,
// and databases no password file names are listed apart (audit B1, B2).
func TestProjectsShowWhatTheEnginesSay(t *testing.T) {
	ps := []Project{
		{Engine: "pg", Name: "shop", Owner: "shop_user", Readonly: "shop_user_readonly"},
		{Engine: "pg", Name: "gone", Owner: "gone_user", Readonly: "gone_user_readonly"},
	}
	unmanaged := Reconcile(ps, project.Listing{Engines: []project.EngineDatabases{{
		Engine: "pg", Reachable: true,
		Databases: []project.Database{{Name: "postgres"}, {Name: "shop", Extensions: []string{"pgcrypto"}}, {Name: "legacy", Extensions: []string{}}},
	}}})
	if ps[0].State != project.Active || ps[1].State != project.Orphaned || len(unmanaged) != 1 || unmanaged[0].Name != "legacy" {
		t.Fatalf("projects %+v, unmanaged %+v", ps, unmanaged)
	}
	collect := func(ctx context.Context) (Snapshot, error) {
		s, _ := fixture(ctx)
		s.Projects, s.Unmanaged = ps, unmanaged
		return s, nil
	}
	page := body(t, serve(t, collect, nil, "/projects", ""))
	for _, want := range []string{
		"1 orphaned project", ">orphaned</span>", ">active</span>", "pgcrypto", "Other databases", "legacy",
		`data-action="project.drop"`, `data-mdb-project-menu`, "Connection details", "Adopt as project",
		`data-action="project.new"`, `&#34;name&#34;:&#34;legacy&#34;`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the Projects page lacks %q", want)
		}
	}
	if strings.Contains(page, `href="/projects/pg/gone"`) {
		t.Error("an orphaned project links to connection details it no longer has")
	}
	if strings.Contains(page, "table-hover") && strings.Contains(page, "PASSWORD FILE") {
		t.Error("projects list must use the row + dropdown layout, not the old wide action table")
	}
	// The nav badge counts every password-file project; the dropdown must
	// list the same set (orphaned included), or the badge lies (ghost+shop → "2" with one row).
	nav := body(t, serve(t, collect, nil, "/", ""))
	if !strings.Contains(nav, `data-sync-target="projects"`) || !strings.Contains(nav, `title="2 with password files; 1 orphaned"`) {
		t.Error("the Projects badge must be 2 (shop + gone), with the orphaned count in its title")
	}
	if !strings.Contains(nav, `href="/projects/pg/shop"`) || !strings.Contains(nav, `>orphaned</span>`) || !strings.Contains(nav, ">gone<") {
		t.Error("the Projects dropdown must list the active project and the orphaned one")
	}
	if strings.Contains(nav, `href="/projects/pg/gone"`) {
		t.Error("an orphaned project must not deep-link to connection details from the nav")
	}
	ov := body(t, serve(t, collect, nil, "/", ""))
	if strings.Contains(ov, `href="/projects/pg/gone"`) {
		t.Error("the overview must not offer connection details for an orphaned project")
	}
}

// The job drawer renders the structured run snapshots the CLI prints with
// --events (design.md D-106) instead of parsing pretty text: no middle-dot
// marks, unfinished nodes settle when the job ends, long work gets a bar after
// 3s, and the tree uses drawn connectors rather than text glyphs.
func TestJobDrawerRendersRunSnapshots(t *testing.T) {
	js, err := os.ReadFile(filepath.Join("resources", "assets", "js", "manage.js")) // #nosec G304 -- fixed path in this package
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	if strings.Contains(src, "return '·'") || strings.Contains(src, `return "·"`) {
		t.Error("the drawer must not fall through to a middle-dot mark")
	}
	for _, want := range []string{
		"'::dbpantry-run:: '", // snapshot line prefix, matches cli.runEventPrefix
		"function settleRun",  // spinners stop when the job ends
		">= 3000",             // indeterminate bar after 3 seconds
		"data-run-toggle",     // foldable steps
		"g-elbow",             // drawn tree connectors
		"data-job-retry",      // failure banner offers Retry
	} {
		if !strings.Contains(src, want) {
			t.Errorf("manage.js lacks %q", want)
		}
	}
	for _, gone := range []string{"splitTreeLine", "classifyJobLine", "mdb-job-row-bar"} {
		if strings.Contains(src, gone) {
			t.Errorf("manage.js still carries the old text parser (%q)", gone)
		}
	}
	css, err := os.ReadFile(filepath.Join("resources", "assets", "css", "dashboard.css")) // #nosec G304 -- fixed path in this package
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		".mdb-panel-head", ".mdb-job-facts", ".mdb-job-segments", ".mdb-job-banner.is-fail",
		".mdb-run-row", ".mdb-run-g.g-elbow", ".mdb-run-indet", ".mdb-raw-row", "prefers-reduced-motion",
	} {
		if !strings.Contains(string(css), want) {
			t.Errorf("dashboard.css lacks %q", want)
		}
	}
}

// Steps is the drawer's default view for every job, including commands that
// print no run snapshot, and a job started without a dialog must release the
// page-busy hold it took (it used to take two and release one, leaving the bar
// up and live refresh paused).
func TestJobDrawerDefaultsToStepsAndReleasesBusy(t *testing.T) {
	js, err := os.ReadFile(filepath.Join("resources", "assets", "js", "manage.js")) // #nosec G304 -- fixed path in this package
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	for _, want := range []string{
		"function effectiveView() { return pickedView || 'steps'; }",
		"function fallbackRun()",
		"setBusyLabel('Starting ' + a.title",
		"issuesEl.classList.toggle('is-some'",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("manage.js lacks %q", want)
		}
	}
	for _, gone := range []string{"b.disabled = !run", "setPageBusy(true, 'Starting ' + a.title"} {
		if strings.Contains(src, gone) {
			t.Errorf("manage.js still carries %q", gone)
		}
	}
}

// Connect's compose and CA snippets use .mdb-env; Tabler paints <pre> light,
// so without an explicit body colour those cards look empty and Copy looks broken.
func TestConnectSnippetsAreVisible(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("resources", "assets", "css", "dashboard.css")) // #nosec G304 -- fixed path in this package
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "color: var(--tblr-body-color)") || !strings.Contains(string(css), ".mdb-env") {
		t.Error(".mdb-env must set --tblr-body-color so Tabler's pre{color:light} does not hide the text")
	}
	page := body(t, serve(t, fixture, nil, "/connect", ""))
	for _, want := range []string{
		`id="network-snippet"`, "external: true", `id="tls-ca-path"`, "certs", "ca.crt",
		`href="/connect"`, "MDB_PUBLISH=", "Every engine's address", `id="addr-net-pg"`,
		`data-copy="#addr-net-pg"`, "In use", "Edit in Configuration", "mdb-publish", `id="publish"`,
		"Start to apply", `data-action="config.set"`, "Switch to",
		"No host ports published", "row-deck",
		`data-mdb-engine-menu`, `data-action="stack.start"`, `data-action="project.new"`,
		"Destroy data volume", "Boot and provision", "Run from this dashboard",
		"mdb-engine-addresses", "Quick access",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the Connect page lacks %q", want)
		}
	}
	// Copy lives in Quick access, not beside the address cell text.
	if i := strings.Index(page, `id="addr-net-pg"`); i >= 0 {
		cell := page[i:]
		if j := strings.Index(cell, "</td>"); j > 0 {
			cell = cell[:j]
		}
		if strings.Contains(cell, `data-copy="#addr-net-pg"`) {
			t.Error("container address copy must sit in Quick access, not the address column")
		}
	}
	ov := body(t, serve(t, fixture, nil, "/", ""))
	if strings.Contains(ov, `id="header-connect"`) {
		t.Error("the navbar must not keep a duplicate How to connect header button; Connect is in the main nav")
	}
	if !strings.Contains(ov, `href="/connect"`) || !strings.Contains(ov, "How to connect") {
		t.Error("Connect must remain reachable from chrome (nav / instance menu)")
	}
	if strings.Contains(ov, "Switch profiles") || strings.Contains(ov, "Configure subnet") || strings.Contains(ov, "Rename project") {
		t.Error("Instance menu must not embed profiles/network editors; those live on Configuration")
	}
	if !strings.Contains(ov, `data-nav-back`) || !strings.Contains(ov, `data-nav-forward`) || !strings.Contains(ov, `data-refresh-toggle`) {
		t.Error("the page header must offer history Back/Forward and a Live refresh toggle")
	}
	if !strings.Contains(ov, `mdb-page-history`) || !strings.Contains(ov, `page-header`) {
		t.Error("history Back/Forward must live in the page header row, not only the brand")
	}
	if !strings.Contains(ov, `data-status-json`) || !strings.Contains(ov, `id="mdb-status"`) {
		t.Error("Status as JSON must open the slide-in panel, not only /api/status")
	}
	if !strings.Contains(ov, `By engine`) || !strings.Contains(ov, `mdb-nav-submenu`) {
		t.Error("Stack nav must nest engines under a By engine submenu")
	}
	if !strings.Contains(ov, `mdb-nav-row`) || !strings.Contains(ov, `mdb-nav-row-main`) || !strings.Contains(ov, `mdb-nav-row-meta`) {
		t.Error("Stack nav rows must use mdb-nav-row so icons/labels/badges align")
	}
	if !strings.Contains(ov, `data-sync-target="engines-menu"`) {
		t.Error("Stack › By engine must sync on partial refresh so activate/deactivate is reactive")
	}
	partial := body(t, serveReq(t, fixture, nil, "/", "", map[string]string{"X-Partial": "1"}))
	if !strings.Contains(partial, `data-sync="engines-menu"`) {
		t.Error("partial refresh must carry data-sync=engines-menu for the Stack › By engine menu")
	}
	for _, want := range []string{
		`id="mdb-job-plain"`, `data-job-facts`, `data-job-segments`, `data-job-banner`,
		`data-job-view="steps"`, `data-job-view="raw"`, `data-job-search`, `data-job-level="issues"`,
		`data-job-follow-btn`, `data-job-steps`,
	} {
		if !strings.Contains(ov, want) {
			t.Errorf("job drawer lacks %q", want)
		}
	}
	if strings.Count(ov, `mdb-panel-head`) < 2 {
		t.Error("job and status drawers must share the slide-in panel header")
	}
	withConfig := func(ctx context.Context) (Snapshot, error) {
		s, err := fixture(ctx)
		if err != nil {
			return s, err
		}
		s.Config = []ConfigEntry{
			{Key: "MDB_PROFILES", Value: "pg", Origin: "file", Group: "Core"},
			{Key: "MDB_NETWORK_SUBNET", Value: "172.28.0.0/16", Origin: "file", Group: "Network"},
		}
		return s, nil
	}
	cfg := body(t, serve(t, withConfig, nil, "/config", ""))
	if strings.Contains(cfg, "table-light") {
		t.Error("config group rows must not use table-light (breaks dark mode)")
	}
	if !strings.Contains(cfg, "mdb-table-section") {
		t.Error("config group rows must use the theme-aware mdb-table-section class")
	}
	for _, want := range []string{
		`mdb-page-crumbs`, `aria-label="breadcrumb"`, `mdb-page-icon`, `bg-azure-lt`,
		`href="/operations"`, // real parent page (not IA-only Operate)
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config page header lacks %q", want)
		}
	}
	if strings.Contains(cfg, `page-pretitle`) {
		t.Error("config must use breadcrumbs instead of a plain page-pretitle path")
	}
	// Crumbs sit after the title (owner), not above it (Tabler default).
	titleAt := strings.Index(cfg, `page-title`)
	crumbAt := strings.Index(cfg, `mdb-page-crumbs`)
	if titleAt < 0 || crumbAt < 0 || crumbAt < titleAt {
		t.Error("config breadcrumbs must come after the page title")
	}
	eng := body(t, serve(t, fixture, nil, "/engines", ""))
	if !strings.Contains(eng, `data-mdb-engine-menu`) || !strings.Contains(eng, `href="/engines/pg"`) {
		t.Error("engine cards must keep clickable details links and a context menu")
	}
	if strings.Contains(eng, `card-inactive`) {
		t.Error("engines must not use Tabler card-inactive (pointer-events:none kills links and ⋮)")
	}
	if !strings.Contains(eng, `mdb-engine-card-actions`) {
		t.Error("engine cards need elevated action chrome above the title link")
	}
	if strings.Contains(eng, `stretched-link`) {
		t.Error("engine cards must not use stretched-link (it fights the ⋮ menu)")
	}
	// Dropdown item labels must not carry title= — those became orphan Bootstrap
	// tooltips over the Connect table while the menu itself was clipped.
	if strings.Contains(page, `Logs (debug)</span></a>`) && strings.Contains(page, `title="Stream container logs"`) {
		t.Error("engine-menu items must not promote labels into floating tooltips")
	}
	one := body(t, serve(t, fixture, nil, "/engines/pg", ""))
	if !strings.Contains(one, `data-mdb-engine-menu`) {
		t.Error("the engine detail page must reuse the shared engine-menu")
	}
	if !strings.Contains(ov, "mdb-instance-menu") || !strings.Contains(ov, "mdb-notify-menu") || !strings.Contains(ov, "mdb-lang-menu") {
		t.Error("the header must offer instance, notifications and language menus (Tabler chrome)")
	}
	if !strings.Contains(ov, "mdb-stats-menu") || !strings.Contains(ov, "mdb-stats-toggle") || !strings.Contains(ov, "mdb-header-chip") {
		t.Error("the header must show uniform Live/Status chips with a status dropdown")
	}
	if !strings.Contains(ov, `href="/status"`) || !strings.Contains(ov, `href="/services"`) {
		t.Error("the status dropdown must link Status and Services")
	}
	if !strings.Contains(ov, `href="/config"`) || !strings.Contains(ov, `href="/about"`) {
		t.Error("the instance menu must link Configuration and About")
	}
	if strings.Contains(ov, `id="navbar-help"`) {
		help := ov[strings.Index(ov, `id="navbar-help"`):]
		if i := strings.Index(help, "</div>"); i > 0 {
			help = help[:i]
		}
		if strings.Contains(help, "On this stack") || strings.Contains(help, "Language") || strings.Contains(help, "Español") {
			t.Error("Help nav must not list languages or On this stack; those live in EN and Connect")
		}
	}
	if !strings.Contains(ov, "mdb-ov-connect") {
		t.Error("Overview Where to connect must use stacked list rows")
	}
	if strings.Contains(ov, "<th>From containers</th>") {
		t.Error("Overview Where to connect must not use a wide scrollable table")
	}
	if !strings.Contains(ov, `href="/connect#publish"`) || !strings.Contains(ov, "not published") {
		t.Error("Overview not-published cells must deep-link to Connect publish modes")
	}
	st := body(t, serve(t, fixture, nil, "/status", ""))
	if !strings.Contains(st, "Live badge legend") || !strings.Contains(st, "Disconnected") {
		t.Error("Status page must explain Live / Paused / Retrying / Disconnected")
	}
	if !strings.Contains(cfg, "mdb-instance-summary") || !strings.Contains(cfg, "Manage profiles") || !strings.Contains(cfg, "MDB_PROFILES") {
		t.Error("Configuration must host the instance summary with Manage profiles")
	}
	if !strings.Contains(cfg, "Configure subnet") || !strings.Contains(cfg, "MDB_NETWORK_SUBNET") || !strings.Contains(cfg, "Rename project") {
		t.Error("Configuration must offer Configure subnet and Rename project via config.set")
	}
	if !strings.Contains(cfg, `href="/connect"`) || !strings.Contains(cfg, "Join guide") {
		t.Error("Configuration network row must link the Connect join guide")
	}
	if !strings.Contains(ov, "Connection details") || !strings.Contains(ov, "Provision a project") || !strings.Contains(ov, "Test the stack") {
		t.Error("Operate must offer connection details, project provision and stack test actions")
	}
}

// Images from every prefix are listed, the current prefix first, and an
// unsupported build (MDB_*_BASE_IMAGE, audit B7) is labelled.
func TestImagesGroupByPrefix(t *testing.T) {
	imgs := []Image{
		ParseImage("otherlocal/dbpantry-pg:15", "1GB", "1 week ago", ""),
		ParseImage("localhost/dbpantry-pg:17", "1.2GB", "2 hours ago", ""),
		ParseImage("localhost/dbpantry-mysql:8.4", "800MB", "1 day ago", "unsupported"),
		ParseImage("localhost/dbpantry-pg:16", "1.1GB", "3 days ago", ""),
	}
	groups := ImageGroups(imgs, "localhost")
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2: %+v", len(groups), groups)
	}
	if groups[0].Prefix != "localhost" || len(groups[0].Images) != 3 {
		t.Fatalf("current group = %+v", groups[0])
	}
	if groups[1].Prefix != "otherlocal" || len(groups[1].Images) != 1 {
		t.Fatalf("other group = %+v", groups[1])
	}
	var unsupported int
	for _, im := range groups[0].Images {
		if im.Unsupported {
			unsupported++
			if im.Ref != "localhost/dbpantry-mysql:8.4" {
				t.Errorf("unsupported = %s", im.Ref)
			}
		}
	}
	if unsupported != 1 {
		t.Errorf("unsupported count = %d", unsupported)
	}
}

// The Backups page shows each PITR-capable engine's on/off state and the
// last backup.verify result from the audit log (audit B8).
func TestBackupsShowPITRAndLastVerify(t *testing.T) {
	r := serve(t, func(ctx context.Context) (Snapshot, error) {
		s, _ := fixture(ctx)
		s.PITR = []PITRStatus{
			{Engine: "pg", Capable: true, Enabled: true, Summary: "on · archive at /var/lib/pgbackrest"},
			{Engine: "mysql", Capable: true, Enabled: false, Summary: "off"},
		}
		s.LastVerify = &VerifyResult{At: time.Now().Add(-time.Hour), Result: "succeeded", Actor: "session"}
		return s, nil
	}, nil, "/backups", "")
	page := body(t, r)
	for _, want := range []string{"Point-in-time recovery", "pg", "on · archive at /var/lib/pgbackrest", "mysql", ">off<", "Last verify", "succeeded"} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

// A form's engine field defaults to its first option, so a running engine
func TestProfilesWithAppendsOnce(t *testing.T) {
	if got, want := profilesWith([]string{"pg", "ui"}, "mysql"), "pg,ui,mysql"; got != want {
		t.Errorf("profilesWith append = %q, want %q", got, want)
	}
	if got, want := profilesWith([]string{"pg", " mysql ", "pg"}, "pg"), "pg,mysql"; got != want {
		t.Errorf("profilesWith dedupe = %q, want %q", got, want)
	}
	if got, want := profilesWith(nil, "cassandra"), "cassandra"; got != want {
		t.Errorf("profilesWith empty = %q, want %q", got, want)
	}
}

// comes before one that is not: provisioning on a stopped cassandra was
// the default on a stack that ran only pg. Stopped engines stay choosable,
// since images build and scan for them.
func TestChoicesPutRunningEnginesFirst(t *testing.T) {
	s := Snapshot{
		Endpoints: endpoint.Document{
			Profiles: []string{"pg", "ui"},
			Endpoints: []endpoint.Endpoint{
				{Engine: "cassandra"}, {Engine: "mysql"}, {Engine: "pg"}, {Engine: "redis"},
			},
		},
		Services: []Service{
			{Name: "cassandra", State: "exited", Health: "none"},
			{Name: "pg", State: "running", Health: "healthy"},
			{Name: "redis", State: "running", Health: "none"},
		},
	}
	c := ChoicesOf(s)
	if got, want := strings.Join(c.Engines, ","), "pg,redis,cassandra,mysql"; got != want {
		t.Errorf("engines = %s, want %s", got, want)
	}
	if got, want := strings.Join(c.Running, ","), "pg,redis"; got != want {
		t.Errorf("running = %s, want %s", got, want)
	}
	if got, want := strings.Join(c.ActiveProfiles, ","), "pg,ui"; got != want {
		t.Errorf("active_profiles = %s, want %s", got, want)
	}
	if !contains(c.Profiles, "pg") || !contains(c.Profiles, "backup") || !contains(c.Profiles, "ui") {
		t.Errorf("profile choices incomplete: %v", c.Profiles)
	}
	if len(c.EnvTypes) < 8 || !contains(c.EnvTypes, "prod") || !contains(c.EnvTypes, "localdev") {
		t.Errorf("env-type choices incomplete: %v", c.EnvTypes)
	}
}

// A container still up after MDB_PROFILES dropped it is orphan ("leftover"),
// not a green "running" next to a plain off badge (design.md D-95).
func TestEngineOrphanStatus(t *testing.T) {
	page := body(t, serve(t, func(ctx context.Context) (Snapshot, error) {
		s, _ := fixture(ctx)
		s.Endpoints.Endpoints = []endpoint.Endpoint{
			{Engine: "pg", Title: "PostgreSQL", Enabled: true, Direct: endpoint.Access{Network: endpoint.Address{Host: "pg", Port: 5432}}},
			{Engine: "mysql", Title: "MySQL", Enabled: false, Direct: endpoint.Access{Network: endpoint.Address{Host: "mysql", Port: 3306}}},
			{Engine: "redis", Title: "Redis", Enabled: false, Direct: endpoint.Access{Network: endpoint.Address{Host: "redis", Port: 6379}}},
		}
		s.Services = []Service{
			{Name: "pg", State: "running", Health: "healthy"},
			{Name: "mysql", State: "running", Health: "healthy"},
		}
		return s, nil
	}, nil, "/engines", ""))
	if !strings.Contains(page, `>leftover</span>`) {
		t.Error(`an up container outside MDB_PROFILES must show "leftover"`)
	}
	if !strings.Contains(page, `bg-azure`) {
		t.Error("orphan engines use the azure card mark")
	}
	nav := body(t, serve(t, func(ctx context.Context) (Snapshot, error) {
		s, _ := fixture(ctx)
		s.Endpoints.Endpoints = []endpoint.Endpoint{
			{Engine: "mysql", Title: "MySQL", Enabled: false, Direct: endpoint.Access{Network: endpoint.Address{Host: "mysql", Port: 3306}}},
		}
		s.Services = []Service{{Name: "mysql", State: "running", Health: "healthy"}}
		return s, nil
	}, nil, "/", ""))
	if !strings.Contains(nav, `status-azure`) || !strings.Contains(nav, `>leftover</span>`) {
		t.Error(`Stack › By engine must mark orphan engines azure with "leftover"`)
	}
	if !strings.Contains(nav, "leftover engine") || !strings.Contains(nav, `href="/engines"`) {
		t.Error(`Overview Needs attention must list leftover engines with a link to /engines`)
	}
	// An engine shown off (no container) carries the muted class, the off mark.
	if strings.Contains(nav, `>off<`) && !strings.Contains(nav, `status-muted`) {
		t.Error("an engine marked off is not muted")
	}
}

func TestHelpExplainsEveryStatus(t *testing.T) {
	help := body(t, serve(t, fixture, nil, "/help", ""))
	if !strings.Contains(help, `id="statuses"`) {
		t.Fatal("Help has no status guide (id=statuses)")
	}
	for _, g := range StateGuide {
		if !strings.Contains(help, `id="statuses-`+g.ID+`"`) {
			t.Errorf("Help's status guide has no %s section", g.ID)
		}
		for _, st := range g.States {
			if !strings.Contains(help, ">"+st.Label+"</span>") {
				t.Errorf("Help's status guide does not show the %s badge %q", g.ID, st.Label)
			}
		}
	}
	for _, path := range []string{"/", "/engines"} {
		if b := body(t, serve(t, fixture, nil, path, "")); !strings.Contains(b, `href="/help#statuses-engine"`) {
			t.Errorf("%s does not link the engine status guide", path)
		}
	}
	if b := body(t, serve(t, fixture, nil, "/status", "")); !strings.Contains(b, `href="/help#statuses"`) || !strings.Contains(b, "Three refreshes failed in a row") {
		t.Error("/status's Live legend must come from the status guide and link it")
	}
}

func TestJobConsoleIsAlwaysDark(t *testing.T) {
	// The page theme is set in the browser, so the console pins its own.
	b := body(t, serve(t, fixture, nil, "/", ""))
	if !strings.Contains(b, `class="mdb-job-console flex-fill d-flex flex-column min-h-0" data-bs-theme="dark"`) {
		t.Error("the job output console must carry data-bs-theme=dark")
	}
}

func TestJobDrawerUsesTablerComponents(t *testing.T) {
	b := body(t, serve(t, fixture, nil, "/", ""))
	for _, want := range []string{
		`class="offcanvas-header mdb-panel-head"`,
		`class="subheader mdb-panel-kicker"`,
		`class="offcanvas-title mdb-panel-title`,
		`class="datagrid mdb-job-facts"`,
		`class="nav nav-segmented mdb-job-views"`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("job drawer lacks Tabler markup %s", want)
		}
	}
	js, err := os.ReadFile(filepath.Join("resources", "assets", "js", "manage.js")) // #nosec G304 -- fixed path in this package
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"'alert alert-danger mdb-job-banner is-fail'", "'alert alert-success mdb-job-banner is-done'", "'badge bg-' + tone + '-lt mdb-run-tag", "'datagrid-item'"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("manage.js lacks %s", want)
		}
	}
}

func TestInstanceSummaryShowsValues(t *testing.T) {
	cfg := body(t, serve(t, fixture, nil, "/config", ""))
	for _, want := range []string{
		`class="row g-3 mdb-inst-row"`, `id="env-file"`, `id="profiles"`, `id="network"`, `id="settings"`,
		`<dt>Subnet</dt>`, `<dt>Project</dt>`, `<dt>Set here</dt>`, `<dt>Extras</dt>`, `class="mdb-inst-actions btn-list"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("Configuration › This instance lacks %s", want)
		}
	}
	if strings.Count(cfg, `class="mdb-inst"`) != 3 {
		t.Error("This instance must be three tiles: env file, profiles, network")
	}
}
