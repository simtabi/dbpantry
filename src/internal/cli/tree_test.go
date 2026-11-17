package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The command tree of refactor plan §7, for the features that exist today
// (phase 2). Adding, renaming or removing a command fails here first, so the
// change is deliberate and docs/reference/cli.md and parity.tsv move with it.
var wantTree = []string{
	"backup",
	"backup run",
	"backup verify",
	"certs",
	"certs init",
	"certs renew",
	"completion",
	"config",
	"config prod-overlay",
	"config render",
	"config validate",
	"dashboard",
	"data",
	"data export",
	"data import",
	"destroy",
	"dev",
	"dev lint",
	"dev release-notes",
	"down",
	"endpoints",
	"fixture",
	"fixture down",
	"fixture up",
	"forward",
	"ha",
	"ha failover",
	"ha reinit",
	"ha status",
	"images",
	"images build",
	"images scan",
	"init",
	"logs",
	"manifest",
	"manifest check-helm",
	"pitr",
	"pitr backup",
	"pitr info",
	"pitr restore",
	"pooler",
	"pooler sync",
	"project",
	"project new",
	"restore",
	"runtime",
	"runtime backend",
	"runtime info",
	"runtime name",
	"secrets",
	"secrets rotate",
	"self-update",
	"shell",
	"status",
	"tools",
	"up",
	"upgrade",
	"verify",
	"version",
}

func newRoot() *cobra.Command {
	a := &app{stdout: &strings.Builder{}, stderr: &strings.Builder{}, environ: func() []string { return nil }}
	root := a.rootCmd()
	root.InitDefaultCompletionCmd()
	return root
}

// commandPaths lists every command below root, without root's name and
// without cobra's generated help and per-shell completion leaves.
func commandPaths(root *cobra.Command) []string {
	var out []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" || sub.Parent().Name() == "completion" {
				continue
			}
			out = append(out, strings.TrimPrefix(sub.CommandPath(), root.Name()+" "))
			walk(sub)
		}
	}
	walk(root)
	slices.Sort(out)
	return out
}

func TestTreeIsPinned(t *testing.T) {
	got := commandPaths(newRoot())
	if !slices.Equal(got, wantTree) {
		t.Fatalf("the command tree changed; update wantTree, docs/reference/cli.md and docs/reference/parity.tsv together.\ngot:\n  %s", strings.Join(got, "\n  "))
	}
}

// addCommandInLoop reports every AddCommand call inside a for or range
// statement: the tree is declared one command at a time, so `--help`, the
// docs and a grep all find each command where it is written (Simtabi rule:
// write the plain, conventional version, never a table-driven tree).
func addCommandInLoop(fset *token.FileSet, f *ast.File) []string {
	var bad []string
	var visit func(n ast.Node, inLoop bool)
	visit = func(n ast.Node, inLoop bool) {
		ast.Inspect(n, func(m ast.Node) bool {
			switch v := m.(type) {
			case *ast.ForStmt:
				if v != n {
					visit(v.Body, true)
					return false
				}
			case *ast.RangeStmt:
				if v != n {
					visit(v.Body, true)
					return false
				}
			case *ast.CallExpr:
				if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "AddCommand" && inLoop {
					bad = append(bad, fset.Position(v.Pos()).String())
				}
			}
			return true
		})
	}
	visit(f, false)
	return bad
}

func TestTreeIsDeclared(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	inspected, adds := 0, 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name) // #nosec G304 -- this package's own files
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		inspected++
		adds += strings.Count(string(src), ".AddCommand(")
		if bad := addCommandInLoop(fset, f); len(bad) > 0 {
			t.Errorf("AddCommand inside a loop (declare each command explicitly): %s", strings.Join(bad, ", "))
		}
	}
	// A glob that stops matching would pass having read nothing.
	if inspected < 8 || adds < 15 {
		t.Fatalf("inspected %d file(s) holding %d AddCommand call(s); expected at least 8 and 15", inspected, adds)
	}
}

// The detector can fail: a planted loop is reported, a plain call is not.
func TestAddCommandInLoopCanFail(t *testing.T) {
	planted := `package p
func f(root, c interface{ AddCommand(...any) }, subs []any) {
	root.AddCommand(c)
	for _, s := range subs { root.AddCommand(s) }
	for i := 0; i < 2; i++ { if true { root.AddCommand(i) } }
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "planted.go", planted, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bad := addCommandInLoop(fset, f); len(bad) != 2 {
		t.Fatalf("want the 2 calls in loops reported, got %v", bad)
	}
}

func TestNoPrefixMatching(t *testing.T) {
	if cobra.EnablePrefixMatching {
		t.Fatal("cobra prefix matching is on; abbreviations are refused (refactor plan §7)")
	}
	if code, _, _ := run(t, "vers"); code != ExitUsage {
		t.Fatalf("an abbreviation ran a command: exit %d", code)
	}
}

// Every command a user can run has a row in the parity ledger, so a command
// cannot ship without saying what proves it (refactor plan §5). The other
// direction, every row naming a real command, check and make recipe, is
// scripts/verify/74-parity-ledger.sh.
func TestLedgerCoversTheTree(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "reference", "parity.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	inLedger := map[string]bool{}
	rows := 0
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "make_target\t") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 4 {
			t.Fatalf("parity.tsv row has %d column(s): %q", len(cols), line)
		}
		inLedger[cols[1]] = true
		rows++
	}
	if rows < 40 {
		t.Fatalf("read %d ledger row(s); expected at least 40", rows)
	}
	root := newRoot()
	leaves := 0
	for _, p := range commandPaths(root) {
		c, _, err := root.Find(strings.Fields(p))
		if err != nil {
			t.Fatal(err)
		}
		if c.HasSubCommands() && !inLedger[p] {
			continue // a group: its commands carry the rows
		}
		leaves++
		if !inLedger[p] {
			t.Errorf("%q has no row in docs/reference/parity.tsv", p)
		}
	}
	if leaves < 40 {
		t.Fatalf("checked %d command(s); expected at least 40", leaves)
	}
}
