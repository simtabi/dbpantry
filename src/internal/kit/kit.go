// Package kit carries the dbpantry kit inside the binary and extracts it.
//
// The kit is what ships with dbpantry: scripts/ (without the verify harness),
// the compose files, engines/, images/ (the build contexts), config/,
// overrides/, deploy/, rls/ and .env.example. `make kit` copies the tracked
// files into files/ and writes files/MANIFEST, one line per file:
//
//	<mode, octal> <sha256, hex> <path>
//
// The binary embeds files/. Run without a checkout, it extracts the kit to
// <home>/kit/<version> (design.md D-85) and runs the scripts there, with the
// instance in the home itself (MDB_HOME, D-84). embed.FS reports no file
// modes, so the manifest carries them: a script extracted without its
// executable bit could not run.
//
// A binary built without `make kit` embeds only files/README.md, has no kit
// (Available is false) and needs a checkout, as before.
package kit

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

//go:embed all:files
var embedded embed.FS

// ManifestName is the manifest's name, in files/ and in an extracted kit.
const ManifestName = "MANIFEST"

// Entry is one file of the kit.
type Entry struct {
	Mode fs.FileMode
	Sum  string // SHA-256, lower-case hex
	Path string // slash-separated, relative to the kit root
}

// Kit is a manifest and the files it lists.
type Kit struct {
	fsys     fs.FS // rooted at the kit, so Path opens directly
	manifest []byte
	Entries  []Entry
}

// Embedded is the kit compiled into this binary, or ErrNoKit when it was
// built without `make kit`.
func Embedded() (*Kit, error) {
	sub, err := fs.Sub(embedded, "files")
	if err != nil {
		return nil, err
	}
	return Load(sub)
}

// ErrNoKit reports a binary built without the kit.
var ErrNoKit = errors.New("this dbpantry binary was built without the kit (`make kit` before `go build`); run it inside a checkout, set DBPANTRY_DIR, or pass --root")

// Load reads the manifest at the root of fsys and parses it.
func Load(fsys fs.FS) (*Kit, error) {
	m, err := fs.ReadFile(fsys, ManifestName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoKit
	}
	if err != nil {
		return nil, err
	}
	entries, err := ParseManifest(m)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, ErrNoKit
	}
	return &Kit{fsys: fsys, manifest: m, Entries: entries}, nil
}

// ParseManifest parses MANIFEST lines. Paths must be relative, slash
// separated and stay inside the kit: a manifest naming ../x would make
// extraction write outside it.
func ParseManifest(b []byte) ([]Entry, error) {
	var out []Entry
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("manifest line %d: want <mode> <sha256> <path>", n)
		}
		mode, err := strconv.ParseUint(parts[0], 8, 32)
		if err != nil || mode&^0o777 != 0 {
			return nil, fmt.Errorf("manifest line %d: bad mode %q", n, parts[0])
		}
		if len(parts[1]) != sha256.Size*2 {
			return nil, fmt.Errorf("manifest line %d: bad sha256 %q", n, parts[1])
		}
		if _, err := hex.DecodeString(parts[1]); err != nil {
			return nil, fmt.Errorf("manifest line %d: bad sha256 %q", n, parts[1])
		}
		p := parts[2]
		if !fs.ValidPath(p) || p == "." || p == ManifestName {
			return nil, fmt.Errorf("manifest line %d: path %q is not a file inside the kit", n, p)
		}
		out = append(out, Entry{Mode: fs.FileMode(mode), Sum: parts[1], Path: p})
	}
	return out, sc.Err()
}

// WriteManifest formats entries as MANIFEST lines, sorted by path.
func WriteManifest(entries []Entry) []byte {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	var b bytes.Buffer
	for _, e := range sorted {
		fmt.Fprintf(&b, "%04o %s %s\n", e.Mode.Perm(), e.Sum, e.Path)
	}
	return b.Bytes()
}

// Sum is the SHA-256 of r, in hex.
func Sum(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Verify reports whether dir holds exactly this kit: the same manifest, and
// every file it lists with that checksum and mode. Files the manifest does
// not list are ignored, so a kit is never "fixed" by deleting something.
func (k *Kit) Verify(dir string) error {
	got, err := os.ReadFile(filepath.Join(dir, ManifestName)) // #nosec G304 -- the kit directory dbpantry extracted
	if err != nil {
		return err
	}
	if !bytes.Equal(got, k.manifest) {
		return errors.New("its manifest differs from this binary's")
	}
	for _, e := range k.Entries {
		p := filepath.Join(dir, filepath.FromSlash(e.Path))
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", e.Path)
		}
		if st.Mode().Perm() != e.Mode.Perm() {
			return fmt.Errorf("%s has mode %04o, want %04o", e.Path, st.Mode().Perm(), e.Mode.Perm())
		}
		f, err := os.Open(p) // #nosec G304 -- a path from the kit's own manifest, inside dir
		if err != nil {
			return err
		}
		sum, err := Sum(f)
		_ = f.Close()
		if err != nil {
			return err
		}
		if sum != e.Sum {
			return fmt.Errorf("%s has changed (checksum %s, want %s)", e.Path, sum[:12], e.Sum[:12])
		}
	}
	return nil
}

// Ensure returns dir holding this kit, extracting it when dir is missing or
// does not verify. It never edits a kit in place: it extracts into a sibling
// temporary directory, verifies that, and swaps it in, so a process running
// scripts from the old copy, or a second dbpantry extracting at the same
// time, never sees a half-written kit. extracted reports whether it wrote one.
func (k *Kit) Ensure(dir string) (extracted bool, err error) {
	if k.Verify(dir) == nil {
		return false, nil
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return false, err
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".new-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := k.extract(tmp); err != nil {
		return false, err
	}
	if err := k.Verify(tmp); err != nil {
		return false, fmt.Errorf("the extracted kit does not verify: %w", err)
	}
	if _, err := os.Lstat(dir); err == nil {
		old, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".old-")
		if err != nil {
			return false, err
		}
		// MkdirTemp made old; Rename needs it absent to move dir there.
		if err := os.Remove(old); err != nil {
			return false, err
		}
		if err := os.Rename(dir, old); err != nil {
			return false, err
		}
		defer func() { _ = os.RemoveAll(old) }()
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Another dbpantry won the race: use its kit if it verifies.
		if k.Verify(dir) == nil {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (k *Kit) extract(dst string) error {
	for _, e := range k.Entries {
		p := filepath.Join(dst, filepath.FromSlash(e.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return err
		}
		src, err := k.fsys.Open(e.Path)
		if err != nil {
			return fmt.Errorf("%s: %w", e.Path, err)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, e.Mode.Perm()) // #nosec G304 -- a path from the kit's own manifest, inside dst
		if err != nil {
			_ = src.Close()
			return err
		}
		_, err = io.Copy(f, src)
		_ = src.Close()
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		// OpenFile's mode is filtered by the umask; the manifest's is exact.
		if err := os.Chmod(p, e.Mode.Perm()); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dst, ManifestName), k.manifest, 0o644) // #nosec G306 -- the manifest holds checksums, nothing secret
}

// DirName is the directory a version's kit is extracted to under
// <home>/kit/: the version with anything but [A-Za-z0-9._-] replaced, so
// a stamp like src/v0.1.0-3-gabc stays one path element.
func DirName(version string) string {
	var b strings.Builder
	for _, r := range version {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if s := strings.Trim(b.String(), "-."); s != "" {
		return s
	}
	return "dev"
}
