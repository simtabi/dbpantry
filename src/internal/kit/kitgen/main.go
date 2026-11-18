// Command kitgen copies the kit's files into internal/kit/files and writes
// its MANIFEST (design.md D-85). `make kit` runs it with the tracked files
// of the Makefile's KIT_PATHS, one repository-relative path per line on
// standard input. It is a build step, not a shipped program.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/simtabi/dbpantry/src/internal/kit"
)

func main() {
	repo := flag.String("repo", "..", "the repository root the paths are relative to")
	out := flag.String("out", "internal/kit/files", "the directory to fill")
	flag.Parse()
	if err := run(*repo, *out, os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "kitgen:", err)
		os.Exit(1)
	}
}

func run(repo, out string, list io.Reader) error {
	var paths []string
	sc := bufio.NewScanner(list)
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		if p == "" {
			continue
		}
		if !fs.ValidPath(p) || p == kit.ManifestName {
			return fmt.Errorf("%q is not a relative path inside the repository", p)
		}
		paths = append(paths, p)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("no files on standard input")
	}
	if err := clean(out); err != nil {
		return err
	}
	entries := make([]kit.Entry, 0, len(paths))
	for _, p := range paths {
		e, err := copyFile(filepath.Join(repo, filepath.FromSlash(p)), filepath.Join(out, filepath.FromSlash(p)))
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		e.Path = p
		entries = append(entries, e)
	}
	// #nosec G306 -- checksums, nothing secret
	if err := os.WriteFile(filepath.Join(out, kit.ManifestName), kit.WriteManifest(entries), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "kit: %d files\n", len(entries))
	return nil
}

// clean empties out, keeping its README.md, which is tracked.
func clean(out string) error {
	ents, err := os.ReadDir(out)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.Name() == "README.md" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(out, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) (kit.Entry, error) {
	st, err := os.Lstat(src)
	if err != nil {
		return kit.Entry{}, err
	}
	if !st.Mode().IsRegular() {
		return kit.Entry{}, errors.New("not a regular file (the kit holds no links)")
	}
	b, err := os.ReadFile(src) // #nosec G304 -- a tracked file of the repository
	if err != nil {
		return kit.Entry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return kit.Entry{}, err
	}
	// Executable or not, as git records it; everything else is world-readable.
	mode := fs.FileMode(0o644)
	if st.Mode().Perm()&0o111 != 0 {
		mode = 0o755
	}
	if err := os.WriteFile(dst, b, mode); err != nil { // #nosec G306 G703 -- kit files are public source; dst is out joined with a path fs.ValidPath accepted
		return kit.Entry{}, err
	}
	sum, err := kit.Sum(bytes.NewReader(b))
	if err != nil {
		return kit.Entry{}, err
	}
	return kit.Entry{Mode: mode, Sum: sum}, nil
}
