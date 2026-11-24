package dashboard

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The security promises of management-plan.md §3.3 and §6, each pinned
// (2026-11-24 audit, G2). CSRF was tested in mode none only.

// Settings.Validate refuses what would expose the dashboard: no login
// beyond loopback, and anything beyond loopback without TLS.
func TestValidateRefusesAnExposedDashboard(t *testing.T) {
	ok := func(auth, listen string, tls bool) Settings {
		return Settings{Auth: auth, Listen: listen, Confirm: ConfirmAll, SessionHours: 1}
	}
	for _, c := range []struct {
		name   string
		s      Settings
		tls    bool
		refuse bool
	}{
		{"none on loopback", ok(AuthNone, "127.0.0.1:8484", false), false, false},
		{"none on ::1", ok(AuthNone, "[::1]:8484", false), false, false},
		{"none on a unix socket", ok(AuthNone, "unix:/tmp/d.sock", false), false, false},
		{"none beyond loopback", ok(AuthNone, "0.0.0.0:8484", true), true, true},
		{"token beyond loopback without TLS", ok(AuthToken, "0.0.0.0:8484", false), false, true},
		{"password beyond loopback without TLS", ok(AuthPassword, "192.168.1.5:8484", false), false, true},
		{"token beyond loopback with TLS", ok(AuthToken, "0.0.0.0:8484", true), true, false},
		{"an unknown mode", ok("open", "127.0.0.1:8484", false), false, true},
		{"an unknown policy", Settings{Auth: AuthToken, Listen: "127.0.0.1:1", Confirm: "never", SessionHours: 1}, false, true},
		{"no session length", Settings{Auth: AuthToken, Listen: "127.0.0.1:1", Confirm: ConfirmAll}, false, true},
	} {
		err := c.s.Validate(c.tls)
		if (err != nil) != c.refuse {
			t.Errorf("%s: Validate = %v, refuse %v", c.name, err, c.refuse)
		}
	}
}

// A change from a browser session needs its CSRF token and a same-origin
// request in every mode, not only in none.
func TestChangesNeedCSRFInEveryMode(t *testing.T) {
	for _, mode := range []string{AuthToken, AuthPassword, AuthNone} {
		m := newMgmt(t, mode, Settings{Auth: mode, Listen: "127.0.0.1:0", Confirm: ConfirmAll, SessionHours: 1}, false)
		var c *http.Cookie
		switch mode {
		case AuthToken:
			r := m.do(call{method: http.MethodGet, path: "/?token=" + url.QueryEscape(m.auth.LaunchToken())})
			c = r.Result().Cookies()[0]
		case AuthPassword:
			r := m.do(call{method: http.MethodPost, path: "/login", body: url.Values{"password": {testPassword}}.Encode(),
				header: map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}})
			c = r.Result().Cookies()[0]
		case AuthNone:
			c, _ = m.session(t)
		}
		page := m.do(call{method: http.MethodGet, path: "/", cookie: c}).Body.String()
		i := strings.Index(page, `name="csrf-token" content="`)
		if i < 0 {
			t.Fatalf("%s: the page carries no CSRF token", mode)
		}
		rest := page[i+len(`name="csrf-token" content="`):]
		csrf := rest[:strings.IndexByte(rest, '"')]
		body := `{"action":"config.validate"}`
		for name, h := range map[string]map[string]string{
			"no CSRF token":  {"Sec-Fetch-Site": "same-origin"},
			"a wrong token":  {"Sec-Fetch-Site": "same-origin", "X-CSRF-Token": "x" + csrf},
			"cross-site":     {"Sec-Fetch-Site": "cross-site", "X-CSRF-Token": csrf},
			"another origin": {"Origin": "https://evil.example", "X-CSRF-Token": csrf},
			"no origin":      {"X-CSRF-Token": csrf},
		} {
			if r := m.do(call{method: http.MethodPost, path: "/api/v1/jobs", body: body, cookie: c, header: h}); r.Code != http.StatusForbidden {
				t.Errorf("%s, %s: %d, want 403", mode, name, r.Code)
			}
		}
		if r := m.do(call{method: http.MethodPost, path: "/api/v1/jobs", body: body, cookie: c,
			header: map[string]string{"Sec-Fetch-Site": "same-origin", "X-CSRF-Token": csrf}}); r.Code != http.StatusAccepted {
			t.Errorf("%s, same origin with the token: %d, want 202", mode, r.Code)
		}
	}
}

// An expired API token is refused, and a revoked one too.
func TestExpiredAndRevokedTokensAreRefused(t *testing.T) {
	m := newMgmt(t, AuthToken, Settings{Auth: AuthToken, Listen: "127.0.0.1:0", Confirm: ConfirmAll, SessionHours: 1}, false)
	plain, _, err := m.tokens.Create("soon", ScopeAdmin, time.Hour, time.Now().Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if r := m.do(call{method: http.MethodGet, path: "/api/v1/actions", header: map[string]string{"Authorization": "Bearer " + plain}}); r.Code != http.StatusUnauthorized {
		t.Errorf("an expired token = %d, want 401", r.Code)
	}
	live, _, err := m.tokens.Create("live", ScopeRead, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{"Authorization": "Bearer " + live}
	if r := m.do(call{method: http.MethodGet, path: "/api/v1/actions", header: hdr}); r.Code != http.StatusOK {
		t.Fatalf("a live token = %d", r.Code)
	}
	if err := m.tokens.Revoke("live"); err != nil {
		t.Fatal(err)
	}
	if r := m.do(call{method: http.MethodGet, path: "/api/v1/actions", header: hdr}); r.Code != http.StatusUnauthorized {
		t.Errorf("a revoked token = %d, want 401", r.Code)
	}
}
