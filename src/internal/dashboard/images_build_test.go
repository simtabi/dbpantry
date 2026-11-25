package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// "Build images" offers every image the kit can build: each
// images/<name>/Dockerfile, which scripts/build takes by name. It offered
// engines only, so the pooler and HA images, which `up` needs for the pooler
// and ha profiles, could not be built from the dashboard (2026-11-25).
func TestBuildImagesOffersEveryImageTheKitBuilds(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"pg", "mysql", "cli", "caddy", "pgbouncer", "patroni"} {
		if err := os.MkdirAll(filepath.Join(root, "images", d), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "images", d, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A folder without a Dockerfile is not an image.
	if err := os.MkdirAll(filepath.Join(root, "images", "_shared"), 0o750); err != nil {
		t.Fatal(err)
	}
	c := ChoicesOf(Snapshot{Root: root})
	if got, want := strings.Join(c.Buildable, ","), "caddy,cli,mysql,patroni,pg,pgbouncer"; got != want {
		t.Fatalf("buildable images = %s, want %s", got, want)
	}

	a, ok := actionByName("images.build")
	if !ok {
		t.Fatal("no images.build action")
	}
	for _, name := range []string{"pgbouncer", "patroni", "caddy", "pg"} {
		v, err := a.validate(map[string]string{"engine": name}, c)
		if err != nil {
			t.Errorf("images.build refused %s: %v", name, err)
			continue
		}
		if got := strings.Join(a.Command(v), " "); got != "images build "+name {
			t.Errorf("images.build %s runs %q", name, got)
		}
	}
	if _, err := a.validate(map[string]string{"engine": "nope"}, c); err == nil {
		t.Error("images.build accepted an image the kit cannot build")
	}
	if v, err := a.validate(map[string]string{}, c); err != nil || strings.Join(a.Command(v), " ") != "images build" {
		t.Errorf("images.build with no image did not build every image: %v", err)
	}
}
