package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubRoot is a checkout whose scripts record how they were called: one line
// per run, "<script> <args>|stdin=<first line>|profiles=<COMPOSE_PROFILES>",
// and exit with $STUB_EXIT. It proves each command runs the script its make
// target ran, with the arguments make passed (docs/reference/parity.tsv).
func stubRoot(t *testing.T) (root, log string) {
	t.Helper()
	root = t.TempDir()
	log = filepath.Join(root, "calls.log")
	for _, d := range []string{"engines", "scripts/verify"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	stub := `#!/usr/bin/env bash
in=""; if [[ ! -t 0 ]]; then IFS= read -r in || true; fi
printf '%s %s|stdin=%s|profiles=%s|tags=%s|checks=%s\n' "${0#"$STUB_ROOT/"}" "$*" "$in" "${COMPOSE_PROFILES:-}" "${VERIFY_TAGS:-}" "${VERIFY_CHECKS:-}" >> "$STUB_LOG"
exit "${STUB_EXIT:-0}"
`
	for _, s := range []string{
		"init", "self-update", "check-env", "render-config", "gen-compose", "prod-overlay",
		"runtime", "exposure-notice", "destroy", "new-project", "project", "backup", "verify-backups",
		"restore", "pitr", "transfer", "upgrade", "ha", "certs", "certs-renew", "rotate-secrets",
		"build", "scan", "lint.sh", "release-notes", "verify/run.sh",
	} {
		if err := os.WriteFile(filepath.Join(root, "scripts", s), []byte(stub), 0o700); err != nil { // #nosec G306 -- an executable stub in a test directory
			t.Fatal(err)
		}
	}
	// scripts/profiles expands its argument; the stub returns it unchanged.
	files := map[string]string{
		"scripts/profiles":       "#!/usr/bin/env bash\nprintf '%s\\n' \"$1\"\n",
		"scripts/engine-lib.sh":  "",
		"scripts/runtime-lib.sh": "mdb_project_name() { echo mdbstub; }\nmdb_service_running() { [[ \"$1\" == mysql-pooler ]]; }\n",
		"scripts/env-lib.sh":     "mdb_image_prefix() { echo localhost; }\nmdb_backup_dir() { printf '%s/backups' \"$1\"; }\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o700); err != nil { // #nosec G306 -- a stub in a test directory
			t.Fatal(err)
		}
	}
	return root, log
}

func runStub(t *testing.T, root, log string, extraEnv []string, stdin string, args ...string) (int, string, string, []string) {
	t.Helper()
	_ = os.Remove(log)
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	env := append([]string{
		"PATH=" + os.Getenv("PATH"),
		"DBPANTRY_DIR=" + root,
		"MDB_ENGINES_DIR=" + filepath.Join(repo, "engines"),
		"MDB_ENV_FILE=" + filepath.Join(repo, ".env.example"),
		"STUB_ROOT=" + root, "STUB_LOG=" + log,
	}, extraEnv...)
	// The scripts inherit the process environment: give them exactly env.
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	var out, errb bytes.Buffer
	a := &app{stdin: strings.NewReader(stdin), stdout: &out, stderr: &errb, environ: os.Environ}
	cmd := a.rootCmd()
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	code := ExitOK
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		code = writeError(&errb, err, a.jsonErrors)
	}
	b, _ := os.ReadFile(log) // #nosec G304 -- the test's own log
	var calls []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			calls = append(calls, l)
		}
	}
	return code, out.String(), errb.String(), calls
}

