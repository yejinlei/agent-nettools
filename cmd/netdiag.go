package cmd

import (
	"fmt"

	"agent-netx/netdiag"

	"github.com/spf13/cobra"
)

// netdiagCmd is a standalone subcommand that dumps socket connections,
// listeners, or captures live packets — the same surface exposed to the LLM
// agent through the net_connections / net_listeners / net_packet / net_stats tools.
func netdiagCmd() *cobra.Command {
	var proto string
	var port, count, timeout int

	cmd := &cobra.Command{
		Use:   "netdiag <conns|listeners|packets|stats>",
		Short: "查看进程网络端口和数据包 (媲美 netstat / ss)",
		Long: `查看进程网络端口和数据包,与 netstat / ss 能力对等,额外支持抓包。

  # 查看所有连接 (类似 netstat -an / ss -tuan)
  agent-netx netdiag conns
  agent-netx netdiag conns --proto tcp
  agent-netx netdiag conns --proto udp

  # 查看监听端口 (类似 ss -tlnp / netstat -anp | LISTEN)
  agent-netx netdiag listeners

  # 抓包 (类似 tcpdump / wireshark, 需要管理员权限)
  agent-netx netdiag packets --count 30 --timeout 15
  agent-netx netdiag packets --proto tcp --port 80
  agent-netx netdiag packets --dst 10.0.0.5 --proto udp

  # 聚合统计 (类似 ss -s)
  agent-netx netdiag stats

抓包需要以管理员/root 身份运行(需要打开原始套接字 ip4:tcp / ip4:udp)。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return cmd.Usage()
			}
			switch args[0] {
			case "conns", "connections":
				return showConns(proto)
			case "listeners", "listen":
				return showListeners()
			case "packets", "packet":
				if port > 0 && proto == "" {
					return fmt.Errorf("--port 需要配合 --proto tcp/udp")
				}
				return showPackets(proto, port, count, timeout)
			case "stats", "stat":
				return showStats()
			default:
				return fmt.Errorf("未知动作 %q (conns/listeners/packets/stats)", args[0])
			}
		},
	}
	cmd.Flags().StringVar(&proto, "proto", "all", "tcp/udp/all")
	cmd.Flags().IntVar(&port, "port", 0, "按端口过滤 (src 或 dst)")
	cmd.Flags().IntVar(&count, "count", 50, "抓包最大数量")
	cmd.Flags().IntVar(&timeout, "timeout", 10, "抓包超时秒数")
	return cmd
}

func showConns(proto string) error {
	conns, err := netdiag.GetConnections(proto)
	if err != nil {
		return err
	}
	fmt.Println(netdiag.FormatConnections(conns))
	return nil
}

func showListeners() error {
	conns, err := netdiag.GetListeners()
	if err != nil {
		return err
	}
	fmt.Println(netdiag.FormatConnections(conns))
	return nil
}

func showPackets(proto string, port, count, timeout int) error {
	pkts, err := netdiag.CapturePackets(netdiag.CaptureOpts{
		Proto:   proto,
		Port:    port,
		Count:   count,
		Timeout: timeout,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Captured %d packets in %ds:\n", len(pkts), timeout)
	fmt.Println(netdiag.FormatPackets(pkts))
	return nil
}

func showStats() error {
	stats, err := netdiag.GetStats()
	if err != nil {
		return err
	}
	fmt.Println(netdiag.FormatStats(stats))
	return nil
}