package cmd

import (
	"fmt"
	"os"
	"strings"

	"agent-netx/agent"

	"github.com/spf13/cobra"
)

func stopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <service|all>",
		Short: "停止某个子服务或全部子服务",
		Long: `停止由 agent-netx 启动的子服务。

  agent-netx stop proxy
  agent-netx stop all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) > 0 {
				target = strings.ToLower(args[0])
			}
			if target == "" {
				return fmt.Errorf("需要指定服务名: proxy/dns/web/tun/n2n/stunvpv/wireguard/frp/tinc/socat/corsproxy/forward/all")
			}
			if target == "all" {
				return stopAll()
			}
			return stopOne(target)
		},
	}
	return cmd
}

func restartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart <service|all>",
		Short: "重启某个子服务或全部子服务",
		Long: `重启子服务 = stop + start。

  agent-netx restart proxy
  agent-netx restart all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) > 0 {
				target = strings.ToLower(args[0])
			}
			if target == "" {
				return fmt.Errorf("需要指定服务名: proxy/dns/web/tun/n2n/stunvpv/wireguard/frp/tinc/socat/corsproxy/forward/all")
			}
			if target == "all" {
				return restartAll(cmd)
			}
			return restartOne(cmd, target)
		},
	}
	return cmd
}

var serviceOrder = []string{"proxy", "dns", "web", "tun", "n2n", "stunvpv", "wireguard", "frp", "tinc", "socat", "corsproxy", "forward"}

func stopOne(name string) error {
	pid, err := agent.Stop(name)
	if err != nil {
		fmt.Printf("  %-14s %s\n", name, err.Error())
		return nil
	}
	fmt.Printf("  %-14s 已停止 (pid=%d)\n", name, pid)
	return nil
}

func stopAll() error {
	fmt.Println("停止全部子服务:")
	for _, n := range serviceOrder {
		_ = stopOne(n)
	}
	return nil
}

func restartOne(cmd *cobra.Command, name string) error {
	_ = stopOne(name)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg := agent.ServiceStartConfig{
		Name:       name,
		ConfigPath: cfgPath,
		Executable: exe,
	}
	fmt.Println("  " + agent.ServiceStart(cfg))
	return nil
}

func restartAll(cmd *cobra.Command) error {
	fmt.Println("重启全部子服务:")
	for _, n := range serviceOrder {
		_ = restartOne(cmd, n)
	}
	return nil
}