// Each case is a make target's recipe after phase 2: the command make now
// runs, and the script calls make ran before (Makefile on main at 00726f1).
func TestCommandsRunTheirScripts(t *testing.T) {
	root, log := stubRoot(t)
	p := "|stdin=|profiles=pg,ui|tags=|checks="
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"init"}, []string{"scripts/init " + p}},
		{[]string{"init", "--prod"}, []string{"scripts/init --prod" + p}},
		{[]string{"self-update"}, []string{"scripts/self-update " + p}},
		{[]string{"config", "validate"}, []string{"scripts/check-env " + p}},
		{[]string{"config", "render"}, []string{"scripts/render-config " + p, "scripts/gen-compose " + p}},
		{[]string{"config", "prod-overlay", ".env.prod"}, []string{"scripts/prod-overlay .env.prod" + p}},
		{[]string{"up"}, []string{"scripts/check-env --quiet" + p, "scripts/render-config " + p, "scripts/gen-compose " + p, "scripts/runtime up" + p, "scripts/exposure-notice " + p}},
		{[]string{"up", "--profiles", "pg,mysql"}, []string{"scripts/check-env --quiet|stdin=|profiles=pg,mysql|tags=|checks=", "scripts/render-config |stdin=|profiles=pg,mysql|tags=|checks=", "scripts/gen-compose |stdin=|profiles=pg,mysql|tags=|checks=", "scripts/runtime up|stdin=|profiles=pg,mysql|tags=|checks=", "scripts/exposure-notice |stdin=|profiles=pg,mysql|tags=|checks="}},
		{[]string{"up", "--profile", "test"}, []string{"scripts/render-config " + p, "scripts/gen-compose " + p, "scripts/runtime up -f docker-compose.yml -f compose.engines.yml -f compose.test.yml" + p}},
		{[]string{"down"}, []string{"scripts/runtime down" + p}},
		{[]string{"status"}, []string{"scripts/runtime status" + p}},
		{[]string{"restart", "pg", "pg-pooler"}, []string{"scripts/runtime restart pg pg-pooler" + p}},
		{[]string{"logs"}, []string{"scripts/runtime logs -f --tail=100" + p}},
		{[]string{"forward"}, []string{"scripts/runtime forward" + p}},
		{[]string{"destroy", "--all"}, []string{"scripts/destroy --all" + p}},
		{[]string{"destroy", "--volume", "mdb_pg17_data", "--confirm", "mdb_pg17_data"}, []string{"scripts/destroy --volume mdb_pg17_data|stdin=mdb_pg17_data|profiles=pg,ui|tags=|checks="}},
		{[]string{"runtime", "info"}, []string{"scripts/runtime info" + p}},
		{[]string{"shell", "pg", "--", "psql", "-U", "postgres"}, []string{"scripts/runtime exec pg psql -U postgres" + p}},
		{[]string{"tools", "--image", "localhost/dbpantry-cli:dev"}, []string{"scripts/runtime ctr run --rm -it --network mdbstub_net -v " + root + "/certs:/certs:ro -v " + root + "/backups:/work/backups localhost/dbpantry-cli:dev" + p}},
		{[]string{"project", "new", "--name", "shop", "--engine", "pg"}, []string{"scripts/new-project --name shop --engine pg" + p}},
		{[]string{"project", "new", "--name", "shop", "--engine", "mysql"}, []string{"scripts/new-project --name shop --engine mysql" + p, "scripts/runtime compose restart mysql-pooler" + p}},
		{[]string{"project", "rotate", "--name", "shop", "--engine", "pg", "--confirm", "shop"}, []string{"scripts/new-project --rotate --name shop --engine pg|stdin=shop|profiles=pg,ui|tags=|checks="}},
		{[]string{"project", "rotate", "--name", "shop", "--engine", "mysql", "--confirm", "shop"}, []string{"scripts/new-project --rotate --name shop --engine mysql|stdin=shop|profiles=pg,ui|tags=|checks=", "scripts/runtime compose restart mysql-pooler" + p}},
		{[]string{"project", "drop", "--name", "shop", "--engine", "pg", "--confirm", "shop"}, []string{"scripts/project drop --name shop --engine pg|stdin=shop|profiles=pg,ui|tags=|checks="}},
		{[]string{"project", "drop", "--name", "shop", "--engine", "mysql", "--confirm", "shop", "--no-pooler-sync"}, []string{"scripts/project drop --name shop --engine mysql|stdin=shop|profiles=pg,ui|tags=|checks="}},
		{[]string{"backup", "run", "--engine", "pg", "--db", "shop"}, []string{"scripts/backup --engine pg --db shop" + p}},
		{[]string{"backup", "run", "--all"}, []string{"scripts/backup --all" + p}},
		{[]string{"backup", "verify"}, []string{"scripts/verify-backups " + p}},
		{[]string{"restore", "--engine", "pg", "--db", "shop", "--file", "x.sql.zst", "--confirm", "shop"}, []string{"scripts/restore --engine pg --db shop --file x.sql.zst|stdin=shop|profiles=pg,ui|tags=|checks="}},
		{[]string{"pitr", "info", "--engine", "pg"}, []string{"scripts/pitr info --engine pg" + p}},
		{[]string{"pitr", "backup", "--engine", "pg", "--type", "full"}, []string{"scripts/pitr backup --engine pg --type full" + p}},
		{[]string{"pitr", "restore", "--engine", "pg", "--to", "2026-11-16 10:00"}, []string{"scripts/pitr restore --engine pg --to 2026-11-16 10:00" + p}},
		{[]string{"data", "import", "--engine", "pg", "--db", "shop", "--from", "in.sql"}, []string{"scripts/transfer import --engine pg --db shop --from in.sql" + p}},
		{[]string{"data", "export", "--engine", "pg", "--db", "shop"}, []string{"scripts/transfer export --engine pg --db shop" + p}},
		{[]string{"upgrade", "--engine", "pg", "--from", "17", "--to", "18"}, []string{"scripts/upgrade --engine pg --from 17 --to 18" + p}},
		{[]string{"ha", "status"}, []string{"scripts/ha status" + p}},
		{[]string{"ha", "failover", "--node", "patroni2", "--confirm", "mdb-pg"}, []string{"scripts/ha failover --node patroni2|stdin=mdb-pg|profiles=pg,ui|tags=|checks="}},
		{[]string{"ha", "reinit", "--node", "patroni2"}, []string{"scripts/ha reinit --node patroni2" + p}},
		{[]string{"pooler", "sync", "mysql"}, []string{"scripts/runtime compose restart mysql-pooler" + p}},
		{[]string{"pooler", "sync", "mariadb"}, nil},
		{[]string{"certs", "init"}, []string{"scripts/certs " + p}},
		{[]string{"certs", "renew"}, []string{"scripts/certs-renew " + p}},
		{[]string{"secrets", "rotate", "--yes"}, []string{"scripts/rotate-secrets --yes" + p, "scripts/runtime compose restart mysql-pooler" + p}},
		{[]string{"secrets", "rotate", "--yes", "--engine", "pg"}, []string{"scripts/rotate-secrets --yes --engine pg" + p}},
		{[]string{"secrets", "rotate", "--yes", "--engine", "mysql"}, []string{"scripts/rotate-secrets --yes --engine mysql" + p, "scripts/runtime compose restart mysql-pooler" + p}},
		{[]string{"images", "build"}, []string{"scripts/build " + p}},
		{[]string{"images", "build", "pg", "mysql"}, []string{"scripts/build pg mysql" + p}},
		{[]string{"images", "scan", "pg"}, []string{"scripts/scan pg" + p}},
		{[]string{"verify", "--checks", "21,28"}, []string{"scripts/verify/run.sh |stdin=|profiles=pg,ui|tags=|checks=21,28"}},
		{[]string{"verify", "--fast"}, []string{"scripts/verify/run.sh |stdin=|profiles=pg,ui|tags=fast|checks="}},
		{[]string{"verify", "--structure"}, []string{"scripts/verify/run.sh --structure-only" + p}},
		{[]string{"dev", "lint"}, []string{"scripts/lint.sh " + p}},
		{[]string{"dev", "release-notes", "Unreleased"}, []string{"scripts/release-notes Unreleased" + p}},
	}
	for _, tc := range cases {
		code, _, errs, calls := runStub(t, root, log, nil, "", tc.args...)
		if code != ExitOK {
			t.Errorf("%v: exit %d: %s", tc.args, code, errs)
			continue
		}
		if strings.Join(calls, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%v ran\n  %s\nwant\n  %s", tc.args, strings.Join(calls, "\n  "), strings.Join(tc.want, "\n  "))
		}
	}
	if len(cases) < 40 {
		t.Fatalf("only %d cases", len(cases))
	}
}

