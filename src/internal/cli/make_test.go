package cli

import (
	"bytes"
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
	// The kit (design.md D-85) is embedded as copies under kitDir; what make
	// must watch is each copy's original, which KIT_FILES lists.
	kitFiles := makeShellList(t, repo, mk, "KIT_FILES")
	const kitDir = "src/internal/kit/files/"
	inKit := 0
	for _, f := range embedded {
		rel, err := filepath.Rel(repo, f)
		if err != nil {
			t.Fatal(err)
		}
		rel = filepath.ToSlash(rel)
		if orig, ok := strings.CutPrefix(rel, kitDir); ok {
			if orig == "README.md" || orig == "MANIFEST" {
				continue
			}
			inKit++
			if !kitFiles[orig] {
				t.Errorf("%s is embedded in the kit but %s is not in KIT_FILES, so editing it does not rebuild", rel, orig)
				continue
			}
			// A copy that differs from its original ships a stale kit.
			want, err := os.ReadFile(filepath.Join(repo, orig)) // #nosec G304 -- a tracked file of the repository
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(f) // #nosec G304 -- a file go list reports as embedded
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("the kit's copy of %s is stale; run make kit", orig)
			}
			continue
		}
		if !sources[rel] {
			t.Errorf("%s is embedded in the binary but is not in DBPANTRY_SOURCES, so editing it does not rebuild", rel)
		}
	}
	// Every KIT_FILES file is in the kit, or the binary ships without it.
	// make go-test generates the kit first and says so; a bare `go test`
	// without `make kit` has none to check.
	if inKit == 0 {
		if os.Getenv("DBPANTRY_KIT_REQUIRED") == "1" {
			t.Fatal("no kit is embedded although make generated one; the embed pattern matches nothing")
		}
		t.Skip("no kit generated (run make kit, or make go-test, which does)")
	}
	if len(kitFiles) < 100 {
		t.Fatalf("KIT_FILES lists %d files; too few to be the kit", len(kitFiles))
	}
	if inKit != len(kitFiles) {
		t.Errorf("the kit embeds %d files but KIT_FILES lists %d; run make kit", inKit, len(kitFiles))
	}
}

// makeShellList runs the Makefile's NAME := $(shell ...) line and returns the
// words it prints.
func makeShellList(t *testing.T, repo string, mk []byte, name string) map[string]bool {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + name + ` := \$\(shell (.+)\)$`).FindSubmatch(mk)
	if m == nil {
		t.Fatalf("no %s := $(shell …) line in the Makefile", name)
	}
	// $(KIT_PATHS) is make's, not the shell's: expand it from its own line.
	cmd := string(m[1])
	for _, v := range regexp.MustCompile(`\$\(([A-Z_]+)\)`).FindAllStringSubmatch(cmd, -1) {
		vm := regexp.MustCompile(`(?m)^` + v[1] + ` := (.+)$`).FindSubmatch(mk)
		if vm == nil {
			t.Fatalf("%s uses $(%s), which the Makefile does not set with :=", name, v[1])
		}
		cmd = strings.ReplaceAll(cmd, v[0], string(vm[1]))
	}
	c := exec.CommandContext(t.Context(), "sh", "-c", cmd) // #nosec G204 -- the Makefile's own expression
	c.Dir = repo
	out, err := c.Output()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	set := map[string]bool{}
	for _, f := range strings.Fields(string(out)) {
		set[f] = true
	}
	return set
}
