package cmd

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"

	"github.com/spf13/cobra"
)

func forwardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forward <listenAddr> <dstAddr>",
		Short: "Forward HTTPS with on-the-fly TLS termination to plain HTTP backend",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 { return fmt.Errorf("usage: net-redirect forward <listenAddr> <dstAddr>") }
			ln, err := net.Listen("tcp", args[0])
			if err != nil { return err }
			defer ln.Close()
			fmt.Printf("forwarding %s (HTTPS) -> %s (HTTP)\n", args[0], args[1])
			for {
				conn, err := ln.Accept()
				if err != nil { return err }
				go handleForwardTLS(conn, args[1])
			}
		},
	}
}

func handleForwardTLS(client net.Conn, dstAddr string) {
	defer client.Close()
	tlsConn := tls.Server(client, &tls.Config{InsecureSkipVerify: true})
	if err := tlsConn.Handshake(); err != nil { return }
	dst, err := net.Dial("tcp", dstAddr)
	if err != nil { return }
	defer dst.Close()
	go io.Copy(dst, tlsConn)
	io.Copy(tlsConn, dst)
}
