package dashboard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// registered matches a route this package registers: mux.HandleFunc("GET /path", …).
var registered = regexp.MustCompile(`HandleFunc\("([A-Z]+) (/[^"]*)"`)

// routeProblems lists each registered route docs/reference/api.md does not
// name: an API route as `METHOD /path`, a page as `/path`. The catch-all
// "/", the overview "/{$}" (documented as `/`) and the favicon are left out.
func routeProblems(t *testing.T, sources map[string]string, doc string) (problems []string, n int) {
	t.Helper()
	for file, src := range sources {
		for _, m := range registered.FindAllStringSubmatch(src, -1) {
			method, path := m[1], m[2]
			if path == "/" || path == "/favicon.ico" {
				continue
			}
			n++
			if path == "/{$}" {
				path = "/"
			}
			want := "`" + path + "`"
			if strings.HasPrefix(path, "/api/") || method != "GET" {
				want = "`" + method + " " + path + "`"
			}
			if !strings.Contains(doc, want) {
				problems = append(problems, file+": "+method+" "+path+" is registered but docs/reference/api.md does not name "+want)
			}
		}
	}
	return problems, n
}

func readRoutes(t *testing.T) (map[string]string, string) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f) // #nosec G304 -- this package's own files
		if err != nil {
			t.Fatal(err)
		}
		sources[f] = string(b)
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "reference", "api.md"))
	if err != nil {
		t.Fatal(err)
	}
	return sources, string(doc)
}

// TestEveryRouteIsDocumented: the dashboard's routes and docs/reference/api.md
// agree (2026-11-24 audit, G6: nine pages and the sign-in routes were
// registered and undocumented).
func TestEveryRouteIsDocumented(t *testing.T) {
	sources, doc := readRoutes(t)
	problems, n := routeProblems(t, sources, doc)
	if n < 40 {
		t.Fatalf("found %d registered routes; too few to mean anything, so the pattern no longer matches", n)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// The guard can fail: a route the doc stops naming is reported.
func TestRouteDocGuardFails(t *testing.T) {
	sources, doc := readRoutes(t)
	for _, gone := range []string{"`POST /logout`", "`/ha`", "`GET /api/v1/choices`"} {
		if !strings.Contains(doc, gone) {
			t.Fatalf("the doc does not name %s, so removing it proves nothing", gone)
		}
		if p, _ := routeProblems(t, sources, strings.ReplaceAll(doc, gone, "")); len(p) == 0 {
			t.Errorf("removing %s from the doc was not reported", gone)
		}
	}
}
