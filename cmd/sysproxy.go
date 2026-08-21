package cmd

import (
	"fmt"
	"strings"

	"agent-netx/sysproxy"

	"github.com/spf13/cobra"
)

// sysproxyCmd is the one-click system-proxy toggle (P0). It shares the
// platform sysproxy package with the agent's tools, so `sysproxy on` from the
// shell and the agent turning the system proxy on do the exact same thing.
//
// Usage:
//   sysproxy on [http://127.0.0.1:7890] [--no-proxy host[,host]]
//   sysproxy off
//   sysproxy status
//
// With no address, `on` defaults to the config's HTTP listen port.
func sysproxyCmd() *cobra.Command {
	var noProxy string
	cmd := &cobra.Command{
		Use:   "sysproxy <on|off|status> [proxyAddr]",
		Short: "一键开关系统代理（Windows 注册表 / Linux gsettings）",
		Long: `一键系统代理：让本机所有应用走 agent-netx 的代理。

  agent-netx sysproxy on                          # 用 config.yml 里的监听端口
  agent-netx sysproxy on http://127.0.0.1:7890     # 指定地址
  agent-netx sysproxy on http://127.0.0.1:7890 --no-proxy localhost,127.0.0.1
  agent-netx sysproxy off                         # 关闭
  agent-netx sysproxy status                      # 查看当前状态

Windows: 写 HKCU\...\Internet Settings (ProxyEnable/ProxyServer) + netsh winhttp。
Linux:   写 gsettings org.gnome.system.proxy + 生成 ~/.proxy.env 供 source。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("usage: sysproxy <on|off|status> [proxyAddr]")
			}
			action := strings.ToLower(args[0])
			switch sysproxy.Action(action) {
			case sysproxy.ActOn:
				addr := ""
				if len(args) >= 2 {
					addr = args[1]
				}
				if addr == "" {
					// Default to the config's HTTP listener.
					cfg, err := loadCfg(cmd)
					if err == nil && cfg.Listen.HTTP != 0 {
						addr = fmt.Sprintf("http://127.0.0.1:%d", cfg.Listen.HTTP)
					}
				}
				if addr == "" {
					return fmt.Errorf("no proxy address given and none in config (pass e.g. http://127.0.0.1:7890)")
				}
				out, err := sysproxy.Enable(sysproxy.Settings{HTTP: addr, HTTPS: addr, NoProxy: noProxy})
				if err != nil {
					return err
				}
				fmt.Println(out)
			case sysproxy.ActOff:
				out, err := sysproxy.Disable()
				if err != nil {
					return err
				}
				fmt.Println(out)
			case sysproxy.ActStatus:
				out, err := sysproxy.Status()
				if err != nil {
					return err
				}
				fmt.Println(out)
			default:
				return fmt.Errorf("unknown action %q (want on|off|status)", action)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&noProxy, "no-proxy", "", "代理排除的主机列表（逗号分隔，如 localhost,127.0.0.1）")
	return cmd
}
