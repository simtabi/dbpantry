// Package config reads dbpantry's settings: the checkout's env file and the
// process environment, layered with koanf.
//
// Phase 1 reads only what the manifest commands need. The full MDB_ key
// registry, validation and --origin are phase 3 (refactor plan §10).
package config

import (
	"bufio"
	"bytes"
	"os"
	"regexp"
	"strings"
)

var envLineRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// ParseEnvFile reads a dotenv file exactly as scripts/env-lib.sh's
// mdb_env_get does: a line KEY=value at the start of the line assigns KEY,
// the last assignment wins, and one layer of quoting is removed
// (mdb_env_unquote). Anything else on a line is ignored, as grep ignores it.
func ParseEnvFile(data []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		m := envLineRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		out[m[1]] = Unquote(m[2])
	}
	return out
}

// ReadEnvFile reads and parses a dotenv file. A missing file is empty.
func ReadEnvFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the operator's own env file, named by them
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseEnvFile(b), nil
}

// Unquote strips one layer of dotenv quoting, as mdb_env_unquote does:
//
//	"a b"        -> a b   (\" \$ \` and \\ unescaped)
//	'a b'        -> a b   (literal)
//	"a b" # note -> a b   (text after the LAST closing quote is dropped)
//	plain        -> plain (trailing whitespace trimmed)
func Unquote(v string) string {
	v = strings.TrimLeft(v, " \t\n\r\v\f")
	switch {
	case len(v) >= 2 && v[0] == '"' && strings.Contains(v[1:], `"`):
		v = v[1:]
		v = v[:strings.LastIndex(v, `"`)]
		v = strings.ReplaceAll(v, `\"`, `"`)
		v = strings.ReplaceAll(v, `\$`, `$`)
		v = strings.ReplaceAll(v, "\\`", "`")
		v = strings.ReplaceAll(v, `\\`, `\`)
		return v
	case len(v) >= 2 && v[0] == '\'' && strings.Contains(v[1:], `'`):
		v = v[1:]
		return v[:strings.LastIndex(v, `'`)]
	default:
		return strings.TrimRight(v, " \t\n\r\v\f")
	}
}
