package cli

import (
	"strings"
	"testing"
)

// start runs up's scripts, then new-project for each project on each engine
// in the profiles (or the one it names), then the dashboard.
func TestStartRunsUpProjectsThenDashboard(t *testing.T) {
	root, log := stubRoot(t)
	p := "|stdin=|profiles=pg,mysql|tags=|checks="
	code, _, errs, calls := runStub(t, root, log, nil, "",
		"start", "--profiles", "pg,mysql", "--project", "shop,billing:pg", "--no-dashboard")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errs)
	}
	want := []string{
		"scripts/check-env --quiet" + p, "scripts/render-config " + p, "scripts/gen-compose " + p,
		"scripts/runtime up" + p, "scripts/exposure-notice " + p,
		"scripts/new-project --name shop --engine mysql" + p, "scripts/runtime compose restart mysql-pooler" + p,
		"scripts/new-project --name shop --engine pg" + p,
		"scripts/new-project --name billing --engine pg" + p,
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("start ran\n  %s\nwant\n  %s", strings.Join(calls, "\n  "), strings.Join(want, "\n  "))
	}

	// Without --no-dashboard the last step is the dashboard: a bad address
	// fails there, after the stack and projects, so the order is proven.
	code, _, errs, calls = runStub(t, root, log, nil, "",
		"start", "--profiles", "pg", "--project", "shop", "--listen", "no-port")
	if code != ExitUsage || !strings.Contains(errs, "--listen") {
		t.Errorf("the dashboard step was not reached last: exit %d: %s", code, errs)
	}
	if n := len(calls); n != 6 || !strings.HasPrefix(calls[n-1], "scripts/new-project --name shop --engine pg") {
		t.Errorf("up and the project run before the dashboard: %v", calls)
	}
}

func TestStartRefusesABadProject(t *testing.T) {
	root, log := stubRoot(t)
	for _, bad := range []string{":pg", "a:b:c"} {
		code, _, _, calls := runStub(t, root, log, nil, "", "start", "--project", bad, "--no-dashboard")
		if code != ExitUsage || len(calls) != 0 {
			t.Errorf("--project %q: exit %d, ran %v; want a usage error before anything runs", bad, code, calls)
		}
	}
}
