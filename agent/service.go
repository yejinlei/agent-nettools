package agent

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// serviceProc tracks one backgrounded subsystem process spawned by the agent
// via the `service` tool. Each corresponds to a standalone subcommand
// (proxy/dns/web/tun/n2n/stunvpv) so that "non-TUI, tools run independently"
// and "TUI agent can start/stop tools" share the same code path.
type serviceProc struct {
	name string
	cmd  *exec.Cmd
}

var (
	procMu    sync.Mutex
	procTable = map[string]*serviceProc{} // name -> proc
)

var allowedServices = map[string]bool{
	"proxy": true, "dns": true, "web": true,
	"tun": true, "n2n": true, "stunvpv": true,
}

func (r *Registry) serviceStart(name string) string {
	if !allowedServices[name] {
		return fmt.Sprintf("error: unknown service %q (可用: proxy/dns/web/tun/n2n/stunvpv)", name)
	}
	procMu.Lock()
	if old, ok := procTable[name]; ok && old.cmd.ProcessState == nil {
		procMu.Unlock()
		return fmt.Sprintf("%s 已在运行 (pid=%d)", name, old.cmd.Process.Pid)
	}
	procMu.Unlock()

	exe, err := os.Executable()
	if err != nil {
		return "error: " + err.Error()
	}
	args := []string{name, "-c", r.configPath()}
	cmd := exec.Command(exe, args...)
	// Detach from the TUI's stdio so the subsystem doesn't write into the REPL.
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Put the service in its own process group so Ctrl-C in the TUI doesn't
	// cascade to it, and so stop can kill the whole tree. The exact mechanism
	// is platform-specific (see service_windows.go / service_unix.go).
	cmd.SysProcAttr = newDetachedSysProcAttr()

	if err := cmd.Start(); err != nil {
		return "error: " + err.Error()
	}
	proc := &serviceProc{name: name, cmd: cmd}
	procMu.Lock()
	procTable[name] = proc
	procMu.Unlock()

	// Reap the process when it exits so status stays accurate and we can drop
	// the entry if it died on its own.
	go func() {
		_ = cmd.Wait()
		procMu.Lock()
		if cur, ok := procTable[name]; ok && cur == proc {
			delete(procTable, name)
		}
		procMu.Unlock()
	}()

	return fmt.Sprintf("%s 已启动 (pid=%d，配置 %s)", name, cmd.Process.Pid, r.configPath())
}

func (r *Registry) serviceStop(name string) string {
	if !allowedServices[name] {
		return fmt.Sprintf("error: unknown service %q", name)
	}
	procMu.Lock()
	proc, ok := procTable[name]
	if !ok {
		procMu.Unlock()
		return fmt.Sprintf("%s 未在运行", name)
	}
	delete(procTable, name)
	procMu.Unlock()

	pid := 0
	if proc.cmd.Process != nil {
		pid = proc.cmd.Process.Pid
		_ = killProcessTree(proc.cmd)
	}
	return fmt.Sprintf("%s 已停止 (pid=%d)", name, pid)
}

func (r *Registry) serviceStatus() string {
	procMu.Lock()
	defer procMu.Unlock()
	if len(procTable) == 0 {
		return "当前没有子服务在运行。可用服务: proxy/dns/web/tun/n2n/stunvpv"
	}
	out := fmt.Sprintf("运行中的子服务 (%d):\n", len(procTable))
	for name, proc := range procTable {
		if proc.cmd.ProcessState != nil {
			out += fmt.Sprintf("  %-10s 已退出\n", name)
			continue
		}
		pid := 0
		if proc.cmd.Process != nil {
			pid = proc.cmd.Process.Pid
		}
		out += fmt.Sprintf("  %-10s pid=%d\n", name, pid)
	}
	return out
}

// quietRun runs a helper command with no stdio attached.
func quietRun(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.Stdout = nil
	c.Stderr = nil
	c.Stdin = nil
	return c
}

// Silence "unused" if runtime is ever only referenced conditionally.
