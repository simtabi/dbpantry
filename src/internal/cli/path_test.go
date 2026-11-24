package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithContainerPathAppendsKnownDirs(t *testing.T) {
	home := t.TempDir()
	orb := filepath.Join(home, ".orbstack", "bin")
	if err := os.MkdirAll(orb, 0o750); err != nil {
		t.Fatal(err)
	}
	local := "/usr/local/bin"
	if _, err := os.Stat(local); err != nil {
		t.Skip("no /usr/local/bin on this host")
	}

	got := withContainerPath([]string{"PATH=/usr/bin:/bin", "HOME=" + home})
	path := envValue(got, "PATH")
	if !strings.HasPrefix(path, "/usr/bin"+string(os.PathListSeparator)+"/bin") {
		t.Fatalf("existing PATH must stay first: %q", path)
	}
	if !strings.Contains(path, local) {
		t.Fatalf("PATH must append %s for Docker Desktop: %q", local, path)
	}
	if !strings.Contains(path, orb) {
		t.Fatalf("PATH must append OrbStack's bin when it exists: %q", path)
	}
	// A second call must not grow forever.
	again := envValue(withContainerPath(got), "PATH")
	if again != path {
		t.Fatalf("idempotent: got %q then %q", path, again)
	}
}

func TestWithContainerPathKeepsStubFirst(t *testing.T) {
	// Check 48 puts stubs ahead of system tools; extras must not jump the queue.
	stubs := t.TempDir()
	got := withContainerPath([]string{"PATH=" + stubs + ":/usr/bin:/bin"})
	path := envValue(got, "PATH")
	if !strings.HasPrefix(path, stubs+string(os.PathListSeparator)) {
		t.Fatalf("stub dir must remain first: %q", path)
	}
}
