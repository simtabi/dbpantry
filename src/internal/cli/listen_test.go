package cli

import (
	"path/filepath"
	"testing"

	"github.com/simtabi/dbpantry/src/internal/config"
)

// S11 (2026-11-24 audit): --listen accepts what dashboard.ParseListen
// accepts, a unix socket included; it used to be refused by a host:port
// check before ParseListen ever saw it.
func TestListenAcceptsAUnixSocket(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	a := &app{environ: func() []string {
		return []string{"DBPANTRY_DIR=" + root, "MDB_ENV_FILE=" + filepath.Join(root, ".env.example")}
	}}
	s, err := config.Load(root, a.environ)
	if err != nil {
		t.Fatal(err)
	}
	changed := func(name string) bool { return name == "listen" || name == "auth" }
	for _, listen := range []string{"unix:/tmp/dbpantry-test.sock", "unix:///tmp/dbpantry-test.sock", "/tmp/dbpantry-test.sock", "127.0.0.1:8484"} {
		ds, err := a.dashboardSettings(root, s, dashboardFlags{listen: listen, auth: "none"}, changed)
		if err != nil || ds.Listen != listen {
			t.Errorf("--listen %s: %v (listen %q)", listen, err, ds.Listen)
		}
	}
	if _, err := a.dashboardSettings(root, s, dashboardFlags{listen: "nonsense", auth: "none"}, changed); err == nil {
		t.Error("--listen nonsense was accepted")
	}
}
