// Package engine is the shared kernel of dbpantry: it reads the engine
// descriptors (engines/*/engine.conf), turns them into typed engines and
// publishes them as the JSON manifest.
//
// The descriptors stay the authored source (design.md D-68). They are shell
// key-value files, sourced by scripts/engine-lib.sh, so this package reads a
// strict subset of shell syntax and refuses anything outside it rather than
// guessing what bash would have done with it.
package engine

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// KeyPrefix is the prefix every descriptor key carries.
const KeyPrefix = "MDB_ENGINE_"

// Descriptor is one engine.conf as written: every key it assigns, with its
// value after shell quoting is removed.
type Descriptor struct {
	// Path is where the descriptor was read from, for error messages.
	Path   string
	values map[string]string
	order  []string
}

// Get returns a key's value and whether the descriptor assigns it. A key
// assigned the empty string (MDB_ENGINE_DEFAULT_DB_ENV=) is present.
func (d Descriptor) Get(key string) (string, bool) {
	v, ok := d.values[key]
	return v, ok
}

// Value returns a key's value, or "" when it is not assigned.
func (d Descriptor) Value(key string) string { return d.values[key] }

// Keys returns every assigned key, sorted.
func (d Descriptor) Keys() []string {
	keys := append([]string(nil), d.order...)
	sort.Strings(keys)
	return keys
}

// Map returns a copy of every key and value.
func (d Descriptor) Map() map[string]string {
	m := make(map[string]string, len(d.values))
	for k, v := range d.values {
		m[k] = v
	}
	return m
}

var (
	assignRe = regexp.MustCompile(`^(` + KeyPrefix + `[A-Z0-9_]+)=(.*)$`)
	// The characters an unquoted value may use: nothing bash would split,
	// expand, glob or treat as an operator.
	bareRe = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]*$`)
)

// ParseError names the file and line a descriptor stopped being the strict
// subset on.
type ParseError struct {
	Path string
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Msg)
}

// Parse reads one descriptor. The accepted subset is: blank lines, comment
// lines (optionally indented) and KEY=VALUE assignments at the start of a
// line, where KEY starts with MDB_ENGINE_ and VALUE is empty, bare (no
// characters the shell would interpret), 'single-quoted' (literal, as in the
// shell) or "double-quoted" with no $, `, \ or " inside. Nothing may follow a
// closing quote. A key assigned twice is refused: the shell would keep the
// last one silently, and a descriptor saying two things is a bug.
func Parse(r io.Reader, path string) (Descriptor, error) {
	d := Descriptor{Path: path, values: map[string]string{}}
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), " \t\r")
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		m := assignRe.FindStringSubmatch(line)
		if m == nil {
			return Descriptor{}, &ParseError{path, n, "not a comment or an " + KeyPrefix + "KEY=value assignment"}
		}
		key, raw := m[1], m[2]
		val, err := unquote(raw)
		if err != nil {
			return Descriptor{}, &ParseError{path, n, key + ": " + err.Error()}
		}
		if _, dup := d.values[key]; dup {
			return Descriptor{}, &ParseError{path, n, key + " is assigned twice"}
		}
		d.values[key] = val
		d.order = append(d.order, key)
	}
	if err := sc.Err(); err != nil {
		return Descriptor{}, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}

func unquote(raw string) (string, error) {
	switch {
	case raw == "":
		return "", nil
	case raw[0] == '\'':
		end := strings.IndexByte(raw[1:], '\'')
		if end < 0 {
			return "", fmt.Errorf("unterminated single quote")
		}
		if rest := raw[end+2:]; rest != "" {
			return "", fmt.Errorf("text after the closing quote: %q", rest)
		}
		return raw[1 : end+1], nil
	case raw[0] == '"':
		end := strings.IndexByte(raw[1:], '"')
		if end < 0 {
			return "", fmt.Errorf("unterminated double quote")
		}
		if rest := raw[end+2:]; rest != "" {
			return "", fmt.Errorf("text after the closing quote: %q", rest)
		}
		inner := raw[1 : end+1]
		if strings.ContainsAny(inner, "$`\\") {
			return "", fmt.Errorf("a double-quoted value may not contain $, ` or \\ (the shell would expand it); use single quotes")
		}
		return inner, nil
	default:
		if !bareRe.MatchString(raw) {
			return "", fmt.Errorf("an unquoted value may use only letters, digits and _ . / : @ %% + = , - (got %q); quote it", raw)
		}
		return raw, nil
	}
}
