package cmd

import (
	"context"
	"agent-netx/tinc"
	"github.com/spf13/cobra"
)

func tincCmd() *cobra.Command {
	var cfg tinc.Config
	cmd := &cobra.Command{
		Use: "tinc",
		Short: "Tinc P2P VPN tunnel",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTinc(cmd.Context(), cfg)
		},
	}
	cmd.Flags().StringVar(&cfg.Private, "private", "", "ed25519 private key (hex)")
	cmd.Flags().StringVar(&cfg.Name, "name", "", "node name")
	cmd.Flags().StringVar(&cfg.CA, "ca", "", "shared CA secret")
	cmd.Flags().StringVar(&cfg.Listen, "listen", ":655", "UDP listen")
	cmd.Flags().StringSliceVar(&cfg.Endpoints, "endpoint", nil, "peer UDP")
	cmd.Flags().StringVar(&cfg.VirtualIP, "vip", "10.0.0.2", "virtual IP")
	cmd.Flags().IntVar(&cfg.Keepalive, "keepalive", 25, "keepalive (s)")
	return cmd
}

func runTinc(ctx context.Context, cfg tinc.Config) error {
	peer, err := tinc.NewPeer(cfg)
	if err != nil { return err }
	return peer.Start(ctx)
}
