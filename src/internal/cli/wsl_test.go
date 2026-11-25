package cli

import (
	"strings"
	"testing"
)

// On Windows the binary hands every command that runs the scripts to the Linux
// dbpantry inside WSL 2, the one supported Windows path (docs/runbooks/
// windows.md); only commands that run no script stay native.
func TestWindowsHandsScriptCommandsToWSL(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"up"}, true},
		{[]string{"dashboard"}, true},
		{[]string{"--home", `C:\x`, "status"}, true},
		{[]string{"images", "build", "pgbouncer"}, true},
		{nil, false},
		{[]string{"version"}, false},
		{[]string{"--version"}, false},
		{[]string{"help"}, false},
		{[]string{"-h"}, false},
		{[]string{"up", "--help"}, false},
		{[]string{"completion", "powershell"}, false},
	} {
		if got := wslWanted("windows", c.args, nil); got != c.want {
			t.Errorf("windows %q: hand to WSL = %v, want %v", c.args, got, c.want)
		}
		if wslWanted("linux", c.args, nil) || wslWanted("darwin", c.args, nil) {
			t.Errorf("%q: handed to WSL off Windows", c.args)
		}
	}
	if wslWanted("windows", []string{"up"}, []string{"DBPANTRY_NO_WSL=1"}) {
		t.Error("DBPANTRY_NO_WSL=1 did not keep the command native")
	}
}

// The command line WSL runs: the caller's directory as the working directory
// (--cd takes an absolute Windows path), a login bash so ~/.local/bin is on
// PATH (wsl -e alone reads no profile), and the arguments as positional
// parameters, so none is ever re-parsed by a shell.
func TestWSLCommandLine(t *testing.T) {
	got := wslArgs(`C:\Users\me\proj`, []string{"up", "--profiles", "pg,ui", "it's"})
	want := []string{"--cd", `C:\Users\me\proj`, "-e", "bash", "-lc", `exec dbpantry "$@"`, "dbpantry", "up", "--profiles", "pg,ui", "it's"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("wsl args = %q, want %q", got, want)
	}
}

// Without WSL, or without dbpantry inside it, the message says what to
// install and where to get it, instead of Windows' "%1 is not a valid Win32
// application" on the first script.
func TestWSLMissingSaysWhatToInstall(t *testing.T) {
	for _, why := range []string{"no wsl.exe", "no dbpantry in WSL"} {
		msg := wslMissing(why).Error()
		for _, want := range []string{why, "wsl --install", "github.com/simtabi/dbpantry/releases", "checksums.txt", "DBPANTRY_NO_WSL"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: the message lacks %q:\n%s", why, want, msg)
			}
		}
	}
}
