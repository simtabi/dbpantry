package config

import (
	"os"
	"path/filepath"
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
