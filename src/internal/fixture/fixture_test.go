package fixture

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simtabi/dbpantry/src/internal/engine"
)

type recorder struct {
	steps []string
	fail  int
}

func (r *recorder) Run(_ context.Context, _ string, s Step) error {
	r.steps = append(r.steps, s.String())
	if r.fail > 0 && len(r.steps) == r.fail {
		return errors.New("boom")
	}
	return nil
}

func manifest(t *testing.T) engine.Manifest {
	t.Helper()
	es, err := engine.Load(filepath.Join("..", "..", "..", "engines"))
	if err != nil {
		t.Fatal(err)
	}
	return engine.NewManifest(es)
}

// fixture up is exactly what make test-profile runs, with the caller's profiles.
func TestUpPlanIsTheTestProfile(t *testing.T) {
	r := &recorder{}
	if err := Run(context.Background(), r, "/x", UpPlan([]string{"pg", "pg-pooler"})); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"scripts/render-config",
		"scripts/gen-compose",
		"COMPOSE_PROFILES=pg,pg-pooler scripts/runtime up -f docker-compose.yml -f compose.engines.yml -f compose.test.yml",
	}
	if strings.Join(r.steps, "\n") != strings.Join(want, "\n") {
		t.Fatalf("plan:\n%s", strings.Join(r.steps, "\n"))
	}
}

func TestDownPlanNeverRemovesVolumes(t *testing.T) {
	for _, s := range DownPlan() {
		for _, a := range s.Args {
			if a == "-v" || a == "--volumes" {
				t.Fatalf("fixture down removes volumes: %s", s)
			}
		}
	}
	if got := DownPlan()[0].String(); got != "scripts/runtime down -f docker-compose.yml -f compose.engines.yml -f compose.test.yml" {
		t.Fatalf("down: %s", got)
	}
}

func TestRunStopsAtTheFirstFailure(t *testing.T) {
	r := &recorder{fail: 1}
	if err := Run(context.Background(), r, "/x", UpPlan([]string{"pg"})); err == nil || len(r.steps) != 1 {
		t.Fatalf("err=%v steps=%v", err, r.steps)
	}
}

func TestValidateProfiles(t *testing.T) {
	m := manifest(t)
	for _, ok := range [][]string{{"pg"}, {"pg", "pg-pooler"}, {"mongodb", "cassandra"}} {
		if err := ValidateProfiles(m, ok); err != nil {
			t.Errorf("%v: %v", ok, err)
		}
	}
	for _, bad := range [][]string{{}, {"ui"}, {"pg", "ha"}, {"backup"}, {"ferretdb"}, {"nope"}} {
		if err := ValidateProfiles(m, bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}
