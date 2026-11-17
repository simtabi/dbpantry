package cli

import (
	"bufio"
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
// dashboard CSS fix did not reach the running page).
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
	cmd := exec.Command("sh", "-c", string(m[1])) // #nosec G204 -- the Makefile's own find expression
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]bool{}
	for _, f := range strings.Fields(string(out)) {
		sources[filepath.Clean(f)] = true
	}

	embedded := 0
	err = filepath.WalkDir(filepath.Join(repo, "src"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		f, err := os.Open(p) // #nosec G304 -- a Go file of this module
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			patterns, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "//go:embed ")
			if !ok {
				continue
			}
			for _, pat := range strings.Fields(patterns) {
				matches, _ := filepath.Glob(filepath.Join(filepath.Dir(p), pat))
				for _, match := range matches {
					_ = filepath.WalkDir(match, func(q string, e os.DirEntry, err error) error {
						if err != nil || e.IsDir() {
							return err
						}
						embedded++
						rel, _ := filepath.Rel(repo, q)
						if !sources[rel] {
							t.Errorf("%s is embedded in the binary but is not in DBPANTRY_SOURCES, so editing it does not rebuild", rel)
						}
						return nil
					})
				}
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if embedded < 10 {
		t.Fatalf("found %d embedded files; the //go:embed scan read too little to mean anything", embedded)
	}
}
