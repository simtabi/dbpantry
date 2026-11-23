package envtype

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Entry is one named environment pointing at an instance home.
type Entry struct {
	Name    string    `json:"name"`
	Type    Kind      `json:"type"`
	Home    string    `json:"home"`
	Project string    `json:"project,omitempty"` // COMPOSE_PROJECT_NAME
	Created time.Time `json:"created"`
	Notes   string    `json:"notes,omitempty"`
}

type fileShape struct {
	Envs []Entry `json:"envs"`
}

// RegistryPath is the default envs.json next to the operator config home.
func RegistryPath(configHome string) string {
	return filepath.Join(configHome, "envs.json")
}

// Load reads the registry (empty when missing).
func Load(path string) ([]Entry, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- operator-chosen registry path
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc fileShape
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc.Envs, nil
}

// Save writes the registry (0600).
func Save(path string, envs []Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sort.SliceStable(envs, func(i, j int) bool {
		return envs[i].Name < envs[j].Name
	})
	raw, err := json.MarshalIndent(fileShape{Envs: envs}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

// ValidName reports whether a registry name is safe for paths and project names.
func ValidName(name string) bool {
	return nameRe.MatchString(name)
}

// Upsert adds or replaces an entry by name.
func Upsert(path string, e Entry) error {
	if !ValidName(e.Name) {
		return fmt.Errorf("environment name %q: want lowercase letters, digits and hyphens (max 48)", e.Name)
	}
	if _, err := Parse(string(e.Type)); err != nil {
		return err
	}
	if e.Home == "" {
		return fmt.Errorf("home is required")
	}
	abs, err := filepath.Abs(e.Home)
	if err != nil {
		return err
	}
	e.Home = abs
	if e.Created.IsZero() {
		e.Created = time.Now().UTC()
	}
	envs, err := Load(path)
	if err != nil {
		return err
	}
	found := false
	for i := range envs {
		if envs[i].Name == e.Name {
			e.Created = envs[i].Created
			envs[i] = e
			found = true
			break
		}
	}
	if !found {
		envs = append(envs, e)
	}
	return Save(path, envs)
}

// Get finds one entry by name.
func Get(path, name string) (Entry, error) {
	envs, err := Load(path)
	if err != nil {
		return Entry{}, err
	}
	for _, e := range envs {
		if e.Name == name {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("environment %q is not registered (dbpantry env ls)", name)
}

// Delete removes an entry (does not delete the home on disk).
func Delete(path, name string) error {
	envs, err := Load(path)
	if err != nil {
		return err
	}
	next := envs[:0]
	found := false
	for _, e := range envs {
		if e.Name == name {
			found = true
			continue
		}
		next = append(next, e)
	}
	if !found {
		return fmt.Errorf("environment %q is not registered", name)
	}
	return Save(path, next)
}

// ResolveHome returns the home for a name or an absolute path.
func ResolveHome(path, nameOrPath string) (string, error) {
	if nameOrPath == "" {
		return "", fmt.Errorf("name or path is empty")
	}
	if strings.Contains(nameOrPath, "/") || strings.HasPrefix(nameOrPath, ".") {
		return filepath.Abs(nameOrPath)
	}
	e, err := Get(path, nameOrPath)
	if err != nil {
		return "", err
	}
	return e.Home, nil
}

// DefaultProject suggests COMPOSE_PROJECT_NAME for a registry name.
func DefaultProject(name string) string {
	return "mdb-" + name
}

// OperatorHome is where envs.json and per-env homes live (not one instance's
// MDB_HOME). Override with DBPANTRY_HOME, else XDG_CONFIG_HOME/dbpantry, else
// ~/.config/dbpantry.
func OperatorHome() string {
	if h := strings.TrimSpace(os.Getenv("DBPANTRY_HOME")); h != "" {
		return h
	}
	if h := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); h != "" {
		return filepath.Join(h, "dbpantry")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "dbpantry")
	}
	return filepath.Join(".", ".config", "dbpantry")
}

// Names returns registered environment names (empty when the registry is missing).
func Names(path string) ([]string, error) {
	envs, err := Load(path)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(envs))
	for _, e := range envs {
		out = append(out, e.Name)
	}
	return out, nil
}