// make exports COMPOSE_PROFILES; the binary passes it on untouched.
func TestMakeProfilesPassThrough(t *testing.T) {
	root, log := stubRoot(t)
	_, _, _, calls := runStub(t, root, log, []string{"COMPOSE_PROFILES=pg,pg-pooler"}, "", "down")
	if len(calls) != 1 || !strings.Contains(calls[0], "|profiles=pg,pg-pooler|") {
		t.Fatalf("%v", calls)
	}
}

func TestScriptStatusPassesThrough(t *testing.T) {
	root, log := stubRoot(t)
	for _, want := range []int{1, 3, 42} {
		code, _, _, _ := runStub(t, root, log, []string{"STUB_EXIT=" + itoa(want)}, "", "config", "validate")
		if code != want {
			t.Errorf("script exited %d, binary %d", want, code)
		}
	}
	// A failing step stops the rest: up stops after check-env.
	_, _, _, calls := runStub(t, root, log, []string{"STUB_EXIT=1"}, "", "up")
	if len(calls) != 1 {
		t.Fatalf("up ran %d step(s) after a failure: %v", len(calls), calls)
	}
}

func TestConfirmation(t *testing.T) {
	root, log := stubRoot(t)
	// --no-input with no --confirm refuses before running anything: exit 7.
	for _, args := range [][]string{
		{"--no-input", "destroy", "--all"},
		{"--no-input", "restore", "--engine", "pg", "--db", "shop", "--file", "x"},
		{"--no-input", "project", "drop", "--name", "shop"},
		{"--no-input", "project", "rotate", "--name", "shop"},
		{"--no-input", "ha", "failover"},
		{"--no-input", "secrets", "rotate"},
		{"--no-input", "pitr", "restore", "--to", "x"},
		{"--no-input", "upgrade", "--engine", "pg", "--from", "17", "--to", "18"},
	} {
		code, _, _, calls := runStub(t, root, log, nil, "", args...)
		if code != ExitConfirm || len(calls) != 0 {
			t.Errorf("%v: exit %d, ran %v; want 7 and nothing run", args, code, calls)
		}
	}
	// What already satisfied the prompt still does.
	if code, _, _, _ := runStub(t, root, log, []string{"CONFIRM=yes"}, "", "--no-input", "restore", "--engine", "pg", "--db", "shop", "--file", "x"); code != ExitOK {
		t.Errorf("CONFIRM=yes no longer satisfies restore: exit %d", code)
	}
	// Without --no-input the script reads the operator's answer.
	_, _, _, calls := runStub(t, root, log, nil, "DESTROY ALL\n", "destroy", "--all")
	if len(calls) != 1 || !strings.Contains(calls[0], "|stdin=DESTROY ALL|") {
		t.Fatalf("the prompt did not reach the operator: %v", calls)
	}
}

