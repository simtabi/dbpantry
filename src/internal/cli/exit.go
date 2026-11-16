package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// Exit codes, from the table mesobia and dbpantry share (refactor plan §7,
// mesobia spec 37 §2). 0 and 1 keep the meaning the scripts give them
// (§6.1). A command that wraps a script exits with the script's own status,
// so `make` and the verify checks see exactly what they saw before the
// binary sat in front of the script. docs/reference/cli.md has the table.
const (
	ExitOK        = 0
	ExitError     = 1
	ExitCancelled = 6
	ExitConfirm   = 7
	ExitUsage     = 64
)

// usageError marks an error as the caller's: exit 64.
type usageError struct{ error }

func usagef(format string, a ...any) error { return usageError{fmt.Errorf(format, a...)} }

// confirmError: a typed confirmation is needed and --no-input forbids the
// prompt: exit 7. `--yes` never satisfies one; `--confirm=<name>` does.
type confirmError struct{ what string }

func (e confirmError) Error() string {
	return fmt.Sprintf("%s needs a typed confirmation and --no-input forbids the prompt; pass --confirm=<%s>", e.what, e.what)
}

// statusError is a wrapped script's non-zero exit status, passed through.
type statusError struct {
	step string
	code int
}

func (e statusError) Error() string { return fmt.Sprintf("%s exited %d", e.step, e.code) }

// exitCode maps an error to the process exit code, and a stable name for it
// (the "code" of a JSON error, N19).
func exitCode(err error) (int, string) {
	var ue usageError
	var ce confirmError
	var se statusError
	var ee *exec.ExitError
	switch {
	case err == nil:
		return ExitOK, ""
	case errors.As(err, &ue):
		return ExitUsage, "usage"
	case errors.As(err, &ce):
		return ExitConfirm, "confirmation_required"
	case errors.As(err, &se):
		return se.code, "script_failed"
	case errors.As(err, &ee) && ee.ExitCode() > 0:
		return ee.ExitCode(), "script_failed"
	case errors.Is(err, context.Canceled):
		return ExitCancelled, "cancelled"
	default:
		return ExitError, "error"
	}
}

// writeError reports a failure on stderr: one line of text, or, for a command
// run with -o json, one JSON object whose "code" is stable for machines.
func writeError(w io.Writer, err error, asJSON bool) int {
	code, name := exitCode(err)
	msg := sanitize(err.Error())
	if asJSON {
		doc := map[string]any{"error": map[string]any{"code": name, "exit": code, "message": msg}}
		if b, jerr := json.Marshal(doc); jerr == nil {
			_, _ = fmt.Fprintf(w, "%s\n", b)
			return code
		}
	}
	_, _ = fmt.Fprintf(w, "dbpantry: %s\n", msg)
	return code
}
