package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simtabi/dbpantry/src/internal/kit"
)

// needKit skips without the generated kit, unless make said there is one.
func needKit(t *testing.T) {
	t.Helper()
	if _, err := kit.Embedded(); errors.Is(err, kit.ErrNoKit) {
		if os.Getenv("DBPANTRY_KIT_REQUIRED") == "1" {
			t.Fatal("make generated the kit, but the binary embeds none")
		}
		t.Skip("no kit generated (run make kit, or make go-test, which does)")
	}
}

// runBare runs the CLI the way a user with only the binary does: in an empty
// directory, with no DBPANTRY_DIR, and HOME pointing at a fresh directory.
func runBare(t *testing.T, env []string, args ...string) (code int, stdout, stderr, home string) {
	t.Helper()
	home = t.TempDir()
	t.Chdir(t.TempDir())
	environ := func() []string { return append([]string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}, env...) }
	var out, errb bytes.Buffer
	code = Execute(context.Background(), args, &out, &errb, environ)
	return code, out.String(), errb.String(), home
}

// TestRootStepFiveExtractsTheKit is step 5 of root resolution (design.md
// D-85): with no checkout anywhere, the built-in kit is extracted to
// ~/.config/dbpantry/kit/<version> and the scripts run from there, with
// MDB_HOME at ~/.config/dbpantry.
func TestRootStepFiveExtractsTheKit(t *testing.T) {
	needKit(t)
	code, out, errs, home := runBare(t, nil, "--dry-run", "up")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errs)
	}
	kitDir := filepath.Join(home, ".config", "dbpantry", "kit", kit.DirName(version))
	if _, err := os.Stat(filepath.Join(kitDir, kit.ManifestName)); err != nil {
		t.Fatalf("no kit extracted at %s: %v", kitDir, err)
	}
	if _, err := os.Stat(filepath.Join(kitDir, "scripts", "init")); err != nil {
		t.Fatalf("the extracted kit has no scripts/init: %v", err)
	}
	if !strings.Contains(out, "scripts/runtime up") {
		t.Errorf("the dry run does not plan scripts/runtime up:\n%s", out)
	}
	if !strings.Contains(errs, "extracted the built-in kit") {
		t.Errorf("the first run did not say it extracted the kit: %s", errs)
	}
}

// TestRootStepFiveHonoursTheHome: --home, then DBPANTRY_HOME, choose where
// the kit and the instance go.
func TestRootStepFiveHonoursTheHome(t *testing.T) {
	needKit(t)
	flagHome := filepath.Join(t.TempDir(), "flag")
	envHome := filepath.Join(t.TempDir(), "env")
	for _, tc := range []struct {
		name string
		env  []string
		args []string
		want string
	}{
		{"--home", []string{"DBPANTRY_HOME=" + envHome}, []string{"--home", flagHome, "--dry-run", "up"}, flagHome},
		{"DBPANTRY_HOME", []string{"DBPANTRY_HOME=" + envHome}, []string{"--dry-run", "up"}, envHome},
	} {
		code, _, errs, home := runBare(t, tc.env, tc.args...)
		if code != ExitOK {
			t.Fatalf("%s: exit %d: %s", tc.name, code, errs)
		}
		if _, err := os.Stat(filepath.Join(tc.want, "kit", kit.DirName(version), kit.ManifestName)); err != nil {
			t.Errorf("%s: no kit under %s: %v", tc.name, tc.want, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".config", "dbpantry")); err == nil {
			t.Errorf("%s: the default home was used as well", tc.name)
		}
	}
}

// TestHomeFlagInACheckout: --home moves the instance of a checkout too, and
// reaches the scripts as MDB_HOME.
func TestHomeFlagInACheckout(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	a := &app{environ: func() []string { return []string{"DBPANTRY_DIR=" + root} }, homeFlag: home}
	got, err := a.root()
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("root = %s, want the checkout %s", got, root)
	}
	if a.env("MDB_HOME") != home || a.home(root) != home {
		t.Fatalf("MDB_HOME = %q, home = %q; want %s", a.env("MDB_HOME"), a.home(root), home)
	}
	// Without --home a checkout is its own home, and MDB_HOME stays unset.
	b := &app{environ: func() []string { return []string{"DBPANTRY_DIR=" + root} }}
	if _, err := b.root(); err != nil {
		t.Fatal(err)
	}
	if b.env("MDB_HOME") != "" || b.home(root) != root {
		t.Fatalf("a checkout without --home: MDB_HOME = %q, home = %q", b.env("MDB_HOME"), b.home(root))
	}
}
