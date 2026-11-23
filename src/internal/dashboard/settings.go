package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Auth modes (management-plan.md §6, decision 2).
const (
	// AuthToken: dbpantry dashboard prints a URL carrying a one-time token,
	// exchanged for a session cookie.
	AuthToken = "token"
	// AuthPassword: a login page asks for the password in
	// secrets/dashboard_password.txt.
	AuthPassword = "password"
	// AuthNone: no login; loopback only.
	AuthNone = "none"
)

// Confirmation policies (decision 3). A destructive action always needs its
// typed confirmation; the policy says which others get a modal first.
const (
	// ConfirmAll asks before every action that changes something.
	ConfirmAll = "all"
	// ConfirmDestructive asks only before destructive ones.
	ConfirmDestructive = "destructive"
)

// Settings are the dashboard's own settings. They are layered, each layer
// overriding the one before (refactor plan §8.8):
//
//  1. the kit's config/dashboard.json, shipped defaults;
//  2. the instance's $MDB_HOME/dashboard.json, which `init` copies from it;
//  3. MDB_DASHBOARD_* keys in the instance's .env;
//  4. the command's flags.
//
// The dashboard never writes the kit's file.
type Settings struct {
	Auth         string `json:"auth"`
	AllowDestroy bool   `json:"allow_destroy"`
	Listen       string `json:"listen"`
	Confirm      string `json:"confirm"`
	// SessionHours is how long a browser session lasts.
	SessionHours int `json:"session_hours"`
}

// Defaults are used for any key no layer sets.
var Defaults = Settings{Auth: AuthToken, Listen: "127.0.0.1:8484", Confirm: ConfirmAll, SessionHours: 12}

// LoadSettings layers the JSON files that exist (later wins), then env,
// which looks a key up in the instance's .env.
func LoadSettings(files []string, env func(string) string) (Settings, error) {
	s := Defaults
	for _, f := range files {
		b, err := os.ReadFile(f) // #nosec G304 -- the kit's and the instance's own settings files
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return s, err
		}
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			return s, fmt.Errorf("%s: %w", f, err)
		}
	}
	if v := env("MDB_DASHBOARD_AUTH"); v != "" {
		s.Auth = v
	}
	if v := env("MDB_DASHBOARD_LISTEN"); v != "" {
		s.Listen = v
	}
	if v := env("MDB_DASHBOARD_CONFIRM"); v != "" {
		s.Confirm = v
	}
	if v := env("MDB_DASHBOARD_ALLOW_DESTROY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return s, fmt.Errorf("MDB_DASHBOARD_ALLOW_DESTROY=%q: want true or false", v)
		}
		s.AllowDestroy = b
	}
	return s, nil
}

// ListenAddr is where the dashboard binds: TCP host:port or a unix socket
// path (design.md D-99). Unix is for same-host projects talking over the
// filesystem; DB unix sockets (MDB_SOCKETS) remain a separate transport.
type ListenAddr struct {
	Network string // "tcp" or "unix"
	Address string // host:port or absolute socket path
	Local   bool   // loopback TCP or any unix — auth none OK without TLS
}

// ParseListen accepts "127.0.0.1:8484", "unix:///run/dbpantry.sock",
// "unix:/run/dbpantry.sock", or an absolute path ending in ".sock".
func ParseListen(raw string) (ListenAddr, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ListenAddr{}, fmt.Errorf("listen is empty")
	}
	switch {
	case strings.HasPrefix(raw, "unix://"):
		path := strings.TrimPrefix(raw, "unix://")
		if path == "" || !strings.HasPrefix(path, "/") {
			return ListenAddr{}, fmt.Errorf("listen %q: unix:// needs an absolute path", raw)
		}
		return ListenAddr{Network: "unix", Address: path, Local: true}, nil
	case strings.HasPrefix(raw, "unix:"):
		path := strings.TrimPrefix(raw, "unix:")
		if path == "" || !strings.HasPrefix(path, "/") {
			return ListenAddr{}, fmt.Errorf("listen %q: unix: needs an absolute path", raw)
		}
		return ListenAddr{Network: "unix", Address: path, Local: true}, nil
	case strings.HasPrefix(raw, "/") && strings.HasSuffix(raw, ".sock"):
		return ListenAddr{Network: "unix", Address: raw, Local: true}, nil
	}
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		return ListenAddr{}, fmt.Errorf("listen %q: %w", raw, err)
	}
	return ListenAddr{Network: "tcp", Address: raw, Local: Loopback(host)}, nil
}

// Validate refuses a combination that would expose the dashboard: an
// unknown mode or policy, no login beyond loopback, or any listen address
// beyond loopback without TLS (§3.3: refused, not just warned about).
func (s Settings) Validate(tls bool) error {
	switch s.Auth {
	case AuthToken, AuthPassword, AuthNone:
	default:
		return fmt.Errorf("auth %q: want token, password or none", s.Auth)
	}
	switch s.Confirm {
	case ConfirmAll, ConfirmDestructive:
	default:
		return fmt.Errorf("confirm %q: want all or destructive", s.Confirm)
	}
	if s.SessionHours <= 0 {
		return fmt.Errorf("session_hours %d: want at least 1", s.SessionHours)
	}
	addr, err := ParseListen(s.Listen)
	if err != nil {
		return err
	}
	if addr.Local {
		return nil
	}
	if s.Auth == AuthNone {
		return fmt.Errorf("listen %s: auth none is allowed on loopback or a unix socket only", s.Listen)
	}
	if !tls {
		return fmt.Errorf("listen %s: beyond loopback the dashboard needs TLS (--tls-cert and --tls-key)", s.Listen)
	}
	return nil
}

// Loopback reports whether host is localhost or a loopback address.
func Loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
