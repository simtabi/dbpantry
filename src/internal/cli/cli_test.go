package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	environ := func() []string {
		return []string{"DBPANTRY_DIR=" + root, "MDB_ENV_FILE=" + filepath.Join(root, ".env.example")}
	}
	code := Execute(context.Background(), args, &out, &errb, environ)
	return code, out.String(), errb.String()
}

func TestManifestJSON(t *testing.T) {
	code, out, errs := run(t, "manifest")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Engines       []struct {
			Name string `json:"name"`
		} `json:"engines"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 1 || len(doc.Engines) < 6 {
		t.Fatalf("%+v", doc)
	}
}

func TestUsageErrorsExit64(t *testing.T) {
	for _, args := range [][]string{
		{"manifest", "-o", "xml"},
		{"manifest", "--matrix", "bogus"},
		{"endpoints", "-o", "xml"},
		{"fixture", "up", "--profiles", "ui", "--dry-run"},
		{"manifest", "--no-such-flag"},
		{"--root", "/", "manifest"},
	} {
		if code, _, _ := run(t, args...); code != ExitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, ExitUsage)
		}
	}
}

func TestEndpointsAndFixtureDryRun(t *testing.T) {
	code, out, errs := run(t, "endpoints", "-o", "json", "--profiles", "pg")
	if code != ExitOK || !strings.Contains(out, `"schema_version": 1`) {
		t.Fatalf("exit %d: %s %s", code, out, errs)
	}
	code, out, _ = run(t, "fixture", "up", "--profiles", "pg,pooler", "--dry-run")
	// The fixture runs as its own compose project on its own ports, never as the stack.
	for _, want := range []string{"COMPOSE_PROFILES=pg,pg-pooler COMPOSE_PROJECT_NAME=mdb-fixture MDB_FIXTURE=1 ", "MDB_PG_HOST_PORT=55000 ", "scripts/runtime up -f docker-compose.yml"} {
		if code != ExitOK || !strings.Contains(out, want) {
			t.Fatalf("exit %d, want %q in: %s", code, want, out)
		}
	}
	code, out, _ = run(t, "endpoints", "-o", "json", "--profiles", "pg", "--fixture", "--project", "t1")
	if code != ExitOK || !strings.Contains(out, `"fixture": true`) || !strings.Contains(out, "/run/secrets/fixture/pg_t1_user_password.txt") {
		t.Fatalf("endpoints --fixture: exit %d: %s", code, out)
	}
	code, out, _ = run(t, "project", "new", "--name", "t1", "--engine", "pg", "--fixture", "--dry-run")
	if code != ExitOK || !strings.Contains(out, "COMPOSE_PROJECT_NAME=mdb-fixture MDB_FIXTURE=1") || !strings.Contains(out, "scripts/new-project --name t1 --engine pg") {
		t.Fatalf("project new --fixture: exit %d: %s", code, out)
	}
	code, out, _ = run(t, "manifest", "check-helm")
	if code != ExitOK || !strings.Contains(out, "matches engines/") {
		t.Fatalf("exit %d: %s", code, out)
	}
}
