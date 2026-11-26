//go:build windows

package cli

import "os/exec"

// killGroupOnCancel keeps exec's default on Windows, which kills the script
// alone: there is no process group to signal. WaitDelay still bounds the
// wait for children that hold the output pipe.
func killGroupOnCancel(*exec.Cmd) {}
