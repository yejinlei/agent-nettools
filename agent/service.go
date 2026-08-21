package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// serviceProc tracks one backgrounded subsystem process spawned by the agent.
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

// ServiceStartConfig is the config bundle for starting a detached service from
// any caller (Registry tool, CLI stop/restart subcommand, etc.).
type ServiceStartConfig struct {
	Name       string
	ConfigPath string
	Executable string // "" → os.Executable()
}

// ServiceStart is the platform-neutral detached-service launcher.
func ServiceStart(cfg ServiceStartConfig) string {
	name := cfg.Name
	if !allowedServices[name] {
		return fmt.Sprintf("error: unknown service %q (可用: proxy/dns/web/tun/n2n/stunvpv)", name)
	}
	procMu.Lock()
	if old, ok := procTable[name]; ok && old.cmd.ProcessState == nil {
		procMu.Unlock()
		return fmt.Sprintf("%s 已在运行 (pid=%d)", name, old.cmd.Process.Pid)
	}
	procMu.Unlock()

	exe := cfg.Executable
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return "error: " + err.Error()
		}
	}
	args := []string{name}
	if cfg.ConfigPath != "" {
		args = append(args, "-c", cfg.ConfigPath)
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = newDetachedSysProcAttr()

	if err := cmd.Start(); err != nil {
		return "error: " + err.Error()
	}
	proc := &serviceProc{name: name, cmd: cmd}
	procMu.Lock()
	procTable[name] = proc
	procMu.Unlock()

	RecordPID(name, cmd.Process.Pid)

	go func() {
		_ = cmd.Wait()
		DeletePID(name)
		procMu.Lock()
		if cur, ok := procTable[name]; ok && cur == proc {
			delete(procTable, name)
		}
		procMu.Unlock()
	}()

	return fmt.Sprintf("%s 已启动 (pid=%d)", name, cmd.Process.Pid)
}

// RecordPID writes pid to ~/.agent-netx/pids/<name>.pid.
func RecordPID(name string, pid int) error {
	dir, err := pidDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".pid"), []byte(strconv.Itoa(pid)), 0644)
}

// DeletePID removes the pid file for a service.
func DeletePID(name string) error {
	dir, err := pidDir()
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, name+".pid"))
}

// Stop looks up the pid file for name and kills the process tree.
func Stop(name string) (pid int, err error) {
	dir, err := pidDir()
	if err != nil {
		return 0, err
	}
	p := filepath.Join(dir, name+".pid")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("%s 未在运行", name)
		}
		return 0, err
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		DeletePID(name)
		return 0, fmt.Errorf("%s 未在运行", name)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, err
	}
	if proc == nil {
		DeletePID(name)
		return 0, fmt.Errorf("%s 未在运行", name)
	}
	if err := KillPID(pid); err != nil {
		DeletePID(name)
		return 0, err
	}
	DeletePID(name)
	procMu.Lock()
	delete(procTable, name)
	procMu.Unlock()
	return pid, nil
}

func pidDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agent-netx", "pids"), nil
}

func (r *Registry) serviceStart(name string) string {
	return ServiceStart(ServiceStartConfig{
		Name:       name,
		ConfigPath: r.configPath(),
		Executable: "",
	})
}

func (r *Registry) serviceStop(name string) string {
	if !allowedServices[name] {
		return fmt.Sprintf("error: unknown service %q", name)
	}
	pid, err := Stop(name)
	if err != nil {
		return err.Error()
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
