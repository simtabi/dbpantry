package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/simtabi/dbpantry/src/internal/jobs"
)

// mgmt is a dashboard with a recording runner: each job's command line is
// kept, and a job blocks until release is closed when block is set.
type mgmt struct {
	h       http.Handler
	auth    *Auth
	tokens  *TokenStore
	audit   string
	runner  *jobs.Runner
	mu      sync.Mutex
	ran     [][]string
	release chan struct{}
}

func newMgmt(t *testing.T, mode string, s Settings, block bool) *mgmt {
	t.Helper()
	dir := t.TempDir()
	m := &mgmt{tokens: NewTokenStore(filepath.Join(dir, "tokens.json")), audit: filepath.Join(dir, "audit", "dashboard.jsonl"), release: make(chan struct{})}
	if !block {
		close(m.release)
	}
	var err error
	if m.auth, err = NewAuth(mode, []byte(testPassword), m.tokens, time.Hour, "s"); err != nil {
		t.Fatal(err)
	}
	m.runner = jobs.New(func(ctx context.Context, args []string, out io.Writer) error {
		m.mu.Lock()
		m.ran = append(m.ran, args)
		m.mu.Unlock()
		_, _ = io.WriteString(out, "ran "+strings.Join(args, " ")+"\n")
		select {
		case <-m.release:
		case <-ctx.Done():
		}
		return nil
	}, m.audit, 10)
	t.Cleanup(func() {
		select {
		case <-m.release:
		default:
			close(m.release)
		}
		m.runner.Close()
	})
	m.h, err = Handler(Sources{Snapshot: fixture, Project: showFixture}, Options{
		Auth: m.auth, Runner: m.runner, Settings: s, Audit: m.audit, BackupDir: "/srv/backups",
		Env: func(string) string { return "" },
		Choices: func(context.Context) (Choices, error) {
			return Choices{Engines: []string{"pg"}, Services: []string{"pg", "caddy"}, Volumes: []string{"mdb_pg_data"}, Dumps: []string{"pg_settings_1.dump"}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type call struct {
	method, path, body string
	header             map[string]string
	cookie             *http.Cookie
}

func (m *mgmt) do(c call) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), c.method, c.path, strings.NewReader(c.body))
	for k, v := range c.header {
		req.Header.Set(k, v)
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	rec := httptest.NewRecorder()
	m.h.ServeHTTP(rec, req)
	return rec
}

// session signs a browser in (mode none: the first GET) and returns its
// cookie and CSRF token.
func (m *mgmt) session(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	r := m.do(call{method: http.MethodGet, path: "/"})
	var c *http.Cookie
	for _, k := range r.Result().Cookies() {
		if k.Name == "s" {
			c = k
		}
	}
	if c == nil {
		t.Fatalf("no session cookie from GET /: %d", r.Code)
	}
	b := r.Body.String()
	i := strings.Index(b, `name="csrf-token" content="`)
	if i < 0 {
		t.Fatal("the page carries no csrf-token meta")
	}
	rest := b[i+len(`name="csrf-token" content="`):]
	return c, rest[:strings.IndexByte(rest, '"')]
}

func (m *mgmt) post(c *http.Cookie, csrf, body string) *httptest.ResponseRecorder {
	return m.do(call{method: http.MethodPost, path: "/api/v1/jobs", body: body, cookie: c,
		header: map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrf, "Sec-Fetch-Site": "same-origin"}})
}

func wait(t *testing.T, r *jobs.Runner, id string) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j, err := r.Get(id); err == nil && j.State != jobs.Running {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s never ended", id)
	return jobs.Job{}
}

// Every action, for every scope: a token below the action's scope is
// refused 403 and runs nothing; at or above it, the action starts.
func TestEveryActionIsGatedByScope(t *testing.T) {
	m := newMgmt(t, AuthToken, Settings{Auth: AuthToken, Listen: "127.0.0.1:0", Confirm: ConfirmAll, SessionHours: 1, AllowDestroy: true}, false)
	params := map[string]map[string]string{
		"service.start": {"service": "pg"}, "service.stop": {"service": "pg"},
		"service.restart": {"service": "pg"}, "project.new": {"name": "app", "engine": "pg"},
		"backup.restore": {"engine": "pg", "db": "app", "file": "pg_settings_1.dump"}, "pooler.sync": {"engine": "pg"},
		"ha.reinit": {"node": "pg1"}, "destroy.volume": {"volume": "mdb_pg_data"},
		"project.rotate": {"name": "app", "engine": "pg"}, "project.drop": {"name": "app", "engine": "pg"},
		"config.set":    {"key": "MDB_PROFILES", "value": "pg,ui"},
		"addon.enable":  {"id": "pooler.pg", "engine": "pg"},
		"addon.disable": {"id": "pooler.pg", "engine": "pg"},
		"env.init":      {"name": "shop-uat", "type": "uat"},
		"env.copy":      {"from": "shop-dev", "to": "shop-uat", "engine": "pg", "db": "shop"},
		"env.migrate":   {"from": "shop-dev", "to": "shop-uat", "engine": "pg", "db": "shop"},
		"env.remove":    {"name": "shop-uat"},
	}
	confirm := map[string]string{"backup.restore": "app", "project.rotate": "app", "project.drop": "app", "secrets.rotate": "ROTATE", "ha.failover": "mdb-pg", "ha.reinit": "pg1", "destroy.volume": "mdb_pg_data", "env.copy": "shop", "env.migrate": "shop", "env.remove": "shop-uat"}
	n := 0
	for _, sc := range []Scope{ScopeRead, ScopeOperate, ScopeAdmin} {
		plain, _, err := m.tokens.Create("t"+sc.String(), sc, time.Hour, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range Actions {
			body, _ := json.Marshal(jobRequest{Action: a.Name, Params: params[a.Name], Confirm: confirm[a.Name]})
			r := m.do(call{method: http.MethodPost, path: "/api/v1/jobs", body: string(body), header: map[string]string{"Authorization": "Bearer " + plain}})
			want := http.StatusAccepted
			if sc < a.Scope {
				want = http.StatusForbidden
			}
			if r.Code != want {
				t.Errorf("%s with a %s token = %d, want %d: %s", a.Name, sc, r.Code, want, r.Body)
				continue
			}
			if want == http.StatusAccepted {
				var j jobs.Job
				_ = json.Unmarshal(r.Body.Bytes(), &j)
				wait(t, m.runner, j.ID)
				n++
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.ran) != n {
		t.Errorf("%d jobs were accepted but %d commands ran", n, len(m.ran))
	}
}

func TestChangesNeedCSRFAndSameOrigin(t *testing.T) {
	m := newMgmt(t, AuthNone, Defaults, false)
	c, csrf := m.session(t)
	body := `{"action":"stack.down"}`
	for name, tc := range map[string]struct {
		header map[string]string
		want   int
	}{
		"no token":     {map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusForbidden},
		"wrong token":  {map[string]string{"Sec-Fetch-Site": "same-origin", "X-CSRF-Token": "nope"}, http.StatusForbidden},
		"cross-site":   {map[string]string{"Sec-Fetch-Site": "cross-site", "X-CSRF-Token": csrf}, http.StatusForbidden},
		"other origin": {map[string]string{"Origin": "https://evil.example", "X-CSRF-Token": csrf}, http.StatusForbidden},
		"same origin":  {map[string]string{"Sec-Fetch-Site": "same-origin", "X-CSRF-Token": csrf}, http.StatusAccepted},
	} {
		r := m.do(call{method: http.MethodPost, path: "/api/v1/jobs", body: body, cookie: c, header: tc.header})
		if r.Code != tc.want {
			t.Errorf("%s: %d, want %d", name, r.Code, tc.want)
		}
		if r.Code == http.StatusAccepted {
			var j jobs.Job
			_ = json.Unmarshal(r.Body.Bytes(), &j)
			wait(t, m.runner, j.ID)
		}
	}
	if r := m.do(call{method: http.MethodPost, path: "/api/v1/jobs", body: body}); r.Code != http.StatusForbidden && r.Code != http.StatusUnauthorized {
		t.Errorf("a POST with no session = %d", r.Code)
	}
}

func TestOneChangeAtATime(t *testing.T) {
	m := newMgmt(t, AuthNone, Defaults, true)
	c, csrf := m.session(t)
	first := m.post(c, csrf, `{"action":"stack.up"}`)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first change = %d %s", first.Code, first.Body)
	}
	second := m.post(c, csrf, `{"action":"stack.down"}`)
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "stack.up") {
		t.Errorf("a second change while one runs = %d %s, want 409 naming the running job", second.Code, second.Body)
	}
	if r := m.post(c, csrf, `{"action":"ha.status"}`); r.Code != http.StatusAccepted {
		t.Errorf("a read while a change runs = %d, want 202", r.Code)
	}
	// The running change is in every page's header, with a button to its output.
	if b := m.do(call{method: http.MethodGet, path: "/services", cookie: c}).Body.String(); !strings.Contains(b, `data-job-open=`) || !strings.Contains(b, "stack.up") {
		t.Error("the header does not show the running change")
	}
	// And every change button is disabled with the reason.
	var acts []actionView
	_ = json.Unmarshal(m.do(call{method: http.MethodGet, path: "/api/v1/actions", cookie: c}).Body.Bytes(), &acts)
	for _, a := range acts {
		if a.Scope > ScopeRead && (a.Allowed || a.Reason == "") {
			t.Errorf("%s is offered while a change runs: %+v", a.Name, a)
		}
	}
	close(m.release)
	var j jobs.Job
	_ = json.Unmarshal(first.Body.Bytes(), &j)
	wait(t, m.runner, j.ID)
}

func TestDestructiveActionsNeedTheirTypedConfirmation(t *testing.T) {
	m := newMgmt(t, AuthNone, Settings{Auth: AuthNone, Listen: "127.0.0.1:0", Confirm: ConfirmAll, SessionHours: 1}, false)
	c, csrf := m.session(t)
	restore := `{"action":"backup.restore","params":{"engine":"pg","db":"app","file":"pg_settings_1.dump"},"confirm":%q}`
	if r := m.post(c, csrf, strings.Replace(restore, "%q", `"nope"`, 1)); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("a wrong confirmation = %d", r.Code)
	}
	r := m.post(c, csrf, strings.Replace(restore, "%q", `"app"`, 1))
	if r.Code != http.StatusAccepted {
		t.Fatalf("the right confirmation = %d %s", r.Code, r.Body)
	}
	var j jobs.Job
	_ = json.Unmarshal(r.Body.Bytes(), &j)
	wait(t, m.runner, j.ID)
	m.mu.Lock()
	got := strings.Join(m.ran[len(m.ran)-1], " ")
	m.mu.Unlock()
	if got != "restore --engine pg --db app --file /srv/backups/pg_settings_1.dump --confirm app" {
		t.Errorf("the command checks the confirmation again: ran %q", got)
	}
	// Deleting volumes is off unless enabled, whatever the confirmation.
	if r := m.post(c, csrf, `{"action":"destroy.volume","params":{"volume":"mdb_pg_data"},"confirm":"mdb_pg_data"}`); r.Code != http.StatusForbidden {
		t.Errorf("destroy with allow_destroy off = %d", r.Code)
	}
	// Parameters are checked against the stack, never passed through.
	for _, bad := range []string{
		`{"action":"service.restart","params":{"service":"nope"}}`,
		`{"action":"service.restart","params":{"service":"pg; rm -rf /"}}`,
		`{"action":"project.new","params":{"name":"app","engine":"pg","extra":"x"}}`,
		`{"action":"backup.restore","params":{"engine":"pg","db":"app","file":"../../etc/passwd"},"confirm":"app"}`,
	} {
		if r := m.post(c, csrf, bad); r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", bad, r.Code)
		}
	}
}

func TestEveryChangeIsAudited(t *testing.T) {
	m := newMgmt(t, AuthNone, Defaults, false)
	c, csrf := m.session(t)
	r := m.post(c, csrf, `{"action":"secrets.rotate","confirm":"ROTATE"}`)
	var j jobs.Job
	_ = json.Unmarshal(r.Body.Bytes(), &j)
	wait(t, m.runner, j.ID)
	got, err := jobs.ReadAudit(m.audit, 0)
	if err != nil || len(got) != 1 || got[0].Actor != "local" || got[0].Action != "secrets.rotate" {
		t.Fatalf("audit = %+v %v", got, err)
	}
	if b := m.do(call{method: http.MethodGet, path: "/jobs", cookie: c}).Body.String(); !strings.Contains(b, "secrets.rotate") || !strings.Contains(b, "dbpantry secrets rotate --confirm ROTATE") {
		t.Error("the Jobs page does not list the change")
	}
}

func TestTheJobStreamsItsOutput(t *testing.T) {
	m := newMgmt(t, AuthNone, Defaults, false)
	c, csrf := m.session(t)
	var j jobs.Job
	_ = json.Unmarshal(m.post(c, csrf, `{"action":"images.build","params":{"engine":"pg"}}`).Body.Bytes(), &j)
	wait(t, m.runner, j.ID)
	b := m.do(call{method: http.MethodGet, path: "/api/v1/jobs/" + j.ID + "/events", cookie: c}).Body.String()
	if !strings.Contains(b, `event: line`) || !strings.Contains(b, `ran images build pg`) || !strings.Contains(b, `event: end`) {
		t.Errorf("events = %q", b)
	}
}

func TestLaunchTokenWorksOnce(t *testing.T) {
	m := newMgmt(t, AuthToken, Defaults, false)
	if r := m.do(call{method: http.MethodGet, path: "/"}); r.Code != http.StatusUnauthorized {
		t.Errorf("no session = %d", r.Code)
	}
	tok := m.auth.LaunchToken()
	r := m.do(call{method: http.MethodGet, path: "/services?token=" + url.QueryEscape(tok)})
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/services" || len(r.Result().Cookies()) == 0 {
		t.Fatalf("the launch link = %d %q", r.Code, r.Header().Get("Location"))
	}
	if r := m.do(call{method: http.MethodGet, path: "/?token=" + url.QueryEscape(tok)}); r.Code != http.StatusUnauthorized {
		t.Errorf("the launch link worked twice: %d", r.Code)
	}
	if r := m.do(call{method: http.MethodGet, path: "/", cookie: r.Result().Cookies()[0]}); r.Code != http.StatusOK {
		t.Errorf("the session = %d", r.Code)
	}
}

