package netdiag

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ProcessFD represents an open file descriptor for a process,
// modeled after `lsof -p`.
type ProcessFD struct {
	PID     int
	Command string
	FD      string // fd number, "cwd", "root", "txt", etc.
	Type    string // "DIR", "REG", "IPv4", "IPv6", "unix", "FIFO", ...
	Device  string
	Size    int64
	Node    string
	Path    string
}

// GetProcessFDs returns open file descriptors for a given PID.
// On Linux it reads /proc/<pid>/fd (no privileges required for own processes;
// other users' processes need CAP_SYS_PTRACE or root).
// On Windows/macOS it falls back to listing connection sockets only (via
// GetConnections with the PID filter), which is what netstat/ss -p would show.
func GetProcessFDs(pid int32) ([]ProcessFD, error) {
	if runtime.GOOS == "linux" {
		return fdsLinux(pid)
	}
	return fdsSockets(pid)
}

func fdsLinux(pid int32) ([]ProcessFD, error) {
	dir := filepath.Join("/proc", fmt.Sprintf("%d", pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		cmd := "<unknown>"
		if exe, e := os.Readlink(filepath.Join("/proc", fmt.Sprintf("%d", pid), "exe")); e == nil {
			cmd = filepath.Base(exe)
		}
		return nil, fmt.Errorf("read fds for pid %d (%s): %w (可能需要管理员权限)", pid, cmd, err)
	}

	var command string
	if comm, err := os.ReadFile(filepath.Join("/proc", fmt.Sprintf("%d", pid), "comm")); err == nil {
		command = strings.TrimSpace(string(comm))
	}

	var fds []ProcessFD
	for _, e := range entries {
		fds = append(fds, ProcessFD{
			PID:     int(pid),
			Command: command,
			FD:      e.Name(),
			Path:    readLink(filepath.Join(dir, e.Name())),
		})
	}
	categorizeFDs(fds)
	return fds, nil
}

func readLink(p string) string {
	dst, err := os.Readlink(p)
	if err != nil {
		return ""
	}
	return dst
}

// categorizeFDs classifies each FD based on its symlink target.
func categorizeFDs(fds []ProcessFD) {
	for i := range fds {
		fd := &fds[i]
		p := fd.Path
		switch {
		case strings.HasPrefix(p, "socket:["):
			fd.Type = "socket"
		case strings.HasPrefix(p, "anon_inode:"), strings.HasPrefix(p, "eventfd:"),
			strings.HasPrefix(p, "signalfd:"), strings.HasPrefix(p, "epoll:"):
			fd.Type = "anon_inode"
		case strings.HasPrefix(p, "/dev/"):
			fd.Type = "CHR"
			fd.Device = p
		case strings.HasPrefix(p, "pipe:"):
			fd.Type = "FIFO"
		case strings.HasPrefix(p, "/"):
			fd.Type = "REG"
		default:
			if p != "" {
				fd.Type = "REG"
			}
		}
	}
}

// fdsSockets returns the network sockets owned by a PID (Windows/macOS fallback).
func fdsSockets(pid int32) ([]ProcessFD, error) {
	conns, err := GetConnections("all", &Filter{PID: pid})
	if err != nil {
		return nil, err
	}
	cmd := "<unknown>"
	if pid > 0 {
		name := procName(pid)
		if name != "" {
			cmd = name
		}
	}
	fds := make([]ProcessFD, 0, len(conns))
	for i, c := range conns {
		local := fmt.Sprintf("%s:%d", c.LocalIP, c.LocalPort)
		remote := "-"
		if c.RemoteIP != "0.0.0.0" || c.RemotePort != 0 {
			remote = fmt.Sprintf("%s:%d", c.RemoteIP, c.RemotePort)
		}
		fds = append(fds, ProcessFD{
			PID:     int(pid),
			Command: cmd,
			FD:      fmt.Sprintf("sock-%02d", i),
			Type:    strings.ToUpper(c.Proto),
			Path:    local + " -> " + remote + " (" + c.State + ")",
		})
	}
	return fds, nil
}

func FormatProcessFDs(fds []ProcessFD) string {
	if len(fds) == 0 {
		return "(no file descriptors)"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%7s %-20s %-8s %-12s %s\n",
		"PID", "Command", "FD", "Type", "Path"))
	sb.WriteString(strings.Repeat("-", 100) + "\n")
	for _, fd := range fds {
		cmd := fd.Command
		if len(cmd) > 18 {
			cmd = cmd[:16] + "…"
		}
		path := fd.Path
		if len(path) > 60 {
			path = path[:58] + "…"
		}
		sb.WriteString(fmt.Sprintf("%7d %-20s %-8s %-12s %s\n",
			fd.PID, cmd, fd.FD, fd.Type, path))
	}
	return sb.String()
}
