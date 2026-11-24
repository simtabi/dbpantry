package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Scope is what a principal may do: read < operate < admin (refactor plan
// §8.7, X14). The browser session is admin, since the person at the machine
// is the operator; an API token may be narrower.
type Scope int

// The scopes, in order.
const (
	ScopeNone Scope = iota
	ScopeRead
	ScopeOperate
	ScopeAdmin
)

func (s Scope) String() string {
	switch s {
	case ScopeRead:
		return "read"
	case ScopeOperate:
		return "operate"
	case ScopeAdmin:
		return "admin"
	default:
		return "none"
	}
}

// ParseScope reads read, operate or admin.
func ParseScope(s string) (Scope, error) {
	switch s {
	case "read":
		return ScopeRead, nil
	case "operate":
		return ScopeOperate, nil
	case "admin":
		return ScopeAdmin, nil
	}
	return ScopeNone, fmt.Errorf("scope %q: want read, operate or admin", s)
}

// MarshalJSON writes the scope's name.
func (s Scope) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON reads a scope's name.
func (s *Scope) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p, err := ParseScope(v)
	*s = p
	return err
}

// Principal is who is asking.
type Principal struct {
	Actor string
	Scope Scope
	// CSRF is the session's token, which every POST must carry. Empty for an
	// API token, which is not sent automatically by a browser.
	CSRF string
	// Session is true for a browser session, which needs CSRF and the
	// same-origin check on every change.
	Session bool
}

// maxSessions caps the session table: in mode none every cookieless GET
// starts one, so a local poller would otherwise grow it without bound.
const maxSessions = 256

type session struct {
	csrf    string
	expires time.Time
}

// Auth decides who a request comes from.
type Auth struct {
	mode     string
	password []byte // AuthPassword: the password's SHA-256
	tokens   *TokenStore
	ttl      time.Duration
	cookie   string
	now      func() time.Time

	mu      sync.Mutex
	launch  string // AuthToken: the one-time launch token, until exchanged
	session map[string]session
}

// NewAuth sets up a mode. password is the dashboard password for
// AuthPassword (read from secrets/dashboard_password.txt); cookie names the
// session cookie, which carries the port so two dashboards on one machine
// never share a session.
func NewAuth(mode string, password []byte, tokens *TokenStore, ttl time.Duration, cookie string) (*Auth, error) {
	a := &Auth{mode: mode, tokens: tokens, ttl: ttl, cookie: cookie, now: time.Now, session: map[string]session{}}
	switch mode {
	case AuthToken:
		t, err := randomToken()
		if err != nil {
			return nil, err
		}
		a.launch = t
	case AuthPassword:
		pw := strings.TrimSpace(string(password))
		if pw == "" {
			return nil, errors.New("auth password: secrets/dashboard_password.txt is missing or empty (run dbpantry init, or write one)")
		}
		h := sha256.Sum256([]byte(pw))
		a.password = h[:]
	case AuthNone:
	default:
		return nil, fmt.Errorf("auth %q: want token, password or none", mode)
	}
	return a, nil
}

// Mode is the auth mode.
func (a *Auth) Mode() string { return a.mode }

// LaunchToken is the one-time token in AuthToken mode, for the URL the
// command prints; empty once exchanged or in another mode.
func (a *Auth) LaunchToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.launch
}

// identify returns the request's principal, or false. It never creates a
// session; see login.
func (a *Auth) identify(r *http.Request) (Principal, bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if a.tokens == nil {
			return Principal{}, false
		}
		t, ok := a.tokens.Lookup(strings.TrimPrefix(h, "Bearer "), a.now())
		if !ok {
			return Principal{}, false
		}
		return Principal{Actor: "token:" + t.Name, Scope: t.Scope}, true
	}
	c, err := r.Cookie(a.cookie)
	if err != nil {
		return Principal{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.session[c.Value]
	if !ok || a.now().After(s.expires) {
		delete(a.session, c.Value)
		return Principal{}, false
	}
	actor := "browser"
	if a.mode == AuthNone {
		actor = "local"
	}
	return Principal{Actor: actor, Scope: ScopeAdmin, CSRF: s.csrf, Session: true}, true
}

// newSession starts a session and sets its cookie: HttpOnly, SameSite=Strict,
// Path=/, for the session's lifetime.
func (a *Auth) newSession(w http.ResponseWriter, secure bool) (Principal, error) {
	id, err := randomToken()
	if err != nil {
		return Principal{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return Principal{}, err
	}
	a.mu.Lock()
	a.pruneLocked()
	a.session[id] = session{csrf: csrf, expires: a.now().Add(a.ttl)}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: a.cookie, Value: id, Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(a.ttl.Seconds())})
	actor := "browser"
	if a.mode == AuthNone {
		actor = "local"
	}
	return Principal{Actor: actor, Scope: ScopeAdmin, CSRF: csrf, Session: true}, nil
}

