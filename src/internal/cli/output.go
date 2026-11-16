package cli

import (
	"encoding/json"
	"io"
	"slices"
	"strings"
)

// Formats is every value -o may take anywhere in the tree (refactor plan
// §7). A command accepts the subset it can produce and refuses the rest with
// exit 64, naming what it does accept.
var Formats = []string{"table", "plain", "json", "ndjson", "csv", "tsv", "yaml"}

// output validates a command's -o value against what it supports, and
// switches error reporting to JSON for -o json.
func (a *app) output(value string, supported ...string) error {
	if value == "json" || value == "ndjson" {
		a.jsonErrors = true
	}
	if slices.Contains(supported, value) {
		return nil
	}
	if !slices.Contains(Formats, value) {
		return usagef("unknown output %q (the formats are %s)", value, strings.Join(Formats, ", "))
	}
	return usagef("this command does not write %s (it writes %s)", value, strings.Join(supported, ", "))
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
