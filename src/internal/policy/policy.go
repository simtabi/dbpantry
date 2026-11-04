// Package policy holds the source rules every package of this module obeys
// (refactor plan N10, N15). It has no runtime code; its test is the guard.
package policy

import "strings"

// Rule is one forbidden construct.
type Rule struct {
	Needle string
	Why    string
}

// Rules are the constructs no file of this module may contain. Every
// outbound call takes an injected transport, verifies TLS, and the binary
// exposes no debug endpoint.
var Rules = []Rule{
	{"InsecureSkipVerify", "TLS is verified, always (N10)"},
	{"http.DefaultClient", "outbound calls take an injected transport (N10, D9)"},
	{"http.DefaultTransport", "outbound calls take an injected transport (N10, D9)"},
	{"http.Get(", "uses the default client"},
	{"http.Post(", "uses the default client"},
	{"http.Head(", "uses the default client"},
	{`"net/http/pprof"`, "no debug endpoints in a shipped binary (N15)"},
	{`"expvar"`, "no debug endpoints in a shipped binary (N15)"},
}

// Violations returns the rules a source text breaks.
func Violations(src string) []Rule {
	var out []Rule
	for _, r := range Rules {
		if strings.Contains(src, r.Needle) {
			out = append(out, r)
		}
	}
	return out
}
