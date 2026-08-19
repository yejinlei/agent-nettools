package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// HostInfo is everything the agent needs to connect to an SSH host. It is the
// unit the agent remembers in Memory (under "ssh:host:<name>") so that, after
// the first interactive setup, subsequent file copies need no prompting.
type HostInfo struct {
	Name     string `json:"name"`     // memorable alias, e.g. "prod-web"
	Host     string `json:"host"`     // hostname or IP
	Port     int    `json:"port"`     // 0 → 22
	User     string `json:"user"`     // login user
	Password string `json:"password"` // optional; empty if using a key
	KeyPath  string `json:"keyPath"`  // optional path to a private key
}

func (h HostInfo) port() int {
	if h.Port > 0 {
		return h.Port
	}
	return 22
}

// PortOf returns the SSH port for h (exported wrapper for cmd package use).
func PortOf(h HostInfo) int { return h.port() }

// FileTransfer copies a single file to/from an SSH host over SFTP.
// dir="upload" → local→remote; dir="download" → remote→local.
// Exported so the standalone `scp` subcommand (cmd/scp.go) shares the same
// transfer path as the TUI's file_copy tool.
func FileTransfer(ctx context.Context, h HostInfo, src, dst, dir string) (int64, error) {
	sshClient, err := dialSSH(ctx, h)
	if err != nil {
		return 0, err
	}
	defer sshClient.Close()

	client, err := sftp.NewClient(sshClient)
	if err != nil {
		return 0, fmt.Errorf("sftp: %w", err)
	}
	defer client.Close()

	if dir == "upload" {
		in, err := os.Open(src)
		if err != nil {
			return 0, fmt.Errorf("open local %s: %w", src, err)
		}
		defer in.Close()

		// Create remote parent dirs so a deep dst path works first time.
		if dir := filepath.Dir(dst); dir != "." && dir != "/" {
			_ = client.MkdirAll(dir)
		}
		out, err := client.Create(dst)
		if err != nil {
			return 0, fmt.Errorf("create remote %s: %w", dst, err)
		}
		defer out.Close()
		return io.Copy(out, in)
	}

	// download
	in, err := client.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open remote %s: %w", src, err)
	}
	defer in.Close()

	if dir := filepath.Dir(dst); dir != "." && dir != "/" {
		_ = os.MkdirAll(dir, 0755)
	}
	out, err := os.Create(dst)
	if err != nil {
		return 0, fmt.Errorf("create local %s: %w", dst, err)
	}
	defer out.Close()
	return io.Copy(out, in)
}

// DialSSH is the exported wrapper over dialSSH so non-agent packages (e.g. the
// `forward remote` -R mode in cmd/forward.go) can obtain an *ssh.Client using
// the same auth + host-key policy + memory + HIL resolution as the TUI tools,
// without depending on dialSSH's internals.
func DialSSH(ctx context.Context, h HostInfo) (*ssh.Client, error) {
	return dialSSH(ctx, h)
}

