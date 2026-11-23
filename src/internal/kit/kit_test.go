package kit

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// testKit builds a kit from name -> (mode, content), with its manifest.
func testKit(t *testing.T, files map[string]struct {
	mode fs.FileMode
	body string
}) *Kit {
	t.Helper()
	fsys := fstest.MapFS{}
	var entries []Entry
	for p, f := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(f.body), Mode: 0o444}
		sum, err := Sum(strings.NewReader(f.body))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, Entry{Mode: f.mode, Sum: sum, Path: p})
	}
	fsys[ManifestName] = &fstest.MapFile{Data: WriteManifest(entries)}
	k, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func sample(t *testing.T) *Kit {
	return testKit(t, map[string]struct {
		mode fs.FileMode
		body string
	}{
		"scripts/init":           {0o755, "#!/bin/sh\necho init\n"},
		"scripts/env-lib.sh":     {0o644, "# lib\n"},
		".env.example":           {0o644, "MDB_PROFILES=pg\n"},
		"engines/pg/engine.conf": {0o644, "MDB_ENGINE_NAME=pg\n"},
	})
}

func TestEnsureExtractsWithModesAndVerifies(t *testing.T) {
	k := sample(t)
	dir := filepath.Join(t.TempDir(), "kit", "v1")
	extracted, err := k.Ensure(dir)
	if err != nil || !extracted {
		t.Fatalf("Ensure = %v, %v; want a fresh extraction", extracted, err)
	}
	st, err := os.Stat(filepath.Join(dir, "scripts", "init"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Errorf("scripts/init mode = %04o, want 0755 (embed.FS reports none; the manifest carries it)", st.Mode().Perm())
	}
	if err := k.Verify(dir); err != nil {
		t.Fatalf("a fresh kit does not verify: %v", err)
	}
	// The second call finds it intact and writes nothing.
	if extracted, err := k.Ensure(dir); err != nil || extracted {
		t.Fatalf("Ensure on an intact kit = %v, %v; want no extraction", extracted, err)
	}
	// Nothing but the kit and its manifest is left behind: no temporary
	// directories beside it.
	ents, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Errorf("the kit's parent holds %d entries, want only the kit", len(ents))
	}
}

func TestEnsureRestoresATamperedKit(t *testing.T) {
	k := sample(t)
	dir := filepath.Join(t.TempDir(), "v1")
	if _, err := k.Ensure(dir); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		tamper func() error
	}{
		{"edited file", func() error {
			return os.WriteFile(filepath.Join(dir, "scripts", "env-lib.sh"), []byte("# edited\n"), 0o644) // #nosec G306 -- test file
		}},
		{"lost executable bit", func() error { return os.Chmod(filepath.Join(dir, "scripts", "init"), 0o644) }}, // #nosec G302 -- a kit file's own mode, in a test directory
		{"deleted file", func() error { return os.Remove(filepath.Join(dir, ".env.example")) }},
		{"other manifest", func() error {
			return os.WriteFile(filepath.Join(dir, ManifestName), []byte("0644 "+strings.Repeat("0", 64)+" x\n"), 0o644) // #nosec G306 -- test file
		}},
	} {
		if err := tc.tamper(); err != nil {
			t.Fatal(err)
		}
		if err := k.Verify(dir); err == nil {
			t.Fatalf("%s: Verify passed a tampered kit", tc.name)
		}
		extracted, err := k.Ensure(dir)
		if err != nil || !extracted {
			t.Fatalf("%s: Ensure = %v, %v; want a re-extraction", tc.name, extracted, err)
		}
		if err := k.Verify(dir); err != nil {
			t.Fatalf("%s: the re-extracted kit does not verify: %v", tc.name, err)
		}
	}
}

func TestParseManifestRefusesPathsOutsideTheKit(t *testing.T) {
	sum := strings.Repeat("a", 64)
	for _, line := range []string{
		"0644 " + sum + " ../escape",
		"0644 " + sum + " /etc/passwd",
		"0644 " + sum + " a/../../b",
		"0644 " + sum + " " + ManifestName,
		"9999 " + sum + " ok",
		"0644 short ok",
		"0644 " + sum,
	} {
		if _, err := ParseManifest([]byte(line + "\n")); err == nil {
			t.Errorf("ParseManifest accepted %q", line)
		}
	}
	if _, err := ParseManifest([]byte("0755 " + sum + " scripts/init\n")); err != nil {
		t.Errorf("ParseManifest refused a valid line: %v", err)
	}
}

func TestLoadWithoutAManifestIsNoKit(t *testing.T) {
	_, err := Load(fstest.MapFS{"README.md": &fstest.MapFile{Data: []byte("x")}})
	if !errors.Is(err, ErrNoKit) {
		t.Fatalf("Load = %v, want ErrNoKit", err)
	}
}

func TestDirName(t *testing.T) {
	for in, want := range map[string]string{
		"src/v0.1.0-49-gf0ae524-dirty": "src-v0.1.0-49-gf0ae524-dirty",
		"v0.2.0":                       "v0.2.0",
		"dev":                          "dev",
		"":                             "dev",
		"../..":                        "dev",
	} {
		if got := DirName(in); got != want {
			t.Errorf("DirName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEmbeddedKit checks the kit `make kit` generated: everything the
// scripts need, executable where git says so, and nothing that belongs to an
// instance or to the verify harness.
func TestEmbeddedKit(t *testing.T) {
	k, err := Embedded()
	if errors.Is(err, ErrNoKit) {
		if os.Getenv("DBPANTRY_KIT_REQUIRED") == "1" {
			t.Fatal("make generated the kit, but the binary embeds none")
		}
		t.Skip("no kit generated (run make kit, or make go-test, which does)")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Entries) < 100 {
		t.Fatalf("the kit holds %d files; too few to be the kit", len(k.Entries))
	}
	have := map[string]fs.FileMode{}
	for _, e := range k.Entries {
		have[e.Path] = e.Mode
		for _, banned := range []string{"scripts/verify/", "secrets/", "certs/", "backups/", ".env", "compose.engines.yml", "compose.proxy.yml", "compose.prod.engines.yml", "config/caddy/Caddyfile", "config/caddy/servers.json"} {
			if e.Path == banned || (strings.HasSuffix(banned, "/") && strings.HasPrefix(e.Path, banned)) {
				t.Errorf("the kit holds %s, which belongs to an instance or the verify harness", e.Path)
			}
		}
	}
	for p, mode := range map[string]fs.FileMode{
		"scripts/init": 0o755, "scripts/runtime": 0o755, "scripts/env-lib.sh": 0o644,
		"docker-compose.yml": 0o644, ".env.example": 0o644, "engines/postgres/engine.conf": 0o644,
	} {
		got, ok := have[p]
		if !ok {
			t.Errorf("the kit has no %s", p)
			continue
		}
		if got != mode {
			t.Errorf("%s has mode %04o in the kit, want %04o", p, got, mode)
		}
	}
	// Every file's checksum is right in the embedded copy itself.
	sub, err := fs.Sub(embedded, "files")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range k.Entries {
		b, err := fs.ReadFile(sub, e.Path)
		if err != nil {
			t.Fatal(err)
		}
		sum, err := Sum(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if sum != e.Sum {
			t.Errorf("%s: embedded checksum %s, manifest %s", e.Path, sum[:12], e.Sum[:12])
		}
	}
}
