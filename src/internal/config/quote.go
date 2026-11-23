package config

import "strings"

// Quote formats a value for writing into an env file: double-quoted with
// " \ $ ` escaped when it contains anything outside the safe set; bare
// otherwise. Matches mdb_env_quote.
func Quote(v string) string {
	if NeedsQuotes(v) {
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`")
		return `"` + r.Replace(v) + `"`
	}
	return v
}

// NeedsQuotes reports whether writing v bare would break the quoting
// convention (mdb_env_needs_quotes). The allow-list matches scripts/env-lib.sh
// _mdb_env_unsafe and the owner's sim-standards pre-commit hook (check 50).
func NeedsQuotes(v string) bool {
	if v == "" {
		return false
	}
	for _, c := range v {
		if !safeEnvRune(c) {
			return true
		}
	}
	return false
}

// safeEnvRune is A–Z a–z 0–9 and _ . / : @ % + = - (built by ranges so the
// allow-list is not one high-entropy literal for secret scanners).
func safeEnvRune(c rune) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '_', '.', '/', ':', '@', '%', '+', '=', '-':
		return true
	}
	return false
}
