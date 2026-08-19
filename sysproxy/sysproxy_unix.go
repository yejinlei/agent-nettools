//go:build !windows

package sysproxy

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Linux/desktop system-proxy: GNOME reads its proxy from gsettings
// (org.gnome.system.proxy mode/host/port), and many CLI tools honor the
// HTTP_PROXY/HTTPS_PROXY/NO_PROXY env vars. We set both so the change lands
// in the DE and in freshly-spawned shells.

// Enable turns the system proxy on.
func Enable(s Settings) (string, error) {
	if s.HTTP == "" {
		return "", fmt.Errorf("sysproxy: HTTP proxy address is required (e.g. http://127.0.0.1:7890)")
	}
	host, port := splitHostPort(stripScheme(s.HTTP))

	// GNOME gsettings — best-effort; ignore error if gsettings absent.
	gs := func(args ...string) { _ = exec.Command("gsettings", args...).Run() }
	gs("set", "org.gnome.system.proxy", "mode", "manual")
	gs("set", "org.gnome.system.proxy.http", "host", host)
	gs("set", "org.gnome.system.proxy.http", "port", port)
	gs("set", "org.gnome.system.proxy.https", "host", host)
	gs("set", "org.gnome.system.proxy.https", "port", port)
	if s.NoProxy != "" {
		gs("set", "org.gnome.system.proxy", "ignore-hosts", fmt.Sprintf("['%s']", strings.Join(strings.Split(s.NoProxy, ","), "','")))
	}

	// Emit a shell-sourceable env snippet to ~/.proxy.env so new shells can
	// `source` it. (We can't set the *current* shell's env persistently from a
	// child process; a sourced file is the Unix-idiomatic way.)
	envPath := os.Getenv("HOME") + "/.proxy.env"
	contents := fmt.Sprintf("export HTTP_PROXY=%s\nexport HTTPS_PROXY=%s\nexport NO_PROXY=%s\n",
		s.HTTP, orDefault(s.HTTPS, s.HTTP), s.NoProxy)
	if err := os.WriteFile(envPath, []byte(contents), 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", envPath, err)
	}

	return fmt.Sprintf("✅ 系统代理已开启 (gsettings + %s)\n  💡 在新终端运行: source %s", s.HTTP, envPath), nil
}

// Disable turns the system proxy off.
func Disable() (string, error) {
	_ = exec.Command("gsettings", "set", "org.gnome.system.proxy", "mode", "none").Run()
	envPath := os.Getenv("HOME") + "/.proxy.env"
	_ = os.WriteFile(envPath, []byte("unset HTTP_PROXY\nunset HTTPS_PROXY\nunset NO_PROXY\n"), 0644)
	return "✅ 系统代理已关闭 (gsettings mode=none)", nil
}

// Status reports the current system-proxy state.
func Status() (string, error) {
	mode, _ := exec.Command("gsettings", "get", "org.gnome.system.proxy", "mode").Output()
	m := strings.TrimSpace(string(mode))
	if m == "'none'" || m == "" {
		envSet := os.Getenv("HTTP_PROXY")
		if envSet != "" {
			return fmt.Sprintf("系统代理: gsettings=none, 但 HTTP_PROXY=%s (当前环境变量)", envSet), nil
		}
		return "系统代理: 关闭", nil
	}
	host, _ := exec.Command("gsettings", "get", "org.gnome.system.proxy.http", "host").Output()
	port, _ := exec.Command("gsettings", "get", "org.gnome.system.proxy.http", "port").Output()
	return fmt.Sprintf("系统代理: 开启 (mode=%s) → %s:%s",
		m, strings.Trim(string(host), "'"), strings.Trim(string(port), "'")), nil
}

func stripScheme(addr string) string {
	addr = strings.TrimSpace(addr)
	for _, p := range []string{"http://", "https://"} {
		addr = strings.TrimPrefix(addr, p)
	}
	return strings.TrimRight(addr, "/")
}

func splitHostPort(hp string) (string, string) {
	if i := strings.LastIndex(hp, ":"); i >= 0 {
		return hp[:i], hp[i+1:]
	}
	return hp, "7890"
}

func orDefault(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