func TestDryRunRunsNothing(t *testing.T) {
	root, log := stubRoot(t)
	code, out, _, calls := runStub(t, root, log, nil, "", "--dry-run", "restore", "--engine", "pg", "--db", "my shop", "--file", "x", "--confirm", "my shop")
	if code != ExitOK || len(calls) != 0 {
		t.Fatalf("exit %d, ran %v", code, calls)
	}
	if !strings.Contains(out, "scripts/restore --engine pg --db 'my shop' --file x <<< 'my shop'") {
		t.Fatalf("dry run printed %q", out)
	}
}

func TestUsageAndJSONErrors(t *testing.T) {
	root, log := stubRoot(t)
	for _, args := range [][]string{
		{"destroy"},
		{"destroy", "--all", "--volume", "v"},
		{"shell", "pg"},
		{"up", "--profile", "prod"},
		{"backup", "bogus"},
		{"version", "-o", "csv"},
		{"pooler", "sync", "nosuchengine"},
	} {
		if code, _, _, calls := runStub(t, root, log, nil, "", args...); code != ExitUsage || len(calls) != 0 {
			t.Errorf("%v: exit %d, ran %v; want 64 and nothing run", args, code, calls)
		}
	}
	code, _, errs, _ := runStub(t, root, log, nil, "", "endpoints", "-o", "json", "--root", "/nonexistent\x1b[2J")
	var doc struct {
		Error struct {
			Code    string `json:"code"`
			Exit    int    `json:"exit"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(errs), &doc); err != nil || doc.Error.Code != "usage" || doc.Error.Exit != code || code != ExitUsage {
		t.Fatalf("exit %d, not a JSON usage error (%v): %q", code, err, errs)
	}
	if strings.ContainsRune(doc.Error.Message, 0x1b) {
		t.Fatalf("an escape sequence reached the terminal: %q", doc.Error.Message)
	}
}

func TestSanitize(t *testing.T) {
	if got := sanitize("ok\x1b]0;owned\x07 db\x9b2J\tx\n"); got != "ok]0;owned db2J\tx\n" {
		t.Fatalf("%q", got)
	}
}

func TestVersionFormats(t *testing.T) {
	for args, want := range map[string]string{"version": "dbpantry ", "version --json": `"version"`, "version -o yaml": "version: "} {
		code, out, _ := run(t, strings.Fields(args)...)
		if code != ExitOK || !strings.Contains(out, want) {
			t.Errorf("%s: exit %d, %q", args, code, out)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