// dialSSH builds an *ssh.Client for h, choosing auth and host-key policy.
func dialSSH(ctx context.Context, h HostInfo) (*ssh.Client, error) {
	authMethods, err := authMethods(h)
	if err != nil {
		return nil, err
	}

	hostKeyCB, err := hostKeyCallback(h)
	if err != nil {
		return nil, err
	}

	cfg := &ssh.ClientConfig{
		User:            h.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCB,
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(h.Host, fmt.Sprint(h.port()))
	d := net.Dialer{Timeout: cfg.Timeout}
	netConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	c, chans, reqs, err := ssh.NewClientConn(netConn, addr, cfg)
	if err != nil {
		netConn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// authMethods assembles the acceptable SSH auth methods for h, in priority
// order: explicit private key → password. At least one is required.
func authMethods(h HostInfo) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if h.KeyPath != "" {
		keyBytes, err := os.ReadFile(h.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read key %s: %w", h.KeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("parse key %s: %w", h.KeyPath, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if h.Password != "" {
		methods = append(methods, ssh.Password(h.Password))
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("no auth: need a password or key path for %s@%s", h.User, h.Host)
	}
	return methods, nil
}

// hostKeyCallback decides host-key verification policy.
//
//   - If the user set known_hosts file path explicitly, verify strictly.
//   - Otherwise use the default ~/.ssh/known_hosts, with trust-on-first-use:
//     an unknown host triggers a HIL prompt; once trusted, its key is appended
//     so later connects verify silently.
//
// This is the core of "HIL when something is missing": an unrecognized host key
// is exactly the kind of gap that should pause and ask the human.
func hostKeyCallback(h HostInfo) (ssh.HostKeyCallback, error) {
	knPath := knownHostsPath()
	if _, err := os.Stat(knPath); err != nil {
		// No known_hosts at all yet → defer to the HIL path (dialSSHWithHIL).
		return ssh.InsecureIgnoreHostKey(), nil
	}
	cb, err := knownhosts.New(knPath)
	if err != nil {
		return nil, fmt.Errorf("knownhosts: %w", err)
	}
	return cb, nil
}

func knownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".ssh/known_hosts"
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

// ResolveHost turns a host alias (or literal host) into a complete HostInfo,
// filling gaps from memory and, when still missing, asking the human via the
// HIL prompter. This is the bridge between "remember" and "ask when missing".
// Exported so the standalone `scp` subcommand shares the same resolution path.
func ResolveHost(ctx context.Context, alias, host, user, password, keyPath string, port int, mem *Memory, ask askFunc) (HostInfo, error) {
	h := HostInfo{
		Name: alias, Host: host, Port: port, User: user,
		Password: password, KeyPath: keyPath,
	}

	// 1. Exact memory hit for the alias → fully known host, no prompting.
	if mem != nil && alias != "" {
		if raw, ok := mem.Get(sshHostKeyPrefix + alias); ok {
			var remembered HostInfo
			if json.Unmarshal([]byte(raw), &remembered) == nil {
				mergeHost(&h, remembered) // explicit args win; gaps filled from memory
			}
		}
	}

	// 2. Still missing essentials → HIL prompt, then remember the answer.
	if h.Host == "" {
		if ask == nil {
			return h, fmt.Errorf("host is required (no HIL prompter available)")
		}
		h.Host = ask(ctx, fmt.Sprintf("SSH 主机地址/IP（%s）: ", orAlias(alias)))
	}
	if h.User == "" {
		if ask == nil {
			return h, fmt.Errorf("user is required (no HIL prompter available)")
		}
		h.User = ask(ctx, "SSH 登录用户: ")
	}
	if h.Password == "" && h.KeyPath == "" {
		if ask == nil {
			return h, fmt.Errorf("auth is required: set password or key-path (no HIL prompter available)")
		}
		if ans := ask(ctx, "认证方式 [1]密码 [2]私钥文件 (默认1): "); strings.TrimSpace(ans) == "2" {
			h.KeyPath = ask(ctx, "私钥文件路径: ")
		} else {
			h.Password = askPassword(ctx, ask)
		}
	}

	// 3. Persist for next time so this host never needs prompting again.
	if mem != nil && alias != "" {
		if raw, err := json.Marshal(h); err == nil {
			mem.Set(sshHostKeyPrefix+alias, string(raw))
		}
	}
	return h, nil
}

func mergeHost(dst *HostInfo, src HostInfo) {
	if dst.Host == "" {
		dst.Host = src.Host
	}
	if dst.Port == 0 {
		dst.Port = src.Port
	}
	if dst.User == "" {
		dst.User = src.User
	}
	if dst.Password == "" {
		dst.Password = src.Password
	}
	if dst.KeyPath == "" {
		dst.KeyPath = src.KeyPath
	}
}

func orAlias(alias string) string {
	if alias != "" {
		return alias
	}
	return "新主机"
}
