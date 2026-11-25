package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Windows runs dbpantry through WSL 2 (docs/runbooks/windows.md): the scripts
// need a real bash, GNU tools and the Linux filesystem for their 0600 secrets,
// so a native Windows run fails on its first script with "%1 is not a valid
// Win32 application". The Windows binary therefore hands every command that
// runs a script to the Linux dbpantry inside WSL, and keeps only the commands
// that run none (version, help, completion). WSL 2 forwards a server's ports to
// Windows' localhost, so `dbpantry.exe dashboard` opens in a Windows browser
// (design.md D-111).

// wslWanted reports whether this command line goes to WSL: on Windows, unless
// DBPANTRY_NO_WSL is 1 or true, for any command that runs a script.
func wslWanted(goos string, args, environ []string) bool {
	if goos != "windows" {
		return false
	}
	for _, kv := range environ {
		if k, v, _ := strings.Cut(kv, "="); k == "DBPANTRY_NO_WSL" && (v == "1" || strings.EqualFold(v, "true")) {
			return false
		}
	}
	cmd := ""
	for _, a := range args {
		switch a {
		case "-h", "--help", "--version":
			return false
		}
		if cmd == "" && !strings.HasPrefix(a, "-") {
			cmd = a
		}
	}
	switch cmd {
	case "", "version", "help", "completion":
		return false
	}
	return true
}

// wslArgs is wsl.exe's command line: the caller's directory, a login bash so
// ~/.local/bin is on PATH, and the arguments as positional parameters.
func wslArgs(cwd string, args []string) []string {
	return append([]string{"--cd", cwd, "-e", "bash", "-lc", `exec dbpantry "$@"`, "dbpantry"}, args...)
}

// wslMissing says what to install when there is no WSL, or no dbpantry in it.
func wslMissing(why string) error {
	return usagef(`%s. On Windows, dbpantry runs inside WSL 2:
  1. wsl --install                (then restart, and open the Linux distribution once)
  2. In WSL, download dbpantry_linux_amd64 (or _arm64) and checksums.txt from
     https://github.com/simtabi/dbpantry/releases, check it with
     sha256sum --ignore-missing -c checksums.txt, and install it:
     install -m 0755 dbpantry_linux_amd64 ~/.local/bin/dbpantry
  3. Install Docker Desktop with the WSL 2 backend, or Docker Engine inside WSL.
Then run dbpantry.exe again, or dbpantry inside WSL. Set DBPANTRY_NO_WSL=1 to
run this binary natively instead (only version, help and completion work there)`, why)
}

// runInWSL runs the command line through WSL's dbpantry and returns its exit
// status, which wsl.exe passes through.
func runInWSL(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		return writeError(stderr, wslMissing("no wsl.exe on PATH"), false)
	}
	probe := exec.CommandContext(ctx, wsl, "-e", "bash", "-lc", "command -v dbpantry") // #nosec G204 -- a fixed probe
	if err := probe.Run(); err != nil {
		return writeError(stderr, wslMissing("no dbpantry in WSL"), false)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return writeError(stderr, err, false)
	}
	cmd := exec.CommandContext(ctx, wsl, wslArgs(cwd, args)...) // #nosec G204 -- the caller's own command line, as positional parameters
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &ee):
		return ee.ExitCode()
	default:
		return writeError(stderr, fmt.Errorf("wsl.exe: %w", err), false)
	}
}
