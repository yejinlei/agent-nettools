package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"agent-nettools/config"
	"agent-nettools/web"

	"github.com/spf13/cobra"
)

// These standalone subcommands each run ONE subsystem in the foreground,
// independent of the rest (the "non-TUI, tools run independently" mode).
// The agent's `service` tool spawns and stops these same commands by name.
//
// Each loads config.yml (so ports/addresses come from the same source), then
// runs exactly one component and blocks until Ctrl-C or a fatal error.

// standaloneRun loads the config and runs a single subsystem, blocking until
// interrupted. It returns the subsystem's error if it exits on its own.
func standaloneRun(cmd *cobra.Command, run func(ctx context.Context, cfg *config.Config, logRing *web.LogRing) error) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	if cfgPath == "" {
		cfgPath = "config.yml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown on Ctrl-C / SIGINT so each subsystem can clean up.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\n收到中断信号，正在停止…")
		cancel()
	}()

	logRing := web.NewLogRing(1000)
	return run(ctx, cfg, logRing)
}

func proxyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "proxy",
		Short: "仅启动 HTTP/SOCKS5 代理监听（独立运行）",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Standalone proxy gets its own stats tracker (no web dashboard to
			// read it, but traffic is still counted; fullStart shares one with
			// web, this path just keeps accounting local).
			return standaloneRun(cmd, func(ctx context.Context, cfg *config.Config, logRing *web.LogRing) error {
				return runProxy(ctx, cfg, logRing, web.NewStatsTracker())
			})
		},
	}
}

func dnsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dns",
		Short: "仅启动本地 DNS 服务器（独立运行）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return standaloneRun(cmd, runDNS)
		},
	}
}

func webCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "web",
		Short: "仅启动 Web 仪表盘（独立运行）",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			if cfgPath == "" {
				cfgPath = "config.yml"
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt)
			go func() { <-sigCh; cancel() }()
			return runWeb(ctx, cfg)
		},
	}
}

func tunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tun",
		Short: "仅启动 TUN 设备（独立运行）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return standaloneRun(cmd, runTUN)
		},
	}
}

func n2nCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "n2n",
		Short: "仅启动 n2n 虚拟局域网节点（独立运行）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return standaloneRun(cmd, runN2N)
		},
	}
}

func stunvpvCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stunvpv",
		Short: "仅启动 STUN/TURN VPN 节点（独立运行）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return standaloneRun(cmd, runSTUNVPV)
		},
	}
}
