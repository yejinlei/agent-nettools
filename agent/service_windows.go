//go:build windows

package agent

import (
	"fmt"
	"os/exec"
	"syscall"
)

// newDetachedSysProcAttr puts a spawned service in its own process group on
// Windows (CREATE_NEW_PROCESS_GROUP = 0x00000200) so Ctrl-C in the TUI does
// not cascade to it.
func newDetachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000200}
}

// killProcessTree kills a spawned service and its whole process tree on
// Windows via taskkill /T (kill the tree) /F (force).
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	c := quietRun("taskkill", "/T", "/F", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
	return c.Run()
}
