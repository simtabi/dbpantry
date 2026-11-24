package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/simtabi/dbpantry/src/internal/hooks"
	"github.com/simtabi/dbpantry/src/internal/jobs"
)

// The tests in this file each pin a finding of the 2026-11-24 codebase
// audit (.claude/design/dbpantry/audits/2026-11-24-codebase-audit.md).

// S7: the page a sign-in returns to is a path on this site. "/\evil.com" is
// read by browsers as "//evil.com", another site.
func TestSignInNeverRedirectsOffSite(t *testing.T) {
	m := newMgmt(t, AuthPassword, Defaults, false)
	for _, next := range []string{`/\evil.com`, `/\/evil.com`, "//evil.com", "https://evil.com", `/%5Cevil.com`} {
		r := m.do(call{method: http.MethodPost, path: "/login?next=" + url.QueryEscape(next), body: url.Values{"password": {testPassword}}.Encode(),
			header: map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}})
		if loc := r.Header().Get("Location"); r.Code != http.StatusSeeOther || loc != "/" {
			t.Errorf("next=%q signed in to %d %q, want 303 to /", next, r.Code, loc)
		}
	}
}

// S9: a session is made for each cookieless GET in mode none, so a local
// poller would grow the session table without bound. It is capped, and
// expired sessions are dropped.
func TestSessionsAreBounded(t *testing.T) {
	m := newMgmt(t, AuthNone, Defaults, false)
	for range maxSessions + 200 {
		m.do(call{method: http.MethodGet, path: "/api/v1/actions"})
	}
	m.auth.mu.Lock()
	n := len(m.auth.session)
	m.auth.mu.Unlock()
	if n > maxSessions {
		t.Errorf("%d sessions after %d cookieless GETs, want at most %d", n, maxSessions+200, maxSessions)
	}
	// The newest still works.
	c, _ := m.session(t)
	if r := m.do(call{method: http.MethodGet, path: "/api/v1/actions", cookie: c}); r.Code != http.StatusOK {
		t.Errorf("the newest session = %d", r.Code)
	}
}

// S12: the Operations page lists API tokens to an admin only, as
// /api/v1/tokens does.
func TestOperationsListsTokensToAdminsOnly(t *testing.T) {
	m := newMgmt(t, AuthToken, Settings{Auth: AuthToken, Listen: "127.0.0.1:0", Confirm: ConfirmAll, SessionHours: 1}, false)
	read, _, err := m.tokens.Create("reader", ScopeRead, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	admin, _, err := m.tokens.Create("adminbot", ScopeAdmin, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	get := func(tok string) string {
		return m.do(call{method: http.MethodGet, path: "/operations", header: map[string]string{"Authorization": "Bearer " + tok}}).Body.String()
	}
	if b := get(read); strings.Contains(b, "adminbot") {
		t.Error("a read token sees the API tokens on /operations")
	}
	if b := get(admin); !strings.Contains(b, "adminbot") {
		t.Error("an admin does not see the API tokens on /operations")
	}
}

// S6: when the runtime does not answer, a service is unavailable, never
// "no such service".
func TestAnOutageIsNotANotFound(t *testing.T) {
	m := newMgmt(t, AuthNone, Defaults, false)
	down := func(ctx context.Context) (Snapshot, error) {
		s, err := fixture(ctx)
		s.Services, s.ServicesErr = nil, "the container runtime did not answer"
		return s, err
	}
	logs := func(context.Context, string, string, bool, io.Writer) error { return nil }
	h, err := Handler(Sources{Snapshot: down, Project: showFixture, Logs: logs}, Options{Auth: m.auth, Runner: m.runner, Settings: Defaults, Audit: m.audit})
	if err != nil {
		t.Fatal(err)
	}
	m.h = h
	c, csrf := m.session(t)
	if r := m.do(call{method: http.MethodGet, path: "/services/pg/logs", cookie: c}); r.Code != http.StatusServiceUnavailable {
		t.Errorf("the logs page during an outage = %d, want 503", r.Code)
	}
	if r := m.do(call{method: http.MethodGet, path: "/api/v1/services/pg/logs", cookie: c}); r.Code != http.StatusServiceUnavailable {
		t.Errorf("the log stream during an outage = %d, want 503", r.Code)
	}
	if r := m.post(c, csrf, `{"action":"service.restart","params":{"service":"pg"}}`); r.Code != http.StatusServiceUnavailable {
		t.Errorf("a service action during an outage = %d, want 503: %s", r.Code, r.Body)
	}
}

// S10: a value is never read as a flag. config set MDB_X -1 failed with an
// unknown shorthand, and a value of --help did nothing and succeeded.
func TestValuesAreNeverFlags(t *testing.T) {
	m := newMgmt(t, AuthNone, Defaults, false)
	c, csrf := m.session(t)
	r := m.post(c, csrf, `{"action":"config.set","params":{"key":"MDB_PROFILES","value":"--help"}}`)
	if r.Code != http.StatusAccepted {
		t.Fatalf("config.set = %d %s", r.Code, r.Body)
	}
	var j jobs.Job
	_ = json.Unmarshal(r.Body.Bytes(), &j)
	wait(t, m.runner, j.ID)
	m.mu.Lock()
	got := strings.Join(m.ran[len(m.ran)-1], " ")
	m.mu.Unlock()
	if got != "config set -- MDB_PROFILES --help" {
		t.Errorf("config.set ran %q, want the value after --", got)
	}
}

// G3: creating and deleting a webhook is audited, as an API token is. The
// URL is recorded by host only: a webhook URL's path is often the secret
// (an incoming-webhook URL is its own credential).
func TestWebhookChangesAreAudited(t *testing.T) {
	m := newMgmt(t, AuthToken, Settings{Auth: AuthToken, Listen: "127.0.0.1:0", Confirm: ConfirmAll, SessionHours: 1}, false)
	bus, err := hooks.New("", 50)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Handler(Sources{Snapshot: fixture, Project: showFixture}, Options{Auth: m.auth, Runner: m.runner, Settings: Settings{Auth: AuthToken, Listen: "127.0.0.1:0", Confirm: ConfirmAll, SessionHours: 1}, Audit: m.audit, Hooks: bus})
	if err != nil {
		t.Fatal(err)
	}
	m.h = h
	admin, _, err := m.tokens.Create("hookadmin", ScopeAdmin, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{"Authorization": "Bearer " + admin}
	secretPath := "/services/T000/B000/" + testPassword
	r := m.do(call{method: http.MethodPost, path: "/api/v1/hooks", body: `{"url":"https://hooks.example.com` + secretPath + `","events":[]}`, header: hdr})
	if r.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", r.Code, r.Body)
	}
	var ep hooks.Endpoint
	_ = json.Unmarshal(r.Body.Bytes(), &ep)
	if r := m.do(call{method: http.MethodDelete, path: "/api/v1/hooks/" + ep.ID, header: hdr}); r.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", r.Code, r.Body)
	}
	entries, err := jobs.ReadAudit(m.audit, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action] = true
		line := strings.Join(e.Args, " ")
		if strings.HasPrefix(e.Action, "hook.") && (!strings.Contains(line, ep.ID) || !strings.Contains(line, "hooks.example.com") || strings.Contains(line, testPassword)) {
			t.Errorf("%s audited as %q: want the id and the host, never the path", e.Action, line)
		}
	}
	if !seen["hook.create"] || !seen["hook.delete"] {
		t.Errorf("audit holds %v, want hook.create and hook.delete", seen)
	}
}
