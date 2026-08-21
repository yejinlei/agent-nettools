package cmd

import (
	"strconv"

	"agent-netx/proxy"
	"github.com/spf13/cobra"
)

func frpCmd() *cobra.Command {
	secret := ""
	targetHost := "127.0.0.1"
	cmd := &cobra.Command{
		Use:   "frp",
		Short: "fast reverse proxy (frp)",
	}
	serverCmd := &cobra.Command{
		Use: "server [listen-port]",
		RunE: func(cmd *cobra.Command, args []string) error {
			port := 7000
			if len(args) > 0 {
				port, _ = strconv.Atoi(args[0])
			}
			return proxy.RunServer(cmd.Context(), secret, port, targetHost)
		},
	}
	serverCmd.Flags().StringVar(&secret, "secret", "", "shared secret")
	serverCmd.Flags().StringVar(&targetHost, "target", "127.0.0.1", "target host")
	serverAddr := ""
	localPort := 0
	remotePort := 80
	clientCmd := &cobra.Command{
		Use: "client <local-port> <remote-port>",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) >= 1 { localPort, _ = strconv.Atoi(args[0]) }
			if len(args) >= 2 { remotePort, _ = strconv.Atoi(args[1]) }
			return proxy.RunClient(cmd.Context(), localPort, remotePort, serverAddr, secret)
		},
	}
	clientCmd.Flags().StringVar(&serverAddr, "server", "", "frps host:port")
	clientCmd.Flags().StringVar(&secret, "secret", "", "shared secret")
	cmd.AddCommand(serverCmd, clientCmd)
	return cmd
}
