package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// SchemaVersion is the manifest's contract version (design.md D-68). Adding a
// field keeps it; removing or renaming one, or changing a field's meaning,
// increments it, and a consumer refuses a version newer than it knows.
const SchemaVersion = 1

// Manifest is the machine-readable form of every descriptor.
type Manifest struct {
	SchemaVersion int        `json:"schema_version"`
	Paradigms     []Paradigm `json:"paradigms"`
	Engines       []Engine   `json:"engines"`
}

// NewManifest wraps loaded engines.
func NewManifest(engines []Engine) Manifest {
	return Manifest{SchemaVersion: SchemaVersion, Paradigms: Paradigms(), Engines: engines}
}

// Engine returns the engine with that name.
func (m Manifest) Engine(name string) (Engine, bool) {
	for _, e := range m.Engines {
		if e.Name == name {
			return e, true
		}
	}
	return Engine{}, false
}

// Implemented returns the engines that can run: the ones that get a Compose
// service, a Kubernetes workload and a certificate secret.
func (m Manifest) Implemented() []Engine {
	var out []Engine
	for _, e := range m.Engines {
		if e.Implemented {
			out = append(out, e)
		}
	}
	return out
}

// WriteJSON writes the manifest, indented, with a trailing newline.
func (m Manifest) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// WriteTSV writes every descriptor key as engine<TAB>key<TAB>value, sorted by
// engine then key. It is the form check 62 compares with what bash sees
// after engine_load, so the parser cannot drift from the shell.
func (m Manifest) WriteTSV(w io.Writer) error {
	for _, e := range m.Engines {
		keys := make([]string, 0, len(e.Descriptor))
		for k := range e.Descriptor {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", e.Name, k, e.Descriptor[k]); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteEngines writes one line per engine for shell consumers:
// name<TAB>implemented<TAB>buildable<TAB>pooler service (or -).
func (m Manifest) WriteEngines(w io.Writer) error {
	for _, e := range m.Engines {
		pooler := "-"
		if e.Pooler != nil {
			pooler = e.Pooler.Service
		}
		if _, err := fmt.Fprintf(w, "%s\t%t\t%t\t%s\n", e.Name, e.Implemented, e.Buildable, pooler); err != nil {
			return err
		}
	}
	return nil
}

// MatrixEntry is one CI build job.
type MatrixEntry struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
}

// Matrix is the CI build matrix, byte-for-byte what scripts/ci-matrix emits.
// "versions" is every (engine, version) pair with a pinned base; "engines" is
// one entry per buildable engine at its default version.
func (m Manifest) Matrix(mode string) ([]MatrixEntry, error) {
	if mode != "versions" && mode != "engines" {
		return nil, fmt.Errorf("unknown matrix mode %q (want: versions|engines)", mode)
	}
	out := []MatrixEntry{}
	for _, e := range m.Engines {
		if !e.Buildable {
			continue
		}
		if mode == "engines" {
			out = append(out, MatrixEntry{e.Name, e.DefaultVersion})
			continue
		}
		for _, v := range e.Versions {
			if e.Bases[v] != "" {
				out = append(out, MatrixEntry{e.Name, v})
			}
		}
	}
	return out, nil
}

// WriteMatrix writes the matrix as one line of JSON for GITHUB_OUTPUT.
func (m Manifest) WriteMatrix(w io.Writer, mode string) error {
	entries, err := m.Matrix(mode)
	if err != nil {
		return err
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}

// Paradigm names, joined, for messages.
func paradigmList() string {
	names := make([]string, 0, len(Paradigms()))
	for _, p := range Paradigms() {
		names = append(names, string(p))
	}
	return strings.Join(names, ", ")
}