func TestPasswordSignIn(t *testing.T) {
	m := newMgmt(t, AuthPassword, Defaults, false)
	if r := m.do(call{method: http.MethodGet, path: "/backups"}); r.Code != http.StatusSeeOther || !strings.HasPrefix(r.Header().Get("Location"), "/login") {
		t.Errorf("no session = %d %q", r.Code, r.Header().Get("Location"))
	}
	form := func(pw string) call {
		return call{method: http.MethodPost, path: "/login?next=/backups", body: url.Values{"password": {pw}}.Encode(),
			header: map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}}
	}
	if r := m.do(form("wrong")); r.Code != http.StatusUnauthorized {
		t.Errorf("a wrong password = %d", r.Code)
	}
	r := m.do(form(testPassword))
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/backups" {
		t.Errorf("the right password = %d %q", r.Code, r.Header().Get("Location"))
	}
}

func TestTokensAreManagedByAnAdminAndShownOnce(t *testing.T) {
	m := newMgmt(t, AuthNone, Defaults, false)
	c, csrf := m.session(t)
	h := map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrf, "Sec-Fetch-Site": "same-origin"}
	r := m.do(call{method: http.MethodPost, path: "/api/v1/tokens", body: `{"name":"ci","scope":"read","days":7}`, cookie: c, header: h})
	if r.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", r.Code, r.Body)
	}
	var created struct {
		Token string `json:"token"`
		Info  Token  `json:"info"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &created)
	if !strings.HasPrefix(created.Token, TokenPrefix) {
		t.Fatalf("token = %q", created.Token)
	}
	if b := m.do(call{method: http.MethodGet, path: "/operations", cookie: c}).Body.String(); strings.Contains(b, created.Token) || !strings.Contains(b, created.Info.ID) {
		t.Error("the Operations page must list the token by id and never show its value")
	}
	// A read token cannot manage tokens, and can read.
	ro := map[string]string{"Authorization": "Bearer " + created.Token}
	if r := m.do(call{method: http.MethodGet, path: "/api/v1/tokens", header: ro}); r.Code != http.StatusForbidden {
		t.Errorf("a read token listing tokens = %d", r.Code)
	}
	if r := m.do(call{method: http.MethodGet, path: "/api/v1/status", header: ro}); r.Code != http.StatusOK {
		t.Errorf("a read token reading status = %d", r.Code)
	}
	if r := m.do(call{method: http.MethodDelete, path: "/api/v1/tokens/" + created.Info.ID, cookie: c, header: h}); r.Code != http.StatusNoContent {
		t.Errorf("revoke = %d", r.Code)
	}
	if r := m.do(call{method: http.MethodGet, path: "/api/v1/status", header: ro}); r.Code != http.StatusUnauthorized {
		t.Errorf("a revoked token = %d", r.Code)
	}
	got, _ := jobs.ReadAudit(m.audit, 0)
	if len(got) != 2 {
		t.Errorf("token changes audited: %d entries", len(got))
	}
}

func TestActionButtons(t *testing.T) {
	m := newMgmt(t, AuthNone, Defaults, false)
	c, _ := m.session(t)
	b := m.do(call{method: http.MethodGet, path: "/operations", cookie: c}).Body.String()
	if !strings.Contains(b, `data-action="secrets.rotate"`) || !strings.Contains(b, `dbpantry ha failover --node &lt;node&gt;`) {
		t.Error("the Operations page lacks its action buttons and command lines")
	}
	// Deleting volumes is off: its button is drawn, disabled, with the reason.
	if !strings.Contains(b, `title="deleting volumes is off`) {
		t.Error("a refused action must be drawn disabled with its reason")
	}
	if !strings.Contains(b, `data-bs-toggle="tooltip"`) || !strings.Contains(b, `data-bs-toggle="popover"`) {
		t.Error("action buttons and area rows must carry tooltips and popovers")
	}
	if !strings.Contains(b, "runs dbpantry") {
		t.Error("tooltips must name the command the button runs")
	}
	if _, err := actionButton(page{Actions: map[string]actionView{}}, "nope", "x"); err == nil {
		t.Error("an unknown action must be a template error")
	}
	ok := actionView{Action: Action{Name: "verify", Title: "Run checks"}, Allowed: true, Command: "dbpantry verify --fast"}
	html, err := actionButton(page{Actions: map[string]actionView{"verify": ok}}, "verify", "Run checks", "class=list-group-item list-group-item-action", "icon=activity")
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if strings.Contains(s, `class="btn`) || strings.Contains(s, " btn ") {
		t.Errorf("list-group action must not keep btn classes, got %s", s)
	}
	if !strings.Contains(s, "list-group-item") || !strings.Contains(s, "d-flex") || !strings.Contains(s, `ms-2`) {
		t.Errorf("list-group action must align like Help rows, got %s", s)
	}
}

