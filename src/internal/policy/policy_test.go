package policy

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minGoFiles is a floor on what the scan reads, so a walk that stops matching
// fails instead of reporting a clean module.
const minGoFiles = 15

func TestNoForbiddenConstructs(t *testing.T) {
	root := filepath.Join("..", "..") // src/
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasPrefix(path, filepath.Join(root, "internal", "policy")) {
			return nil
		}
		b, err := os.ReadFile(path) // #nosec G304 G122 -- a file of this module, from the walk
		if err != nil {
			return err
		}
		n++
		for _, r := range Violations(string(b)) {
			t.Errorf("%s contains %s: %s", path, r.Needle, r.Why)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < minGoFiles {
		t.Fatalf("scanned %d Go files, expected at least %d: the walk no longer reaches the module", n, minGoFiles)
	}
}

// The scanner can fail: every rule fires on a sample that contains it, and a
// clean sample passes.
func TestViolationsCanFire(t *testing.T) {
	for _, r := range Rules {
		if v := Violations("x := " + r.Needle); len(v) != 1 || v[0].Needle != r.Needle {
			t.Errorf("rule %q did not fire", r.Needle)
		}
	}
	if v := Violations("package x\nfunc f() {}\n"); len(v) != 0 {
		t.Errorf("clean sample reported %v", v)
	}
}
