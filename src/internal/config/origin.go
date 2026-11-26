package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Origin is where an effective value came from.
type Origin string

// A value is from the process environment, the env file, or neither.
const (
	OriginEnv  Origin = "env"
	OriginFile Origin = "file"
	OriginNone Origin = "unset"
)

// Entry is one setting as `config show` prints it.
type Entry struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Origin  Origin `json:"origin"`
	File    string `json:"file,omitempty"`
	Secret  bool   `json:"secret,omitempty"`
	Group   string `json:"group,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// OriginOf says whether the effective value of key comes from the
// environment, the env file, or nowhere.
func (s *Settings) OriginOf(key string) Origin {
	if lookup(s.environ, key) != "" {
		return OriginEnv
	}
	if s.file.Exists(key) {
		return OriginFile
	}
	return OriginNone
}

// Show lists every MDB_ key from the example file (and any extra keys set
// in the env file or the environment), sorted. A key whose name suggests a
// secret has its value redacted.
func (s *Settings) Show(examplePath string) ([]Entry, error) {
	keys, groups, comments, err := documentedKeys(examplePath)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Entry
	add := func(key string) {
		if seen[key] || !strings.HasPrefix(key, "MDB_") {
			return
		}
		seen[key] = true
		e := Entry{
			Key: key, Value: s.Effective(key), Origin: s.OriginOf(key),
			Group: groups[key], Comment: comments[key],
		}
		if e.Origin == OriginFile || e.Origin == OriginEnv {
			e.File = s.EnvFile
		}
		if LooksSecret(key) {
			e.Secret = true
			if e.Value != "" {
				e.Value = "***"
			}
		}
		out = append(out, e)
	}
	for _, k := range keys {
		add(k)
	}
	for _, k := range s.file.Keys() {
		add(k)
	}
	for _, line := range s.environ() {
		if k, _, ok := strings.Cut(line, "="); ok && strings.HasPrefix(k, "MDB_") {
			add(k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Set writes key=value into the env file, quoting per check 50, replacing
// an existing assignment or appending one. It refuses to edit .env.example.
func (s *Settings) Set(key, value string) error {
	if !strings.HasPrefix(key, "MDB_") || strings.ContainsAny(key, "=\n\r") {
		return fmt.Errorf("config set: %q is not an MDB_ key", key)
	}
	path := s.EnvFile
	if path == "" || strings.HasSuffix(path, ".env.example") {
		return fmt.Errorf("config set: no writable env file (run dbpantry init)")
	}
	b, err := os.ReadFile(path) // #nosec G304 -- the instance's own env file
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	line := key + "=" + Quote(value)
	var out []byte
	replaced := false
	if len(b) > 0 {
		for _, raw := range strings.SplitAfter(string(b), "\n") {
			trim := strings.TrimRight(raw, "\n\r")
			if m := envLineRe.FindStringSubmatch(trim); m != nil && m[1] == key {
				nl := ""
				if strings.HasSuffix(raw, "\n") {
					nl = "\n"
				}
				out = append(out, []byte(line+nl)...)
				replaced = true
				continue
			}
			out = append(out, []byte(raw)...)
		}
	}
	if !replaced {
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, []byte(line+"\n")...)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600) // #nosec G306 G703 -- the instance's own env file
}

// documentedKeys reads KEY= lines (and commented # KEY=) from .env.example,
// with the nearest section heading as the group and the preceding comment
// as the help text.
func documentedKeys(path string) (keys []string, groups, comments map[string]string, err error) {
	groups, comments = map[string]string{}, map[string]string{}
	b, err := os.ReadFile(path) // #nosec G304 -- the kit's .env.example
	if err != nil {
		return nil, nil, nil, err
	}
	group, pending := "", ""
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "# ==="):
			pending = ""
		case strings.HasPrefix(line, "# ") && !strings.Contains(line, "="):
			text := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if text != "" && !strings.HasPrefix(text, "===") {
				if pending == "" && looksLikeHeading(text) {
					group = text
				} else if pending == "" {
					pending = text
				} else {
					pending += " " + text
				}
			}
		default:
			key := ""
			if m := envLineRe.FindStringSubmatch(strings.TrimPrefix(line, "#")); m != nil && strings.HasPrefix(m[1], "MDB_") {
				key = m[1]
			} else if m := envLineRe.FindStringSubmatch(line); m != nil && strings.HasPrefix(m[1], "MDB_") {
				key = m[1]
			}
			if key != "" {
				keys = append(keys, key)
				groups[key] = group
				comments[key] = pending
				pending = ""
			} else if line == "" {
				pending = ""
			}
		}
	}
	return keys, groups, comments, nil
}

func looksLikeHeading(s string) bool {
	// Section titles in .env.example are short Title Case phrases.
	if len(s) > 60 || strings.HasSuffix(s, ".") {
		return false
	}
	words := strings.Fields(s)
	return len(words) >= 1 && len(words) <= 6
}

// LooksSecret reports whether a key's value is masked wherever it is shown:
// the Configuration page, and the arguments a dashboard job records
// (config.set's value in the job list and the audit log).
func LooksSecret(key string) bool {
	u := strings.ToUpper(key)
	for _, part := range []string{"PASSWORD", "SECRET", "TOKEN", "KEY", "HASH", "CREDENTIAL"} {
		if strings.Contains(u, part) {
			return true
		}
	}
	return false
}
