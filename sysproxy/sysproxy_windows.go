//go:build windows

package sysproxy

import (
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Windows system-proxy lives under:
//   HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings
// with ProxyEnable (DWORD 0/1) and ProxyServer ("host:port" or
// "http=host:port;https=host:port"). We also set netsh winhttp so system
// services (which ignore the per-user HKCU key) honor the proxy too.

const inetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// Enable turns the system proxy on with the given settings.
func Enable(s Settings) (string, error) {
	if s.HTTP == "" {
		return "", fmt.Errorf("sysproxy: HTTP proxy address is required (e.g. http://127.0.0.1:7890)")
	}
	// Normalize to host:port for ProxyServer. Accept "http://127.0.0.1:7890"
	// or "127.0.0.1:7890".
	httpHP := stripScheme(s.HTTP)
	httpsHP := httpHP
	if s.HTTPS != "" {
		httpsHP = stripScheme(s.HTTPS)
	}

	k, _, err := registry.CreateKey(registry.CURRENT_USER, inetSettings, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("open HKCU Internet Settings: %w", err)
	}
	defer k.Close()

	// ProxyServer can be a single "host:port" (applies to all) or a
	// per-protocol "http=...;https=...". We use the per-protocol form so HTTP
	// and HTTPS can differ if the user wants.
	server := fmt.Sprintf("http=%s;https=%s", httpHP, httpsHP)
	if err := k.SetStringValue("ProxyServer", server); err != nil {
		return "", fmt.Errorf("set ProxyServer: %w", err)
	}
	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return "", fmt.Errorf("set ProxyEnable: %w", err)
	}
	if s.NoProxy != "" {
		if err := k.SetStringValue("ProxyOverride", strings.ReplaceAll(s.NoProxy, ",", ";")); err != nil {
			return "", fmt.Errorf("set ProxyOverride: %w", err)
		}
	}

	// netsh winhttp for system services. Ignore its error — it needs admin and
	// is a best-effort mirror of the per-user setting.
	_ = exec.Command("netsh", "winhttp", "set", "proxy", httpHP, bypassArg(s.NoProxy)).Run()

	return fmt.Sprintf("✅ 系统代理已开启 (HKCU + netsh): %s", server), nil
}

// Disable turns the system proxy off.
func Disable() (string, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, inetSettings, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("open HKCU Internet Settings: %w", err)
	}
	defer k.Close()
	if err := k.SetDWordValue("ProxyEnable", 0); err != nil {
		return "", fmt.Errorf("set ProxyEnable: %w", err)
	}
	_ = exec.Command("netsh", "winhttp", "reset", "proxy").Run()
	return "✅ 系统代理已关闭", nil
}

// Status reports the current system-proxy state.
func Status() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetSettings, registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("open HKCU Internet Settings: %w", err)
	}
	defer k.Close()
	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil {
		return "系统代理: 未知 (无法读取 ProxyEnable)", nil
	}
	server, _, _ := k.GetStringValue("ProxyServer")
	if enable == 0 {
		return "系统代理: 关闭 (ProxyEnable=0)", nil
	}
	return fmt.Sprintf("系统代理: 开启 → %s", server), nil
}

func stripScheme(addr string) string {
	addr = strings.TrimSpace(addr)
	for _, p := range []string{"http://", "https://"} {
		addr = strings.TrimPrefix(addr, p)
	}
	return strings.TrimRight(addr, "/")
}

func bypassArg(noProxy string) string {
	if noProxy == "" {
		return `""`
	}
	return `"` + strings.ReplaceAll(noProxy, ",", ";") + `"`
}
