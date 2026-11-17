package fixture

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	if err := Run(context.Background(), r, "/x", UpPlan([]string{"pg", "pg-pooler"}, nil)); err != nil {
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
	for _, s := range DownPlan(nil) {
		for _, a := range s.Args {
			if a == "-v" || a == "--volumes" {
				t.Fatalf("fixture down removes volumes: %s", s)
			}
		}
	}
	if got := DownPlan(nil)[0].String(); got != "scripts/runtime down -f docker-compose.yml -f compose.engines.yml -f compose.test.yml" {
		t.Fatalf("down: %s", got)
	}
}

func TestRunStopsAtTheFirstFailure(t *testing.T) {
	r := &recorder{fail: 1}
	if err := Run(context.Background(), r, "/x", UpPlan([]string{"pg"}, nil)); err == nil || len(r.steps) != 1 {
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

// A fixture runs as its own compose project on its own ports, so it never
// replaces or stops the long-lived stack's engines.
func TestEnvSeparatesTheFixtureFromTheStack(t *testing.T) {
	m := manifest(t)
	env, err := Env(m, "mdb", DefaultPortBase, DefaultSubnet)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		have[k] = v
	}
	if have["COMPOSE_PROJECT_NAME"] != "mdb-fixture" || have["MDB_FIXTURE"] != "1" {
		t.Fatalf("env %v", env)
	}
	if _, ok := have["MDB_PUBLISH"]; ok {
		t.Fatalf("env sets MDB_PUBLISH, which gen-compose refuses from the environment: %v", env)
	}
	if _, ok := have["MDB_PORT_BASE"]; ok {
		t.Fatalf("env sets MDB_PORT_BASE, which gen-compose refuses from the environment: %v", env)
	}
	n := 0
	for _, e := range m.Implemented() {
		up := strings.ToUpper(strings.ReplaceAll(e.Name, "-", "_"))
		want := fmt.Sprint(DefaultPortBase + e.PortOffset)
		if have["MDB_"+up+"_HOST_PORT"] != want {
			t.Errorf("%s host port = %q, want %s", e.Name, have["MDB_"+up+"_HOST_PORT"], want)
		}
		n++
	}
	if n == 0 {
		t.Fatal("the manifest has no implemented engine, so nothing was checked")
	}
	if ProjectName("") != "mdb-fixture" || ProjectName("shop") != "shop-fixture" {
		t.Fatal("ProjectName")
	}
}

func TestPlansCarryTheFixtureEnv(t *testing.T) {
	fx := []string{"COMPOSE_PROJECT_NAME=mdb-fixture"}
	up := UpPlan([]string{"pg"}, fx)
	if got := up[len(up)-1].String(); !strings.HasPrefix(got, "COMPOSE_PROFILES=pg COMPOSE_PROJECT_NAME=mdb-fixture scripts/runtime up") {
		t.Fatalf("up: %s", got)
	}
	if got := DownPlan(fx)[0].String(); !strings.HasPrefix(got, "COMPOSE_PROJECT_NAME=mdb-fixture scripts/runtime down") {
		t.Fatalf("down: %s", got)
	}
}

func TestCleanupRemovesOnlyFixtureSecrets(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "secrets", "pg_shop_user_password.txt")
	drop := filepath.Join(root, "secrets", SecretsSubdir, "pg_t1_user_password.txt")
	for _, p := range []string{keep, drop} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Cleanup(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(drop); !os.IsNotExist(err) {
		t.Fatalf("fixture secret survived: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("the stack's own secret was removed: %v", err)
	}
}

// PoolerAddr is gen-compose's pooler_addr: the stack's default subnet gives
// 172.28.255.254 for the first pooler, which is what compose.engines.yml has
// always pinned.
func TestPoolerAddrMatchesGenCompose(t *testing.T) {
	cases := map[string]string{
		"172.28.0.0/16|1": "172.28.255.254",
		"172.28.0.0/16|2": "172.28.255.253",
		"172.29.0.0/16|1": "172.29.255.254",
		"10.210.7.0/24|3": "10.210.7.252",
	}
	for in, want := range cases {
		subnet, idx, _ := strings.Cut(in, "|")
		got, err := PoolerAddr(subnet, map[string]int{"1": 1, "2": 2, "3": 3}[idx])
		if err != nil || got != want {
			t.Errorf("PoolerAddr(%s, %s) = %q, %v; want %s", subnet, idx, got, err, want)
		}
	}
	if _, err := PoolerAddr("not-a-cidr", 1); err == nil {
		t.Error("an invalid subnet was accepted")
	}
}

// The fixture's network must not overlap the stack's, or Docker refuses it.
func TestEnvGivesTheFixtureItsOwnSubnet(t *testing.T) {
	m := manifest(t)
	env, err := Env(m, "mdb", DefaultPortBase, DefaultSubnet)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		have[k] = v
	}
	if have["MDB_NETWORK_SUBNET"] != DefaultSubnet || Overlaps(DefaultSubnet, "172.28.0.0/16") {
		t.Fatalf("fixture subnet %q overlaps the stack's default", have["MDB_NETWORK_SUBNET"])
	}
	poolers := 0
	for _, e := range m.Implemented() {
		if e.Pooler == nil {
			continue
		}
		poolers++
		up := strings.ToUpper(strings.ReplaceAll(e.Name, "-", "_"))
		ip := have["MDB_FIXTURE_"+up+"_POOLER_IP"]
		if !Overlaps(ip+"/32", DefaultSubnet) || have["MDB_FIXTURE_"+up+"_POOLER_ADDR"] != ip+"/32" {
			t.Errorf("%s pooler address %q / %q is not inside %s", e.Name, ip, have["MDB_FIXTURE_"+up+"_POOLER_ADDR"], DefaultSubnet)
		}
	}
	if poolers == 0 {
		t.Fatal("no pooler in the manifest, so no address was checked")
	}
	if !Overlaps("172.28.0.0/16", "172.28.4.0/24") || Overlaps("172.28.0.0/16", "172.29.0.0/16") {
		t.Fatal("Overlaps")
	}
}
