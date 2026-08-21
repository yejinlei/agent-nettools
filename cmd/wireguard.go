package cmd

import (
	"context"

	"agent-netx/config"
	"agent-netx/web"
	"agent-netx/wireguard"

	"github.com/spf13/cobra"
)

// wireguardCmd is the standalone WireGuard subcommand (tunnel.Peer implementation
// on top of UDP + DH + ChaCha20-Poly1305). When TUN is enabled in config.yml
// (wireguard.enable + tun.enable) and not --no-tun, the peer is auto-bridged
// to a kernel TUN via bridgeTUN — same plumbing as n2n/stunvpv.
func wireguardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wireguard",
		Short: "仅启动 WireGuard P2P 节点（独立运行）",
		Long: `WireGuard P2P 节点，实现 tunnel.Peer 接缝。
用法: agent-netx wireguard
      agent-netx wireguard --no-tun   # 仅测试 UDP 通道，不接 TUN`,
		RunE: func(cmd *cobra.Command, args []string) error {
			noTun, _ := cmd.Flags().GetBool("no-tun")
			return standaloneRun(cmd, func(ctx context.Context, cfg *config.Config, logRing *web.LogRing) error {
				return runWireGuard(ctx, cfg, logRing, !noTun)
			})
		},
	}
	cmd.Flags().Bool("no-tun", false, "不将 TUN 网桥接入 WireGuard peer")
	return cmd
}

// runWireGuard builds a wireguard.Peer from the wireguard: section and bridges
// it to TUN when enabled. It calls bridgeTUN (cmd/runtime.go) for the same
// auto-bridge path used by n2n/stunvpv.
func runWireGuard(ctx context.Context, cfg *config.Config, logRing *web.LogRing, useTun bool) error {
	wgCfg := wireguard.Config{
		Private:   cfg.WireGuard.Private,
		Public:    cfg.WireGuard.Public,
		Preshared: cfg.WireGuard.Preshared,
		Listen:    cfg.WireGuard.Listen,
		PeerAddr:  cfg.WireGuard.PeerAddr,
		VirtualIP: cfg.WireGuard.VirtualIP,
		Keepalive: cfg.WireGuard.Keepalive,
		Handshake: cfg.WireGuard.Handshake,
	}
	peer, err := wireguard.NewPeer(wgCfg)
	if err != nil {
		return err
	}
	bridgeTUN(ctx, cfg, useTun, peer, logRing)
	if logRing != nil {
		logRing.Write(web.INFO, "wireguard: peer starting (listen=%s peer=%s)", wgCfg.Listen, wgCfg.PeerAddr)
	}
	return peer.Start(ctx)
}