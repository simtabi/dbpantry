package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// make rebuilds build/<os>/dbpantry only when one of DBPANTRY_SOURCES is
// newer. Every file a //go:embed directive pulls in has to be among them,
// or an edited template or stylesheet ships stale (found 2026-11-17, when a
// dashboard CSS fix did not reach the running page). The embedded files are
// the ones the Go toolchain itself reports (go list's EmbedFiles), so the
// test reads no directive by hand.
func TestMakeRebuildsOnEveryEmbeddedFile(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	mk, err := os.ReadFile(filepath.Join(repo, "Makefile")) // #nosec G304 -- the repository's own Makefile
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^DBPANTRY_SOURCES := \$\(shell (.+)\)$`).FindSubmatch(mk)
	if m == nil {
		t.Fatal("no DBPANTRY_SOURCES := $(shell …) line in the Makefile")
	}
	find := exec.CommandContext(t.Context(), "sh", "-c", string(m[1])) // #nosec G204 -- the Makefile's own find expression
	find.Dir = repo
	out, err := find.Output()
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]bool{}
	for _, f := range strings.Fields(string(out)) {
		sources[filepath.Clean(f)] = true
	}

	list := exec.CommandContext(t.Context(), "go", "list", "-f", `{{range .EmbedFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}`, "./...")
	list.Dir = filepath.Join(repo, "src")
	embeddedOut, err := list.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	embedded := strings.Fields(string(embeddedOut))
	if len(embedded) < 10 {
		t.Fatalf("go list reported %d embedded files; too few to mean anything", len(embedded))
	}
	for _, f := range embedded {
		rel, err := filepath.Rel(repo, f)
		if err != nil {
			t.Fatal(err)
		}
		if !sources[rel] {
			t.Errorf("%s is embedded in the binary but is not in DBPANTRY_SOURCES, so editing it does not rebuild", rel)
		}
	}
}
