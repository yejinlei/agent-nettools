//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// newDetachedSysProcAttr puts a spawned service in its own session/process
// group on Unix so Ctrl-C in the TUI does not cascade to it, and so stop can
// kill the whole tree via the negative pid.
func newDetachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree kills a spawned service and its whole process group on Unix.
// Because Setpgid was set at start, the group id equals the lead pid, so
// sending the signal to -pid reaches the entire tree.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	return cmd.Process.Kill()
}
