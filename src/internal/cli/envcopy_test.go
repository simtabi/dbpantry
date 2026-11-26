package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// env copy and env migrate run their export in the source home and their
// restore in the destination, each with that home's own stack: its Compose
// project from its .env and its profiles from its MDB_PROFILES. The caller's
// COMPOSE_PROJECT_NAME, MDB_ENV_FILE and COMPOSE_PROFILES outrank a home's
// .env, so carrying them into both steps pointed both at one stack, and the
// copy reported success (2026-11-24 audit, K3).
func TestEnvCopyRunsEachStepInItsOwnHome(t *testing.T) {
	for _, verb := range []string{"copy", "migrate"} {
		t.Run(verb, func(t *testing.T) {
			root, log := stubRoot(t)
			// The two steps record the variables that pick a stack.
			rec := `#!/usr/bin/env bash
printf '%s home=%s project=%s envfile=%s profiles=%s\n' "${0##*/}" "${MDB_HOME:-}" "${COMPOSE_PROJECT_NAME:-}" "${MDB_ENV_FILE:-}" "${COMPOSE_PROFILES:-}" >> "$STUB_LOG"
`
			for _, s := range []string{"transfer", "restore"} {
				if err := os.WriteFile(filepath.Join(root, "scripts", s), []byte(rec), 0o700); err != nil { // #nosec G306 -- an executable stub in a test directory
					t.Fatal(err)
				}
			}
			src, dst := t.TempDir(), t.TempDir()
			for home, body := range map[string]string{
				src: "COMPOSE_PROJECT_NAME=mdb-src\nMDB_PROFILES=pg\n",
				dst: "COMPOSE_PROJECT_NAME=mdb-dst\nMDB_PROFILES=mysql\n",
			} {
				if err := os.WriteFile(filepath.Join(home, ".env"), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			tmp := os.TempDir()
			code, _, stderr, calls := runStub(t, root, log, []string{
				"TMPDIR=" + tmp,
				"COMPOSE_PROJECT_NAME=mdb-caller",
				"COMPOSE_PROFILES=pg,ui",
				"DBPANTRY_HOME=" + t.TempDir(),
			}, "", "env", verb, "--from", src, "--to", dst, "--engine", "pg", "--db", "app", "--confirm", "app")
			if code != ExitOK {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			want := []string{
				"transfer home=" + src + " project= envfile= profiles=pg",
				"restore home=" + dst + " project= envfile= profiles=mysql",
			}
			if strings.Join(calls, "\n") != strings.Join(want, "\n") {
				t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}
