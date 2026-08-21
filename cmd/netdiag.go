package cmd

import (
	"fmt"

	"agent-netx/netdiag"

	"github.com/spf13/cobra"
)

// netdiagCmd is a standalone subcommand for netstat / ss / lsof-class diagnostics.
func netdiagCmd() *cobra.Command {
	var (
		proto    string
		port     int
		pid      int
		state    string
		src, dst string
		count    int
		timeout  int
	)

	cmd := &cobra.Command{
		Use:   "netdiag <conns|listeners|packets|stats|interfaces|routes|proto|fd>",
		Short: "查看进程网络端口和数据包 (netstat / ss / lsof 等价, 额外支持抓包)",
		Long: `网络诊断工具, 与 netstat / ss / lsof 能力对等, 额外支持抓包。

  # 连接表 (类似 netstat -an / ss -tuan)
  agent-netx netdiag conns --pid 1234 --port 8080 --state established --proto tcp
  agent-netx netdiag conns --src 10.0.0.5 --dst 1.2.3.4

  # 监听端口 (类似 ss -tlnp / netstat -anp | LISTEN)
  agent-netx netdiag listeners

  # 抓包 (类似 tcpdump, 需要管理员权限)
  agent-netx netdiag packets --count 30 --timeout 15
  agent-netx netdiag packets --proto tcp --port 80

  # 聚合统计 (类似 ss -s)
  agent-netx netdiag stats

  # 网络接口 (类似 ip -s link / netstat -i)
  agent-netx netdiag interfaces

  # 路由表 (类似 netstat -r / ip route)
  agent-netx netdiag routes

  # 协议统计 (类似 netstat -s)
  agent-netx netdiag proto

  # 进程 FD (类似 lsof -p, Linux 需管理员)
  agent-netx netdiag fd --pid 1234

抓包需要以管理员/root 身份运行。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return cmd.Usage()
			}
			switch args[0] {
			case "conns", "connections":
				return showConns(proto, port, pid, state, src, dst)
			case "listeners", "listen":
				return showListeners(proto, port, pid, state, src, dst)
			case "packets", "packet":
				if port > 0 && proto == "" {
					return fmt.Errorf("--port 需要配合 --proto tcp/udp")
				}
				return showPackets(proto, port, count, timeout)
			case "stats", "stat":
				return showStats()
			case "interfaces", "ifaces":
				return showInterfaces()
			case "routes", "route":
				return showRoutes()
			case "proto", "protocols":
				return showProto()
			case "fd", "fds":
				if pid <= 0 {
					return fmt.Errorf("fd 需要 --pid <pid>")
				}
				return showFDs(pid)
			default:
				return fmt.Errorf("未知动作 %q (conns/listeners/packets/stats/interfaces/routes/proto/fd)", args[0])
			}
		},
	}
	cmd.Flags().StringVar(&proto, "proto", "all", "协议过滤: tcp/udp/raw/unix/all")
	cmd.Flags().IntVar(&port, "port", 0, "按端口过滤 (src 或 dst)")
	cmd.Flags().IntVar(&pid, "pid", 0, "按 PID 过滤")
	cmd.Flags().StringVar(&state, "state", "", "按连接状态过滤: established/listen/time-wait/...")
	cmd.Flags().StringVar(&src, "src", "", "按源地址过滤 (ip 或 ip:port)")
	cmd.Flags().StringVar(&dst, "dst", "", "按目的地址过滤 (ip 或 ip:port)")
	cmd.Flags().IntVar(&count, "count", 50, "抓包最大数量")
	cmd.Flags().IntVar(&timeout, "timeout", 10, "抓包超时秒数")
	return cmd
}

func connFilter(proto string, port, pid int, state, src, dst string) *netdiag.Filter {
	f := &netdiag.Filter{
		Proto: proto,
		Port:  port,
		PID:   int32(pid),
		State: state,
		Src:   src,
		Dst:   dst,
	}
	if f.Proto == "" || f.Proto == "all" {
		f.Proto = "all"
	}
	return f
}

func showConns(proto string, port, pid int, state, src, dst string) error {
	conns, err := netdiag.GetConnections(proto, connFilter(proto, port, pid, state, src, dst))
	if err != nil {
		return err
	}
	fmt.Println(netdiag.FormatConnections(conns))
	return nil
}

func showListeners(proto string, port, pid int, state, src, dst string) error {
	conns, err := netdiag.GetListeners(connFilter(proto, port, pid, state, src, dst))
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

func showInterfaces() error {
	ifaces, err := netdiag.GetInterfaces()
	if err != nil {
		return err
	}
	fmt.Println(netdiag.FormatInterfaces(ifaces))
	fmt.Println()
	fmt.Println(netdiag.FormatIOAddresses(ifaces))
	return nil
}

func showRoutes() error {
	routes, err := netdiag.GetRoutes()
	if err != nil {
		return err
	}
	fmt.Println(netdiag.FormatRoutes(routes))
	return nil
}

func showProto() error {
	stats, err := netdiag.GetProtoStats(nil)
	if err != nil {
		return err
	}
	fmt.Println(netdiag.FormatProtoStats(stats))
	return nil
}

func showFDs(pid int) error {
	fds, err := netdiag.GetProcessFDs(int32(pid))
	if err != nil {
		return err
	}
	fmt.Println(netdiag.FormatProcessFDs(fds))
	return nil
}
