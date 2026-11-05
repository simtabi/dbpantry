package endpoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/simtabi/dbpantry/src/internal/config"
	"github.com/simtabi/dbpantry/src/internal/engine"
)

func manifest(t *testing.T) engine.Manifest {
	t.Helper()
	es, err := engine.Load(filepath.Join("..", "..", "..", "engines"))
	if err != nil {
		t.Fatal(err)
	}
	return engine.NewManifest(es)
}

func settings(t *testing.T, env string, environ ...string) *config.Settings {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := config.Load(root, func() []string { return environ })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func find(t *testing.T, d Document, name string) Endpoint {
	t.Helper()
	for _, e := range d.Endpoints {
		if e.Engine == name {
			return e
		}
	}
	t.Fatalf("no endpoint for %s", name)
	return Endpoint{}
}

func TestNonePublishesNothing(t *testing.T) {
	d, err := Resolve(manifest(t), settings(t, "MDB_PROFILES=\"pg,ui\"\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	pg := find(t, d, "pg")
	if d.Publish != "none" || pg.Direct.Published != nil || pg.Pooled.Published != nil {
		t.Errorf("none mode published something: %+v", pg)
	}
	if !pg.Enabled || pg.Pooled.Enabled || find(t, d, "mysql").Enabled {
		t.Error("enabled flags do not follow the profiles")
	}
	for _, e := range d.Endpoints {
		if e.Engine == "ferretdb" {
			t.Error("an engine with no image has no endpoint")
		}
	}
}

// The arithmetic of scripts/gen-compose: base + offset, pooler + 100, an
// explicit HOST_PORT from the environment over the file.
func TestDirectPortsFollowGenCompose(t *testing.T) {
	s := settings(t, "MDB_PUBLISH=direct\nMDB_PORT_BASE=50000\nMDB_MYSQL_HOST_PORT=3399\n",
		"MDB_MYSQL_HOST_PORT=3400", "MDB_BIND_ADDR=0.0.0.0", "MDB_PUBLISH=none")
	d, err := Resolve(manifest(t), s, Options{Profiles: "pg,mysql,pooler"})
	if err != nil {
		t.Fatal(err)
	}
	pg, my := find(t, d, "pg"), find(t, d, "mysql")
	if d.Publish != "direct" {
		t.Fatal("MDB_PUBLISH comes from the env file only, as gen-compose reads it")
	}
	if p := pg.Direct.Published; p == nil || p.Port != 50000 || p.Address != "0.0.0.0" || p.Via != "direct" {
		t.Errorf("pg direct %+v", p)
	}
	if p := pg.Pooled.Published; p == nil || p.Port != 50100 {
		t.Errorf("pg pooled %+v", p)
	}
	if p := my.Direct.Published; p == nil || p.Port != 3400 {
		t.Errorf("mysql explicit host port %+v", p)
	}
	if !pg.Pooled.Enabled || !my.Pooled.Enabled || pg.Pooled.Superuser {
		t.Error("pooler flags wrong")
	}
	if c := find(t, d, "cassandra"); c.Direct.Published.Port != 50004 || c.BootBudget != engine.HeavyBootBudgetSeconds {
		t.Errorf("cassandra %+v", c)
	}
}

func TestProxyRoutesEnginesOnly(t *testing.T) {
	d, err := Resolve(manifest(t), settings(t, "MDB_PUBLISH=proxy\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	pg := find(t, d, "pg")
	if p := pg.Direct.Published; p == nil || p.Port != 54000 || p.Via != "proxy" {
		t.Errorf("pg %+v", p)
	}
	if pg.Pooled.Published != nil {
		t.Error("the proxy does not route poolers")
	}
}

func TestBadPortsAreErrors(t *testing.T) {
	if _, err := Resolve(manifest(t), settings(t, "MDB_PORT_BASE=lots\n"), Options{}); err == nil {
		t.Error("a non-numeric base must fail")
	}
	if _, err := Resolve(manifest(t), settings(t, "MDB_PUBLISH=direct\nMDB_PG_HOST_PORT=99999\n"), Options{}); err == nil {
		t.Error("an out-of-range port must fail")
	}
}

func TestProfilesExpandPoolerLikeScriptsProfiles(t *testing.T) {
	m := manifest(t)
	got := Profiles(m, settings(t, ""), "pg,mysql,pooler")
	want := []string{"pg", "mysql", "mysql-pooler", "pg-pooler"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if p := Profiles(m, settings(t, "", "PROFILES=mysql"), ""); len(p) != 1 || p[0] != "mysql" {
		t.Errorf("PROFILES: %v", p)
	}
	if p := Profiles(m, settings(t, ""), ""); len(p) != 2 || p[0] != "pg" || p[1] != "ui" {
		t.Errorf("default: %v", p)
	}
}

func TestProjectPrincipals(t *testing.T) {
	d, err := Resolve(manifest(t), settings(t, ""), Options{Project: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if ps := find(t, d, "mysql").Principals; len(ps) != 3 || ps[2].PasswordFile != "/run/secrets/mysql_shop_readonly_password.txt" {
		t.Errorf("%+v", ps)
	}
}
