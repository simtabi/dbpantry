//go:build !windows

package cli

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// killGroupOnCancel runs cmd in a process group of its own and makes
// cancelling its context end the whole group: the script and whatever it
// started (docker compose, a build, a log follower). Killing the script
// alone left its children holding the output pipe, so a dashboard job kept
// running, and the one-change lock held, after an abort.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// The group id is the script's pid (Setpgid with Pgid 0).
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
