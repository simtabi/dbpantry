package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The cases mdb_env_unquote documents (scripts/env-lib.sh).
func TestUnquoteMatchesEnvLib(t *testing.T) {
	cases := map[string]string{
		`"a b"`:           "a b",
		`'a b'`:           "a b",
		`"a b" # note`:    "a b",
		`plain`:           "plain",
		`plain   `:        "plain",
		`  "x"`:           "x",
		`"a\"b\$c\\d"`:    `a"b$c\d`,
		`'$literal'`:      "$literal",
		`"`:               `"`,
		`pg,ui # comment`: "pg,ui # comment",
	}
	for in, want := range cases {
		if got := Unquote(in); got != want {
			t.Errorf("Unquote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEnvFileLastWins(t *testing.T) {
	got := ParseEnvFile([]byte("# c\nMDB_A=1\n MDB_B=ignored\nMDB_A=\"2 3\"\nexport MDB_C=4\n"))
	if got["MDB_A"] != "2 3" {
		t.Errorf("MDB_A = %q", got["MDB_A"])
	}
	if _, ok := got["MDB_B"]; ok {
		t.Error("an indented line is not an assignment to mdb_env_get")
	}
	if _, ok := got["MDB_C"]; ok {
		t.Error("an export line is not an assignment to mdb_env_get")
	}
}

func TestSettingsLayers(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("MDB_PUBLISH=direct\nMDB_BIND_ADDR=127.0.0.1\nMDB_PROFILES=\"pg,mysql\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environ := func() []string { return []string{"MDB_PUBLISH=proxy", "MDB_BIND_ADDR=0.0.0.0", "PROFILES=pg"} }
	s, err := Load(root, environ)
	if err != nil {
		t.Fatal(err)
	}
	if s.File("MDB_PUBLISH") != "direct" || s.Effective("MDB_PUBLISH") != "proxy" {
		t.Errorf("publish file=%q effective=%q", s.File("MDB_PUBLISH"), s.Effective("MDB_PUBLISH"))
	}
	if s.Effective("MDB_BIND_ADDR") != "0.0.0.0" || s.File("MDB_PROFILES") != "pg,mysql" || s.Env("PROFILES") != "pg" {
		t.Error("layers wrong")
	}
	if s.EnvFile != filepath.Join(root, ".env") {
		t.Errorf("env file %s", s.EnvFile)
	}
}

func TestQuoteMatchesEnvLib(t *testing.T) {
	cases := map[string]string{
		"plain":        "plain",
		"pg,ui":        `"pg,ui"`,
		"a b":          `"a b"`,
		`say "hi"`:     `"say \"hi\""`,
		"a$USER":       `"a\$USER"`,
		"127.0.0.1":    "127.0.0.1",
		"path/to:file": "path/to:file",
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShowOriginAndSet(t *testing.T) {
	root := t.TempDir()
	example := filepath.Join(root, ".env.example")
	env := filepath.Join(root, ".env")
	if err := os.WriteFile(example, []byte("# Core\n# ===\n# Profiles to start.\nMDB_PROFILES=pg\nMDB_PUBLISH=none\n#MDB_HOME=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env, []byte("MDB_PROFILES=\"pg,ui\"\nMDB_TZ=UTC\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environ := func() []string { return []string{"MDB_PUBLISH=direct"} }
	s, err := Load(root, environ)
	if err != nil {
		t.Fatal(err)
	}
	if s.OriginOf("MDB_PUBLISH") != OriginEnv || s.OriginOf("MDB_PROFILES") != OriginFile || s.OriginOf("MDB_HOME") != OriginNone {
		t.Errorf("origins publish=%s profiles=%s home=%s", s.OriginOf("MDB_PUBLISH"), s.OriginOf("MDB_PROFILES"), s.OriginOf("MDB_HOME"))
	}
	entries, err := s.Show(example)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Entry{}
	for _, e := range entries {
		by[e.Key] = e
	}
	if by["MDB_PROFILES"].Value != "pg,ui" || by["MDB_PROFILES"].Origin != OriginFile {
		t.Errorf("profiles = %+v", by["MDB_PROFILES"])
	}
	if by["MDB_PUBLISH"].Value != "direct" || by["MDB_PUBLISH"].Origin != OriginEnv {
		t.Errorf("publish = %+v", by["MDB_PUBLISH"])
	}
	if err := s.Set("MDB_PROFILES", "pg,mysql,ui"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("MDB_BIND_ADDR", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEnvFile(env)
	if err != nil {
		t.Fatal(err)
	}
	if got["MDB_PROFILES"] != "pg,mysql,ui" || got["MDB_BIND_ADDR"] != "127.0.0.1" || got["MDB_TZ"] != "UTC" {
		t.Errorf("after set: %+v", got)
	}
	raw, _ := os.ReadFile(env)
	if !strings.Contains(string(raw), `MDB_PROFILES="pg,mysql,ui"`) {
		t.Errorf("set did not quote the list: %s", raw)
	}
	if err := (&Settings{EnvFile: example}).Set("MDB_PROFILES", "x"); err == nil {
		t.Error("set must refuse .env.example")
	}
}

func TestSettingsFallBackToExample(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("MDB_PORT_BASE=1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(root, func() []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if s.File("MDB_PORT_BASE") != "1000" {
		t.Error("did not fall back to .env.example")
	}
	s, err = Load(root, func() []string { return []string{"MDB_ENV_FILE=" + filepath.Join(root, "none")} })
	if err != nil || s.File("MDB_PORT_BASE") != "" {
		t.Errorf("MDB_ENV_FILE naming a missing file reads as empty: %v", err)
	}
}
