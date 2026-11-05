package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/v2"
)

// keyDelim is a byte no MDB_ key contains, so koanf never nests a key.
const keyDelim = "\x00"

// Settings are the two layers the toolkit reads, kept apart because the
// scripts themselves read them differently:
//
//   - File: the env file alone. scripts/gen-compose reads MDB_PUBLISH and
//     MDB_PORT_BASE this way, so the generated ports ignore the environment.
//   - Effective: the environment over the env file, the precedence Compose
//     uses when it interpolates ${MDB_BIND_ADDR}, ${MDB_<ENGINE>_HOST_PORT}
//     and the rest into the generated files.
type Settings struct {
	// EnvFile is the file the File layer was read from.
	EnvFile   string
	file      *koanf.Koanf
	effective *koanf.Koanf
	environ   func() []string
}

// Load reads the env file for a checkout, chosen as scripts/gen-compose
// chooses it: MDB_ENV_FILE, else <root>/.env, else <root>/.env.example.
// environ supplies the process environment (os.Environ when nil).
func Load(root string, environ func() []string) (*Settings, error) {
	if environ == nil {
		environ = os.Environ
	}
	path := lookup(environ, "MDB_ENV_FILE")
	if path == "" {
		path = filepath.Join(root, ".env")
		if _, err := os.Stat(path); err != nil {
			path = filepath.Join(root, ".env.example")
		}
	}
	values, err := ReadEnvFile(path)
	if err != nil {
		return nil, err
	}
	fileMap := make(map[string]any, len(values))
	for k, v := range values {
		fileMap[k] = v
	}

	s := &Settings{EnvFile: path, file: koanf.New(keyDelim), effective: koanf.New(keyDelim), environ: environ}
	if err := s.file.Load(confmap.Provider(fileMap, keyDelim), nil); err != nil {
		return nil, err
	}
	if err := s.effective.Load(confmap.Provider(fileMap, keyDelim), nil); err != nil {
		return nil, err
	}
	// Only MDB_ keys reach the effective layer: the toolkit reads no other
	// variable from the environment through it.
	if err := s.effective.Load(env.Provider(keyDelim, env.Opt{Prefix: "MDB_", EnvironFunc: environ}), nil); err != nil {
		return nil, err
	}
	return s, nil
}

// File returns a value from the env file only.
func (s *Settings) File(key string) string { return s.file.String(key) }

// Effective returns a value from the environment, else the env file.
func (s *Settings) Effective(key string) string { return s.effective.String(key) }

// Env returns a process environment variable outside the MDB_ namespace
// (PROFILES, COMPOSE_PROFILES), or "".
func (s *Settings) Env(key string) string { return lookup(s.environ, key) }

func lookup(environ func() []string, key string) string {
	for _, kv := range environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}