// pruneLocked drops expired sessions and, at maxSessions, the one closest
// to expiring (the oldest), to make room. Called with a.mu held.
func (a *Auth) pruneLocked() {
	now := a.now()
	for id, s := range a.session {
		if now.After(s.expires) {
			delete(a.session, id)
		}
	}
	for len(a.session) >= maxSessions {
		oldest, at := "", time.Time{}
		for id, s := range a.session {
			if oldest == "" || s.expires.Before(at) {
				oldest, at = id, s.expires
			}
		}
		delete(a.session, oldest)
	}
}

// exchange trades the launch token for a session. It works once.
func (a *Auth) exchange(token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mode != AuthToken || a.launch == "" || subtle.ConstantTimeCompare([]byte(token), []byte(a.launch)) != 1 {
		return false
	}
	a.launch = ""
	return true
}

// checkPassword compares a submitted password in constant time.
func (a *Auth) checkPassword(pw string) bool {
	if a.mode != AuthPassword {
		return false
	}
	h := sha256.Sum256([]byte(pw))
	return subtle.ConstantTimeCompare(h[:], a.password) == 1
}

// logout ends the request's session.
func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(a.cookie); err == nil {
		a.mu.Lock()
		delete(a.session, c.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

// sameOrigin is the check every browser POST must pass, in every mode:
// Sec-Fetch-Site same-origin when the browser sends it, else an Origin
// naming this host. A request with neither is refused; every current
// browser sends Origin on a POST.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// csrfOK compares the request's X-CSRF-Token (or form field csrf) with the
// session's.
func csrfOK(r *http.Request, p Principal) bool {
	got := r.Header.Get("X-CSRF-Token")
	if got == "" {
		got = r.PostFormValue("csrf")
	}
	return got != "" && p.CSRF != "" && subtle.ConstantTimeCompare([]byte(got), []byte(p.CSRF)) == 1
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// --- API tokens -------------------------------------------------------------

// TokenPrefix starts every API token, so a scanner can recognise one.
const TokenPrefix = "dbpantry_"

// Token is a stored API token: its SHA-256, never the token itself.
type Token struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Scope   Scope     `json:"scope"`
	SHA256  string    `json:"sha256"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

// TokenStore keeps API tokens in a 0600 JSON file in the instance home
// ($MDB_HOME/api-tokens.json).
type TokenStore struct {
	path string
	mu   sync.Mutex
}

// NewTokenStore opens the store at path; the file need not exist yet.
func NewTokenStore(path string) *TokenStore { return &TokenStore{path: path} }

func (s *TokenStore) load() ([]Token, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ts []Token
	if err := json.Unmarshal(b, &ts); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return ts, nil
}

func (s *TokenStore) save(ts []Token) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ts, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Create makes a token, stores its hash and returns the token itself, which
// is shown once and never again. An expiry is required.
func (s *TokenStore) Create(name string, scope Scope, ttl time.Duration, now time.Time) (string, Token, error) {
	if name == "" || strings.ContainsAny(name, " \t\n/") {
		return "", Token{}, fmt.Errorf("token name %q: one word, no spaces or slashes", name)
	}
	if scope == ScopeNone {
		return "", Token{}, errors.New("a token needs a scope: read, operate or admin")
	}
	if ttl <= 0 {
		return "", Token{}, errors.New("a token needs an expiry")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, err := s.load()
	if err != nil {
		return "", Token{}, err
	}
	for _, t := range ts {
		if t.Name == name {
			return "", Token{}, fmt.Errorf("a token named %q exists; revoke it first", name)
		}
	}
	raw, err := randomToken()
	if err != nil {
		return "", Token{}, err
	}
	plain := TokenPrefix + raw
	sum := sha256.Sum256([]byte(plain))
	id := hex.EncodeToString(sum[:4])
	t := Token{ID: id, Name: name, Scope: scope, SHA256: hex.EncodeToString(sum[:]), Created: now.UTC(), Expires: now.Add(ttl).UTC()}
	if err := s.save(append(ts, t)); err != nil {
		return "", Token{}, err
	}
	return plain, t, nil
}

// List returns the stored tokens.
func (s *TokenStore) List() ([]Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Revoke deletes the token with this name or id.
func (s *TokenStore) Revoke(nameOrID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, err := s.load()
	if err != nil {
		return err
	}
	out := ts[:0]
	found := false
	for _, t := range ts {
		if t.Name == nameOrID || t.ID == nameOrID {
			found = true
			continue
		}
		out = append(out, t)
	}
	if !found {
		return fmt.Errorf("no token named %q", nameOrID)
	}
	return s.save(out)
}

// Lookup finds an unexpired token by its value, comparing hashes in
// constant time.
func (s *TokenStore) Lookup(plain string, now time.Time) (Token, bool) {
	if !strings.HasPrefix(plain, TokenPrefix) {
		return Token{}, false
	}
	ts, err := s.List()
	if err != nil {
		return Token{}, false
	}
	sum := sha256.Sum256([]byte(plain))
	want := hex.EncodeToString(sum[:])
	for _, t := range ts {
		if subtle.ConstantTimeCompare([]byte(t.SHA256), []byte(want)) == 1 && now.Before(t.Expires) {
			return t, true
		}
	}
	return Token{}, false
}