func TestStackStartSkipsASecondDashboard(t *testing.T) {
	var a Action
	for _, x := range Actions {
		if x.Name == "stack.start" {
			a = x
			break
		}
	}
	if a.Name == "" {
		t.Fatal("stack.start is missing from Actions")
	}
	got := strings.Join(a.Command(Values{"profiles": "pg", "project": "myapp:pg"}), " ")
	if !strings.Contains(got, "start") || !strings.Contains(got, "--no-dashboard") {
		t.Fatalf("stack.start must run start --no-dashboard, got %q", got)
	}
	if !strings.Contains(got, "--project myapp:pg") || !strings.Contains(got, "--profiles pg") {
		t.Fatalf("stack.start must forward profiles and project, got %q", got)
	}
}

func TestActionTipExplainsTheCommand(t *testing.T) {
	v := actionView{
		Action:  Action{Name: "stack.up", Title: "Start the stack"},
		Allowed: true,
		Command: "dbpantry up",
	}
	tip := actionTip(v)
	if !strings.Contains(tip, "Start the stack") || !strings.Contains(tip, "dbpantry up") {
		t.Fatalf("tip = %q", tip)
	}
	v.Allowed, v.Reason = false, "needs the operate scope"
	if actionTip(v) != "needs the operate scope" {
		t.Fatalf("refused tip = %q", actionTip(v))
	}
}
