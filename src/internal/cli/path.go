package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// containerPathDirs are directories where Docker Desktop, Homebrew, MacPorts,
// Podman and OrbStack put their CLIs on developer machines. A process started
// by launchd or an agent often has PATH=/usr/bin:/bin only, so `command -v
// docker` fails even though the binary is installed. These are APPENDED, never
// prepended: a stub earlier on PATH (check 48) still wins.
func containerPathDirs(home string) []string {
	dirs := []string{
		"/usr/local/bin",
		"/opt/homebrew/bin",
		"/opt/local/bin",
		"/opt/podman/bin",
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".orbstack", "bin"),
			filepath.Join(home, ".local", "bin"),
		)
	}
	return dirs
}

// withContainerPath returns a copy of env whose PATH lists the well-known
// container CLI directories after any existing entries. Other keys are
// unchanged. Used for every script the binary runs (design.md D-88).
func withContainerPath(env []string) []string {
	out := append([]string(nil), env...)
	home := envValue(out, "HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	path := envValue(out, "PATH")
	parts := splitPath(path)
	seen := map[string]bool{}
	for _, p := range parts {
		seen[p] = true
	}
	for _, d := range containerPathDirs(home) {
		if d == "" || seen[d] {
			continue
		}
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			continue
		}
		parts = append(parts, d)
		seen[d] = true
	}
	return setEnvValue(out, "PATH", strings.Join(parts, string(os.PathListSeparator)))
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(path, string(os.PathListSeparator)) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
